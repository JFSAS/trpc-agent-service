package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant/domain"
)

func TestProvisionTenantCreatesActiveTenantWithOwnerAtomically(t *testing.T) {
	now := time.Date(2026, time.August, 31, 14, 0, 0, 0, time.UTC)
	store := &tenantStoreStub{}
	accounts := &accountLookupStub{active: true}
	service := application.NewService(application.Dependencies{
		Store:           store,
		Accounts:        accounts,
		NewTenantID:     func() (string, error) { return "tenant-1", nil },
		NewMembershipID: func() (string, error) { return "membership-1", nil },
		Now:             func() time.Time { return now },
	})

	result, err := service.ProvisionTenant(context.Background(), application.ProvisionTenantCommand{
		Slug: "team-a", Name: "Team A", OwnerUserID: "user-1", ActorUserID: "operator-1",
	})
	if err != nil {
		t.Fatalf("ProvisionTenant() error = %v", err)
	}
	if result.Tenant.ID != "tenant-1" || result.Owner.ID != "membership-1" {
		t.Fatalf("result = %#v", result)
	}
	if result.Tenant.Status != domain.TenantStatusActive || result.Owner.Role != domain.MembershipRoleOwner {
		t.Fatalf("tenant/owner state = %#v/%#v", result.Tenant, result.Owner)
	}
	if store.provisionCalls != 1 {
		t.Fatalf("provision calls = %d, want 1", store.provisionCalls)
	}
}

func TestOwnerAddsExistingActiveMember(t *testing.T) {
	store := &tenantStoreStub{memberships: map[string]domain.Membership{
		"owner": {TenantID: "tenant-1", UserID: "owner", Role: domain.MembershipRoleOwner},
	}}
	service := application.NewService(application.Dependencies{
		Store:           store,
		Accounts:        &accountLookupStub{active: true},
		NewTenantID:     func() (string, error) { return "tenant-2", nil },
		NewMembershipID: func() (string, error) { return "membership-2", nil },
		Now:             time.Now,
	})

	membership, err := service.AddMember(context.Background(), application.AddMemberCommand{
		TenantID: "tenant-1", ActorUserID: "owner", UserID: "user-2",
	})
	if err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}
	if membership.Role != domain.MembershipRoleMember || membership.UserID != "user-2" {
		t.Fatalf("membership = %#v", membership)
	}
}

func TestMemberCannotManageMemberships(t *testing.T) {
	store := &tenantStoreStub{memberships: map[string]domain.Membership{
		"member": {TenantID: "tenant-1", UserID: "member", Role: domain.MembershipRoleMember},
	}}
	service := application.NewService(application.Dependencies{
		Store: store, Accounts: &accountLookupStub{active: true},
		NewTenantID:     func() (string, error) { return "tenant-2", nil },
		NewMembershipID: func() (string, error) { return "membership-2", nil }, Now: time.Now,
	})

	_, err := service.AddMember(context.Background(), application.AddMemberCommand{
		TenantID: "tenant-1", ActorUserID: "member", UserID: "user-2",
	})
	if !errors.Is(err, application.ErrTenantForbidden) {
		t.Fatalf("AddMember() error = %v, want ErrTenantForbidden", err)
	}
}

func TestOwnerMembershipCannotBeRemovedByOrdinaryRemove(t *testing.T) {
	store := &tenantStoreStub{memberships: map[string]domain.Membership{
		"owner":  {TenantID: "tenant-1", UserID: "owner", Role: domain.MembershipRoleOwner},
		"owner2": {TenantID: "tenant-1", UserID: "owner2", Role: domain.MembershipRoleOwner},
	}}
	service := application.NewService(application.Dependencies{
		Store: store, Accounts: &accountLookupStub{},
		NewTenantID:     func() (string, error) { return "tenant-id", nil },
		NewMembershipID: func() (string, error) { return "membership-id", nil }, Now: time.Now,
	})

	err := service.RemoveMember(context.Background(), application.RemoveMemberCommand{
		TenantID: "tenant-1", ActorUserID: "owner", UserID: "owner2",
	})
	if !errors.Is(err, application.ErrOwnerRequiresTransfer) {
		t.Fatalf("RemoveMember() error = %v, want ErrOwnerRequiresTransfer", err)
	}
}

type tenantStoreStub struct {
	provisionCalls int
	tenant         domain.Tenant
	owner          domain.Membership
	memberships    map[string]domain.Membership
}

func (s *tenantStoreStub) ProvisionTenant(
	_ context.Context,
	tenant domain.Tenant,
	owner domain.Membership,
) error {
	s.provisionCalls++
	s.tenant = tenant
	s.owner = owner
	return nil
}

func (s *tenantStoreStub) GetTenantMembership(
	_ context.Context,
	_, userID string,
) (domain.TenantMembership, error) {
	membership, ok := s.memberships[userID]
	if !ok {
		return domain.TenantMembership{}, application.ErrMembershipNotFound
	}
	return domain.TenantMembership{
		Tenant:     domain.Tenant{ID: membership.TenantID, Status: domain.TenantStatusActive},
		Membership: membership,
	}, nil
}

func (s *tenantStoreStub) GetMembership(
	_ context.Context,
	_, userID string,
) (domain.Membership, error) {
	membership, ok := s.memberships[userID]
	if !ok {
		return domain.Membership{}, application.ErrMembershipNotFound
	}
	return membership, nil
}

func (s *tenantStoreStub) CreateMembership(_ context.Context, membership domain.Membership) error {
	if s.memberships == nil {
		s.memberships = make(map[string]domain.Membership)
	}
	s.memberships[membership.UserID] = membership
	return nil
}

func (s *tenantStoreStub) DeleteMembership(context.Context, string, string) error { return nil }

func (s *tenantStoreStub) ListMyTenants(context.Context, string) ([]domain.TenantMembership, error) {
	return nil, nil
}

func (s *tenantStoreStub) ListMembers(context.Context, string) ([]domain.Membership, error) {
	return nil, nil
}

func (s *tenantStoreStub) ListTenants(context.Context, application.Page) (application.TenantPage, error) {
	return application.TenantPage{}, nil
}

type accountLookupStub struct {
	active bool
	err    error
}

func (s *accountLookupStub) IsActiveAccount(context.Context, string) (bool, error) {
	return s.active, s.err
}
