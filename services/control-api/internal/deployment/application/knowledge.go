package application

import (
	"context"
	"errors"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/deployment/domain"
	profileapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/application"
	"strings"
	"unicode/utf8"
)

const MaxKnowledgeTextBytes = 1 << 20

var (
	ErrKnowledgeInvalid     = errors.New("invalid knowledge import")
	ErrKnowledgeForbidden   = errors.New("knowledge forbidden")
	ErrKnowledgeTooLarge    = errors.New("knowledge import too large")
	ErrKnowledgeUnavailable = errors.New("knowledge backend unavailable")
)

type KnowledgeCredentialResolver interface {
	ResolveKnowledgeForOwner(context.Context, profileapp.OwnerKnowledgeCredentialsCommand) (profileapp.CredentialBatch, error)
}
type KnowledgeBackend interface {
	ImportKnowledge(context.Context, KnowledgeRequest) (KnowledgeResult, error)
}
type KnowledgeImportCommand struct {
	TenantID, DeploymentID, ActorUserID, Resource, Name, Text string
	RevisionNumber                                            int64
}
type KnowledgeSecrets struct {
	QdrantAPIKey    string `json:"qdrant_api_key"`
	EmbeddingAPIKey string `json:"embedding_api_key"`
}
type KnowledgeRequest struct {
	TenantID             string           `json:"tenant_id"`
	ManifestRef          string           `json:"manifest_ref"`
	ManifestDigest       string           `json:"manifest_digest"`
	DeploymentRevisionID string           `json:"deployment_revision_id"`
	Resource             string           `json:"resource"`
	Name                 string           `json:"name"`
	Text                 string           `json:"text"`
	Operation            string           `json:"operation"`
	Credentials          KnowledgeSecrets `json:"credentials"`
}
type KnowledgeResult struct {
	Documents int `json:"documents"`
}

func (s *Service) ImportKnowledge(ctx context.Context, c KnowledgeImportCommand) (KnowledgeResult, error) {
	if err := s.authorizeOwner(ctx, c.TenantID, c.ActorUserID); err != nil {
		return KnowledgeResult{}, err
	}
	if c.RevisionNumber < 1 || !ValidArtifactName(c.Name) || c.Resource == "" || len(c.Resource) > 64 || c.Text == "" || !utf8.ValidString(c.Text) || strings.ContainsRune(c.Text, 0) {
		return KnowledgeResult{}, ErrKnowledgeInvalid
	}
	if len(c.Text) > MaxKnowledgeTextBytes {
		return KnowledgeResult{}, ErrKnowledgeTooLarge
	}
	if s.deps.KnowledgeBackend == nil || s.deps.KnowledgeCredentials == nil {
		return KnowledgeResult{}, ErrKnowledgeUnavailable
	}
	published, err := s.GetDeploymentRevision(ctx, c.TenantID, c.DeploymentID, c.ActorUserID, c.RevisionNumber)
	if err != nil {
		return KnowledgeResult{}, err
	}
	content, err := domain.ValidateManifestContent(published.Manifest.Content, published.Manifest.ContentDigest)
	if err != nil {
		return KnowledgeResult{}, ErrPublicationIntegrity
	}
	k, ok := content.Resources.Knowledge[c.Resource]
	if !ok || k.Backend == nil || k.Credential == nil {
		return KnowledgeResult{}, ErrKnowledgeForbidden
	}
	if int64(len(c.Text)) > k.Backend.Limits.MaxBytes {
		return KnowledgeResult{}, ErrKnowledgeTooLarge
	}
	uses := []profileapp.CredentialUse{{CredentialID: k.Credential.CredentialID, Purpose: "qdrant_api_key", AudienceDigest: k.Credential.AudienceDigest}, {CredentialID: k.Embedding.Credential.CredentialID, Purpose: "embedding_api_key", AudienceDigest: k.Embedding.Credential.AudienceDigest}}
	batch, err := s.deps.KnowledgeCredentials.ResolveKnowledgeForOwner(ctx, profileapp.OwnerKnowledgeCredentialsCommand{CheckProfileCredentialsCommand: profileapp.CheckProfileCredentialsCommand{TenantID: c.TenantID, ProfileID: content.Sources.Profile.ProfileID, ActorUserID: c.ActorUserID, ProfileRevisionNumber: content.Sources.Profile.RevisionNumber, Uses: uses}, ResourceName: c.Resource})
	if err != nil {
		return KnowledgeResult{}, ErrKnowledgeUnavailable
	}
	defer batch.Clear()
	if len(batch.Credentials) != 2 {
		return KnowledgeResult{}, ErrKnowledgeUnavailable
	}
	request := KnowledgeRequest{TenantID: c.TenantID, ManifestRef: published.Manifest.ID, ManifestDigest: published.Manifest.ContentDigest, DeploymentRevisionID: published.Revision.ID, Resource: c.Resource, Name: c.Name, Text: c.Text, Operation: "import", Credentials: KnowledgeSecrets{QdrantAPIKey: string(batch.Credentials[0].Value), EmbeddingAPIKey: string(batch.Credentials[1].Value)}}
	out, err := s.deps.KnowledgeBackend.ImportKnowledge(ctx, request)
	request.Credentials = KnowledgeSecrets{}
	if err != nil {
		return KnowledgeResult{}, err
	}
	if out.Documents < 0 {
		return KnowledgeResult{}, ErrKnowledgeUnavailable
	}
	return out, nil
}
