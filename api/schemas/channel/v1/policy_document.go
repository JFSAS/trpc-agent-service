package channelv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"
	"unicode"

	"github.com/gowebpki/jcs"
)

const MaxAccessPolicyDocumentBytes = 1 << 20
const MaxAccessPolicyResolveBytes = MaxAccessPolicyDocumentBytes + 4096

type PolicyReference struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}
type AccessPolicyResolveRequest struct {
	SchemaVersion int             `json:"schema_version"`
	AccountID     string          `json:"account_id"`
	Reference     PolicyReference `json:"reference"`
}
type AccessPolicyBody struct {
	AccessMode             string          `json:"access_mode"`
	AllowedPrincipalIDs    []string        `json:"allowed_principal_ids"`
	AllowedConversationIDs []string        `json:"allowed_conversation_ids"`
	AllowedOperations      []string        `json:"allowed_operations"`
	SessionPolicy          PolicyReference `json:"session_policy"`
	TenantQuota            PolicyReference `json:"tenant_quota_ref"`
	AuthorizationMaxAgeMS  int64           `json:"authorization_max_age_ms"`
}

// AccessPolicyDocument is immutable wire content, not an authorization grant.
// Its integrity does not establish principal state or projection freshness.
type AccessPolicyDocument struct {
	SchemaVersion int              `json:"schema_version"`
	TenantID      string           `json:"tenant_id"`
	AccountID     string           `json:"account_id"`
	Provider      string           `json:"provider"`
	PolicyID      string           `json:"policy_id"`
	Revision      int64            `json:"revision"`
	Body          AccessPolicyBody `json:"body"`
	PublishedBy   string           `json:"published_by"`
	PublishedAt   time.Time        `json:"published_at"`
	Digest        string           `json:"digest,omitempty"`
}
type AccessPolicyResolveResponse struct {
	SchemaVersion int                  `json:"schema_version"`
	ScopeID       string               `json:"scope_id"`
	SourceEpoch   string               `json:"source_epoch"`
	Policy        AccessPolicyDocument `json:"policy"`
}

// DecodeAccessPolicyResolveResponse checks the closed shape, canonical sets and
// the owner's complete-envelope JCS/SHA256 digest independently of Control code.
// Failures return a zero response; the caller still checks trusted identities.
func DecodeAccessPolicyResolveResponse(raw []byte) (AccessPolicyResolveResponse, error) {
	var out AccessPolicyResolveResponse
	if len(raw) > MaxAccessPolicyResolveBytes || Decode("access-policy-resolve-response.schema.json", raw, &out) != nil {
		return AccessPolicyResolveResponse{}, ErrInvalidDocument
	}
	fail := func() (AccessPolicyResolveResponse, error) { return AccessPolicyResolveResponse{}, ErrInvalidDocument }
	p := out.Policy
	if p.PublishedAt.IsZero() {
		return fail()
	}
	for _, set := range [][]string{p.Body.AllowedPrincipalIDs, p.Body.AllowedConversationIDs, p.Body.AllowedOperations} {
		if !slices.IsSorted(set) {
			return fail()
		}
	}
	for _, id := range p.Body.AllowedConversationIDs {
		if len(id) > 1024 {
			return fail()
		}
		for _, r := range id {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return fail()
			}
		}
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return fail()
	}
	actual, err := jcs.Transform(encoded)
	if err != nil {
		return fail()
	}
	original, err := jcs.Transform(raw)
	// Reject null/omitted/case variants and noncanonical time representations.
	if err != nil || !bytes.Equal(actual, original) {
		return fail()
	}
	encoded, err = json.Marshal(p)
	if err != nil {
		return fail()
	}
	encoded, err = jcs.Transform(encoded)
	if err != nil || len(encoded) > MaxAccessPolicyDocumentBytes {
		return fail()
	}
	expected := p.Digest
	p.Digest = ""
	encoded, err = json.Marshal(p)
	if err != nil {
		return fail()
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return fail()
	}
	sum := sha256.Sum256(canonical)
	if "sha256:"+hex.EncodeToString(sum[:]) != expected {
		return fail()
	}
	return out, nil
}
