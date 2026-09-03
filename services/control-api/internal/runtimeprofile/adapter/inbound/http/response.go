package httpadapter

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	identityapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/domain"
)

type runtimeProfileResponse struct {
	ID                   string    `json:"id"`
	TenantID             string    `json:"tenant_id"`
	Name                 string    `json:"name"`
	Description          string    `json:"description"`
	LatestRevisionNumber *int64    `json:"latest_revision_number"`
	CreatedBy            string    `json:"created_by"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func runtimeProfileView(profile domain.RuntimeProfile) runtimeProfileResponse {
	return runtimeProfileResponse{
		ID: profile.ID, TenantID: profile.TenantID, Name: profile.Name,
		Description: profile.Description, LatestRevisionNumber: profile.LatestRevisionNumber,
		CreatedBy: profile.CreatedBy, CreatedAt: profile.CreatedAt, UpdatedAt: profile.UpdatedAt,
	}
}

type runtimeProfileDraftResponse struct {
	ProfileID string          `json:"profile_id"`
	TenantID  string          `json:"tenant_id"`
	Revision  int64           `json:"revision"`
	Spec      json.RawMessage `json:"spec"`
	UpdatedBy string          `json:"updated_by"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func profileDraftView(draft domain.ProfileDraft) runtimeProfileDraftResponse {
	return runtimeProfileDraftResponse{
		ProfileID: draft.ProfileID, TenantID: draft.TenantID, Revision: draft.Revision,
		Spec: draft.Spec, UpdatedBy: draft.UpdatedBy, UpdatedAt: draft.UpdatedAt,
	}
}

type runtimeProfileRevisionResponse struct {
	ID                  string          `json:"id"`
	TenantID            string          `json:"tenant_id"`
	ProfileID           string          `json:"profile_id"`
	RevisionNumber      int64           `json:"revision_number"`
	SourceDraftRevision int64           `json:"source_draft_revision"`
	SchemaVersion       string          `json:"schema_version"`
	Spec                json.RawMessage `json:"spec"`
	SpecDigest          string          `json:"spec_digest"`
	PublishedBy         string          `json:"published_by"`
	PublishedAt         time.Time       `json:"published_at"`
}

func profileRevisionView(revision domain.ProfileRevision) runtimeProfileRevisionResponse {
	return runtimeProfileRevisionResponse{
		ID: revision.ID, TenantID: revision.TenantID, ProfileID: revision.ProfileID,
		RevisionNumber: revision.RevisionNumber, SourceDraftRevision: revision.SourceDraftRevision,
		SchemaVersion: revision.SchemaVersion, Spec: revision.Spec,
		SpecDigest: revision.SpecDigest, PublishedBy: revision.PublishedBy,
		PublishedAt: revision.PublishedAt,
	}
}

type runtimeProfileRevisionSummaryResponse struct {
	ID                  string    `json:"id"`
	TenantID            string    `json:"tenant_id"`
	ProfileID           string    `json:"profile_id"`
	RevisionNumber      int64     `json:"revision_number"`
	SourceDraftRevision int64     `json:"source_draft_revision"`
	SchemaVersion       string    `json:"schema_version"`
	SpecDigest          string    `json:"spec_digest"`
	PublishedBy         string    `json:"published_by"`
	PublishedAt         time.Time `json:"published_at"`
}

func profileRevisionSummaryView(
	revision domain.ProfileRevisionSummary,
) runtimeProfileRevisionSummaryResponse {
	return runtimeProfileRevisionSummaryResponse{
		ID: revision.ID, TenantID: revision.TenantID, ProfileID: revision.ProfileID,
		RevisionNumber: revision.RevisionNumber, SourceDraftRevision: revision.SourceDraftRevision,
		SchemaVersion: revision.SchemaVersion, SpecDigest: revision.SpecDigest,
		PublishedBy: revision.PublishedBy, PublishedAt: revision.PublishedAt,
	}
}

type validationDiagnosticResponse struct {
	Code         string  `json:"code"`
	Severity     string  `json:"severity"`
	Pointer      string  `json:"pointer"`
	ResourceKind *string `json:"resource_kind"`
	ResourceKey  *string `json:"resource_key"`
	Message      string  `json:"message"`
}

type runtimeProfileValidationReportResponse struct {
	Valid         bool                           `json:"valid"`
	SchemaVersion string                         `json:"schema_version"`
	DraftRevision int64                          `json:"draft_revision"`
	Diagnostics   []validationDiagnosticResponse `json:"diagnostics"`
}

func validationReportView(report domain.ValidationReport) runtimeProfileValidationReportResponse {
	diagnostics := make([]validationDiagnosticResponse, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		diagnostics = append(diagnostics, validationDiagnosticResponse{
			Code: diagnostic.Code, Severity: string(diagnostic.Severity),
			Pointer: diagnostic.Pointer, ResourceKind: diagnostic.ResourceKind,
			ResourceKey: diagnostic.ResourceKey, Message: diagnostic.Message,
		})
	}
	return runtimeProfileValidationReportResponse{
		Valid: report.Valid, SchemaVersion: report.SchemaVersion,
		DraftRevision: report.DraftRevision, Diagnostics: diagnostics,
	}
}

type createRuntimeProfileResponse struct {
	Profile runtimeProfileResponse      `json:"profile"`
	Draft   runtimeProfileDraftResponse `json:"draft"`
}

type publishProfileRevisionResponse struct {
	Revision runtimeProfileRevisionResponse `json:"revision"`
}

type runtimeProfilePageResponse struct {
	RuntimeProfiles []runtimeProfileResponse `json:"runtime_profiles"`
	Total           int                      `json:"total"`
	Offset          int                      `json:"offset"`
	Limit           int                      `json:"limit"`
}

type profileRevisionPageResponse struct {
	Revisions []runtimeProfileRevisionSummaryResponse `json:"revisions"`
	Total     int                                     `json:"total"`
	Offset    int                                     `json:"offset"`
	Limit     int                                     `json:"limit"`
}

type errorResponse struct {
	Error      errorBody                               `json:"error"`
	Validation *runtimeProfileValidationReportResponse `json:"validation,omitempty"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, errorResponse{Error: errorBody{Code: code, Message: message}})
}

func writeValidationError(c *gin.Context, report domain.ValidationReport) {
	view := validationReportView(report)
	c.JSON(http.StatusUnprocessableEntity, errorResponse{
		Error: errorBody{
			Code: "RUNTIME_PROFILE_SPEC_INVALID", Message: "RuntimeProfileSpec validation failed",
		},
		Validation: &view,
	})
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

func handleApplicationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidRuntimeProfile):
		writeError(c, http.StatusBadRequest, "INVALID_RUNTIME_PROFILE", "runtime profile metadata is invalid")
	case errors.Is(err, application.ErrRuntimeProfileNotFound):
		writeError(c, http.StatusNotFound, "RUNTIME_PROFILE_NOT_FOUND", "runtime profile was not found")
	case errors.Is(err, application.ErrProfileRevisionNotFound):
		writeError(c, http.StatusNotFound, "RUNTIME_PROFILE_REVISION_NOT_FOUND", "runtime profile revision was not found")
	case errors.Is(err, application.ErrTenantForbidden):
		writeError(c, http.StatusForbidden, "TENANT_FORBIDDEN", "tenant access is forbidden")
	case errors.Is(err, application.ErrDraftRevisionConflict):
		writeError(c, http.StatusConflict, "RUNTIME_PROFILE_DRAFT_REVISION_CONFLICT", "draft revision is stale")
	default:
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "request could not be completed")
	}
}
