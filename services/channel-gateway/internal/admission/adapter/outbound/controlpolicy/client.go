// Package controlpolicy is the Gateway adapter for shared Control readers.
package controlpolicy

import shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization/controlhttp"

type Options = shared.Options
type Client = shared.Client
type AuthorizationTarget = shared.AuthorizationTarget
type AuthorizationRead = shared.AuthorizationRead

var (
	ErrInvalid         = shared.ErrInvalid
	ErrUnavailable     = shared.ErrUnavailable
	ErrUnauthorized    = shared.ErrUnauthorized
	ErrIntegrity       = shared.ErrIntegrity
	ErrNotFound        = shared.ErrNotFound
	ErrSnapshotChanged = shared.ErrSnapshotChanged
	ErrSnapshotExpired = shared.ErrSnapshotExpired
	ErrSnapshotStage   = shared.ErrSnapshotStage
)

func New(o Options) (*Client, error) { return shared.New(o) }
