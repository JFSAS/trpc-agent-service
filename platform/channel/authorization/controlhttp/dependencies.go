package controlhttp

import (
	"context"
	"encoding/json"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
)

// ReadPolicyDependencies fetches exact immutable dependencies for an already
// verified access policy. It never renews any current snapshot expiry or grants
// quota capacity. Ordinary messages/tools must not call this synchronously.
func (c *Client) ReadPolicyDependencies(ctx context.Context, policy wire.AccessPolicyDocument) (shared.PolicyDependencies, error) {
	zero := shared.PolicyDependencies{}
	if ctx == nil {
		return zero, ErrInvalid
	}
	in := wire.AccessPolicyResolveRequest{SchemaVersion: 1, AccountID: policy.AccountID, Reference: wire.PolicyReference{ID: policy.PolicyID, Revision: policy.Revision, Digest: policy.Digest}}
	body, e := json.Marshal(in)
	if e != nil || wire.Validate("access-policy-resolve-request.schema.json", body) != nil {
		return zero, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, e := c.post(ctx, c.origin+"/internal/v1/channel-policy-dependencies:resolve", body, wire.MaxPolicyDependenciesBytes, true)
	if e != nil {
		return zero, e
	}
	out, e := wire.DecodePolicyDependenciesResponse(raw)
	if e != nil {
		return zero, ErrIntegrity
	}
	p := out.Policy
	if out.ScopeID != c.scope || out.SourceEpoch != c.epoch || p.TenantID != policy.TenantID || p.AccountID != policy.AccountID || p.Provider != policy.Provider || p.PolicyID != policy.PolicyID || p.Revision != policy.Revision || p.Digest != policy.Digest {
		return zero, ErrIntegrity
	}
	pair, e := shared.MatchPolicyDependencies(p, out.Session, out.Quota)
	if e != nil {
		return zero, ErrIntegrity
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	return pair, nil
}
