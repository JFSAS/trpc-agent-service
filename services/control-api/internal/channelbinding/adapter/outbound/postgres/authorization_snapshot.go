package postgresadapter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

// ReadAuthorizationManifest captures current policy head, account gate and the
// complete principal digest in one repeatable-read transaction. Principal rows
// are streamed, never accumulated into an unlimited in-memory array.
func (s *Store) ReadAuthorizationManifest(ctx context.Context, scope, account string) (wire.AuthorizationSnapshotManifest, error) {
	if ctx == nil {
		return wire.AuthorizationSnapshotManifest{}, application.ErrDependencyUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return wire.AuthorizationSnapshotManifest{}, dbError(err)
	}
	defer rollback(tx)
	identity, a, err := s.authorizationIdentity(ctx, tx, scope, account)
	if err != nil {
		return wire.AuthorizationSnapshotManifest{}, err
	}
	var captured time.Time
	if err = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&captured); err != nil {
		return wire.AuthorizationSnapshotManifest{}, dbError(err)
	}
	var raw []byte
	var policyID, digest string
	var revision int64
	err = tx.QueryRow(ctx, `SELECT h.policy_id,h.latest_revision,r.digest,r.document_jsonb FROM channel_access_policies h JOIN channel_access_policy_revisions r ON r.tenant_id=h.tenant_id AND r.account_id=h.account_id AND r.provider=h.provider AND r.policy_id=h.policy_id AND r.revision=h.latest_revision WHERE h.tenant_id=$1 AND h.account_id=$2 AND h.provider=$3`, a.TenantID, a.ID, a.Provider).Scan(&policyID, &revision, &digest, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return wire.AuthorizationSnapshotManifest{}, application.ErrPolicyNotFound
	}
	if err != nil {
		return wire.AuthorizationSnapshotManifest{}, dbError(err)
	}
	policy, err := domain.DecodeAccessPolicyRevision(raw)
	if err != nil || policy.TenantID != a.TenantID || policy.AccountID != a.ID || policy.Provider != a.Provider || policy.PolicyID != policyID || policy.Revision != revision || policy.Digest != digest {
		return wire.AuthorizationSnapshotManifest{}, integrity()
	}
	rows, err := tx.Query(ctx, `SELECT `+principalColumns+` FROM channel_principal_bindings WHERE tenant_id=$1 AND account_id=$2 AND provider=$3 ORDER BY principal_id COLLATE "C"`, a.TenantID, a.ID, a.Provider)
	if err != nil {
		return wire.AuthorizationSnapshotManifest{}, dbError(err)
	}
	proof := wire.NewPrincipalSetDigest()
	for rows.Next() {
		p, e := scanPrincipal(rows)
		if e != nil {
			rows.Close()
			return wire.AuthorizationSnapshotManifest{}, e
		}
		if e = proof.Add(authorizationPrincipal(p)); e != nil {
			rows.Close()
			return wire.AuthorizationSnapshotManifest{}, integrity()
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return wire.AuthorizationSnapshotManifest{}, dbError(err)
	}
	var body domain.AccessPolicyBody
	if json.Unmarshal(policy.Body, &body) != nil {
		return wire.AuthorizationSnapshotManifest{}, integrity()
	}
	count, root := proof.Result()
	m := wire.AuthorizationSnapshotManifest{AuthorizationSnapshotIdentity: identity, AccountRevision: a.Revision, AccountEnabled: a.Enabled, Policy: wire.PolicyReference{ID: policyID, Revision: revision, Digest: digest}, CapturedAt: captured.UTC(), AuthorizationMaxAgeMS: body.AuthorizationMaxAgeMS, PrincipalCount: count, PrincipalDigest: root}
	encoded, _ := json.Marshal(m)
	if _, err = wire.DecodeAuthorizationSnapshotManifest(encoded); err != nil {
		return wire.AuthorizationSnapshotManifest{}, integrity()
	}
	if err = tx.Commit(ctx); err != nil {
		return wire.AuthorizationSnapshotManifest{}, dbError(err)
	}
	return m, nil
}

