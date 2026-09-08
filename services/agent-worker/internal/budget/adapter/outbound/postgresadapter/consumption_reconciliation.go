package postgresadapter

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
)

// ConsumptionSettler exposes only accounting recovery, never reservation or
// dispatch authority. Recovery needs immutable usage, not a fabricated current
// policy grant; production can construct it before new-call admission is enabled.
type ConsumptionSettler struct {
	pool   *pgxpool.Pool
	reader ConsumptionReader
}

func NewConsumptionSettler(pool *pgxpool.Pool, reader ConsumptionReader) (*ConsumptionSettler, error) {
	if pool == nil || reader == nil {
		return nil, domain.ErrInvalid
	}
	return &ConsumptionSettler{pool: pool, reader: reader}, nil
}

// Reconcile visits one keyset page. An unknown or faulty early reservation never
// pins the scan to the first page. Empty tail wraps to the beginning on the next
// tick, including late inserts with keys below the cursor. Restarting at the
// beginning is harmless: Settle verifies facts and replays immutable settlement.
// Each candidate owns a separate bounded transaction; no network I/O occurs there.
func (s *ConsumptionSettler) Reconcile(ctx context.Context, after string, limit int) (domain.Reconciliation, error) {
	out := domain.Reconciliation{Next: after}
	if ctx == nil || domain.ValidateReconciliation(after, limit) != nil {
		return out, domain.ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `SELECT r.tenant_id,r.run_id,r.operation_id,r.input_digest,r.bound_digest,r.unit,r.maximum FROM worker_consumption_reservations r WHERE r.operation_id>$1 AND NOT EXISTS(SELECT 1 FROM worker_consumption_settlements s WHERE s.operation_id=r.operation_id) ORDER BY r.operation_id LIMIT $2`, after, limit)
	if err != nil {
		return out, err
	}
	candidates := make([]domain.ConsumptionRequest, 0, limit)
	for rows.Next() {
		var r domain.ConsumptionRequest
		if err = rows.Scan(&r.TenantID, &r.RunID, &r.OperationID, &r.InputDigest, &r.BoundDigest, &r.Unit, &r.Maximum); err != nil {
			rows.Close()
			return out, err
		}
		candidates = append(candidates, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(candidates) == 0 {
		out.Next = ""
		return out, nil
	}
	var firstError error
	for _, r := range candidates {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		_, err = s.Settle(ctx, r)
		out.Scanned++
		out.Next = r.OperationID
		switch {
		case err == nil:
			out.Settled++
		case errors.Is(err, domain.ErrNotReady):
			out.Waiting++
		default:
			out.Failed++
			if firstError == nil {
				firstError = err
			}
		}
	}
	return out, firstError
}
