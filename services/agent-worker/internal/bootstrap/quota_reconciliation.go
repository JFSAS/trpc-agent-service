package bootstrap

import (
	"context"
	"time"

	budgetpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/adapter/outbound/postgresadapter"
	budget "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	execution "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/application"
)

type quotaReconciler interface {
	Reconcile(context.Context, string, int) (budget.Reconciliation, error)
}

func (a *App) configureQuotaReconciliation() error {
	quota, err := budgetpg.NewRunQuota(a.pool, int64(a.config.Limits.MaxRetainedRuns))
	if err != nil {
		return err
	}
	a.quota, err = budgetpg.NewRunQuotaSettler(quota, a.ledger)
	if err != nil {
		return err
	}
	a.consumption, err = budgetpg.NewConsumptionSettler(a.pool, a.ledger)
	return err
}

// reconcileQuota shares the Worker lifetime and timing bounds. It never grants
// execution or reserves new budget. A failed scan conservatively leaves holds;
// each tick backs off even when work exists, bounding aggregate transaction rate.
func (a *App) reconcileQuota(ctx context.Context) {
	a.reconcileAccounting(ctx, a.quota, "quota_reconcile")
}
func (a *App) reconcileConsumption(ctx context.Context) {
	a.reconcileAccounting(ctx, a.consumption, "consumption_reconcile")
}
func (a *App) reconcileAccounting(ctx context.Context, owner quotaReconciler, operation string) {
	cursor := ""
	for ctx.Err() == nil {
		op, cancel := context.WithTimeout(ctx, a.config.Timing.OperationTimeout.Value())
		start := time.Now()
		result, err := owner.Reconcile(op, cursor, a.config.Limits.ScanBatch)
		cursor = result.Next
		if result.Scanned > 0 || err != nil {
			outcome := execution.ObservationResult(err)
			if err == nil && result.Settled == 0 && result.Waiting > 0 {
				outcome = "quota_wait"
			}
			a.observation.Observe(op, execution.Observation{Operation: operation, Result: outcome, Duration: time.Since(start)})
		}
		cancel()
		if !wait(ctx, a.config.Timing.PollInterval.Value()) {
			return
		}
	}
}
