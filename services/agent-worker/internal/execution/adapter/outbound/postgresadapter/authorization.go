package postgresadapter

import (
	"context"
	"encoding/json"
	"errors"
	shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	installer "github.com/liuzengh/trpc-agent-service/platform/channel/authorization/postgres"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

// NewAuthorizationProjection installs only into this Worker's database/table
// family. A reader must independently authenticate Control; Gateway's admitted
// fact is never accepted as the input to current-state installation.
func NewAuthorizationProjection(pool *pgxpool.Pool, scope, epoch string) (*installer.Store, error) {
	return installer.New(pool, scope, epoch, installer.Worker)
}

// currentAuthorization checks local current account/policy/principal state and
// exact enabled dependency documents under
// the caller's Claim transaction. nil means this check passed, not that quota,
// session isolation, tool authority or the remaining Claim gates have passed.
func (l *Ledger) currentAuthorization(ctx context.Context, tx pgx.Tx, r domain.Requested) error {
	a := r.Authorization
	if a == nil || a.ValidateFor(r) != nil {
		return domain.ErrNotReady
	}
	var epoch, tenant, provider, policyID, digest, blocked string
	var generation, policyRevision int64
	var enabled bool
	var raw, sessionRaw, quotaRaw []byte
	var start, until, now time.Time
	err := tx.QueryRow(ctx, `SELECT source_epoch,tenant_id,provider,generation,account_enabled,policy_id,policy_revision,policy_digest,policy_jsonb,read_started_at,fresh_until,blocked_reason,session_policy_jsonb,quota_policy_jsonb FROM worker_authorization_snapshots WHERE scope_id=$1 AND account_id=$2 FOR SHARE`, a.ScopeID, r.Route.AccountID).Scan(&epoch, &tenant, &provider, &generation, &enabled, &policyID, &policyRevision, &digest, &raw, &start, &until, &blocked, &sessionRaw, &quotaRaw)
	if err != nil || blocked != "" || epoch != a.SourceEpoch || tenant != r.Route.TenantID || provider != r.Route.Provider || generation < a.Generation || policyID != a.PolicyID || policyRevision < a.PolicyRevision || policyRevision == a.PolicyRevision && digest != a.PolicyDigest {
		return domain.ErrNotReady
	}
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil || now.Before(start) || !now.Before(until) {
		return domain.ErrNotReady
	}
	var p wire.AccessPolicyDocument
	if json.Unmarshal(raw, &p) != nil {
		return domain.ErrNotReady
	}
	envelope, _ := json.Marshal(wire.AccessPolicyResolveResponse{SchemaVersion: 1, ScopeID: a.ScopeID, SourceEpoch: epoch, Policy: p})
	decoded, err := wire.DecodeAccessPolicyResolveResponse(envelope)
	if err != nil || decoded.Policy.TenantID != tenant || decoded.Policy.AccountID != r.Route.AccountID || decoded.Policy.Provider != provider || decoded.Policy.PolicyID != policyID || decoded.Policy.Revision != policyRevision || decoded.Policy.Digest != digest || !until.Equal(start.Add(time.Duration(p.Body.AuthorizationMaxAgeMS)*time.Millisecond)) {
		return domain.ErrNotReady
	}
	if !enabled || p.Body.AccessMode == "DENY_ALL" {
		return domain.ErrFenced
	}
	var principal, state string
	var revision int64
	err = tx.QueryRow(ctx, `SELECT principal_id,state,revision FROM worker_authorization_principals WHERE scope_id=$1 AND account_id=$2 AND generation=$3 AND tenant_id=$4 AND provider=$5 AND external_user_id=$6`, a.ScopeID, r.Route.AccountID, generation, tenant, provider, r.Input.SenderID).Scan(&principal, &state, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrFenced
	}
	if err != nil {
		return domain.ErrNotReady
	}
	if principal != a.PrincipalID || revision < a.PrincipalRevision {
		return domain.ErrNotReady
	}
	if state == "REVOKED" {
		return domain.ErrFenced
	}
	if state != "ACTIVE" || p.Body.AccessMode != "ALLOWLIST" {
		return domain.ErrNotReady
	}
	if !slices.Contains(p.Body.AllowedPrincipalIDs, principal) || !slices.Contains(p.Body.AllowedOperations, a.Operation) || a.ConversationKind == "group" && !slices.Contains(p.Body.AllowedConversationIDs, a.ConversationID) {
		return domain.ErrFenced
	}
	if _, e := shared.CheckPolicyDependencies(p, sessionRaw, quotaRaw, nil); e != nil {
		if errors.Is(e, shared.ErrDependencyDisabled) || errors.Is(e, shared.ErrDependencyDenied) {
			return domain.ErrFenced
		}
		return domain.ErrNotReady
	}
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil || now.Before(start) || !now.Before(until) {
		return domain.ErrNotReady
	}
	return nil
}
