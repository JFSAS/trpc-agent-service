package httpadapter

import (
	"context"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
	identity "github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity/application"
)

type Queries interface {
	ReadExact(context.Context, application.Actor, domain.Kind, string, int64) (domain.Revision, error)
}
type QueryHandler struct{ queries Queries }

func NewQueryHandler(q Queries) *QueryHandler { return &QueryHandler{queries: q} }
func (h *QueryHandler) Register(r gin.IRoutes) {
	r.GET("/v1/tenants/:tenant_id/channel-policy-definitions/:kind/:policy_id/revisions/:revision", h.get)
}
func (h *QueryHandler) get(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	user, ok := identity.IdentityFromContext(c.Request.Context())
	if !ok {
		fail(c, 401, "UNAUTHENTICATED")
		return
	}
	if user.Restricted {
		respondError(c, application.ErrPermissionDenied)
		return
	}
	version := c.Param("revision")
	revision, err := strconv.ParseInt(version, 10, 64)
	// Canonical decimal only: no aliases such as 01, +1, exponent or latest.
	if err != nil || strconv.FormatInt(revision, 10) != version || !domain.ValidRevision(revision) || c.Request.URL.RawQuery != "" || c.GetHeader("Content-Encoding") != "" {
		respondError(c, domain.ErrInvalid)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1))
	if err != nil || len(raw) > 0 {
		respondError(c, domain.ErrInvalid)
		return
	}
	out, err := h.queries.ReadExact(c.Request.Context(), application.Actor{TenantID: c.Param("tenant_id"), UserID: user.UserID}, domain.Kind(c.Param("kind")), c.Param("policy_id"), revision)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(200, out)
}
