package httpadapter

import (
	"context"
	"errors"
	"io"
	"mime"

	"github.com/gin-gonic/gin"
	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
	identity "github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity/application"
)

type Publisher interface {
	AuthorizeWrite(context.Context, application.Actor) error
	Publish(context.Context, application.Actor, domain.Kind, string, string, application.PublishInput) (application.PublishResult, error)
}
type Handler struct{ publisher Publisher }

func NewHandler(p Publisher) *Handler { return &Handler{publisher: p} }
func (h *Handler) Register(r gin.IRoutes) {
	r.POST("/v1/tenants/:tenant_id/channel-policy-definitions/:kind/:policy_id/revisions", h.publish)
}
func fail(c *gin.Context, status int, code string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": "policy definition request was not completed"}})
}
func respondError(c *gin.Context, err error) {
	status, code := 503, "CHANNEL_POLICY_DEFINITION_UNAVAILABLE"
	switch {
	case errors.Is(err, application.ErrPermissionDenied):
		status, code = 403, application.ErrPermissionDenied.Error()
	case errors.Is(err, domain.ErrInvalid):
		status, code = 400, domain.ErrInvalid.Error()
	case errors.Is(err, application.ErrRevisionConflict):
		status, code = 409, application.ErrRevisionConflict.Error()
	case errors.Is(err, application.ErrIdempotencyConflict):
		status, code = 409, application.ErrIdempotencyConflict.Error()
	case errors.Is(err, application.ErrNotFound):
		status, code = 404, application.ErrNotFound.Error()
	case errors.Is(err, application.ErrIntegrity):
		status, code = 500, application.ErrIntegrity.Error()
	}
	fail(c, status, code)
}
func (h *Handler) publish(c *gin.Context) {
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
	a := application.Actor{TenantID: c.Param("tenant_id"), UserID: user.UserID}
	if err := h.publisher.AuthorizeWrite(c.Request.Context(), a); err != nil {
		respondError(c, err)
		return
	}
	kind := domain.Kind(c.Param("kind"))
	id := c.Param("policy_id")
	if !domain.ValidID(id) || (kind != domain.Session && kind != domain.Quota) || c.Request.URL.RawQuery != "" {
		respondError(c, domain.ErrInvalid)
		return
	}
	keys := c.Request.Header.Values("Idempotency-Key")
	if len(keys) != 1 || len(keys[0]) < 1 || len(keys[0]) > 128 {
		respondError(c, domain.ErrInvalid)
		return
	}
	for _, r := range keys[0] {
		if r < 33 || r > 126 {
			respondError(c, domain.ErrInvalid)
			return
		}
	}
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" || c.GetHeader("Content-Encoding") != "" {
		respondError(c, domain.ErrInvalid)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 16*1024+1))
	defer clear(raw)
	if err != nil {
		respondError(c, domain.ErrInvalid)
		return
	}
	if len(raw) > 16*1024 {
		fail(c, 413, "CHANNEL_POLICY_DEFINITION_LIMIT_EXCEEDED")
		return
	}
	var in application.PublishInput
	if channelv1.Decode("policy-definition-publish.schema.json", raw, &in) != nil {
		respondError(c, domain.ErrInvalid)
		return
	}
	out, err := h.publisher.Publish(c.Request.Context(), a, kind, id, keys[0], in)
	if err != nil {
		respondError(c, err)
		return
	}
	if !out.ValidFor(a.TenantID, kind, id, in.ExpectedRevision+1) {
		respondError(c, application.ErrIntegrity)
		return
	}
	c.JSON(201, out)
}
