package policypostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

// BindSource pins a verified JetStream creation identity. A later epoch/source
// mismatch commits a permanent block instead of silently replacing the pin.
// Existing unbound history requires explicit snapshot recovery, not adoption.
func (s *Store) BindSource(ctx context.Context, created time.Time) error {
	if ctx == nil || created.IsZero() {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer rollback(tx)
	tag, err := tx.Exec(ctx, `INSERT INTO gateway_policy_sources(scope_id,source_epoch,stream_name,stream_created) VALUES($1,$2,$3,$4) ON CONFLICT(scope_id) DO NOTHING`, s.scope, s.epoch, wire.AccessPolicyStream, created.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return ErrUnavailable
	}
	var epoch, name, stamp, blocked string
	err = tx.QueryRow(ctx, `SELECT source_epoch,stream_name,stream_created,blocked_reason FROM gateway_policy_sources WHERE scope_id=$1 FOR UPDATE`, s.scope).Scan(&epoch, &name, &stamp, &blocked)
	if err != nil {
		return ErrUnavailable
	}
	if blocked != "" {
		return ErrBlocked
	}
	reason := ""
	if epoch != s.epoch || name != wire.AccessPolicyStream || stamp != created.UTC().Format(time.RFC3339Nano) {
		reason = "SOURCE_CHANGED"
	}
	if tag.RowsAffected() == 1 {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gateway_policy_projection_heads WHERE scope_id=$1)`, s.scope).Scan(&exists); err != nil {
			return ErrUnavailable
		}
		if exists {
			reason = "UNBOUND_HISTORY"
		}
	}
	if reason != "" {
		if _, err = tx.Exec(ctx, `UPDATE gateway_policy_sources SET blocked_reason=$2 WHERE scope_id=$1`, s.scope, reason); err != nil {
			return ErrUnavailable
		}
	}
	if tx.Commit(ctx) != nil {
		return ErrUnavailable
	}
	if reason != "" {
		return ErrBlocked
	}
	return nil
}

// guardSource holds the source row through a write commit. Binding/rebinding
// takes an exclusive lock, so a block cannot race a write past the same fence.
func (s *Store) guardSource(ctx context.Context, tx pgx.Tx, write bool) error {
	sql := `SELECT source_epoch,blocked_reason FROM gateway_policy_sources WHERE scope_id=$1`
	if write {
		sql += " FOR SHARE"
	}
	var epoch, blocked string
	err := tx.QueryRow(ctx, sql, s.scope).Scan(&epoch, &blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		if write {
			return ErrBlocked
		}
		return nil
	}
	if err != nil {
		return ErrUnavailable
	}
	if epoch != s.epoch || blocked != "" {
		return ErrBlocked
	}
	return nil
}
