package internalhttp

import (
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"net/http"
)

func (h *Handler) authorizationManifest(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !application.CanReadAuthorization(p) {
		handleError(w, application.ErrWorkloadDenied)
		return
	}
	in, ok := decode[application.AuthorizationManifestRequest](w, r, "authorization-manifest-request.schema.json", 8192)
	if !ok {
		return
	}
	out, err := h.service.ReadAuthorizationManifest(r.Context(), p, in)
	if err != nil {
		handleError(w, err)
		return
	}
	writeJSON(w, 200, out, wire.MaxAuthorizationManifestBytes)
}
func (h *Handler) authorizationPage(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !application.CanReadAuthorization(p) {
		handleError(w, application.ErrWorkloadDenied)
		return
	}
	in, ok := decode[application.AuthorizationPageRequest](w, r, "authorization-page-request.schema.json", 8192)
	if !ok {
		return
	}
	out, err := h.service.ReadAuthorizationPage(r.Context(), p, in)
	if err != nil {
		handleError(w, err)
		return
	}
	writeJSON(w, 200, out, wire.MaxAuthorizationPageBytes)
}
