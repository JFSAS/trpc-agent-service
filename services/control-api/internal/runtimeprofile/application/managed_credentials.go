package application

import (
	"context"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/domain"
)

// ManagedCredentialTargetResolver returns only the digest of a tenant-authorized,
// immutable PostgreSQL or Redis Memory target. Profile never receives connection secrets.
type ManagedCredentialTargetResolver interface {
	ResolveMemoryCredentialAudience(context.Context, string, string, uint64) (string, error)
}

func (s *Service) bindManagedMemoryCredentialTargets(ctx context.Context, tenant string, input ProfileWrite, next *domain.Spec, previous domain.Spec) error {
	for name, r := range next.Storage {
		if r.Kind != domain.StorageKindManagedMemory {
			continue
		}
		action := input.Credentials["storage"][name]["dsn_password"]
		old := previous.Storage[name]
		if old.Kind != domain.StorageKindManagedMemory {
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
		digest, err := s.deps.ManagedCredentialTargets.ResolveMemoryCredentialAudience(ctx, tenant, r.BackendID, r.BackendRevision)
		if err != nil || !validSHA256Digest(digest) {
			return ErrManagedBackend
		}
		r.CredentialAudienceDigest = digest
		next.Storage[name] = r
	}
	return nil
}
