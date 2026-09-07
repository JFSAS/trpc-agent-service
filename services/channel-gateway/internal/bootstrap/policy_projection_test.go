package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

func bootstrapPolicyDocument(t *testing.T) wire.AccessPolicyDocument {
	t.Helper()
	p := wire.AccessPolicyDocument{SchemaVersion: 1, TenantID: "tenant", AccountID: "account", Provider: "telegram", PolicyID: "policy", Revision: 1, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), Body: wire.AccessPolicyBody{AccessMode: "DENY_ALL", AllowedPrincipalIDs: []string{}, AllowedConversationIDs: []string{}, AllowedOperations: []string{}, AuthorizationMaxAgeMS: 30000}}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = jcs.Transform(raw)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	p.Digest = "sha256:" + hex.EncodeToString(sum[:])
	return p
}
func TestPolicyProjectionBootstrapRealRuntime(t *testing.T) {
	t.Setenv("GATEWAY_TEST_POLICY_BOOTSTRAP", "1")
	TestControlRuntimeMTLSRealPGNATSRotationAndInbound(t)
}
func TestPolicyProjectionConfigAndDisabledConstruction(t *testing.T) {
	c := configWithAccounts(t, nil)
	c.PolicyProjectionEnabled = true
	if c.Validate() == nil {
		t.Fatal("policy projection allowed fixture mode")
	}
	c.PolicyProjectionEnabled = false
	if r, err := newPolicyProjection(context.Background(), c, nil, nil); err != nil || r != nil {
		t.Fatal("disabled feature acquired dependencies", r, err)
	}
	c.PolicyProjectionEnabled = true
	c.AccountSource = "control"
	if _, err := newPolicyProjection(context.Background(), c, nil, nil); err == nil {
		t.Fatal("missing enabled dependencies accepted")
	}
	t.Setenv("GATEWAY_POLICY_PROJECTION_ENABLED", "yes")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("invalid enablement accepted")
	}
}

type policyLoopFunc func(context.Context) error

func (f policyLoopFunc) Run(ctx context.Context) error { return f(ctx) }
func TestPolicyProjectionLifecycleCancellationAndCloseOnce(t *testing.T) {
	var closed atomic.Int32
	entered := make(chan struct{})
	r := &policyProjectionRuntime{loop: policyLoopFunc(func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }), close: func() { closed.Add(1) }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("loop not started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("loop did not stop")
	}
	r.Close()
	r.Close()
	if closed.Load() != 1 {
		t.Fatal(closed.Load())
	}
}
