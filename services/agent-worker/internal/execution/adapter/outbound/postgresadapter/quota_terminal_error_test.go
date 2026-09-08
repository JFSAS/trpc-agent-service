package postgresadapter

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	budget "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	"testing"
)

type terminalErrorTx struct {
	pgx.Tx
	err error
}

func (t *terminalErrorTx) Begin(context.Context) (pgx.Tx, error) { return t, nil }
func (t *terminalErrorTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (t *terminalErrorTx) Rollback(context.Context) error { return nil }
func (t *terminalErrorTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return terminalErrorRow{t.err}
}

type terminalErrorRow struct{ err error }

func (r terminalErrorRow) Scan(...any) error { return r.err }
func TestTerminalReaderPreservesDatabaseErrors(t *testing.T) {
	req := budget.RunQuotaRequest{TenantID: "tenant-error", RunID: "run-error", InputDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded, errors.New("database unavailable"), pgx.ErrNoRows} {
		t.Run(failure.Error(), func(t *testing.T) {
			tx := &terminalErrorTx{err: failure}
			_, got := (&Ledger{}).ReadRunQuotaTerminal(context.Background(), tx, req)
			want := failure
			if errors.Is(failure, pgx.ErrNoRows) {
				want = budget.ErrNotReady
			}
			if !errors.Is(got, want) {
				t.Fatalf("got %v; want %v", got, want)
			}
		})
	}
}
