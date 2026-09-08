package postgresadapter

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
)

// TerminalReader must lock and verify the Execution owner's committed terminal
// facts in this transaction. The budget owner never queries Execution tables.
type TerminalReader interface {
	ReadRunQuotaTerminal(context.Context, pgx.Tx, domain.RunQuotaRequest) (domain.RunTerminalProof, error)
}
type RunQuotaSettler struct {
	quota    *RunQuota
	terminal TerminalReader
}

func NewRunQuotaSettler(q *RunQuota, reader TerminalReader) (*RunQuotaSettler, error) {
	if q == nil || reader == nil {
		return nil, domain.ErrInvalid
	}
	return &RunQuotaSettler{q, reader}, nil
}
func (s *RunQuotaSettler) Settle(ctx context.Context, r domain.RunQuotaRequest) (domain.RunQuotaSettlement, error) {
	zero := domain.RunQuotaSettlement{}
	if ctx == nil {
		return zero, domain.ErrInvalid
	}
	tx, e := s.quota.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer rollback(tx)
	out, e := s.SettleInTransaction(ctx, tx, r)
	if e != nil {
		return zero, e
	}
	if e = tx.Commit(ctx); e != nil {
		return zero, e
	}
	return out, nil
}

// SettleInTransaction acquires budget identity/tenant locks BEFORE the Execution
// reader's Session/Run locks, matching reservation -> Run insertion ordering.
// Call after Execution completion commits; do not invoke from an already-locked
// Execution completion transaction. Only concurrent occupancy is released: the
// immutable minute-window and retained-history charge remain unchanged.
func (s *RunQuotaSettler) SettleInTransaction(ctx context.Context, tx pgx.Tx, r domain.RunQuotaRequest) (domain.RunQuotaSettlement, error) {
	zero := domain.RunQuotaSettlement{}
	if ctx == nil || tx == nil || r.Validate() != nil {
		return zero, domain.ErrInvalid
	}
	save, e := tx.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer rollback(save)
	if _, e = save.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731004288))`, r.RunID); e != nil {
		return zero, e
	}
	var tenant, input, fingerprint string
	if e = save.QueryRow(ctx, `SELECT tenant_id,input_digest,fingerprint FROM worker_run_quota_reservations WHERE run_id=$1`, r.RunID).Scan(&tenant, &input, &fingerprint); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return zero, domain.ErrNotReady
		}
		return zero, e
	}
	if tenant != r.TenantID || input != r.InputDigest {
		return zero, domain.ErrConflict
	}
	var old domain.RunQuotaSettlement
	e = save.QueryRow(ctx, `SELECT tenant_id,run_id,reservation_fingerprint,completion_id,result_digest,disposition,released_at FROM worker_run_quota_settlements WHERE run_id=$1`, r.RunID).Scan(&old.TenantID, &old.RunID, &old.Fingerprint, &old.CompletionID, &old.ResultDigest, &old.Disposition, &old.ReleasedAt)
	if e == nil {
		if old.Fingerprint != fingerprint || old.TenantID != tenant {
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
	if _, e = save.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731004289))`, tenant); e != nil {
		return zero, e
	}
	proof, e := s.terminal.ReadRunQuotaTerminal(ctx, save, r)
	if e != nil {
		return zero, e
	}
	if proof.Validate() != nil || proof.TenantID != r.TenantID || proof.RunID != r.RunID || proof.InputDigest != r.InputDigest {
		return zero, domain.ErrNotReady
	}
	out := domain.RunQuotaSettlement{TenantID: tenant, RunID: r.RunID, Fingerprint: fingerprint, CompletionID: proof.CompletionID, ResultDigest: proof.ResultDigest, Disposition: proof.Disposition}
	if e = save.QueryRow(ctx, `INSERT INTO worker_run_quota_settlements(run_id,tenant_id,reservation_fingerprint,completion_id,result_digest,disposition) VALUES($1,$2,$3,$4,$5,$6) RETURNING released_at`, r.RunID, tenant, fingerprint, proof.CompletionID, proof.ResultDigest, proof.Disposition).Scan(&out.ReleasedAt); e != nil {
		return zero, e
	}
	if e = save.Commit(ctx); e != nil {
		return zero, e
	}
	return out, nil
}
