package postgresadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/adapter/outbound/credentialcrypto"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

func TestPolicyResolveScopeEpochAndExactRevisionAgainstPostgreSQL(t *testing.T) {
	store, _, _, account := policyPG(t)
	ctx := context.Background()
	pub := policyPublisher(t, store, &policyReferenceFixture{})
	first, err := pub.Publish(ctx, testActor, account.ID, "resolve-first", application.PublishAccessPolicyInput{Body: domain.DefaultAccessPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pub.Publish(ctx, testActor, account.ID, "resolve-second", application.PublishAccessPolicyInput{ExpectedRevision: 1, Body: domain.DefaultAccessPolicy()}); err != nil {
		t.Fatal(err)
	}
	cipher, err := credentialcrypto.New("k1", map[string]credentialcrypto.Key{"k1": {Encryption: bytes.Repeat([]byte{1}, 32), MAC: bytes.Repeat([]byte{2}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := application.NewRuntimeService(store, cipher, testScope, testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	principal := application.WorkloadPrincipal{PrincipalID: "spiffe://test/gateway", InstanceID: "gateway", ScopeID: testScope, Audience: application.WorkloadAudience, Consumers: []string{application.PolicyProjectionConsumer}}
	req := application.PolicyResolveRequest{SchemaVersion: 1, AccountID: account.ID, Reference: domain.PolicyRevisionReference{ID: first.PolicyID, Revision: 1, Digest: first.Digest}}
	got, err := runtime.ResolveAccessPolicy(ctx, principal, req)
	if err != nil || got.Policy.Revision != 1 || got.Policy.Digest != first.Digest || got.Policy.TenantID != account.TenantID {
		t.Fatal("exact historical document", got, err)
	}
	raw, _ := json.Marshal(got)
	if err = channelv1.Validate("access-policy-resolve-response.schema.json", raw); err != nil {
		t.Fatal("response schema", err)
	}
	decoded, err := channelv1.DecodeAccessPolicyResolveResponse(raw)
	if err != nil || decoded.Policy.Digest != first.Digest {
		t.Fatal("owner PG response failed consumer integrity decoder", err)
	}
	for _, field := range []string{"capability", "scope", "digest", "revision", "account", "policy"} {
		p, in := principal, req
		want := application.ErrPolicyNotFound
		switch field {
		case "capability":
			p.Consumers = []string{"telegram_receiver"}
			want = application.ErrWorkloadDenied
		case "scope":
			p.ScopeID = "other"
			want = application.ErrWorkloadDenied
		case "digest":
			in.Reference.Digest = "sha256:" + strings.Repeat("a", 64)
		case "revision":
			in.Reference.Revision = 3
		case "account":
			in.AccountID = "cha_missing"
		case "policy":
			in.Reference.ID = "pol_missing"
		}
		out, e := runtime.ResolveAccessPolicy(ctx, p, in)
		if !errors.Is(e, want) || out.Policy.Digest != "" {
			t.Fatal(field, out, e)
		}
	}
	// A valid reader of another catalog cannot resolve this existing account,
	// even with the correct immutable reference. This exercises the SQL join,
	// rather than only the application-level workload scope check.
	other := *store
	other.options.ScopeID = "other_gateway_pool"
	if err = other.EnsureCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := other.ReadAccessPolicy(ctx, other.options.ScopeID, account.ID, req.Reference)
	if !errors.Is(err, application.ErrPolicyNotFound) || out.Policy.Digest != "" {
		t.Fatal("cross-scope existing account", out, err)
	}

	stale := *store
	stale.options.SourceEpoch = "22222222-2222-4222-8222-222222222222"
	if _, err = stale.ReadAccessPolicy(ctx, testScope, account.ID, req.Reference); !errors.Is(err, application.ErrEpochMismatch) {
		t.Fatal("catalog epoch mismatch", err)
	}
}
