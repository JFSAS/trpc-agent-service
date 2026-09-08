package channelv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/gowebpki/jcs"
)

const MaxPolicyDefinitionBytes = 64 * 1024

type SessionDefinition struct {
	Partition string `json:"partition"`
}
type QuotaDefinition struct {
	// MaxTotalModelTokens is a cumulative tenant token cap across revisions.
	// Nil is unconfigured, not unlimited; zero denies new model consumption.
	MaxTotalModelTokens *int64 `json:"max_total_model_tokens,omitempty"`
	PublicLimited       bool   `json:"public_limited"`
	MaxConcurrentRuns   int64  `json:"max_concurrent_runs"`
	MaxRunsPerMinute    int64  `json:"max_runs_per_minute"`
}
type PolicyDefinition struct {
	Enabled bool               `json:"enabled"`
	Session *SessionDefinition `json:"session,omitempty"`
	Quota   *QuotaDefinition   `json:"quota,omitempty"`
}

// PolicyDefinitionDocument carries the complete immutable owner envelope.
// Its digest is not a freshness proof, usage reservation or execution grant.
type PolicyDefinitionDocument struct {
	SchemaVersion int              `json:"schema_version"`
	TenantID      string           `json:"tenant_id"`
	PolicyID      string           `json:"policy_id"`
	Kind          string           `json:"kind"`
	Revision      int64            `json:"revision"`
	Definition    PolicyDefinition `json:"definition"`
	PublishedBy   string           `json:"published_by"`
	PublishedAt   time.Time        `json:"published_at"`
	Digest        string           `json:"digest,omitempty"`
}

// DecodePolicyDefinitionDocument independently verifies the closed owner shape,
// kind-dependent fields, canonical representation and full-envelope JCS digest.
// No Control domain package or draft interpretation is needed by consumers.
func DecodePolicyDefinitionDocument(raw []byte) (PolicyDefinitionDocument, error) {
	var out PolicyDefinitionDocument
	fail := func() (PolicyDefinitionDocument, error) { return PolicyDefinitionDocument{}, ErrInvalidDocument }
	if len(raw) > MaxPolicyDefinitionBytes || Decode("policy-definition-document.schema.json", raw, &out) != nil || out.PublishedAt.IsZero() {
		return fail()
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
	if err != nil || !bytes.Equal(actual, original) {
		return fail()
	}
	expected := out.Digest
	unsigned := out
	unsigned.Digest = ""
	encoded, err = json.Marshal(unsigned)
	if err != nil {
		return fail()
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return fail()
	}
	sum := sha256.Sum256(canonical)
	if expected != "sha256:"+hex.EncodeToString(sum[:]) {
		return fail()
	}
	return out, nil
}
