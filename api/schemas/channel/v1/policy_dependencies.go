package channelv1

import "encoding/json"

const MaxPolicyDependenciesBytes = MaxAccessPolicyResolveBytes + 2*MaxPolicyDefinitionBytes + 4096

type PolicyDependenciesResponse struct {
	SchemaVersion int                      `json:"schema_version"`
	ScopeID       string                   `json:"scope_id"`
	SourceEpoch   string                   `json:"source_epoch"`
	Policy        AccessPolicyDocument     `json:"policy"`
	Session       PolicyDefinitionDocument `json:"session"`
	Quota         PolicyDefinitionDocument `json:"quota"`
}

// DecodePolicyDependenciesResponse validates all complete immutable envelopes.
// Account/tenant/reference binding and freshness remain caller responsibilities.
func DecodePolicyDependenciesResponse(raw []byte) (PolicyDependenciesResponse, error) {
	var out PolicyDependenciesResponse
	fail := func() (PolicyDependenciesResponse, error) { return PolicyDependenciesResponse{}, ErrInvalidDocument }
	if len(raw) > MaxPolicyDependenciesBytes || Decode("policy-dependencies-response.schema.json", raw, &out) != nil {
		return fail()
	}
	// Preserve original nested bytes: unmarshalling then remarshal before decoding
	// would hide noncanonical timestamp or omitted-field representations.
	var original map[string]json.RawMessage
	if json.Unmarshal(raw, &original) != nil {
		return fail()
	}
	s, e := DecodePolicyDefinitionDocument(original["session"])
	if e != nil {
		return fail()
	}
	q, e := DecodePolicyDefinitionDocument(original["quota"])
	if e != nil {
		return fail()
	}
	delete(original, "session")
	delete(original, "quota")
	policyRaw, e := json.Marshal(original)
	if e != nil {
		return fail()
	}
	p, e := DecodeAccessPolicyResolveResponse(policyRaw)
	if e != nil {
		return fail()
	}
	out.Policy = p.Policy
	out.Session = s
	out.Quota = q
	return out, nil
}
