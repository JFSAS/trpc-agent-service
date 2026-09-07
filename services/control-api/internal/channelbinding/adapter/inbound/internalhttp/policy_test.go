package internalhttp

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
)

type policyResolveFake struct {
	runtimeFake
	calls atomic.Int64
}

func (s *policyResolveFake) ResolveAccessPolicy(context.Context, application.WorkloadPrincipal, application.PolicyResolveRequest) (application.PolicyResolveResponse, error) {
	s.calls.Add(1)
	return application.PolicyResolveResponse{}, application.ErrPolicyNotFound
}

const policyResolveBody = `{"schema_version":1,"account_id":"cha_a","reference":{"id":"pol_a","revision":1,"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`

func TestPolicyResolveRealMTLSAndCapability(t *testing.T) {
	caCert, ca := preflightTLSCertificate(t, nil, nil, false, "")
	caKey := caCert.PrivateKey.(ed25519.PrivateKey)
	serverCert, _ := preflightTLSCertificate(t, ca, caKey, false, "")
	clientCert, _ := preflightTLSCertificate(t, ca, caKey, true, preflightPrincipal().PrincipalID)
	otherCert, _ := preflightTLSCertificate(t, ca, caKey, true, "spiffe://test/not-mapped")
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	s := &policyResolveFake{}
	p := preflightPrincipal()
	p.Consumers = []string{application.PolicyProjectionConsumer}
	h, err := NewHandler(s, []application.WorkloadPrincipal{p})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(h)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	server.StartTLS()
	defer server.Close()
	for _, tc := range []struct {
		name string
		cert *tls.Certificate
		want int
	}{{"trusted policy reader", &clientCert, 404}, {"unknown SAN", &otherCert, 403}, {"missing client", nil, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool}
			if tc.cert != nil {
				cfg.Certificates = []tls.Certificate{*tc.cert}
			}
			transport := &http.Transport{TLSClientConfig: cfg}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			req, err := http.NewRequest(http.MethodPost, server.URL+"/internal/v1/channel-access-policies:resolve", strings.NewReader(policyResolveBody))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			response, err := client.Do(req)
			if tc.want == 0 {
				if err == nil {
					response.Body.Close()
					t.Fatal("mTLS accepted absent certificate")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.want || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d headers=%v", response.StatusCode, response.Header)
			}
		})
	}
	if s.calls.Load() != 1 {
		t.Fatalf("authenticated calls=%d", s.calls.Load())
	}
}
func TestPolicyResolveRequiresExplicitCapabilityBeforeParsing(t *testing.T) {
	s := &policyResolveFake{}
	p := preflightPrincipal()
	h, err := NewHandler(s, []application.WorkloadPrincipal{p})
	if err != nil {
		t.Fatal(err)
	}
	w := preflightInternalRequest(h, "/internal/v1/channel-access-policies:resolve", "{", true)
	if w.Code != 403 || s.calls.Load() != 0 {
		t.Fatal(w.Code, s.calls.Load())
	}
	p.Consumers = []string{application.PolicyProjectionConsumer}
	h, err = NewHandler(s, []application.WorkloadPrincipal{p})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{strings.Replace(policyResolveBody, `"account_id":`, `"tenant_id":"other","account_id":`, 1), strings.Replace(policyResolveBody, `"revision":1`, `"revision":1,"revision":2`, 1)} {
		w = preflightInternalRequest(h, "/internal/v1/channel-access-policies:resolve", body, true)
		if w.Code != 400 || s.calls.Load() != 0 {
			t.Fatal(w.Code, s.calls.Load())
		}
	}
}
