package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

var ErrAuthorizationSnapshotChanged = errors.New("CHANNEL_AUTHORIZATION_SNAPSHOT_CHANGED")

type AuthorizationManifestRequest struct {
	SchemaVersion int    `json:"schema_version"`
	AccountID     string `json:"account_id"`
}
type AuthorizationPageRequest struct {
	SchemaVersion    int    `json:"schema_version"`
	AccountID        string `json:"account_id"`
	SourceEpoch      string `json:"source_epoch"`
	Generation       int64  `json:"generation"`
	AfterPrincipalID string `json:"after_principal_id"`
}

func (s *RuntimeService) authorizationReader(p WorkloadPrincipal) error {
	if err := s.authorize(p); err != nil {
		return err
	}
	if !slices.Contains(p.Consumers, PolicyProjectionConsumer) {
		return ErrWorkloadDenied
	}
	return nil
}
func (s *RuntimeService) ReadAuthorizationManifest(ctx context.Context, p WorkloadPrincipal, in AuthorizationManifestRequest) (wire.AuthorizationSnapshotManifest, error) {
	if err := s.authorizationReader(p); err != nil {
		return wire.AuthorizationSnapshotManifest{}, err
	}
	if in.SchemaVersion != 1 || !domain.ValidID(in.AccountID) {
		return wire.AuthorizationSnapshotManifest{}, invalid("/account_id")
	}
	out, err := s.store.ReadAuthorizationManifest(ctx, p.ScopeID, in.AccountID)
	if err != nil {
		return wire.AuthorizationSnapshotManifest{}, err
	}
	raw, _ := json.Marshal(out)
	if _, err = wire.DecodeAuthorizationSnapshotManifest(raw); err != nil || out.ScopeID != s.scope || out.SourceEpoch != s.epoch || out.AccountID != in.AccountID {
		return wire.AuthorizationSnapshotManifest{}, &domain.Error{Code: domain.SourceIntegrity}
	}
	return out, nil
}
func (s *RuntimeService) ReadAuthorizationPage(ctx context.Context, p WorkloadPrincipal, in AuthorizationPageRequest) (wire.AuthorizationSnapshotPage, error) {
	if err := s.authorizationReader(p); err != nil {
		return wire.AuthorizationSnapshotPage{}, err
	}
	if in.SchemaVersion != 1 || !domain.ValidID(in.AccountID) || !domain.ValidEpoch(in.SourceEpoch) || !domain.ValidVersion(in.Generation) || (in.AfterPrincipalID != "" && !domain.ValidID(in.AfterPrincipalID)) {
		return wire.AuthorizationSnapshotPage{}, invalid("/generation")
	}
	if in.SourceEpoch != s.epoch {
		return wire.AuthorizationSnapshotPage{}, ErrEpochMismatch
	}
	out, err := s.store.ReadAuthorizationPage(ctx, p.ScopeID, in.SourceEpoch, in.AccountID, in.Generation, in.AfterPrincipalID)
	if err != nil {
		return wire.AuthorizationSnapshotPage{}, err
	}
	raw, _ := json.Marshal(out)
	if _, err = wire.DecodeAuthorizationSnapshotPage(raw); err != nil || out.ScopeID != s.scope || out.SourceEpoch != s.epoch || out.AccountID != in.AccountID || out.Generation != in.Generation || out.AfterPrincipalID != in.AfterPrincipalID {
		return wire.AuthorizationSnapshotPage{}, &domain.Error{Code: domain.SourceIntegrity}
	}
	return out, nil
}
