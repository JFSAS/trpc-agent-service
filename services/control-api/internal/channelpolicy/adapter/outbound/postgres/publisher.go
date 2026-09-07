package postgresadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5"
	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

type WriteDB interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}
type TenantAuthorizer interface {
	AuthorizeOwner(context.Context, pgx.Tx, string, string) (bool, error)
}
type PublicationStore struct {
	db   WriteDB
	auth TenantAuthorizer
}

func NewPublicationStore(db WriteDB, auth TenantAuthorizer) (*PublicationStore, error) {
	if db == nil || auth == nil {
		return nil, application.ErrUnavailable
	}
	return &PublicationStore{db: db, auth: auth}, nil
}
func (s *PublicationStore) WithPublish(ctx context.Context, scope application.WriteScope, fn func(application.PublishTransaction) error) error {
	if !domain.ValidID(scope.Actor.TenantID) || !domain.ValidID(scope.Actor.UserID) || !domain.ValidID(scope.PolicyID) || (scope.Kind != domain.Session && scope.Kind != domain.Quota) || fn == nil {
		return application.ErrPermissionDenied
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return writeError(ctx, err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()
	allowed, err := s.auth.AuthorizeOwner(ctx, tx, scope.Actor.TenantID, scope.Actor.UserID)
	if err != nil {
		return writeError(ctx, err)
	}
	if !allowed {
		return application.ErrPermissionDenied
	}
	// Transaction-scoped per-definition locking also serializes the first insert.
	// '/' cannot occur in ValidID, and the namespace is distinct from other owners.
	// A hash collision only causes extra serialization, never a lost CAS check.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "channelpolicy/v1/"+scope.Actor.TenantID+"/"+string(scope.Kind)+"/"+scope.PolicyID); err != nil {
		return writeError(ctx, err)
	}
	writer := &publicationTx{tx: tx, scope: scope}
	if err = fn(writer); err != nil {
		return err
	}
	if writer.published != nil && !writer.receiptSaved {
		return application.ErrIntegrity
	}
	if err = tx.Commit(ctx); err != nil {
		return writeError(ctx, err)
	}
	return nil
}

type publicationTx struct {
	tx           pgx.Tx
	scope        application.WriteScope
	published    *application.PublishResult
	receiptSaved bool
}

func (t *publicationTx) FindReceipt(ctx context.Context, keyHash string) (application.Receipt, bool, error) {
	var r application.Receipt
	r.KeyHash = keyHash
	err := t.tx.QueryRow(ctx, `SELECT mac_key_id,request_mac,result_jsonb,created_by,created_at FROM channel_policy_definition_receipts WHERE tenant_id=$1 AND kind=$2 AND policy_id=$3 AND key_hash=$4`, t.scope.Actor.TenantID, t.scope.Kind, t.scope.PolicyID, keyHash).Scan(&r.MACKeyID, &r.RequestMAC, &r.Result, &r.CreatedBy, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, writeError(ctx, err)
	}
	return r, true, nil
}
func (t *publicationTx) LatestRevision(ctx context.Context) (int64, error) {
	var raw []byte
	var digest string
	err := t.tx.QueryRow(ctx, `SELECT document_jsonb,digest FROM channel_policy_definition_revisions WHERE tenant_id=$1 AND kind=$2 AND policy_id=$3 ORDER BY revision DESC LIMIT 1`, t.scope.Actor.TenantID, t.scope.Kind, t.scope.PolicyID).Scan(&raw, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, writeError(ctx, err)
	}
	doc, err := domain.Decode(raw)
	if err != nil || doc.TenantID != t.scope.Actor.TenantID || doc.PolicyID != t.scope.PolicyID || doc.Kind != t.scope.Kind || doc.Digest != digest {
		return 0, application.ErrIntegrity
	}
	return doc.Revision, nil
}
func (t *publicationTx) Append(ctx context.Context, doc domain.Revision, eventID, auditID string) error {
	if t.published != nil || doc.Validate() != nil || !domain.ValidID(eventID) || !domain.ValidID(auditID) || eventID == auditID {
		return application.ErrIntegrity
	}
	if doc.TenantID != t.scope.Actor.TenantID || doc.PublishedBy != t.scope.Actor.UserID || doc.Kind != t.scope.Kind || doc.PolicyID != t.scope.PolicyID {
		return application.ErrPermissionDenied
	}
	latest, err := t.LatestRevision(ctx)
	if err != nil {
		return err
	}
	if latest >= domain.MaxRevision || doc.Revision != latest+1 {
		return application.ErrRevisionConflict
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return application.ErrIntegrity
	}
	if _, err = t.tx.Exec(ctx, `INSERT INTO channel_policy_definition_revisions(tenant_id,kind,policy_id,revision,digest,document_jsonb,published_by,published_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, doc.TenantID, doc.Kind, doc.PolicyID, doc.Revision, doc.Digest, raw, doc.PublishedBy, doc.PublishedAt); err != nil {
		return writeError(ctx, err)
	}
	for _, event := range []struct{ id, kind string }{{eventID, channelv1.PolicyDefinitionPublishedEvent}, {auditID, channelv1.PolicyDefinitionAuditEvent}} {
		body := channelv1.PolicyDefinitionEvent{SchemaVersion: 1, EventID: event.id, EventType: event.kind, TenantID: doc.TenantID, PolicyKind: string(doc.Kind), PolicyID: doc.PolicyID, Revision: doc.Revision, Digest: doc.Digest, OccurredAt: doc.PublishedAt}
		if event.id == auditID {
			body.PolicyAuditFields = &channelv1.PolicyAuditFields{ActorID: doc.PublishedBy, ActorKind: "CONTROL_USER", Action: "channel.policy_definition.publish", Decision: "PUBLISHED", ReasonCode: "OWNER_POLICY_PUBLICATION"}
		}
		encoded, payloadDigest, err := body.CanonicalJSON()
		if err != nil {
			return application.ErrIntegrity
		}
		if _, err = t.tx.Exec(ctx, `INSERT INTO control_outbox(tenant_id,id,aggregate_type,aggregate_id,aggregate_revision,event_type,schema_version,payload_jsonb,payload_digest,available_at,created_at,updated_at) VALUES($1,$2,'ChannelPolicyDefinition',$3,$4,$5,'1',$6,$7,clock_timestamp(),clock_timestamp(),clock_timestamp())`, doc.TenantID, event.id, string(doc.Kind)+":"+doc.PolicyID, doc.Revision, event.kind, encoded, payloadDigest); err != nil {
			return writeError(ctx, err)
		}
	}
	t.published = &application.PublishResult{TenantID: doc.TenantID, Kind: doc.Kind, PolicyID: doc.PolicyID, Revision: doc.Revision, Digest: doc.Digest, EventID: eventID, AuditEventID: auditID, Distribution: "PENDING"}
	return nil
}
func (t *publicationTx) SaveReceipt(ctx context.Context, r application.Receipt) error {
	if t.receiptSaved || t.published == nil || r.CreatedBy != t.scope.Actor.UserID || r.CreatedAt.IsZero() || len(r.Result) > 2048 || !domain.ValidID(r.MACKeyID) {
		return application.ErrIntegrity
	}
	var result application.PublishResult
	if json.Unmarshal(r.Result, &result) != nil || result != *t.published {
		return application.ErrIntegrity
	}
	canonical, err := jcs.Transform(r.Result)
	if err != nil {
		return application.ErrIntegrity
	}
	expected, err := json.Marshal(result)
	if err != nil {
		return application.ErrIntegrity
	}
	expected, err = jcs.Transform(expected)
	if err != nil || !bytes.Equal(canonical, expected) {
		return application.ErrIntegrity
	}
	_, err = t.tx.Exec(ctx, `INSERT INTO channel_policy_definition_receipts(tenant_id,kind,policy_id,key_hash,mac_key_id,request_mac,result_jsonb,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, t.scope.Actor.TenantID, t.scope.Kind, t.scope.PolicyID, r.KeyHash, r.MACKeyID, r.RequestMAC, r.Result, r.CreatedBy, r.CreatedAt)
	if err == nil {
		t.receiptSaved = true
	}
	return writeError(ctx, err)
}
func writeError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return application.ErrUnavailable
}

var _ application.PublicationStore = (*PublicationStore)(nil)
