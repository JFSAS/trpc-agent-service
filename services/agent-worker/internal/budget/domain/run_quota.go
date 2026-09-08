// Package domain defines budget-owner identities without transport or SQL types.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var ErrInvalid = errors.New("BUDGET_INVALID")
var ErrConflict = errors.New("BUDGET_RESERVATION_CONFLICT")
var ErrQuota = errors.New("BUDGET_RUN_QUOTA_EXCEEDED")
var ErrCapacity = errors.New("BUDGET_STORAGE_CAPACITY")
var id = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type RunQuotaRequest struct{ TenantID, RunID, InputDigest string }

func (r RunQuotaRequest) Validate() error {
	if !id.MatchString(r.TenantID) || !id.MatchString(r.RunID) || !digest.MatchString(r.InputDigest) {
		return ErrInvalid
	}
	return nil
}

type QuotaReference struct {
	ID       string
	Revision int64
	Digest   string
}

func (q QuotaReference) Validate() error {
	if !id.MatchString(q.ID) || q.Revision < 1 || q.Revision > 9007199254740991 || !digest.MatchString(q.Digest) {
		return ErrInvalid
	}
	return nil
}
func (r RunQuotaRequest) Fingerprint(q QuotaReference) (string, error) {
	if r.Validate() != nil || q.Validate() != nil {
		return "", ErrInvalid
	}
	raw, _ := json.Marshal([]any{"run-quota-v1", r.TenantID, r.RunID, r.InputDigest, q.ID, q.Revision, q.Digest})
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

type RunQuotaReservation struct {
	TenantID, RunID, Fingerprint string
	Quota                        QuotaReference
	ReservedAt                   time.Time
}
