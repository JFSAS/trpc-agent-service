package authorization

import (
	"encoding/json"
	"errors"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

var ErrDependencyIntegrity = errors.New("CHANNEL_AUTHORIZATION_DEPENDENCY_INTEGRITY")

// PolicyDependencies is exact immutable content referenced by a verified access
// policy. It establishes no current authorization, enabled status, quota capacity
// or tool isolation. Callers must still apply those separate runtime gates.
type PolicyDependencies struct {
	Session wire.PolicyDefinitionDocument
	Quota   wire.PolicyDefinitionDocument
}

// MatchPolicyDependencies checks full content even for typed/untrusted inputs,
// then binds tenant, kind, ID, revision and digest independently for both owners.
// A newer publication never silently substitutes for an exact reference.
// The returned value is detached from input pointers.
func MatchPolicyDependencies(policy wire.AccessPolicyDocument, session, quota wire.PolicyDefinitionDocument) (PolicyDependencies, error) {
	zero := PolicyDependencies{}
	check := func(d wire.PolicyDefinitionDocument, kind string, ref wire.PolicyReference) (wire.PolicyDefinitionDocument, error) {
		raw, e := json.Marshal(d)
		if e != nil {
			return wire.PolicyDefinitionDocument{}, ErrDependencyIntegrity
		}
		verified, e := wire.DecodePolicyDefinitionDocument(raw)
		if e != nil || verified.TenantID != policy.TenantID || verified.Kind != kind || verified.PolicyID != ref.ID || verified.Revision != ref.Revision || verified.Digest != ref.Digest {
			return wire.PolicyDefinitionDocument{}, ErrDependencyIntegrity
		}
		return verified, nil
	}
	s, e := check(session, "session", policy.Body.SessionPolicy)
	if e != nil {
		return zero, e
	}
	q, e := check(quota, "quota", policy.Body.TenantQuota)
	if e != nil {
		return zero, e
	}
	return PolicyDependencies{Session: s, Quota: q}, nil
}
