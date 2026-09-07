package domain

import (
	"context"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"time"
)

// AuthorizationTarget is derived from trusted routing/account state.
type AuthorizationTarget struct{ TenantID, AccountID, Provider string }

// AuthorizationRead is fetched state, not an Admission grant. Local monotonic
// timestamps are in-process diagnostics; durable expiry uses a DB clock anchor
// acquired before the reader starts its authenticated current-state request.
type AuthorizationRead struct {
	Manifest             wire.AuthorizationSnapshotManifest
	Policy               wire.AccessPolicyDocument
	StartedAt, ExpiresAt time.Time
}
type AuthorizationReader interface {
	ReadAuthorization(context.Context, AuthorizationTarget, func(context.Context, wire.AuthorizationSnapshotPage) error) (AuthorizationRead, error)
}
