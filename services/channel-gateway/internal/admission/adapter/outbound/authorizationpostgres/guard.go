package authorizationpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
)

// VerifyAuthorization shares the installer's head lock until the caller commits.
// It never opens a second transaction or performs remote HTTP. Missing or stale
// dependencies are retryable, while determinate denial is a durable decision.
// PUBLIC_LIMITED remains unknown until isolation/quota dependencies are wired.
func (s *Store) VerifyAuthorization(ctx context.Context, tx pgx.Tx, in domain.Inbound, route domain.RouteSnapshot) (domain.AdmissionAuthorization, error) {
	var a domain.AdmissionAuthorization
	if tx == nil || in.Kind != "text" || route.ValidateFor(in.Key) != nil {
		return a, domain.ErrUnavailable
	}
	var raw []byte
	var enabled bool
	var blocked string
	err := tx.QueryRow(ctx, `SELECT source_epoch,tenant_id,provider,generation,account_enabled,policy_id,policy_revision,policy_digest,policy_jsonb,read_started_at,fresh_until,blocked_reason FROM gateway_authorization_snapshots WHERE scope_id=$1 AND account_id=$2 FOR SHARE`, s.scope, in.Key.AccountID).Scan(&a.SourceEpoch, &a.TenantID, &a.Provider, &a.Generation, &enabled, &a.PolicyID, &a.PolicyRevision, &a.PolicyDigest, &raw, &a.ReadStartedAt, &a.FreshUntil, &blocked)
	if err != nil || a.SourceEpoch != s.epoch || a.TenantID != route.TenantID || a.Provider != in.Key.Provider || blocked != "" {
		return domain.AdmissionAuthorization{}, domain.ErrUnavailable
	}
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&a.EvaluatedAt); err != nil || a.EvaluatedAt.Before(a.ReadStartedAt) || !a.EvaluatedAt.Before(a.FreshUntil) {
		return domain.AdmissionAuthorization{}, domain.ErrUnavailable
	}
	var policy wire.AccessPolicyDocument
	if json.Unmarshal(raw, &policy) != nil {
		return domain.AdmissionAuthorization{}, domain.ErrUnavailable
	}
	envelope, _ := json.Marshal(wire.AccessPolicyResolveResponse{SchemaVersion: 1, ScopeID: s.scope, SourceEpoch: s.epoch, Policy: policy})
	decoded, err := wire.DecodeAccessPolicyResolveResponse(envelope)
	if err != nil || decoded.Policy.TenantID != a.TenantID || decoded.Policy.AccountID != in.Key.AccountID || decoded.Policy.Provider != a.Provider || decoded.Policy.PolicyID != a.PolicyID || decoded.Policy.Revision != a.PolicyRevision || decoded.Policy.Digest != a.PolicyDigest || !a.FreshUntil.Equal(a.ReadStartedAt.Add(time.Duration(policy.Body.AuthorizationMaxAgeMS)*time.Millisecond)) {
		return domain.AdmissionAuthorization{}, domain.ErrUnavailable
	}
	a.ExternalUserID = in.SenderID
	a.BindingID = route.BindingID
	a.RouteGeneration = route.Generation
	a.SchemaVersion = 1
	a.ScopeID = s.scope
	a.AccountID = in.Key.AccountID
	a.Operation = "message.send"
	a.ConversationID = in.ConversationID
	a.ConversationKind = in.ConversationKind
	a.Decision = "DENIED"
	var state string
	err = tx.QueryRow(ctx, `SELECT principal_id,revision,state FROM gateway_authorization_principals WHERE scope_id=$1 AND account_id=$2 AND tenant_id=$3 AND provider=$4 AND generation=$5 AND external_user_id=$6`, s.scope, in.Key.AccountID, a.TenantID, a.Provider, a.Generation, in.SenderID).Scan(&a.PrincipalID, &a.PrincipalRevision, &state)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.AdmissionAuthorization{}, domain.ErrUnavailable
	}
	switch {
	case !enabled:
		a.Reason = "ACCOUNT_DISABLED"
	case policy.Body.AccessMode == "DENY_ALL":
		a.Reason = "ACCESS_DENIED"
	case errors.Is(err, pgx.ErrNoRows):
		if policy.Body.AccessMode == "PUBLIC_LIMITED" {
			return domain.AdmissionAuthorization{}, domain.ErrUnavailable
		}
		a.Reason = "PRINCIPAL_UNKNOWN"
	case state == "REVOKED":
		a.Reason = "PRINCIPAL_REVOKED"
	case state != "ACTIVE":
		return domain.AdmissionAuthorization{}, domain.ErrUnavailable
	case policy.Body.AccessMode != "ALLOWLIST":
		return domain.AdmissionAuthorization{}, domain.ErrUnavailable
	case !slices.Contains(policy.Body.AllowedPrincipalIDs, a.PrincipalID):
		a.Reason = "PRINCIPAL_NOT_ALLOWED"
	case !slices.Contains(policy.Body.AllowedOperations, a.Operation):
		a.Reason = "OPERATION_NOT_ALLOWED"
	case in.ConversationKind != "private" && in.ConversationKind != "group":
		return domain.AdmissionAuthorization{}, domain.ErrUnavailable
	case in.ConversationKind == "group" && !slices.Contains(policy.Body.AllowedConversationIDs, in.ConversationID):
		a.Reason = "CONVERSATION_NOT_ALLOWED"
	default:
		a.Decision = "ALLOW"
	}
	if a.ValidateFor(in, route) != nil {
		return domain.AdmissionAuthorization{}, domain.ErrUnavailable
	}
	return a, nil
}

// Recheck uses DB time after all admission writes; a lock protects replacement,
// not the passage of time. Expiry rolls back Receipt, Admission and both outboxes.
func (s *Store) RecheckAuthorization(ctx context.Context, tx pgx.Tx, a domain.AdmissionAuthorization) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT source_epoch=$3 AND generation=$4 AND blocked_reason='' AND clock_timestamp()>=read_started_at AND clock_timestamp()<fresh_until AND fresh_until=$5 FROM gateway_authorization_snapshots WHERE scope_id=$1 AND account_id=$2`, s.scope, a.AccountID, a.SourceEpoch, a.Generation, a.FreshUntil).Scan(&valid)
	if err != nil || !valid {
		return domain.ErrUnavailable
	}
	return nil
}
