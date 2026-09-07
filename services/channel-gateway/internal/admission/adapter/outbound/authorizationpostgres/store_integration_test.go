package authorizationpostgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/migrations"
)

const epoch = "11111111-1111-4111-8111-111111111111"

type readerFunc func(context.Context, domain.AuthorizationTarget, func(context.Context, wire.AuthorizationSnapshotPage) error) (domain.AuthorizationRead, error)

func (f readerFunc) ReadAuthorization(c context.Context, t domain.AuthorizationTarget, s func(context.Context, wire.AuthorizationSnapshotPage) error) (domain.AuthorizationRead, error) {
	return f(c, t, s)
}
func setup(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("GATEWAY_TEST_DATABASE_URL requires disposable PostgreSQL")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(admin.Close)
	var b [8]byte
	if _, e = rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	name := "auth_snapshot_" + hex.EncodeToString(b[:])
	schema := pgx.Identifier{name}.Sanitize()
	role := pgx.Identifier{name + "_runtime"}.Sanitize()
	var database string
	if e = admin.QueryRow(ctx, `SELECT current_database()`).Scan(&database); e != nil {
		t.Fatal(e)
	}
	if _, e = admin.Exec(ctx, `REVOKE TEMP ON DATABASE `+pgx.Identifier{database}.Sanitize()+` FROM PUBLIC; CREATE ROLE `+role+` NOLOGIN; CREATE SCHEMA `+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, e := admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE; DROP ROLE `+role)
		if e != nil {
			t.Error(e)
		}
	})
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = name
	owner, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(owner.Close)
	if e = migrations.Apply(ctx, owner); e != nil {
		t.Fatal(e)
	}
	if _, e = owner.Exec(ctx, `GRANT USAGE ON SCHEMA `+schema+` TO `+role+`; GRANT SELECT,INSERT,UPDATE,DELETE ON gateway_authorization_snapshots,gateway_authorization_principals,gateway_authorization_staging TO `+role); e != nil {
		t.Fatal(e)
	}
	rc := cfg.Copy()
	rc.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { _, e := c.Exec(ctx, `SET ROLE `+role); return e }
	runtime, e := pgxpool.NewWithConfig(ctx, rc)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(runtime.Close)
	var temp, create bool
	if e = runtime.QueryRow(ctx, `SELECT has_database_privilege(current_user,current_database(),'TEMP'),has_database_privilege(current_user,current_database(),'CREATE')`).Scan(&temp, &create); e != nil || temp || create {
		t.Fatal("runtime unexpectedly has TEMP", temp, e)
	}
	s, e := New(runtime, "pool", epoch)
	if e != nil {
		t.Fatal(e)
	}
	return s, owner
}
func fixture(t *testing.T, generation int64, n int) (domain.AuthorizationRead, []wire.AuthorizationSnapshotPage) {
	t.Helper()
	id := wire.AuthorizationSnapshotIdentity{SchemaVersion: 1, ScopeID: "pool", SourceEpoch: epoch, TenantID: "tenant", AccountID: "account", Provider: "wecom", Generation: generation}
	p := wire.AccessPolicyDocument{SchemaVersion: 1, TenantID: "tenant", AccountID: "account", Provider: "wecom", PolicyID: "policy", Revision: 1, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), Body: wire.AccessPolicyBody{AccessMode: "DENY_ALL", AllowedPrincipalIDs: []string{}, AllowedConversationIDs: []string{}, AllowedOperations: []string{}, AuthorizationMaxAgeMS: 30000}}
	sign(t, &p)
	d := wire.NewPrincipalSetDigest()
	records := make([]wire.AuthorizationPrincipal, 0, n)
	for i := 0; i < n; i++ {
		v := wire.AuthorizationPrincipal{PrincipalID: fmt.Sprintf("p_%04d", i), ExternalUserID: fmt.Sprintf("u_%d", i), State: "ACTIVE", Revision: 1}
		if generation > 1 && i == 0 {
			v.State = "REVOKED"
			v.Revision = generation
		}
		if d.Add(v) != nil {
			t.Fatal("digest")
		}
		records = append(records, v)
	}
	count, root := d.Result()
	m := wire.AuthorizationSnapshotManifest{AuthorizationSnapshotIdentity: id, AccountRevision: 1, AccountEnabled: true, Policy: wire.PolicyReference{ID: p.PolicyID, Revision: 1, Digest: p.Digest}, CapturedAt: time.Now().UTC(), AuthorizationMaxAgeMS: 30000, PrincipalCount: count, PrincipalDigest: root}
	pages := []wire.AuthorizationSnapshotPage{}
	cursor := ""
	for start := 0; ; start += 128 {
		end := min(start+128, n)
		page := wire.AuthorizationSnapshotPage{AuthorizationSnapshotIdentity: id, AfterPrincipalID: cursor, Principals: append([]wire.AuthorizationPrincipal{}, records[start:end]...), Complete: end == n}
		if !page.Complete {
			page.NextPrincipalID = records[end-1].PrincipalID
		}
		pages = append(pages, page)
		if page.Complete {
			break
		}
		cursor = page.NextPrincipalID
	}
	return domain.AuthorizationRead{Manifest: m, Policy: p}, pages
}
func sign(t *testing.T, p *wire.AccessPolicyDocument) {
	t.Helper()
	p.Digest = ""
	raw, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	raw, e = jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(raw)
	p.Digest = "sha256:" + hex.EncodeToString(h[:])
}
func reader(result domain.AuthorizationRead, pages []wire.AuthorizationSnapshotPage) domain.AuthorizationReader {
	return readerFunc(func(ctx context.Context, _ domain.AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (domain.AuthorizationRead, error) {
		for _, p := range pages {
			if e := stage(ctx, p); e != nil {
				return domain.AuthorizationRead{}, e
			}
		}
		return result, nil
	})
}
func target() domain.AuthorizationTarget {
	return domain.AuthorizationTarget{TenantID: "tenant", AccountID: "account", Provider: "wecom"}
}
func installed(t *testing.T, pool *pgxpool.Pool) (int64, int, string) {
	t.Helper()
	var g int64
	var n int
	var b string
	e := pool.QueryRow(context.Background(), `SELECT generation,(SELECT count(*) FROM gateway_authorization_principals),blocked_reason FROM gateway_authorization_snapshots WHERE scope_id='pool' AND account_id='account'`).Scan(&g, &n, &b)
	if e != nil {
		t.Fatal(e)
	}
	var staged int
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM gateway_authorization_staging`).Scan(&staged); e != nil || staged != 0 {
		t.Fatal("committed staging rows", staged, e)
	}
	return g, n, b
}
func TestCurrentSnapshotAtomicInstallWithoutDDLPrivilege(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	first, one := fixture(t, 1, 1)
	if e := s.Refresh(ctx, target(), reader(first, one)); e != nil {
		t.Fatal(e)
	}
	next, pages := fixture(t, 2, 257)
	next.Policy = first.Policy
	next.Manifest.Policy = first.Manifest.Policy
	var dbBefore time.Time
	if e := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbBefore); e != nil {
		t.Fatal(e)
	}
	e := s.Refresh(ctx, target(), readerFunc(func(ctx context.Context, _ domain.AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (domain.AuthorizationRead, error) {
		for _, page := range pages {
			if e := stage(ctx, page); e != nil {
				return domain.AuthorizationRead{}, e
			}
			g, n, b := installed(t, pool)
			if g != 1 || n != 1 || b != "" {
				t.Fatal("partial set exposed", g, n, b)
			}
		}
		next.StartedAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
		next.ExpiresAt = next.StartedAt.Add(time.Hour)
		return next, nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	g, n, b := installed(t, pool)
	if g != 2 || n != 257 || b != "" {
		t.Fatal(g, n, b)
	}
	var state string
	var start, expires time.Time
	if e = pool.QueryRow(ctx, `SELECT state FROM gateway_authorization_principals WHERE principal_id='p_0000'`).Scan(&state); e != nil || state != "REVOKED" {
		t.Fatal(state, e)
	}
	if e = pool.QueryRow(ctx, `SELECT read_started_at,fresh_until FROM gateway_authorization_snapshots`).Scan(&start, &expires); e != nil || start.Before(dbBefore) || expires.Sub(start) != 30*time.Second || expires.Year() == 2099 {
		t.Fatal("DB deadline anchor", e)
	}
	// Same content and generation may be refreshed by a genuinely new read.
	if e = s.Refresh(ctx, target(), reader(next, pages)); e != nil {
		t.Fatal("same generation refresh", e)
	}
	for _, sql := range []string{`UPDATE gateway_authorization_snapshots SET generation=1`, `UPDATE gateway_authorization_snapshots SET fresh_until=read_started_at+interval '31 seconds'`, `DELETE FROM gateway_authorization_snapshots`} {
		if _, e = pool.Exec(ctx, sql); e == nil {
			t.Fatal("SQL fence bypass")
		}
	}
	t.Log("CURRENT_INSTALL=PASS 257 principals atomically replaced; REVOKED preserved; DB-before-request expiry; no TEMP/CREATE privilege")
}
func TestCurrentSnapshotFailuresKeepPriorSet(t *testing.T) {
	for _, mode := range []string{"partial", "root", "stage_ignored", "expired", "cancel", "commit", "staging_corrupt", "installed_corrupt"} {
		t.Run(mode, func(t *testing.T) {
			s, pool := setup(t)
			ctx := context.Background()
			base, pages := fixture(t, 1, 1)
			if e := s.Refresh(ctx, target(), reader(base, pages)); e != nil {
				t.Fatal(e)
			}
			next, many := fixture(t, 2, 129)
			next.Policy = base.Policy
			next.Manifest.Policy = base.Manifest.Policy
			if mode == "commit" {
				_, e := pool.Exec(ctx, `CREATE FUNCTION reject_snapshot_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.generation=2 THEN RAISE EXCEPTION 'snapshot commit fixture'; END IF; RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER reject_snapshot_commit AFTER INSERT OR UPDATE ON gateway_authorization_snapshots DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_snapshot_commit()`)
				if e != nil {
					t.Fatal(e)
				}
			}
			if mode == "staging_corrupt" || mode == "installed_corrupt" {
				table := "gateway_authorization_staging"
				if mode == "installed_corrupt" {
					table = "gateway_authorization_principals"
				}
				_, e := pool.Exec(ctx, `CREATE FUNCTION corrupt_snapshot_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.state:='ACTIVE'; RETURN NEW; END $$; CREATE TRIGGER corrupt_snapshot_fixture BEFORE INSERT ON `+table+` FOR EACH ROW EXECUTE FUNCTION corrupt_snapshot_fixture()`)
				if e != nil {
					t.Fatal(e)
				}
			}
			call, cancel := context.WithCancel(ctx)
			defer cancel()
			e := s.Refresh(call, target(), readerFunc(func(c context.Context, _ domain.AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (domain.AuthorizationRead, error) {
				for i, p := range many {
					if mode == "stage_ignored" {
						p.TenantID = "other"
					}
					err := stage(c, p)
					if err != nil && mode != "stage_ignored" {
						return domain.AuthorizationRead{}, err
					}
					if i == 0 && mode == "partial" {
						return domain.AuthorizationRead{}, errors.New("fixture interrupted")
					}
				}
				switch mode {
				case "root":
					next.Manifest.PrincipalDigest = "sha256:" + fmt.Sprintf("%064d", 1)
				case "expired":
					next.Policy.Revision++
					next.Manifest.Policy.Revision = next.Policy.Revision
					next.Policy.Body.AuthorizationMaxAgeMS = 1
					sign(t, &next.Policy)
					next.Manifest.Policy.Digest = next.Policy.Digest
					next.Manifest.AuthorizationMaxAgeMS = 1
					time.Sleep(5 * time.Millisecond)
				case "cancel":
					cancel()
				}
				return next, nil
			}))
			if e == nil {
				t.Fatal("failed read installed")
			}
			g, n, b := installed(t, pool)
			if g != 1 || n != 1 || b != "" {
				t.Fatal("prior state modified", g, n, b)
			}
		})
	}
}
func TestCurrentSnapshotConcurrentOlderReadDoesNotReplaceOrBlock(t *testing.T) {
	s, pool := setup(t)
	base, p := fixture(t, 1, 1)
	if e := s.Refresh(context.Background(), target(), reader(base, p)); e != nil {
		t.Fatal(e)
	}
	older, op := fixture(t, 2, 2)
	newer, np := fixture(t, 3, 3)
	older.Policy = base.Policy
	older.Manifest.Policy = base.Manifest.Policy
	newer.Policy = base.Policy
	newer.Manifest.Policy = base.Manifest.Policy
	entered, resume := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Refresh(context.Background(), target(), readerFunc(func(ctx context.Context, _ domain.AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (domain.AuthorizationRead, error) {
			if e := stage(ctx, op[0]); e != nil {
				return domain.AuthorizationRead{}, e
			}
			close(entered)
			select {
			case <-resume:
			case <-ctx.Done():
				return domain.AuthorizationRead{}, ctx.Err()
			}
			return older, nil
		}))
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reader not started")
	}
	if e := s.Refresh(context.Background(), target(), reader(newer, np)); e != nil {
		close(resume)
		t.Fatal(e)
	}
	close(resume)
	if e := <-done; !errors.Is(e, ErrSuperseded) {
		t.Fatal(e)
	}
	g, n, b := installed(t, pool)
	if g != 3 || n != 3 || b != "" {
		t.Fatal(g, n, b)
	}
}
func TestCurrentSnapshotRegressionAndConflictPersistentlyBlock(t *testing.T) {
	for _, mode := range []string{"regression", "conflict", "epoch", "policy_reinterpretation"} {
		t.Run(mode, func(t *testing.T) {
			s, pool := setup(t)
			base, p := fixture(t, 3, 1)
			if e := s.Refresh(context.Background(), target(), reader(base, p)); e != nil {
				t.Fatal(e)
			}
			var oldDeadline time.Time
			if e := pool.QueryRow(context.Background(), `SELECT fresh_until FROM gateway_authorization_snapshots`).Scan(&oldDeadline); e != nil {
				t.Fatal(e)
			}
			next, pages := fixture(t, 3, 2)
			next.Policy = base.Policy
			next.Manifest.Policy = base.Manifest.Policy
			if mode == "policy_reinterpretation" {
				next.Manifest.Generation = 4
				for i := range pages {
					pages[i].Generation = 4
				}
				next.Policy.PublishedBy = "changed-owner"
				sign(t, &next.Policy)
				next.Manifest.Policy.Digest = next.Policy.Digest
				raw, _ := json.Marshal(next.Policy)
				if _, err := pool.Exec(context.Background(), `UPDATE gateway_authorization_snapshots SET generation=4,policy_digest=$1,policy_jsonb=$2`, next.Policy.Digest, raw); err == nil {
					t.Fatal("SQL policy reinterpretation accepted")
				}

			}
			if mode == "regression" {
				next.Manifest.Generation = 2
				for i := range pages {
					pages[i].Generation = 2
				}
			}
			if mode == "epoch" {
				s.epoch = "22222222-2222-4222-8222-222222222222"
				next.Manifest.SourceEpoch = s.epoch
				for i := range pages {
					pages[i].SourceEpoch = s.epoch
				}
			}
			if e := s.Refresh(context.Background(), target(), reader(next, pages)); !errors.Is(e, ErrBlocked) {
				t.Fatal(e)
			}
			g, n, b := installed(t, pool)
			if g != 3 || n != 1 || b == "" {
				t.Fatal(g, n, b)
			}
			var deadline time.Time
			if e := pool.QueryRow(context.Background(), `SELECT fresh_until FROM gateway_authorization_snapshots`).Scan(&deadline); e != nil || !deadline.Equal(oldDeadline) {
				t.Fatal("blocked refresh extended deadline", e)
			}
			if mode == "policy_reinterpretation" && b != "SNAPSHOT_CONFLICT" {
				t.Fatal("wrong block reason", b)
			}
			again, e := New(s.pool, "pool", s.epoch)
			if e != nil {
				t.Fatal(e)
			}
			if e = again.Refresh(context.Background(), target(), reader(next, pages)); !errors.Is(e, ErrBlocked) {
				t.Fatal("restart cleared block", e)
			}
			if _, e = pool.Exec(context.Background(), `UPDATE gateway_authorization_snapshots SET blocked_reason=''`); e == nil {
				t.Fatal("SQL unblock accepted")
			}
		})
	}
}
