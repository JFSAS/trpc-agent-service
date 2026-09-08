package domain

import (
	"strings"
	"testing"
)

func TestConsumptionFingerprintAndBounds(t *testing.T) {
	r := ConsumptionRequest{TenantID: "tenant", RunID: "run", OperationID: "operation", InputDigest: "sha256:" + strings.Repeat("a", 64), BoundDigest: "sha256:" + strings.Repeat("b", 64), Unit: ModelTokens, Maximum: 100}
	first, err := r.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"tenant", "run", "operation", "input", "bound", "unit", "maximum"} {
		t.Run(field, func(t *testing.T) {
			v := r
			switch field {
			case "tenant":
				v.TenantID = "foreign"
			case "run":
				v.RunID = "other"
			case "operation":
				v.OperationID = "other"
			case "input":
				v.InputDigest = v.BoundDigest
			case "bound":
				v.BoundDigest = v.InputDigest
			case "unit":
				v.Unit = ToolUnits
			case "maximum":
				v.Maximum++
			}
			fingerprint, e := v.Fingerprint()
			if e != nil || fingerprint == first {
				t.Fatal(fingerprint, e)
			}
		})
	}
	for _, amount := range []int64{0, -1, MaxAmount + 1} {
		v := r
		v.Maximum = amount
		if v.Validate() == nil {
			t.Fatal("invalid maximum accepted", amount)
		}
	}
	r.Unit = "USD"
	if r.Validate() == nil {
		t.Fatal("currency confused with consumption units")
	}
}

func TestConsumptionRequiresFinalKnownProof(t *testing.T) {
	d := "sha256:" + strings.Repeat("a", 64)
	p := ConsumptionProof{Fingerprint: d, EvidenceID: "usage", EvidenceDigest: d, Final: true, Amount: 0}
	if p.Validate() != nil {
		t.Fatal("verified zero usage rejected")
	}
	p.Final = false
	if p.Validate() == nil {
		t.Fatal("unknown treated as zero")
	}
	p.Final = true
	p.Amount = MaxAmount + 1
	if p.Validate() == nil {
		t.Fatal("overflow accepted")
	}
	p.Amount = -1
	if p.Validate() == nil {
		t.Fatal("negative refund accepted")
	}
}
