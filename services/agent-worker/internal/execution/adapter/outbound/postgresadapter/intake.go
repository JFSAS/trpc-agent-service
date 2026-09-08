package postgresadapter

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/application"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

// NewIntake is the production receipt-first input adapter. Authorized new inputs
// remain unassigned until policy-bound registry/Run promotion is implemented.
// Historical receipts replay first; inputs without authorization keep their
// existing intake contract. This is not a Session hash compatibility fallback.
func NewIntake(pool *pgxpool.Pool) application.IntakeLedger {
	return &intakeLedger{ledger: New(pool)}
}

type intakeLedger struct{ ledger *Ledger }

func (i *intakeLedger) Accept(ctx context.Context, req domain.Requested, policy domain.Policy, limits domain.IntakeLimits) (domain.Receipt, error) {
	return i.ledger.accept(ctx, req, policy, limits, true)
}
