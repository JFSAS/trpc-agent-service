package bootstrap

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/manifestadapter"
	ledgerpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/postgresadapter"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	manifestpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/manifest/adapter/outbound/postgresadapter"
	sessionpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/adapter/outbound/postgres"
)

type pendingManifestTarget struct{ contract string }

func (p pendingManifestTarget) VerifyPendingTarget(ctx context.Context, tx pgx.Tx, route domain.Route) error {
	reader := manifestadapter.Reader{Projection: manifestpg.NewTransactionReader(tx), ContractDigest: p.contract}
	_, err := reader.Resolve(ctx, route)
	return err
}

// newPendingSessionAuthorizer composes the real owners for one pending message.
// It is a prerequisite for atomic promotion, not a broker command/reset route.
func newPendingSessionAuthorizer(l *ledgerpg.Ledger, c Config, event string) (*sessionpg.CurrentAuthorizer, error) {
	if c.Authorization == nil || c.Authorization.validate() != nil || !domain.DigestValid(c.PlatformContractDigest) {
		return nil, domain.ErrInvalid
	}
	routes, err := l.PendingRouteAuthority(event, c.Authorization.ScopeID, c.Authorization.SourceEpoch, pendingManifestTarget{c.PlatformContractDigest})
	if err != nil {
		return nil, err
	}
	return sessionpg.NewCurrentAuthorizer(c.Authorization.ScopeID, c.Authorization.SourceEpoch, routes)
}
