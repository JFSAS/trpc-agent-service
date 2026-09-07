package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

type ownerPolicyFixture struct {
	session                             PublishedSessionPolicy
	quota                               PublishedQuotaPolicy
	sessionErr, quotaErr, toolErr       error
	sessionCalls, quotaCalls, toolCalls int
	tenant                              string
}

func (f *ownerPolicyFixture) ReadPublishedSessionPolicy(_ context.Context, tenant string, ref domain.PolicyRevisionReference) (PublishedSessionPolicy, error) {
	f.sessionCalls++
	f.tenant = tenant
	return f.session, f.sessionErr
}
func (f *ownerPolicyFixture) ReadPublishedQuotaPolicy(_ context.Context, tenant string, ref domain.PolicyRevisionReference) (PublishedQuotaPolicy, error) {
	f.quotaCalls++
	f.tenant = tenant
	return f.quota, f.quotaErr
}
func (f *ownerPolicyFixture) RequirePublicToolIsolation(_ context.Context, actor Actor, a domain.Account, body domain.AccessPolicyBody) error {
	f.toolCalls++
	return f.toolErr
}
func referenceFixture(t *testing.T) (*PolicyReferenceValidator, *ownerPolicyFixture, domain.Account, domain.AccessPolicyBody) {
	t.Helper()
	account, err := domain.NewAccount(owner.TenantID, "account-a", "scope-a", owner.UserID, domain.Telegram, "123", "Bot", "", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	body := domain.DefaultAccessPolicy()
	body.AccessMode = domain.AccessPublicLimited
	body.SessionPolicy = domain.PolicyRevisionReference{ID: "session-a", Revision: 1, Digest: "sha256:" + strings.Repeat("a", 64)}
	body.TenantQuota = domain.PolicyRevisionReference{ID: "quota-a", Revision: 2, Digest: "sha256:" + strings.Repeat("b", 64)}
	body.AllowedOperations = []domain.ChannelOperation{domain.OperationMessageSend}
	fixture := &ownerPolicyFixture{session: PublishedSessionPolicy{TenantID: owner.TenantID, Reference: body.SessionPolicy, Enabled: true, Partition: SessionPerUserInConversation}, quota: PublishedQuotaPolicy{TenantID: owner.TenantID, Reference: body.TenantQuota, Enabled: true, PublicLimited: true, MaxConcurrentRuns: 1, MaxRunsPerMinute: 5}}
	v, err := NewPolicyReferenceValidator(fixture, fixture, fixture, PublicQuotaCeiling{MaxConcurrentRuns: 1, MaxRunsPerMinute: 5})
	if err != nil {
		t.Fatal(err)
	}
	return v, fixture, account, body
}
func TestPolicyReferencesEnforcePublicIsolationAndQuotas(t *testing.T) {
	for _, kind := range []string{"valid", "shared", "session_disabled", "quota_disabled", "not_public", "unlimited_concurrency", "unlimited_rate", "concurrency_over", "rate_over", "sensitive_tools"} {
		t.Run(kind, func(t *testing.T) {
			v, f, a, body := referenceFixture(t)
			switch kind {
			case "shared":
				f.session.Partition = SessionSharedConversation
			case "session_disabled":
				f.session.Enabled = false
			case "quota_disabled":
				f.quota.Enabled = false
			case "not_public":
				f.quota.PublicLimited = false
			case "unlimited_concurrency":
				f.quota.MaxConcurrentRuns = 0
			case "unlimited_rate":
				f.quota.MaxRunsPerMinute = 0
			case "concurrency_over":
				f.quota.MaxConcurrentRuns = 2
			case "rate_over":
				f.quota.MaxRunsPerMinute = 6
			case "sensitive_tools":
				f.toolErr = ErrPolicyReferenceDenied
			}
			err := v.ValidatePublishedPolicy(context.Background(), owner, a, body)
			if kind == "valid" {
				if err != nil || f.toolCalls != 1 {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, ErrPolicyReferenceDenied) {
					t.Fatal(kind, err)
				}
				if kind != "sensitive_tools" && f.toolCalls != 0 {
					t.Fatal("tool owner called after denial")
				}
			}
		})
	}
}
func TestPolicyReferencesRejectUnprovenOwnerData(t *testing.T) {
	for _, kind := range []string{"session_tenant", "session_id", "session_revision", "session_digest", "partition", "quota_tenant", "quota_id", "quota_revision", "quota_digest", "negative_quota", "overflow_quota", "session_error", "quota_error", "tool_error"} {
		t.Run(kind, func(t *testing.T) {
			v, f, a, body := referenceFixture(t)
			switch kind {
			case "session_tenant":
				f.session.TenantID = "other"
			case "session_id":
				f.session.Reference.ID = "other"
			case "session_revision":
				f.session.Reference.Revision++
			case "session_digest":
				f.session.Reference.Digest = "other"
			case "partition":
				f.session.Partition = "cross_conversation"
			case "quota_tenant":
				f.quota.TenantID = "other"
			case "quota_id":
				f.quota.Reference.ID = "other"
			case "quota_revision":
				f.quota.Reference.Revision++
			case "quota_digest":
				f.quota.Reference.Digest = "other"
			case "negative_quota":
				f.quota.MaxRunsPerMinute = -1
			case "overflow_quota":
				f.quota.MaxConcurrentRuns = domain.MaxVersion + 1
			case "session_error":
				f.sessionErr = errors.New("PRIVATE_SESSION_ERROR")
			case "quota_error":
				f.quotaErr = errors.New("PRIVATE_QUOTA_ERROR")
			case "tool_error":
				f.toolErr = errors.New("PRIVATE_TOOL_ERROR")
			}
			err := v.ValidatePublishedPolicy(context.Background(), owner, a, body)
			if !errors.Is(err, ErrDependencyUnavailable) || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal(err)
			}
		})
	}
}
func TestPolicyReferenceModesAndCancellation(t *testing.T) {
	v, f, a, body := referenceFixture(t)
	if err := v.ValidatePublishedPolicy(context.Background(), owner, a, domain.DefaultAccessPolicy()); err != nil || f.sessionCalls != 0 || f.quotaCalls != 0 || f.toolCalls != 0 {
		t.Fatal("deny resolved grants", err)
	}
	body.AccessMode = domain.AccessAllowlist
	f.session.Partition = SessionSharedConversation
	f.quota.PublicLimited = false
	f.quota.MaxConcurrentRuns = 100
	f.quota.MaxRunsPerMinute = 100
	if err := v.ValidatePublishedPolicy(context.Background(), owner, a, body); err != nil || f.toolCalls != 0 {
		t.Fatal("private policy restricted as public", err)
	}
	actor := owner
	actor.TenantID = "other"
	if err := v.ValidatePublishedPolicy(context.Background(), actor, a, body); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := v.ValidatePublishedPolicy(ctx, owner, a, body); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestPolicyReferenceValidatorRequiresExplicitDependenciesAndCeilings(t *testing.T) {
	_, f, _, _ := referenceFixture(t)
	for _, c := range []PublicQuotaCeiling{{}, {MaxConcurrentRuns: 1}, {MaxRunsPerMinute: 5}, {MaxConcurrentRuns: -1, MaxRunsPerMinute: 5}, {MaxConcurrentRuns: 1, MaxRunsPerMinute: domain.MaxVersion + 1}} {
		if _, err := NewPolicyReferenceValidator(f, f, f, c); !errors.Is(err, ErrDependencyUnavailable) {
			t.Fatal(c, err)
		}
	}
	c := PublicQuotaCeiling{MaxConcurrentRuns: 1, MaxRunsPerMinute: 5}
	if _, err := NewPolicyReferenceValidator(nil, f, f, c); err == nil {
		t.Fatal("missing session reader")
	}
	if _, err := NewPolicyReferenceValidator(f, nil, f, c); err == nil {
		t.Fatal("missing quota reader")
	}
	if _, err := NewPolicyReferenceValidator(f, f, nil, c); err == nil {
		t.Fatal("missing tool isolation owner")
	}
}
