package application

import (
	"context"
	"strings"
	"testing"
	"time"

	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

type policyRelayBox struct {
	claim     AccessPolicyClaim
	code      string
	published bool
}

func (b *policyRelayBox) ClaimAccessPolicy(context.Context) (AccessPolicyClaim, bool, error) {
	return b.claim, true, nil
}
func (b *policyRelayBox) FinishAccessPolicy(_ context.Context, _ AccessPolicyClaim, p bool, c string, _ time.Duration) error {
	b.published = p
	b.code = c
	return nil
}

type policyRelayPublisher struct{ calls int }

func (p *policyRelayPublisher) PublishAccessPolicy(context.Context, string, []byte) error {
	p.calls++
	return nil
}
func TestPolicyRelayRejectsEnvelopeFenceMismatch(t *testing.T) {
	epoch := "11111111-1111-4111-8111-111111111111"
	e := channelv1.AccessPolicyEvent{SchemaVersion: 1, EventID: "evt_a", EventType: channelv1.AccessPolicyPublishedEvent, ScopeID: "scope_a", SourceEpoch: epoch, TenantID: "tnt_a", AccountID: "cha_a", PolicyID: "pol_a", Provider: "telegram", PolicyRevision: 1, PolicyDigest: "sha256:" + strings.Repeat("a", 64), OccurredAt: time.Now().UTC()}
	raw, digest, err := e.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"valid", "scope", "epoch", "tenant", "account", "policy", "revision", "event", "schema", "digest", "size"} {
		t.Run(field, func(t *testing.T) {
			box := &policyRelayBox{claim: AccessPolicyClaim{TenantID: e.TenantID, EventID: e.EventID, PolicyID: e.PolicyID, AccountID: e.AccountID, Revision: 1, SchemaVersion: "1", Payload: raw, PayloadDigest: digest, Attempt: 1}}
			scope, expectedEpoch := "scope_a", epoch
			switch field {
			case "scope":
				scope = "scope_other"
			case "epoch":
				expectedEpoch = "22222222-2222-4222-8222-222222222222"
			case "tenant":
				box.claim.TenantID = "tnt_other"
			case "account":
				box.claim.AccountID = "cha_other"
			case "policy":
				box.claim.PolicyID = "pol_other"
			case "revision":
				box.claim.Revision = 2
			case "event":
				box.claim.EventID = "evt_other"
			case "schema":
				box.claim.SchemaVersion = "2"
			case "digest":
				box.claim.PayloadDigest = "sha256:bad"
			case "size":
				box.claim.Payload = []byte(strings.Repeat("x", channelv1.MaxAccessPolicyEventBytes+1))
			}
			publisher := &policyRelayPublisher{}
			relay, err := NewAccessPolicyRelay(box, publisher, scope, expectedEpoch)
			if err != nil {
				t.Fatal(err)
			}
			if found, err := relay.Step(context.Background()); err != nil || !found {
				t.Fatal(found, err)
			}
			if field == "valid" {
				if !box.published || publisher.calls != 1 {
					t.Fatal("valid notification not published")
				}
			} else if box.published || publisher.calls != 0 || box.code != PolicyOutboxIntegrity {
				t.Fatal(field, box, publisher)
			}
		})
	}
}
