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

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
)

type authorizationRuntimeFake struct {
	runtimeFake
	calls atomic.Int64
}

func (s *authorizationRuntimeFake) ReadAuthorizationManifest(context.Context, application.WorkloadPrincipal, application.AuthorizationManifestRequest) (wire.AuthorizationSnapshotManifest, error) {
	s.calls.Add(1)
	return wire.AuthorizationSnapshotManifest{}, application.ErrAccountNotFound
}
func (s *authorizationRuntimeFake) ReadAuthorizationPage(context.Context, application.WorkloadPrincipal, application.AuthorizationPageRequest) (wire.AuthorizationSnapshotPage, error) {
	s.calls.Add(1)
	return wire.AuthorizationSnapshotPage{}, application.ErrAuthorizationSnapshotChanged
}

var authorizationRequests = []struct {
	path, body string
	status     int
}{
	{"/internal/v1/channel-authorizations:snapshot", `{"schema_version":1,"account_id":"cha_a"}`, 404},
	{"/internal/v1/channel-authorizations:page", `{"schema_version":1,"account_id":"cha_a","source_epoch":"11111111-1111-4111-8111-111111111111","generation":1,"after_principal_id":""}`, 409},
}

func TestAuthorizationSnapshotRealMTLSAndClosedRequests(t *testing.T) {
	caCert, ca := preflightTLSCertificate(t, nil, nil, false, "")
	key := caCert.PrivateKey.(ed25519.PrivateKey)
	serverCert, _ := preflightTLSCertificate(t, ca, key, false, "")
	clientCert, _ := preflightTLSCertificate(t, ca, key, true, preflightPrincipal().PrincipalID)
	otherCert, _ := preflightTLSCertificate(t, ca, key, true, "spiffe://test/unmapped")
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	p := preflightPrincipal()
	p.Consumers = []string{application.PolicyProjectionConsumer}
	s := &authorizationRuntimeFake{}
	h, err := NewHandler(s, []application.WorkloadPrincipal{p})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(h)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
	server.StartTLS()
	defer server.Close()
	for _, in := range authorizationRequests {
		t.Run(in.path, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				cert   *tls.Certificate
				status int
			}{{"trusted", &clientCert, in.status}, {"unmapped", &otherCert, 403}, {"missing", nil, 0}} {
				t.Run(tc.name, func(t *testing.T) {
					cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}
					if tc.cert != nil {
						cfg.Certificates = []tls.Certificate{*tc.cert}
					}
					transport := &http.Transport{TLSClientConfig: cfg}
					defer transport.CloseIdleConnections()
					client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
					req, _ := http.NewRequest(http.MethodPost, server.URL+in.path, strings.NewReader(in.body))
					req.Header.Set("Content-Type", "application/json")
					resp, e := client.Do(req)
					if tc.status == 0 {
						if e == nil {
							resp.Body.Close()
							t.Fatal("missing cert accepted")
						}
						return
					}
					if e != nil {
						t.Fatal(e)
					}
					defer resp.Body.Close()
					if resp.StatusCode != tc.status || resp.Header.Get("Cache-Control") != "no-store" {
						t.Fatal(resp.StatusCode)
					}
				})
			}
		})
	}
	if s.calls.Load() != 2 {
		t.Fatal("untrusted reader reached runtime", s.calls.Load())
	}
	for _, in := range authorizationRequests {
		for _, body := range []string{"{", strings.Replace(in.body, `"account_id":`, `"tenant_id":"other","account_id":`, 1), strings.Replace(in.body, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1)} {
			before := s.calls.Load()
			w := preflightInternalRequest(h, in.path, body, true)
			if w.Code != 400 || s.calls.Load() != before {
				t.Fatal("invalid closed request", w.Code)
			}
		}
	}
	p.Consumers = []string{"telegram_receiver"}
	h, err = NewHandler(s, []application.WorkloadPrincipal{p})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range authorizationRequests {
		before := s.calls.Load()
		w := preflightInternalRequest(h, in.path, "{", true)
		if w.Code != 403 || s.calls.Load() != before {
			t.Fatal("capability was not checked before decode", w.Code)
		}
	}
}

func TestWorkerAuthorizationCapabilityIsExclusiveReadOnly(t *testing.T) {
	p := preflightPrincipal()
	p.Consumers = []string{application.WorkerAuthorizationConsumer}
	fake := &authorizationRuntimeFake{}
	h, err := NewHandler(fake, []application.WorkloadPrincipal{p})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range authorizationRequests {
		w := preflightInternalRequest(h, in.path, in.body, true)
		if w.Code != in.status {
			t.Fatal("Worker snapshot denied", w.Code)
		}
	}
	for _, path := range []string{"/internal/v1/channel-accounts/snapshot", "/internal/v1/tenants/tenant/channel-accounts/account/credentials:resolve", "/internal/v1/channel-account-observations", "/internal/v1/channel-preflights:claim", "/internal/v1/channel-wecom-preflights:claim"} {
		w := preflightInternalRequest(h, path, "not-json", true)
		if w.Code != 403 {
			t.Fatal("Worker escaped read-only routes", path, w.Code)
		}
	}
	p.Consumers = append(p.Consumers, "telegram_receiver")
	if _, err := NewHandler(fake, []application.WorkloadPrincipal{p}); err == nil {
		t.Fatal("mixed Worker and Gateway capabilities")
	}
}
