package authorization

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

func dependency(t *testing.T, kind string) wire.PolicyDefinitionDocument {
	t.Helper()
	d := wire.PolicyDefinitionDocument{SchemaVersion: 1, TenantID: "tenant", PolicyID: kind, Kind: kind, Revision: 1, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}
	if kind == "session" {
		d.Definition.Session = &wire.SessionDefinition{Partition: "per_user_in_conversation"}
	} else {
		d.Definition.Quota = &wire.QuotaDefinition{MaxConcurrentRuns: 1, MaxRunsPerMinute: 5}
	}
	raw, _ := json.Marshal(d)
	raw, e := jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	d.Digest = "sha256:" + hex.EncodeToString(sum[:])
	return d
}
func TestDependencyExactBindingAndDetachedContent(t *testing.T) {
	s, q := dependency(t, "session"), dependency(t, "quota")
	p := wire.AccessPolicyDocument{TenantID: "tenant", Body: wire.AccessPolicyBody{SessionPolicy: wire.PolicyReference{ID: s.PolicyID, Revision: s.Revision, Digest: s.Digest}, TenantQuota: wire.PolicyReference{ID: q.PolicyID, Revision: q.Revision, Digest: q.Digest}}}
	got, e := MatchPolicyDependencies(p, s, q)
	if e != nil {
		t.Fatal(e)
	}
	// Disabled content still has valid integrity: this function must not silently
	// collapse immutable verification into enabled/runtime quota authorization.
	if got.Session.Definition.Enabled {
		t.Fatal("enabled status invented")
	}
	got.Session.Definition.Session.Partition = "shared_conversation"
	if s.Definition.Session.Partition != "per_user_in_conversation" {
		t.Fatal("shared pointers")
	}
	for name, mutate := range map[string]func(*wire.AccessPolicyDocument){"tenant": func(p *wire.AccessPolicyDocument) { p.TenantID = "other" }, "revision": func(p *wire.AccessPolicyDocument) { p.Body.SessionPolicy.Revision++ }, "id": func(p *wire.AccessPolicyDocument) { p.Body.TenantQuota.ID = "other" }, "digest": func(p *wire.AccessPolicyDocument) { p.Body.TenantQuota.Digest = s.Digest }} {
		cp := p
		mutate(&cp)
		out, e := MatchPolicyDependencies(cp, s, q)
		if e == nil || !reflect.DeepEqual(out, PolicyDependencies{}) {
			t.Fatal(name, e)
		}
	}
	if _, e := MatchPolicyDependencies(p, q, s); e == nil {
		t.Fatal("kind substitution")
	}
	q.Definition.Quota.MaxRunsPerMinute++
	if _, e := MatchPolicyDependencies(p, s, q); e == nil {
		t.Fatal("typed forgery")
	}
}
