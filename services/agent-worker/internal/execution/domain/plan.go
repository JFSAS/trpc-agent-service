package domain

// Plan is the runtime-ready projection of one fully validated immutable
// Manifest. It carries only the V1 closure, never mutable Profile values.
type Plan struct {
	TenantID, ManifestID, ManifestDigest, DeploymentRevisionID string
	ProfileID                                                  string
	ProfileRevision                                            int64
	NodeID, Instruction                                        string
	ModelEndpoint, ModelName                                   string
	Temperature                                                *float64
	NodeMaxOutputTokens                                        *int64
	MaxOutputTokens, MaxRunSeconds                             int64
	ModelCredential, SessionCredential                         CredentialUse
	SessionTarget                                              StorageTarget
}
type CredentialUse struct{ CredentialID, Purpose, AudienceDigest string }
type StorageTarget struct {
	Host                        string
	Port                        uint16
	Database, Username, SSLMode string
}

func (p Plan) Uses() []CredentialUse {
	uses := make([]CredentialUse, 0, 2)
	if p.ModelCredential.CredentialID != "" {
		uses = append(uses, p.ModelCredential)
	}
	if p.SessionCredential.CredentialID != "" {
		uses = append(uses, p.SessionCredential)
	}
	return uses
}

type RuntimeResult struct {
	// UsageKnown distinguishes an explicit final zero from absent/unverifiable usage.
	// It can accompany an execution error; it authorizes accounting validation,
	// never Session Stage or successful Completion.
	UsageKnown                             bool
	FinalText                              string
	Snapshot                               []byte
	InputTokens, OutputTokens, TotalTokens int64
}
