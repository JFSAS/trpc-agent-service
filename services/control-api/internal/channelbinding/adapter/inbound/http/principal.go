package httpadapter

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

// PrincipalCommands manages identity records only; it does not grant runtime access.
type PrincipalCommands interface {
	AuthorizeWrite(context.Context, application.Actor) error
	RegisterExternalPrincipal(context.Context, application.Actor, string, string, application.RegisterPrincipalInput) (application.CommandResult, error)
	SetExternalPrincipalState(context.Context, application.Actor, string, string, string, application.SetPrincipalStateInput) (application.CommandResult, error)
}
type PrincipalHandler struct{ commands PrincipalCommands }

func NewPrincipalHandler(commands PrincipalCommands) *PrincipalHandler {
	return &PrincipalHandler{commands: commands}
}
func (h *PrincipalHandler) Register(routes gin.IRoutes) {
	routes.POST("/v1/tenants/:tenant_id/channel-principals", h.authorize, h.register)
	routes.POST("/v1/tenants/:tenant_id/channel-principals/:principal_id/state", h.authorize, h.setState)
}
func (h *PrincipalHandler) authorize(c *gin.Context) {
	a, ok := actor(c)
	if !ok {
		c.Abort()
		return
	}
	if err := h.commands.AuthorizeWrite(c.Request.Context(), a); err != nil {
		handleError(c, err)
		c.Abort()
	}
}

type principalRegisterRequest struct {
	AccountID      string `json:"account_id"`
	ExternalUserID string `json:"external_user_id"`
}
type principalStateRequest struct {
	AccountID        string                `json:"account_id"`
	ExpectedRevision int64                 `json:"expected_principal_revision"`
	State            domain.PrincipalState `json:"state"`
}

func (h *PrincipalHandler) register(c *gin.Context) {
	a, ok := actor(c)
	if !ok {
		return
	}
	k, ok := key(c)
	if !ok {
		return
	}
	in, ok := decode[principalRegisterRequest](c, "principal-register.schema.json", 16*1024)
	if !ok {
		return
	}
	r, err := h.commands.RegisterExternalPrincipal(c.Request.Context(), a, in.AccountID, k, application.RegisterPrincipalInput{ExternalUserID: in.ExternalUserID})
	principalResponse(c, 201, in.AccountID, r, err)
}
func (h *PrincipalHandler) setState(c *gin.Context) {
	a, ok := actor(c)
	if !ok {
		return
	}
	k, ok := key(c)
	if !ok {
		return
	}
	in, ok := decode[principalStateRequest](c, "principal-state.schema.json", 16*1024)
	if !ok {
		return
	}
	r, err := h.commands.SetExternalPrincipalState(c.Request.Context(), a, in.AccountID, c.Param("principal_id"), k, application.SetPrincipalStateInput{ExpectedRevision: in.ExpectedRevision, State: in.State})
	principalResponse(c, 200, in.AccountID, r, err)
}
func principalResponse(c *gin.Context, status int, accountID string, r application.CommandResult, err error) {
	if err != nil {
		handleError(c, err)
		return
	}
	// Project only this command's identity facts, never account/credential fields.
	if r.Principal == nil || r.Principal.Validate() != nil || r.Principal.Identity.TenantID != c.Param("tenant_id") || r.Principal.Identity.AccountID != accountID || (c.Param("principal_id") != "" && r.Principal.ID != c.Param("principal_id")) || r.Distribution != "NOT_EMITTED" {
		handleError(c, &domain.Error{Code: domain.SourceIntegrity})
		return
	}
	c.JSON(status, struct {
		Principal    *domain.ExternalPrincipal `json:"principal"`
		Distribution string                    `json:"distribution"`
	}{r.Principal, r.Distribution})
}
