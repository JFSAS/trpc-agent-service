package postgresadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

type workerAuthorizationReader struct {
	read shared.AuthorizationRead
	page wire.AuthorizationSnapshotPage
}

func (r workerAuthorizationReader) ReadAuthorization(ctx context.Context, target shared.AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (shared.AuthorizationRead, error) {
	if err := stage(ctx, r.page); err != nil {
		return shared.AuthorizationRead{}, err
	}
	return r.read, nil
}
func workerAuthorizationFixture(t *testing.T, req domain.Requested, generation int64, state string) workerAuthorizationReader {
	t.Helper()
	a := req.Authorization
	session, quota := authorizationDefinition(t, req.Route.TenantID, "session"), authorizationDefinition(t, req.Route.TenantID, "quota")
	sr := wire.PolicyReference{ID: session.PolicyID, Revision: 1, Digest: session.Digest}
	qr := wire.PolicyReference{ID: quota.PolicyID, Revision: 1, Digest: quota.Digest}
	policy := wire.AccessPolicyDocument{SchemaVersion: 1, TenantID: req.Route.TenantID, AccountID: req.Route.AccountID, Provider: req.Route.Provider, PolicyID: a.PolicyID, Revision: a.PolicyRevision, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), Body: wire.AccessPolicyBody{AccessMode: "ALLOWLIST", AllowedPrincipalIDs: []string{a.PrincipalID}, AllowedConversationIDs: []string{}, AllowedOperations: []string{"message.send"}, SessionPolicy: sr, TenantQuota: qr, AuthorizationMaxAgeMS: 30000}}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = jcs.Transform(raw)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	policy.Digest = "sha256:" + hex.EncodeToString(sum[:])
	identity := wire.AuthorizationSnapshotIdentity{SchemaVersion: 1, ScopeID: a.ScopeID, SourceEpoch: a.SourceEpoch, TenantID: req.Route.TenantID, AccountID: req.Route.AccountID, Provider: req.Route.Provider, Generation: generation}
	p := wire.AuthorizationPrincipal{PrincipalID: a.PrincipalID, ExternalUserID: req.Input.SenderID, State: state, Revision: generation}
	d := wire.NewPrincipalSetDigest()
	if err = d.Add(p); err != nil {
		t.Fatal(err)
	}
	count, root := d.Result()
	manifest := wire.AuthorizationSnapshotManifest{AuthorizationSnapshotIdentity: identity, AccountRevision: 1, AccountEnabled: true, Policy: wire.PolicyReference{ID: policy.PolicyID, Revision: policy.Revision, Digest: policy.Digest}, CapturedAt: time.Now().UTC(), AuthorizationMaxAgeMS: 30000, PrincipalCount: count, PrincipalDigest: root}
	return workerAuthorizationReader{shared.AuthorizationRead{Manifest: manifest, Policy: policy, Dependencies: &shared.PolicyDependencies{Session: session, Quota: quota}}, wire.AuthorizationSnapshotPage{AuthorizationSnapshotIdentity: identity, Principals: []wire.AuthorizationPrincipal{p}, Complete: true}}
}

func authorizationDefinition(t *testing.T, tenant, kind string) wire.PolicyDefinitionDocument {
	t.Helper()
	d := wire.PolicyDefinitionDocument{SchemaVersion: 1, TenantID: tenant, PolicyID: kind, Kind: kind, Revision: 1, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), Definition: wire.PolicyDefinition{Enabled: true}}
	if kind == "session" {
		d.Definition.Session = &wire.SessionDefinition{Partition: "per_user_in_conversation"}
	} else {
		d.Definition.Quota = &wire.QuotaDefinition{MaxConcurrentRuns: 1, MaxRunsPerMinute: 5}
	}
	signAuthorizationDefinition(t, &d)
	return d
}
func signAuthorizationDefinition(t *testing.T, d *wire.PolicyDefinitionDocument) {
	t.Helper()
	d.Digest = ""
	raw, _ := json.Marshal(d)
	raw, e := jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	d.Digest = "sha256:" + hex.EncodeToString(sum[:])
}
