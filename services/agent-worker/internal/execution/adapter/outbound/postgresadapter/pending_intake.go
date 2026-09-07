package postgresadapter

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	wire "github.com/liuzengh/trpc-agent-service/api/events/execution/v1"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

// StageAuthorization persists an unassigned input and its first policy/deadline.
// Success means PENDING, never an Execution Receipt, Run, Session or broker ACK.
// The caller must authenticate the event before domain decoding and staging.
func (l *Ledger) StageAuthorization(ctx context.Context, req domain.Requested, policy domain.Policy, limits domain.IntakeLimits) error {
	if ctx == nil || req.Validate() != nil || req.Authorization == nil || policy.Validate() != nil || limits.Validate() != nil {
		return domain.ErrInvalid
	}
	tx, e := l.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer rollback(tx)
	keys := []string{"event:" + req.EventID, "run:" + req.RunID, "admission:" + req.AdmissionID}
	sort.Strings(keys)
	for _, key := range keys {
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731004284))`, key); e != nil {
			return e
		}
	}
	raw, e := json.Marshal(req)
	if e != nil || len(raw) > wire.MaxRunRequestedBytes || len(req.Input.Text) > wire.MaxInputTextBytes {
		return domain.ErrInvalid
	}
	var same bool
	e = tx.QueryRow(ctx, `SELECT request_json=$2::jsonb FROM execution_pending_intakes WHERE event_id=$1`, req.EventID, raw).Scan(&same)
	if e == nil {
		if !same {
			return domain.ErrConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	// Existing accepted identities must go through normal receipt replay, not be
	// relabelled as a new pending input. No accepted identity is silently replaced.
	var accepted bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM execution_receipts WHERE event_id=$1) OR EXISTS(SELECT 1 FROM execution_runs WHERE run_id=$2 OR admission_id=$3)`, req.EventID, req.RunID, req.AdmissionID).Scan(&accepted); e != nil {
		return e
	}
	if accepted {
		return domain.ErrConflict
	}
	var conflict bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM execution_pending_intakes WHERE (run_id=$1 OR admission_id=$2) AND (run_id<>$1 OR admission_id<>$2 OR run_digest<>$3 OR tenant_id<>$4))`, req.RunID, req.AdmissionID, req.RunDigest, req.Route.TenantID).Scan(&conflict); e != nil {
		return e
	}
	if conflict {
		return domain.ErrConflict
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(731004285)`); e != nil {
		return e
	}
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return e
	}
	deadline := req.Input.ReceivedAt.Add(policy.MaxRunAge)
	if !now.Before(deadline) || req.Input.ReceivedAt.After(now.Add(policy.MaxFutureSkew)) {
		return domain.ErrInvalid
	}
	var queued, retained int64
	if e = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM execution_runs WHERE status IN ('QUEUED','RUNNING','RETRY_WAIT'))+(SELECT count(*) FROM execution_pending_intakes WHERE expires_at>$1),(SELECT count(*) FROM execution_runs)+(SELECT count(*) FROM execution_pending_intakes)`, now).Scan(&queued, &retained); e != nil {
		return e
	}
	if queued >= int64(limits.MaxQueuedRuns) || retained >= int64(limits.MaxRetainedRuns) {
		return domain.ErrCapacity
	}
	p, _ := json.Marshal(policy)
	a := req.Authorization
	if _, e = tx.Exec(ctx, `INSERT INTO execution_pending_intakes(event_id,event_digest,run_id,admission_id,run_digest,tenant_id,scope_id,source_epoch,account_id,provider,generation,request_json,policy_json,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, req.EventID, req.EventDigest, req.RunID, req.AdmissionID, req.RunDigest, req.Route.TenantID, a.ScopeID, a.SourceEpoch, req.Route.AccountID, req.Route.Provider, a.Generation, raw, p, deadline); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
