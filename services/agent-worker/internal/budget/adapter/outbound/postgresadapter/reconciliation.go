package postgresadapter

import (
	"context"
	"errors"

	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
)

// Reconcile visits one keyset page. An unknown or faulty early reservation never
// pins the scan to the first page. Empty tail wraps to the beginning on the next
// tick, including late inserts with keys below the cursor. Restarting at the
// beginning is harmless: Settle verifies facts and replays immutable settlement.
// Each candidate owns a separate bounded transaction; no network I/O occurs there.
func (s *RunQuotaSettler) Reconcile(ctx context.Context, after string, limit int) (domain.Reconciliation, error) {
	out := domain.Reconciliation{Next: after}
	if ctx == nil || domain.ValidateReconciliation(after, limit) != nil {
		return out, domain.ErrInvalid
	}
	rows, err := s.quota.pool.Query(ctx, `SELECT r.tenant_id,r.run_id,r.input_digest FROM worker_run_quota_reservations r WHERE r.run_id>$1 AND NOT EXISTS(SELECT 1 FROM worker_run_quota_settlements s WHERE s.run_id=r.run_id) ORDER BY r.run_id LIMIT $2`, after, limit)
	if err != nil {
		return out, err
	}
	candidates := make([]domain.RunQuotaRequest, 0, limit)
	for rows.Next() {
		var r domain.RunQuotaRequest
		if err = rows.Scan(&r.TenantID, &r.RunID, &r.InputDigest); err != nil {
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
		out.Next = r.RunID
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
