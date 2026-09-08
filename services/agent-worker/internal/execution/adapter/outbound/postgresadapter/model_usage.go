package postgresadapter

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

// RecordModelUsage commits before Session Stage. Exact authenticated replay can
// survive lease expiry, but new facts require a live EXECUTING Attempt. This does
// not release any consumption reservation or accept unknown/default-zero usage.
func (l *Ledger) RecordModelUsage(ctx context.Context, g domain.Grant, result domain.RuntimeResult) error {
	if ctx == nil {
		return domain.ErrInvalid
	}
	digest, err := domain.ModelUsageDigest(g, result)
	if err != nil {
		return err
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	run, _, _, err := lockRun(ctx, tx, g.Run.Request.Route.TenantID, g.Run.Request.RunID)
	if err != nil {
		return err
	}
	if run.Request.RunDigest != g.Run.Request.RunDigest || run.Request.Route.ManifestDigest != g.Run.Request.Route.ManifestDigest {
		return domain.ErrFenced
	}
	// Authenticate immutable Attempt identity even for a historical replay. No
	// credential value is written to usage facts or returned in a diagnostic.
	var worker, token, status, runID, parentRef, parentDigest string
	var epoch, generation int64
	err = tx.QueryRow(ctx, `SELECT worker_id,token_hash,status,run_id,lease_epoch,generation,parent_ref,parent_digest FROM execution_attempts WHERE tenant_id=$1 AND attempt_id=$2 FOR UPDATE`, run.Request.Route.TenantID, g.AttemptID).Scan(&worker, &token, &status, &runID, &epoch, &generation, &parentRef, &parentDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrFenced
	}
	if err != nil {
		return err
	}
	if g.Token == "" || worker != g.WorkerID || token != domain.Digest([]byte(g.Token)) || runID != run.Request.RunID || epoch != g.LeaseEpoch || generation != g.Generation || parentRef != g.Parent.Ref || parentDigest != g.Parent.Digest {
		return domain.ErrFenced
	}
	operation := domain.ModelOperationID(g)
	var old string
	err = tx.QueryRow(ctx, `SELECT result_digest FROM execution_model_usage WHERE operation_id=$1`, operation).Scan(&old)
	if err == nil {
		if old != digest {
			return domain.ErrConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if status != "EXECUTING" {
		return domain.ErrFenced
	}
	if _, _, _, err = fenced(ctx, tx, g); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO execution_model_usage(operation_id,tenant_id,run_id,attempt_id,input_tokens,output_tokens,total_tokens,result_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, operation, run.Request.Route.TenantID, run.Request.RunID, g.AttemptID, result.InputTokens, result.OutputTokens, result.TotalTokens, digest)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
