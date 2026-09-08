// Package postgresadapter implements Execution's atomic, tenant-scoped ledger.
package postgresadapter

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/application"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

type Ledger struct{ pool *pgxpool.Pool }

var _ application.Ledger = (*Ledger)(nil)

func New(pool *pgxpool.Pool) *Ledger { return &Ledger{pool: pool} }
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func databaseTime(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, err
}
func mapped(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

const runColumns = `request_json,session_id,session_sequence,status,wait_reason,policy_json,accepted_at,run_deadline,reply_deadline,execution_deadline,attempts,generation,lease_epoch,current_attempt_id`

func prefixColumns(prefix, columns string) string {
	return prefix + "." + strings.ReplaceAll(columns, ",", ","+prefix+".")
}
func scanRun(row pgx.Row) (domain.Run, error) {
	var r domain.Run
	var raw, policy []byte
	err := row.Scan(&raw, &r.SessionID, &r.Sequence, &r.Status, &r.WaitReason, &policy, &r.AcceptedAt, &r.RunDeadline, &r.ReplyDeadline, &r.ExecutionDeadline, &r.Attempts, &r.Generation, &r.LeaseEpoch, &r.CurrentAttemptID)
	if err != nil {
		return r, mapped(err)
	}
	if err = json.Unmarshal(raw, &r.Request); err != nil {
		return r, err
	}
	err = json.Unmarshal(policy, &r.Policy)
	return r, err
}
func (l *Ledger) FindRun(ctx context.Context, tenant, id string) (domain.Run, error) {
	return scanRun(l.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM execution_runs WHERE tenant_id=$1 AND run_id=$2`, tenant, id))
}

// lockRun uses the same Session -> Run order in Claim, Renew, Complete and
// recovery. The first read only locates the immutable session identity.
func lockRun(ctx context.Context, tx pgx.Tx, tenant, id string) (domain.Run, domain.Head, int64, error) {
	var session string
	if err := tx.QueryRow(ctx, `SELECT session_id FROM execution_runs WHERE tenant_id=$1 AND run_id=$2`, tenant, id).Scan(&session); err != nil {
		return domain.Run{}, domain.Head{}, 0, mapped(err)
	}
	var head domain.Head
	var settled int64
	if err := tx.QueryRow(ctx, `SELECT accepted_ref,accepted_digest,settled_sequence FROM execution_sessions WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, tenant, session).Scan(&head.Ref, &head.Digest, &settled); err != nil {
		return domain.Run{}, head, 0, err
	}
	r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM execution_runs WHERE tenant_id=$1 AND run_id=$2 FOR UPDATE`, tenant, id))
	return r, head, settled, err
}
func rejected(ctx context.Context, tx pgx.Tx, source, digest, reason string) error {
	_, err := tx.Exec(ctx, `INSERT INTO execution_rejections(rejection_id,source_identity,digest,reason) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, domain.StableID("rej", source+"\x00"+digest), source, digest, reason)
	return err
}
func (l *Ledger) Reject(ctx context.Context, source, digest, reason string) error {
	if source == "" || !domain.DigestValid(digest) || reason == "" {
		return domain.ErrInvalid
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err = rejected(ctx, tx, source, digest, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func receipt(ctx context.Context, tx pgx.Tx, eventID string) (domain.Receipt, string, error) {
	var r domain.Receipt
	var digest string
	err := tx.QueryRow(ctx, `SELECT e.event_id,e.event_digest,e.tenant_id,e.run_id,e.outcome,r.session_id,r.session_sequence FROM execution_receipts e JOIN execution_runs r ON r.tenant_id=e.tenant_id AND r.run_id=e.run_id WHERE e.event_id=$1`, eventID).Scan(&r.EventID, &digest, &r.TenantID, &r.RunID, &r.Outcome, &r.SessionID, &r.Sequence)
	return r, digest, mapped(err)
}

func (l *Ledger) Accept(ctx context.Context, req domain.Requested, policy domain.Policy, limits domain.IntakeLimits) (domain.Receipt, error) {
	return l.accept(ctx, req, policy, limits, false)
}

func (l *Ledger) accept(ctx context.Context, req domain.Requested, policy domain.Policy, limits domain.IntakeLimits, stageAuthorized bool) (domain.Receipt, error) {
	if ctx == nil || req.Validate() != nil || policy.Validate() != nil || limits.Validate() != nil {
		return domain.Receipt{}, domain.ErrInvalid
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return domain.Receipt{}, err
	}
	defer rollback(tx)
	// Admission identity locks precede all business row locks. Sorted identities
	// serialize collisions even when event IDs, tenant or Session differ.
	keys := []string{"event:" + req.EventID, "run:" + req.RunID, "admission:" + req.AdmissionID}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731004284))`, key); err != nil {
			return domain.Receipt{}, err
		}
	}
	if old, digest, e := receipt(ctx, tx, req.EventID); e == nil {
		if digest == req.EventDigest {
			return old, tx.Commit(ctx)
		}
		if e = rejected(ctx, tx, "event:"+req.EventID, req.EventDigest, "EVENT_CONFLICT"); e != nil {
			return domain.Receipt{}, e
		}
		if e = tx.Commit(ctx); e != nil {
			return domain.Receipt{}, e
		}
		return domain.Receipt{}, domain.ErrConflict
	} else if !errors.Is(e, domain.ErrNotFound) {
		return domain.Receipt{}, e
	}
	// A pending identity can only be promoted by the future registry/Run
	// transaction. Keep all collisions retryable here: ErrConflict is terminal
	// to the broker and must not ACK an input still awaiting authorization.
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM execution_pending_intakes WHERE event_id=$1 OR run_id=$2 OR admission_id=$3)`, req.EventID, req.RunID, req.AdmissionID).Scan(&pending); err != nil {
		return domain.Receipt{}, err
	}
	if pending {
		return domain.Receipt{}, domain.ErrNotReady
	}
	var tenant, id, digest, admission string
	err = tx.QueryRow(ctx, `SELECT tenant_id,run_id,request_digest,admission_id FROM execution_runs WHERE run_id=$1 OR admission_id=$2 ORDER BY run_id LIMIT 1`, req.RunID, req.AdmissionID).Scan(&tenant, &id, &digest, &admission)
	if err == nil {
		if tenant != req.Route.TenantID || id != req.RunID || admission != req.AdmissionID || digest != req.RunDigest {
			if err = rejected(ctx, tx, "event:"+req.EventID, req.EventDigest, "RUN_CONFLICT"); err != nil {
				return domain.Receipt{}, err
			}
			if err = tx.Commit(ctx); err != nil {
				return domain.Receipt{}, err
			}
			return domain.Receipt{}, domain.ErrConflict
		}
		if _, err = tx.Exec(ctx, `INSERT INTO execution_receipts(event_id,event_digest,tenant_id,run_id,outcome) VALUES($1,$2,$3,$4,'ACCEPTED')`, req.EventID, req.EventDigest, tenant, id); err != nil {
			return domain.Receipt{}, err
		}
		r, _, e := receipt(ctx, tx, req.EventID)
		if e != nil {
			return r, e
		}
		return r, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Receipt{}, err
	}
	if stageAuthorized && req.Authorization != nil {
		if err = stageAuthorization(ctx, tx, req, policy, limits); err != nil {
			return domain.Receipt{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.Receipt{}, err
		}
		return domain.Receipt{}, domain.ErrNotReady
	}
	// Capacity admission is globally serialized, but only for new identities.
	// Retries replay their receipt even while the queue is full.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(731004285)`); err != nil {
		return domain.Receipt{}, err
	}
	// Count all retained Run identities under the same cross-process admission
	// lock. Completed history still occupies storage, but this gate never blocks
	// receipt replay, an alias for an existing Run, or that Run's recovery writes.
	var queued, retained int64
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM execution_runs WHERE status IN ('QUEUED','RUNNING','RETRY_WAIT'))+(SELECT count(*) FROM execution_pending_intakes WHERE expires_at>clock_timestamp()),(SELECT count(*) FROM execution_runs)+(SELECT count(*) FROM execution_pending_intakes)`).Scan(&queued, &retained); err != nil {
		return domain.Receipt{}, err
	}
	if queued >= int64(limits.MaxQueuedRuns) || retained >= int64(limits.MaxRetainedRuns) {
		return domain.Receipt{}, domain.ErrCapacity
	}
	session := req.SessionID()
	scope, err := json.Marshal(req.Scope())
	if err != nil {
		return domain.Receipt{}, err
	}
	r, err := insertRun(ctx, tx, req, policy, session, scope)
	if err != nil {
		return domain.Receipt{}, err
	}
	return r, tx.Commit(ctx)
}

// insertRun only writes into its owner's transaction. Its session identity and
// canonical scope are supplied by the selected intake contract, not recomputed.
func insertRun(ctx context.Context, tx pgx.Tx, req domain.Requested, policy domain.Policy, session string, scope []byte) (domain.Receipt, error) {
	var err error
	if _, err = tx.Exec(ctx, `INSERT INTO execution_sessions(tenant_id,session_id,scope_json) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, req.Route.TenantID, session, scope); err != nil {
		return domain.Receipt{}, err
	}
	var seq int64
	var same bool
	if err = tx.QueryRow(ctx, `SELECT next_sequence,scope_json=$3::jsonb FROM execution_sessions WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, req.Route.TenantID, session, scope).Scan(&seq, &same); err != nil {
		return domain.Receipt{}, err
	}
	if !same {
		return domain.Receipt{}, domain.ErrConflict
	}
	seq++
	now, err := databaseTime(ctx, tx)
	if err != nil {
		return domain.Receipt{}, err
	}
	wait := "MANIFEST"
	if req.Input.ReceivedAt.After(now.Add(policy.MaxFutureSkew)) {
		wait = "INVALID_CLOCK"
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return domain.Receipt{}, err
	}
	p, err := json.Marshal(policy)
	if err != nil {
		return domain.Receipt{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO execution_runs(tenant_id,run_id,admission_id,request_digest,request_json,session_id,session_sequence,status,wait_reason,policy_json,accepted_at,run_deadline,reply_deadline) VALUES($1,$2,$3,$4,$5,$6,$7,'QUEUED',$8,$9,$10,$11,$12)`, req.Route.TenantID, req.RunID, req.AdmissionID, req.RunDigest, raw, session, seq, wait, p, now, req.Input.ReceivedAt.Add(policy.MaxRunAge), req.Input.ReceivedAt.Add(policy.MaxReplyAge))
	if err != nil {
		return domain.Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE execution_sessions SET next_sequence=$3 WHERE tenant_id=$1 AND session_id=$2`, req.Route.TenantID, session, seq); err != nil {
		return domain.Receipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO execution_receipts(event_id,event_digest,tenant_id,run_id,outcome) VALUES($1,$2,$3,$4,'ACCEPTED')`, req.EventID, req.EventDigest, req.Route.TenantID, req.RunID); err != nil {
		return domain.Receipt{}, err
	}
	r := domain.Receipt{EventID: req.EventID, RunID: req.RunID, TenantID: req.Route.TenantID, SessionID: session, Sequence: seq, Outcome: "ACCEPTED"}
	return r, nil
}

func (l *Ledger) Ready(ctx context.Context, limit int) ([]domain.Run, error) {
	if limit < 1 {
		return nil, domain.ErrInvalid
	}
	rows, err := l.pool.Query(ctx, `SELECT `+prefixColumns("r", runColumns)+` FROM execution_runs r JOIN execution_sessions s ON s.tenant_id=r.tenant_id AND s.session_id=r.session_id WHERE r.status IN ('QUEUED','RUNNING','RETRY_WAIT') AND r.session_sequence=s.settled_sequence+1 AND NOT EXISTS (SELECT 1 FROM execution_attempts a WHERE a.tenant_id=r.tenant_id AND a.attempt_id=r.current_attempt_id AND a.status IN ('PREPARING','EXECUTING') AND a.lease_until>clock_timestamp()) AND (r.retry_at IS NULL OR r.retry_at<=clock_timestamp() OR r.run_deadline<=clock_timestamp() OR r.execution_deadline<=clock_timestamp()) ORDER BY r.accepted_at,r.tenant_id,r.session_id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
