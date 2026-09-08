package postgresadapter

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	sessionpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/adapter/outbound/postgres"
	session "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/domain"
	"strings"
	"testing"
	"time"
)

type reservationFixture func(context.Context, pgx.Tx, domain.Requested, domain.Policy) error

func (f reservationFixture) ReservePendingRun(c context.Context, tx pgx.Tx, r domain.Requested, p domain.Policy) error {
	return f(c, tx, r, p)
}

// This fixture proves reservation-port participation/rollback, not actual quota
// accounting or bounded model-cost calculation. No production budget is installed.
func exercisePendingRunWriter(t *testing.T, ctx context.Context, owner, pool *pgxpool.Pool, store *Ledger, req domain.Requested, s session.Scope, policy domain.Policy, target PendingTargetVerifier) {
	t.Helper()
	if _, e := owner.Exec(ctx, `CREATE TABLE IF NOT EXISTS pending_run_budget_fixture(run_id text PRIMARY KEY)`); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"no_budget", "budget_denied", "wrong_selection", "not_consumed", "outer_rollback", "commit"} {
		t.Run("writer_"+mode, func(t *testing.T) {
			input := req
			input.EventID = "writer-event-" + mode
			input.RunID = "writer-run-" + mode
			input.AdmissionID = "writer-admission-" + mode
			input.EventDigest = domain.Digest([]byte(input.EventID))
			input.RunDigest = domain.Digest([]byte(input.RunID))
			if _, e := NewIntake(pool).Accept(ctx, input, policy, domain.IntakeLimits{MaxQueuedRuns: 100, MaxRetainedRuns: 1000}); !errors.Is(e, domain.ErrNotReady) {
				t.Fatal(e)
			}
			proof, e := store.PendingRouteAuthority(input.EventID, input.Authorization.ScopeID, input.Authorization.SourceEpoch, target)
			if e != nil {
				t.Fatal(e)
			}
			auth, e := sessionpg.NewCurrentAuthorizer(input.Authorization.ScopeID, input.Authorization.SourceEpoch, proof)
			if e != nil {
				t.Fatal(e)
			}
			registry, e := sessionpg.New(pool, auth)
			if e != nil {
				t.Fatal(e)
			}
			var before int
			if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM execution_runs)+(SELECT count(*) FROM execution_pending_intakes)`).Scan(&before); e != nil {
				t.Fatal(e)
			}
			limits := domain.IntakeLimits{MaxQueuedRuns: before, MaxRetainedRuns: before}
			tx, e := pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer rollback(tx)
			var selected session.Selection
			var receipt domain.Receipt
			var budget PendingRunReservation = reservationFixture(func(c context.Context, tx pgx.Tx, r domain.Requested, p domain.Policy) error {
				if r.RunID != input.RunID || p != policy {
					t.Fatal("reservation lost original input/policy")
				}
				if _, e := tx.Exec(c, `INSERT INTO pending_run_budget_fixture(run_id) VALUES($1)`, r.RunID); e != nil {
					return e
				}
				if mode == "budget_denied" {
					return domain.ErrCapacity
				}
				return nil
			})
			if mode == "no_budget" {
				budget = nil
			}
			e = registry.WithCurrentInTransaction(ctx, tx, s, s.PrincipalID, func(c context.Context, tx pgx.Tx, v session.Selection) error {
				selected = v
				if mode == "wrong_selection" {
					v.Generation++
				}
				var err error
				receipt, err = proof.WriteRunInTransaction(c, tx, s, v, limits, budget)
				return err
			})
			if mode == "no_budget" || mode == "budget_denied" || mode == "wrong_selection" {
				if e == nil {
					t.Fatal("unproved Run written")
				}
				wantError := domain.ErrInvalid
				if mode == "budget_denied" {
					wantError = domain.ErrCapacity
				}
				if !errors.Is(e, wantError) {
					t.Fatal("wrong rejection boundary", mode, e)
				}
				if e = tx.Commit(ctx); e != nil {
					t.Fatal(e)
				} // failed savepoints must prevent leaks
			} else {
				if e != nil {
					t.Fatal("write real pending Run", e)
				}
				if receipt.SessionID == input.SessionID() || receipt.SessionID != selected.SessionID {
					t.Fatal("legacy hash selected", receipt, selected)
				}
				if mode == "not_consumed" {
					if e = tx.Commit(ctx); e == nil || !strings.Contains(e.Error(), "PARTITIONED_RUN_PENDING_NOT_CONSUMED") {
						t.Fatal("Run committed with pending still present or wrong failure", e)
					}
				} else {
					forged := receipt
					forged.RunID = "other"
					if e = proof.ConsumeRunInTransaction(ctx, tx, s, selected, forged); e == nil {
						t.Fatal("forged receipt consumed input")
					}
					if e = proof.ConsumeRunInTransaction(ctx, tx, s, selected, receipt); e != nil {
						t.Fatal(e)
					}
					if mode == "outer_rollback" {
						e = tx.Rollback(ctx)
					} else {
						e = tx.Commit(ctx)
					}
					if e != nil {
						t.Fatal(e)
					}
				}
			}
			want := 0
			if mode == "commit" {
				want = 1
			}
			var runs, reservations, pending, after int
			if e = pool.QueryRow(ctx, `SELECT count(*) FROM execution_runs WHERE run_id=$1`, input.RunID).Scan(&runs); e != nil {
				t.Fatal(e)
			}
			if e = pool.QueryRow(ctx, `SELECT count(*) FROM pending_run_budget_fixture WHERE run_id=$1`, input.RunID).Scan(&reservations); e != nil {
				t.Fatal(e)
			}
			if e = pool.QueryRow(ctx, `SELECT count(*) FROM execution_pending_intakes WHERE event_id=$1`, input.EventID).Scan(&pending); e != nil {
				t.Fatal(e)
			}
			if runs != want || reservations != want || pending != 1-want {
				t.Fatal("promotion atomicity", runs, reservations, pending, mode)
			}
			if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM execution_runs)+(SELECT count(*) FROM execution_pending_intakes)`).Scan(&after); e != nil || after != before {
				t.Fatal("promotion double-counted retained input", before, after, e)
			}
			if mode == "commit" {
				got, e := store.FindRun(ctx, input.Route.TenantID, input.RunID)
				if e != nil || got.SessionID != selected.SessionID || !got.RunDeadline.Equal(input.Input.ReceivedAt.Add(policy.MaxRunAge).Truncate(time.Microsecond)) {
					t.Fatal("persisted Run identity/deadline", got, e)
				}
				replay, e := NewIntake(pool).Accept(ctx, input, policy, domain.IntakeLimits{MaxQueuedRuns: 1, MaxRetainedRuns: 1})
				if e != nil || replay != receipt {
					t.Fatal("promoted receipt replay", replay, e)
				}
				var key string
				var generation int64
				if e = pool.QueryRow(ctx, `SELECT scope_key,generation FROM execution_session_partitions WHERE tenant_id=$1 AND session_id=$2`, s.TenantID, selected.SessionID).Scan(&key, &generation); e != nil || key != selected.ScopeKey || generation != selected.Generation {
					t.Fatal("partition fact lost", key, generation, e)
				}
				if _, e = pool.Exec(ctx, `UPDATE execution_session_partitions SET generation=generation+1 WHERE tenant_id=$1 AND session_id=$2`, s.TenantID, selected.SessionID); e == nil {
					t.Fatal("partition identity mutable")
				}
				if _, e = store.Claim(ctx, domain.ClaimRequest{TenantID: s.TenantID, RunID: input.RunID, WorkerID: "test", MaxRunSeconds: 120, MaxActive: 1}); e == nil {
					t.Fatal("writer bypassed execution gates")
				}
				var attempts int
				if e = pool.QueryRow(ctx, `SELECT count(*) FROM execution_attempts WHERE run_id=$1`, input.RunID).Scan(&attempts); e != nil || attempts != 0 {
					t.Fatal("held authorization allocated Attempt", attempts, e)
				}
			}
		})
	}
	t.Log("POLICY_RUN_WRITER=PASS real partitioned Run/Receipt/link; mandatory reservation port; deferred pending consumption; rollback/commit/replay; retained capacity replacement; zero Attempts (budget is transaction fixture, not production quota)")
}
