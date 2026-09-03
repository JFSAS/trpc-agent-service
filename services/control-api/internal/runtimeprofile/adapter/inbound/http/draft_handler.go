package httpadapter

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/application"
)

func (h *Handler) getProfileDraft(c *gin.Context) {
	identity, ok := usableIdentity(c)
	if !ok {
		return
	}
	draft, err := h.service.GetProfileDraft(
		c.Request.Context(), c.Param("tenant_id"), c.Param("profile_id"), identity.UserID,
	)
	if err != nil {
		handleApplicationError(c, err)
		return
	}
	c.JSON(http.StatusOK, profileDraftView(draft))
}

func (h *Handler) saveProfileDraft(c *gin.Context) {
	identity, ok := usableIdentity(c)
	if !ok {
		return
	}
	var request saveProfileDraftRequest
	if err := decodeStrictJSON(c, &request, maxRuntimeProfileSpecRequestBytes); err != nil ||
		request.ExpectedRevision <= 0 || request.Spec == nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "expected_revision and spec are required")
		return
	}
	draft, report, err := h.service.SaveProfileDraft(c.Request.Context(), application.SaveProfileDraftCommand{
		TenantID: c.Param("tenant_id"), ProfileID: c.Param("profile_id"),
		ActorUserID: identity.UserID, ExpectedRevision: request.ExpectedRevision,
		Spec: request.Spec,
	})
	if errors.Is(err, application.ErrRuntimeProfileSpecInvalid) {
		writeValidationError(c, report)
		return
	}
	if err != nil {
		handleApplicationError(c, err)
		return
	}
	c.JSON(http.StatusOK, profileDraftView(draft))
}

func (h *Handler) validateProfileDraft(c *gin.Context) {
	identity, ok := usableIdentity(c)
	if !ok {
		return
	}
	var request profileDraftRevisionRequest
	if err := decodeStrictJSON(c, &request, 4*1024); err != nil || request.ExpectedRevision <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "expected_revision is required")
		return
	}
	report, err := h.service.ValidateProfileDraft(c.Request.Context(), application.ValidateProfileDraftCommand{
		TenantID: c.Param("tenant_id"), ProfileID: c.Param("profile_id"),
		ActorUserID: identity.UserID, ExpectedRevision: request.ExpectedRevision,
	})
	if err != nil {
		handleApplicationError(c, err)
		return
	}
	c.JSON(http.StatusOK, validationReportView(report))
}
