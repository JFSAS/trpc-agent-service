package domain

import (
	"bytes"
	"encoding/json"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"
)

type ChannelAccessMode string

const (
	AccessDenyAll       ChannelAccessMode = "DENY_ALL"
	AccessAllowlist     ChannelAccessMode = "ALLOWLIST"
	AccessPublicLimited ChannelAccessMode = "PUBLIC_LIMITED"
)

type ChannelOperation string

const (
	OperationMessageSend  ChannelOperation = "message.send"
	OperationSessionNew   ChannelOperation = "session.new"
	OperationRunCancelOwn ChannelOperation = "run.cancel_own"
	// These are preparation bounds, not a measured runtime propagation SLA. F10
	// must calibrate freshness; complete large member sets need paged projections.
	MaxAuthorizationAgeMS        int64 = 30000
	MaxAccessPolicyMembers             = 4096
	MaxAccessPolicyDocumentBytes       = 1024 * 1024
)

// PolicyRevisionReference fixes owner-published content, never a mutable draft.
// Structural validation is not proof that the referenced record exists, belongs
// to the tenant, or supplies the required runtime capability.
type PolicyRevisionReference struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}

func (r PolicyRevisionReference) valid() bool {
	return ValidID(r.ID) && ValidVersion(r.Revision) && ValidDigest(r.Digest)
}

type AccessPolicyBody struct {
	AccessMode             ChannelAccessMode       `json:"access_mode"`
	AllowedPrincipalIDs    []string                `json:"allowed_principal_ids"`
	AllowedConversationIDs []string                `json:"allowed_conversation_ids"`
	AllowedOperations      []ChannelOperation      `json:"allowed_operations"`
	SessionPolicy          PolicyRevisionReference `json:"session_policy"`
	TenantQuota            PolicyRevisionReference `json:"tenant_quota_ref"`
	AuthorizationMaxAgeMS  int64                   `json:"authorization_max_age_ms"`
}

func DefaultAccessPolicy() AccessPolicyBody {
	return AccessPolicyBody{AccessMode: AccessDenyAll, AllowedPrincipalIDs: []string{}, AllowedConversationIDs: []string{}, AllowedOperations: []ChannelOperation{}, AuthorizationMaxAgeMS: MaxAuthorizationAgeMS}
}

// canonicalAccessPolicy validates only content shape. Owner resolution, active
// principal checks and PUBLIC_LIMITED isolation/quota/tool constraints belong to
// the publish application transaction before this candidate can become a grant.
func canonicalAccessPolicy(body AccessPolicyBody) (json.RawMessage, error) {
	if len(body.AllowedPrincipalIDs) > MaxAccessPolicyMembers || len(body.AllowedConversationIDs) > MaxAccessPolicyMembers || len(body.AllowedOperations) > MaxAccessPolicyMembers || body.AuthorizationMaxAgeMS < 1 || body.AuthorizationMaxAgeMS > MaxAuthorizationAgeMS {
		return nil, failure(InputInvalid, "/access_policy")
	}
	for _, id := range body.AllowedPrincipalIDs {
		if !ValidID(id) {
			return nil, failure(InputInvalid, "/allowed_principal_ids")
		}
	}
	for _, id := range body.AllowedConversationIDs {
		// Conversation IDs are opaque provider identifiers, not platform principal
		// IDs; preserve case and punctuation while rejecting whitespace/control text.
		if len(id) < 1 || len(id) > 1024 || !utf8.ValidString(id) {
			return nil, failure(InputInvalid, "/allowed_conversation_ids")
		}
		for _, r := range id {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return nil, failure(InputInvalid, "/allowed_conversation_ids")
			}
		}
	}
	for _, op := range body.AllowedOperations {
		switch op {
		case OperationMessageSend, OperationSessionNew, OperationRunCancelOwn:
		default:
			return nil, failure(InputInvalid, "/allowed_operations")
		}
	}
	switch body.AccessMode {
	case AccessDenyAll:
		if len(body.AllowedPrincipalIDs) != 0 || len(body.AllowedConversationIDs) != 0 || len(body.AllowedOperations) != 0 || body.SessionPolicy != (PolicyRevisionReference{}) || body.TenantQuota != (PolicyRevisionReference{}) {
			return nil, failure(InputInvalid, "/access_mode")
		}
	case AccessAllowlist, AccessPublicLimited:
		if !body.SessionPolicy.valid() || !body.TenantQuota.valid() {
			return nil, failure(InputInvalid, "/policy_references")
		}
		// A public policy cannot carry a misleading principal allowlist that the
		// public branch would ignore. Conversation and operation scopes still apply.
		if body.AccessMode == AccessPublicLimited && len(body.AllowedPrincipalIDs) != 0 {
			return nil, failure(InputInvalid, "/allowed_principal_ids")
		}
	default:
		return nil, failure(InputInvalid, "/access_mode")
	}
	body.AllowedPrincipalIDs = canonicalPolicySet(body.AllowedPrincipalIDs)
	body.AllowedConversationIDs = canonicalPolicySet(body.AllowedConversationIDs)
	body.AllowedOperations = canonicalPolicySet(body.AllowedOperations)
	raw, _, err := CanonicalJSON(body)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxAccessPolicyDocumentBytes {
		return nil, failure(InputInvalid, "/access_policy")
	}
	return raw, nil
}
func canonicalPolicySet[T ~string](input []T) []T {
	out := append([]T{}, input...)
	slices.Sort(out)
	return slices.Compact(out)
}

