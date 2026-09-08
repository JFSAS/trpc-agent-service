package bootstrap

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	budgetpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/adapter/outbound/postgresadapter"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	ledgerpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/postgresadapter"
	execution "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
)

func TestConsumptionReconciliationAppPostgres(t *testing.T) {
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
	owner, a, b := open(mu), open(ru), open(ru)
	if e := migrations.ApplyForRuntime(ctx, owner, "worker_runtime"); e != nil {
		t.Fatal(e)
	}
	ledger := ledgerpg.New(a)
	// Only published-policy/bound authority remains a fixture. Usage reader and
	// reservation verifier are the actual Execution and Budget owners.
	authority := &consumptionLoopAuthority{}
	budget, e := budgetpg.NewConsumption(a, 1000, authority, ledger)
	if e != nil {
		t.Fatal(e)
	}
	prefix := fmt.Sprintf("model-link%d", time.Now().UnixNano())
	policy := execution.Policy{Version: "model-link", MaxRunAge: time.Hour, MaxReplyAge: time.Hour, MaxFutureSkew: time.Minute, LeaseTTL: 3 * time.Second, RenewalInterval: time.Second, RetryBackoff: time.Millisecond, MaxAttempts: 3}
	makeGrant := func(suffix string) (execution.Grant, domain.ConsumptionRequest) {
		t.Helper()
		tenant := prefix + suffix
		req := execution.Requested{EventID: tenant + "event", RunID: tenant + "run", AdmissionID: tenant + "admission", EventDigest: execution.Digest([]byte(tenant + "event")), RunDigest: execution.Digest([]byte(tenant)), Route: execution.Route{TenantID: tenant, Provider: "telegram", AccountID: "account", BindingID: "binding", DeploymentRevisionID: "revision", ManifestRef: "manifest", ManifestDigest: execution.Digest([]byte("manifest")), Generation: 1}, Input: execution.Input{ConversationID: "42", SenderID: "43", Text: "fixture", ReceivedAt: time.Now().UTC()}}
		if _, err := ledger.Accept(ctx, req, policy, execution.IntakeLimits{MaxQueuedRuns: 100, MaxRetainedRuns: 1000}); err != nil {
			t.Fatal(err)
		}
		g, err := ledger.Claim(ctx, execution.ClaimRequest{TenantID: tenant, RunID: req.RunID, WorkerID: "link-test", MaxRunSeconds: 120, MaxActive: 100})
		if err != nil {
			t.Fatal(err)
		}
		return g, domain.ConsumptionRequest{TenantID: tenant, RunID: req.RunID, OperationID: execution.ModelOperationID(g), InputDigest: req.RunDigest, BoundDigest: "sha256:" + strings.Repeat("b", 64), Unit: domain.ModelTokens, Maximum: 60}
	}
	g, r := makeGrant("good")
	tx, e := a.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = budget.ReserveInTransaction(ctx, tx, r); e != nil {
		t.Fatal(e)
	}
	if e = ledger.BindModelReservation(ctx, tx, g, r, budget); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	// A permanently unknown early key must not starve a later known operation.
	unknown := r
	unknown.OperationID = "a-" + prefix
	unknown.TenantID = prefix + "unknown"
	if _, e = budget.Reserve(ctx, unknown); e != nil {
		t.Fatal(e)
	}
	app := &App{pool: a, ledger: ledger, config: Config{Limits: Limits{MaxRetainedRuns: 1000, ScanBatch: 1}, Timing: Timing{PollInterval: Duration(5 * time.Millisecond), OperationTimeout: Duration(time.Second)}}}
	if e = app.configureQuotaReconciliation(); e != nil || app.consumption == nil {
		t.Fatal("production composition", e)
	}
	// The real owner sees no usage yet, even though reservation and link exist.
	initial, e := app.consumption.Reconcile(ctx, "", 1000)
	// Other package fixtures can contain tool-unit holds unsupported by the
	// model reader. Their per-row errors must not prevent scanning our holds.
	if initial.Waiting < 2 || (e != nil && initial.Failed == 0) {
		t.Fatal("unknown usage released", initial, e)
	}
	var premature int
	if e = b.QueryRow(ctx, `SELECT count(*) FROM worker_consumption_settlements WHERE operation_id=ANY($1::text[])`, []string{r.OperationID, unknown.OperationID}).Scan(&premature); e != nil || premature != 0 {
		t.Fatal("premature settlement", premature, e)
	}
	if e = ledger.MarkExecuting(ctx, g); e != nil {
		t.Fatal(e)
	}
	if e = ledger.RecordModelUsage(ctx, g, execution.RuntimeResult{UsageKnown: true, InputTokens: 12, OutputTokens: 8, TotalTokens: 20}); e != nil {
		t.Fatal(e)
	}
	if e = ledger.FailAttempt(ctx, g, "SESSION_STAGE_FAILED", false); e != nil {
		t.Fatal(e)
	}
	loopCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); app.reconcileConsumption(loopCtx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("consumption loop leaked")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		e = b.QueryRow(ctx, `SELECT count(*) FROM worker_consumption_settlements WHERE operation_id=$1 AND actual=20`, r.OperationID).Scan(&count)
		if e != nil {
			t.Fatal(e)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("managed reconciliation did not settle real usage")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	<-done
	// Fresh cursor after restart rechecks unknown facts but never duplicates usage.
	result, e := app.consumption.Reconcile(ctx, "", 1000)
	if result.Waiting < 1 || (e != nil && result.Failed == 0) {
		t.Fatal("restart scan", result, e)
	}
	var count int
	if e = b.QueryRow(ctx, `SELECT count(*) FROM worker_consumption_settlements WHERE operation_id=$1`, unknown.OperationID).Scan(&count); e != nil || count != 0 {
		t.Fatal("unknown refunded", count, e)
	}
	next := r
	next.OperationID += "-next"
	next.Maximum = 80
	if _, e = budget.Reserve(ctx, next); e != nil {
		t.Fatal("known usage not released", e)
	}
	t.Log("CONSUMPTION_APP_RECONCILIATION=PASS reservation_authority=FIXTURE usage_reader=REAL managed_loop=REAL actual=20 unknown=HELD restart=IDEMPOTENT")
}

type consumptionLoopAuthority struct{}

func (*consumptionLoopAuthority) AuthorizeConsumption(_ context.Context, _ pgx.Tx, r domain.ConsumptionRequest) (domain.ConsumptionGrant, error) {
	f, _ := r.Fingerprint()
	return domain.ConsumptionGrant{Fingerprint: f, Enabled: true, Cap: 100, Policy: domain.QuotaReference{ID: "fixture-policy", Revision: 1, Digest: "sha256:" + strings.Repeat("a", 64)}}, nil
}
