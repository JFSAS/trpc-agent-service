// Package postgresadapter persists Worker conversation generations and reset receipts.
package postgresadapter

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/domain"
)

// Authorizer must verify current principal/policy/operation and freshness in tx.
// A scope is an identity, not a capability. No permissive default is supplied.
type Authorizer interface {
	AuthorizeSession(context.Context, pgx.Tx, domain.Scope, string, string) error
}
type Registry struct {
	pool          *pgxpool.Pool
	authorization Authorizer
}

func New(pool *pgxpool.Pool, a Authorizer) (*Registry, error) {
	if pool == nil || a == nil {
		return nil, domain.ErrInvalid
	}
	return &Registry{pool, a}, nil
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func lock(ctx context.Context, tx pgx.Tx, s domain.Scope) (domain.Selection, error) {
	key, e := s.Key()
	if e != nil {
		return domain.Selection{}, e
	}
	raw, _ := s.Canonical()
	if _, e = tx.Exec(ctx, `INSERT INTO worker_conversation_registry(tenant_id,scope_key,scope_json,generation) VALUES($1,$2,$3,1) ON CONFLICT DO NOTHING`, s.TenantID, key, raw); e != nil {
		return domain.Selection{}, e
	}
	var generation int64
	var same bool
	if e = tx.QueryRow(ctx, `SELECT generation,scope_json=$3::jsonb FROM worker_conversation_registry WHERE tenant_id=$1 AND scope_key=$2 FOR UPDATE`, s.TenantID, key, raw).Scan(&generation, &same); e != nil {
		return domain.Selection{}, e
	}
	if !same {
		return domain.Selection{}, domain.ErrConflict
	}
	id, e := s.SessionID(generation)
	return domain.Selection{ScopeKey: key, SessionID: id, Generation: generation}, e
}

// WithCurrent lets intake select generation and persist its Run in the SAME
// transaction. The callback must not commit, retain tx or perform external I/O.
func (r *Registry) WithCurrent(ctx context.Context, s domain.Scope, actor string, apply func(context.Context, pgx.Tx, domain.Selection) error) error {
	if ctx == nil || s.Validate() != nil || apply == nil || !domain.ValidActor(actor) {
		return domain.ErrInvalid
	}
	if s.Partition == domain.PerUser && actor != s.PrincipalID {
		return domain.ErrDenied
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer rollback(tx)
	chosen, e := lock(ctx, tx, s)
	if e != nil {
		return e
	}
	if e = r.authorization.AuthorizeSession(ctx, tx, s, actor, "message.send"); e != nil {
		return e
	}
	if e = apply(ctx, tx, chosen); e != nil {
		return e
	}
	if e = r.authorization.AuthorizeSession(ctx, tx, s, actor, "message.send"); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (r *Registry) Reset(ctx context.Context, cmd domain.Reset) (domain.Selection, error) {
	zero := domain.Selection{}
	if ctx == nil {
		return zero, domain.ErrInvalid
	}
	digest, e := cmd.Digest()
	if e != nil {
		return zero, e
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer rollback(tx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731004286))`, cmd.CommandID); e != nil {
		return zero, e
	}
	var prior domain.Selection
	var old string
	e = tx.QueryRow(ctx, `SELECT request_digest,scope_key,session_id,generation FROM worker_conversation_commands WHERE command_id=$1`, cmd.CommandID).Scan(&old, &prior.ScopeKey, &prior.SessionID, &prior.Generation)
	if e == nil {
		expectedScope, _ := cmd.Scope.Key()
		expectedSession, _ := cmd.Scope.SessionID(cmd.ExpectedGeneration + 1)
		if old != digest || prior.ScopeKey != expectedScope || prior.SessionID != expectedSession || prior.Generation != cmd.ExpectedGeneration+1 {
			return zero, domain.ErrConflict
		}
		return prior, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return zero, e
	}
	selected, e := lock(ctx, tx, cmd.Scope)
	if e != nil {
		return zero, e
	}
	if e = r.authorization.AuthorizeSession(ctx, tx, cmd.Scope, cmd.ActorID, cmd.Operation()); e != nil {
		return zero, e
	}
	if selected.Generation != cmd.ExpectedGeneration {
		return zero, domain.ErrConflict
	}
	selected.Generation++
	selected.SessionID, e = cmd.Scope.SessionID(selected.Generation)
	if e != nil {
		return zero, e
	}
	if _, e = tx.Exec(ctx, `UPDATE worker_conversation_registry SET generation=$3 WHERE tenant_id=$1 AND scope_key=$2`, cmd.Scope.TenantID, selected.ScopeKey, selected.Generation); e != nil {
		return zero, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO worker_conversation_commands(command_id,request_digest,tenant_id,scope_key,actor_id,operation,generation,session_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, cmd.CommandID, digest, cmd.Scope.TenantID, selected.ScopeKey, cmd.ActorID, cmd.Operation(), selected.Generation, selected.SessionID); e != nil {
		return zero, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO worker_conversation_audit_outbox(command_id,tenant_id,scope_key,actor_id,operation,generation) VALUES($1,$2,$3,$4,$5,$6)`, cmd.CommandID, cmd.Scope.TenantID, selected.ScopeKey, cmd.ActorID, cmd.Operation(), selected.Generation); e != nil {
		return zero, e
	}
	if e = r.authorization.AuthorizeSession(ctx, tx, cmd.Scope, cmd.ActorID, cmd.Operation()); e != nil {
		return zero, e
	}
	if e = tx.Commit(ctx); e != nil {
		return zero, e
	}
	return selected, nil
}
