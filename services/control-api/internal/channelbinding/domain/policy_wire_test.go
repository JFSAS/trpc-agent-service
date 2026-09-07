package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	d "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

func policyWireEnvelope(t *testing.T, p d.ChannelAccessPolicyRevision) []byte {
	t.Helper()
	raw, err := json.Marshal(struct {
		SchemaVersion int                           `json:"schema_version"`
		ScopeID       string                        `json:"scope_id"`
		SourceEpoch   string                        `json:"source_epoch"`
		Policy        d.ChannelAccessPolicyRevision `json:"policy"`
	}{1, "scope-a", "11111111-1111-4111-8111-111111111111", p})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPolicyOwnerCanonicalDocumentsDecodeInSharedWire(t *testing.T) {
	for _, mode := range []d.ChannelAccessMode{d.AccessDenyAll, d.AccessAllowlist, d.AccessPublicLimited} {
		t.Run(string(mode), func(t *testing.T) {
			a, body, now := policyFixture(t)
			body.AccessMode = mode
			switch mode {
			case d.AccessDenyAll:
				body = d.DefaultAccessPolicy()
			case d.AccessPublicLimited:
				body.AllowedPrincipalIDs = []string{}
			}
			// Exercise real owner preparation, not a hand-reimplemented fixture digest.
			p, err := d.PrepareAccessPolicyRevision(a, "policy-a", 1, "owner-a", body, now)
			if err != nil {
				t.Fatal(err)
			}
			got, err := wire.DecodeAccessPolicyResolveResponse(policyWireEnvelope(t, p))
			if err != nil || got.Policy.Digest != p.Digest || got.Policy.AccountID != a.ID || got.Policy.Body.AccessMode != string(mode) {
				t.Fatal(got, err)
			}
		})
	}
}
func TestPolicyWireRejectsNoncanonicalBodyEvenWithRecomputedDigest(t *testing.T) {
	for _, kind := range []string{"unsorted", "duplicate", "whitespace", "unicode-space", "control", "oversize-conversation"} {
		t.Run(kind, func(t *testing.T) {
			a, b, now := policyFixture(t)
			p, err := d.PrepareAccessPolicyRevision(a, "policy-a", 1, "owner-a", b, now)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err = json.Unmarshal(p.Body, &body); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "unsorted":
				body["allowed_principal_ids"] = []string{"principal-b", "principal-a"}
			case "duplicate":
				body["allowed_principal_ids"] = []string{"principal-a", "principal-a"}
			case "whitespace":
				body["allowed_conversation_ids"] = []string{"room name"}
			case "unicode-space":
				body["allowed_conversation_ids"] = []string{"room\u00a0name"}
			case "control":
				body["allowed_conversation_ids"] = []string{"room\u0000name"}
			case "oversize-conversation":
				body["allowed_conversation_ids"] = []string{strings.Repeat("中", 400)}
			}
			p.Body, _, err = d.CanonicalJSON(body)
			if err != nil {
				t.Fatal(err)
			}
			p.Digest = ""
			_, p.Digest, err = d.CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			got, err := wire.DecodeAccessPolicyResolveResponse(policyWireEnvelope(t, p))
			if err == nil || got.Policy.Digest != "" {
				t.Fatal("invalid document accepted", got, err)
			}
		})
	}
}
