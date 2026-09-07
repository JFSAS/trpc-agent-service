package policypostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

var ErrGap = errors.New("POLICY_PROCESSING_GAP")

func (s *Store) positionDigest(created time.Time, sequence uint64, e wire.AccessPolicyEvent) (string, error) {
	if created.IsZero() || sequence == 0 || sequence > 9007199254740991 || e.EventType != wire.AccessPolicyPublishedEvent || (e.ScopeID == s.scope && e.SourceEpoch != s.epoch) {
		return "", ErrInvalid
	}
	_, digest, err := e.CanonicalJSON()
	if err != nil {
		return "", ErrInvalid
	}
	return digest, nil
}

// Processed verifies an exact committed message position before a replay may
// bypass the external reader. It never treats sequence <= cursor alone as proof.
func (s *Store) Processed(ctx context.Context, created time.Time, sequence uint64, e wire.AccessPolicyEvent) (bool, error) {
	return s.checkpoint(ctx, created, sequence, e, false)
}

// RecordProcessed commits a contiguous position only after checking the durable
// local document. Other scopes record only the validated notification digest.
// Document commit may precede this transaction; a crash between them simply
// replays Apply. No ACK is allowed before this checkpoint transaction commits.
func (s *Store) RecordProcessed(ctx context.Context, created time.Time, sequence uint64, e wire.AccessPolicyEvent) error {
	_, err := s.checkpoint(ctx, created, sequence, e, true)
	return err
}
func (s *Store) checkpoint(ctx context.Context, created time.Time, seq uint64, e wire.AccessPolicyEvent, record bool) (bool, error) {
	if ctx == nil {
		return false, ErrInvalid
	}
	digest, err := s.positionDigest(created, seq, e)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, ErrUnavailable
	}
	defer rollback(tx)
	var epoch, stamp, blocked string
	var processed, observed int64
	err = tx.QueryRow(ctx, `SELECT source_epoch,stream_created,blocked_reason,processed_sequence,observed_sequence FROM gateway_policy_sources WHERE scope_id=$1 FOR UPDATE`, s.scope).Scan(&epoch, &stamp, &blocked, &processed, &observed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrBlocked
	}
	if err != nil {
		return false, ErrUnavailable
	}
	if blocked != "" || epoch != s.epoch || stamp != created.UTC().Format(time.RFC3339Nano) {
		return false, ErrBlocked
	}
	if int64(seq) <= processed {
		var old string
		err = tx.QueryRow(ctx, `SELECT event_digest FROM gateway_policy_processed_messages WHERE scope_id=$1 AND sequence=$2`, s.scope, int64(seq)).Scan(&old)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, ErrUnavailable
		}
		if old != digest {
			if _, err = tx.Exec(ctx, `UPDATE gateway_policy_sources SET blocked_reason='POSITION_CONFLICT' WHERE scope_id=$1`, s.scope); err != nil {
				return false, ErrUnavailable
			}
			if tx.Commit(ctx) != nil {
				return false, ErrUnavailable
			}
			return false, ErrBlocked
		}
		if tx.Commit(ctx) != nil {
			return false, ErrUnavailable
		}
		return true, nil
	}
	if int64(seq) != processed+1 {
		_, err = tx.Exec(ctx, `UPDATE gateway_policy_sources SET observed_sequence=GREATEST(observed_sequence,$2) WHERE scope_id=$1`, s.scope, int64(seq))
		if err != nil {
			return false, ErrUnavailable
		}
		if tx.Commit(ctx) != nil {
			return false, ErrUnavailable
		}
		return false, ErrGap
	}
	if !record {
		if tx.Commit(ctx) != nil {
			return false, ErrUnavailable
		}
		return false, nil
	}
	if e.ScopeID == s.scope {
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT d.document_jsonb FROM gateway_policy_projection_documents d JOIN gateway_policy_projection_heads h USING(scope_id,source_epoch,account_id) WHERE d.scope_id=$1 AND d.source_epoch=$2 AND d.tenant_id=$3 AND d.account_id=$4 AND d.provider=$5 AND d.policy_id=$6 AND d.revision=$7 AND d.digest=$8 AND h.blocked_reason=''`, s.scope, s.epoch, e.TenantID, e.AccountID, e.Provider, e.PolicyID, e.PolicyRevision, e.PolicyDigest).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrBlocked
		}
		if err != nil {
			return false, ErrUnavailable
		}
		doc, err := s.decodeStored(raw)
		if err != nil || doc.TenantID != e.TenantID || doc.AccountID != e.AccountID || doc.Provider != e.Provider || doc.PolicyID != e.PolicyID || doc.Revision != e.PolicyRevision || doc.Digest != e.PolicyDigest {
			return false, ErrBlocked
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO gateway_policy_processed_messages(scope_id,sequence,event_digest) VALUES($1,$2,$3)`, s.scope, int64(seq), digest)
	if err != nil {
		return false, ErrUnavailable
	}
	_, err = tx.Exec(ctx, `UPDATE gateway_policy_sources SET processed_sequence=$2,observed_sequence=GREATEST(observed_sequence,$2) WHERE scope_id=$1`, s.scope, int64(seq))
	if err != nil {
		return false, ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return false, ErrUnavailable
	}
	return true, nil
}

// ObserveBroker detects an ACK floor ahead of local processing even when the
// durable is idle and will never redeliver that missing history on its own.
// The observed high-water mark is diagnostic progress, not an authorization TTL.
func (s *Store) ObserveBroker(ctx context.Context, created time.Time, ackFloor, last uint64) error {
	if ctx == nil || created.IsZero() || ackFloor > last || last > 9007199254740991 {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer rollback(tx)
	var epoch, stamp, blocked string
	var processed int64
	err = tx.QueryRow(ctx, `SELECT source_epoch,stream_created,blocked_reason,processed_sequence FROM gateway_policy_sources WHERE scope_id=$1 FOR UPDATE`, s.scope).Scan(&epoch, &stamp, &blocked, &processed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBlocked
	}
	if err != nil {
		return ErrUnavailable
	}
	if epoch != s.epoch || stamp != created.UTC().Format(time.RFC3339Nano) || blocked != "" {
		return ErrBlocked
	}
	if _, err = tx.Exec(ctx, `UPDATE gateway_policy_sources SET observed_sequence=GREATEST(observed_sequence,$2) WHERE scope_id=$1`, s.scope, int64(last)); err != nil {
		return ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ErrUnavailable
	}
	if ackFloor > uint64(processed) {
		return ErrGap
	}
	return nil
}
