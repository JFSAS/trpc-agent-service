package postgresadapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

// Owner I/O fixtures only. The actual PolicyReferenceValidator and publisher
// run together against real PG; this does not certify production owner readers.
type publishedOwnerFixture struct {
	partition   string
	concurrency int64
	toolErr     error
	toolCalls   int
}

func (f *publishedOwnerFixture) ReadPublishedSessionPolicy(_ context.Context, tenant string, ref domain.PolicyRevisionReference) (application.PublishedSessionPolicy, error) {
	return application.PublishedSessionPolicy{TenantID: tenant, Reference: ref, Enabled: true, Partition: f.partition}, nil
}
func (f *publishedOwnerFixture) ReadPublishedQuotaPolicy(_ context.Context, tenant string, ref domain.PolicyRevisionReference) (application.PublishedQuotaPolicy, error) {
	return application.PublishedQuotaPolicy{TenantID: tenant, Reference: ref, Enabled: true, PublicLimited: true, MaxConcurrentRuns: f.concurrency, MaxRunsPerMinute: 5}, nil
}
func (f *publishedOwnerFixture) RequirePublicToolIsolation(context.Context, application.Actor, domain.Account, domain.AccessPolicyBody) error {
	f.toolCalls++
	return f.toolErr
}
func TestPolicyReferenceValidatorJoinsPublisherAndPostgreSQL(t *testing.T) {
	store, _, pool, a := policyPG(t)
	ctx := context.Background()
	owners := &publishedOwnerFixture{partition: application.SessionSharedConversation, concurrency: 1}
	validator, err := application.NewPolicyReferenceValidator(owners, owners, owners, application.PublicQuotaCeiling{MaxConcurrentRuns: 1, MaxRunsPerMinute: 5})
	if err != nil {
		t.Fatal(err)
	}
	publisher := policyPublisher(t, store, validator)
	body := domain.DefaultAccessPolicy()
	body.AccessMode = domain.AccessPublicLimited
	body.AllowedOperations = []domain.ChannelOperation{domain.OperationMessageSend}
	body.SessionPolicy = domain.PolicyRevisionReference{ID: "session-fixture", Revision: 1, Digest: "sha256:" + strings.Repeat("a", 64)}
	body.TenantQuota = domain.PolicyRevisionReference{ID: "quota-fixture", Revision: 1, Digest: "sha256:" + strings.Repeat("b", 64)}
	input := application.PublishAccessPolicyInput{Body: body}
	assertEmpty := func() {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM channel_access_policy_revisions)+(SELECT count(*) FROM control_outbox WHERE aggregate_type='ChannelAccessPolicy')+(SELECT count(*) FROM channel_command_receipts WHERE operation='PublishChannelAccessPolicy')`).Scan(&count); err != nil || count != 0 {
			t.Fatal("denied dependency left facts", count, err)
		}
	}
	for _, step := range []string{"shared", "over_quota", "sensitive_tools"} {
		switch step {
		case "over_quota":
			owners.partition = application.SessionPerUserInConversation
			owners.concurrency = 2
		case "sensitive_tools":
			owners.concurrency = 1
			owners.toolErr = application.ErrPolicyReferenceDenied
		}
		out, err := publisher.Publish(ctx, testActor, a.ID, "joined", input)
		if !errors.Is(err, application.ErrPolicyReferenceDenied) || out != (application.PolicyPublishResult{}) {
			t.Fatal(step, out, err)
		}
		assertEmpty()
	}
	owners.toolErr = nil
	result, err := publisher.Publish(ctx, testActor, a.ID, "joined", input)
	if err != nil || result.Distribution != "PENDING" || owners.toolCalls != 2 {
		t.Fatal(result, err, owners.toolCalls)
	}
	var revisions, events, receipts int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM channel_access_policy_revisions),(SELECT count(*) FROM control_outbox WHERE aggregate_type='ChannelAccessPolicy'),(SELECT count(*) FROM channel_command_receipts WHERE operation='PublishChannelAccessPolicy')`).Scan(&revisions, &events, &receipts); err != nil || revisions != 1 || events != 2 || receipts != 1 {
		t.Fatal("publication facts", revisions, events, receipts, err)
	}
}
