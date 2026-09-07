package postgresadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/adapter/outbound/credentialcrypto"
	"strings"
	"testing"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/adapter/outbound/policyowner"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
	ownerpg "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/adapter/outbound/postgres"
	ownerapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/application"
	ownerdomain "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
	"github.com/liuzengh/trpc-agent-service/services/control-api/migrations"
)

func TestPolicyOwnerReadersUseRealPublishedDocumentsAgainstPostgreSQL(t *testing.T) {
	store, _, pool, a := policyPG(t)
	ctx := context.Background()
	sql, err := migrations.Files.ReadFile("0007_channel_policy_definitions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	session, err := ownerdomain.NewRevision(a.TenantID, "session-a", testActor.UserID, ownerdomain.Session, 1, ownerdomain.DefaultSessionDefinition(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	quota, err := ownerdomain.NewRevision(a.TenantID, "quota-a", testActor.UserID, ownerdomain.Quota, 1, ownerdomain.Definition{Enabled: true, Quota: &ownerdomain.QuotaDefinition{PublicLimited: true, MaxConcurrentRuns: 1, MaxRunsPerMinute: 5}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	insert := func(doc ownerdomain.Revision) {
		t.Helper()
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO channel_policy_definition_revisions(tenant_id,kind,policy_id,revision,digest,document_jsonb,published_by,published_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, doc.TenantID, doc.Kind, doc.PolicyID, doc.Revision, doc.Digest, raw, doc.PublishedBy, doc.PublishedAt); err != nil {
			t.Fatal(err)
		}
	}
	// Normal definitions are created through owner commands. Raw insertion below
	// is retained only for explicit corruption injection.
	author := definitionPublisher(t, pool)
	actor := ownerapp.Actor{TenantID: testActor.TenantID, UserID: testActor.UserID}
	if _, err = author.Publish(ctx, actor, session.Kind, session.PolicyID, "session-definition", ownerapp.PublishInput{Definition: session.Definition}); err != nil {
		t.Fatal(err)
	}
	if _, err = author.Publish(ctx, actor, quota.Kind, quota.PolicyID, "quota-definition", ownerapp.PublishInput{Definition: quota.Definition}); err != nil {
		t.Fatal(err)
	}
	source, err := ownerpg.NewReader(pool)
	if err != nil {
		t.Fatal(err)
	}
	session, err = source.ReadExact(ctx, a.TenantID, ownerdomain.Session, session.PolicyID, 1)
	if err != nil {
		t.Fatal(err)
	}
	quota, err = source.ReadExact(ctx, a.TenantID, ownerdomain.Quota, quota.PolicyID, 1)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := policyowner.NewReader(source)
	if err != nil {
		t.Fatal(err)
	}
	sr := domain.PolicyRevisionReference{ID: session.PolicyID, Revision: 1, Digest: session.Digest}
	qr := domain.PolicyRevisionReference{ID: quota.PolicyID, Revision: 1, Digest: quota.Digest}
	loaded, err := reader.ReadPublishedSessionPolicy(ctx, a.TenantID, sr)
	if err != nil || loaded.Partition != application.SessionPerUserInConversation {
		t.Fatal("real Session document", loaded, err)
	}
	q, err := reader.ReadPublishedQuotaPolicy(ctx, a.TenantID, qr)
	if err != nil || q.MaxRunsPerMinute != 5 || !q.PublicLimited {
		t.Fatal("real Quota document", q, err)
	}
	wrong := sr
	wrong.Digest = "sha256:" + strings.Repeat("f", 64)
	if _, err = reader.ReadPublishedSessionPolicy(ctx, a.TenantID, wrong); !errors.Is(err, application.ErrPolicyReferenceDenied) {
		t.Fatal("wrong digest", err)
	}
	if _, err = reader.ReadPublishedSessionPolicy(ctx, "tnt_b", sr); !errors.Is(err, application.ErrPolicyReferenceDenied) {
		t.Fatal("cross tenant", err)
	}
	if _, err = reader.ReadPublishedQuotaPolicy(ctx, a.TenantID, sr); !errors.Is(err, application.ErrPolicyReferenceDenied) {
		t.Fatal("kind substitution", err)
	}
	if _, err = source.ReadExact(ctx, a.TenantID, ownerdomain.Session, sr.ID, 2); !errors.Is(err, ownerapp.ErrNotFound) {
		t.Fatal("no latest fallback", err)
	}
	for _, stmt := range []string{`UPDATE channel_policy_definition_revisions SET digest=digest`, `DELETE FROM channel_policy_definition_revisions`} {
		if _, err = pool.Exec(ctx, stmt); err == nil {
			t.Fatal("immutable definitions changed")
		}
	}
	// A syntactically valid but forged row must be rejected on read, not projected.
	corrupt := session
	corrupt.PolicyID = "corrupt-session"
	insert(corrupt)
	if _, err = source.ReadExact(ctx, a.TenantID, ownerdomain.Session, corrupt.PolicyID, 1); !errors.Is(err, ownerapp.ErrIntegrity) {
		t.Fatal("corrupt digest trusted", err)
	}
	if _, err = reader.ReadPublishedSessionPolicy(ctx, a.TenantID, domain.PolicyRevisionReference{ID: corrupt.PolicyID, Revision: 1, Digest: corrupt.Digest}); !errors.Is(err, application.ErrDependencyUnavailable) {
		t.Fatal("corruption became approval", err)
	}
	// The tool owner remains an explicit fixture, not a claimed runtime guard.
	tools := &publishedOwnerFixture{}
	validator, err := application.NewPolicyReferenceValidator(reader, reader, tools, application.PublicQuotaCeiling{MaxConcurrentRuns: 1, MaxRunsPerMinute: 5})
	if err != nil {
		t.Fatal(err)
	}
	publisher := policyPublisher(t, store, validator)
	body := domain.DefaultAccessPolicy()
	body.AccessMode = domain.AccessPublicLimited
	body.SessionPolicy = sr
	body.TenantQuota = qr
	body.AllowedOperations = []domain.ChannelOperation{domain.OperationMessageSend}
	result, err := publisher.Publish(ctx, testActor, a.ID, "real-owner-docs", application.PublishAccessPolicyInput{Body: body})
	if err != nil || result.Revision != 1 || result.Distribution != "PENDING" {
		t.Fatal(result, err)
	}

	cipher, e := credentialcrypto.New("k1", map[string]credentialcrypto.Key{"k1": {Encryption: bytes.Repeat([]byte{1}, 32), MAC: bytes.Repeat([]byte{2}, 32)}})
	if e != nil {
		t.Fatal(e)
	}
	runtime, e := application.NewRuntimeService(store, cipher, testScope, testEpoch, reader)
	if e != nil {
		t.Fatal(e)
	}
	resolver, e := application.NewPolicyDependencyService(runtime, reader)
	if e != nil {
		t.Fatal(e)
	}
	principal := application.WorkloadPrincipal{PrincipalID: "spiffe://test/worker", InstanceID: "worker", ScopeID: testScope, Audience: application.WorkloadAudience, Consumers: []string{application.WorkerAuthorizationConsumer}}
	input := application.PolicyResolveRequest{SchemaVersion: 1, AccountID: a.ID, Reference: domain.PolicyRevisionReference{ID: result.PolicyID, Revision: result.Revision, Digest: result.Digest}}
	bundle, e := resolver.Resolve(ctx, principal, input)
	if e != nil || bundle.Session.Digest != sr.Digest || bundle.Quota.Digest != qr.Digest || bundle.Session.TenantID != a.TenantID {
		t.Fatal("exact runtime dependency bundle", bundle, e)
	}
	principal.Consumers = []string{"telegram_receiver"}
	if _, e = resolver.Resolve(ctx, principal, input); !errors.Is(e, application.ErrWorkloadDenied) {
		t.Fatal("unprivileged dependency resolve", e)
	}
	if _, e = reader.ReadPublishedDefinition(ctx, "tnt_b", "session", sr); !errors.Is(e, application.ErrPolicyReferenceDenied) {
		t.Fatal("cross tenant definition", e)
	}
	if _, e = reader.ReadPublishedDefinition(ctx, a.TenantID, "quota", sr); !errors.Is(e, application.ErrPolicyReferenceDenied) {
		t.Fatal("kind definition", e)
	}
	principal.Consumers = []string{application.WorkerAuthorizationConsumer}
	verifyPolicyDependencyMTLS(t, runtime, principal, bundle)
	t.Log("POLICY_DEPENDENCIES=PASS owner publication -> PG exact read -> runtime account authorization -> independent wire digest and reference binding")
	// Verify persisted JSONB against the exact shared consumer contract, not only
	// in-memory producer values. Both owner definitions and the access policy emit
	// one notification plus one audit, and their transport digests survive JSONB.
	rows, err := pool.Query(ctx, `SELECT id,tenant_id,event_type,aggregate_revision,payload_jsonb,payload_digest FROM control_outbox WHERE aggregate_type IN ('ChannelPolicyDefinition','ChannelAccessPolicy')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, tenant, event, digest string
		var revision int64
		var raw []byte
		if err = rows.Scan(&id, &tenant, &event, &revision, &raw, &digest); err != nil {
			t.Fatal(err)
		}
		var actual string
		switch event {
		case channelv1.AccessPolicyPublishedEvent, channelv1.AccessPolicyAuditEvent:
			e, err := channelv1.DecodeAccessPolicyEvent(raw)
			if err != nil || e.EventID != id || e.TenantID != tenant || e.EventType != event || e.PolicyRevision != revision {
				t.Fatal("access policy wire", err)
			}
			_, actual, err = e.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
		case channelv1.PolicyDefinitionPublishedEvent, channelv1.PolicyDefinitionAuditEvent:
			e, err := channelv1.DecodePolicyDefinitionEvent(raw)
			if err != nil || e.EventID != id || e.TenantID != tenant || e.EventType != event || e.Revision != revision {
				t.Fatal("definition wire", err)
			}
			_, actual, err = e.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("unrecognized policy event", event)
		}
		if actual != digest {
			t.Fatal("outbox transport digest differs from canonical contract")
		}
		count++
	}
	if rows.Err() != nil || count != 6 {
		t.Fatal("policy event coverage", count, rows.Err())
	}

}