// ChannelAccessPolicyRevision is an integrity-checked publication candidate.
// Holding it is not runtime authorization. Persisting/replacing any part requires
// a new publication revision in the owner transaction; Validate detects mutation.
type ChannelAccessPolicyRevision struct {
	SchemaVersion int             `json:"schema_version"`
	TenantID      string          `json:"tenant_id"`
	AccountID     string          `json:"account_id"`
	Provider      Provider        `json:"provider"`
	PolicyID      string          `json:"policy_id"`
	Revision      int64           `json:"revision"`
	Body          json.RawMessage `json:"body"`
	PublishedBy   string          `json:"published_by"`
	PublishedAt   time.Time       `json:"published_at"`
	Digest        string          `json:"digest,omitempty"`
}

// PrepareAccessPolicyRevision derives identity from an already owner-loaded
// account and canonicalizes copies of the caller's sets. The publisher still
// must resolve referenced owners, check CAS and atomically commit policy/outboxes.
func PrepareAccessPolicyRevision(account Account, id string, revision int64, actor string, body AccessPolicyBody, now time.Time) (ChannelAccessPolicyRevision, error) {
	if err := account.Validate(); err != nil {
		return ChannelAccessPolicyRevision{}, err
	}
	if !ValidID(id) || !ValidID(actor) || !ValidVersion(revision) || now.IsZero() {
		return ChannelAccessPolicyRevision{}, failure(InputInvalid, "/access_policy")
	}
	raw, err := canonicalAccessPolicy(body)
	if err != nil {
		return ChannelAccessPolicyRevision{}, err
	}
	p := ChannelAccessPolicyRevision{SchemaVersion: 1, TenantID: account.TenantID, AccountID: account.ID, Provider: account.Provider, PolicyID: id, Revision: revision, Body: raw, PublishedBy: actor, PublishedAt: now.UTC()}
	_, p.Digest, err = CanonicalJSON(p) // Digest is omitted while hashing the complete envelope.
	if err != nil {
		return ChannelAccessPolicyRevision{}, err
	}
	encoded, _, err := CanonicalJSON(p)
	if err != nil || len(encoded) > MaxAccessPolicyDocumentBytes {
		return ChannelAccessPolicyRevision{}, failure(InputInvalid, "/access_policy")
	}
	return p, nil
}
func (p ChannelAccessPolicyRevision) Validate() error {
	if p.SchemaVersion != 1 || !ValidID(p.TenantID) || !ValidID(p.AccountID) || !ValidID(p.PolicyID) || !ValidID(p.PublishedBy) || !ValidVersion(p.Revision) || !ValidDigest(p.Digest) || p.PublishedAt.IsZero() || (p.Provider != Telegram && p.Provider != WeCom) || len(p.Body) > MaxAccessPolicyDocumentBytes {
		return failure(SourceIntegrity, "")
	}
	var body AccessPolicyBody
	decoder := json.NewDecoder(bytes.NewReader(p.Body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		return failure(SourceIntegrity, "")
	}
	canonical, err := canonicalAccessPolicy(body)
	if err != nil || !bytes.Equal(canonical, p.Body) {
		return failure(SourceIntegrity, "")
	}
	expected := p.Digest
	p.Digest = ""
	_, actual, err := CanonicalJSON(p)
	if err != nil || actual != expected {
		return failure(SourceIntegrity, "")
	}
	p.Digest = expected
	encoded, _, err := CanonicalJSON(p)
	if err != nil || len(encoded) > MaxAccessPolicyDocumentBytes {
		return failure(SourceIntegrity, "")
	}
	return nil
}
func DecodeAccessPolicyRevision(raw []byte) (ChannelAccessPolicyRevision, error) {
	if len(raw) > MaxAccessPolicyDocumentBytes {
		return ChannelAccessPolicyRevision{}, failure(SourceIntegrity, "")
	}
	canonical, err := canonicalRaw(raw)
	if err != nil {
		return ChannelAccessPolicyRevision{}, err
	}
	var p ChannelAccessPolicyRevision
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil {
		return ChannelAccessPolicyRevision{}, failure(SourceIntegrity, "")
	}
	if err = p.Validate(); err != nil {
		return ChannelAccessPolicyRevision{}, err
	}
	encoded, _, err := CanonicalJSON(p)
	// Reject unknown, case-varied, omitted or null fields, not only bad digests.
	if err != nil || !bytes.Equal(canonical, encoded) {
		return ChannelAccessPolicyRevision{}, failure(SourceIntegrity, "")
	}
	return p, nil
}

// NormalizeAccessPolicyBody validates and copies draft content for deterministic
// command MACs. It does not resolve references or authorize publication.
func NormalizeAccessPolicyBody(body AccessPolicyBody) (AccessPolicyBody, error) {
	raw, err := canonicalAccessPolicy(body)
	if err != nil {
		return AccessPolicyBody{}, err
	}
	var normalized AccessPolicyBody
	if json.Unmarshal(raw, &normalized) != nil {
		return AccessPolicyBody{}, failure(SourceIntegrity, "")
	}
	return normalized, nil
}
