package postgresadapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
)

func TestSingleModelDispatchPostgres(t *testing.T) {
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
	prefix := fmt.Sprintf("dispatch%d", time.Now().UnixNano())
	policy := domain.Policy{Version: "usage", MaxRunAge: time.Hour, MaxReplyAge: time.Hour, MaxFutureSkew: time.Minute, LeaseTTL: 3 * time.Second, RenewalInterval: time.Second, RetryBackoff: time.Millisecond, MaxAttempts: 3}
	req := domain.Requested{EventID: prefix + "event", RunID: prefix + "run", AdmissionID: prefix + "admission", EventDigest: domain.Digest([]byte(prefix + "event")), RunDigest: domain.Digest([]byte(prefix)), Route: domain.Route{TenantID: prefix, Provider: "telegram", AccountID: "account", BindingID: "binding", DeploymentRevisionID: "revision", ManifestRef: "manifest", ManifestDigest: domain.Digest([]byte("manifest")), Generation: 1}, Input: domain.Input{ConversationID: "42", SenderID: "43", Text: "usage fixture", ReceivedAt: time.Now().UTC()}}
	if _, e := ledger.Accept(ctx, req, policy, domain.IntakeLimits{MaxQueuedRuns: 100, MaxRetainedRuns: 1000}); e != nil {
		t.Fatal(e)
	}
	grant, e := ledger.Claim(ctx, domain.ClaimRequest{TenantID: prefix, RunID: req.RunID, WorkerID: "usage-test", MaxRunSeconds: 120, MaxActive: 100})
	if e != nil {
		t.Fatal(e)
	}
	// A forged grant must not consume the first dispatch opportunity.
	forged := grant
	forged.Token = "wrong"
	if e = ledger.MarkExecuting(ctx, forged); !errors.Is(e, domain.ErrFenced) {
		t.Fatal("forged dispatch", e)
	}
	results := make(chan error, 8)
	start := make(chan struct{})
	for n := 0; n < 8; n++ {
		current := ledger
		if n%2 == 1 {
			current = other
		}
		go func(l *Ledger) { <-start; results <- l.MarkExecuting(ctx, grant) }(current)
	}
	close(start)
	wins, denied := 0, 0
	for n := 0; n < 8; n++ {
		e := <-results
		if e == nil {
			wins++
		} else if errors.Is(e, domain.ErrFenced) {
			denied++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || denied != 7 {
		t.Fatalf("dispatch permits: wins=%d fenced=%d", wins, denied)
	}
	var original, after time.Time
	if e = a.QueryRow(ctx, `SELECT agent_started_at FROM execution_attempts WHERE attempt_id=$1`, grant.AttemptID).Scan(&original); e != nil || original.IsZero() {
		t.Fatal(e)
	}
	// Old binaries must also fail instead of interpreting an idempotent UPDATE
	// as a second dispatch permit. Clearing/reverting the state is forbidden.
	for _, sql := range []string{
		`UPDATE execution_attempts SET status='EXECUTING',agent_started_at=COALESCE(agent_started_at,clock_timestamp()) WHERE attempt_id=$1`,
		`UPDATE execution_attempts SET status='PREPARING' WHERE attempt_id=$1`,
		`UPDATE execution_attempts SET agent_started_at=NULL WHERE attempt_id=$1`,
	} {
		if _, e = a.Exec(ctx, sql, grant.AttemptID); e == nil {
			t.Fatal("direct repeat/regression accepted")
		}
	}
	// A new owner object/process must not interpret a previous successful dispatch
	// receipt as permission to issue the same physical model call again.
	if e = New(b).MarkExecuting(ctx, grant); !errors.Is(e, domain.ErrFenced) {
		t.Fatal("restart replay granted", e)
	}
	if e = a.QueryRow(ctx, `SELECT agent_started_at FROM execution_attempts WHERE attempt_id=$1`, grant.AttemptID).Scan(&after); e != nil || !original.Equal(after) {
		t.Fatal("start time changed", e)
	}
	if _, e = ledger.Renew(ctx, grant); e != nil {
		t.Fatal("dispatch fence broke renewal", e)
	}
	if e = ledger.RecordModelUsage(ctx, grant, domain.RuntimeResult{UsageKnown: true, InputTokens: 1, OutputTokens: 2, TotalTokens: 3}); e != nil {
		t.Fatal("dispatch fence broke usage", e)
	}
	if e = ledger.FailAttempt(ctx, grant, "SESSION_STAGE_FAILED", false); e != nil {
		t.Fatal(e)
	}
	if e = other.MarkExecuting(ctx, grant); !errors.Is(e, domain.ErrFenced) {
		t.Fatal("terminal dispatch granted", e)
	}
	t.Log("SINGLE_MODEL_DISPATCH=PASS pools=2 contenders=8 permits=1 fenced=7 restart=DENIED renewal=PASS usage=PASS terminal=DENIED")
}
