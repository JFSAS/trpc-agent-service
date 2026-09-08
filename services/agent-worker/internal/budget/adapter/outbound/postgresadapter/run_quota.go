// Package postgresadapter owns shared Worker run-count reservations.
package postgresadapter

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	"time"
)

type RunQuota struct {
	pool        *pgxpool.Pool
	maxRetained int64
}

func NewRunQuota(pool *pgxpool.Pool, maxRetained int64) (*RunQuota, error) {
	if pool == nil || maxRetained < 1 || maxRetained > 9007199254740991 {
		return nil, domain.ErrInvalid
	}
	return &RunQuota{pool, maxRetained}, nil
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// ReserveRunQuota owns a transaction for standalone owner use. Intake composition
// must use ReserveRunQuotaInTransaction so quota and Run facts commit together.
// This is NOT the full PendingRunReservation/model-cost contract.
func (q *RunQuota) ReserveRunQuota(ctx context.Context, r domain.RunQuotaRequest, doc wire.PolicyDefinitionDocument) (domain.RunQuotaReservation, error) {
	if ctx == nil {
		return domain.RunQuotaReservation{}, domain.ErrInvalid
	}
	tx, e := q.pool.Begin(ctx)
	if e != nil {
		return domain.RunQuotaReservation{}, e
	}
	defer rollback(tx)
	out, e := q.ReserveRunQuotaInTransaction(ctx, tx, r, doc)
	if e != nil {
		return domain.RunQuotaReservation{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return domain.RunQuotaReservation{}, e
	}
	return out, nil
}

// ReserveRunQuotaInTransaction checks a complete immutable Quota document, not a
// caller-provided pair of limits. The caller separately proves that this exact
// document is CURRENT for the authorized input. Counters are tenant-wide across
// policy IDs/revisions; changing policy cannot reset prior consumption.
// Zero means zero capacity, never unlimited. Reservations do not expire/refund:
// the terminal-settlement owner must prove release; unknown work stays held.
func (q *RunQuota) ReserveRunQuotaInTransaction(ctx context.Context, tx pgx.Tx, r domain.RunQuotaRequest, doc wire.PolicyDefinitionDocument) (domain.RunQuotaReservation, error) {
	zero := domain.RunQuotaReservation{}
	if ctx == nil || tx == nil || r.Validate() != nil {
		return zero, domain.ErrInvalid
	}
	raw, e := json.Marshal(doc)
	if e != nil {
		return zero, domain.ErrInvalid
	}
	verified, e := wire.DecodePolicyDefinitionDocument(raw)
	if e != nil || verified.Kind != "quota" || verified.TenantID != r.TenantID || verified.Definition.Quota == nil {
		return zero, domain.ErrInvalid
	}
	ref := domain.QuotaReference{ID: verified.PolicyID, Revision: verified.Revision, Digest: verified.Digest}
	fingerprint, e := r.Fingerprint(ref)
	if e != nil {
		return zero, e
	}
	save, e := tx.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer rollback(save)
	// Global Run identity precedes tenant and global storage locks, across all
	// instances/transactions. No network call or per-process counter is involved.
	if _, e = save.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731004288))`, r.RunID); e != nil {
		return zero, e
	}
	var old domain.RunQuotaReservation
	e = save.QueryRow(ctx, `SELECT tenant_id,run_id,fingerprint,quota_id,quota_revision,quota_digest,reserved_at FROM worker_run_quota_reservations WHERE run_id=$1`, r.RunID).Scan(&old.TenantID, &old.RunID, &old.Fingerprint, &old.Quota.ID, &old.Quota.Revision, &old.Quota.Digest, &old.ReservedAt)
	if e == nil {
		if old.Fingerprint != fingerprint || old.TenantID != r.TenantID || old.Quota != ref {
			return zero, domain.ErrConflict
		}
		if e = save.Commit(ctx); e != nil {
			return zero, e
		}
		return old, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return zero, e
	}
	limits := verified.Definition.Quota
	if !verified.Definition.Enabled || limits.MaxConcurrentRuns == 0 || limits.MaxRunsPerMinute == 0 {
		return zero, domain.ErrQuota
	}
	if _, e = save.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731004289))`, r.TenantID); e != nil {
		return zero, e
	}
	if _, e = save.Exec(ctx, `SELECT pg_advisory_xact_lock(731004290)`); e != nil {
		return zero, e
	}
	var now time.Time
	if e = save.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return zero, e
	}
	var held, recent, retained int64
	if e = save.QueryRow(ctx, `SELECT count(*) FILTER(WHERE tenant_id=$1 AND NOT EXISTS(SELECT 1 FROM worker_run_quota_settlements s WHERE s.run_id=r.run_id)),count(*) FILTER(WHERE tenant_id=$1 AND reserved_at>$2::timestamptz-interval '1 minute'),count(*) FROM worker_run_quota_reservations r`, r.TenantID, now).Scan(&held, &recent, &retained); e != nil {
		return zero, e
	}
	if held >= limits.MaxConcurrentRuns || recent >= limits.MaxRunsPerMinute {
		return zero, domain.ErrQuota
	}
	if retained >= q.maxRetained {
		return zero, domain.ErrCapacity
	}
	if _, e = save.Exec(ctx, `INSERT INTO worker_run_quota_reservations(run_id,tenant_id,input_digest,fingerprint,quota_id,quota_revision,quota_digest,quota_jsonb,reserved_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, r.RunID, r.TenantID, r.InputDigest, fingerprint, ref.ID, ref.Revision, ref.Digest, raw, now); e != nil {
		return zero, e
	}
	out := domain.RunQuotaReservation{TenantID: r.TenantID, RunID: r.RunID, Fingerprint: fingerprint, Quota: ref, ReservedAt: now}
	if e = save.Commit(ctx); e != nil {
		return zero, e
	}
	return out, nil
}
