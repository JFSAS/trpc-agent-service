package domain

// ModelCall is the final serialized SDK request. Body contains sensitive history
// and instructions: admission may inspect it synchronously, never log or retain it.
// RequestDigest proves byte identity, not token count or current authorization.
type ModelCall struct {
	TenantID, SessionID, RunID, AttemptID string
	Endpoint, Model, RequestDigest        string
	MaxOutputTokens                       int64
	Body                                  []byte
}
