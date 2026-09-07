package application

import (
	"context"
	"errors"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

var (
	ErrNotFound    = errors.New("CHANNEL_POLICY_DEFINITION_NOT_FOUND")
	ErrUnavailable = errors.New("CHANNEL_POLICY_DEFINITION_UNAVAILABLE")
	ErrIntegrity   = errors.New("CHANNEL_POLICY_DEFINITION_INTEGRITY")
)

// PublishedRevisionReader is an internal owner read port. The caller has an
// authenticated tenant context; no transport endpoint exposes arbitrary tenants.
type PublishedRevisionReader interface {
	ReadExact(context.Context, string, domain.Kind, string, int64) (domain.Revision, error)
}
