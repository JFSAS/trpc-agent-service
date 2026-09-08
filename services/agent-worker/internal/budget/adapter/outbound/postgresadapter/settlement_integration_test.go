package postgresadapter

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	ledgerpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/postgresadapter"
	execution "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunQuotaTerminalSettlementPostgres(t *testing.T) {
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
	quota, e := NewRunQuota(a, 1000)
	if e != nil {
		t.Fatal(e)
	}
	ledger := ledgerpg.New(a)
	settler, e := NewRunQuotaSettler(quota, ledger)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewRunQuotaSettler(quota, nil); e != domain.ErrInvalid {
		t.Fatal("default terminal proof", e)
	}
	policy := execution.Policy{Version: "quota-terminal", MaxRunAge: time.Hour, MaxReplyAge: time.Hour, MaxFutureSkew: time.Minute, LeaseTTL: 3 * time.Second, RenewalInterval: time.Second, RetryBackoff: time.Millisecond, MaxAttempts: 3}
	prefix := fmt.Sprintf("settle%d", time.Now().UnixNano())
	for _, mode := range []string{"no_attempt", "success", "unknown_failure", "status_only", "rate"} {
		t.Run(mode, func(t *testing.T) {
			tenant := prefix + mode
			req := execution.Requested{EventID: tenant + "event", RunID: tenant + "run", AdmissionID: tenant + "admission", EventDigest: execution.Digest([]byte(tenant + "event")), RunDigest: execution.Digest([]byte(tenant + "run")), Route: execution.Route{TenantID: tenant, Provider: "telegram", AccountID: "account", BindingID: "binding", DeploymentRevisionID: "revision", ManifestRef: "manifest", ManifestDigest: execution.Digest([]byte("manifest")), Generation: 1}, Input: execution.Input{ConversationID: "42", SenderID: "43", Text: "test", ReceivedAt: time.Now().UTC()}}
			r := domain.RunQuotaRequest{TenantID: tenant, RunID: req.RunID, InputDigest: req.RunDigest}
			rate := int64(10)
			if mode == "rate" {
				rate = 1
			}
			doc := quotaDocument(t, tenant, 1, rate)
			original, e := quota.ReserveRunQuota(ctx, r, doc)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = ledger.Accept(ctx, req, policy, execution.IntakeLimits{MaxQueuedRuns: 100, MaxRetainedRuns: 1000}); e != nil {
				t.Fatal(e)
			}
			if _, e = settler.Settle(ctx, r); !errors.Is(e, domain.ErrNotReady) {
				t.Fatal("live input released", e)
			}
			forged := r
			forged.TenantID = "foreign"
			if _, e = settler.Settle(ctx, forged); !errors.Is(e, domain.ErrConflict) {
				t.Fatal("foreign settlement", e)
			}
			forged = r
			forged.InputDigest = "sha256:" + strings.Repeat("b", 64)
			if _, e = settler.Settle(ctx, forged); !errors.Is(e, domain.ErrConflict) {
				t.Fatal("wrong input settlement", e)
			}
			if mode == "no_attempt" || mode == "rate" {
				if done, e := ledger.Terminalize(ctx, tenant, req.RunID, "UNSUPPORTED_MANIFEST"); e != nil || !done {
					t.Fatal(done, e)
				}
			} else if mode == "status_only" {
				// Negative database fault fixture: a terminal status without Completion is
				// not an Execution proof. No claim of normal completion is made here.
				if _, e = owner.Exec(ctx, `UPDATE execution_runs SET status='SUCCEEDED' WHERE tenant_id=$1 AND run_id=$2`, tenant, req.RunID); e != nil {
					t.Fatal(e)
				}
			} else {
				grant, e := ledger.Claim(ctx, execution.ClaimRequest{TenantID: tenant, RunID: req.RunID, WorkerID: "quota-test", MaxRunSeconds: 120, MaxActive: 100})
				if e != nil {
					t.Fatal(e)
				}
				if e = ledger.MarkExecuting(ctx, grant); e != nil {
					t.Fatal(e)
				}
				if mode == "unknown_failure" {
					e = ledger.FailAttempt(ctx, grant, "MODEL_RESULT_UNKNOWN", false)
				} else {
					_, e = ledger.Complete(ctx, execution.Finish{Grant: grant, Status: execution.Succeeded, FinalText: "fixture completion", Candidate: execution.Candidate{Ref: "candidate-" + req.RunID, Digest: execution.Digest([]byte("candidate")), Parent: grant.Parent}})
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			next := r
			next.RunID += "next"
			if mode == "unknown_failure" || mode == "status_only" {
				if _, e = settler.Settle(ctx, r); !errors.Is(e, domain.ErrNotReady) {
					t.Fatal("uncertain terminal freed concurrency", e)
				}
				if _, e = quota.ReserveRunQuota(ctx, next, doc); !errors.Is(e, domain.ErrQuota) {
					t.Fatal("uncertain hold disappeared", e)
				}
				return
			}
			// Release is part of the owner's transaction, not a side effect before commit.
			tx, e := a.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = settler.SettleInTransaction(ctx, tx, r); e != nil {
				rollback(tx)
				t.Fatal(e)
			}
			var n int
			if e = b.QueryRow(ctx, `SELECT count(*) FROM worker_run_quota_settlements WHERE run_id=$1`, r.RunID).Scan(&n); e != nil || n != 0 {
				rollback(tx)
				t.Fatal("settlement committed early", n, e)
			}
			if e = tx.Rollback(ctx); e != nil {
				t.Fatal(e)
			}
			if _, e = quota.ReserveRunQuota(ctx, next, doc); !errors.Is(e, domain.ErrQuota) {
				t.Fatal("rolled back release persisted", e)
			}
			type answer struct {
				result domain.RunQuotaSettlement
				err    error
			}
			answers := make(chan answer, 8)
			var wg sync.WaitGroup
			for k := 0; k < 8; k++ {
				wg.Add(1)
				go func() { defer wg.Done(); v, e := settler.Settle(ctx, r); answers <- answer{v, e} }()
			}
			wg.Wait()
			close(answers)
			var first domain.RunQuotaSettlement
			for v := range answers {
				if v.err != nil {
					t.Fatal(v.err)
				}
				if first.RunID == "" {
					first = v.result
				} else if first != v.result {
					t.Fatal("settlement replay changed", first, v.result)
				}
			}
			expected := "NO_ATTEMPT"
			if mode == "success" {
				expected = "SUCCEEDED_ATTEMPT"
			}
			if first.Disposition != expected {
				t.Fatal(first)
			}
			if e = a.QueryRow(ctx, `SELECT count(*) FROM worker_run_quota_settlements WHERE run_id=$1`, r.RunID).Scan(&n); e != nil || n != 1 {
				t.Fatal("duplicate release", n, e)
			}
			replay, e := quota.ReserveRunQuota(ctx, r, doc)
			if e != nil || replay.Fingerprint != original.Fingerprint || !replay.ReservedAt.Equal(original.ReservedAt) {
				t.Fatal("old reservation reactivated", replay, e)
			}
			_, e = quota.ReserveRunQuota(ctx, next, doc)
			if mode == "rate" {
				if !errors.Is(e, domain.ErrQuota) {
					t.Fatal("settlement refunded minute charge", e)
				}
			} else {
				if e != nil {
					t.Fatal("known-ended concurrency not released", e)
				}
			}
			for _, sql := range []string{`UPDATE worker_run_quota_settlements SET released_at=clock_timestamp() WHERE run_id=$1`, `DELETE FROM worker_run_quota_settlements WHERE run_id=$1`} {
				if _, e = a.Exec(ctx, sql, r.RunID); e == nil {
					t.Fatal("settlement mutable")
				}
			}
		})
	}
	if t.Failed() {
		return
	}
	t.Log("RUN_QUOTA_SETTLEMENT=PASS real Execution terminal reader; no-attempt and committed-success release; active/unknown/fault hold; eight concurrent replays; rollback; minute charges preserved (ledger fixtures, no model/storage execution)")
}
