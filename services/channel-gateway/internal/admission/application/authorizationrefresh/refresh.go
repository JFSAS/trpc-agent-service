package authorizationrefresh

import shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization/refresh"

type Desired = shared.Desired
type Directory = shared.Directory
type Refresher = shared.Refresher
type Options = shared.Options
type Summary = shared.Summary
type Service = shared.Service

var (
	ErrInvalid   = shared.ErrInvalid
	ErrDirectory = shared.ErrDirectory
)

func New(d Directory, r Refresher, o Options) (*Service, error) { return shared.New(d, r, o) }
