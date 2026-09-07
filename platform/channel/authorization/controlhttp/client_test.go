package controlhttp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

const testEpoch = "00000000-0000-4000-8000-000000000001"

func tlsFixture(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, root, root, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	issue := func(n int64, usage x509.ExtKeyUsage) tls.Certificate {
		pub, k, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		x := &x509.Certificate{SerialNumber: big.NewInt(n), Subject: pkix.Name{CommonName: "test-leaf"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{usage}, KeyUsage: x509.KeyUsageDigitalSignature}
		b, e := x509.CreateCertificate(rand.Reader, x, ca, pub, key)
		if e != nil {
			t.Fatal(e)
		}
		kb, e := x509.MarshalPKCS8PrivateKey(k)
		if e != nil {
			t.Fatal(e)
		}
		cert, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}))
		if e != nil {
			t.Fatal(e)
		}
		return cert
	}
	server := httptest.NewUnstartedServer(h)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{issue(2, x509.ExtKeyUsageServerAuth)}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	t.Cleanup(server.Close)
	client, e := New(Options{BaseURL: server.URL, ScopeID: "pool", SourceEpoch: testEpoch, RootCAs: roots, Certificate: issue(3, x509.ExtKeyUsageClientAuth)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	return client, server
}

func policyFixture(t *testing.T) (wire.AccessPolicyResolveResponse, wire.AccessPolicyEvent) {
	t.Helper()
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	p := wire.AccessPolicyDocument{SchemaVersion: 1, TenantID: "tnt_test", AccountID: "cha_test", Provider: "wecom", PolicyID: "pol_test", Revision: 2, PublishedBy: "usr_owner", PublishedAt: now, Body: wire.AccessPolicyBody{AccessMode: "DENY_ALL", AllowedPrincipalIDs: []string{}, AllowedConversationIDs: []string{}, AllowedOperations: []string{}, AuthorizationMaxAgeMS: 30000}}
	sign(t, &p)
	return wire.AccessPolicyResolveResponse{SchemaVersion: 1, ScopeID: "pool", SourceEpoch: testEpoch, Policy: p}, wire.AccessPolicyEvent{SchemaVersion: 1, EventID: "evt_test", EventType: wire.AccessPolicyPublishedEvent, ScopeID: "pool", SourceEpoch: testEpoch, TenantID: p.TenantID, AccountID: p.AccountID, Provider: p.Provider, PolicyID: p.PolicyID, PolicyRevision: p.Revision, PolicyDigest: p.Digest, OccurredAt: now}
}
func sign(t *testing.T, p *wire.AccessPolicyDocument) {
	t.Helper()
	p.Digest = ""
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
}
func TestFetchMutualTLSExactPolicy(t *testing.T) {
	out, event := policyFixture(t)
	cl, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || r.Method != "POST" || r.URL.Path != "/internal/v1/channel-access-policies:resolve" || r.URL.RawQuery != "" {
			t.Error("transport identity")
		}
		raw, _ := io.ReadAll(r.Body)
		var req wire.AccessPolicyResolveRequest
		if wire.Decode("access-policy-resolve-request.schema.json", raw, &req) != nil || req.AccountID != event.AccountID || req.Reference.Digest != event.PolicyDigest || req.Reference.Revision != 2 {
			t.Error("exact request")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}))
	got, err := cl.Fetch(context.Background(), event)
	if err != nil || !reflect.DeepEqual(got, out.Policy) {
		t.Fatal(got, err)
	}
}
func TestFetchRejectsIdentityAndDocumentMutations(t *testing.T) {
	for _, field := range []string{"scope", "epoch", "tenant", "account", "provider", "policy", "revision", "digest", "body", "unknown", "duplicate", "null", "size"} {
		t.Run(field, func(t *testing.T) {
			out, event := policyFixture(t)
			switch field {
			case "scope":
				out.ScopeID = "other"
			case "epoch":
				out.SourceEpoch = "11111111-1111-4111-8111-111111111111"
			case "tenant":
				out.Policy.TenantID = "other"
				sign(t, &out.Policy)
			case "account":
				out.Policy.AccountID = "other"
				sign(t, &out.Policy)
			case "provider":
				out.Policy.Provider = "telegram"
				sign(t, &out.Policy)
			case "policy":
				out.Policy.PolicyID = "other"
				sign(t, &out.Policy)
			case "revision":
				out.Policy.Revision = 3
				sign(t, &out.Policy)
			case "digest":
				out.Policy.Digest = "sha256:" + strings.Repeat("a", 64)
			case "body":
				out.Policy.Body.AuthorizationMaxAgeMS = 1
			}
			raw, _ := json.Marshal(out)
			switch field {
			case "unknown":
				raw = append([]byte(`{"secret":"DO_NOT_LEAK",`), raw[1:]...)
			case "duplicate":
				raw = append([]byte(`{"schema_version":1,`), raw[1:]...)
			case "null":
				raw = []byte(strings.Replace(string(raw), `"allowed_operations":[]`, `"allowed_operations":null`, 1))
			case "size":
				raw = []byte(strings.Repeat(" ", wire.MaxAccessPolicyResolveBytes+1))
			}
			cl, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write(raw)
			}))
			got, err := cl.Fetch(context.Background(), event)
			if !errors.Is(err, ErrIntegrity) || !reflect.DeepEqual(got, wire.AccessPolicyDocument{}) {
				t.Fatal(got, err)
			}
		})
	}
}
func TestFetchTransportBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name            string
		code            int
		encoding, media string
		want            error
	}{
		{"unauthorized", 403, "", "application/json", ErrUnauthorized},
		{"missing", 404, "", "application/json", ErrNotFound},
		{"epoch conflict", 409, "", "application/json", ErrIntegrity},
		{"upstream", 503, "", "application/json", ErrUnavailable},
		{"redirect", 307, "", "application/json", ErrUnavailable},
		{"compressed", 200, "gzip", "application/json", ErrIntegrity},
		{"html", 200, "", "text/html", ErrIntegrity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, event := policyFixture(t)
			var calls atomic.Int32
			cl, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", tc.media)
				w.Header().Set("Content-Encoding", tc.encoding)
				w.Header().Set("Location", "/other")
				w.WriteHeader(tc.code)
				w.Write([]byte("DO_NOT_LEAK"))
			}))
			got, err := cl.Fetch(context.Background(), event)
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, wire.AccessPolicyDocument{}) || calls.Load() != 1 || strings.Contains(err.Error(), "DO_NOT_LEAK") {
				t.Fatal(got, err, calls.Load())
			}
		})
	}
}
func TestFetchValidatesBeforeIOAndCancellation(t *testing.T) {
	_, event := policyFixture(t)
	var calls atomic.Int32
	cl, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	for _, field := range []string{"scope", "epoch", "type", "reference"} {
		bad := event
		switch field {
		case "scope":
			bad.ScopeID = "other"
		case "epoch":
			bad.SourceEpoch = "11111111-1111-4111-8111-111111111111"
		case "type":
			bad.EventType = wire.AccessPolicyAuditEvent
		case "reference":
			bad.PolicyRevision = 0
		}
		if _, err := cl.Fetch(context.Background(), bad); err == nil {
			t.Fatal(field)
		}
	}
	if _, err := cl.Fetch(nil, event); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cl.Fetch(ctx, event); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input reached transport")
	}
	// A valid server certificate alone must not be enough to authenticate a reader.
	cl.transport.TLSClientConfig.Certificates = nil
	if _, err := cl.Fetch(context.Background(), event); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("missing client certificate reached handler")
	}
}
