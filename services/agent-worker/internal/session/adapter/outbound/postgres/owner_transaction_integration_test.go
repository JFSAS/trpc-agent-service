package postgresadapter

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRegistryOwnerTransactionPostgres(t *testing.T) {
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
	owner, pool := open(mu), open(ru)
	if e := migrations.ApplyForRuntime(ctx, owner, "worker_runtime"); e != nil {
		t.Fatal(e)
	}
	if _, e := owner.Exec(ctx, `CREATE TABLE IF NOT EXISTS registry_owner_tx_fixture(id text PRIMARY KEY, phase text NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"commit", "outer_rollback", "apply_error", "final_denial"} {
		t.Run(mode, func(t *testing.T) {
			id := fmt.Sprintf("owner_%s_%d", mode, time.Now().UnixNano())
			s := domain.Scope{TenantID: "tenant", Provider: "telegram", AccountID: id, ConversationID: "42", ConversationKind: "private", BindingID: "binding", DeploymentRevisionID: "revision", PolicyID: "policy", PolicyRevision: 1, PolicyDigest: "sha256:" + strings.Repeat("a", 64), Partition: domain.PerUser, PrincipalID: "principal"}
			calls := 0
			auth := authority(func(c context.Context, tx pgx.Tx, _ domain.Scope, _, _ string) error {
				calls++
				if calls == 2 {
					var phase string
					if e := tx.QueryRow(c, `SELECT phase FROM registry_owner_tx_fixture WHERE id=$1`, id).Scan(&phase); e != nil || phase != "pending" {
						t.Fatal("source consumed before final check", phase, e)
					}
					if mode == "final_denial" {
						return domain.ErrDenied
					}
				}
				return nil
			})
			registry, e := New(pool, auth)
			if e != nil {
				t.Fatal(e)
			}
			tx, e := pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer rollback(tx)
			applyErr := errors.New("apply failed")
			e = registry.WithCurrentInTransaction(ctx, tx, s, "principal", func(c context.Context, tx pgx.Tx, v domain.Selection) error {
				if v.Generation != 1 {
					t.Fatal(v)
				}
				if _, err := tx.Exec(c, `INSERT INTO registry_owner_tx_fixture(id,phase) VALUES($1,'pending')`, id); err != nil {
					return err
				}
				if mode == "apply_error" {
					return applyErr
				}
				return nil
			})
			if mode == "apply_error" || mode == "final_denial" {
				if e == nil {
					t.Fatal("failed callback/check succeeded")
				}
				// Deliberately commit outer tx: failed savepoint must retain zero registry/
				// callback writes even when the owner mishandles the returned error.
				if e = tx.Commit(ctx); e != nil {
					t.Fatal(e)
				}
			} else {
				if e != nil || calls != 2 {
					t.Fatal(e, calls)
				}
				var n int
				if e = pool.QueryRow(ctx, `SELECT count(*) FROM registry_owner_tx_fixture WHERE id=$1`, id).Scan(&n); e != nil || n != 0 {
					t.Fatal("registry helper committed owner tx", n, e)
				}
				// Releasing the savepoint must not release the registry lock. A
				// competing reset cannot advance until the outer owner finishes.
				short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
				_, resetErr := registry.Reset(short, domain.Reset{CommandID: id + "_compete", ActorID: "principal", Scope: s, ExpectedGeneration: 1})
				timedOut := errors.Is(short.Err(), context.DeadlineExceeded)
				stop()
				if resetErr == nil || !timedOut {
					t.Fatal("registry lock escaped outer transaction", resetErr, timedOut)
				}
				if _, e = tx.Exec(ctx, `UPDATE registry_owner_tx_fixture SET phase='consumed' WHERE id=$1`, id); e != nil {
					t.Fatal(e)
				}
				if mode == "commit" {
					e = tx.Commit(ctx)
				} else {
					e = tx.Rollback(ctx)
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			var n int
			if e = pool.QueryRow(ctx, `SELECT count(*) FROM registry_owner_tx_fixture WHERE id=$1`, id).Scan(&n); e != nil {
				t.Fatal(e)
			}
			want := 0
			if mode == "commit" {
				want = 1
			}
			if n != want {
				t.Fatal("callback atomicity", n, want)
			}
			key, _ := s.Key()
			if e = pool.QueryRow(ctx, `SELECT count(*) FROM worker_conversation_registry WHERE tenant_id=$1 AND scope_key=$2`, s.TenantID, key).Scan(&n); e != nil || n != want {
				t.Fatal("registry atomicity", n, want, e)
			}
		})
	}
	t.Log("REGISTRY_OWNER_TX=PASS outer commit/rollback; source retained for final proof; savepoint prevents failed callback or final denial leaking into outer commit")
}
