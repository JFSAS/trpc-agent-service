package postgresadapter

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/domain"
)

var ErrAuthorizationNotReady = errors.New("SESSION_AUTHORIZATION_NOT_READY")
var scopePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var epochPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// RouteAuthority must bind the requested Binding/DeploymentRevision and source-
// authenticated conversation facts to this account in the SAME transaction.
// Principal IDs are supplied from verified ingress identity, not a UI/body claim.
type RouteAuthority interface {
	AuthorizeSessionRoute(context.Context, pgx.Tx, domain.Scope, string) error
}
type CurrentAuthorizer struct {
	scope, epoch string
	routes       RouteAuthority
}

func NewCurrentAuthorizer(scope, epoch string, routes RouteAuthority) (*CurrentAuthorizer, error) {
	if !scopePattern.MatchString(scope) || !epochPattern.MatchString(epoch) || routes == nil {
		return nil, domain.ErrInvalid
	}
	return &CurrentAuthorizer{scope, epoch, routes}, nil
}

// AuthorizeSession reads only Worker's current tables; it neither calls Control
// nor treats a saved AdmissionAuthorization or registry key as an execution grant.
func (a *CurrentAuthorizer) AuthorizeSession(ctx context.Context, tx pgx.Tx, s domain.Scope, actor, operation string) error {
	if ctx == nil || tx == nil || s.Validate() != nil || !domain.ValidActor(actor) {
		return domain.ErrInvalid
	}
	if operation != "message.send" && operation != "session.new" && operation != "session.reset_shared" {
		return domain.ErrDenied
	}
	if (s.Partition == domain.PerUser && actor != s.PrincipalID) || (operation == "session.new" && s.Partition != domain.PerUser) || (operation == "session.reset_shared" && s.Partition != domain.Shared) {
		return domain.ErrDenied
	}
	if e := a.routes.AuthorizeSessionRoute(ctx, tx, s, actor); e != nil {
		return e
	}
	var epoch, tenant, provider, blocked, policyID, policyDigest string
	var policyRevision int64
	var enabled bool
	var generation int64
	var policyRaw, sessionRaw, quotaRaw []byte
	var start, until, now time.Time
	e := tx.QueryRow(ctx, `SELECT source_epoch,tenant_id,provider,generation,account_enabled,policy_jsonb,session_policy_jsonb,quota_policy_jsonb,read_started_at,fresh_until,blocked_reason,policy_id,policy_revision,policy_digest FROM worker_authorization_snapshots WHERE scope_id=$1 AND account_id=$2 FOR SHARE`, a.scope, s.AccountID).Scan(&epoch, &tenant, &provider, &generation, &enabled, &policyRaw, &sessionRaw, &quotaRaw, &start, &until, &blocked, &policyID, &policyRevision, &policyDigest)
	if e != nil || epoch != a.epoch || tenant != s.TenantID || provider != s.Provider || blocked != "" {
		return ErrAuthorizationNotReady
	}
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil || now.Before(start) || !now.Before(until) {
		return ErrAuthorizationNotReady
	}
	// Preserve original JSONB document bytes through the public envelope decoder.
	raw, e := json.Marshal(struct {
		SchemaVersion int             `json:"schema_version"`
		ScopeID       string          `json:"scope_id"`
		SourceEpoch   string          `json:"source_epoch"`
		Policy        json.RawMessage `json:"policy"`
	}{1, a.scope, a.epoch, policyRaw})
	if e != nil {
		return ErrAuthorizationNotReady
	}
	decoded, e := wire.DecodeAccessPolicyResolveResponse(raw)
	if e != nil {
		return ErrAuthorizationNotReady
	}
	p := decoded.Policy
	if p.PolicyID != policyID || p.Revision != policyRevision || p.Digest != policyDigest || p.TenantID != tenant || p.AccountID != s.AccountID || p.Provider != provider || !until.Equal(start.Add(time.Duration(p.Body.AuthorizationMaxAgeMS)*time.Millisecond)) {
		return ErrAuthorizationNotReady
	}
	if !enabled || p.Body.AccessMode == "DENY_ALL" {
		return domain.ErrDenied
	}
	if p.Body.AccessMode != "ALLOWLIST" {
		return ErrAuthorizationNotReady
	}
	var state string
	e = tx.QueryRow(ctx, `SELECT state FROM worker_authorization_principals WHERE scope_id=$1 AND account_id=$2 AND tenant_id=$3 AND provider=$4 AND generation=$5 AND principal_id=$6`, a.scope, s.AccountID, s.TenantID, s.Provider, generation, actor).Scan(&state)
	if errors.Is(e, pgx.ErrNoRows) || state == "REVOKED" {
		return domain.ErrDenied
	}
	if e != nil || state != "ACTIVE" {
		return ErrAuthorizationNotReady
	}
	if !slices.Contains(p.Body.AllowedPrincipalIDs, actor) || !slices.Contains(p.Body.AllowedOperations, operation) || (s.ConversationKind == "group" && !slices.Contains(p.Body.AllowedConversationIDs, s.ConversationID)) {
		return domain.ErrDenied
	}
	constraints, e := shared.CheckPolicyDependencies(p, sessionRaw, quotaRaw, nil)
	if errors.Is(e, shared.ErrDependencyDisabled) || errors.Is(e, shared.ErrDependencyDenied) {
		return domain.ErrDenied
	}
	if e != nil {
		return ErrAuthorizationNotReady
	}
	if constraints.SessionPolicy.ID != s.PolicyID || constraints.SessionPolicy.Revision != s.PolicyRevision || constraints.SessionPolicy.Digest != s.PolicyDigest || constraints.Partition != s.Partition {
		return domain.ErrDenied
	}
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil || now.Before(start) || !now.Before(until) {
		return ErrAuthorizationNotReady
	}
	return nil
}

var _ Authorizer = (*CurrentAuthorizer)(nil)
