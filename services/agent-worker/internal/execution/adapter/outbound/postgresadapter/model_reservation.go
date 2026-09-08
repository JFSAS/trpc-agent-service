package postgresadapter

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	budget "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

type ModelReservationVerifier interface {
	VerifyUnsettledReservation(context.Context, pgx.Tx, budget.ConsumptionRequest) error
}

// BindModelReservation must run after reservation in the same owner transaction,
// before the external model call. It takes Budget identity lock before Execution
// Session/Run locks and leaves outer commit to the caller. No default proof exists.
func (l *Ledger) BindModelReservation(ctx context.Context, tx pgx.Tx, g domain.Grant, r budget.ConsumptionRequest, verifier ModelReservationVerifier) error {
	if ctx == nil || tx == nil || verifier == nil || r.Validate() != nil || r.Unit != budget.ModelTokens || r.TenantID != g.Run.Request.Route.TenantID || r.RunID != g.Run.Request.RunID || r.OperationID != domain.ModelOperationID(g) || r.InputDigest != g.Run.Request.RunDigest {
		return budget.ErrInvalid
	}
	save, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(save)
	if err = verifier.VerifyUnsettledReservation(ctx, save, r); err != nil {
		return err
	}
	run, _, _, err := fenced(ctx, save, g)
	if err != nil {
		return err
	}
	if run.Request.RunDigest != r.InputDigest {
		return budget.ErrConflict
	}
	fingerprint, _ := r.Fingerprint()
	var old string
	err = save.QueryRow(ctx, `SELECT reservation_fingerprint FROM execution_model_reservation_links WHERE operation_id=$1`, r.OperationID).Scan(&old)
	if err == nil {
		if old != fingerprint {
			return budget.ErrConflict
		}
		return save.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var status string
	if err = save.QueryRow(ctx, `SELECT status FROM execution_attempts WHERE tenant_id=$1 AND attempt_id=$2`, r.TenantID, g.AttemptID).Scan(&status); err != nil {
		return err
	}
	if status != "PREPARING" {
		return domain.ErrFenced
	}
	var afterUsage bool
	if err = save.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM execution_model_usage WHERE tenant_id=$1 AND attempt_id=$2)`, r.TenantID, g.AttemptID).Scan(&afterUsage); err != nil {
		return err
	}
	if afterUsage {
		return budget.ErrNotReady
	}
	_, err = save.Exec(ctx, `INSERT INTO execution_model_reservation_links(operation_id,tenant_id,run_id,attempt_id,reservation_fingerprint,input_digest,bound_digest,maximum) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, r.OperationID, r.TenantID, r.RunID, g.AttemptID, fingerprint, r.InputDigest, r.BoundDigest, r.Maximum)
	if err != nil {
		return err
	}
	return save.Commit(ctx)
}

// ReadConsumption implements Budget's final-usage port using only Execution
// tables. The immutable pre-call link binds the reservation; an ended/failed Run
// does not erase known model consumption. Unknown usage never generates a proof.
func (l *Ledger) ReadConsumption(ctx context.Context, tx pgx.Tx, r budget.ConsumptionRequest) (budget.ConsumptionProof, error) {
	var zero budget.ConsumptionProof
	if ctx == nil || tx == nil || r.Validate() != nil || r.Unit != budget.ModelTokens {
		return zero, budget.ErrInvalid
	}
	run, _, _, err := lockRun(ctx, tx, r.TenantID, r.RunID)
	if errors.Is(err, domain.ErrNotFound) {
		return zero, budget.ErrNotReady
	}
	if err != nil {
		return zero, err
	}
	if run.Request.RunDigest != r.InputDigest {
		return zero, budget.ErrConflict
	}
	var attempt, fingerprint, input, bound, digest, parentRef, parentDigest string
	var maximum int64
	var usage domain.RuntimeResult
	err = tx.QueryRow(ctx, `SELECT l.attempt_id,l.reservation_fingerprint,l.input_digest,l.bound_digest,l.maximum,u.result_digest,u.input_tokens,u.output_tokens,u.total_tokens,a.parent_ref,a.parent_digest FROM execution_model_reservation_links l JOIN execution_model_usage u ON u.operation_id=l.operation_id AND u.tenant_id=l.tenant_id AND u.run_id=l.run_id AND u.attempt_id=l.attempt_id JOIN execution_attempts a ON a.tenant_id=l.tenant_id AND a.attempt_id=l.attempt_id AND a.run_id=l.run_id WHERE l.operation_id=$1 AND l.tenant_id=$2 AND l.run_id=$3`, r.OperationID, r.TenantID, r.RunID).Scan(&attempt, &fingerprint, &input, &bound, &maximum, &digest, &usage.InputTokens, &usage.OutputTokens, &usage.TotalTokens, &parentRef, &parentDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, budget.ErrNotReady
	}
	if err != nil {
		return zero, err
	}
	expected, _ := r.Fingerprint()
	if fingerprint != expected || input != r.InputDigest || bound != r.BoundDigest || maximum != r.Maximum {
		return zero, budget.ErrConflict
	}
	grant := domain.Grant{Run: run, AttemptID: attempt, Parent: domain.Head{Ref: parentRef, Digest: parentDigest}}
	usage.UsageKnown = true
	recomputed, err := domain.ModelUsageDigest(grant, usage)
	if err != nil || recomputed != digest || domain.ModelOperationID(grant) != r.OperationID {
		return zero, budget.ErrNotReady
	}
	// Explicit framing binds the accounting proof to both immutable owner facts.
	b, _ := json.Marshal([]string{"model-consumption-proof-v1", expected, digest})
	return budget.ConsumptionProof{Fingerprint: expected, EvidenceID: domain.StableID("usage", r.OperationID), EvidenceDigest: domain.Digest(b), Final: true, Amount: usage.TotalTokens}, nil
}
