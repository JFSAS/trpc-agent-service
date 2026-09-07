package domain

import "time"

type TargetSelector struct {
	DeploymentID   string `json:"deployment_id"`
	RevisionNumber int64  `json:"revision_number"`
}
type PublishedTarget struct {
	TenantID             string `json:"tenant_id"`
	DeploymentID         string `json:"deployment_id"`
	RevisionNumber       int64  `json:"revision_number"`
	DeploymentRevisionID string `json:"deployment_revision_id"`
	ManifestID           string `json:"manifest_ref"`
	ManifestDigest       string `json:"manifest_digest"`
}

func (t PublishedTarget) Validate(tenant string) error {
	if t.TenantID != tenant || !ValidID(t.TenantID) || !ValidID(t.DeploymentID) || !ValidVersion(t.RevisionNumber) || !ValidID(t.DeploymentRevisionID) || !ValidID(t.ManifestID) || !ValidDigest(t.ManifestDigest) {
		return failure(TargetIntegrity, "/target")
	}
	return nil
}
func (t PublishedTarget) Selector() TargetSelector {
	return TargetSelector{t.DeploymentID, t.RevisionNumber}
}

type Binding struct {
	TenantID  string          `json:"tenant_id"`
	ID        string          `json:"binding_id"`
	AccountID string          `json:"account_id"`
	Revision  int64           `json:"binding_revision"`
	Enabled   bool            `json:"enabled"`
	Target    PublishedTarget `json:"target"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func NewBinding(id, actor string, a Account, target PublishedTarget, now time.Time) (Binding, error) {
	if !ValidID(id) || !ValidID(actor) {
		return Binding{}, failure(InputInvalid, "")
	}
	if err := target.Validate(a.TenantID); err != nil {
		return Binding{}, err
	}
	return Binding{TenantID: a.TenantID, ID: id, AccountID: a.ID, Revision: 1, Target: target, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}, nil
}
func (b Binding) SetTarget(expected int64, target PublishedTarget, now time.Time) (Binding, bool, error) {
	if !ValidVersion(expected) || expected != b.Revision {
		return b, false, failure(BindingRevisionConflict, "/expected_binding_revision")
	}
	if err := target.Validate(b.TenantID); err != nil {
		return b, false, err
	}
	if target == b.Target {
		return b, false, nil
	}
	revision, err := NextVersion(b.Revision)
	if err != nil {
		return b, false, err
	}
	b.Revision = revision
	b.Target = target
	b.UpdatedAt = now
	return b, true, nil
}
func (b Binding) SetEnabled(expected int64, enabled bool, a Account, credentials []CredentialMeta, now time.Time) (Binding, bool, error) {
	if !ValidVersion(expected) || b.Revision != expected {
		return b, false, failure(BindingRevisionConflict, "/expected_binding_revision")
	}
	if a.TenantID != b.TenantID || a.ID != b.AccountID {
		return b, false, failure(SourceIntegrity, "")
	}
	if enabled {
		if !a.Enabled {
			return b, false, failure(AccountDisabled, "")
		}
		if err := ValidateCredentialSet(a.Provider, credentials, true, a.Config.ReceiveMode); err != nil {
			return b, false, err
		}
		if err := b.Target.Validate(a.TenantID); err != nil {
			return b, false, err
		}
	}
	if enabled == b.Enabled {
		return b, false, nil
	}
	revision, err := NextVersion(b.Revision)
	if err != nil {
		return b, false, err
	}
	b.Revision = revision
	b.Enabled = enabled
	b.UpdatedAt = now
	return b, true, nil
}
