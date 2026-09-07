package httpadapter

import (
	"context"
	"errors"
	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
	identityapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity/application"
)

type principalCommandsFake struct {
	result           application.CommandResult
	authErr, err     error
	calls            int
	actor            application.Actor
	account, id, key string
	registerInput    application.RegisterPrincipalInput
	stateInput       application.SetPrincipalStateInput
}

func (f *principalCommandsFake) AuthorizeWrite(context.Context, application.Actor) error {
	return f.authErr
}
func (f *principalCommandsFake) RegisterExternalPrincipal(_ context.Context, a application.Actor, account, key string, in application.RegisterPrincipalInput) (application.CommandResult, error) {
	f.calls++
	f.actor = a
	f.account = account
	f.key = key
	f.registerInput = in
	return f.result, f.err
}
func (f *principalCommandsFake) SetExternalPrincipalState(_ context.Context, a application.Actor, account, id, key string, in application.SetPrincipalStateInput) (application.CommandResult, error) {
	f.calls++
	f.actor = a
	f.account = account
	f.id = id
	f.key = key
	f.stateInput = in
	return f.result, f.err
}
func principalRouter(f *principalCommandsFake, identity *identityapp.IdentityContext) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if identity != nil {
			c.Request = c.Request.WithContext(identityapp.WithIdentity(c.Request.Context(), *identity))
		}
	})
	NewPrincipalHandler(f).Register(r)
	return r
}

const principalBase = "/v1/tenants/tnt_test/channel-principals"

func TestPrincipalHTTPRejectsBeforeCommand(t *testing.T) {
	for _, tc := range []struct {
		name, body, path string
		identity         *identityapp.IdentityContext
		auth             error
		status           int
	}{
		{"unauthenticated", "{", principalBase, nil, nil, 401},
		{"restricted", "{", principalBase, &identityapp.IdentityContext{UserID: "usr_owner", Restricted: true}, nil, 403},
		{"member-before-parse", "{", principalBase, &identityapp.IdentityContext{UserID: "usr_owner"}, application.ErrPermissionDenied, 403},
		{"tenant-in-body", `{"account_id":"cha_test","external_user_id":"123","tenant_id":"other"}`, principalBase, &identityapp.IdentityContext{UserID: "usr_owner"}, nil, 400},
		{"duplicate", `{"account_id":"cha_test","external_user_id":"123","external_user_id":"456"}`, principalBase, &identityapp.IdentityContext{UserID: "usr_owner"}, nil, 400},
		{"case-variant", `{"Account_id":"cha_test","external_user_id":"123"}`, principalBase, &identityapp.IdentityContext{UserID: "usr_owner"}, nil, 400},
		{"invalid-state", `{"account_id":"cha_test","expected_principal_revision":1,"state":"ADMIN"}`, principalBase + "/prn_test/state", &identityapp.IdentityContext{UserID: "usr_owner"}, nil, 400},
		{"missing-cas", `{"account_id":"cha_test","state":"REVOKED"}`, principalBase + "/prn_test/state", &identityapp.IdentityContext{UserID: "usr_owner"}, nil, 400},
		{"query", `{}`, principalBase + "?tenant_id=other", &identityapp.IdentityContext{UserID: "usr_owner"}, nil, 400},
		{"large", strings.Repeat("x", 16385), principalBase, &identityapp.IdentityContext{UserID: "usr_owner"}, nil, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &principalCommandsFake{authErr: tc.auth}
			w := preflightRequest(principalRouter(f, tc.identity), http.MethodPost, tc.path, tc.body)
			if w.Code != tc.status || f.calls != 0 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Code, w.Body, f.calls)
			}
		})
	}
}
func TestPrincipalHTTPForwardsScopedCommandAndSanitizesErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{application.ErrPrincipalNotFound, 404}, {application.ErrPrincipalIdentityConflict, 409}, {application.ErrIdempotencyConflict, 409}, {&domain.Error{Code: domain.PrincipalRevisionConflict}, 409}, {errors.New("PRIVATE_DATABASE_DETAIL"), 503},
	} {
		f := &principalCommandsFake{err: tc.err}
		r := principalRouter(f, &identityapp.IdentityContext{UserID: "usr_owner"})
		w := preflightRequest(r, "POST", principalBase+"/prn_test/state", `{"account_id":"cha_test","expected_principal_revision":7,"state":"REVOKED"}`)
		if w.Code != tc.status || f.calls != 1 || f.actor != (application.Actor{TenantID: "tnt_test", UserID: "usr_owner"}) || f.account != "cha_test" || f.id != "prn_test" || f.key != "create-preflight-1" || f.stateInput.ExpectedRevision != 7 || strings.Contains(w.Body.String(), "PRIVATE_") {
			t.Fatal(w.Code, w.Body, f)
		}
	}
}
func TestPrincipalHTTPRejectsMissingKeyAndEncodedBody(t *testing.T) {
	for _, kind := range []string{"key", "encoding", "media"} {
		f := &principalCommandsFake{}
		r := principalRouter(f, &identityapp.IdentityContext{UserID: "usr_owner"})
		req := httptest.NewRequest("POST", principalBase, strings.NewReader(`{"account_id":"cha_test","external_user_id":"123"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "test")
		switch kind {
		case "key":
			req.Header.Del("Idempotency-Key")
		case "encoding":
			req.Header.Set("Content-Encoding", "gzip")
		case "media":
			req.Header.Set("Content-Type", "text/plain")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(kind, w.Code, f.calls)
		}
	}
}

func TestPrincipalHTTPProjectsClosedScopedResult(t *testing.T) {
	now := time.Now().UTC()
	principal := domain.ExternalPrincipal{Identity: domain.ExternalPrincipalIdentity{TenantID: "tnt_test", AccountID: "cha_test", Provider: domain.Telegram, ExternalUserID: "123"}, ID: "prn_test", State: domain.PrincipalActive, Revision: 1, CreatedBy: "usr_owner", CreatedAt: now, UpdatedAt: now}
	for _, field := range []string{"valid", "tenant", "account", "id"} {
		t.Run(field, func(t *testing.T) {
			p := principal
			switch field {
			case "tenant":
				p.Identity.TenantID = "tnt_other"
			case "account":
				p.Identity.AccountID = "cha_other"
			case "id":
				p.ID = "prn_other"
			}
			f := &principalCommandsFake{result: application.CommandResult{Principal: &p, Distribution: "NOT_EMITTED", EventID: "PRIVATE_EVENT_FIELD", RouteGeneration: 999}}
			w := preflightRequest(principalRouter(f, &identityapp.IdentityContext{UserID: "usr_owner"}), "POST", principalBase+"/prn_test/state", `{"account_id":"cha_test","expected_principal_revision":1,"state":"ACTIVE"}`)
			if field == "valid" {
				if w.Code != 200 || channelv1.Validate("principal-result.schema.json", w.Body.Bytes()) != nil || strings.Contains(w.Body.String(), "PRIVATE_EVENT_FIELD") {
					t.Fatal(w.Code, w.Body)
				}
			} else if w.Code != 500 || strings.Contains(w.Body.String(), "prn_other") || strings.Contains(w.Body.String(), "cha_other") {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
}
