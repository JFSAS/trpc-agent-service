package postgresadapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
)

type consumptionAuthorityFixture struct {
	calls            atomic.Int64
	cap              int64
	policy           string
	disabled         bool
	wrongFingerprint bool
	err              error
}

func (a *consumptionAuthorityFixture) AuthorizeConsumption(_ context.Context, _ pgx.Tx, r domain.ConsumptionRequest) (domain.ConsumptionGrant, error) {
	a.calls.Add(1)
	f, _ := r.Fingerprint()
	if a.wrongFingerprint {
		f = "sha256:" + strings.Repeat("f", 64)
	}
	return domain.ConsumptionGrant{Fingerprint: f, Enabled: !a.disabled, Cap: a.cap, Policy: domain.QuotaReference{ID: a.policy, Revision: 1, Digest: "sha256:" + strings.Repeat("a", 64)}}, a.err
}

type consumptionReaderFixture struct {
	mu     sync.Mutex
	proofs map[string]domain.ConsumptionProof
	calls  int
	err    error
}

func (f *consumptionReaderFixture) ReadConsumption(_ context.Context, _ pgx.Tx, r domain.ConsumptionRequest) (domain.ConsumptionProof, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return domain.ConsumptionProof{}, f.err
	}
	p, ok := f.proofs[r.OperationID]
	if !ok {
		return p, domain.ErrNotReady
	}
	return p, nil
}
func (f *consumptionReaderFixture) prove(r domain.ConsumptionRequest, amount int64, evidence string) {
	fingerprint, _ := r.Fingerprint()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proofs[r.OperationID] = domain.ConsumptionProof{Fingerprint: fingerprint, EvidenceID: evidence, EvidenceDigest: "sha256:" + strings.Repeat("c", 64), Amount: amount, Final: true}
}

