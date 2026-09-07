// Code generated from api/events/execution/v1/run-requested.schema.json; DO NOT EDIT.

// Package executionv1 contains transport DTOs, not Gateway or Worker domain entities.
package executionv1

// RunRequested is defined by the versioned execution JSON Schema.
type RunRequested struct {
	AdmissionID   string                  `json:"admission_id"`
	Authorization *AdmissionAuthorization `json:"authorization,omitempty"`
	EventID       string                  `json:"event_id"`
	Input         Inbound                 `json:"input"`
	Route         RouteSnapshot           `json:"route"`
	RunID         string                  `json:"run_id"`
	SchemaVersion int64                   `json:"schema_version"`
}

// AdmissionAuthorization is defined by the versioned execution JSON Schema.
type AdmissionAuthorization struct {
	AccountID         string `json:"account_id"`
	BindingID         string `json:"binding_id"`
	ConversationID    string `json:"conversation_id"`
	ConversationKind  string `json:"conversation_kind"`
	Decision          string `json:"decision"`
	EvaluatedAt       string `json:"evaluated_at"`
	ExternalUserID    string `json:"external_user_id"`
	FreshUntil        string `json:"fresh_until"`
	Generation        int64  `json:"generation"`
	Operation         string `json:"operation"`
	PolicyDigest      string `json:"policy_digest"`
	PolicyID          string `json:"policy_id"`
	PolicyRevision    int64  `json:"policy_revision"`
	PrincipalID       string `json:"principal_id"`
	PrincipalRevision int64  `json:"principal_revision"`
	Provider          string `json:"provider"`
	ReadStartedAt     string `json:"read_started_at"`
	RouteGeneration   int64  `json:"route_generation"`
	SchemaVersion     int64  `json:"schema_version"`
	ScopeID           string `json:"scope_id"`
	SourceEpoch       string `json:"source_epoch"`
	TenantID          string `json:"tenant_id"`
}

// EventKey is defined by the versioned execution JSON Schema.
type EventKey struct {
	AccountID string `json:"account_id"`
	EventID   string `json:"event_id"`
	Provider  string `json:"provider"`
}

// Inbound is defined by the versioned execution JSON Schema.
type Inbound struct {
	ConversationID string        `json:"conversation_id"`
	Key            EventKey      `json:"key"`
	Kind           string        `json:"kind"`
	ReceivedAt     string        `json:"received_at"`
	ReplyContext   *ReplyContext `json:"reply_context"`
	SenderID       string        `json:"sender_id"`
	SourceDigest   string        `json:"source_digest"`
	Text           string        `json:"text"`
	ThreadID       string        `json:"thread_id,omitempty"`
}

// ReplyContext is defined by the versioned execution JSON Schema.
type ReplyContext struct {
	CallbackReqID   string `json:"callback_req_id,omitempty"`
	ChatID          string `json:"chat_id,omitempty"`
	ChatType        string `json:"chat_type,omitempty"`
	ChatIDOrUserID  string `json:"chatid_or_userid,omitempty"`
	MessageThreadID string `json:"message_thread_id,omitempty"`
	ReceivedAt      string `json:"received_at,omitempty"`
	SourceMessageID string `json:"source_message_id,omitempty"`
}

// RouteSnapshot is defined by the versioned execution JSON Schema.
type RouteSnapshot struct {
	AccountID            string `json:"account_id"`
	BindingID            string `json:"binding_id"`
	DeploymentRevisionID string `json:"deployment_revision_id"`
	Generation           int64  `json:"generation"`
	ManifestDigest       string `json:"manifest_digest"`
	ManifestRef          string `json:"manifest_ref"`
	Provider             string `json:"provider"`
	TenantID             string `json:"tenant_id"`
}
