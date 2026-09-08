package application

import (
	"context"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/domain"
)

// ManagedCredentialTargetResolver returns only the digest of a tenant-authorized,
// immutable Memory or Redis Session target. Profile never receives connection secrets.
type ManagedCredentialTargetResolver interface {
	ResolveStorageCredentialAudience(context.Context, string, string, uint64, string) (string, error)
}

func (s *Service) bindManagedStorageCredentialTargets(ctx context.Context, tenant string, input ProfileWrite, next *domain.Spec, previous domain.Spec) error {
	for name, r := range next.Storage {
		if r.Kind != domain.StorageKindManagedMemory && r.Kind != domain.StorageKindManagedSession {
			continue
		}
		action := input.Credentials["storage"][name]["dsn_password"]
		old := previous.Storage[name]
		if old.Kind != r.Kind {
			old = domain.StorageResource{}
		}
		// Empty/cleared Drafts remain representable; active use requires a credential
		// at Deployment compilation. No configured password means no target binding.
		if action.Action == "clear" || (action.Action != "replace" && old.DSNCredentialID == "") {
			continue
		}
		if s.deps.ManagedCredentialTargets == nil {
			return ErrManagedBackend
		}
		digest, err := s.deps.ManagedCredentialTargets.ResolveStorageCredentialAudience(ctx, tenant, r.BackendID, r.BackendRevision, r.Kind.Role())
		if err != nil || !validSHA256Digest(digest) {
			return ErrManagedBackend
		}
		r.CredentialAudienceDigest = digest
		next.Storage[name] = r
	}
	return nil
}
