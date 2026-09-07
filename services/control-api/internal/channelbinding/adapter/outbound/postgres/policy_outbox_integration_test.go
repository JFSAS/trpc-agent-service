package postgresadapter

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	natsadapter "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/adapter/outbound/nats"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
	"github.com/nats-io/nats.go"
)

type policyBroker struct {
	err   error
	calls int
}

func (p *policyBroker) PublishAccessPolicy(context.Context, string, []byte) error {
	p.calls++
	return p.err
}
func TestAccessPolicyOutboxLeaseRetryAndIntegrityAgainstPostgreSQL(t *testing.T) {
	store, _, pool, a := policyPG(t)
	ctx := context.Background()
	published, err := policyPublisher(t, store, &policyReferenceFixture{}).Publish(ctx, testActor, a.ID, "relay-first", application.PublishAccessPolicyInput{Body: domain.DefaultAccessPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	old, found, err := store.ClaimAccessPolicy(ctx)
	if err != nil || !found || old.EventID != published.EventID {
		t.Fatal(old, found, err)
	}
	if _, found, err = store.ClaimAccessPolicy(ctx); err != nil || found {
		t.Fatal("double claim", found, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE control_outbox SET claimed_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.EventID); err != nil {
		t.Fatal(err)
	}
	next, found, err := store.ClaimAccessPolicy(ctx)
	if err != nil || !found || next.Attempt != old.Attempt+1 || next.ClaimToken == old.ClaimToken {
		t.Fatal("reclaim", next, err)
	}
	if err = store.FinishAccessPolicy(ctx, old, true, "", 0); !errors.Is(err, application.ErrOutboxLeaseLost) {
		t.Fatal("old completion", err)
	}
	if err = store.FinishAccessPolicy(ctx, next, false, application.PolicyPublishUnavailable, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	due := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE control_outbox SET available_at=clock_timestamp()-interval '1 second' WHERE id=$1`, old.EventID); err != nil {
			t.Fatal(err)
		}
	}
	due()
	broker := &policyBroker{err: errors.New("PRIVATE_BROKER_DETAIL")}
	relay, err := application.NewAccessPolicyRelay(store, broker, testScope, testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if found, err = relay.Step(ctx); err != nil || !found {
		t.Fatal(err)
	}
	var state, code string
	if err = pool.QueryRow(ctx, `SELECT status,last_error FROM control_outbox WHERE id=$1`, old.EventID).Scan(&state, &code); err != nil || state != "PENDING" || code != application.PolicyPublishUnavailable {
		t.Fatal(state, code, err)
	}
	due()
	// Corrupt the claimed envelope in transit; do not disable immutable DB guards.
	relay, err = application.NewAccessPolicyRelay(corruptPolicyClaim{store}, broker, testScope, testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if found, err = relay.Step(ctx); err != nil || !found {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status,last_error FROM control_outbox WHERE id=$1`, old.EventID).Scan(&state, &code); err != nil || state != "FAILED" || code != application.PolicyOutboxIntegrity || broker.calls != 1 {
		t.Fatal(state, code, broker.calls, err)
	}
	if _, found, err = store.ClaimAccessPolicy(ctx); err != nil || found {
		t.Fatal("audit claimed", found, err)
	}
	if _, found, err = store.ClaimRoute(ctx); err != nil || found {
		t.Fatal("policy leaked to route relay", found, err)
	}
}
func TestAccessPolicyRelayDurableJetStreamACKAgainstPostgreSQL(t *testing.T) {
	url := os.Getenv("CONTROL_TEST_POLICY_NATS_URL")
	if url == "" {
		t.Skip("CONTROL_TEST_POLICY_NATS_URL is not set (requires disposable server)")
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = js.StreamInfo(channelv1.AccessPolicyStream); !errors.Is(err, nats.ErrStreamNotFound) {
		t.Fatal("expected isolated server without policy stream", err)
	}
	if _, err = js.AddStream(&nats.StreamConfig{Name: channelv1.AccessPolicyStream, Subjects: []string{channelv1.AccessPolicySubject}, Storage: nats.FileStorage, Retention: nats.LimitsPolicy, Discard: nats.DiscardNew, MaxBytes: 16 * 1024 * 1024, MaxMsgSize: channelv1.MaxAccessPolicyEventBytes, Duplicates: time.Minute}); err != nil {
		t.Fatal(err)
	}
	defer js.DeleteStream(channelv1.AccessPolicyStream)
	store, _, pool, a := policyPG(t)
	ctx := context.Background()
	result, err := policyPublisher(t, store, &policyReferenceFixture{}).Publish(ctx, testActor, a.ID, "durable-policy", application.PublishAccessPolicyInput{Body: domain.DefaultAccessPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := natsadapter.NewPublisher(nc)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := application.NewAccessPolicyRelay(store, broker, testScope, testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if found, err := relay.Step(ctx); err != nil || !found {
		t.Fatal(found, err)
	}
	var state string
	if err = pool.QueryRow(ctx, `SELECT status FROM control_outbox WHERE id=$1`, result.EventID).Scan(&state); err != nil || state != "PUBLISHED" {
		t.Fatal(state, err)
	}
	msg, err := js.GetLastMsg(channelv1.AccessPolicyStream, channelv1.AccessPolicySubject)
	if err != nil {
		t.Fatal(err)
	}
	event, err := channelv1.DecodeAccessPolicyEvent(msg.Data)
	if err != nil || event.EventID != result.EventID || event.PolicyDigest != result.Digest {
		t.Fatal(event, err)
	}
	if err = broker.PublishAccessPolicy(ctx, result.EventID, msg.Data); err != nil {
		t.Fatal("duplicate durable ack", err)
	}
	info, err := js.StreamInfo(channelv1.AccessPolicyStream)
	if err != nil || info.State.Msgs != 1 {
		t.Fatal("dedup", info, err)
	}
	// Equal event IDs in another tenant do not collide in JetStream deduplication.
	event.TenantID = "tnt_other"
	raw, _, err := event.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err = broker.PublishAccessPolicy(ctx, result.EventID, raw); err != nil {
		t.Fatal(err)
	}
	info, err = js.StreamInfo(channelv1.AccessPolicyStream)
	if err != nil || info.State.Msgs != 2 {
		t.Fatal("tenant dedup isolation", info, err)
	}
	if _, found, err := store.ClaimAccessPolicy(ctx); err != nil || found {
		t.Fatal("audit notification isolation", found, err)
	}
}

type corruptPolicyClaim struct{ *Store }

func (c corruptPolicyClaim) ClaimAccessPolicy(ctx context.Context) (application.AccessPolicyClaim, bool, error) {
	claim, found, err := c.Store.ClaimAccessPolicy(ctx)
	claim.PayloadDigest = "sha256:" + strings.Repeat("f", 64)
	return claim, found, err
}
