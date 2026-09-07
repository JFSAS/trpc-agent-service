package postgresadapter

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

const principalColumns = `tenant_id,provider,account_id,external_user_id,principal_id,state,revision,created_by,created_at,updated_at`

func scanPrincipal(row pgx.Row) (domain.ExternalPrincipal, error) {
	var p domain.ExternalPrincipal
	err := row.Scan(&p.Identity.TenantID, &p.Identity.Provider, &p.Identity.AccountID, &p.Identity.ExternalUserID, &p.ID, &p.State, &p.Revision, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, application.ErrPrincipalNotFound
	}
	if err != nil {
		return domain.ExternalPrincipal{}, dbError(err)
	}
	if err = p.Validate(); err != nil {
		return domain.ExternalPrincipal{}, err
	}
	return p, nil
}
func (t *writeTx) LoadPrincipal(ctx context.Context, accountID, id string) (domain.ExternalPrincipal, error) {
	// Lock the owning account first, using the same scope and tenant fences as routes.
	if _, err := t.lockedAccount(ctx, accountID); err != nil {
		return domain.ExternalPrincipal{}, err
	}
	return scanPrincipal(t.tx.QueryRow(ctx, `SELECT `+principalColumns+` FROM channel_principal_bindings WHERE tenant_id=$1 AND account_id=$2 AND principal_id=$3 FOR UPDATE`, t.scope.Actor.TenantID, accountID, id))
}

// SavePrincipal never upserts an identity: a revoked mapping retains its unique
// key, and only an explicit state transition with the current revision changes it.
func (t *writeTx) SavePrincipal(ctx context.Context, p domain.ExternalPrincipal, expected int64) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.Identity.TenantID != t.scope.Actor.TenantID {
		return application.ErrPermissionDenied
	}
	a, err := t.lockedAccount(ctx, p.Identity.AccountID)
	if err != nil {
		return err
	}
	if a.Account.Provider != p.Identity.Provider {
		return integrity()
	}
	if expected == 0 {
		if p.Revision != 1 || p.State != domain.PrincipalActive || p.CreatedBy != t.scope.Actor.UserID || !p.CreatedAt.Equal(p.UpdatedAt) {
			return integrity()
		}
		// ON CONFLICT has no update action: it cannot reactivate a revoked identity.
		tag, err := t.tx.Exec(ctx, `INSERT INTO channel_principal_bindings (`+principalColumns+`) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, p.Identity.TenantID, p.Identity.Provider, p.Identity.AccountID, p.Identity.ExternalUserID, p.ID, p.State, p.Revision, p.CreatedBy, p.CreatedAt, p.UpdatedAt)
		if err != nil {
			return dbError(err)
		}
		if tag.RowsAffected() != 1 {
			return application.ErrPrincipalIdentityConflict
		}
		return nil
	}
	old, err := t.LoadPrincipal(ctx, p.Identity.AccountID, p.ID)
	if err != nil {
		return err
	}
	next, changed, err := old.SetState(expected, p.State, p.UpdatedAt)
	if err != nil {
		return err
	}
	if next != p {
		return integrity()
	}
	if !changed {
		return nil
	}
	tag, err := t.tx.Exec(ctx, `UPDATE channel_principal_bindings SET state=$4,revision=$5,updated_at=$6 WHERE tenant_id=$1 AND principal_id=$2 AND revision=$3`, p.Identity.TenantID, p.ID, expected, p.State, p.Revision, p.UpdatedAt)
	if err != nil {
		return dbError(err)
	}
	if tag.RowsAffected() != 1 {
		return &domain.Error{Code: domain.PrincipalRevisionConflict}
	}
	return nil
}