func TestConsumptionBudgetPostgres(t *testing.T) {
	mu, ru := os.Getenv("WORKER_TEST_MIGRATION_URL"), os.Getenv("WORKER_TEST_RUNTIME_URL")
	if mu == "" || ru == "" {
		t.Skip("requires owned Worker roles")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	prefix := fmt.Sprintf("consumption%d", time.Now().UnixNano())
	authority := &consumptionAuthorityFixture{cap: 100, policy: "policy-a"}
	reader := &consumptionReaderFixture{proofs: map[string]domain.ConsumptionProof{}}
	ca, e := NewConsumption(a, 1000, authority, reader)
	if e != nil {
		t.Fatal(e)
	}
	cb, e := NewConsumption(b, 1000, authority, reader)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewConsumption(a, 1000, nil, reader); e != domain.ErrInvalid {
		t.Fatal("missing authority", e)
	}
	if _, e = NewConsumption(a, 1000, authority, nil); e != domain.ErrInvalid {
		t.Fatal("missing reader", e)
	}
	request := func(tenant, operation string, maximum int64) domain.ConsumptionRequest {
		return domain.ConsumptionRequest{TenantID: prefix + tenant, RunID: prefix + tenant + "run", OperationID: prefix + operation, InputDigest: "sha256:" + strings.Repeat("a", 64), BoundDigest: "sha256:" + strings.Repeat("b", 64), Unit: domain.ModelTokens, Maximum: maximum}
	}
	var winner domain.ConsumptionRequest
	t.Run("shared_capacity_and_replay", func(t *testing.T) {
		type answer struct {
			request     domain.ConsumptionRequest
			reservation domain.ConsumptionReservation
			err         error
		}
		answers := make(chan answer, 8)
		var wg sync.WaitGroup
		for n := 0; n < 8; n++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				c := ca
				if n%2 == 1 {
					c = cb
				}
				r := request("race", fmt.Sprint(n), 60)
				v, e := c.Reserve(ctx, r)
				answers <- answer{r, v, e}
			}(n)
		}
		wg.Wait()
		close(answers)
		success, denied := 0, 0
		var original domain.ConsumptionReservation
		for v := range answers {
			if v.err == nil {
				success++
				winner = v.request
				original = v.reservation
			} else if errors.Is(v.err, domain.ErrConsumption) {
				denied++
			} else {
				t.Fatal(v.err)
			}
		}
		if success != 1 || denied != 7 {
			t.Fatal(success, denied)
		}
		calls := authority.calls.Load()
		replay, e := cb.Reserve(ctx, winner)
		if e != nil || replay != original || authority.calls.Load() != calls {
			t.Fatal("replay changed grant/time or charged again", replay, e)
		}
		for _, field := range []string{"tenant", "run", "input", "bound", "unit", "maximum"} {
			changed := winner
			switch field {
			case "tenant":
				changed.TenantID += "foreign"
			case "run":
				changed.RunID += "other"
			case "input":
				changed.InputDigest = changed.BoundDigest
			case "bound":
				changed.BoundDigest = changed.InputDigest
			case "unit":
				changed.Unit = domain.ToolUnits
			case "maximum":
				changed.Maximum++
			}
			if _, e = ca.Reserve(ctx, changed); !errors.Is(e, domain.ErrConflict) {
				t.Fatal(field, e)
			}
			if _, e = ca.Settle(ctx, changed); !errors.Is(e, domain.ErrConflict) {
				t.Fatal("settle "+field, e)
			}
		}
		// New published policy IDs do not reset the shared cumulative ledger.
		other, e := NewConsumption(b, 1000, &consumptionAuthorityFixture{cap: 100, policy: "policy-b"}, reader)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = other.Reserve(ctx, request("race", "policy-switch", 60)); !errors.Is(e, domain.ErrConsumption) {
			t.Fatal("policy reset budget", e)
		}
		if _, e = ca.Reserve(ctx, request("isolated", "other-tenant", 100)); e != nil {
			t.Fatal("tenant isolation", e)
		}
		tool := request("race", "tool", 100)
		tool.Unit = domain.ToolUnits
		if _, e = ca.Reserve(ctx, tool); e != nil {
			t.Fatal("units mixed", e)
		}
	})
	if t.Failed() {
		return
	}
	t.Run("unknown_holds_and_final_usage", func(t *testing.T) {
		if _, e := ca.Settle(ctx, winner); !errors.Is(e, domain.ErrNotReady) {
			t.Fatal("unknown refund", e)
		}
		if _, e := ca.Reserve(ctx, request("race", "unknown-retry", 60)); !errors.Is(e, domain.ErrConsumption) {
			t.Fatal("unknown freed budget", e)
		}
		reader.prove(winner, 20, prefix+"usage-winner")
		// Settlement cannot become visible until its owner commits.
		tx, e := a.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ca.SettleInTransaction(ctx, tx, winner); e != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(e)
		}
		var count int
		if e = b.QueryRow(ctx, `SELECT count(*) FROM worker_consumption_settlements WHERE operation_id=$1`, winner.OperationID).Scan(&count); e != nil || count != 0 {
			_ = tx.Rollback(ctx)
			t.Fatal(count, e)
		}
		if e = tx.Rollback(ctx); e != nil {
			t.Fatal(e)
		}
		if _, e = ca.Reserve(ctx, request("race", "rolled-back-release", 60)); !errors.Is(e, domain.ErrConsumption) {
			t.Fatal(e)
		}
		results := make(chan domain.ConsumptionSettlement, 8)
		failures := make(chan error, 8)
		var wg sync.WaitGroup
		for n := 0; n < 8; n++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				c := ca
				if n%2 == 1 {
					c = cb
				}
				s, e := c.Settle(ctx, winner)
				results <- s
				failures <- e
			}(n)
		}
		wg.Wait()
		close(results)
		close(failures)
		for e := range failures {
			if e != nil {
				t.Fatal(e)
			}
		}
		var first domain.ConsumptionSettlement
		for s := range results {
			if s.Actual != 20 || s.Maximum != 60 || s.BoundViolated {
				t.Fatal(s)
			}
			if first.OperationID == "" {
				first = s
			} else if first != s {
				t.Fatal("non-idempotent settlement", s)
			}
		}
		if _, e = ca.Reserve(ctx, request("race", "after-usage", 80)); e != nil {
			t.Fatal("actual usage not used", e)
		}
		if _, e = ca.Reserve(ctx, request("race", "past-cap", 1)); !errors.Is(e, domain.ErrConsumption) {
			t.Fatal(e)
		}
		for _, table := range []string{"worker_consumption_reservations", "worker_consumption_settlements"} {
			for _, sql := range []string{"UPDATE " + table + " SET operation_id=operation_id WHERE operation_id=$1", "DELETE FROM " + table + " WHERE operation_id=$1"} {
				if _, e = a.Exec(ctx, sql, winner.OperationID); e == nil {
					t.Fatal("mutable consumption fact", table)
				}
			}
		}
	})
	t.Run("reservation_outer_rollback_and_saved_denial", func(t *testing.T) {
		r := request("rollback", "reservation-rollback", 100)
		tx, e := a.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ca.ReserveInTransaction(ctx, tx, r); e != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(e)
		}
		if e = tx.Rollback(ctx); e != nil {
			t.Fatal(e)
		}
		if _, e = cb.Reserve(ctx, r); e != nil {
			t.Fatal("rolled back charge remained", e)
		}
		bad := &consumptionAuthorityFixture{cap: 100, policy: "policy-a", wrongFingerprint: true}
		c, e := NewConsumption(a, 1000, bad, reader)
		if e != nil {
			t.Fatal(e)
		}
		tx, e = a.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		invalid := request("bad-proof", "bad-proof", 1)
		if _, e = c.ReserveInTransaction(ctx, tx, invalid); !errors.Is(e, domain.ErrNotReady) {
			_ = tx.Rollback(ctx)
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal("savepoint poisoned outer transaction", e)
		}
		var count int
		if e = b.QueryRow(ctx, `SELECT count(*) FROM worker_consumption_reservations WHERE operation_id=$1`, invalid.OperationID).Scan(&count); e != nil || count != 0 {
			t.Fatal(count, e)
		}
	})
	t.Run("bound_overrun_is_recorded_and_fences_unit", func(t *testing.T) {
		r := request("overrun", "overrun", 10)
		if _, e := ca.Reserve(ctx, r); e != nil {
			t.Fatal(e)
		}
		reader.prove(r, 20, prefix+"usage-overrun")
		result, e := ca.Settle(ctx, r)
		if e != nil || result.Actual != 20 || !result.BoundViolated {
			t.Fatal("overrun hidden", result, e)
		}
		if _, e = ca.Reserve(ctx, request("overrun", "after-overrun", 1)); !errors.Is(e, domain.ErrBoundViolation) {
			t.Fatal("overrun continued", e)
		}
		replay, e := cb.Settle(ctx, r)
		if e != nil || replay != result {
			t.Fatal("overrun replay", replay, e)
		}
	})
	t.Run("one_usage_evidence_cannot_settle_two_operations", func(t *testing.T) {
		first, second := request("evidence", "evidence1", 10), request("evidence", "evidence2", 10)
		for _, r := range []domain.ConsumptionRequest{first, second} {
			if _, e := ca.Reserve(ctx, r); e != nil {
				t.Fatal(e)
			}
			reader.prove(r, 0, prefix+"shared-evidence")
		}
		if s, e := ca.Settle(ctx, first); e != nil || s.Actual != 0 {
			t.Fatal("known zero", s, e)
		}
		if _, e := ca.Settle(ctx, second); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("proof reused", e)
		}
	})

	t.Run("unknown_forged_and_dependency_proofs_do_not_refund", func(t *testing.T) {
		r := request("proof-errors", "proof-errors", 100)
		if _, e := ca.Reserve(ctx, r); e != nil {
			t.Fatal(e)
		}
		fingerprint, _ := r.Fingerprint()
		reader.mu.Lock()
		reader.proofs[r.OperationID] = domain.ConsumptionProof{Fingerprint: fingerprint, EvidenceID: prefix + "not-final", EvidenceDigest: "sha256:" + strings.Repeat("c", 64), Amount: 0, Final: false}
		reader.mu.Unlock()
		if _, e := ca.Settle(ctx, r); !errors.Is(e, domain.ErrNotReady) {
			t.Fatal("non-final became zero usage", e)
		}
		reader.prove(r, 0, prefix+"forged")
		reader.mu.Lock()
		proof := reader.proofs[r.OperationID]
		proof.Fingerprint = "sha256:" + strings.Repeat("f", 64)
		reader.proofs[r.OperationID] = proof
		reader.mu.Unlock()
		if _, e := ca.Settle(ctx, r); !errors.Is(e, domain.ErrNotReady) {
			t.Fatal("wrong identity proof", e)
		}
		for _, failure := range []error{context.Canceled, context.DeadlineExceeded, errors.New("usage owner unavailable")} {
			reader.mu.Lock()
			reader.err = failure
			reader.mu.Unlock()
			_, e := ca.Settle(ctx, r)
			reader.mu.Lock()
			reader.err = nil
			reader.mu.Unlock()
			if !errors.Is(e, failure) {
				t.Fatal("lost dependency failure", e)
			}
		}
		if _, e := ca.Reserve(ctx, request("proof-errors", "no-refund", 1)); !errors.Is(e, domain.ErrConsumption) {
			t.Fatal("unknown proof freed budget", e)
		}
		badAuthority := &consumptionAuthorityFixture{cap: 100, policy: "policy-a", err: context.DeadlineExceeded}
		c, e := NewConsumption(a, 1000, badAuthority, reader)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = c.Reserve(ctx, request("authority-error", "authority-error", 1)); !errors.Is(e, context.DeadlineExceeded) {
			t.Fatal("authority error lost", e)
		}
	})
	t.Run("storage_bound_replay_and_disabled_zero", func(t *testing.T) {
		full, e := NewConsumption(a, 1, authority, reader)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = full.Reserve(ctx, winner); e != nil {
			t.Fatal("full capacity rejected replay", e)
		}
		if _, e = full.Reserve(ctx, request("storage", "storage", 1)); !errors.Is(e, domain.ErrCapacity) {
			t.Fatal(e)
		}
		for _, grant := range []*consumptionAuthorityFixture{{cap: 0, policy: "zero"}, {cap: 100, policy: "disabled", disabled: true}} {
			c, e := NewConsumption(a, 1000, grant, reader)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.Reserve(ctx, request("deny", grant.policy, 1)); !errors.Is(e, domain.ErrConsumption) {
				t.Fatal(e)
			}
		}
	})
	if t.Failed() {
		return
	}
	t.Log("CONSUMPTION_BUDGET=PASS pools=2 concurrent_reserve=1/8 unknown=HELD known_usage=CHARGED rollback=PASS replay=EXACT cross_tenant_unit=ISOLATED overrun=RECORDED_AND_FENCED evidence_reuse=DENIED")
}
