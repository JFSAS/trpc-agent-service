package domain

// PolicyContinuity describes locally retained policy history only. Contiguous
// history does not prove current Control state, principal status or freshness.
type PolicyContinuity struct {
	ObservedRevision   int64
	ContiguousRevision int64
	BlockedReason      string
}

func (p PolicyContinuity) HasGap() bool { return p.ContiguousRevision < p.ObservedRevision }
