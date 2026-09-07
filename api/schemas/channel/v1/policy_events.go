package channelv1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/gowebpki/jcs"
)

const (
	AccessPolicyPublishedEvent     = "channel.access_policy.published.v1"
	AccessPolicyAuditEvent         = "channel.access_policy.audit.v1"
	PolicyDefinitionPublishedEvent = "channel.policy_definition.published.v1"
	PolicyDefinitionAuditEvent     = "channel.policy_definition.audit.v1"
)

// PolicyAuditFields appear only on audit events. Publication notifications contain
// references, not principals, policy documents, provider IDs or credential data.
type PolicyAuditFields struct {
	ActorID    string `json:"actor_id"`
	ActorKind  string `json:"actor_kind"`
	Action     string `json:"action"`
	Decision   string `json:"decision"`
	ReasonCode string `json:"reason_code"`
}
type AccessPolicyEvent struct {
	SchemaVersion  int       `json:"schema_version"`
	EventID        string    `json:"event_id"`
	EventType      string    `json:"event_type"`
	ScopeID        string    `json:"scope_id"`
	SourceEpoch    string    `json:"source_epoch"`
	TenantID       string    `json:"tenant_id"`
	AccountID      string    `json:"account_id"`
	Provider       string    `json:"provider"`
	PolicyID       string    `json:"policy_id"`
	PolicyRevision int64     `json:"policy_revision"`
	PolicyDigest   string    `json:"policy_digest"`
	OccurredAt     time.Time `json:"occurred_at"`
	*PolicyAuditFields
}
type PolicyDefinitionEvent struct {
	SchemaVersion int       `json:"schema_version"`
	EventID       string    `json:"event_id"`
	EventType     string    `json:"event_type"`
	TenantID      string    `json:"tenant_id"`
	PolicyKind    string    `json:"policy_kind"`
	PolicyID      string    `json:"policy_id"`
	Revision      int64     `json:"revision"`
	Digest        string    `json:"digest"`
	OccurredAt    time.Time `json:"occurred_at"`
	*PolicyAuditFields
}

// CanonicalJSON validates the closed wire document before computing its transport
// digest. The transport digest is distinct from the referenced policy digest.
func (e AccessPolicyEvent) CanonicalJSON() ([]byte, string, error) {
	if e.OccurredAt.IsZero() {
		return nil, "", ErrInvalidDocument
	}
	return canonicalPolicyEvent("access-policy-event.schema.json", e)
}
func (e PolicyDefinitionEvent) CanonicalJSON() ([]byte, string, error) {
	if e.OccurredAt.IsZero() {
		return nil, "", ErrInvalidDocument
	}
	return canonicalPolicyEvent("policy-definition-event.schema.json", e)
}
func canonicalPolicyEvent(schema string, e any) ([]byte, string, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, "", ErrInvalidDocument
	}
	if err = Validate(schema, raw); err != nil {
		return nil, "", err
	}
	raw, err = jcs.Transform(raw)
	if err != nil {
		return nil, "", ErrInvalidDocument
	}
	sum := sha256.Sum256(raw)
	return raw, "sha256:" + hex.EncodeToString(sum[:]), nil
}
func DecodeAccessPolicyEvent(raw []byte) (AccessPolicyEvent, error) {
	var e AccessPolicyEvent
	err := Decode("access-policy-event.schema.json", raw, &e)
	if err != nil {
		return AccessPolicyEvent{}, err
	}
	if e.OccurredAt.IsZero() {
		return AccessPolicyEvent{}, ErrInvalidDocument
	}
	return e, nil
}
func DecodePolicyDefinitionEvent(raw []byte) (PolicyDefinitionEvent, error) {
	var e PolicyDefinitionEvent
	err := Decode("policy-definition-event.schema.json", raw, &e)
	if err != nil {
		return PolicyDefinitionEvent{}, err
	}
	if e.OccurredAt.IsZero() {
		return PolicyDefinitionEvent{}, ErrInvalidDocument
	}
	return e, nil
}

// Access policy notifications have a separate retained stream from route events.
// Audit and reusable definition events must not be published to this subject.
const AccessPolicySubject = "control.channel-access-policy.v1"
const AccessPolicyStream = "CHANNEL_ACCESS_POLICIES_V1"
const MaxAccessPolicyEventBytes = 16 * 1024

// PolicyConsumerName is stable across replicas and unique per configured scope.
// Callers validate scope IDs before provisioning or using this durable name.
func PolicyConsumerName(scope string) string {
	sum := sha256.Sum256([]byte(scope))
	return "channel-gateway-policy-" + hex.EncodeToString(sum[:])
}
