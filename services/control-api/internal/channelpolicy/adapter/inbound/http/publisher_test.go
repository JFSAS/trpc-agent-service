package httpadapter

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
	identity "github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity/application"
)

type publisherFake struct {
	auth  error
	calls int
}

func (f *publisherFake) AuthorizeWrite(context.Context, application.Actor) error { return f.auth }
func (f *publisherFake) Publish(context.Context, application.Actor, domain.Kind, string, string, application.PublishInput) (application.PublishResult, error) {
	f.calls++
	return application.PublishResult{}, errors.New("PRIVATE_BACKEND_SECRET")
}
func TestDefinitionHTTPBoundary(t *testing.T) {
	body := `{"expected_revision":0,"definition":{"enabled":true,"session":{"partition":"per_user_in_conversation"}}}`
	for _, tc := range []struct {
		name   string
		status int
		calls  int
	}{
		{"valid-private-error", 503, 1}, {"unauth", 401, 0}, {"restricted", 403, 0}, {"owner-before-body", 403, 0}, {"unknown", 400, 0}, {"duplicate", 400, 0}, {"query", 400, 0}, {"key", 400, 0}, {"encoding", 400, 0}, {"large", 413, 0}, {"kind", 400, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			f := &publisherFake{}
			if tc.name == "owner-before-body" {
				f.auth = application.ErrPermissionDenied
			}
			r.Use(func(c *gin.Context) {
				if tc.name != "unauth" {
					c.Request = c.Request.WithContext(identity.WithIdentity(c.Request.Context(), identity.IdentityContext{UserID: "usr_test", Restricted: tc.name == "restricted"}))
				}
			})
			NewHandler(f).Register(r)
			path := "/v1/tenants/tnt_test/channel-policy-definitions/session/session-test/revisions"
			input := body
			switch tc.name {
			case "owner-before-body":
				input = "{"
			case "unknown":
				input = strings.Replace(body, `"definition":`, `"private":true,"definition":`, 1)
			case "duplicate":
				input = strings.Replace(body, `"expected_revision":0`, `"expected_revision":0,"expected_revision":1`, 1)
			case "query":
				path += "?tenant_id=other"
			case "large":
				input = strings.Repeat("x", 16385)
			case "kind":
				path = strings.Replace(path, "/session/", "/unknown/", 1)
			}
			req := httptest.NewRequest("POST", path, strings.NewReader(input))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "key")
			if tc.name == "key" {
				req.Header.Del("Idempotency-Key")
			}
			if tc.name == "encoding" {
				req.Header.Set("Content-Encoding", "gzip")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.status || f.calls != tc.calls || strings.Contains(w.Body.String(), "PRIVATE_") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Code, w.Body, f.calls)
			}
		})
	}
}
