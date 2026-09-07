package postgresadapter

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/adapter/inbound/internalhttp"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/adapter/outbound/credentialcrypto"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
	"github.com/liuzengh/trpc-agent-service/services/control-api/migrations"
)

func TestAuthorizationSnapshotActualRuntimeMTLSPostgres(t *testing.T) {
	store, service, pool, a := policyPG(t)
	ctx := context.Background()
	sql, e := migrations.Files.ReadFile("0008_channel_authorization_generation.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, string(sql)); e != nil {
		t.Fatal(e)
	}
	if e = appendPolicy(t, store, a, 1, domain.DefaultAccessPolicy(), "http-snapshot-policy"); e != nil {
		t.Fatal(e)
	}
	registered, e := service.RegisterExternalPrincipal(ctx, testActor, a.ID, "http-snapshot-principal", application.RegisterPrincipalInput{ExternalUserID: "456"})
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := credentialcrypto.New("fixture", map[string]credentialcrypto.Key{"fixture": {Encryption: bytes.Repeat([]byte{1}, 32), MAC: bytes.Repeat([]byte{2}, 32)}})
	if e != nil {
		t.Fatal(e)
	}
	runtime, e := application.NewRuntimeService(store, cipher, testScope, testEpoch)
	if e != nil {
		t.Fatal(e)
	}
	workload := application.WorkloadPrincipal{PrincipalID: "spiffe://test/snapshot-reader", ScopeID: testScope, InstanceID: "gateway", Audience: application.WorkloadAudience, Consumers: []string{application.PolicyProjectionConsumer}}
	handler, e := internalhttp.NewHandler(runtime, []application.WorkloadPrincipal{workload})
	if e != nil {
		t.Fatal(e)
	}
	// Dedicated self-signed test client trust anchor; httptest separately supplies
	// its server certificate and the client's server trust. TLS verifies both peers.
	public, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	uri, _ := url.Parse(workload.PrincipalID)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true, IsCA: true, URIs: []*url.URL{uri}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(cert)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots}
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}
	client.Transport = transport
	client.Timeout = 5 * time.Second
	defer transport.CloseIdleConnections()
	post := func(path string, value any, want int) []byte {
		t.Helper()
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		r, e := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		response, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		b, e := io.ReadAll(io.LimitReader(response.Body, wire.MaxAuthorizationPageBytes+1))
		if e != nil || response.StatusCode != want || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("bounded private response", response.StatusCode, e)
		}
		return b
	}
	manifestRaw := post("/internal/v1/channel-authorizations:snapshot", application.AuthorizationManifestRequest{SchemaVersion: 1, AccountID: a.ID}, 200)
	m, e := wire.DecodeAuthorizationSnapshotManifest(manifestRaw)
	if e != nil || m.PrincipalCount != 1 {
		t.Fatal("actual manifest", e)
	}
	request := application.AuthorizationPageRequest{SchemaVersion: 1, AccountID: a.ID, SourceEpoch: testEpoch, Generation: m.Generation, AfterPrincipalID: ""}
	raw := post("/internal/v1/channel-authorizations:page", request, 200)
	page, e := wire.DecodeAuthorizationSnapshotPage(raw)
	if e != nil {
		t.Fatal(e)
	}
	proof, e := wire.NewAuthorizationSnapshotProof(m)
	if e != nil || proof.Add(page) != nil || proof.Finish() != nil {
		t.Fatal("actual complete proof", e)
	}
	if len(page.Principals) != 1 || page.Principals[0].ExternalUserID != "456" {
		t.Fatal("actual external identity missing")
	}
	if _, e = service.SetExternalPrincipalState(ctx, testActor, a.ID, registered.Principal.ID, "http-revoke", application.SetPrincipalStateInput{ExpectedRevision: 1, State: domain.PrincipalRevoked}); e != nil {
		t.Fatal(e)
	}
	body := post("/internal/v1/channel-authorizations:page", request, 409)
	if bytes.Contains(body, []byte("456")) || bytes.Contains(body, []byte("principals")) {
		t.Fatal("stale response leaked partial set")
	}
	t.Log("AUTHORIZATION_HTTP_PG=PASS verified mTLS -> real RuntimeService -> PG current snapshot -> shared complete proof; owner revoke -> stale page HTTP 409")
}