// ReadAuthorizationPage reads a bounded keyset page only if the SQL generation
// still equals the caller's manifest. Epoch comes from a verified catalog, not
// an untrusted cursor. No page call extends the manifest capture time.
func (s *Store) ReadAuthorizationPage(ctx context.Context, scope, epoch, account string, generation int64, after string) (wire.AuthorizationSnapshotPage, error) {
	if ctx == nil {
		return wire.AuthorizationSnapshotPage{}, application.ErrDependencyUnavailable
	}
	if epoch != s.options.SourceEpoch {
		return wire.AuthorizationSnapshotPage{}, application.ErrEpochMismatch
	}
	if !domain.ValidVersion(generation) || (after != "" && !domain.ValidID(after)) {
		return wire.AuthorizationSnapshotPage{}, application.ErrAuthorizationSnapshotChanged
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return wire.AuthorizationSnapshotPage{}, dbError(err)
	}
	defer rollback(tx)
	identity, a, err := s.authorizationIdentity(ctx, tx, scope, account)
	if err != nil {
		return wire.AuthorizationSnapshotPage{}, err
	}
	if identity.Generation != generation {
		return wire.AuthorizationSnapshotPage{}, application.ErrAuthorizationSnapshotChanged
	}
	rows, err := tx.Query(ctx, `SELECT `+principalColumns+` FROM channel_principal_bindings WHERE tenant_id=$1 AND account_id=$2 AND provider=$3 AND principal_id COLLATE "C">$4 COLLATE "C" ORDER BY principal_id COLLATE "C" LIMIT $5`, a.TenantID, a.ID, a.Provider, after, wire.AuthorizationPageSize+1)
	if err != nil {
		return wire.AuthorizationSnapshotPage{}, dbError(err)
	}
	page := wire.AuthorizationSnapshotPage{AuthorizationSnapshotIdentity: identity, AfterPrincipalID: after, Principals: make([]wire.AuthorizationPrincipal, 0, wire.AuthorizationPageSize), Complete: true}
	for rows.Next() {
		p, e := scanPrincipal(rows)
		if e != nil {
			rows.Close()
			return wire.AuthorizationSnapshotPage{}, e
		}
		if len(page.Principals) == wire.AuthorizationPageSize {
			page.Complete = false
			page.NextPrincipalID = page.Principals[len(page.Principals)-1].PrincipalID
			break
		}
		page.Principals = append(page.Principals, authorizationPrincipal(p))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return wire.AuthorizationSnapshotPage{}, dbError(err)
	}
	raw, _ := json.Marshal(page)
	if _, err = wire.DecodeAuthorizationSnapshotPage(raw); err != nil {
		return wire.AuthorizationSnapshotPage{}, integrity()
	}
	if err = tx.Commit(ctx); err != nil {
		return wire.AuthorizationSnapshotPage{}, dbError(err)
	}
	return page, nil
}
func authorizationPrincipal(p domain.ExternalPrincipal) wire.AuthorizationPrincipal {
	return wire.AuthorizationPrincipal{PrincipalID: p.ID, ExternalUserID: p.Identity.ExternalUserID, State: string(p.State), Revision: p.Revision}
}
func (s *Store) authorizationIdentity(ctx context.Context, tx pgx.Tx, scope, account string) (wire.AuthorizationSnapshotIdentity, domain.Account, error) {
	var zero wire.AuthorizationSnapshotIdentity
	if scope != s.options.ScopeID {
		return zero, domain.Account{}, application.ErrWorkloadDenied
	}
	if !domain.ValidID(account) {
		return zero, domain.Account{}, application.ErrAccountNotFound
	}
	cat, err := s.catalog(ctx, tx, "")
	if err != nil {
		return zero, domain.Account{}, err
	}
	a, err := scanAccount(tx.QueryRow(ctx, `SELECT `+accountColumns+` FROM channel_accounts WHERE scope_id=$1 AND id=$2`, scope, account))
	if err != nil {
		return zero, domain.Account{}, err
	}
	// The existing tenant owner adapter uses SELECT FOR SHARE. These read
	// transactions therefore intentionally do not set PostgreSQL READ ONLY.
	allowed, err := s.auth.AuthorizeActiveTenant(ctx, tx, a.TenantID)
	if err != nil {
		return zero, domain.Account{}, dbError(err)
	}
	if !allowed {
		return zero, domain.Account{}, application.ErrAccountNotFound
	}
	var generation int64
	err = tx.QueryRow(ctx, `SELECT generation FROM channel_authorization_generations WHERE tenant_id=$1 AND account_id=$2`, a.TenantID, a.ID).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !domain.ValidVersion(generation) {
		return zero, domain.Account{}, integrity()
	}
	if err != nil {
		return zero, domain.Account{}, dbError(err)
	}
	return wire.AuthorizationSnapshotIdentity{SchemaVersion: 1, ScopeID: scope, SourceEpoch: cat.epoch, TenantID: a.TenantID, AccountID: a.ID, Provider: string(a.Provider), Generation: generation}, a, nil
}
