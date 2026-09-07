// Package policyowner translates immutable owner definitions into the Channel
// publisher's read ports. It neither queries another owner's tables directly nor
// interprets drafts or arbitrary JSON as verified policy facts.
package policyowner

import (
	"context"
	"errors"

	channel "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	channeldomain "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
	owner "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/application"
	ownerdomain "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

type Reader struct{ owner owner.PublishedRevisionReader }

func NewReader(source owner.PublishedRevisionReader) (*Reader, error) {
	if source == nil {
		return nil, channel.ErrDependencyUnavailable
	}
	return &Reader{owner: source}, nil
}
func (r *Reader) read(ctx context.Context, tenant string, kind ownerdomain.Kind, ref channeldomain.PolicyRevisionReference) (ownerdomain.Revision, error) {
	doc, err := r.owner.ReadExact(ctx, tenant, kind, ref.ID, ref.Revision)
	if err != nil {
		if ctx.Err() != nil {
			return ownerdomain.Revision{}, ctx.Err()
		}
		if errors.Is(err, owner.ErrNotFound) {
			return ownerdomain.Revision{}, channel.ErrPolicyReferenceDenied
		}
		return ownerdomain.Revision{}, channel.ErrDependencyUnavailable
	}
	if doc.Validate() != nil || doc.TenantID != tenant || doc.Kind != kind || doc.PolicyID != ref.ID || doc.Revision != ref.Revision {
		return ownerdomain.Revision{}, channel.ErrDependencyUnavailable
	}
	if doc.Digest != ref.Digest {
		return ownerdomain.Revision{}, channel.ErrPolicyReferenceDenied
	}
	return doc, nil
}
func (r *Reader) ReadPublishedSessionPolicy(ctx context.Context, tenant string, ref channeldomain.PolicyRevisionReference) (channel.PublishedSessionPolicy, error) {
	doc, err := r.read(ctx, tenant, ownerdomain.Session, ref)
	if err != nil {
		return channel.PublishedSessionPolicy{}, err
	}
	return channel.PublishedSessionPolicy{TenantID: doc.TenantID, Reference: ref, Enabled: doc.Definition.Enabled, Partition: doc.Definition.Session.Partition}, nil
}
func (r *Reader) ReadPublishedQuotaPolicy(ctx context.Context, tenant string, ref channeldomain.PolicyRevisionReference) (channel.PublishedQuotaPolicy, error) {
	doc, err := r.read(ctx, tenant, ownerdomain.Quota, ref)
	if err != nil {
		return channel.PublishedQuotaPolicy{}, err
	}
	q := doc.Definition.Quota
	return channel.PublishedQuotaPolicy{TenantID: doc.TenantID, Reference: ref, Enabled: doc.Definition.Enabled, PublicLimited: q.PublicLimited, MaxConcurrentRuns: q.MaxConcurrentRuns, MaxRunsPerMinute: q.MaxRunsPerMinute}, nil
}

var _ channel.PublishedSessionPolicyReader = (*Reader)(nil)
var _ channel.PublishedQuotaPolicyReader = (*Reader)(nil)
