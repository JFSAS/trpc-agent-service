package postgresadapter

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	budget "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

// ReadRunQuotaTerminal proves known-ended execution, not usage/cost reconciliation.
// Missing/live/failed/aborted/ambiguous attempts remain held. Budget must acquire
// its own identity/tenant locks first; this reader then follows Session -> Run.
func (l *Ledger) ReadRunQuotaTerminal(ctx context.Context, tx pgx.Tx, in budget.RunQuotaRequest) (budget.RunTerminalProof, error) {
	zero := budget.RunTerminalProof{}
	if ctx == nil || tx == nil || in.Validate() != nil {
		return zero, budget.ErrInvalid
	}
	run, _, settled, e := lockRun(ctx, tx, in.TenantID, in.RunID)
	if e != nil {
		if errors.Is(e, domain.ErrNotFound) || errors.Is(e, pgx.ErrNoRows) {
			return zero, budget.ErrNotReady
		}
		return zero, e
	}
	if run.Request.RunDigest != in.InputDigest {
		return zero, budget.ErrConflict
	}
	if (run.Status != domain.Succeeded && run.Status != domain.Failed) || settled < run.Sequence {
		return zero, budget.ErrNotReady
	}
	c, e := scanCompletion(tx.QueryRow(ctx, `SELECT `+completionColumns+` FROM execution_completions WHERE tenant_id=$1 AND run_id=$2`, in.TenantID, in.RunID))
	if e != nil {
		if errors.Is(e, domain.ErrNotFound) || errors.Is(e, pgx.ErrNoRows) {
			return zero, budget.ErrNotReady
		}
		return zero, e
	}
	if c.Status != run.Status || c.CompletedAt.IsZero() || !domain.DigestValid(c.ResultDigest) {
		return zero, budget.ErrNotReady
	}
	var attempts, succeeded int64
	if e = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE status='SUCCEEDED' AND ended_at IS NOT NULL) FROM execution_attempts WHERE tenant_id=$1 AND run_id=$2`, in.TenantID, in.RunID).Scan(&attempts, &succeeded); e != nil {
		return zero, e
	}
	if attempts != int64(run.Attempts) {
		return zero, budget.ErrNotReady
	}
	proof := budget.RunTerminalProof{TenantID: in.TenantID, RunID: in.RunID, InputDigest: in.InputDigest, CompletionID: c.CompletionID, ResultDigest: c.ResultDigest}
	if attempts == 0 && c.Kind == "SYSTEM_TERMINATION" && c.Status == domain.Failed && c.AttemptID == "" && run.CurrentAttemptID == "" {
		proof.Disposition = "NO_ATTEMPT"
		return proof, nil
	}
	if attempts != 1 || succeeded != 1 || c.Kind != "ATTEMPT" || c.Status != domain.Succeeded || c.AttemptID != run.CurrentAttemptID {
		return zero, budget.ErrNotReady
	}
	var committed bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM execution_session_commits WHERE tenant_id=$1 AND run_id=$2 AND session_id=$3 AND candidate_ref=$4 AND candidate_digest=$5)`, in.TenantID, in.RunID, run.SessionID, c.Candidate.Ref, c.Candidate.Digest).Scan(&committed); e != nil {
		return zero, e
	}
	if !committed {
		return zero, budget.ErrNotReady
	}
	proof.Disposition = "SUCCEEDED_ATTEMPT"
	return proof, nil
}
