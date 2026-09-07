package application

import (
	"context"
	"errors"
	"slices"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

const PolicyProjectionConsumer = "channel_policy_projection"
const WorkerAuthorizationConsumer = "worker_channel_authorization"

func CanReadAuthorization(p WorkloadPrincipal) bool {
	return slices.Contains(p.Consumers, PolicyProjectionConsumer) || slices.Contains(p.Consumers, WorkerAuthorizationConsumer)
}
func WorkerAuthorizationOnly(p WorkloadPrincipal) bool {
	return slices.Contains(p.Consumers, WorkerAuthorizationConsumer)
}

var ErrPolicyNotFound = errors.New("CHANNEL_POLICY_NOT_FOUND")

type PolicyResolveRequest struct {
	SchemaVersion int                            `json:"schema_version"`
	AccountID     string                         `json:"account_id"`
	Reference     domain.PolicyRevisionReference `json:"reference"`
}

// PolicyResolveResponse contains one immutable document, not a current-state
// authorization snapshot. It conveys no principal revocation or freshness fence.
type PolicyResolveResponse struct {
	SchemaVersion int                                `json:"schema_version"`
	ScopeID       string                             `json:"scope_id"`
	SourceEpoch   string                             `json:"source_epoch"`
	Policy        domain.ChannelAccessPolicyRevision `json:"policy"`
}

func (s *RuntimeService) ResolveAccessPolicy(ctx context.Context, p WorkloadPrincipal, in PolicyResolveRequest) (PolicyResolveResponse, error) {
	if err := s.authorize(p); err != nil {
		return PolicyResolveResponse{}, err
	}
	if !CanReadAuthorization(p) {
		return PolicyResolveResponse{}, ErrWorkloadDenied
	}
	if in.SchemaVersion != 1 || !domain.ValidID(in.AccountID) || !domain.ValidID(in.Reference.ID) || !domain.ValidVersion(in.Reference.Revision) || !domain.ValidDigest(in.Reference.Digest) {
		return PolicyResolveResponse{}, invalid("/reference")
	}
	out, err := s.store.ReadAccessPolicy(ctx, p.ScopeID, in.AccountID, in.Reference)
	if err != nil {
		return PolicyResolveResponse{}, err
	}
	if out.SchemaVersion != 1 || out.ScopeID != s.scope || out.SourceEpoch != s.epoch {
		return PolicyResolveResponse{}, ErrEpochMismatch
	}
	d := out.Policy
	if d.AccountID != in.AccountID || d.PolicyID != in.Reference.ID || d.Revision != in.Reference.Revision || d.Digest != in.Reference.Digest || d.Validate() != nil {
		return PolicyResolveResponse{}, &domain.Error{Code: domain.SourceIntegrity}
	}
	return out, nil
}
