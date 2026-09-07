package policyowner

import (
	"context"
	"encoding/json"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	channel "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	channeldomain "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
	ownerdomain "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

// ReadPublishedDefinition supplies complete immutable dependency content through
// the owner's read port. Tenant must be derived from an authorized account read,
// never accepted as a workload-supplied tenant override. No direct cross-owner SQL.
func (r *Reader) ReadPublishedDefinition(ctx context.Context, tenant, kind string, ref channeldomain.PolicyRevisionReference) (wire.PolicyDefinitionDocument, error) {
	if ctx == nil || !channeldomain.ValidID(tenant) || (kind != "session" && kind != "quota") {
		return wire.PolicyDefinitionDocument{}, channel.ErrPolicyReferenceDenied
	}
	doc, e := r.read(ctx, tenant, ownerdomain.Kind(kind), ref)
	if e != nil {
		return wire.PolicyDefinitionDocument{}, e
	}
	raw, e := json.Marshal(doc)
	if e != nil {
		return wire.PolicyDefinitionDocument{}, channel.ErrDependencyUnavailable
	}
	out, e := wire.DecodePolicyDefinitionDocument(raw)
	if e != nil {
		return wire.PolicyDefinitionDocument{}, channel.ErrDependencyUnavailable
	}
	return out, nil
}
