package postgresadapter

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
	"os"
	"strings"
	"testing"
	"time"
)

type failingConsumptionReader struct {
	ConsumptionReader
	operation string
	err       error
}

func (f failingConsumptionReader) ReadConsumption(ctx context.Context, tx pgx.Tx, r domain.ConsumptionRequest) (domain.ConsumptionProof, error) {
	if r.OperationID == f.operation {
		return domain.ConsumptionProof{}, f.err
	}
	return f.ConsumptionReader.ReadConsumption(ctx, tx, r)
}
func TestConsumptionReconciliationPostgres(t *testing.T) {
	mu, ru := os.Getenv("WORKER_TEST_MIGRATION_URL"), os.Getenv("WORKER_TEST_RUNTIME_URL")
	if mu == "" || ru == "" {
		t.Skip("requires owned Worker roles")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	open := func(url string) *pgxpool.Pool {
		p, e := pgxpool.New(ctx, url)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(p.Close)
		return p
	}
	owner, a, b := open(mu), open(ru), open(ru)
	if e := migrations.ApplyForRuntime(ctx, owner, "worker_runtime"); e != nil {
		t.Fatal(e)
	}
	prefix := fmt.Sprintf("zzconsume%d", time.Now().UnixNano())
	reader := &consumptionReaderFixture{proofs: map[string]domain.ConsumptionProof{}}
	budget, e := NewConsumption(a, 1000, &consumptionAuthorityFixture{cap: 100, policy: "fixture"}, reader)
	if e != nil {
		t.Fatal(e)
	}
	makeRequest := func(suffix string) domain.ConsumptionRequest {
		r := domain.ConsumptionRequest{TenantID: prefix + suffix, RunID: prefix + suffix, OperationID: prefix + suffix, InputDigest: "sha256:" + strings.Repeat("a", 64), BoundDigest: "sha256:" + strings.Repeat("b", 64), Unit: domain.ModelTokens, Maximum: 60}
		if _, e := budget.Reserve(ctx, r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	broken, unknown, known := makeRequest("-a"), makeRequest("-b"), makeRequest("-c")
	reader.prove(known, 20, known.OperationID)
	sentinel := errors.New("usage reader unavailable")
	scan, e := NewConsumptionSettler(a, failingConsumptionReader{reader, broken.OperationID, sentinel})
	if e != nil {
		t.Fatal(e)
	}
	result, e := scan.Reconcile(ctx, prefix, 1)
	if !errors.Is(e, sentinel) || result.Failed != 1 || result.Next != broken.OperationID {
		t.Fatal(result, e)
	}
	result, e = scan.Reconcile(ctx, result.Next, 1)
	if e != nil || result.Waiting != 1 || result.Next != unknown.OperationID {
		t.Fatal(result, e)
	}
	result, e = scan.Reconcile(ctx, result.Next, 1)
	if e != nil || result.Settled != 1 || result.Next != known.OperationID {
		t.Fatal(result, e)
	}
	result, e = scan.Reconcile(ctx, result.Next, 1)
	if e != nil || result.Next != "" || result.Scanned != 0 {
		t.Fatal("tail wrap", result, e)
	}
	// Late keys below the cursor are visited on a fresh scan. Independent nodes
	// may select the same row: immutable settlement, not cursor, prevents refund twice.
	late := makeRequest("-0")
	reader.prove(late, 0, late.OperationID)
	sa, e := NewConsumptionSettler(a, reader)
	if e != nil {
		t.Fatal(e)
	}
	sb, e := NewConsumptionSettler(b, reader)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 2)
	for _, s := range []*ConsumptionSettler{sa, sb} {
		go func(s *ConsumptionSettler) { _, e := s.Reconcile(ctx, prefix, 100); done <- e }(s)
	}
	for n := 0; n < 2; n++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	for _, r := range []domain.ConsumptionRequest{broken, unknown, known, late} {
		want := 0
		if r.OperationID == known.OperationID || r.OperationID == late.OperationID {
			want = 1
		}
		var n int
		if e := a.QueryRow(ctx, `SELECT count(*) FROM worker_consumption_settlements WHERE operation_id=$1`, r.OperationID).Scan(&n); e != nil || n != want {
			t.Fatal(r.OperationID, n, e)
		}
	}
	for _, limit := range []int{0, 1001} {
		if _, e = sa.Reconcile(ctx, "", limit); !errors.Is(e, domain.ErrInvalid) {
			t.Fatal("unbounded scan", e)
		}
	}
	if _, e = sa.Reconcile(nil, "", 1); !errors.Is(e, domain.ErrInvalid) {
		t.Fatal(e)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, e = sa.Reconcile(cancelled, "", 1); e == nil {
		t.Fatal("cancel ignored")
	}
	t.Log("CONSUMPTION_RECONCILIATION=PASS errors=ADVANCE unknown=HELD late_key=VISITED concurrent=IDEMPOTENT")
}
