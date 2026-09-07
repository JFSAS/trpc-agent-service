package bootstrap

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	scheduler "github.com/liuzengh/trpc-agent-service/platform/channel/authorization/refresh"
	runwire "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/inbound/wire"
	ledger "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/postgresadapter"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
)

func TestAuthorizationRuntimeOptIn(t *testing.T) {
	if r, e := newAuthorizationRuntime(Config{}, nil); e != nil || r != nil {
		t.Fatal("omission must disable acquisition", e)
	}
	valid := AuthorizationConfig{URL: "https://127.0.0.1:443", ScopeID: "pool", SourceEpoch: "11111111-1111-4111-8111-111111111111"}
	if e := valid.validate(); e != nil {
		t.Fatal(e)
	}
	for _, u := range []string{"http://control", "https://user@control", "https://control/path", "https://control?", "https://control#fragment"} {
		c := valid
		c.URL = u
		if c.validate() == nil {
			t.Fatalf("accepted %s", u)
		}
	}
	for _, mutate := range []func(*AuthorizationConfig){func(c *AuthorizationConfig) { c.ScopeID = "" }, func(c *AuthorizationConfig) { c.SourceEpoch = "latest" }} {
		c := valid
		mutate(&c)
		if c.validate() == nil {
			t.Fatal("accepted unpinned source")
		}
	}
	if _, e := newAuthorizationRuntime(Config{Authorization: &valid}, nil); e == nil {
		t.Fatal("accepted missing database")
	}
	if _, e := (authorizationTargets{}).AuthorizationTargets(context.Background()); !errors.Is(e, scheduler.ErrDirectory) {
		t.Fatal(e)
	}
}

// Uses a dedicated disposable schema and real Worker migrations + Ledger.Accept.
// No network source is faked here: the read-only mTLS reader is covered by the
// actual Control RuntimeService/Postgres integration test in Control's adapter.
func TestAuthorizationTargetsPostgresAndLifecycle(t *testing.T) {
	dsn := os.Getenv("WORKER_AUTHORIZATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := fmt.Sprintf("worker_auth_targets_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(c, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	if e = migrations.Apply(ctx, pool); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../../../../api/events/execution/v1/fixtures/valid/telegram-authorized.json")
	if e != nil {
		t.Fatal(e)
	}
	req, e := runwire.Decode(raw)
	if e != nil {
		t.Fatal(e)
	}
	req.Input.ReceivedAt = time.Now().UTC()
	policy := domain.Policy{Version: "test", MaxRunAge: time.Hour, MaxReplyAge: time.Hour, MaxFutureSkew: time.Minute, LeaseTTL: 3 * time.Second, RenewalInterval: time.Second, RetryBackoff: time.Millisecond, MaxAttempts: 3}
	store := ledger.New(pool)
	for n := 0; n < 2; n++ {
		r := req
		r.EventID = fmt.Sprintf("event-%d", n)
		r.RunID = fmt.Sprintf("run-%d", n)
		r.AdmissionID = fmt.Sprintf("adm-%d", n)
		r.EventDigest = domain.Digest([]byte(r.EventID))
		r.RunDigest = domain.Digest([]byte(r.RunID))
		if _, e = store.Accept(ctx, r, policy, domain.IntakeLimits{MaxQueuedRuns: 100, MaxRetainedRuns: 1000}); e != nil {
			t.Fatal(e)
		}
	}
	directory := authorizationTargets{pool: pool, scope: req.Authorization.ScopeID}
	got, e := directory.AuthorizationTargets(ctx)
	if e != nil || len(got) != 1 {
		t.Fatal("deduplicated targets", got, e)
	}
	if got[0].Target.TenantID != req.Route.TenantID || got[0].Target.AccountID != req.Route.AccountID || got[0].Revision != req.Authorization.Generation {
		t.Fatal("persisted wire target lost", got)
	}
	foreign := authorizationTargets{pool: pool, scope: "other-scope"}
	if v, e := foreign.AuthorizationTargets(ctx); e != nil || len(v) != 0 {
		t.Fatal(v, e)
	}
	if _, e = pool.Exec(ctx, "UPDATE execution_runs SET status='FAILED'"); e != nil {
		t.Fatal(e)
	}
	if v, e := directory.AuthorizationTargets(ctx); e != nil || len(v) != 0 {
		t.Fatal("terminal target retained", v, e)
	}
	pki := newPKI(t)
	_, cert, key := pki.issue(t, "auth-reader", "spiffe://test/worker-authorization-reader", x509.ExtKeyUsageClientAuth)
	c := Config{Authorization: &AuthorizationConfig{URL: "https://127.0.0.1:1", ScopeID: req.Authorization.ScopeID, SourceEpoch: req.Authorization.SourceEpoch}}
	c.ControlTLS.CertFile = cert
	c.ControlTLS.KeyFile = key
	c.ControlTLS.CAFile = filepath.Join(pki.dir, "ca.crt")
	r, e := newAuthorizationRuntime(c, pool)
	if e != nil {
		t.Fatal(e)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- r.Run(runCtx) }()
	waitUntil(t, ctx, func() bool { return r.loop.Summary().DirectoryReady })
	stop()
	r.Close()
	r.Close()
	select {
	case e := <-done:
		if e != nil && !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("refresh lifecycle failed to drain")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, e = directory.AuthorizationTargets(canceled); !errors.Is(e, scheduler.ErrDirectory) {
		t.Fatal(e)
	}
	t.Log("WORKER_AUTH_TARGETS=PASS real migrations+Ledger.Accept; scope/dedup/terminal filters; opt-in constructor/cancel/close")
}
