package application

import (
	"context"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant/domain"
)

func (s *Service) ListMyTenants(ctx context.Context, userID string) ([]domain.TenantMembership, error) {
	return s.deps.Store.ListMyTenants(ctx, userID)
}

func (s *Service) GetTenant(
	ctx context.Context,
	tenantID, userID string,
) (domain.TenantMembership, error) {
	return s.requireMembership(ctx, tenantID, userID)
}

func (s *Service) ListMembers(
	ctx context.Context,
	tenantID, userID string,
) ([]domain.Membership, error) {
	if _, err := s.requireMembership(ctx, tenantID, userID); err != nil {
		return nil, err
	}
	return s.deps.Store.ListMembers(ctx, tenantID)
}

func (s *Service) ListTenants(ctx context.Context, page Page) (TenantPage, error) {
	if page.Offset < 0 {
		page.Offset = 0
	}
	if page.Limit <= 0 {
		page.Limit = 20
	}
	if page.Limit > 100 {
		page.Limit = 100
	}
	return s.deps.Store.ListTenants(ctx, page)
}
