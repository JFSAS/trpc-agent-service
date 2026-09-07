package application

import (
	"context"
	"errors"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

// QueryService exposes exact immutable definitions to current tenant OWNERs.
// Internal owner ports remain separate; a reference is not a read capability.
type QueryService struct {
	reader PublishedRevisionReader
	access TenantAccess
}

func NewQueryService(reader PublishedRevisionReader, access TenantAccess) (*QueryService, error) {
	if reader == nil || access == nil {
		return nil, ErrUnavailable
	}
	return &QueryService{reader: reader, access: access}, nil
}
func (s *QueryService) ReadExact(ctx context.Context, a Actor, kind domain.Kind, id string, revision int64) (domain.Revision, error) {
	if !domain.ValidID(a.TenantID) || !domain.ValidID(a.UserID) {
		return domain.Revision{}, ErrPermissionDenied
	}
	allowed, err := s.access.IsActiveOwner(ctx, a.TenantID, a.UserID)
	if err != nil {
		return domain.Revision{}, ErrUnavailable
	}
	if !allowed {
		return domain.Revision{}, ErrPermissionDenied
	}
	if !domain.ValidID(id) || !domain.ValidRevision(revision) || (kind != domain.Session && kind != domain.Quota) {
		return domain.Revision{}, domain.ErrInvalid
	}
	out, err := s.reader.ReadExact(ctx, a.TenantID, kind, id, revision)
	if err != nil {
		if ctx.Err() != nil {
			return domain.Revision{}, ctx.Err()
		}
		if errors.Is(err, ErrNotFound) {
			return domain.Revision{}, ErrNotFound
		}
		if errors.Is(err, ErrIntegrity) {
			return domain.Revision{}, ErrIntegrity
		}
		return domain.Revision{}, ErrUnavailable
	}
	if out.TenantID != a.TenantID || out.Kind != kind || out.PolicyID != id || out.Revision != revision || out.Validate() != nil {
		return domain.Revision{}, ErrIntegrity
	}
	return out, nil
}
