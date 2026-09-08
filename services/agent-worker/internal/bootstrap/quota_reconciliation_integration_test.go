package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	budgetpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/adapter/outbound/postgresadapter"
	budget "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	ledgerpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/postgresadapter"
	execution "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
)

// Actual bootstrap composition and loop with real Execution/Budget owners. This
// does not start NATS, a model, IM or the full App HTTP lifecycle.
func TestQuotaReconciliationAppPostgres(t *testing.T) {
	mu, ru := os.Getenv("WORKER_TEST_MIGRATION_URL"), os.Getenv("WORKER_TEST_RUNTIME_URL")
	if mu == "" || ru == "" {
		t.Skip("requires owned Worker PostgreSQL roles")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	ledger := ledgerpg.New(pool)
	a := &App{pool: pool, ledger: ledger, config: Config{Limits: Limits{MaxRetainedRuns: 1000, ScanBatch: 1}, Timing: Timing{PollInterval: Duration(10 * time.Millisecond), OperationTimeout: Duration(time.Second)}}}
	if e := a.configureQuotaReconciliation(); e != nil || a.quota == nil {
		t.Fatal("production composition", e)
	}
	tenant := fmt.Sprintf("apploop%d", time.Now().UnixNano())
	policy := execution.Policy{Version: "app-loop", MaxRunAge: time.Hour, MaxReplyAge: time.Hour, MaxFutureSkew: time.Minute, LeaseTTL: 3 * time.Second, RenewalInterval: time.Second, RetryBackoff: time.Millisecond, MaxAttempts: 3}
	req := execution.Requested{EventID: tenant + "event", RunID: tenant + "run", AdmissionID: tenant + "admission", EventDigest: execution.Digest([]byte(tenant + "event")), RunDigest: execution.Digest([]byte(tenant)), Route: execution.Route{TenantID: tenant, Provider: "telegram", AccountID: "account", BindingID: "binding", DeploymentRevisionID: "revision", ManifestRef: "manifest", ManifestDigest: execution.Digest([]byte("manifest")), Generation: 1}, Input: execution.Input{ConversationID: "42", SenderID: "43", Text: "app loop", ReceivedAt: time.Now().UTC()}}
	doc := wire.PolicyDefinitionDocument{SchemaVersion: 1, TenantID: tenant, PolicyID: "quota", Kind: "quota", Revision: 1, Definition: wire.PolicyDefinition{Enabled: true, Quota: &wire.QuotaDefinition{MaxConcurrentRuns: 1, MaxRunsPerMinute: 10}}, PublishedBy: "owner", PublishedAt: time.Now().UTC()}
	raw, e := json.Marshal(doc)
	if e != nil {
		t.Fatal(e)
	}
	raw, e = jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	doc.Digest = "sha256:" + hex.EncodeToString(sum[:])
	quota, e := budgetpg.NewRunQuota(pool, 1000)
	if e != nil {
		t.Fatal(e)
	}
	r := budget.RunQuotaRequest{TenantID: tenant, RunID: req.RunID, InputDigest: req.RunDigest}
	if _, e = quota.ReserveRunQuota(ctx, r, doc); e != nil {
		t.Fatal(e)
	}
	if _, e = ledger.Accept(ctx, req, policy, execution.IntakeLimits{MaxQueuedRuns: 100, MaxRetainedRuns: 1000}); e != nil {
		t.Fatal(e)
	}
	loopCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); a.reconcileQuota(loopCtx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("production loop leaked")
		}
	})
	count := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM worker_run_quota_settlements WHERE run_id=$1`, r.RunID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// One deterministic real reconcile confirms the active Run is not releasable;
	// the background loop shares exactly this owner, with its own cursor/transactions.
	if result, e := a.quota.Reconcile(ctx, tenant, 1); e != nil || result.Waiting != 1 {
		t.Fatal("active Run", result, e)
	}
	if count() != 0 {
		t.Fatal("active hold released")
	}
	if finished, e := ledger.Terminalize(ctx, tenant, req.RunID, "UNSUPPORTED_MANIFEST"); e != nil || !finished {
		t.Fatal(finished, e)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for count() != 1 {
		select {
		case <-deadline.C:
			t.Fatal("background loop did not settle committed terminal Run")
		case <-ticker.C:
		}
	}
	stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop failed to drain")
	}
	next := r
	next.RunID = tenant + "next"
	if _, e = quota.ReserveRunQuota(ctx, next, doc); e != nil {
		t.Fatal("concurrency was not actually freed", e)
	}
	t.Log("APP_QUOTA_LOOP=PASS actual_composition=YES actual_terminal_release=YES next_run_reservation=PASS shutdown=PASS")
}
