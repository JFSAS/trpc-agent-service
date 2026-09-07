package authorizationpostgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
	install "github.com/liuzengh/trpc-agent-service/platform/channel/authorization/postgres"
	worker "github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
)

func definition(t *testing.T, kind string) wire.PolicyDefinitionDocument {
	t.Helper()
	d := wire.PolicyDefinitionDocument{SchemaVersion: 1, TenantID: "tenant", PolicyID: kind, Kind: kind, Revision: 1, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), Definition: wire.PolicyDefinition{Enabled: true}}
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
func TestDependencyAtomicInstallBothOwnersPostgres(t *testing.T) {
	for _, ns := range []install.Namespace{install.Gateway, install.Worker} {
		t.Run(string(ns), func(t *testing.T) {
			gateway, pool := setup(t)
			ctx := context.Background()
			runtimePool := gateway.pool
			if ns == install.Worker {
				if e := worker.Apply(ctx, pool); e != nil {
					t.Fatal(e)
				}
				runtimePool = pool
			}
			store, e := install.New(runtimePool, "pool", epoch, ns)
			if e != nil {
				t.Fatal(e)
			}
			result, pages := fixture(t, 1, 2)
			s, q := definition(t, "session"), definition(t, "quota")
			result.Policy.Body.AccessMode = "ALLOWLIST"
			result.Policy.Body.AllowedOperations = []string{"message.send"}
			result.Policy.Body.SessionPolicy = wire.PolicyReference{ID: s.PolicyID, Revision: s.Revision, Digest: s.Digest}
			result.Policy.Body.TenantQuota = wire.PolicyReference{ID: q.PolicyID, Revision: q.Revision, Digest: q.Digest}
			sign(t, &result.Policy)
			result.Manifest.Policy.Digest = result.Policy.Digest
			table := string(ns) + "_authorization_snapshots"
			// Legacy/source-only install is explicitly incomplete, never a grant.
			if e = store.Refresh(ctx, target(), reader(result, pages)); e != nil {
				t.Fatal(e)
			}
			result.Dependencies = &shared.PolicyDependencies{Session: s, Quota: q}
			if e = store.Refresh(ctx, target(), reader(result, pages)); e != nil {
				t.Fatal("same-generation complete upgrade", e)
			}
			var old []byte
			var start, until time.Time
			if e = pool.QueryRow(ctx, "SELECT jsonb_build_array(session_policy_jsonb,quota_policy_jsonb),read_started_at,fresh_until FROM "+table).Scan(&old, &start, &until); e != nil || until.Sub(start) != 30*time.Second {
				t.Fatal("fixed DB expiry", e)
			}
			check := func() {
				t.Helper()
				var now []byte
				var anchor time.Time
				if e := pool.QueryRow(ctx, "SELECT jsonb_build_array(session_policy_jsonb,quota_policy_jsonb),read_started_at FROM "+table).Scan(&now, &anchor); e != nil || string(now) != string(old) || !anchor.Equal(start) {
					t.Fatal("failed install changed prior state", e)
				}
			}
			broken := result
			broken.Dependencies = &shared.PolicyDependencies{Session: s, Quota: q}
			broken.Dependencies.Quota.Digest = s.Digest
			if e = store.Refresh(ctx, target(), reader(broken, pages)); e == nil {
				t.Fatal("mismatched dependency accepted")
			}
			check()
			broken = result
			broken.Dependencies = nil
			if e = store.Refresh(ctx, target(), reader(broken, pages)); e == nil {
				t.Fatal("legacy downgrade erased complete set")
			}
			check()
			for _, sql := range []string{"UPDATE " + table + " SET session_policy_jsonb=NULL", "UPDATE " + table + " SET session_policy_jsonb=NULL,quota_policy_jsonb=NULL", "UPDATE " + table + " SET quota_policy_jsonb=jsonb_set(quota_policy_jsonb,'{tenant_id}','\"other\"')"} {
				if _, e = pool.Exec(ctx, sql); e == nil {
					t.Fatal("SQL dependency guard bypass", sql)
				}
			}
			check()
			// Corrupt a new policy's dependency after the write; readback must abort all
			// head/principal changes, not only the dependency value.
			trigger := fmt.Sprintf(`CREATE FUNCTION corrupt_dependencies() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.quota_policy_jsonb=jsonb_set(NEW.quota_policy_jsonb,'{definition,enabled}','false'); RETURN NEW; END $$; CREATE TRIGGER z_corrupt_dependencies BEFORE UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION corrupt_dependencies();`, table)
			if _, e = pool.Exec(ctx, trigger); e != nil {
				t.Fatal(e)
			}
			newer := result
			newer.Policy.Revision = 2
			sign(t, &newer.Policy)
			newer.Manifest.Policy = wire.PolicyReference{ID: newer.Policy.PolicyID, Revision: 2, Digest: newer.Policy.Digest}
			newer.Manifest.Generation = 2
			newerPages := append([]wire.AuthorizationSnapshotPage{}, pages...)
			for i := range newerPages {
				newerPages[i].Generation = 2
			}
			if e = store.Refresh(ctx, target(), reader(newer, newerPages)); e == nil {
				t.Fatal("DB mutation trusted")
			}
			check()
			t.Log("ATOMIC_DEPENDENCIES=PASS owner=", ns, "same-generation upgrade; DB expiry; invalid/partial/downgrade/SQL mutation rollback")
		})
	}
}
