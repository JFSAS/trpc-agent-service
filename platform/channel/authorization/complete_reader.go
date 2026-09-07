package authorization

import (
	"context"
	"errors"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

type PolicyDependencyReader interface {
	ReadPolicyDependencies(context.Context, wire.AccessPolicyDocument) (PolicyDependencies, error)
}

// CompleteReader keeps dependency I/O inside the original current-read expiry.
// The installer additionally covers all I/O and install time with its DB anchor.
// Historical definitions must never establish a new freshness interval.
type CompleteReader struct {
	Current     AuthorizationReader
	Definitions PolicyDependencyReader
}

var ErrDependencyExpired = errors.New("CHANNEL_AUTHORIZATION_DEPENDENCY_EXPIRED")

func (r CompleteReader) ReadAuthorization(ctx context.Context, t AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (AuthorizationRead, error) {
	zero := AuthorizationRead{}
	if ctx == nil || r.Current == nil || r.Definitions == nil {
		return zero, ErrDependencyIntegrity
	}
	out, e := r.Current.ReadAuthorization(ctx, t, stage)
	if e != nil {
		return zero, e
	}
	if out.StartedAt.IsZero() || !out.ExpiresAt.After(out.StartedAt) || !time.Now().Before(out.ExpiresAt) {
		return zero, ErrDependencyExpired
	}
	if out.Policy.Body.AccessMode == "DENY_ALL" {
		out.Dependencies = nil
		return out, nil
	}
	bounded, cancel := context.WithDeadline(ctx, out.ExpiresAt)
	defer cancel()
	pair, e := r.Definitions.ReadPolicyDependencies(bounded, out.Policy)
	if e != nil {
		return zero, e
	}
	pair, e = MatchPolicyDependencies(out.Policy, pair.Session, pair.Quota)
	if e != nil {
		return zero, e
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	if !time.Now().Before(out.ExpiresAt) {
		return zero, ErrDependencyExpired
	}
	out.Dependencies = &pair
	return out, nil
}
