package httpadapter_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	identityapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity/application"
	httpadapter "github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant/adapter/inbound/http"
	tenantapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant/domain"
)

func TestHandlerAddsMemberForAuthenticatedOwner(t *testing.T) {
	service := &tenantServiceStub{added: domain.Membership{
		ID: "membership-2", TenantID: "tenant-1", UserID: "user-2",
		Role: domain.MembershipRoleMember, CreatedBy: "owner-1",
		CreatedAt: time.Date(2026, time.August, 31, 15, 0, 0, 0, time.UTC),
	}}
	router := authenticatedRouter(service, identityapp.IdentityContext{UserID: "owner-1"})

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant-1/members",
		bytes.NewBufferString(`{"user_id":"user-2"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if service.addCommand.ActorUserID != "owner-1" || service.addCommand.UserID != "user-2" {
		t.Fatalf("command = %#v", service.addCommand)
	}
}

func TestHandlerBlocksRestrictedSession(t *testing.T) {
	service := &tenantServiceStub{}
	router := authenticatedRouter(service, identityapp.IdentityContext{
		UserID: "owner-1", Restricted: true,
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/me/tenants", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden ||
		recorder.Body.String() != `{"error":{"code":"PASSWORD_CHANGE_REQUIRED","message":"password change is required"}}` {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func authenticatedRouter(service httpadapter.TenantService, identity identityapp.IdentityContext) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(identityapp.WithIdentity(c.Request.Context(), identity))
		c.Next()
	})
	httpadapter.NewHandler(service).Register(router)
	return router
}

type tenantServiceStub struct {
	added      domain.Membership
	addCommand tenantapp.AddMemberCommand
}

func (s *tenantServiceStub) ListMyTenants(context.Context, string) ([]domain.TenantMembership, error) {
	return nil, nil
}

func (s *tenantServiceStub) GetTenant(context.Context, string, string) (domain.TenantMembership, error) {
	return domain.TenantMembership{}, nil
}

func (s *tenantServiceStub) ListMembers(context.Context, string, string) ([]domain.Membership, error) {
	return nil, nil
}

func (s *tenantServiceStub) AddMember(_ context.Context, command tenantapp.AddMemberCommand) (domain.Membership, error) {
	s.addCommand = command
	return s.added, nil
}

func (s *tenantServiceStub) RemoveMember(context.Context, tenantapp.RemoveMemberCommand) error {
	return nil
}
