package domain_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	d "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

func policyFixture(t *testing.T) (d.Account, d.AccessPolicyBody, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	a, err := d.NewAccount("tenant-a", "account-a", "scope-a", "owner-a", d.Telegram, "123", "Bot", "", now)
	if err != nil {
		t.Fatal(err)
	}
	body := d.DefaultAccessPolicy()
	body.AccessMode = d.AccessAllowlist
	body.AllowedPrincipalIDs = []string{"principal-b", "principal-a"}
	body.AllowedConversationIDs = []string{"conversation-b", "conversation-a"}
	body.AllowedOperations = []d.ChannelOperation{d.OperationSessionNew, d.OperationMessageSend}
	body.SessionPolicy = d.PolicyRevisionReference{ID: "session-policy", Revision: 1, Digest: "sha256:" + strings.Repeat("a", 64)}
	body.TenantQuota = d.PolicyRevisionReference{ID: "quota-policy", Revision: 2, Digest: "sha256:" + strings.Repeat("b", 64)}
	return a, body, now
}
func TestAccessPolicyDefaultDenyAndCanonicalRevision(t *testing.T) {
	a, body, now := policyFixture(t)
	defaults := d.DefaultAccessPolicy()
	if defaults.AccessMode != d.AccessDenyAll || len(defaults.AllowedOperations) != 0 || len(defaults.AllowedPrincipalIDs) != 0 {
		t.Fatal("implicit grant")
	}
	if _, err := d.PrepareAccessPolicyRevision(a, "policy-a", 1, "owner-a", defaults, now); err != nil {
		t.Fatal("default deny", err)
	}
	first, err := d.PrepareAccessPolicyRevision(a, "policy-a", 1, "owner-a", body, now)
	if err != nil {
		t.Fatal(err)
	}
	// Equivalent sets, different ordering and duplicates, produce one canonical body.
	permuted := body
	permuted.AllowedPrincipalIDs = []string{"principal-a", "principal-b", "principal-a"}
	permuted.AllowedConversationIDs = []string{"conversation-a", "conversation-b"}
	permuted.AllowedOperations = []d.ChannelOperation{d.OperationMessageSend, d.OperationSessionNew, d.OperationMessageSend, d.OperationSessionNew}
	second, err := d.PrepareAccessPolicyRevision(a, "policy-a", 1, "owner-a", permuted, now)
	if err != nil || first.Digest != second.Digest || !bytes.Equal(first.Body, second.Body) {
		t.Fatal("canonicalization", err)
	}
	if body.AllowedPrincipalIDs[0] != "principal-b" {
		t.Fatal("input slice mutated")
	}
	body.AllowedPrincipalIDs[0] = "mutated"
	if first.Validate() != nil {
		t.Fatal("candidate aliases caller input")
	}
	if first.TenantID != a.TenantID || first.AccountID != a.ID || first.Provider != a.Provider {
		t.Fatal("wrong owner scope")
	}
	encoded, _ := json.Marshal(first)
	decoded, err := d.DecodeAccessPolicyRevision(encoded)
	if err != nil || !reflect.DeepEqual(first, decoded) {
		t.Fatal("roundtrip", err)
	}
	// Revision and scope are covered by the digest, not only the policy body.
	second.Revision++
	if second.Validate() == nil {
		t.Fatal("revision substitution")
	}
	second = first
	second.TenantID = "tenant-b"
	if second.Validate() == nil {
		t.Fatal("cross tenant substitution")
	}
	second = first
	second.AccountID = "account-b"
	if second.Validate() == nil {
		t.Fatal("cross account substitution")
	}
}
func TestAccessPolicyRejectsInvalidBodyWithoutInputEcho(t *testing.T) {
	a, valid, now := policyFixture(t)
	for _, name := range []string{"mode", "operation", "principal", "conversation", "session_ref", "quota_ref", "freshness_zero", "freshness_overflow", "too_many_members", "deny_grants", "public_missing_ref", "public_principal_list"} {
		t.Run(name, func(t *testing.T) {
			body := valid
			switch name {
			case "mode":
				body.AccessMode = "PRIVATE_CANARY"
			case "operation":
				body.AllowedOperations = []d.ChannelOperation{"PRIVATE_CANARY"}
			case "principal":
				body.AllowedPrincipalIDs = []string{"PRIVATE_CANARY\n"}
			case "conversation":
				body.AllowedConversationIDs = []string{"PRIVATE_CANARY\n"}
			case "session_ref":
				body.SessionPolicy.Revision = 0
			case "quota_ref":
				body.TenantQuota.Digest = "PRIVATE_CANARY"
			case "freshness_zero":
				body.AuthorizationMaxAgeMS = 0
			case "freshness_overflow":
				body.AuthorizationMaxAgeMS = d.MaxAuthorizationAgeMS + 1
			case "too_many_members":
				body.AllowedPrincipalIDs = make([]string, d.MaxAccessPolicyMembers+1)
			case "deny_grants":
				body.AccessMode = d.AccessDenyAll
			case "public_missing_ref":
				body.AccessMode = d.AccessPublicLimited
				body.AllowedPrincipalIDs = nil
				body.TenantQuota = d.PolicyRevisionReference{}
			case "public_principal_list":
				body.AccessMode = d.AccessPublicLimited
			}
			_, err := d.PrepareAccessPolicyRevision(a, "policy-a", 1, "owner-a", body, now)
			if err == nil || strings.Contains(err.Error(), "PRIVATE_CANARY") {
				t.Fatal("invalid body or echoed value", err)
			}
		})
	}
}
func TestAccessPolicyDecoderRejectsTamperingAndUnknownFields(t *testing.T) {
	a, body, now := policyFixture(t)
	p, err := d.PrepareAccessPolicyRevision(a, "policy-a", 1, "owner-a", body, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	for _, bad := range [][]byte{
		append(append([]byte{}, raw...), []byte(` {}`)...),
		bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1,"bypass":true`), 1),
		bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1),
		bytes.Replace(raw, []byte(`principal-a`), []byte(`principal-z`), 1),
		bytes.Replace(raw, []byte(`"access_mode":"ALLOWLIST"`), []byte(`"access_mode":"ALLOWLIST","bypass":true`), 1),
	} {
		if _, err := d.DecodeAccessPolicyRevision(bad); err == nil {
			t.Fatal("accepted altered wire document")
		}
	}
}
func TestAccessPolicyPublicLimitedIsCandidateNotDependencyProof(t *testing.T) {
	a, body, now := policyFixture(t)
	body.AccessMode = d.AccessPublicLimited
	body.AllowedPrincipalIDs = nil
	candidate, err := d.PrepareAccessPolicyRevision(a, "policy-a", 1, "owner-a", body, now)
	if err != nil || candidate.Validate() != nil {
		t.Fatal(err)
	}
	// Candidate creation validates exact references only; publication must resolve
	// their tenant, isolation, low quota and tool capabilities under owner contracts.
	if candidate.Revision != 1 {
		t.Fatal(candidate.Revision)
	}
}

func TestAccessPolicyMetadataAndInputBoundaries(t *testing.T) {
	a, body, now := policyFixture(t)
	for _, revision := range []int64{0, -1, d.MaxVersion + 1} {
		if _, err := d.PrepareAccessPolicyRevision(a, "policy-a", revision, "owner-a", body, now); err == nil {
			t.Fatal("revision bound", revision)
		}
	}
	if _, err := d.PrepareAccessPolicyRevision(a, "policy-a", 1, "owner-a", body, time.Time{}); err == nil {
		t.Fatal("zero publication time")
	}
	p, err := d.PrepareAccessPolicyRevision(a, "policy-a", d.MaxVersion, "owner-a", body, now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Validate() != nil {
		t.Fatal("max supported revision")
	}
	for _, field := range []string{"provider", "publisher", "time", "schema", "digest"} {
		other := p
		switch field {
		case "provider":
			other.Provider = d.WeCom
		case "publisher":
			other.PublishedBy = "owner-b"
		case "time":
			other.PublishedAt = now.Add(time.Second)
		case "schema":
			other.SchemaVersion = 2
		case "digest":
			other.Digest = ""
		}
		if other.Validate() == nil {
			t.Fatal("metadata mutation", field)
		}
	}
	original, _ := json.Marshal(p)
	invalids := [][]byte{
		bytes.Replace(original, []byte(`"schema_version":1`), []byte(`"Schema_Version":1`), 1),
		bytes.Replace(original, []byte(`"published_by":"owner-a"`), []byte(`"published_by":null`), 1),
		bytes.Replace(original, []byte(`"published_by":"owner-a",`), nil, 1),
		[]byte(`null`), []byte{0xff}, make([]byte, d.MaxAccessPolicyDocumentBytes+1),
	}
	for i, raw := range invalids {
		if _, err := d.DecodeAccessPolicyRevision(raw); err == nil {
			t.Fatal("accepted invalid wire document", i)
		}
	}
}

func FuzzDecodeAccessPolicyRevision(f *testing.F) {
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	account, err := d.NewAccount("tenant-a", "account-a", "scope-a", "owner-a", d.Telegram, "123", "Bot", "", now)
	if err != nil {
		f.Fatal(err)
	}
	candidate, err := d.PrepareAccessPolicyRevision(account, "policy-a", 1, "owner-a", d.DefaultAccessPolicy(), now)
	if err != nil {
		f.Fatal(err)
	}
	seed, _ := json.Marshal(candidate)
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		decoded, err := d.DecodeAccessPolicyRevision(raw)
		if err != nil {
			return
		}
		if err = decoded.Validate(); err != nil {
			t.Fatal("decoder returned invalid policy", err)
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		again, err := d.DecodeAccessPolicyRevision(encoded)
		if err != nil || !reflect.DeepEqual(decoded, again) {
			t.Fatal("unstable accepted policy", err)
		}
	})
}

func TestSharedSessionResetRequiresExplicitOperation(t *testing.T) {
	_, body, _ := policyFixture(t)
	normalized, e := d.NormalizeAccessPolicyBody(body)
	if e != nil {
		t.Fatal(e)
	}
	before := normalized
	for _, op := range before.AllowedOperations {
		if op == d.OperationSessionResetShared {
			t.Fatal("shared reset implicitly granted")
		}
	}
	body.AllowedOperations = append(body.AllowedOperations, d.OperationSessionResetShared)
	if _, e = d.NormalizeAccessPolicyBody(body); e != nil {
		t.Fatal("explicit shared reset rejected", e)
	}
}
