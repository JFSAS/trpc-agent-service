package postgresadapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
)

type authority func(context.Context, pgx.Tx, domain.Scope, string, string) error

func (f authority) AuthorizeSession(c context.Context, tx pgx.Tx, s domain.Scope, a, o string) error {
	return f(c, tx, s, a, o)
}
func TestConversationRegistryPostgres(t *testing.T) {
	migrationURL, runtimeURL := os.Getenv("WORKER_TEST_MIGRATION_URL"), os.Getenv("WORKER_TEST_RUNTIME_URL")
	if migrationURL == "" || runtimeURL == "" {
		t.Skip("requires owned Worker PostgreSQL roles")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	owner, e := pgxpool.New(ctx, migrationURL)
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Close()
	runtime, e := pgxpool.New(ctx, runtimeURL)
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	if e = migrations.ApplyForRuntime(ctx, owner, "worker_runtime"); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(ctx, `CREATE TABLE IF NOT EXISTS worker_session_registry_test_intakes(id text PRIMARY KEY,session_id text NOT NULL,generation bigint NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	s := domain.Scope{TenantID: "tenant", Provider: "telegram", AccountID: fmt.Sprintf("account_%d", time.Now().UnixNano()), ConversationID: "group", ConversationKind: "group", BindingID: "binding", DeploymentRevisionID: "revision", PolicyID: "policy", PolicyRevision: 1, PolicyDigest: "sha256:" + strings.Repeat("a", 64), Partition: domain.PerUser, PrincipalID: "principal"}
	var deny atomic.Bool
	var calls atomic.Int64
	auth := authority(func(_ context.Context, tx pgx.Tx, _ domain.Scope, actor, operation string) error {
		if tx == nil || actor != "principal" {
			return domain.ErrDenied
		}
		calls.Add(1)
		if deny.Load() {
			return domain.ErrDenied
		}
		if operation == "session.reset_shared" {
			return domain.ErrDenied
		}
		return nil
	})
	registry, e := New(runtime, auth)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = New(runtime, nil); e != domain.ErrInvalid {
		t.Fatal("default authorization")
	}
	var first domain.Selection
	save := func(id string) func(context.Context, pgx.Tx, domain.Selection) error {
		return func(ctx context.Context, tx pgx.Tx, v domain.Selection) error {
			first = v
			_, e := tx.Exec(ctx, `INSERT INTO worker_session_registry_test_intakes(id,session_id,generation) VALUES($1,$2,$3)`, id, v.SessionID, v.Generation)
			return e
		}
	}
	if e = registry.WithCurrent(ctx, s, "principal", save(s.AccountID+"_before")); e != nil || first.Generation != 1 {
		t.Fatal(first, e)
	}
	initial := first
	cmd := domain.Reset{CommandID: s.AccountID + "_reset", ActorID: "principal", Scope: s, ExpectedGeneration: 1}
	var wg sync.WaitGroup
	results := make(chan domain.Selection, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); v, e := registry.Reset(ctx, cmd); results <- v; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var reset domain.Selection
	for v := range results {
		if v.Generation != 2 || v.SessionID == initial.SessionID {
			t.Fatal(v)
		}
		reset = v
	}
	beforeReplay := calls.Load()
	deny.Store(true)
	replayed, e := registry.Reset(ctx, cmd)
	if e != nil || replayed != reset || calls.Load() != beforeReplay {
		t.Fatal("receipt replay reapplied reset", replayed, e)
	}
	deny.Store(false)
	changed := cmd
	changed.ActorID = "other"
	if _, e = registry.Reset(ctx, changed); e != domain.ErrDenied {
		t.Fatal("foreign actor replay", e)
	}
	changed = cmd
	changed.ExpectedGeneration = 2
	if _, e = registry.Reset(ctx, changed); e != domain.ErrConflict {
		t.Fatal("command collision", e)
	}
	// Two distinct commands race the same expected generation: only one advances.
	errs = make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := cmd
			c.CommandID = fmt.Sprintf("%s_cas%d", s.AccountID, i)
			c.ExpectedGeneration = 2
			_, e := registry.Reset(ctx, c)
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	passed, conflicts := 0, 0
	for e := range errs {
		if e == nil {
			passed++
		} else if errors.Is(e, domain.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if passed != 1 || conflicts != 1 {
		t.Fatal(passed, conflicts)
	}
	if e = registry.WithCurrent(ctx, s, "principal", save(s.AccountID+"_after")); e != nil || first.Generation != 3 {
		t.Fatal(first, e)
	}
	// Intake callback and generation choice roll back together on final recheck.
	deny.Store(false)
	if e = registry.WithCurrent(ctx, s, "principal", func(c context.Context, tx pgx.Tx, v domain.Selection) error {
		if e := save(s.AccountID+"_aborted")(c, tx, v); e != nil {
			return e
		}
		deny.Store(true)
		return nil
	}); e != domain.ErrDenied {
		t.Fatal("final authority recheck", e)
	}
	deny.Store(false)
	var n int
	if e = runtime.QueryRow(ctx, `SELECT count(*) FROM worker_session_registry_test_intakes WHERE id=$1`, s.AccountID+"_aborted").Scan(&n); e != nil || n != 0 {
		t.Fatal("aborted intake persisted", n, e)
	}
	// Audit failure must roll back registry CAS and the durable command receipt.
	if _, e = owner.Exec(ctx, `CREATE FUNCTION session_test_audit_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture'; END $$; CREATE TRIGGER session_test_audit_fail BEFORE INSERT ON worker_conversation_audit_outbox FOR EACH ROW EXECUTE FUNCTION session_test_audit_fail()`); e != nil {
		t.Fatal(e)
	}
	c := cmd
	c.CommandID = s.AccountID + "_audit"
	c.ExpectedGeneration = 3
	if _, e = registry.Reset(ctx, c); e == nil {
		t.Fatal("audit failure committed")
	}
	if _, e = owner.Exec(ctx, `DROP TRIGGER session_test_audit_fail ON worker_conversation_audit_outbox;DROP FUNCTION session_test_audit_fail()`); e != nil {
		t.Fatal(e)
	}
	if e = registry.WithCurrent(ctx, s, "principal", save(s.AccountID+"_auditcheck")); e != nil || first.Generation != 3 {
		t.Fatal("audit failure advanced generation", first, e)
	}
	shared := s
	shared.Partition = domain.Shared
	shared.PrincipalID = ""
	c.Scope = shared
	c.ExpectedGeneration = 1
	c.CommandID = s.AccountID + "_shared"
	if _, e = registry.Reset(ctx, c); e != domain.ErrDenied {
		t.Fatal("shared reset permission", e)
	}
	if e = runtime.QueryRow(ctx, `SELECT count(*) FROM worker_conversation_commands WHERE scope_key=$1`, reset.ScopeKey).Scan(&n); e != nil || n != 2 {
		t.Fatal("command count", n, e)
	}
	if e = runtime.QueryRow(ctx, `SELECT count(*) FROM worker_conversation_audit_outbox WHERE scope_key=$1`, reset.ScopeKey).Scan(&n); e != nil || n != 2 {
		t.Fatal("audit count", n, e)
	}

	if _, e = runtime.Exec(ctx, `UPDATE worker_conversation_registry SET generation=generation+1 WHERE tenant_id=$1 AND scope_key=$2`, s.TenantID, reset.ScopeKey); e == nil {
		t.Fatal("direct generation advance lacked command/audit")
	}

	// Hold the registry lock inside an intake callback while a reset is dispatched.
	// The callback row must retain generation 3; the later reset chooses 4.
	entered, release := make(chan struct{}), make(chan struct{})
	intakeDone := make(chan error, 1)
	go func() {
		intakeDone <- registry.WithCurrent(ctx, s, "principal", func(c context.Context, tx pgx.Tx, v domain.Selection) error {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, e := tx.Exec(c, `INSERT INTO worker_session_registry_test_intakes(id,session_id,generation) VALUES($1,$2,$3)`, s.AccountID+"_ordered", v.SessionID, v.Generation)
			return e
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	resetDone := make(chan error, 1)
	dispatched := make(chan struct{})
	go func() {
		close(dispatched)
		c := cmd
		c.CommandID = s.AccountID + "_ordered_reset"
		c.ExpectedGeneration = 3
		v, e := registry.Reset(ctx, c)
		if e == nil && v.Generation != 4 {
			e = domain.ErrConflict
		}
		resetDone <- e
	}()
	<-dispatched
	close(release)
	if e = <-intakeDone; e != nil {
		t.Fatal(e)
	}
	if e = <-resetDone; e != nil {
		t.Fatal(e)
	}
	var savedGeneration int64
	if e = runtime.QueryRow(ctx, `SELECT generation FROM worker_session_registry_test_intakes WHERE id=$1`, s.AccountID+"_ordered").Scan(&savedGeneration); e != nil || savedGeneration != 3 {
		t.Fatal("reset changed prior intake generation", savedGeneration, e)
	}
	reopened, e := New(runtime, auth)
	if e != nil {
		t.Fatal(e)
	}
	if e = reopened.WithCurrent(ctx, s, "principal", save(s.AccountID+"_reopened")); e != nil || first.Generation != 4 {
		t.Fatal("registry required process-local state", first, e)
	}
	t.Log("SESSION_REGISTRY=PASS real runtime role; generation selection; concurrent duplicate/reset CAS; immutable replay; permission/final-recheck/audit rollback")
}
