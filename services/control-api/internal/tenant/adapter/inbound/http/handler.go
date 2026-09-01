package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	identityapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity/application"
	tenantapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant/domain"
)

// TenantService is the authenticated HTTP surface required from Tenant.
type TenantService interface {
	ListMyTenants(context.Context, string) ([]domain.TenantMembership, error)
	GetTenant(context.Context, string, string) (domain.TenantMembership, error)
	ListMembers(context.Context, string, string) ([]domain.Membership, error)
	AddMember(context.Context, tenantapp.AddMemberCommand) (domain.Membership, error)
	RemoveMember(context.Context, tenantapp.RemoveMemberCommand) error
}

type Handler struct {
	service TenantService
}

func NewHandler(service TenantService) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Register(routes gin.IRoutes) {
	routes.GET("/v1/me/tenants", h.listMyTenants)
	routes.GET("/v1/tenants/:tenant_id", h.getTenant)
	routes.GET("/v1/tenants/:tenant_id/members", h.listMembers)
	routes.POST("/v1/tenants/:tenant_id/members", h.addMember)
	routes.DELETE("/v1/tenants/:tenant_id/members/:user_id", h.removeMember)
}

func (h *Handler) listMyTenants(c *gin.Context) {
	identity, ok := usableIdentity(c)
	if !ok {
		return
	}
	states, err := h.service.ListMyTenants(c.Request.Context(), identity.UserID)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "request could not be completed")
		return
	}
	items := make([]tenantMembershipResponse, 0, len(states))
	for _, state := range states {
		items = append(items, tenantMembershipView(state))
	}
	c.JSON(http.StatusOK, gin.H{"tenants": items})
}

func (h *Handler) getTenant(c *gin.Context) {
	identity, ok := usableIdentity(c)
	if !ok {
		return
	}
	state, err := h.service.GetTenant(c.Request.Context(), c.Param("tenant_id"), identity.UserID)
	if err != nil {
		handleTenantError(c, err)
		return
	}
	c.JSON(http.StatusOK, tenantMembershipView(state))
}

func (h *Handler) listMembers(c *gin.Context) {
	identity, ok := usableIdentity(c)
	if !ok {
		return
	}
	members, err := h.service.ListMembers(
		c.Request.Context(), c.Param("tenant_id"), identity.UserID,
	)
	if err != nil {
		handleTenantError(c, err)
		return
	}
	items := make([]membershipResponse, 0, len(members))
	for _, member := range members {
		items = append(items, membershipView(member))
	}
	c.JSON(http.StatusOK, gin.H{"members": items})
}

type addMemberRequest struct {
	UserID string `json:"user_id"`
}

func (h *Handler) addMember(c *gin.Context) {
	identity, ok := usableIdentity(c)
	if !ok {
		return
	}
	var request addMemberRequest
	if err := decodeStrictJSON(c, &request, 4*1024); err != nil || strings.TrimSpace(request.UserID) == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "user_id is required")
		return
	}
	membership, err := h.service.AddMember(c.Request.Context(), tenantapp.AddMemberCommand{
		TenantID: c.Param("tenant_id"), ActorUserID: identity.UserID,
		UserID: strings.TrimSpace(request.UserID),
	})
	if err != nil {
		handleTenantError(c, err)
		return
	}
	c.JSON(http.StatusCreated, membershipView(membership))
}

func (h *Handler) removeMember(c *gin.Context) {
	identity, ok := usableIdentity(c)
	if !ok {
		return
	}
	err := h.service.RemoveMember(c.Request.Context(), tenantapp.RemoveMemberCommand{
		TenantID: c.Param("tenant_id"), ActorUserID: identity.UserID,
		UserID: c.Param("user_id"),
	})
	if err != nil {
		handleTenantError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type tenantMembershipResponse struct {
	ID        string                `json:"id"`
	Slug      string                `json:"slug"`
	Name      string                `json:"name"`
	Status    domain.TenantStatus   `json:"status"`
	Role      domain.MembershipRole `json:"role"`
	CreatedAt time.Time             `json:"created_at"`
	UpdatedAt time.Time             `json:"updated_at"`
}

func tenantMembershipView(state domain.TenantMembership) tenantMembershipResponse {
	return tenantMembershipResponse{
		ID: state.Tenant.ID, Slug: state.Tenant.Slug, Name: state.Tenant.Name,
		Status: state.Tenant.Status, Role: state.Membership.Role,
		CreatedAt: state.Tenant.CreatedAt, UpdatedAt: state.Tenant.UpdatedAt,
	}
}

type membershipResponse struct {
	ID        string                `json:"id"`
	UserID    string                `json:"user_id"`
	Role      domain.MembershipRole `json:"role"`
	CreatedBy string                `json:"created_by"`
	CreatedAt time.Time             `json:"created_at"`
}

func membershipView(member domain.Membership) membershipResponse {
	return membershipResponse{
		ID: member.ID, UserID: member.UserID, Role: member.Role,
		CreatedBy: member.CreatedBy, CreatedAt: member.CreatedAt,
	}
}

func usableIdentity(c *gin.Context) (identityapp.IdentityContext, bool) {
	identity, ok := identityapp.IdentityFromContext(c.Request.Context())
	if !ok {
		writeError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return identityapp.IdentityContext{}, false
	}
	if identity.Restricted {
		writeError(c, http.StatusForbidden, "PASSWORD_CHANGE_REQUIRED", "password change is required")
		return identityapp.IdentityContext{}, false
	}
	return identity, true
}

func handleTenantError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, tenantapp.ErrTenantForbidden):
		writeError(c, http.StatusForbidden, "TENANT_FORBIDDEN", "tenant access is forbidden")
	case errors.Is(err, tenantapp.ErrMembershipNotFound):
		writeError(c, http.StatusNotFound, "MEMBERSHIP_NOT_FOUND", "membership was not found")
	case errors.Is(err, tenantapp.ErrMembershipExists):
		writeError(c, http.StatusConflict, "MEMBERSHIP_EXISTS", "membership already exists")
	case errors.Is(err, tenantapp.ErrAccountUnavailable):
		writeError(c, http.StatusBadRequest, "ACCOUNT_UNAVAILABLE", "account is unavailable")
	case errors.Is(err, tenantapp.ErrOwnerRequiresTransfer):
		writeError(c, http.StatusConflict, "OWNER_REQUIRES_TRANSFER", "owner must be transferred before removal")
	default:
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "request could not be completed")
	}
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, errorResponse{Error: errorBody{Code: code, Message: message}})
}

func decodeStrictJSON(c *gin.Context, destination any, maxBytes int64) error {
	if !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "application/json") {
		return errors.New("content type must be application/json")
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain exactly one JSON value")
	}
	return nil
}
