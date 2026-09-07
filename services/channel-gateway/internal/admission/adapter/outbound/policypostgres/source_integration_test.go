package policypostgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
)

func TestPolicySourceRestartEpochAndPermanentFence(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	created := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	e, p := fixture(t, 1)
	if _, err := s.Apply(ctx, e, p); err != nil {
		t.Fatal(err)
	}
	again, _ := New(pool, "pool", testEpoch)
	if err := again.BindSource(ctx, created); err != nil {
		t.Fatal(err)
	}
	if err := again.BindSource(ctx, created.Add(time.Second)); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if err := s.BindSource(ctx, created); !errors.Is(err, ErrBlocked) {
		t.Fatal("restart cleared source block", err)
	}
	state, err := s.Apply(ctx, e, p)
	if !errors.Is(err, ErrBlocked) || state != (domain.PolicyContinuity{}) {
		t.Fatal(state, err)
	}
	if _, _, err = s.ReadExact(ctx, "tenant", "account", reference(p)); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	for _, sql := range []string{`DELETE FROM gateway_policy_sources`, `UPDATE gateway_policy_sources SET blocked_reason=''`, `UPDATE gateway_policy_sources SET source_epoch='other'`} {
		if _, err = pool.Exec(ctx, sql); err == nil {
			t.Fatal("source fence bypass", sql)
		}
	}
	newer, _ := New(pool, "pool", "22222222-2222-4222-8222-222222222222")
	if err = newer.BindSource(ctx, created); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
}
func TestPolicySourceConcurrentFirstBind(t *testing.T) {
	_, pool := setup(t)
	ctx := context.Background()
	created := time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
	s, _ := New(pool, "fresh", testEpoch)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.BindSource(ctx, created); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gateway_policy_sources WHERE scope_id='fresh'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	conflict, _ := New(pool, "conflict", testEpoch)
	results := make(chan error, 2)
	for _, stamp := range []time.Time{created, created.Add(time.Second)} {
		wg.Add(1)
		go func(stamp time.Time) { defer wg.Done(); results <- conflict.BindSource(ctx, stamp) }(stamp)
	}
	wg.Wait()
	close(results)
	success, blocked := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrBlocked) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || blocked != 1 {
		t.Fatal(success, blocked)
	}
	if err := conflict.BindSource(ctx, created); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
}
func TestPolicySourceRejectsUnboundHistoryAndUnboundWrites(t *testing.T) {
	_, pool := setup(t)
	ctx := context.Background()
	s, _ := New(pool, "unbound", testEpoch)
	e, p := fixture(t, 1)
	e.ScopeID = "unbound"
	if _, err := s.Apply(ctx, e, p); !errors.Is(err, ErrBlocked) {
		t.Fatal("unbound write accepted", err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO gateway_policy_projection_heads(scope_id,source_epoch,tenant_id,account_id,provider,policy_id) VALUES('unbound',$1,'tenant','account','wecom','policy')`, testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BindSource(ctx, time.Now().UTC()); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	var reason string
	if err = pool.QueryRow(ctx, `SELECT blocked_reason FROM gateway_policy_sources WHERE scope_id='unbound'`).Scan(&reason); err != nil || reason != "UNBOUND_HISTORY" {
		t.Fatal(reason, err)
	}
}
func TestPolicySourceBlockWaitsForWriteFence(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if err = s.guardSource(ctx, tx, true); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err = s.BindSource(bounded, time.Now().UTC()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("source update passed held shared lock", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Failed/canceled rebind did not leave a half-applied permanent block.
	e, p := fixture(t, 1)
	if _, err = s.Apply(ctx, e, p); err != nil {
		t.Fatal(err)
	}
	if err = s.BindSource(ctx, time.Now().UTC()); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, e, p); !errors.Is(err, ErrBlocked) {
		t.Fatal("write passed committed source block", err)
	}
}
