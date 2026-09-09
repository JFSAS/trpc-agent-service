package application

import (
	"context"
	"errors"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/domain"
)

// ResolveArtifactForOwner is an internal management port, never a public secret API.
// Deployment supplies fixed publication uses after its own Owner authorization.
func (s *Service) ResolveArtifactForOwner(ctx context.Context, c CheckProfileCredentialsCommand) (CredentialBatch, error) {
	if err := s.credentialDependencies(); err != nil {
		return CredentialBatch{}, err
	}
	if err := s.authorizeOwner(ctx, c.TenantID, c.ActorUserID); err != nil {
		return CredentialBatch{}, err
	}
	if len(c.Uses) != 2 || c.Uses[0].Purpose != "access_key_id" || c.Uses[1].Purpose != "secret_access_key" || c.Uses[0].AudienceDigest != c.Uses[1].AudienceDigest {
		return CredentialBatch{}, domain.ErrCredentialAssociation
	}
	result := CredentialBatch{TenantID: c.TenantID, ProfileID: c.ProfileID, ProfileRevisionNumber: c.ProfileRevisionNumber}
	err := s.deps.Credentials.WithinProfile(ctx, c.TenantID, c.ProfileID, func(tx CredentialTransaction) error {
		if err := s.authorizeOwner(ctx, c.TenantID, c.ActorUserID); err != nil {
			return err
		}
		records, err := s.checkedCredentialRecords(ctx, tx, c.TenantID, c.ProfileID, c.ProfileRevisionNumber, c.Uses)
		if err != nil {
			return err
		}
		for i, r := range records {
			v, err := s.deps.Cipher.Decrypt(ctx, r.AssociatedData(), r.Ciphertext)
			if err != nil {
				return errors.New("resolve artifact credential")
			}
			result.Credentials = append(result.Credentials, ResolvedCredential{Use: c.Uses[i], CredentialRevision: r.Revision, Value: v})
		}
		return nil
	})
	if err != nil {
		result.Clear()
		return CredentialBatch{}, err
	}
	return result, nil
}
