package application

import (
	"context"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/manifest/domain"
)

type Reader interface {
	Read(context.Context, string, string) (domain.Publication, error)
}
type Projection interface {
	Reader
	Apply(context.Context, domain.Publication, int) error
}
