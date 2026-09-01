package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant/domain"
)

type AddMemberCommand struct {
	TenantID    string
	ActorUserID string
	UserID      string
}

func (s *Service) AddMember(ctx context.Context, command AddMemberCommand) (domain.Membership, error) {
	actor, err := s.requireMembership(ctx, command.TenantID, command.ActorUserID)
	if err != nil {
		return domain.Membership{}, err
	}
	if !actor.Membership.CanManageMembers() {
		return domain.Membership{}, ErrTenantForbidden
	}
	active, err := s.deps.Accounts.IsActiveAccount(ctx, command.UserID)
	if err != nil {
		return domain.Membership{}, fmt.Errorf("resolve member account: %w", err)
	}
	if !active {
		return domain.Membership{}, ErrAccountUnavailable
	}
	id, err := s.deps.NewMembershipID()
	if err != nil {
		return domain.Membership{}, fmt.Errorf("generate membership id: %w", err)
	}
	membership := domain.Membership{
		ID: id, TenantID: command.TenantID, UserID: command.UserID,
		Role: domain.MembershipRoleMember, CreatedBy: command.ActorUserID,
		CreatedAt: s.deps.Now().UTC(),
	}
	if err := s.deps.Store.CreateMembership(ctx, membership); err != nil {
		if errors.Is(err, ErrMembershipExists) {
			return domain.Membership{}, ErrMembershipExists
		}
		return domain.Membership{}, fmt.Errorf("create membership: %w", err)
	}
	return membership, nil
}

type RemoveMemberCommand struct {
	TenantID    string
	ActorUserID string
	UserID      string
}

func (s *Service) RemoveMember(ctx context.Context, command RemoveMemberCommand) error {
	actor, err := s.requireMembership(ctx, command.TenantID, command.ActorUserID)
	if err != nil {
		return err
	}
	if !actor.Membership.CanManageMembers() {
		return ErrTenantForbidden
	}
	target, err := s.deps.Store.GetMembership(ctx, command.TenantID, command.UserID)
	if errors.Is(err, ErrMembershipNotFound) {
		return ErrMembershipNotFound
	}
	if err != nil {
		return fmt.Errorf("find target membership: %w", err)
	}
	if target.Role == domain.MembershipRoleOwner {
		return ErrOwnerRequiresTransfer
	}
	if err := s.deps.Store.DeleteMembership(ctx, command.TenantID, command.UserID); err != nil {
		return fmt.Errorf("delete membership: %w", err)
	}
	return nil
}

func (s *Service) requireMembership(
	ctx context.Context,
	tenantID, userID string,
) (domain.TenantMembership, error) {
	state, err := s.deps.Store.GetTenantMembership(ctx, tenantID, userID)
	if errors.Is(err, ErrMembershipNotFound) || errors.Is(err, ErrTenantNotFound) {
		return domain.TenantMembership{}, ErrTenantForbidden
	}
	if err != nil {
		return domain.TenantMembership{}, fmt.Errorf("authorize tenant membership: %w", err)
	}
	if state.Tenant.Status != domain.TenantStatusActive {
		return domain.TenantMembership{}, ErrTenantForbidden
	}
	return state, nil
}
