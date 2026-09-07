package authorization

import (
	"errors"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

var ErrDependencyDisabled = errors.New("CHANNEL_AUTHORIZATION_DEPENDENCY_DISABLED")
var ErrDependencyDenied = errors.New("CHANNEL_AUTHORIZATION_DEPENDENCY_DENIED")
var ErrDependencyNotReady = errors.New("CHANNEL_AUTHORIZATION_DEPENDENCY_NOT_READY")

type PublicQuotaCeiling struct{ MaxConcurrentRuns, MaxRunsPerMinute int64 }

// DependencyConstraints are verified published limits, not evidence that a
// session was partitioned or capacity reserved. Zero ordinary limits are kept
// literal; only the quota owner may interpret or reserve them.
type DependencyConstraints struct {
	Partition                           string
	PublicLimited                       bool
	MaxConcurrentRuns, MaxRunsPerMinute int64
	SessionPolicy, TenantQuota          wire.PolicyReference
}

// CheckPolicyDependencies consumes bytes read under the current head's lock.
// Both documents must verify before any disabled/denied decision is definitive.
// The caller owns current epoch, DB-time freshness and principal checks.
func CheckPolicyDependencies(policy wire.AccessPolicyDocument, sessionRaw, quotaRaw []byte, ceiling *PublicQuotaCeiling) (DependencyConstraints, error) {
	zero := DependencyConstraints{}
	s, e := wire.DecodePolicyDefinitionDocument(sessionRaw)
	if e != nil {
		return zero, ErrDependencyIntegrity
	}
	q, e := wire.DecodePolicyDefinitionDocument(quotaRaw)
	if e != nil {
		return zero, ErrDependencyIntegrity
	}
	pair, e := MatchPolicyDependencies(policy, s, q)
	if e != nil {
		return zero, e
	}
	if !pair.Session.Definition.Enabled || !pair.Quota.Definition.Enabled {
		return zero, ErrDependencyDisabled
	}
	partition := pair.Session.Definition.Session.Partition
	limits := pair.Quota.Definition.Quota
	switch policy.Body.AccessMode {
	case "ALLOWLIST":
	case "PUBLIC_LIMITED":
		if partition != "per_user_in_conversation" || !limits.PublicLimited || limits.MaxConcurrentRuns < 1 || limits.MaxRunsPerMinute < 1 {
			return zero, ErrDependencyDenied
		}
		if ceiling == nil || ceiling.MaxConcurrentRuns < 1 || ceiling.MaxRunsPerMinute < 1 || ceiling.MaxConcurrentRuns > 9007199254740991 || ceiling.MaxRunsPerMinute > 9007199254740991 {
			return zero, ErrDependencyNotReady
		}
		if limits.MaxConcurrentRuns > ceiling.MaxConcurrentRuns || limits.MaxRunsPerMinute > ceiling.MaxRunsPerMinute {
			return zero, ErrDependencyDenied
		}
	default:
		return zero, ErrDependencyDenied
	}
	return DependencyConstraints{Partition: partition, PublicLimited: limits.PublicLimited, MaxConcurrentRuns: limits.MaxConcurrentRuns, MaxRunsPerMinute: limits.MaxRunsPerMinute, SessionPolicy: policy.Body.SessionPolicy, TenantQuota: policy.Body.TenantQuota}, nil
}
