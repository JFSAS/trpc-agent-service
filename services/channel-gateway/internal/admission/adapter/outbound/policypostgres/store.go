// Package policypostgres retains immutable policy documents and account-local
// revision continuity. Only snapshot/current-state fences may establish authority.
package policypostgres

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
)

var (
	ErrInvalid     = errors.New("POLICY_PROJECTION_INVALID")
	ErrUnavailable = errors.New("POLICY_PROJECTION_UNAVAILABLE")
	ErrBlocked     = errors.New("POLICY_PROJECTION_BLOCKED")
	ErrNotFound    = errors.New("POLICY_PROJECTION_NOT_FOUND")
)
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var epochPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Store struct {
	pool         *pgxpool.Pool
	scope, epoch string
}

func New(pool *pgxpool.Pool, scope, epoch string) (*Store, error) {
	if pool == nil || !idPattern.MatchString(scope) || !epochPattern.MatchString(epoch) {
		return nil, ErrInvalid
	}
	return &Store{pool: pool, scope: scope, epoch: epoch}, nil
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func validated(scope, epoch string, p wire.AccessPolicyDocument) ([]byte, error) {
	raw, err := json.Marshal(wire.AccessPolicyResolveResponse{SchemaVersion: 1, ScopeID: scope, SourceEpoch: epoch, Policy: p})
	if err != nil {
		return nil, ErrInvalid
	}
	if _, err = wire.DecodeAccessPolicyResolveResponse(raw); err != nil {
		return nil, ErrInvalid
	}
	raw, err = json.Marshal(p)
	if err != nil {
		return nil, ErrInvalid
	}
	return raw, nil
}

// Apply validates a trusted notification and its independently verified document.
// It serializes first and concurrent updates on the account row. Conflict marks
// are committed before returning ErrBlocked, and survive process restart. Filling
// a history gap never clears a conflict or creates an authorization timestamp.
func (s *Store) Apply(ctx context.Context, e wire.AccessPolicyEvent, p wire.AccessPolicyDocument) (domain.PolicyContinuity, error) {
	zero := domain.PolicyContinuity{}
	if ctx == nil {
		return zero, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, _, err := e.CanonicalJSON(); err != nil || e.EventType != wire.AccessPolicyPublishedEvent || e.ScopeID != s.scope || e.SourceEpoch != s.epoch || e.TenantID != p.TenantID || e.AccountID != p.AccountID || e.Provider != p.Provider || e.PolicyID != p.PolicyID || e.PolicyRevision != p.Revision || e.PolicyDigest != p.Digest {
		return zero, ErrInvalid
	}
	raw, err := validated(s.scope, s.epoch, p)
	if err != nil {
		return zero, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, ErrUnavailable
	}
	defer rollback(tx)
	if err = s.guardSource(ctx, tx, true); err != nil {
		return zero, err
	}

	_, err = tx.Exec(ctx, `INSERT INTO gateway_policy_projection_heads(scope_id,source_epoch,tenant_id,account_id,provider,policy_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(scope_id,source_epoch,account_id) DO NOTHING`, s.scope, s.epoch, p.TenantID, p.AccountID, p.Provider, p.PolicyID)
	if err != nil {
		return zero, ErrUnavailable
	}
	var tenant, provider, policy string
	var state domain.PolicyContinuity
	err = tx.QueryRow(ctx, `SELECT tenant_id,provider,policy_id,observed_revision,contiguous_revision,blocked_reason FROM gateway_policy_projection_heads WHERE scope_id=$1 AND source_epoch=$2 AND account_id=$3 FOR UPDATE`, s.scope, s.epoch, p.AccountID).Scan(&tenant, &provider, &policy, &state.ObservedRevision, &state.ContiguousRevision, &state.BlockedReason)
	if err != nil {
		return zero, ErrUnavailable
	}
	if state.BlockedReason != "" {
		return zero, ErrBlocked
	}
	reason := ""
	if tenant != p.TenantID || provider != p.Provider || policy != p.PolicyID {
		reason = "IDENTITY_CONFLICT"
	}
	var existing string
	var stored []byte
	if reason == "" {
		err = tx.QueryRow(ctx, `SELECT digest,document_jsonb FROM gateway_policy_projection_documents WHERE scope_id=$1 AND source_epoch=$2 AND account_id=$3 AND revision=$4`, s.scope, s.epoch, p.AccountID, p.Revision).Scan(&existing, &stored)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return zero, ErrUnavailable
		}
		if existing != "" {
			old, decodeErr := s.decodeStored(stored)
			if decodeErr != nil || existing != p.Digest || old.Digest != existing || old.TenantID != p.TenantID || old.AccountID != p.AccountID || old.Provider != p.Provider || old.PolicyID != p.PolicyID || old.Revision != p.Revision {
				reason = "REVISION_CONFLICT"
			}
		}
	}
	if reason != "" {
		_, err = tx.Exec(ctx, `UPDATE gateway_policy_projection_heads SET blocked_reason=$4 WHERE scope_id=$1 AND source_epoch=$2 AND account_id=$3`, s.scope, s.epoch, p.AccountID, reason)
		if err != nil {
			return zero, ErrUnavailable
		}
		if tx.Commit(ctx) != nil {
			return zero, ErrUnavailable
		}
		return zero, ErrBlocked
	}
	if existing == "" {
		_, err = tx.Exec(ctx, `INSERT INTO gateway_policy_projection_documents(scope_id,source_epoch,tenant_id,account_id,provider,policy_id,revision,digest,document_jsonb) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, s.scope, s.epoch, p.TenantID, p.AccountID, p.Provider, p.PolicyID, p.Revision, p.Digest, raw)
		if err != nil {
			return zero, ErrUnavailable
		}
	}
	if p.Revision > state.ObservedRevision {
		state.ObservedRevision = p.Revision
	}
	if p.Revision == state.ContiguousRevision+1 {
		// The first retained revision without a successor is the end of the newly
		// connected range. Missing older revisions remain visible as a gap.
		err = tx.QueryRow(ctx, `SELECT min(d.revision) FROM gateway_policy_projection_documents d WHERE d.scope_id=$1 AND d.source_epoch=$2 AND d.account_id=$3 AND d.revision >= $4 AND NOT EXISTS(SELECT 1 FROM gateway_policy_projection_documents n WHERE n.scope_id=d.scope_id AND n.source_epoch=d.source_epoch AND n.account_id=d.account_id AND n.revision=d.revision+1)`, s.scope, s.epoch, p.AccountID, p.Revision).Scan(&state.ContiguousRevision)
		if err != nil {
			return zero, ErrUnavailable
		}
	}
	_, err = tx.Exec(ctx, `UPDATE gateway_policy_projection_heads SET observed_revision=$4,contiguous_revision=$5 WHERE scope_id=$1 AND source_epoch=$2 AND account_id=$3`, s.scope, s.epoch, p.AccountID, state.ObservedRevision, state.ContiguousRevision)
	if err != nil {
		return zero, ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return zero, ErrUnavailable
	}
	return state, nil
}

// ReadExact revalidates persisted content in a read-only repeatable-read
// transaction. It is a history read, not an admission guard or current head grant.
func (s *Store) ReadExact(ctx context.Context, tenant, account string, ref wire.PolicyReference) (wire.AccessPolicyDocument, domain.PolicyContinuity, error) {
	zero := wire.AccessPolicyDocument{}
	zs := domain.PolicyContinuity{}
	if ctx == nil || !idPattern.MatchString(tenant) || !idPattern.MatchString(account) {
		return zero, zs, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := json.Marshal(wire.AccessPolicyResolveRequest{SchemaVersion: 1, AccountID: account, Reference: ref})
	if wire.Validate("access-policy-resolve-request.schema.json", req) != nil {
		return zero, zs, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return zero, zs, ErrUnavailable
	}
	defer rollback(tx)
	if err = s.guardSource(ctx, tx, false); err != nil {
		return zero, zs, err
	}

	var raw []byte
	var state domain.PolicyContinuity
	err = tx.QueryRow(ctx, `SELECT d.document_jsonb,h.observed_revision,h.contiguous_revision,h.blocked_reason FROM gateway_policy_projection_documents d JOIN gateway_policy_projection_heads h USING(scope_id,source_epoch,account_id) WHERE d.scope_id=$1 AND d.source_epoch=$2 AND d.tenant_id=$3 AND d.account_id=$4 AND d.policy_id=$5 AND d.revision=$6 AND d.digest=$7`, s.scope, s.epoch, tenant, account, ref.ID, ref.Revision, ref.Digest).Scan(&raw, &state.ObservedRevision, &state.ContiguousRevision, &state.BlockedReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, zs, ErrNotFound
	}
	if err != nil {
		return zero, zs, ErrUnavailable
	}
	if state.BlockedReason != "" {
		return zero, zs, ErrBlocked
	}
	document, err := s.decodeStored(raw)
	if err != nil || document.TenantID != tenant || document.AccountID != account || document.PolicyID != ref.ID || document.Revision != ref.Revision || document.Digest != ref.Digest {
		return zero, zs, ErrBlocked
	}
	if tx.Commit(ctx) != nil {
		return zero, zs, ErrUnavailable
	}
	return document, state, nil
}

func (s *Store) decodeStored(raw []byte) (wire.AccessPolicyDocument, error) {
	envelope, err := json.Marshal(struct {
		SchemaVersion int             `json:"schema_version"`
		ScopeID       string          `json:"scope_id"`
		SourceEpoch   string          `json:"source_epoch"`
		Policy        json.RawMessage `json:"policy"`
	}{1, s.scope, s.epoch, raw})
	if err != nil {
		return wire.AccessPolicyDocument{}, ErrBlocked
	}
	out, err := wire.DecodeAccessPolicyResolveResponse(envelope)
	if err != nil {
		return wire.AccessPolicyDocument{}, ErrBlocked
	}
	return out.Policy, nil
}
