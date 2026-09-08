package postgresadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5/pgxpool"
	policywire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	protocol "github.com/liuzengh/trpc-agent-service/api/schemas/deployment/v1"
	shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
	budget "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	runwire "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/inbound/wire"
	execapp "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/application"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	manifestwire "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/manifest/adapter/inbound/wire"
	manifestpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/manifest/adapter/outbound/postgresadapter"
	session "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
	"os"
	"testing"
	"time"
)

func TestPendingModelBudgetPostgres(t *testing.T) {
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
	req.EventID = "pending-budget-event"
	req.RunID = "pending-budget-run"
	req.AdmissionID = "pending-budget-admission"
	req.Route.DeploymentRevisionID = publication.DeploymentRevisionID
	req.Route.ManifestRef = publication.ManifestID
	req.Route.ManifestDigest = publication.ContentDigest
	req.Input.ReceivedAt = time.Now().UTC().Truncate(time.Microsecond).Add(123 * time.Nanosecond)
	req.Authorization.ScopeID = "model-budget-pool"
	read := workerAuthorizationFixture(t, req, 3, "ACTIVE", 1000)
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
	s := session.Scope{TenantID: req.Route.TenantID, Provider: req.Route.Provider, AccountID: req.Route.AccountID, ConversationID: req.Input.ConversationID, ConversationKind: req.Authorization.ConversationKind, ThreadID: req.Input.ThreadID, BindingID: req.Route.BindingID, DeploymentRevisionID: req.Route.DeploymentRevisionID, PolicyID: read.read.Dependencies.Session.PolicyID, PolicyRevision: 1, PolicyDigest: read.read.Dependencies.Session.Digest, Partition: session.PerUser, PrincipalID: req.Authorization.PrincipalID}
	check := func(scope session.Scope, actor string) (PendingModelBudget, error) {
		tx, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer rollback(tx)
		return route.ReadModelBudget(ctx, tx, scope, actor)
	}
	if _, e = check(s, s.PrincipalID); !errors.Is(e, execapp.ErrManifestMissing) {
		t.Fatal("missing target granted budget", e)
	}
	if e = manifestpg.New(pool).Apply(ctx, publication, 100); e != nil {
		t.Fatal(e)
	}
	cap, e := check(s, s.PrincipalID)
	if e != nil || cap.Cap != 1000 || cap.Policy.Digest != read.read.Dependencies.Quota.Digest || cap.TenantID != s.TenantID {
		t.Fatal("actual snapshot cap", cap, e)
	}
	for _, field := range []string{"actor", "tenant", "session", "binding"} {
		changed := s
		actor := s.PrincipalID
		switch field {
		case "actor":
			actor = "other"
		case "tenant":
			changed.TenantID = "other"
		case "session":
			changed.PolicyDigest = domain.Digest([]byte("other"))
		case "binding":
			changed.BindingID = "other"
		}
		out, e := check(changed, actor)
		if e == nil || out != (PendingModelBudget{}) {
			t.Fatal("substituted scope", field, out, e)
		}
	}
	// A newer independently installed policy is authoritative, not the admitted cap.
	for index, value := range []int64{0, 2000} {
		fresh := workerAuthorizationFixture(t, req, int64(4+index), "ACTIVE", value)
		fresh.read.Policy.Revision = req.Authorization.PolicyRevision + int64(1+index)
		signPendingBudgetPolicy(t, &fresh)
		if e = projection.Refresh(ctx, target, fresh); e != nil {
			t.Fatal(e)
		}
		out, e := check(s, s.PrincipalID)
		if e != nil || out.Cap != value {
			t.Fatal("newest cap ignored", out, e)
		}
	}
	revoked := workerAuthorizationFixture(t, req, 6, "REVOKED", 2000)
	revoked.read.Policy.Revision = req.Authorization.PolicyRevision + 3
	signPendingBudgetPolicy(t, &revoked)
	if e = projection.Refresh(ctx, target, revoked); e != nil {
		t.Fatal(e)
	}
	if out, e := check(s, s.PrincipalID); e == nil || out != (PendingModelBudget{}) {
		t.Fatal("revoked principal read grant", out, e)
	}
	missing := workerAuthorizationFixture(t, req, 7, "ACTIVE")
	missing.read.Policy.Revision = req.Authorization.PolicyRevision + 4
	signPendingBudgetPolicy(t, &missing)
	if e = projection.Refresh(ctx, target, missing); e != nil {
		t.Fatal(e)
	}
	if out, e := check(s, s.PrincipalID); !errors.Is(e, budget.ErrNotReady) || out != (PendingModelBudget{}) {
		t.Fatal("absent cap treated as unlimited", out, e)
	}
	fresh := workerAuthorizationFixture(t, req, 8, "ACTIVE", 2000)
	fresh.read.Policy.Revision = req.Authorization.PolicyRevision + 5
	fresh.read.Policy.Body.AuthorizationMaxAgeMS = 1000
	fresh.read.Manifest.AuthorizationMaxAgeMS = 1000
	signPendingBudgetPolicy(t, &fresh)
	if e = projection.Refresh(ctx, target, fresh); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `SELECT pg_sleep(1.1)`); e != nil {
		t.Fatal(e)
	}
	if out, e := check(s, s.PrincipalID); e == nil || out != (PendingModelBudget{}) {
		t.Fatal("expired snapshot", out, e)
	}
	var runs, attempts int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM execution_runs WHERE run_id=$1),(SELECT count(*) FROM execution_attempts WHERE run_id=$1)`, req.RunID).Scan(&runs, &attempts); e != nil || runs != 0 || attempts != 0 {
		t.Fatal("budget read allocated execution", runs, attempts, e)
	}
	t.Log("CURRENT_MODEL_BUDGET=PASS pending=REAL projection=REAL manifest=REAL source_reader=FIXTURE cap=1000 newer=2000 zero=PRESERVED revoked=DENIED absent=HELD stale=DENIED runs=0 attempts=0")
}
func signPendingBudgetPolicy(t *testing.T, r *workerAuthorizationReader) {
	t.Helper()
	r.read.Dependencies.Quota.Revision = r.read.Policy.Revision
	signAuthorizationDefinition(t, &r.read.Dependencies.Quota)
	r.read.Policy.Body.TenantQuota = policywire.PolicyReference{ID: r.read.Dependencies.Quota.PolicyID, Revision: r.read.Dependencies.Quota.Revision, Digest: r.read.Dependencies.Quota.Digest}
	r.read.Policy.Digest = ""
	raw, _ := json.Marshal(r.read.Policy)
	raw, e := jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	r.read.Policy.Digest = "sha256:" + hex.EncodeToString(sum[:])
	r.read.Manifest.Policy = policywire.PolicyReference{ID: r.read.Policy.PolicyID, Revision: r.read.Policy.Revision, Digest: r.read.Policy.Digest}
}
