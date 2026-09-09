package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	managementv1 "github.com/liuzengh/trpc-agent-service/api/runtime/management/v1"
	identityapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runmanagement/application"
)

type Service interface {
	ListRuns(ctx context.Context, tenant, user string, offset, limit int) (managementv1.RunPage, error)
	GetRun(ctx context.Context, tenant, user, runID string) (managementv1.RunDetail, error)
	ListAudit(ctx context.Context, tenant, user string, offset, limit int) (managementv1.AuditPage, error)
}

type Handler struct{ service Service }

func New(service Service) *Handler { return &Handler{service: service} }

func (h *Handler) Register(routes gin.IRoutes) {
	routes.GET("/v1/tenants/:tenant_id/runs", h.listRuns)
	routes.GET("/v1/tenants/:tenant_id/runs/:run_id", h.getRun)
	routes.GET("/v1/tenants/:tenant_id/audit-events", h.listAudit)
}

func identity(c *gin.Context) (identityapp.IdentityContext, bool) {
	id, ok := identityapp.IdentityFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "AUTHENTICATION_REQUIRED"}})
		return id, false
	}
	if id.Restricted {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "PASSWORD_CHANGE_REQUIRED"}})
		return id, false
	}
	return id, true
}

func page(c *gin.Context) (int, int, bool) {
	for key, values := range c.Request.URL.Query() {
		if (key != "offset" && key != "limit") || len(values) != 1 {
			return 0, 0, false
		}
	}
	offset, limit := 0, 25
	var err error
	if raw := c.Query("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, false
		}
	}
	if raw := c.Query("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, false
		}
	}
	return offset, limit, offset >= 0 && limit > 0 && limit <= 100
}

func (h *Handler) listRuns(c *gin.Context) {
	id, ok := identity(c)
	if !ok {
		return
	}
	offset, limit, ok := page(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_PAGE"}})
		return
	}
	result, err := h.service.ListRuns(c.Request.Context(), c.Param("tenant_id"), id.UserID, offset, limit)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}

func (h *Handler) getRun(c *gin.Context) {
	id, ok := identity(c)
	if !ok {
		return
	}
	if c.Request.URL.RawQuery != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_QUERY"}})
		return
	}
	result, err := h.service.GetRun(c.Request.Context(), c.Param("tenant_id"), id.UserID, c.Param("run_id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}

func (h *Handler) listAudit(c *gin.Context) {
	id, ok := identity(c)
	if !ok {
		return
	}
	offset, limit, ok := page(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_PAGE"}})
		return
	}
	result, err := h.service.ListAudit(c.Request.Context(), c.Param("tenant_id"), id.UserID, offset, limit)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}

func fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "TENANT_FORBIDDEN"}})
	case errors.Is(err, application.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "RUN_NOT_FOUND"}})
	case errors.Is(err, application.ErrInvalidPage):
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_PAGE"}})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "RUN_MANAGEMENT_UNAVAILABLE"}})
	}
}
