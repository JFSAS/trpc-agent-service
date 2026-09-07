package postgresadapter

import (
	"encoding/json"
	wire "github.com/liuzengh/trpc-agent-service/api/events/execution/v1"
	dto "github.com/liuzengh/trpc-agent-service/gen/events/execution/v1"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
	"time"
)

// runRequestedPayload is an outbound Adapter conversion. Domain structs do not
// import transport DTOs, and only schema-validated normalized addresses leave the
// Gateway on the durable execution event.
func runRequestedPayload(c domain.Acceptance, authorization *domain.AdmissionAuthorization) ([]byte, error) {
	reply, err := wire.DecodeReplyContext(c.Input.Key.Provider, c.Input.ReplyContext)
	if err != nil {
		return nil, err
	}
	var fact *dto.AdmissionAuthorization
	if authorization != nil {
		if c.Route == nil || authorization.Decision != "ALLOW" || authorization.ValidateFor(c.Input, *c.Route) != nil {
			return nil, domain.ErrInvalidInput
		}
		raw, e := json.Marshal(authorization)
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &fact); e != nil {
			return nil, e
		}
	}
	return wire.EncodeRunRequested(dto.RunRequested{Authorization: fact,
		SchemaVersion: 1, EventID: c.Receipt.AdmissionID, AdmissionID: c.Receipt.AdmissionID, RunID: c.Receipt.RunID,
		Route: dto.RouteSnapshot{Provider: c.Route.Provider, AccountID: c.Route.AccountID, TenantID: c.Route.TenantID, BindingID: c.Route.BindingID, Generation: c.Route.Generation, DeploymentRevisionID: c.Route.DeploymentRevisionID, ManifestRef: c.Route.ManifestRef, ManifestDigest: c.Route.ManifestDigest},
		Input: dto.Inbound{Key: dto.EventKey{Provider: c.Input.Key.Provider, AccountID: c.Input.Key.AccountID, EventID: c.Input.Key.EventID}, Kind: c.Input.Kind, ConversationID: c.Input.ConversationID, ThreadID: c.Input.ThreadID, SenderID: c.Input.SenderID, Text: c.Input.Text, SourceDigest: c.Input.SourceDigest, ReceivedAt: c.Input.ReceivedAt.Format(time.RFC3339Nano), ReplyContext: reply},
	})
}
