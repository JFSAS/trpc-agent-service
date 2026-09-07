package postgresadapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/adapter/outbound/credentialcrypto"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

// This is a publication orchestration fixture, not a production owner resolver.
type policyReferenceFixture struct {
	calls   int
	err     error
	actor   application.Actor
	account domain.Account
	mutate  bool
}

func (v *policyReferenceFixture) ValidatePublishedPolicy(_ context.Context, a application.Actor, account domain.Account, body domain.AccessPolicyBody) error {
	v.calls++
	v.actor = a
	v.account = account
	if v.mutate && len(body.AllowedOperations) > 0 {
		body.AllowedOperations[0] = "PRIVATE_INJECTED"
	}
	return v.err
}
func policyPublisher(t *testing.T, store *Store, refs application.PublishedPolicyValidator) *application.PolicyPublisher {
	t.Helper()
	cipher, err := credentialcrypto.New("k1", map[string]credentialcrypto.Key{"k1": {Encryption: bytes.Repeat([]byte{1}, 32), MAC: bytes.Repeat([]byte{2}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	var seq atomic.Int64
	s, err := application.NewPolicyPublisher(application.PolicyPublisherDependencies{Store: store, Access: allowOwner{}, Cipher: cipher, References: refs, ScopeID: testScope, NewID: func(prefix string) (string, error) { return fmt.Sprintf("%s_publish_%d", prefix, seq.Add(1)), nil }, Now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestPolicyPublisherAtomicReplayAndOwnerAgainstPostgreSQL(t *testing.T) {
	store, _, pool, a := policyPG(t)
	ctx := context.Background()
	refs := &policyReferenceFixture{}
	s := policyPublisher(t, store, refs)
	in := application.PublishAccessPolicyInput{Body: domain.DefaultAccessPolicy()}
	var wg sync.WaitGroup
	outputs := make([]application.PolicyPublishResult, 2)
	failures := make([]error, 2)
	for i := range outputs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); outputs[i], failures[i] = s.Publish(ctx, testActor, a.ID, "publish", in) }(i)
	}
	wg.Wait()
	if failures[0] != nil || failures[1] != nil || !reflect.DeepEqual(outputs[0], outputs[1]) {
		t.Fatal("concurrent first publication", failures)
	}
	first, err := outputs[0], failures[0]
	if err != nil || first.Distribution != "PENDING" {
		t.Fatal(first, err)
	}
	again, err := s.Publish(ctx, testActor, a.ID, "publish", in)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("replay", again, err)
	}
	if refs.calls != 0 {
		t.Fatal("deny policy resolved nonexistent grants")
	}
	if _, err = s.Publish(ctx, testActor, a.ID, "stale", in); !errors.Is(err, application.ErrPolicyRevisionConflict) {
		t.Fatal("stale publish", err)
	}
	in.ExpectedRevision = 1
	if _, err = s.Publish(ctx, testActor, a.ID, "publish", in); !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatal("key payload mismatch", err)
	}
	second, err := s.Publish(ctx, testActor, a.ID, "second", in)
	if err != nil || second.PolicyID != first.PolicyID || second.Revision != 2 || second.Digest == first.Digest {
		t.Fatal(second, err)
	}
	count := func(expected int) {
		t.Helper()
		var revisions, events, receipts int
		err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM channel_access_policy_revisions),(SELECT count(*) FROM control_outbox WHERE aggregate_type='ChannelAccessPolicy'),(SELECT count(*) FROM channel_command_receipts WHERE operation='PublishChannelAccessPolicy')`).Scan(&revisions, &events, &receipts)
		if err != nil || revisions != expected || events != 2*expected || receipts != expected {
			t.Fatal("atomic", revisions, events, receipts, err)
		}
	}
	count(2)
	if _, err = pool.Exec(ctx, `CREATE FUNCTION fail_policy_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='PublishChannelAccessPolicy' THEN RAISE EXCEPTION 'private receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_policy_receipt BEFORE INSERT ON channel_command_receipts FOR EACH ROW EXECUTE FUNCTION fail_policy_receipt()`); err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision = 2
	failed, err := s.Publish(ctx, testActor, a.ID, "third", in)
	if err == nil || failed != (application.PolicyPublishResult{}) {
		t.Fatal("uncommitted success returned", failed, err)
	}
	count(2)
	if _, err = pool.Exec(ctx, `DROP TRIGGER fail_policy_receipt ON channel_command_receipts; DROP FUNCTION fail_policy_receipt()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, testActor, a.ID, "third", in); err != nil {
		t.Fatal("retry", err)
	}
	count(3)
	// Even replay must revalidate the OWNER inside the transaction.
	if _, err = pool.Exec(ctx, `UPDATE tenant_memberships SET role='MEMBER' WHERE tenant_id=$1 AND user_id=$2`, testActor.TenantID, testActor.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, testActor, a.ID, "publish", application.PublishAccessPolicyInput{Body: domain.DefaultAccessPolicy()}); !errors.Is(err, application.ErrPermissionDenied) {
		t.Fatal("revoked owner replay", err)
	}
	count(3)
}
func TestPolicyPublisherRequiresTrustedReferencesAgainstPostgreSQL(t *testing.T) {
	store, _, pool, a := policyPG(t)
	ctx := context.Background()
	refs := &policyReferenceFixture{err: errors.New("PRIVATE_OWNER_ERROR")}
	s := policyPublisher(t, store, refs)
	body := domain.DefaultAccessPolicy()
	body.AccessMode = domain.AccessPublicLimited
	// Large policy bodies must not inflate the bounded success receipt.
	for i := 0; i < 1024; i++ {
		body.AllowedConversationIDs = append(body.AllowedConversationIDs, fmt.Sprintf("conversation-%04d-", i)+strings.Repeat("x", 80))
	}
	body.AllowedOperations = []domain.ChannelOperation{domain.OperationSessionNew, domain.OperationMessageSend}
	body.SessionPolicy = domain.PolicyRevisionReference{ID: "session-fixture", Revision: 1, Digest: "sha256:" + strings.Repeat("a", 64)}
	body.TenantQuota = domain.PolicyRevisionReference{ID: "quota-fixture", Revision: 1, Digest: "sha256:" + strings.Repeat("b", 64)}
	in := application.PublishAccessPolicyInput{Body: body}
	if _, err := s.Publish(ctx, testActor, a.ID, "public", in); !errors.Is(err, application.ErrDependencyUnavailable) || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("unknown owner approved/leaked", err)
	}
	refs.err = application.ErrPolicyReferenceDenied
	if _, err := s.Publish(ctx, testActor, a.ID, "public", in); !errors.Is(err, application.ErrPolicyReferenceDenied) {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM channel_access_policy_revisions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed owner check persisted", count, err)
	}
	// The explicit fixture accepts. The publisher must pass authoritative identity
	// and detach the signed input from the adapter's mutable view.
	refs.err = nil
	refs.mutate = true
	first, err := s.Publish(ctx, testActor, a.ID, "public", in)
	if err != nil {
		t.Fatal(err)
	}
	var receiptBytes int
	if err = pool.QueryRow(ctx, `SELECT octet_length(result_jsonb::text) FROM channel_command_receipts WHERE operation='PublishChannelAccessPolicy'`).Scan(&receiptBytes); err != nil || receiptBytes > 2048 {
		t.Fatal("unbounded success receipt", receiptBytes, err)
	}
	if refs.actor != testActor || refs.account.TenantID != a.TenantID || refs.account.ID != a.ID || refs.account.ScopeID != testScope {
		t.Fatal("untrusted scope")
	}
	calls := refs.calls
	refs.err = application.ErrPolicyReferenceDenied
	in.Body.AllowedOperations = []domain.ChannelOperation{domain.OperationMessageSend, domain.OperationSessionNew, domain.OperationSessionNew}
	again, err := s.Publish(ctx, testActor, a.ID, "public", in)
	if err != nil || !reflect.DeepEqual(first, again) || refs.calls != calls {
		t.Fatal("normalized replay resolved or republished", err)
	}
}
