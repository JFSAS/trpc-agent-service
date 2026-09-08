package postgresadapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	ledgerpg "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/postgresadapter"
	execution "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
)

type faultyTerminalReader struct {
	real TerminalReader
	run  string
	err  error
}

func (f faultyTerminalReader) ReadRunQuotaTerminal(ctx context.Context, tx pgx.Tx, r domain.RunQuotaRequest) (domain.RunTerminalProof, error) {
	if r.RunID == f.run {
		return domain.RunTerminalProof{}, f.err
	}
	return f.real.ReadRunQuotaTerminal(ctx, tx, r)
}

func TestRunQuotaReconciliationPostgres(t *testing.T) {
	mu, ru := os.Getenv("WORKER_TEST_MIGRATION_URL"), os.Getenv("WORKER_TEST_RUNTIME_URL")
	if mu == "" || ru == "" {
		t.Skip("requires owned Worker PostgreSQL roles")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	open := func(url string) *pgxpool.Pool {
		p, err := pgxpool.New(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	owner, a, b := open(mu), open(ru), open(ru)
	if err := migrations.ApplyForRuntime(ctx, owner, "worker_runtime"); err != nil {
		t.Fatal(err)
	}
	qa, err := NewRunQuota(a, 1000)
	if err != nil {
		t.Fatal(err)
	}
	qb, err := NewRunQuota(b, 1000)
	if err != nil {
		t.Fatal(err)
	}
	ledger := ledgerpg.New(a)
	sa, err := NewRunQuotaSettler(qa, ledger)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := NewRunQuotaSettler(qb, ledgerpg.New(b))
	if err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("zreconcile%d", time.Now().UnixNano())
	policy := execution.Policy{Version: "reconcile", MaxRunAge: time.Hour, MaxReplyAge: time.Hour, MaxFutureSkew: time.Minute, LeaseTTL: 3 * time.Second, RenewalInterval: time.Second, RetryBackoff: time.Millisecond, MaxAttempts: 3}
	makeRun := func(suffix string, terminal bool) domain.RunQuotaRequest {
		t.Helper()
		tenant := prefix + suffix
		req := execution.Requested{EventID: tenant + "event", RunID: tenant, AdmissionID: tenant + "admission", EventDigest: execution.Digest([]byte(tenant + "event")), RunDigest: execution.Digest([]byte(tenant)), Route: execution.Route{TenantID: tenant, Provider: "telegram", AccountID: "account", BindingID: "binding", DeploymentRevisionID: "revision", ManifestRef: "manifest", ManifestDigest: execution.Digest([]byte("manifest")), Generation: 1}, Input: execution.Input{ConversationID: "42", SenderID: "43", Text: "reconcile fixture", ReceivedAt: time.Now().UTC()}}
		r := domain.RunQuotaRequest{TenantID: tenant, RunID: req.RunID, InputDigest: req.RunDigest}
		if _, e := qa.ReserveRunQuota(ctx, r, quotaDocument(t, tenant, 1, 10)); e != nil {
			t.Fatal(e)
		}
		if _, e := ledger.Accept(ctx, req, policy, execution.IntakeLimits{MaxQueuedRuns: 100, MaxRetainedRuns: 1000}); e != nil {
			t.Fatal(e)
		}
		if terminal {
			if done, e := ledger.Terminalize(ctx, tenant, req.RunID, "UNSUPPORTED_MANIFEST"); e != nil || !done {
				t.Fatal(done, e)
			}
		}
		return r
	}
	broken := makeRun("-a", true)
	waiting := makeRun("-b", false)
	ended := makeRun("-c", true)
	sentinel := errors.New("terminal owner unavailable")
	faulty, err := NewRunQuotaSettler(qa, faultyTerminalReader{ledger, broken.RunID, sentinel})
	if err != nil {
		t.Fatal(err)
	}
	// A failing early row reports its cause and advances; it does not prevent later
	// known-ended rows from releasing. Error does not refund the failing row.
	result, err := faulty.Reconcile(ctx, prefix, 1)
	if !errors.Is(err, sentinel) || result.Scanned != 1 || result.Failed != 1 || result.Next != broken.RunID {
		t.Fatal(result, err)
	}
	result, err = faulty.Reconcile(ctx, result.Next, 1)
	if err != nil || result.Waiting != 1 || result.Next != waiting.RunID {
		t.Fatal(result, err)
	}
	result, err = faulty.Reconcile(ctx, result.Next, 1)
	if err != nil || result.Settled != 1 || result.Next != ended.RunID {
		t.Fatal(result, err)
	}
	result, err = faulty.Reconcile(ctx, result.Next, 1)
	if err != nil || result.Scanned != 0 || result.Next != "" {
		t.Fatal("tail must wrap", result, err)
	}
	assertSettled := func(r domain.RunQuotaRequest, want int) {
		t.Helper()
		var n int
		if e := a.QueryRow(ctx, `SELECT count(*) FROM worker_run_quota_settlements WHERE run_id=$1`, r.RunID).Scan(&n); e != nil || n != want {
			t.Fatal(r.RunID, n, e)
		}
	}
	assertSettled(broken, 0)
	assertSettled(waiting, 0)
	assertSettled(ended, 1)
	// A later insert before the old cursor and a newly terminalized unknown hold
	// are both visited after wrap. No timestamp or status-only release is involved.
	late := makeRun("-0", true)
	if done, e := ledger.Terminalize(ctx, waiting.TenantID, waiting.RunID, "UNSUPPORTED_MANIFEST"); e != nil || !done {
		t.Fatal(done, e)
	}
	cursor := result.Next
	for n := 0; n < 100; n++ {
		result, err = sa.Reconcile(ctx, cursor, 1)
		if err != nil {
			t.Fatal(result, err)
		}
		cursor = result.Next
		if cursor == "" {
			break
		}
		if n == 99 {
			t.Fatal("scan did not wrap")
		}
	}
	for _, r := range []domain.RunQuotaRequest{late, broken, waiting, ended} {
		assertSettled(r, 1)
	}
	// Independent reconcilers can select the same candidate. Immutable settlement
	// identity, rather than a process-local lease or cursor, is the release guard.
	concurrent := makeRun("-d", true)
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s := sa
			if n%2 == 1 {
				s = sb
			}
			_, e := s.Reconcile(ctx, ended.RunID, 1)
			failures <- e
		}(n)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	assertSettled(concurrent, 1)
	// Restart/reset cursor replays no additional charge/release for settled inputs.
	if result, err = sb.Reconcile(ctx, prefix, 100); err != nil || result.Scanned != 0 {
		t.Fatal("restart", result, err)
	}
	canceled, cancelNow := context.WithCancel(ctx)
	cancelNow()
	if _, err = sa.Reconcile(canceled, prefix, 1); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	for _, limit := range []int{0, 1001} {
		if _, err = sa.Reconcile(ctx, prefix, limit); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("unbounded batch", err)
		}
	}
	if _, err = sa.Reconcile(ctx, "bad cursor", 1); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
	t.Log("RECONCILIATION=PASS bounded_pages=1 poison_skipped=YES unknown_held=YES wrap_late_input=YES replicas=2 duplicate_release=0 restart=PASS cancel=PASS")
}
