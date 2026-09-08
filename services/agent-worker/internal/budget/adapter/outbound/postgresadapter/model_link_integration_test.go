package postgresadapter

import (
	"context"
	"errors"
	"fmt"
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

func TestRealModelConsumptionLinkPostgres(t *testing.T) {
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
	authority := &consumptionAuthorityFixture{cap: 100, policy: "fixture-policy"}
	budget, e := NewConsumption(a, 1000, authority, ledger)
	if e != nil {
		t.Fatal(e)
	}
	second, e := NewConsumption(b, 1000, authority, ledgerpg.New(b))
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
	if err := ledger.BindModelReservation(ctx, tx, g, r, budget); !errors.Is(err, domain.ErrNotReady) {
		_ = tx.Rollback(ctx)
		t.Fatal("missing reservation", err)
	}
	_ = tx.Rollback(ctx)
	// Reserve+link are one transaction; outer rollback restores both owners.
	for _, commit := range []bool{false, true} {
		tx, e = a.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = budget.ReserveInTransaction(ctx, tx, r); e != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(e)
		}
		changed := r
		changed.Maximum++
		if e = ledger.BindModelReservation(ctx, tx, g, changed, budget); !errors.Is(e, domain.ErrConflict) {
			_ = tx.Rollback(ctx)
			t.Fatal("forged maximum", e)
		}
		if e = ledger.BindModelReservation(ctx, tx, g, r, budget); e != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(e)
		}
		// Exact linkage replay is a receipt, not another model dispatch.
		if e = ledger.BindModelReservation(ctx, tx, g, r, budget); e != nil {
			_ = tx.Rollback(ctx)
			t.Fatal("exact link replay", e)
		}
		var n int
		if e = b.QueryRow(ctx, `SELECT count(*) FROM execution_model_reservation_links WHERE operation_id=$1`, r.OperationID).Scan(&n); e != nil || n != 0 {
			_ = tx.Rollback(ctx)
			t.Fatal("uncommitted link visible", n, e)
		}
		if commit {
			e = tx.Commit(ctx)
		} else {
			e = tx.Rollback(ctx)
		}
		if e != nil {
			t.Fatal(e)
		}
		if !commit {
			for _, table := range []string{"worker_consumption_reservations", "execution_model_reservation_links"} {
				var left int
				if e = b.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE operation_id=$1", r.OperationID).Scan(&left); e != nil || left != 0 {
					t.Fatal("outer rollback retained fact", table, left, e)
				}
			}
		}
	}
	if _, e = budget.Settle(ctx, r); !errors.Is(e, domain.ErrNotReady) {
		t.Fatal("missing usage refunded", e)
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
	out, e := second.Settle(ctx, r)
	if e != nil || out.Actual != 20 || out.BoundViolated {
		t.Fatal("real usage not settled", out, e)
	}
	replay, e := budget.Settle(ctx, r)
	if e != nil || replay != out {
		t.Fatal("replay changed settlement", replay, e)
	}
	verifyTx, err := a.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = budget.VerifyUnsettledReservation(ctx, verifyTx, r)
	_ = verifyTx.Rollback(ctx)
	if !errors.Is(err, domain.ErrNotReady) {
		t.Fatal("settled reservation accepted for binding", err)
	}
	next := r
	next.OperationID += "-next"
	next.Maximum = 80
	if _, e = budget.Reserve(ctx, next); e != nil {
		t.Fatal("actual charge not applied", e)
	}
	for _, field := range []string{"bound", "input", "maximum", "unit"} {
		changed := r
		switch field {
		case "bound":
			changed.BoundDigest = "sha256:" + strings.Repeat("c", 64)
		case "input":
			changed.InputDigest = changed.BoundDigest
		case "maximum":
			changed.Maximum++
		case "unit":
			changed.Unit = domain.ToolUnits
		}

		readTx, readErr := a.Begin(ctx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		_, readErr = ledger.ReadConsumption(ctx, readTx, changed)
		_ = readTx.Rollback(ctx)
		want := domain.ErrConflict
		if field == "unit" {
			want = domain.ErrInvalid
		}
		if !errors.Is(readErr, want) {
			t.Fatal("Execution reader accepted substituted proof", field, readErr)
		}
		if _, err := budget.Settle(ctx, changed); !errors.Is(err, domain.ErrConflict) {
			t.Fatal(field, err)
		}
	}
	// Once execution starts, a caller cannot attach a reservation retroactively.
	late, lateReq := makeGrant("late")
	if _, e = budget.Reserve(ctx, lateReq); e != nil {
		t.Fatal(e)
	}
	if e = ledger.MarkExecuting(ctx, late); e != nil {
		t.Fatal(e)
	}
	tx, e = a.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = ledger.BindModelReservation(ctx, tx, late, lateReq, budget); !errors.Is(e, execution.ErrFenced) {
		_ = tx.Rollback(ctx)
		t.Fatal("late binding accepted", e)
	}
	_ = tx.Rollback(ctx)
	if e = ledger.RecordModelUsage(ctx, late, execution.RuntimeResult{UsageKnown: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = budget.Settle(ctx, lateReq); !errors.Is(e, domain.ErrNotReady) {
		t.Fatal("unlinked zero refunded", e)
	}
	if _, e = a.Exec(ctx, `DELETE FROM execution_model_reservation_links WHERE operation_id=$1`, r.OperationID); e == nil {
		t.Fatal("mutable link")
	}
	t.Log("REAL_CONSUMPTION_LINK=PASS authority=FIXTURE reservation_verifier=REAL usage_reader=REAL rollback=ATOMIC actual=20 next_reservation=80 late_binding=DENIED unlinked_zero=HELD")
}
