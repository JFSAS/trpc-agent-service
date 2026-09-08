package domain

import (
	"errors"
	"time"
)

var ErrNotReady = errors.New("BUDGET_TERMINAL_NOT_READY")

type RunTerminalProof struct{ TenantID, RunID, InputDigest, CompletionID, ResultDigest, Disposition string }

func (p RunTerminalProof) Validate() error {
	if (RunQuotaRequest{TenantID: p.TenantID, RunID: p.RunID, InputDigest: p.InputDigest}).Validate() != nil || !id.MatchString(p.CompletionID) || !digest.MatchString(p.ResultDigest) || (p.Disposition != "NO_ATTEMPT" && p.Disposition != "SUCCEEDED_ATTEMPT") {
		return ErrInvalid
	}
	return nil
}

type RunQuotaSettlement struct {
	TenantID, RunID, Fingerprint, CompletionID, ResultDigest, Disposition string
	ReleasedAt                                                            time.Time
}
