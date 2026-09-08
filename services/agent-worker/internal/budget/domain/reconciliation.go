package domain

// Reconciliation is one bounded accounting scan. Cursor is
// only a scheduling hint; correctness is provided by immutable DB settlements.
// Settled counts confirmed results including idempotent replays, not new refunds.
type Reconciliation struct {
	Next                              string
	Scanned, Settled, Waiting, Failed int
}

func ValidateReconciliation(after string, limit int) error {
	if (after != "" && !id.MatchString(after)) || limit < 1 || limit > 1000 {
		return ErrInvalid
	}
	return nil
}
