package postgresadapter

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/platform/channel/authorization/controlhttp"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/adapter/inbound/internalhttp"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
)

func verifyPolicyDependencyMTLS(t *testing.T, runtime *application.RuntimeService, p application.WorkloadPrincipal, bundle application.PolicyDependenciesResponse) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	handler, e := internalhttp.NewHandler(runtime, []application.WorkloadPrincipal{p})
	if e != nil {
		t.Fatal(e)
	}
	public, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	uri, _ := url.Parse(p.PrincipalID)
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
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	reader, e := controlhttp.New(controlhttp.Options{BaseURL: server.URL, ScopeID: testScope, SourceEpoch: testEpoch, RootCAs: roots, Certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private}})
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	raw, _ := json.Marshal(bundle)
	decoded, e := wire.DecodePolicyDependenciesResponse(raw)
	if e != nil {
		t.Fatal("bundle contract", e)
	}

	for _, field := range []string{"session", "quota"} {
		for _, mutation := range []string{"omit", "time", "digest", "unknown"} {
			var body map[string]any
			json.Unmarshal(raw, &body)
			d := body[field].(map[string]any)
			switch mutation {
			case "omit":
				delete(d, "definition")
			case "time":
				d["published_at"] = "2026-09-08T00:00:00.000Z"
			case "digest":
				d["digest"] = "sha256:" + string(bytes.Repeat([]byte("a"), 64))
			case "unknown":
				d["secret"] = "PRIVATE_CANARY"
			}
			bad, _ := json.Marshal(body)
			if _, err := wire.DecodePolicyDependenciesResponse(bad); err == nil {
				t.Fatal("forged dependency accepted", field, mutation)
			}
		}
	}
	got, e := reader.ReadPolicyDependencies(ctx, decoded.Policy)
	if e != nil || got.Session.Digest != bundle.Session.Digest || got.Quota.Digest != bundle.Quota.Digest {
		t.Fatal("actual mTLS dependencies", got, e)
	}
	wrong := decoded.Policy
	wrong.TenantID = "other"
	if _, e = reader.ReadPolicyDependencies(ctx, wrong); e != controlhttp.ErrIntegrity {
		t.Fatal("tenant substitution", e)
	}
	wrong = decoded.Policy
	wrong.Revision++
	if _, e = reader.ReadPolicyDependencies(ctx, wrong); e != controlhttp.ErrNotFound {
		t.Fatal("no latest dependency fallback", e)
	}
	client := server.Client()
	tr := client.Transport.(*http.Transport).Clone()
	tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	tr.TLSClientConfig.Certificates = []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}
	client.Transport = tr
	defer tr.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/internal/v1/tenants/tenant/channel-accounts/account/credentials:resolve", bytes.NewBufferString("{}"))
	request.Header.Set("Content-Type", "application/json")
	response, e := client.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("Worker gained credentials", response.StatusCode)
	}
	t.Log("POLICY_DEPENDENCIES_MTLS=PASS actual Control/PG -> exclusive Worker identity -> public reader; tenant/revision/credential negatives")
}
