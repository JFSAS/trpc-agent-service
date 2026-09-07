package postgresadapter

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

const policyPublishedEvent = channelv1.AccessPolicyPublishedEvent
const policyAuditEvent = channelv1.AccessPolicyAuditEvent

func (s *Store) WithPolicyWrite(ctx context.Context, scope application.WriteScope, fn func(application.PolicyRevisionTransaction) error) error {
	if fn == nil {
		return application.ErrDependencyUnavailable
	}
	return s.WithWrite(ctx, scope, func(tx application.Transaction) error { return fn(tx.(*writeTx)) })
}
func (t *writeTx) LoadAccessPolicy(ctx context.Context, accountID string) (domain.ChannelAccessPolicyRevision, bool, error) {
	a, err := t.lockedAccount(ctx, accountID)
	if err != nil {
		return domain.ChannelAccessPolicyRevision{}, false, err
	}
	var raw []byte
	var id, digest string
	var revision int64
	err = t.tx.QueryRow(ctx, `SELECT r.document_jsonb,r.policy_id,r.revision,r.digest FROM channel_access_policies h JOIN channel_access_policy_revisions r ON r.tenant_id=h.tenant_id AND r.policy_id=h.policy_id AND r.revision=h.latest_revision WHERE h.tenant_id=$1 AND h.account_id=$2 FOR UPDATE OF h`, t.scope.Actor.TenantID, accountID).Scan(&raw, &id, &revision, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ChannelAccessPolicyRevision{}, false, nil
	}
	if err != nil {
		return domain.ChannelAccessPolicyRevision{}, false, dbError(err)
	}
	p, err := domain.DecodeAccessPolicyRevision(raw)
	if err != nil {
		return p, false, err
	}
	if p.TenantID != a.Account.TenantID || p.AccountID != accountID || p.Provider != a.Account.Provider || p.PolicyID != id || p.Revision != revision || p.Digest != digest {
		return domain.ChannelAccessPolicyRevision{}, false, integrity()
	}
	return p, true, nil
}

// AppendAccessPolicy receives an application-validated publication candidate.
// Notifications carry only exact references, never complete principal lists.
// Revision, head, members and both durable outboxes commit together or not at all.
func (t *writeTx) AppendAccessPolicy(ctx context.Context, p domain.ChannelAccessPolicyRevision, expected int64, eventID, auditID string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.TenantID != t.scope.Actor.TenantID || p.PublishedBy != t.scope.Actor.UserID {
		return application.ErrPermissionDenied
	}
	if !domain.ValidID(eventID) || !domain.ValidID(auditID) || eventID == auditID {
		return integrity()
	}
	if expected < 0 || expected >= domain.MaxVersion || p.Revision != expected+1 {
		return application.ErrPolicyRevisionConflict
	}
	a, err := t.lockedAccount(ctx, p.AccountID)
	if err != nil {
		return err
	}
	if a.Account.Provider != p.Provider {
		return integrity()
	}
	old, found, err := t.LoadAccessPolicy(ctx, p.AccountID)
	if err != nil {
		return err
	}
	if found && (old.Revision != expected || old.PolicyID != p.PolicyID) || !found && expected != 0 {
		return application.ErrPolicyRevisionConflict
	}
	if found && p.PublishedAt.Before(old.PublishedAt) {
		return integrity()
	}
	var body domain.AccessPolicyBody
	if json.Unmarshal(p.Body, &body) != nil {
		return integrity()
	}
	// Canonical body ordering gives deterministic principal lock ordering. The
	// existing account lock serializes this with principal revocation commands.
	for _, id := range body.AllowedPrincipalIDs {
		principal, err := t.LoadPrincipal(ctx, p.AccountID, id)
		if err != nil {
			return err
		}
		if principal.State != domain.PrincipalActive {
			return application.ErrPermissionDenied
		}
	}
	if found {
		tag, err := t.tx.Exec(ctx, `UPDATE channel_access_policies SET latest_revision=$4 WHERE tenant_id=$1 AND account_id=$2 AND latest_revision=$3`, p.TenantID, p.AccountID, expected, p.Revision)
		if err != nil {
			return dbError(err)
		}
		if tag.RowsAffected() != 1 {
			return application.ErrPolicyRevisionConflict
		}
	} else {
		_, err = t.tx.Exec(ctx, `INSERT INTO channel_access_policies(tenant_id,account_id,provider,policy_id,latest_revision) VALUES($1,$2,$3,$4,$5)`, p.TenantID, p.AccountID, p.Provider, p.PolicyID, p.Revision)
		if err != nil {
			return dbError(err)
		}
	}
	raw, _, err := domain.CanonicalJSON(p)
	if err != nil {
		return err
	}
	_, err = t.tx.Exec(ctx, `INSERT INTO channel_access_policy_revisions(tenant_id,account_id,provider,policy_id,revision,digest,document_jsonb,published_by,published_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, p.TenantID, p.AccountID, p.Provider, p.PolicyID, p.Revision, p.Digest, raw, p.PublishedBy, p.PublishedAt)
	if err != nil {
		return dbError(err)
	}
	for _, id := range body.AllowedPrincipalIDs {
		_, err = t.tx.Exec(ctx, `INSERT INTO channel_access_policy_principals(tenant_id,account_id,provider,policy_id,revision,principal_id) VALUES($1,$2,$3,$4,$5,$6)`, p.TenantID, p.AccountID, p.Provider, p.PolicyID, p.Revision, id)
		if err != nil {
			return dbError(err)
		}
	}
	for _, item := range []struct{ id, kind string }{{eventID, policyPublishedEvent}, {auditID, policyAuditEvent}} {
		// The audit contains the owner actor and immutable policy reference, not the
		// access list or provider user IDs. No relay consumes these new types yet.
		payload := channelv1.AccessPolicyEvent{SchemaVersion: 1, EventID: item.id, EventType: item.kind, ScopeID: t.scope.ScopeID, SourceEpoch: t.store.options.SourceEpoch, TenantID: p.TenantID, AccountID: p.AccountID, Provider: string(p.Provider), PolicyID: p.PolicyID, PolicyRevision: p.Revision, PolicyDigest: p.Digest, OccurredAt: p.PublishedAt}
		if item.kind == policyAuditEvent {
			payload.PolicyAuditFields = &channelv1.PolicyAuditFields{ActorID: p.PublishedBy, ActorKind: "CONTROL_USER", Action: "channel.access_policy.publish", Decision: "PUBLISHED", ReasonCode: "OWNER_POLICY_PUBLICATION"}
		}
		encoded, digest, err := payload.CanonicalJSON()
		if err != nil {
			return err
		}
		_, err = t.tx.Exec(ctx, `INSERT INTO control_outbox(tenant_id,id,aggregate_type,aggregate_id,aggregate_revision,event_type,schema_version,payload_jsonb,payload_digest,available_at,created_at,updated_at) VALUES($1,$2,'ChannelAccessPolicy',$3,$4,$5,'1',$6,$7,clock_timestamp(),clock_timestamp(),clock_timestamp())`, p.TenantID, item.id, p.PolicyID, p.Revision, item.kind, encoded, digest)
		if err != nil {
			return dbError(err)
		}
	}
	return nil
}

var _ application.PolicyRevisionStore = (*Store)(nil)
var _ application.PolicyRevisionTransaction = (*writeTx)(nil)
