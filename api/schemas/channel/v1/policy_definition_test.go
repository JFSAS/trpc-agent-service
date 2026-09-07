package channelv1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
)

func definitionFixture(t *testing.T, kind string) []byte {
	t.Helper()
	d := PolicyDefinitionDocument{SchemaVersion: 1, TenantID: "tenant", PolicyID: "policy", Kind: kind, Revision: 1, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), Definition: PolicyDefinition{Enabled: true}}
	if kind == "session" {
		d.Definition.Session = &SessionDefinition{Partition: "per_user_in_conversation"}
	} else {
		d.Definition.Quota = &QuotaDefinition{PublicLimited: true, MaxConcurrentRuns: 1, MaxRunsPerMinute: 5}
	}
	raw, _ := json.Marshal(d)
	canonical, e := jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(canonical)
	d.Digest = "sha256:" + hex.EncodeToString(sum[:])
	raw, _ = json.Marshal(d)
	return raw
}
func TestPolicyDefinitionCompleteEnvelope(t *testing.T) {
	for _, kind := range []string{"session", "quota"} {
		t.Run(kind, func(t *testing.T) {
			raw := definitionFixture(t, kind)
			d, e := DecodePolicyDefinitionDocument(raw)
			if e != nil || d.Kind != kind {
				t.Fatal(d, e)
			}
			for _, field := range []string{"tenant_id", "policy_id", "kind", "revision", "definition", "published_by", "published_at", "digest"} {
				var body map[string]any
				json.Unmarshal(raw, &body)
				delete(body, field)
				bad, _ := json.Marshal(body)
				out, e := DecodePolicyDefinitionDocument(bad)
				if e == nil || !reflect.DeepEqual(out, PolicyDefinitionDocument{}) {
					t.Fatal("missing field trusted", field)
				}
			}
			for name, mutate := range map[string]func(map[string]any){
				"tenant":  func(v map[string]any) { v["tenant_id"] = "other" },
				"unknown": func(v map[string]any) { v["token"] = "SECRET_CANARY" },
				"null":    func(v map[string]any) { v["definition"] = nil },
				"kind substitution": func(v map[string]any) {
					if kind == "session" {
						v["kind"] = "quota"
					} else {
						v["kind"] = "session"
					}
				},
				"changed enabled":    func(v map[string]any) { v["definition"].(map[string]any)["enabled"] = false },
				"extra owner fields": func(v map[string]any) { v["definition"].(map[string]any)["token_limit"] = 5 },
				"time alias":         func(v map[string]any) { v["published_at"] = "2026-09-08T00:00:00.000Z" },
			} {
				var body map[string]any
				json.Unmarshal(raw, &body)
				mutate(body)
				bad, _ := json.Marshal(body)
				out, e := DecodePolicyDefinitionDocument(bad)
				if e == nil || strings.Contains(e.Error(), "SECRET_CANARY") || !reflect.DeepEqual(out, PolicyDefinitionDocument{}) {
					t.Fatal(name, e)
				}
			}
			for _, bad := range [][]byte{append([]byte(`{"schema_version":1,`), raw[1:]...), []byte(strings.Repeat(" ", MaxPolicyDefinitionBytes+1)), []byte(strings.Replace(string(raw), `"tenant_id"`, `"Tenant_ID"`, 1))} {
				if _, e := DecodePolicyDefinitionDocument(bad); e == nil {
					t.Fatal("invalid envelope accepted")
				}
			}
		})
	}
}

func TestPolicyDefinitionRejectsSignedInvalidShape(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"public zero quota": func(v map[string]any) {
			v["definition"].(map[string]any)["quota"].(map[string]any)["max_runs_per_minute"] = float64(0)
		},
		"negative quota": func(v map[string]any) {
			v["definition"].(map[string]any)["quota"].(map[string]any)["max_concurrent_runs"] = float64(-1)
		},
		"kind mismatch": func(v map[string]any) { v["kind"] = "session" },
		"two kinds": func(v map[string]any) {
			v["definition"].(map[string]any)["session"] = map[string]any{"partition": "per_user_in_conversation"}
		},
		"unknown budget": func(v map[string]any) {
			v["definition"].(map[string]any)["quota"].(map[string]any)["token_budget"] = float64(5)
		},
		"oversized revision": func(v map[string]any) { v["revision"] = float64(9007199254740992) },
	} {
		t.Run(name, func(t *testing.T) {
			var v map[string]any
			json.Unmarshal(definitionFixture(t, "quota"), &v)
			change(v)
			delete(v, "digest")
			raw, _ := json.Marshal(v)
			raw, e := jcs.Transform(raw)
			if e != nil {
				t.Fatal(e)
			}
			sum := sha256.Sum256(raw)
			v["digest"] = "sha256:" + hex.EncodeToString(sum[:])
			raw, _ = json.Marshal(v)
			if _, e = DecodePolicyDefinitionDocument(raw); e == nil {
				t.Fatal("valid digest bypassed closed semantic shape")
			}
		})
	}
}
