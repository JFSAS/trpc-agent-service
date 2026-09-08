package postgresadapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
)

func TestModelUsageFactsPostgres(t *testing.T) {
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
	ledger, other := New(a), New(b)
	prefix := fmt.Sprintf("usage%d", time.Now().UnixNano())
	policy := domain.Policy{Version: "usage", MaxRunAge: time.Hour, MaxReplyAge: time.Hour, MaxFutureSkew: time.Minute, LeaseTTL: 3 * time.Second, RenewalInterval: time.Second, RetryBackoff: time.Millisecond, MaxAttempts: 3}
	req := domain.Requested{EventID: prefix + "event", RunID: prefix + "run", AdmissionID: prefix + "admission", EventDigest: domain.Digest([]byte(prefix + "event")), RunDigest: domain.Digest([]byte(prefix)), Route: domain.Route{TenantID: prefix, Provider: "telegram", AccountID: "account", BindingID: "binding", DeploymentRevisionID: "revision", ManifestRef: "manifest", ManifestDigest: domain.Digest([]byte("manifest")), Generation: 1}, Input: domain.Input{ConversationID: "42", SenderID: "43", Text: "usage fixture", ReceivedAt: time.Now().UTC()}}
	if _, e := ledger.Accept(ctx, req, policy, domain.IntakeLimits{MaxQueuedRuns: 100, MaxRetainedRuns: 1000}); e != nil {
		t.Fatal(e)
	}
	grant, e := ledger.Claim(ctx, domain.ClaimRequest{TenantID: prefix, RunID: req.RunID, WorkerID: "usage-test", MaxRunSeconds: 120, MaxActive: 100})
	if e != nil {
		t.Fatal(e)
	}
	result := domain.RuntimeResult{UsageKnown: true, InputTokens: 37, OutputTokens: 11, TotalTokens: 48}
	if e = ledger.RecordModelUsage(ctx, grant, result); !errors.Is(e, domain.ErrFenced) {
		t.Fatal("PREPARING accepted", e)
	}
	if e = ledger.MarkExecuting(ctx, grant); e != nil {
		t.Fatal(e)
	}
	unknown := result
	unknown.UsageKnown = false
	if e = ledger.RecordModelUsage(ctx, grant, unknown); !errors.Is(e, domain.ErrInvalid) {
		t.Fatal("unknown inserted", e)
	}
	forged := grant
	forged.Token = "foreign"
	if e = ledger.RecordModelUsage(ctx, forged, result); !errors.Is(e, domain.ErrFenced) {
		t.Fatal("forged token", e)
	}
	failures := make(chan error, 8)
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			l := ledger
			if n%2 == 1 {
				l = other
			}
			failures <- l.RecordModelUsage(ctx, grant, result)
		}(n)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	operation := domain.ModelOperationID(grant)
	var count, total int64
	var stamp time.Time
	if e = a.QueryRow(ctx, `SELECT count(*),sum(total_tokens),min(recorded_at) FROM execution_model_usage WHERE operation_id=$1`, operation).Scan(&count, &total, &stamp); e != nil || count != 1 || total != 48 {
		t.Fatal(count, total, e)
	}
	changed := result
	changed.OutputTokens++
	changed.TotalTokens++
	if e = ledger.RecordModelUsage(ctx, grant, changed); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("rewrote usage", e)
	}
	// Failure after model execution must not discard committed consumption facts.
	if e = ledger.FailAttempt(ctx, grant, "SESSION_STAGE_FAILED", false); e != nil {
		t.Fatal(e)
	}
	if e = other.RecordModelUsage(ctx, grant, result); e != nil {
		t.Fatal("authenticated terminal replay", e)
	}
	if e = other.RecordModelUsage(ctx, forged, result); !errors.Is(e, domain.ErrFenced) {
		t.Fatal("terminal replay bypassed identity", e)
	}
	var after time.Time
	if e = b.QueryRow(ctx, `SELECT recorded_at FROM execution_model_usage WHERE operation_id=$1`, operation).Scan(&after); e != nil || !after.Equal(stamp) {
		t.Fatal("replay changed first timestamp", after, e)
	}
	for _, sql := range []string{`UPDATE execution_model_usage SET total_tokens=total_tokens WHERE operation_id=$1`, `DELETE FROM execution_model_usage WHERE operation_id=$1`} {
		if _, e = a.Exec(ctx, sql, operation); e == nil {
			t.Fatal("mutable usage fact")
		}
	}
	t.Log("MODEL_USAGE_FACT=PASS real_attempt=YES concurrent_writes=8 rows=1 total=48 stage_failure_survival=YES authenticated_replay=YES immutable=YES")
}
