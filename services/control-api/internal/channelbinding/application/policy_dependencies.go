package application

import (
	"context"
	"encoding/json"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

type PublishedDefinitionReader interface {
	ReadPublishedDefinition(context.Context, string, string, domain.PolicyRevisionReference) (wire.PolicyDefinitionDocument, error)
}

type PolicyDependenciesResponse struct {
	SchemaVersion int                                `json:"schema_version"`
	ScopeID       string                             `json:"scope_id"`
	SourceEpoch   string                             `json:"source_epoch"`
	Policy        domain.ChannelAccessPolicyRevision `json:"policy"`
	Session       wire.PolicyDefinitionDocument      `json:"session"`
	Quota         wire.PolicyDefinitionDocument      `json:"quota"`
}

// PolicyDependencyService resolves only dependencies named by an exact access
// policy authorized through RuntimeService. No request can override the tenant,
// dependency kind or dependency reference. Immutable reads grant no freshness.
type PolicyDependencyService struct {
	runtime *RuntimeService
	reader  PublishedDefinitionReader
}

func NewPolicyDependencyService(runtime *RuntimeService, reader PublishedDefinitionReader) (*PolicyDependencyService, error) {
	if runtime == nil || reader == nil {
		return nil, ErrDependencyUnavailable
	}
	return &PolicyDependencyService{runtime: runtime, reader: reader}, nil
}
func (s *PolicyDependencyService) Resolve(ctx context.Context, p WorkloadPrincipal, in PolicyResolveRequest) (PolicyDependenciesResponse, error) {
	zero := PolicyDependenciesResponse{}
	access, e := s.runtime.ResolveAccessPolicy(ctx, p, in)
	if e != nil {
		return zero, e
	}
	raw, e := json.Marshal(access)
	if e != nil {
		return zero, ErrDependencyUnavailable
	}
	verified, e := wire.DecodeAccessPolicyResolveResponse(raw)
	if e != nil {
		return zero, ErrDependencyUnavailable
	}
	if verified.Policy.Body.AccessMode == "DENY_ALL" {
		return zero, ErrPolicyReferenceDenied
	}
	ref := func(v wire.PolicyReference) domain.PolicyRevisionReference {
		return domain.PolicyRevisionReference{ID: v.ID, Revision: v.Revision, Digest: v.Digest}
	}
	session, e := s.reader.ReadPublishedDefinition(ctx, verified.Policy.TenantID, "session", ref(verified.Policy.Body.SessionPolicy))
	if e != nil {
		return zero, e
	}
	quota, e := s.reader.ReadPublishedDefinition(ctx, verified.Policy.TenantID, "quota", ref(verified.Policy.Body.TenantQuota))
	if e != nil {
		return zero, e
	}
	pair, e := shared.MatchPolicyDependencies(verified.Policy, session, quota)
	if e != nil {
		return zero, ErrDependencyUnavailable
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	return PolicyDependenciesResponse{SchemaVersion: 1, ScopeID: access.ScopeID, SourceEpoch: access.SourceEpoch, Policy: access.Policy, Session: pair.Session, Quota: pair.Quota}, nil
}

// ResolvePolicyDependencies is optional for old composition; missing owner reads
// fail closed without weakening the existing workload capability check.
func (s *RuntimeService) ResolvePolicyDependencies(ctx context.Context, p WorkloadPrincipal, in PolicyResolveRequest) (PolicyDependenciesResponse, error) {
	if e := s.authorizationReader(p); e != nil {
		return PolicyDependenciesResponse{}, e
	}
	resolver, e := NewPolicyDependencyService(s, s.definitions)
	if e != nil {
		return PolicyDependenciesResponse{}, e
	}
	return resolver.Resolve(ctx, p, in)
}
