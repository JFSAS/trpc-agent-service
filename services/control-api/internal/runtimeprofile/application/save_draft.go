package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/domain"
)

func (s *Service) SaveProfileDraft(
	ctx context.Context,
	command SaveProfileDraftCommand,
) (domain.ProfileDraft, domain.ValidationReport, error) {
	if err := s.authorize(ctx, command.TenantID, command.ActorUserID); err != nil {
		return domain.ProfileDraft{}, domain.ValidationReport{}, err
	}
	if command.ExpectedRevision <= 0 {
		return domain.ProfileDraft{}, domain.ValidationReport{}, ErrDraftRevisionConflict
	}
	report := domain.ValidateDraftForStorage(command.Spec, command.ExpectedRevision)
	if !report.Valid {
		return domain.ProfileDraft{}, report, ErrRuntimeProfileSpecInvalid
	}
	draft := domain.ProfileDraft{
		TenantID: command.TenantID, ProfileID: command.ProfileID,
		Revision: command.ExpectedRevision + 1,
		Spec:     append([]byte(nil), command.Spec...), UpdatedBy: command.ActorUserID,
		UpdatedAt: s.deps.Now().UTC(),
	}
	if err := s.deps.Store.SaveProfileDraft(ctx, draft, command.ExpectedRevision); err != nil {
		switch {
		case errors.Is(err, ErrDraftRevisionConflict):
			return domain.ProfileDraft{}, domain.ValidationReport{}, ErrDraftRevisionConflict
		case errors.Is(err, ErrRuntimeProfileNotFound):
			return domain.ProfileDraft{}, domain.ValidationReport{}, ErrRuntimeProfileNotFound
		default:
			return domain.ProfileDraft{}, domain.ValidationReport{}, fmt.Errorf("save runtime profile draft: %w", err)
		}
	}
	return draft.Clone(), report, nil
}
