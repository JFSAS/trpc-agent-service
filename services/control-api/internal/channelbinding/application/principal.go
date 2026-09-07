package application

import (
	"context"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

type RegisterPrincipalInput struct {
	ExternalUserID string `json:"external_user_id"`
}
type SetPrincipalStateInput struct {
	ExpectedRevision int64                 `json:"expected_principal_revision"`
	State            domain.PrincipalState `json:"state"`
}

// RegisterExternalPrincipal records an explicit owner-managed identity. It neither
// grants runtime access nor creates a Control membership. Publication is separate.
func (s *Service) RegisterExternalPrincipal(ctx context.Context, actor Actor, accountID, key string, in RegisterPrincipalInput) (CommandResult, error) {
	return s.execute(ctx, actor, "RegisterChannelPrincipal", accountID, key, in, func(context.Context) (mutation, error) {
		if !domain.ValidID(accountID) {
			return nil, invalid("/account_id")
		}
		return func(ctx context.Context, tx Transaction) (CommandResult, error) {
			a, err := tx.LoadAccount(ctx, accountID)
			if err != nil {
				return CommandResult{}, err
			}
			id, err := s.deps.NewID("prn")
			if err != nil {
				return CommandResult{}, ErrDependencyUnavailable
			}
			p, err := domain.NewExternalPrincipal(a.Account, id, actor.UserID, in.ExternalUserID, s.deps.Now())
			if err != nil {
				return CommandResult{}, err
			}
			if err = tx.SavePrincipal(ctx, p, 0); err != nil {
				return CommandResult{}, err
			}
			return principalResult(p), nil
		}, nil
	})
}

// SetExternalPrincipalState changes Control's identity record with CAS. Until
// policy publication/projection is wired, NOT_EMITTED is not runtime revocation.
func (s *Service) SetExternalPrincipalState(ctx context.Context, actor Actor, accountID, principalID, key string, in SetPrincipalStateInput) (CommandResult, error) {
	input := struct {
		AccountID string                 `json:"account_id"`
		State     SetPrincipalStateInput `json:"transition"`
	}{accountID, in}
	return s.execute(ctx, actor, "SetChannelPrincipalState", principalID, key, input, func(context.Context) (mutation, error) {
		if !domain.ValidID(accountID) || !domain.ValidID(principalID) {
			return nil, invalid("")
		}
		return func(ctx context.Context, tx Transaction) (CommandResult, error) {
			if _, err := tx.LoadAccount(ctx, accountID); err != nil {
				return CommandResult{}, err
			}
			old, err := tx.LoadPrincipal(ctx, accountID, principalID)
			if err != nil {
				return CommandResult{}, err
			}
			p, changed, err := old.SetState(in.ExpectedRevision, in.State, s.deps.Now())
			if err != nil {
				return CommandResult{}, err
			}
			if changed {
				if err = tx.SavePrincipal(ctx, p, in.ExpectedRevision); err != nil {
					return CommandResult{}, err
				}
			}
			return principalResult(p), nil
		}, nil
	})
}
func principalResult(p domain.ExternalPrincipal) CommandResult {
	return CommandResult{Principal: &p, Distribution: "NOT_EMITTED"}
}
