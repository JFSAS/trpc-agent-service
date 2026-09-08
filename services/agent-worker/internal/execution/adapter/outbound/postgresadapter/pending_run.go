package postgresadapter

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	session "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/domain"
	"sort"
	"time"
)

// PendingRunReservation must atomically reserve the bounded run cost through the
// budget owner, using the stable RunID, in the supplied transaction. Missing or
// unknown reservation is an error, not a default grant. No production implementation
// is supplied yet, so production cannot select this writer to bypass budget gates.
type PendingRunReservation interface {
	ReservePendingRun(context.Context, pgx.Tx, domain.Requested, domain.Policy) error
}

// WriteRunInTransaction writes a policy-bound Run and provisional Receipt inside
// the caller's Registry transaction. It neither consumes pending nor commits the
// outer transaction; the caller must complete final proof and guarded consumption.
// The budget owner must not commit/retain tx or perform external I/O.
func (p *PendingRouteAuthority) WriteRunInTransaction(ctx context.Context, tx pgx.Tx, s session.Scope, selected session.Selection, limits domain.IntakeLimits, budget PendingRunReservation) (domain.Receipt, error) {
	zero := domain.Receipt{}
	if ctx == nil || tx == nil || budget == nil || limits.Validate() != nil || s.Validate() != nil {
		return zero, domain.ErrInvalid
	}
	key, e := s.Key()
	if e != nil {
		return zero, e
	}
	id, e := s.SessionID(selected.Generation)
	if e != nil || selected.ScopeKey != key || selected.SessionID != id {
		return zero, domain.ErrInvalid
	}
	nested, e := tx.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer rollback(nested)
	var raw, policyRaw []byte
	var expires, now time.Time
	if e = nested.QueryRow(ctx, `SELECT request_json,policy_json,expires_at FROM execution_pending_intakes WHERE event_id=$1`, p.event).Scan(&raw, &policyRaw, &expires); e != nil {
		return zero, domain.ErrNotReady
	}
	var req domain.Requested
	var policy domain.Policy
	if json.Unmarshal(raw, &req) != nil || req.Validate() != nil || req.Authorization == nil || json.Unmarshal(policyRaw, &policy) != nil || policy.Validate() != nil {
		return zero, domain.ErrNotReady
	}
	keys := []string{"event:" + req.EventID, "run:" + req.RunID, "admission:" + req.AdmissionID}
	sort.Strings(keys)
	for _, v := range keys {
		if _, e = nested.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731004284))`, v); e != nil {
			return zero, e
		}
	}
	if e = p.AuthorizeSessionRoute(ctx, nested, s, req.Authorization.PrincipalID, "message.send"); e != nil {
		return zero, e
	}
	// The source policy is already verified under its current-head lock. Require
	// the exact selected SessionPolicy/partition, not just an arbitrary registry key.
	var policyID, policyDigest, partition string
	var revision int64
	if e = nested.QueryRow(ctx, `SELECT session_policy_jsonb->>'policy_id',(session_policy_jsonb->>'revision')::bigint,session_policy_jsonb->>'digest',session_policy_jsonb->'definition'->'session'->>'partition' FROM worker_authorization_snapshots WHERE scope_id=$1 AND account_id=$2`, p.scope, s.AccountID).Scan(&policyID, &revision, &policyDigest, &partition); e != nil {
		return zero, domain.ErrNotReady
	}
	if s.PolicyID != policyID || s.PolicyRevision != revision || s.PolicyDigest != policyDigest || s.Partition != partition {
		return zero, domain.ErrFenced
	}
	scope, e := s.Canonical()
	if e != nil {
		return zero, e
	}
	var generation int64
	var same bool
	if e = nested.QueryRow(ctx, `SELECT generation,scope_json=$3::jsonb FROM worker_conversation_registry WHERE tenant_id=$1 AND scope_key=$2 FOR UPDATE`, s.TenantID, key, scope).Scan(&generation, &same); e != nil || !same || generation != selected.Generation {
		return zero, domain.ErrNotReady
	}
	var exists bool
	if e = nested.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM execution_receipts WHERE event_id=$1) OR EXISTS(SELECT 1 FROM execution_runs WHERE run_id=$2 OR admission_id=$3)`, req.EventID, req.RunID, req.AdmissionID).Scan(&exists); e != nil {
		return zero, e
	}
	if exists {
		return zero, domain.ErrNotReady
	} // outer receipt-first replay owns this case
	if _, e = nested.Exec(ctx, `SELECT pg_advisory_xact_lock(731004285)`); e != nil {
		return zero, e
	}
	if e = nested.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil || !now.Before(expires) || !expires.Equal(req.Input.ReceivedAt.Add(policy.MaxRunAge).Truncate(time.Microsecond)) {
		return zero, domain.ErrNotReady
	}
	var queued, retained int64
	if e = nested.QueryRow(ctx, `SELECT (SELECT count(*) FROM execution_runs WHERE status IN ('QUEUED','RUNNING','RETRY_WAIT'))+(SELECT count(*) FROM execution_pending_intakes WHERE expires_at>$1),(SELECT count(*) FROM execution_runs)+(SELECT count(*) FROM execution_pending_intakes)`, now).Scan(&queued, &retained); e != nil {
		return zero, e
	}
	// The current pending row already owns one queue/storage slot. Promotion
	// replaces it; counting the new Run as another retained input would deadlock
	// promotion at capacity. The outer transaction MUST consume the pending row.
	if queued > int64(limits.MaxQueuedRuns) || retained > int64(limits.MaxRetainedRuns) {
		return zero, domain.ErrCapacity
	}
	if e = budget.ReservePendingRun(ctx, nested, req, policy); e != nil {
		return zero, e
	}
	result, e := insertRun(ctx, nested, req, policy, id, scope)
	if e != nil {
		return zero, e
	}
	if _, e = nested.Exec(ctx, `INSERT INTO execution_session_partitions(tenant_id,session_id,scope_key,generation) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, s.TenantID, id, key, selected.Generation); e != nil {
		return zero, e
	}
	var storedKey string
	var storedGeneration int64
	if e = nested.QueryRow(ctx, `SELECT scope_key,generation FROM execution_session_partitions WHERE tenant_id=$1 AND session_id=$2`, s.TenantID, id).Scan(&storedKey, &storedGeneration); e != nil || storedKey != key || storedGeneration != selected.Generation {
		return zero, domain.ErrConflict
	}
	if e = nested.Commit(ctx); e != nil {
		return zero, e
	}
	return result, nil
}

// ConsumeRunInTransaction runs after Registry's final authority check and before
// the owner's commit. It verifies real Receipt/Run/partition facts, not a caller's
// assertion of success, before deleting exactly the source pending event.
func (p *PendingRouteAuthority) ConsumeRunInTransaction(ctx context.Context, tx pgx.Tx, s session.Scope, selected session.Selection, receipt domain.Receipt) error {
	if ctx == nil || tx == nil || s.Validate() != nil {
		return domain.ErrInvalid
	}
	key, e := s.Key()
	if e != nil {
		return e
	}
	id, e := s.SessionID(selected.Generation)
	if e != nil || selected.ScopeKey != key || selected.SessionID != id || receipt.EventID != p.event || receipt.TenantID != s.TenantID || receipt.SessionID != id || receipt.Outcome != "ACCEPTED" {
		return domain.ErrInvalid
	}
	var raw []byte
	if e = tx.QueryRow(ctx, `SELECT request_json FROM execution_pending_intakes WHERE event_id=$1 FOR SHARE`, p.event).Scan(&raw); e != nil {
		return domain.ErrNotReady
	}
	var req domain.Requested
	if json.Unmarshal(raw, &req) != nil || req.Validate() != nil || req.Authorization == nil {
		return domain.ErrNotReady
	}
	if e = p.AuthorizeSessionRoute(ctx, tx, s, req.Authorization.PrincipalID, "message.send"); e != nil {
		return e
	}
	var proved bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM execution_receipts e JOIN execution_runs r ON r.tenant_id=e.tenant_id AND r.run_id=e.run_id JOIN execution_session_partitions s ON s.tenant_id=r.tenant_id AND s.session_id=r.session_id WHERE e.event_id=$1 AND e.event_digest=$2 AND e.tenant_id=$3 AND e.run_id=$4 AND e.outcome='ACCEPTED' AND r.request_json=$5::jsonb AND r.session_id=$6 AND r.session_sequence=$7 AND s.scope_key=$8 AND s.generation=$9)`, p.event, req.EventDigest, receipt.TenantID, receipt.RunID, raw, id, receipt.Sequence, key, selected.Generation).Scan(&proved); e != nil {
		return e
	}
	if !proved {
		return domain.ErrNotReady
	}
	deleted, e := tx.Exec(ctx, `DELETE FROM execution_pending_intakes WHERE event_id=$1`, p.event)
	if e != nil {
		return e
	}
	if deleted.RowsAffected() != 1 {
		return domain.ErrNotReady
	}
	return nil
}
