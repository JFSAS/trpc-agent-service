package postgresadapter

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/manifest/application"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/manifest/domain"
)

// NewTransactionReader binds immutable publication/conflict checks to the caller's
// transaction. Its read lock serializes with Apply's identity/conflict writes and
// survives until the caller commits or rolls back. It never owns the transaction.
func NewTransactionReader(tx pgx.Tx) application.Reader { return transactionReader{tx} }

type transactionReader struct{ tx pgx.Tx }

func (r transactionReader) Read(ctx context.Context, tenant, id string) (domain.Publication, error) {
	if ctx == nil || r.tx == nil {
		return domain.Publication{}, domain.ErrMissing
	}
	if _, err := r.tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(731004287)`); err != nil {
		return domain.Publication{}, err
	}
	return readPublication(ctx, r.tx, tenant, id)
}
