package domain

import "time"

// AdmissionAuthorization records the local decision at acceptance, not a bearer
// capability. Worker must independently check current state before execution.
// This fact intentionally contains no complete policy body or principal list.
type AdmissionAuthorization struct {
	ExternalUserID    string    `json:"external_user_id"`
	BindingID         string    `json:"binding_id"`
	RouteGeneration   int64     `json:"route_generation"`
	SchemaVersion     int       `json:"schema_version"`
	ScopeID           string    `json:"scope_id"`
	SourceEpoch       string    `json:"source_epoch"`
	Generation        int64     `json:"generation"`
	TenantID          string    `json:"tenant_id"`
	AccountID         string    `json:"account_id"`
	Provider          string    `json:"provider"`
	PrincipalID       string    `json:"principal_id,omitempty"`
	PrincipalRevision int64     `json:"principal_revision,omitempty"`
	PolicyID          string    `json:"policy_id"`
	PolicyRevision    int64     `json:"policy_revision"`
	PolicyDigest      string    `json:"policy_digest"`
	Operation         string    `json:"operation"`
	ConversationKind  string    `json:"conversation_kind"`
	ConversationID    string    `json:"conversation_id"`
	Decision          string    `json:"decision"`
	Reason            string    `json:"reason,omitempty"`
	ReadStartedAt     time.Time `json:"read_started_at"`
	EvaluatedAt       time.Time `json:"evaluated_at"`
	FreshUntil        time.Time `json:"fresh_until"`
}

func (a AdmissionAuthorization) ValidateFor(r Requested) error {
	if a.SchemaVersion != 1 || a.ScopeID == "" || a.SourceEpoch == "" || a.Generation < 1 || a.PrincipalID == "" || a.PrincipalRevision < 1 || a.PolicyID == "" || a.PolicyRevision < 1 || !DigestValid(a.PolicyDigest) || a.Decision != "ALLOW" || a.Reason != "" || a.Operation != "message.send" || a.TenantID != r.Route.TenantID || a.AccountID != r.Route.AccountID || a.Provider != r.Route.Provider || a.BindingID != r.Route.BindingID || a.RouteGeneration != r.Route.Generation || a.ExternalUserID != r.Input.SenderID || a.ConversationID != r.Input.ConversationID || (a.ConversationKind != "private" && a.ConversationKind != "group") || a.ReadStartedAt.IsZero() || a.EvaluatedAt.Before(a.ReadStartedAt) || !a.EvaluatedAt.Before(a.FreshUntil) || a.FreshUntil.Sub(a.ReadStartedAt) > 30*time.Second {
		return ErrInvalid
	}
	return nil
}
