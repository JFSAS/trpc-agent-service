package domain

// Plan is the runtime-ready projection of one fully validated immutable
// Manifest. It carries only the V1 closure, never mutable Profile values.
type Plan struct {
	Summary                                                    *SummaryPlan
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

// SummaryPlan is the fixed model dependency for Session summary generation.
// It shares the execution per-output ceiling, not an accumulated token budget.
type SummaryPlan struct {
	ModelEndpoint, ModelName string
	ModelCredential          CredentialUse
	EventThreshold           int64
	AddSessionSummary        bool
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
	if p.Summary != nil && p.Summary.ModelCredential.CredentialID != "" {
		use := p.Summary.ModelCredential
		duplicate := false
		for _, existing := range uses {
			if existing == use {
				duplicate = true
				break
			}
		}
		if !duplicate {
			uses = append(uses, use)
		}
	}
	return uses
}

type RuntimeResult struct {
	FinalText                              string
	Snapshot                               []byte
	InputTokens, OutputTokens, TotalTokens int64
}
