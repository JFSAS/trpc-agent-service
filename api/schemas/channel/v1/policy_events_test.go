package channelv1

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPolicyEventClosedCanonicalContracts(t *testing.T) {
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	access := AccessPolicyEvent{SchemaVersion: 1, EventID: "evt_a", EventType: AccessPolicyPublishedEvent, ScopeID: "scope_a", SourceEpoch: "11111111-1111-4111-8111-111111111111", TenantID: "tnt_a", AccountID: "cha_a", Provider: "telegram", PolicyID: "pol_a", PolicyRevision: 1, PolicyDigest: "sha256:" + strings.Repeat("a", 64), OccurredAt: now}
	definition := PolicyDefinitionEvent{SchemaVersion: 1, EventID: "evt_d", EventType: PolicyDefinitionPublishedEvent, TenantID: "tnt_a", PolicyKind: "session", PolicyID: "session-a", Revision: 1, Digest: "sha256:" + strings.Repeat("b", 64), OccurredAt: now}
	for _, audit := range []bool{false, true} {
		a, d := access, definition
		if audit {
			a.EventType = AccessPolicyAuditEvent
			a.PolicyAuditFields = &PolicyAuditFields{ActorID: "usr_a", ActorKind: "CONTROL_USER", Action: "channel.access_policy.publish", Decision: "PUBLISHED", ReasonCode: "OWNER_POLICY_PUBLICATION"}
			d.EventType = PolicyDefinitionAuditEvent
			copy := *a.PolicyAuditFields
			copy.Action = "channel.policy_definition.publish"
			d.PolicyAuditFields = &copy
		}
		raw, digest, err := a.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		round, err := DecodeAccessPolicyEvent(raw)
		if err != nil {
			t.Fatal(err)
		}
		again, digest2, err := round.CanonicalJSON()
		if err != nil || !bytes.Equal(raw, again) || digest != digest2 || digest == a.PolicyDigest {
			t.Fatal("access canonical roundtrip")
		}
		raw, digest, err = d.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		def, err := DecodePolicyDefinitionEvent(raw)
		if err != nil {
			t.Fatal(err)
		}
		again, digest2, err = def.CanonicalJSON()
		if err != nil || !bytes.Equal(raw, again) || digest != digest2 || digest == d.Digest {
			t.Fatal("definition canonical roundtrip")
		}
	}
	raw, _, _ := access.CanonicalJSON()
	for _, kind := range []string{"unknown", "duplicate", "audit-missing-fields", "actor-leak", "invalid-epoch", "bad-digest", "wrong-version", "zero-time"} {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		switch kind {
		case "unknown":
			m["body"] = map[string]any{"secret": "PRIVATE"}
		case "audit-missing-fields":
			m["event_type"] = AccessPolicyAuditEvent
		case "actor-leak":
			m["actor_id"] = "usr_a"
		case "invalid-epoch":
			m["source_epoch"] = "bad"
		case "bad-digest":
			m["policy_digest"] = "sha256:bad"
		case "wrong-version":
			m["schema_version"] = 2
		case "zero-time":
			m["occurred_at"] = "0001-01-01T00:00:00Z"
		}
		b, _ := json.Marshal(m)
		if kind == "duplicate" {
			b = append([]byte(`{"event_id":"evt_other",`), b[1:]...)
		}
		e, err := DecodeAccessPolicyEvent(b)
		if err == nil || e != (AccessPolicyEvent{}) {
			t.Fatal(kind, e, err)
		}
	}
	definition.EventType = PolicyDefinitionAuditEvent
	if _, _, err := definition.CanonicalJSON(); err == nil {
		t.Fatal("audit without actor accepted")
	}
	definition.EventType = PolicyDefinitionPublishedEvent
	definition.PolicyAuditFields = &PolicyAuditFields{ActorID: "usr_a"}
	if _, _, err := definition.CanonicalJSON(); err == nil {
		t.Fatal("publication with audit data accepted")
	}
}
