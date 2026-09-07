// Package authorizationpostgres owns Gateway current projection adapters.
package authorizationpostgres

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization/postgres"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
)

var (
	ErrInvalid     = shared.ErrInvalid
	ErrUnavailable = shared.ErrUnavailable
	ErrIntegrity   = shared.ErrIntegrity
	ErrBlocked     = shared.ErrBlocked
	ErrExpired     = shared.ErrExpired
	ErrSuperseded  = shared.ErrSuperseded
)

type Store struct {
	pool         *pgxpool.Pool
	scope, epoch string
}

func New(pool *pgxpool.Pool, scope, epoch string) (*Store, error) {
	if _, err := shared.New(pool, scope, epoch, shared.Gateway); err != nil {
		return nil, err
	}
	return &Store{pool, scope, epoch}, nil
}
func (s *Store) Refresh(ctx context.Context, target domain.AuthorizationTarget, reader domain.AuthorizationReader) error {
	installer, err := shared.New(s.pool, s.scope, s.epoch, shared.Gateway)
	if err != nil {
		return err
	}
	return installer.Refresh(ctx, target, reader)
}
