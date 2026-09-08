package postgresadapter

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	policywire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	protocol "github.com/liuzengh/trpc-agent-service/api/schemas/deployment/v1"
	shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
	runwire "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/inbound/wire"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/manifestadapter"
	execapp "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/application"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	manifestwire "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/manifest/adapter/inbound/wire"
	manifestpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/manifest/adapter/outbound/postgresadapter"
	manifestdomain "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/manifest/domain"
	sessionpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/adapter/outbound/postgres"
	session "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type actualPendingTarget struct{ contract string }

func (a actualPendingTarget) VerifyPendingTarget(c context.Context, tx pgx.Tx, r domain.Route) error {
	reader := manifestadapter.Reader{Projection: manifestpg.NewTransactionReader(tx), ContractDigest: a.contract}
	_, e := reader.Resolve(c, r)
	return e
}
func pendingFixture(t *testing.T, path string) []byte {
	t.Helper()
	dir, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	for {
		if _, e = os.Stat(filepath.Join(dir, "go.mod")); e == nil {
			break
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("root missing")
		}
		dir = next
	}
	b, e := os.ReadFile(filepath.Join(dir, path))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestPendingRouteAuthorityPostgres(t *testing.T) {
	mu, ru := os.Getenv("WORKER_TEST_MIGRATION_URL"), os.Getenv("WORKER_TEST_RUNTIME_URL")
	if mu == "" || ru == "" {
		t.Skip("requires owned Worker roles")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	open := func(url string) *pgxpool.Pool {
		p, e := pgxpool.New(ctx, url)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(p.Close)
		return p
	}
	owner, pool := open(mu), open(ru)
	if e := migrations.ApplyForRuntime(ctx, owner, "worker_runtime"); e != nil {
		t.Fatal(e)
	}
	req, e := runwire.Decode(pendingFixture(t, "api/events/execution/v1/fixtures/valid/telegram-authorized.json"))
	if e != nil {
		t.Fatal(e)
	}
	publication, e := manifestwire.Decode(pendingFixture(t, "api/events/control/v1/examples/valid/runtime-manifest-worker-v1.json"))
	if e != nil {
		t.Fatal(e)
	}
	envelope, e := protocol.DecodeRuntimeManifest(publication.Envelope)
	if e != nil {
		t.Fatal(e)
	}
	content, e := protocol.VerifyRuntimeManifest(envelope)
	if e != nil {
		t.Fatal(e)
	}
	req.EventID = "pending-route-event"
	req.RunID = "pending-route-run"
	req.AdmissionID = "pending-route-admission"
	req.Route.DeploymentRevisionID = publication.DeploymentRevisionID
	req.Route.ManifestRef = publication.ManifestID
	req.Route.ManifestDigest = publication.ContentDigest
	req.Input.ReceivedAt = time.Now().UTC().Truncate(time.Microsecond).Add(123 * time.Nanosecond)
	read := workerAuthorizationFixture(t, req, 3, "ACTIVE")
	req.Authorization.PolicyDigest = read.read.Policy.Digest
	store := New(pool)
	projection, e := NewAuthorizationProjection(pool, req.Authorization.ScopeID, req.Authorization.SourceEpoch)
	if e != nil {
		t.Fatal(e)
	}
	target := shared.AuthorizationTarget{TenantID: req.Route.TenantID, AccountID: req.Route.AccountID, Provider: req.Route.Provider}
	if e = projection.Refresh(ctx, target, read); e != nil {
		t.Fatal(e)
	}
	policy := domain.Policy{Version: "pending-route", MaxRunAge: time.Hour, MaxReplyAge: time.Hour, MaxFutureSkew: time.Minute, LeaseTTL: 3 * time.Second, RenewalInterval: time.Second, RetryBackoff: time.Millisecond, MaxAttempts: 3}
	if _, e = NewIntake(pool).Accept(ctx, req, policy, domain.IntakeLimits{MaxQueuedRuns: 100, MaxRetainedRuns: 1000}); !errors.Is(e, domain.ErrNotReady) {
		t.Fatal(e)
	}
	verifier := actualPendingTarget{content.PlatformContract.Digest}
	route, e := store.PendingRouteAuthority(req.EventID, req.Authorization.ScopeID, req.Authorization.SourceEpoch, verifier)
	if e != nil {
		t.Fatal(e)
	}
	authority, e := sessionpg.NewCurrentAuthorizer(req.Authorization.ScopeID, req.Authorization.SourceEpoch, route)
	if e != nil {
		t.Fatal(e)
	}
	s := session.Scope{TenantID: req.Route.TenantID, Provider: req.Route.Provider, AccountID: req.Route.AccountID, ConversationID: req.Input.ConversationID, ConversationKind: req.Authorization.ConversationKind, ThreadID: req.Input.ThreadID, BindingID: req.Route.BindingID, DeploymentRevisionID: req.Route.DeploymentRevisionID, PolicyID: read.read.Dependencies.Session.PolicyID, PolicyRevision: 1, PolicyDigest: read.read.Dependencies.Session.Digest, Partition: session.PerUser, PrincipalID: req.Authorization.PrincipalID}
	check := func(scope session.Scope, actor, op string) error {
		tx, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer rollback(tx)
		return authority.AuthorizeSession(ctx, tx, scope, actor, op)
	}
	if e = check(s, s.PrincipalID, "message.send"); !errors.Is(e, execapp.ErrManifestMissing) {
		t.Fatal("missing target granted", e)
	}
	manifests := manifestpg.New(pool)
	if e = manifests.Apply(ctx, publication, 100); e != nil {
		t.Fatal(e)
	}
	if e = check(s, s.PrincipalID, "message.send"); e != nil {
		t.Fatal("real pending target denied", e)
	}
	for _, source := range []struct{ event, scope, epoch string }{
		{"absent-pending", req.Authorization.ScopeID, req.Authorization.SourceEpoch},
		{req.EventID, "foreign-pool", req.Authorization.SourceEpoch},
		{req.EventID, req.Authorization.ScopeID, "22222222-2222-4222-8222-222222222222"},
	} {
		bound, err := store.PendingRouteAuthority(source.event, source.scope, source.epoch, verifier)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		err = bound.AuthorizeSessionRoute(ctx, tx, s, s.PrincipalID, "message.send")
		rollback(tx)
		if err == nil {
			t.Fatal("foreign/missing pending proof granted")
		}
	}
	for _, field := range []string{"tenant", "account", "binding", "revision", "conversation", "kind", "thread", "actor", "operation", "policy"} {
		t.Run(field, func(t *testing.T) {
			bad := s
			actor, op := s.PrincipalID, "message.send"
			switch field {
			case "tenant":
				bad.TenantID = "other"
			case "account":
				bad.AccountID = "other"
			case "binding":
				bad.BindingID = "other"
			case "revision":
				bad.DeploymentRevisionID = "other"
			case "conversation":
				bad.ConversationID = "other"
			case "kind":
				bad.ConversationKind = "group"
			case "thread":
				bad.ThreadID = "other"
			case "actor":
				actor = "other"
			case "operation":
				op = "session.new"
			case "policy":
				bad.PolicyDigest = domain.Digest([]byte("other"))
			}
			if e := check(bad, actor, op); e == nil {
				t.Fatal("caller substituted pending route")
			}
		})
	}
	registry, e := sessionpg.New(pool, authority)
	if e != nil {
		t.Fatal(e)
	}
	// The real pending proof remains available through the registry's final
	// authorization check. The intake owner can consume it afterward, and an
	// outer rollback restores it along with registry selection. No Run promotion
	// is claimed: this transaction deliberately rolls back the deletion.
	outer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.WithCurrentInTransaction(ctx, outer, s, s.PrincipalID, func(context.Context, pgx.Tx, session.Selection) error { return nil }); err != nil {
		rollback(outer)
		t.Fatal(err)
	}
	deleted, err := outer.Exec(ctx, `DELETE FROM execution_pending_intakes WHERE event_id=$1`, req.EventID)
	if err != nil || deleted.RowsAffected() != 1 {
		rollback(outer)
		t.Fatal("consume after final proof", deleted, err)
	}
	if err = outer.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var restored int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM execution_pending_intakes WHERE event_id=$1`, req.EventID).Scan(&restored); err != nil || restored != 1 {
		t.Fatal("pending consumption escaped rollback", restored, err)
	}
	t.Log("PENDING_OWNER_TX=PASS real pending proof precedes consumption; outer rollback restores pending and selection")
	if e = registry.WithCurrent(ctx, s, s.PrincipalID, func(_ context.Context, _ pgx.Tx, selection session.Selection) error {
		if selection.Generation != 1 {
			t.Fatal(selection)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"execution_runs", "execution_sessions", "execution_receipts"} {
		var n int
		if e = pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil || n != 0 {
			t.Fatal("proof allocated execution", table, n, e)
		}
	}
	exercisePendingRunWriter(t, ctx, owner, pool, store, req, s, policy, verifier)
	remapped := workerAuthorizationFixture(t, req, 4, "ACTIVE")
	remapped.page.Principals[0].ExternalUserID = "99"
	digest := policywire.NewPrincipalSetDigest()
	if e = digest.Add(remapped.page.Principals[0]); e != nil {
		t.Fatal(e)
	}
	remapped.read.Manifest.PrincipalCount, remapped.read.Manifest.PrincipalDigest = digest.Result()
	if e = projection.Refresh(ctx, target, remapped); e != nil {
		t.Fatal(e)
	}
	if e = check(s, s.PrincipalID, "message.send"); e == nil {
		t.Fatal("historical external identity mapping granted")
	}
	revoked := workerAuthorizationFixture(t, req, 5, "REVOKED")
	if e = projection.Refresh(ctx, target, revoked); e != nil {
		t.Fatal(e)
	}
	if e = check(s, s.PrincipalID, "message.send"); e == nil {
		t.Fatal("revoked pending principal granted")
	}
	active := workerAuthorizationFixture(t, req, 6, "ACTIVE")
	if e = projection.Refresh(ctx, target, active); e != nil {
		t.Fatal(e)
	}
	// Conflict publication must wait until the target-check transaction releases its
	// shared identity lock; it cannot poison the manifest between proof and commit.
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = verifier.VerifyPendingTarget(ctx, tx, req.Route); e != nil {
		rollback(tx)
		t.Fatal(e)
	}
	collision := publication
	collision.EventID = "manifest-pending-conflict"
	collision.EventDigest = domain.Digest([]byte(collision.EventID))
	collision.Envelope = append(append([]byte{}, publication.Envelope...), byte(' '))
	collision.EnvelopeDigest = domain.Digest(collision.Envelope)
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	e = manifests.Apply(short, collision, 100)
	stop()
	rollback(tx)
	if e == nil || errors.Is(e, manifestdomain.ErrConflict) {
		t.Fatal("publication crossed transaction proof lock", e)
	}
	if e = manifests.Apply(ctx, collision, 100); !errors.Is(e, manifestdomain.ErrConflict) {
		t.Fatal("conflict not recorded after unlock", e)
	}
	if e = check(s, s.PrincipalID, "message.send"); !errors.Is(e, execapp.ErrManifestInvalid) {
		t.Fatal("conflicted target granted", e)
	}
	t.Log("PENDING_ROUTE_AUTHORITY=PASS real pending+current projection+manifest+registry; scope actor operation substitutions denied; revoked denied; target conflict transaction lock; initial proof zero execution facts; separate writer cases verified")
}
