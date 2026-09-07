package postgresadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
	install "github.com/liuzengh/trpc-agent-service/platform/channel/authorization/postgres"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
)

const authEpoch = "11111111-1111-4111-8111-111111111111"

type routeAuthority func(context.Context, pgx.Tx, domain.Scope, string) error

func (f routeAuthority) AuthorizeSessionRoute(c context.Context, tx pgx.Tx, s domain.Scope, a string) error {
	return f(c, tx, s, a)
}

type authRead struct {
	read shared.AuthorizationRead
	page wire.AuthorizationSnapshotPage
}

func (r authRead) ReadAuthorization(c context.Context, _ shared.AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (shared.AuthorizationRead, error) {
	if e := stage(c, r.page); e != nil {
		return shared.AuthorizationRead{}, e
	}
	return r.read, nil
}
func signSessionDocument(t *testing.T, d *wire.PolicyDefinitionDocument) {
	t.Helper()
	d.Digest = ""
	raw, _ := json.Marshal(d)
	raw, e := jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	d.Digest = "sha256:" + hex.EncodeToString(sum[:])
}
func sessionAuthorityFixture(t *testing.T, account, partition, mode string) (domain.Scope, authRead) {
	t.Helper()
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	session := wire.PolicyDefinitionDocument{SchemaVersion: 1, TenantID: "tenant", PolicyID: "session", Kind: "session", Revision: 1, PublishedBy: "owner", PublishedAt: now, Definition: wire.PolicyDefinition{Enabled: true, Session: &wire.SessionDefinition{Partition: partition}}}
	quota := wire.PolicyDefinitionDocument{SchemaVersion: 1, TenantID: "tenant", PolicyID: "quota", Kind: "quota", Revision: 1, PublishedBy: "owner", PublishedAt: now, Definition: wire.PolicyDefinition{Enabled: true, Quota: &wire.QuotaDefinition{MaxConcurrentRuns: 1, MaxRunsPerMinute: 5}}}
	if mode == "disabled" {
		session.Definition.Enabled = false
	}
	signSessionDocument(t, &session)
	signSessionDocument(t, &quota)
	policy := wire.AccessPolicyDocument{SchemaVersion: 1, TenantID: "tenant", AccountID: account, Provider: "telegram", PolicyID: "access", Revision: 1, PublishedBy: "owner", PublishedAt: now, Body: wire.AccessPolicyBody{AccessMode: "ALLOWLIST", AllowedPrincipalIDs: []string{"principal"}, AllowedConversationIDs: []string{"group"}, AllowedOperations: []string{"message.send", "session.new", "session.reset_shared"}, AuthorizationMaxAgeMS: 30000, SessionPolicy: wire.PolicyReference{ID: session.PolicyID, Revision: 1, Digest: session.Digest}, TenantQuota: wire.PolicyReference{ID: quota.PolicyID, Revision: 1, Digest: quota.Digest}}}
	if mode == "no_shared_permission" {
		policy.Body.AllowedOperations = []string{"message.send", "session.new"}
	}
	raw, _ := json.Marshal(policy)
	raw, e := jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	policy.Digest = "sha256:" + hex.EncodeToString(sum[:])
	identity := wire.AuthorizationSnapshotIdentity{SchemaVersion: 1, ScopeID: "pool", SourceEpoch: authEpoch, TenantID: "tenant", AccountID: account, Provider: "telegram", Generation: 1}
	principal := wire.AuthorizationPrincipal{PrincipalID: "principal", ExternalUserID: "100", State: "ACTIVE", Revision: 1}
	if mode == "revoked" {
		principal.State = "REVOKED"
	}
	d := wire.NewPrincipalSetDigest()
	if e = d.Add(principal); e != nil {
		t.Fatal(e)
	}
	count, root := d.Result()
	manifest := wire.AuthorizationSnapshotManifest{AuthorizationSnapshotIdentity: identity, AccountRevision: 1, AccountEnabled: true, Policy: wire.PolicyReference{ID: policy.PolicyID, Revision: 1, Digest: policy.Digest}, CapturedAt: time.Now().UTC(), AuthorizationMaxAgeMS: 30000, PrincipalCount: count, PrincipalDigest: root}
	result := authRead{read: shared.AuthorizationRead{Manifest: manifest, Policy: policy, Dependencies: &shared.PolicyDependencies{Session: session, Quota: quota}}, page: wire.AuthorizationSnapshotPage{AuthorizationSnapshotIdentity: identity, Principals: []wire.AuthorizationPrincipal{principal}, Complete: true}}
	if mode == "missing" {
		result.read.Dependencies = nil
	}
	scope := domain.Scope{TenantID: "tenant", Provider: "telegram", AccountID: account, ConversationID: "group", ConversationKind: "group", BindingID: "binding", DeploymentRevisionID: "revision", PolicyID: session.PolicyID, PolicyRevision: 1, PolicyDigest: session.Digest, Partition: partition}
	if partition == domain.PerUser {
		scope.PrincipalID = "principal"
	}
	return scope, result
}
func TestCurrentSessionAuthorizerPostgres(t *testing.T) {
	mu, ru := os.Getenv("WORKER_TEST_MIGRATION_URL"), os.Getenv("WORKER_TEST_RUNTIME_URL")
	if mu == "" || ru == "" {
		t.Skip("requires owned Worker PostgreSQL roles")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	owner, e := pgxpool.New(ctx, mu)
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Close()
	runtime, e := pgxpool.New(ctx, ru)
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	if e = migrations.ApplyForRuntime(ctx, owner, "worker_runtime"); e != nil {
		t.Fatal(e)
	}
	projection, e := install.New(runtime, "pool", authEpoch, install.Worker)
	if e != nil {
		t.Fatal(e)
	}
	routes := routeAuthority(func(_ context.Context, tx pgx.Tx, s domain.Scope, actor string) error {
		if tx == nil || s.BindingID != "binding" || s.DeploymentRevisionID != "revision" {
			return domain.ErrDenied
		}
		return nil
	})
	authority, e := NewCurrentAuthorizer("pool", authEpoch, routes)
	if e != nil {
		t.Fatal(e)
	}
	registry, e := New(runtime, authority)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewCurrentAuthorizer("pool", authEpoch, nil); e != domain.ErrInvalid {
		t.Fatal("route bypass default")
	}
	for _, mode := range []string{"per_user", "shared", "no_shared_permission", "missing", "disabled", "revoked", "foreign_tenant", "wrong_policy", "wrong_partition", "wrong_group", "wrong_route", "wrong_epoch"} {
		t.Run(mode, func(t *testing.T) {
			partition := domain.PerUser
			if mode == "shared" || mode == "no_shared_permission" {
				partition = domain.Shared
			}
			s, read := sessionAuthorityFixture(t, fmt.Sprintf("a_%d", time.Now().UnixNano()), partition, mode)
			if e = projection.Refresh(ctx, shared.AuthorizationTarget{TenantID: s.TenantID, AccountID: s.AccountID, Provider: s.Provider}, read); e != nil {
				t.Fatal(e)
			}
			selectedRegistry := registry
			switch mode {
			case "foreign_tenant":
				s.TenantID = "other"
			case "wrong_policy":
				s.PolicyRevision++
			case "wrong_partition":
				s.Partition = domain.Shared
				s.PrincipalID = ""
			case "wrong_group":
				s.ConversationID = "other"
			case "wrong_route":
				s.BindingID = "other"
			case "wrong_epoch":
				a, _ := NewCurrentAuthorizer("pool", "22222222-2222-4222-8222-222222222222", routes)
				selectedRegistry, _ = New(runtime, a)
			}
			command := domain.Reset{CommandID: s.AccountID, ActorID: "principal", Scope: s, ExpectedGeneration: 1}
			selected, err := selectedRegistry.Reset(ctx, command)
			if mode == "per_user" || mode == "shared" {
				if err != nil || selected.Generation != 2 {
					t.Fatal("real projection denied reset", selected, err)
				}
				// Revoke the same principal without changing historical command content.
				read.read.Manifest.Generation = 2
				read.page.Generation = 2
				read.page.Principals[0].State = "REVOKED"
				read.page.Principals[0].Revision = 2
				d := wire.NewPrincipalSetDigest()
				d.Add(read.page.Principals[0])
				read.read.Manifest.PrincipalCount, read.read.Manifest.PrincipalDigest = d.Result()
				if e = projection.Refresh(ctx, shared.AuthorizationTarget{TenantID: s.TenantID, AccountID: s.AccountID, Provider: s.Provider}, read); e != nil {
					t.Fatal(e)
				}
				again, err := selectedRegistry.Reset(ctx, command)
				if err != nil || again != selected {
					t.Fatal("old result replay", err)
				}
				command.CommandID += "_new"
				command.ExpectedGeneration = 2
				if _, err = selectedRegistry.Reset(ctx, command); !errors.Is(err, domain.ErrDenied) {
					t.Fatal("revoked reset executed", err)
				}
			} else {
				if err == nil {
					t.Fatal("unverified reset executed", mode)
				}
				var n int
				if e = runtime.QueryRow(ctx, `SELECT count(*) FROM worker_conversation_commands WHERE command_id=$1`, command.CommandID).Scan(&n); e != nil || n != 0 {
					t.Fatal("denied command persisted", n, e)
				}
			}
		})
	}
	t.Log("CURRENT_SESSION_AUTHORITY=PASS real Worker projection+registry; per-user/shared permissions; revoked replay/new-command distinction; scope/reference/dependency/route negatives")
}
