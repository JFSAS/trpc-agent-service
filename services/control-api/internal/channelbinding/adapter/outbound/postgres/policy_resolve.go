package postgresadapter

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

func (s *Store) ReadAccessPolicy(ctx context.Context, scope, account string, ref domain.PolicyRevisionReference) (application.PolicyResolveResponse, error) {
	if scope != s.options.ScopeID {
		return application.PolicyResolveResponse{}, application.ErrWorkloadDenied
	}
	if !domain.ValidID(account) || !domain.ValidID(ref.ID) || !domain.ValidVersion(ref.Revision) || !domain.ValidDigest(ref.Digest) {
		return application.PolicyResolveResponse{}, application.ErrPolicyNotFound
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return application.PolicyResolveResponse{}, dbError(err)
	}
	defer rollback(tx)
	catalog, err := s.catalog(ctx, tx, "")
	if err != nil {
		return application.PolicyResolveResponse{}, err
	}
	var raw []byte
	var tenant, provider, digest string
	err = tx.QueryRow(ctx, `SELECT p.document_jsonb,p.tenant_id,p.provider,p.digest FROM channel_access_policy_revisions p JOIN channel_accounts a ON a.tenant_id=p.tenant_id AND a.id=p.account_id AND a.provider=p.provider WHERE a.scope_id=$1 AND a.id=$2 AND p.policy_id=$3 AND p.revision=$4`, scope, account, ref.ID, ref.Revision).Scan(&raw, &tenant, &provider, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.PolicyResolveResponse{}, application.ErrPolicyNotFound
	}
	if err != nil {
		return application.PolicyResolveResponse{}, dbError(err)
	}
	doc, err := domain.DecodeAccessPolicyRevision(raw)
	if err != nil || doc.TenantID != tenant || string(doc.Provider) != provider || doc.AccountID != account || doc.PolicyID != ref.ID || doc.Revision != ref.Revision || doc.Digest != digest {
		return application.PolicyResolveResponse{}, integrity()
	}
	if digest != ref.Digest {
		return application.PolicyResolveResponse{}, application.ErrPolicyNotFound
	}
	if err = tx.Commit(ctx); err != nil {
		return application.PolicyResolveResponse{}, dbError(err)
	}
	return application.PolicyResolveResponse{SchemaVersion: 1, ScopeID: scope, SourceEpoch: catalog.epoch, Policy: doc}, nil
}
