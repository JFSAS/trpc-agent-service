package domain

import "time"

// ExternalPrincipalIdentity is scoped to one tenant and one ChannelAccount.
// ExternalUserID comes from an authenticated provider event, never display names,
// role text, or a caller-selected tenant. Equal provider IDs on different accounts
// are separate identities. Association with a Control login is a separate action.
type ExternalPrincipalIdentity struct {
	TenantID       string   `json:"tenant_id"`
	Provider       Provider `json:"provider"`
	AccountID      string   `json:"account_id"`
	ExternalUserID string   `json:"external_user_id"`
}

type PrincipalState string

const (
	PrincipalActive  PrincipalState = "ACTIVE"
	PrincipalRevoked PrincipalState = "REVOKED"
)

// ExternalPrincipal is a Control-owned identity record, not an access grant.
// Creating an ACTIVE principal does not create tenant membership or authorize a
// Run. Published policy and fresh runtime projections must independently allow it.
// No provider token, login credential, or presentation name belongs in this record.
type ExternalPrincipal struct {
	Identity  ExternalPrincipalIdentity `json:"identity"`
	ID        string                    `json:"principal_id"`
	State     PrincipalState            `json:"state"`
	Revision  int64                     `json:"revision"`
	CreatedBy string                    `json:"created_by"`
	CreatedAt time.Time                 `json:"created_at"`
	UpdatedAt time.Time                 `json:"updated_at"`
}

const PrincipalRevisionConflict = "CHANNEL_PRINCIPAL_REVISION_CONFLICT"

// NewExternalPrincipal derives tenant/account/provider from the owner-loaded
// account. This creates an identity only; policy publication authorizes use.
func NewExternalPrincipal(account Account, id, actor, externalUserID string, now time.Time) (ExternalPrincipal, error) {
	if err := account.Validate(); err != nil {
		return ExternalPrincipal{}, err
	}
	if !ValidID(id) || !ValidID(actor) || now.IsZero() {
		return ExternalPrincipal{}, failure(InputInvalid, "")
	}
	external, err := normalizeExternalUserID(account.Provider, externalUserID)
	if err != nil {
		return ExternalPrincipal{}, err
	}
	return ExternalPrincipal{Identity: ExternalPrincipalIdentity{TenantID: account.TenantID, Provider: account.Provider, AccountID: account.ID, ExternalUserID: external}, ID: id, State: PrincipalActive, Revision: 1, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}, nil
}

func normalizeExternalUserID(provider Provider, value string) (string, error) {
	// Both currently supported providers use the same bounded opaque/decimal
	// identity representation here. Remap the field; never expose an input value.
	canonical, err := NormalizeProviderAccountID(provider, value)
	if err != nil {
		return "", failure(InputInvalid, "/external_user_id")
	}
	return canonical, nil
}

func (p ExternalPrincipal) Validate() error {
	identity := p.Identity
	canonical, err := normalizeExternalUserID(identity.Provider, identity.ExternalUserID)
	if err != nil || canonical != identity.ExternalUserID || !ValidID(identity.TenantID) || !ValidID(identity.AccountID) || !ValidID(p.ID) || !ValidID(p.CreatedBy) || !ValidVersion(p.Revision) || !validPrincipalState(p.State) || p.CreatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) {
		return failure(SourceIntegrity, "")
	}
	return nil
}

// SetState is an owner-command transition, not a new registration. Revocation
// keeps the identity key. Even a no-op requires the current expected revision.
// The repository must compare this revision again while holding its row lock.
func (p ExternalPrincipal) SetState(expected int64, state PrincipalState, now time.Time) (ExternalPrincipal, bool, error) {
	if err := p.Validate(); err != nil {
		return p, false, err
	}
	if !ValidVersion(expected) || expected != p.Revision {
		return p, false, failure(PrincipalRevisionConflict, "/expected_principal_revision")
	}
	if !validPrincipalState(state) || now.IsZero() || now.Before(p.UpdatedAt) {
		return p, false, failure(InputInvalid, "")
	}
	if state == p.State {
		return p, false, nil
	}
	revision, err := NextVersion(p.Revision)
	if err != nil {
		return p, false, err
	}
	p.State, p.Revision, p.UpdatedAt = state, revision, now
	return p, true, nil
}

func validPrincipalState(state PrincipalState) bool {
	return state == PrincipalActive || state == PrincipalRevoked
}
