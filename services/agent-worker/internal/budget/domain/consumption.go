package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var ErrConsumption = errors.New("BUDGET_CONSUMPTION_EXCEEDED")
var ErrBoundViolation = errors.New("BUDGET_CONSUMPTION_BOUND_VIOLATED")

// Units are distinct dimensions, never interchangeable prices or currency.
// Provider bills need a separately verified pricing/usage conversion contract.
type ConsumptionUnit string

const (
	ModelTokens ConsumptionUnit = "model_tokens"
	ToolUnits   ConsumptionUnit = "tool_units"
	MaxAmount   int64           = 9007199254740991
)

type ConsumptionRequest struct {
	TenantID, RunID, OperationID, InputDigest, BoundDigest string
	Unit                                                   ConsumptionUnit
	Maximum                                                int64
}

func (r ConsumptionRequest) Validate() error {
	if !id.MatchString(r.TenantID) || !id.MatchString(r.RunID) || !id.MatchString(r.OperationID) || !digest.MatchString(r.InputDigest) || !digest.MatchString(r.BoundDigest) || (r.Unit != ModelTokens && r.Unit != ToolUnits) || r.Maximum < 1 || r.Maximum > MaxAmount {
		return ErrInvalid
	}
	return nil
}
func (r ConsumptionRequest) Fingerprint() (string, error) {
	if r.Validate() != nil {
		return "", ErrInvalid
	}
	b, _ := json.Marshal([]any{"consumption-v1", r.TenantID, r.RunID, r.OperationID, r.InputDigest, r.BoundDigest, r.Unit, r.Maximum})
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ConsumptionGrant is an owner-verified CURRENT authorization plus a proved
// operation bound, not caller-supplied capacity. Cap is cumulative consumption
// for this tenant/unit across policy IDs/revisions; there is no implicit reset.
type ConsumptionGrant struct {
	Fingerprint string
	Policy      QuotaReference
	Enabled     bool
	Cap         int64
}

func (g ConsumptionGrant) Validate() error {
	if !digest.MatchString(g.Fingerprint) || g.Policy.Validate() != nil || g.Cap < 0 || g.Cap > MaxAmount {
		return ErrInvalid
	}
	return nil
}

type ConsumptionReservation struct {
	Request     ConsumptionRequest
	Fingerprint string
	Grant       ConsumptionGrant
	ReservedAt  time.Time
}

// Only final, owner-proved usage settles a reservation. Unknown usage is an error
// or Final=false, never a default zero. Evidence must identify this exact input,
// bound, unit and operation via Fingerprint. Zero usage still needs final proof.
type ConsumptionProof struct {
	Fingerprint, EvidenceID, EvidenceDigest string
	Final                                   bool
	Amount                                  int64
}

func (p ConsumptionProof) Validate() error {
	if !p.Final || !digest.MatchString(p.Fingerprint) || !id.MatchString(p.EvidenceID) || !digest.MatchString(p.EvidenceDigest) || p.Amount < 0 || p.Amount > MaxAmount {
		return ErrInvalid
	}
	return nil
}

type ConsumptionSettlement struct {
	TenantID, OperationID, Fingerprint, EvidenceID, EvidenceDigest string
	Unit                                                           ConsumptionUnit
	Maximum, Actual                                                int64
	BoundViolated                                                  bool
	SettledAt                                                      time.Time
}
