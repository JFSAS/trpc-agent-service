package application

import (
	"context"
	"errors"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

const (
	SessionPerUserInConversation = "per_user_in_conversation"
	SessionSharedConversation    = "shared_conversation"
)

// Owner readers must verify the owner document digest before projecting these
// fields. They read immutable published revisions, never drafts or client claims.
type PublishedSessionPolicy struct {
	TenantID  string
	Reference domain.PolicyRevisionReference
	Enabled   bool
	Partition string
}
type PublishedQuotaPolicy struct {
	TenantID          string
	Reference         domain.PolicyRevisionReference
	Enabled           bool
	PublicLimited     bool
	MaxConcurrentRuns int64
	MaxRunsPerMinute  int64
}
type PublishedSessionPolicyReader interface {
	ReadPublishedSessionPolicy(context.Context, string, domain.PolicyRevisionReference) (PublishedSessionPolicy, error)
}
type PublishedQuotaPolicyReader interface {
	ReadPublishedQuotaPolicy(context.Context, string, domain.PolicyRevisionReference) (PublishedQuotaPolicy, error)
}

// PublicToolIsolation is supplied by the tool authorization owner. It must
// establish that this channel's PUBLIC_LIMITED path excludes sensitive tools in
// the runtime policy, not merely that a configuration flag was requested.
type PublicToolIsolation interface {
	RequirePublicToolIsolation(context.Context, Actor, domain.Account, domain.AccessPolicyBody) error
}

// PublicQuotaCeiling is explicit operator configuration. No guessed "low" quota
// or silent unlimited default is installed by the validator.
type PublicQuotaCeiling struct {
	MaxConcurrentRuns int64
	MaxRunsPerMinute  int64
}
type PolicyReferenceValidator struct {
	sessions PublishedSessionPolicyReader
	quotas   PublishedQuotaPolicyReader
	tools    PublicToolIsolation
	ceiling  PublicQuotaCeiling
}

func NewPolicyReferenceValidator(sessions PublishedSessionPolicyReader, quotas PublishedQuotaPolicyReader, tools PublicToolIsolation, ceiling PublicQuotaCeiling) (*PolicyReferenceValidator, error) {
	if sessions == nil || quotas == nil || tools == nil || !domain.ValidVersion(ceiling.MaxConcurrentRuns) || !domain.ValidVersion(ceiling.MaxRunsPerMinute) {
		return nil, ErrDependencyUnavailable
	}
	return &PolicyReferenceValidator{sessions: sessions, quotas: quotas, tools: tools, ceiling: ceiling}, nil
}
func (v *PolicyReferenceValidator) ValidatePublishedPolicy(ctx context.Context, actor Actor, account domain.Account, input domain.AccessPolicyBody) error {
	if !domain.ValidID(actor.UserID) || actor.TenantID != account.TenantID {
		return ErrPermissionDenied
	}
	if err := account.Validate(); err != nil {
		return ErrDependencyUnavailable
	}
	body, err := domain.NormalizeAccessPolicyBody(input)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if body.AccessMode == domain.AccessDenyAll {
		return nil
	}
	session, err := v.sessions.ReadPublishedSessionPolicy(ctx, actor.TenantID, body.SessionPolicy)
	if err != nil {
		return policyOwnerError(ctx, err)
	}
	if session.TenantID != actor.TenantID || session.Reference != body.SessionPolicy {
		return ErrDependencyUnavailable
	}
	if session.Partition != SessionPerUserInConversation && session.Partition != SessionSharedConversation {
		return ErrDependencyUnavailable
	}
	if !session.Enabled {
		return ErrPolicyReferenceDenied
	}
	if body.AccessMode == domain.AccessPublicLimited && session.Partition != SessionPerUserInConversation {
		return ErrPolicyReferenceDenied
	}
	quota, err := v.quotas.ReadPublishedQuotaPolicy(ctx, actor.TenantID, body.TenantQuota)
	if err != nil {
		return policyOwnerError(ctx, err)
	}
	if quota.TenantID != actor.TenantID || quota.Reference != body.TenantQuota {
		return ErrDependencyUnavailable
	}
	if !quota.Enabled {
		return ErrPolicyReferenceDenied
	}
	if quota.MaxConcurrentRuns < 0 || quota.MaxRunsPerMinute < 0 || quota.MaxConcurrentRuns > domain.MaxVersion || quota.MaxRunsPerMinute > domain.MaxVersion {
		return ErrDependencyUnavailable
	}
	if body.AccessMode == domain.AccessPublicLimited {
		// Zero (including an owner's "unlimited" representation) cannot satisfy a
		// bounded public quota. Both dimensions and explicit public intent are needed.
		if !quota.PublicLimited || quota.MaxConcurrentRuns < 1 || quota.MaxRunsPerMinute < 1 || quota.MaxConcurrentRuns > v.ceiling.MaxConcurrentRuns || quota.MaxRunsPerMinute > v.ceiling.MaxRunsPerMinute {
			return ErrPolicyReferenceDenied
		}
		if err = v.tools.RequirePublicToolIsolation(ctx, actor, account, body); err != nil {
			return policyOwnerError(ctx, err)
		}
	}
	return ctx.Err()
}
func policyOwnerError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrPolicyReferenceDenied) {
		return ErrPolicyReferenceDenied
	}
	return ErrDependencyUnavailable
}

var _ PublishedPolicyValidator = (*PolicyReferenceValidator)(nil)
