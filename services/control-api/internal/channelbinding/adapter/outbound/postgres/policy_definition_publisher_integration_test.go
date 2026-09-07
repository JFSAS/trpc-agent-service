package postgresadapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/adapter/outbound/credentialcrypto"
	ownerpg "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/adapter/outbound/postgres"
	owner "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/application"
	def "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
	tenantpg "github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant/adapter/outbound/postgres"
	"github.com/liuzengh/trpc-agent-service/services/control-api/migrations"
)

func definitionPublisher(t *testing.T, pool *pgxpool.Pool) *owner.Publisher {
	t.Helper()
	store, err := ownerpg.NewPublicationStore(pool, tenantpg.TransactionAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := credentialcrypto.New("k1", map[string]credentialcrypto.Key{"k1": {Encryption: bytes.Repeat([]byte{1}, 32), MAC: bytes.Repeat([]byte{2}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	var seq atomic.Int64
	publisher, err := owner.NewPublisher(owner.PublisherDependencies{Store: store, Access: allowOwner{}, Signer: signer, NewID: func(prefix string) (string, error) { return fmt.Sprintf("%s_definition_%d", prefix, seq.Add(1)), nil }, Now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }})
	if err != nil {
		t.Fatal(err)
	}
	return publisher
}
func TestPolicyDefinitionPublisherOwnerCASReplayAndRollbackAgainstPostgreSQL(t *testing.T) {
	_, _, pool, _ := policyPG(t)
	ctx := context.Background()
	raw, err := migrations.Files.ReadFile("0007_channel_policy_definitions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	publisher := definitionPublisher(t, pool)
	actor := owner.Actor{TenantID: testActor.TenantID, UserID: testActor.UserID}
	input := owner.PublishInput{Definition: def.DefaultSessionDefinition()}
	results := make([]owner.PublishResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = publisher.Publish(ctx, actor, def.Session, "session-owner", "first", input)
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || !reflect.DeepEqual(results[0], results[1]) {
		t.Fatal("concurrent first publication", errs)
	}
	if _, err = publisher.Publish(ctx, actor, def.Session, "session-owner", "stale", input); !errors.Is(err, owner.ErrRevisionConflict) {
		t.Fatal("CAS", err)
	}
	input.ExpectedRevision = 1
	if _, err = publisher.Publish(ctx, actor, def.Session, "session-owner", "first", input); !errors.Is(err, owner.ErrIdempotencyConflict) {
		t.Fatal("key conflict", err)
	}
	input.Definition.Session.Partition = def.SharedConversation
	second, err := publisher.Publish(ctx, actor, def.Session, "session-owner", "second", input)
	if err != nil || second.Revision != 2 || second.Digest == results[0].Digest {
		t.Fatal(second, err)
	}
	reader, _ := ownerpg.NewReader(pool)
	original, err := reader.ReadExact(ctx, actor.TenantID, def.Session, "session-owner", 1)
	if err != nil || original.Definition.Session.Partition != def.PerUserInConversation {
		t.Fatal("old revision changed", err)
	}
	count := func(want int) {
		t.Helper()
		var revisions, events, receipts int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM channel_policy_definition_revisions),(SELECT count(*) FROM control_outbox WHERE aggregate_type='ChannelPolicyDefinition'),(SELECT count(*) FROM channel_policy_definition_receipts)`).Scan(&revisions, &events, &receipts); err != nil || revisions != want || events != 2*want || receipts != want {
			t.Fatal("atomic count", revisions, events, receipts, err)
		}
	}
	count(2)
	if _, err = pool.Exec(ctx, `CREATE FUNCTION fail_definition_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'PRIVATE_RECEIPT_ERROR'; END $$; CREATE TRIGGER fail_definition_receipt BEFORE INSERT ON channel_policy_definition_receipts FOR EACH ROW EXECUTE FUNCTION fail_definition_receipt()`); err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = 2
	failed, err := publisher.Publish(ctx, actor, def.Session, "session-owner", "third", input)
	if err == nil || failed != (owner.PublishResult{}) {
		t.Fatal("uncommitted success", failed, err)
	}
	count(2)
	if _, err = pool.Exec(ctx, `DROP TRIGGER fail_definition_receipt ON channel_policy_definition_receipts; DROP FUNCTION fail_definition_receipt()`); err != nil {
		t.Fatal(err)
	}
	if _, err = publisher.Publish(ctx, actor, def.Session, "session-owner", "third", input); err != nil {
		t.Fatal("retry", err)
	}
	count(3)
	// The low-level store also rejects an append whose command forgot its receipt.
	rawStore, err := ownerpg.NewPublicationStore(pool, tenantpg.TransactionAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := def.NewRevision(actor.TenantID, "session-owner", actor.UserID, def.Session, 4, input.Definition, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	err = rawStore.WithPublish(ctx, owner.WriteScope{Actor: actor, Kind: def.Session, PolicyID: "session-owner"}, func(tx owner.PublishTransaction) error {
		return tx.Append(ctx, candidate, "evt_no_receipt", "aud_no_receipt")
	})
	if !errors.Is(err, owner.ErrIntegrity) {
		t.Fatal("receiptless append committed", err)
	}
	count(3)
	cross := actor
	cross.TenantID = "tnt_b"
	if _, err = publisher.Publish(ctx, cross, def.Session, "session-owner", "cross", owner.PublishInput{Definition: def.DefaultSessionDefinition()}); !errors.Is(err, owner.ErrPermissionDenied) {
		t.Fatal("cross tenant owner", err)
	}
	count(3)
	// Different idempotency keys must not bypass the same expected-revision fence.
	input.ExpectedRevision = 3
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = publisher.Publish(ctx, actor, def.Session, "session-owner", fmt.Sprintf("race-%d", i), input)
		}(i)
	}
	wg.Wait()
	var committed, conflicted int
	for i, err := range errs {
		switch {
		case err == nil && results[i].Revision == 4:
			committed++
		case errors.Is(err, owner.ErrRevisionConflict) && results[i] == (owner.PublishResult{}):
			conflicted++
		default:
			t.Fatal("unexpected concurrent publication result", results[i], err)
		}
	}
	if committed != 1 || conflicted != 1 {
		t.Fatal("CAS did not select exactly one winner", committed, conflicted)
	}
	count(4)
	if _, err = pool.Exec(ctx, `UPDATE tenant_memberships SET role='MEMBER' WHERE tenant_id=$1 AND user_id=$2`, actor.TenantID, actor.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = publisher.Publish(ctx, actor, def.Session, "session-owner", "first", owner.PublishInput{Definition: def.DefaultSessionDefinition()}); !errors.Is(err, owner.ErrPermissionDenied) {
		t.Fatal("revoked OWNER replay", err)
	}
	count(4)
}
