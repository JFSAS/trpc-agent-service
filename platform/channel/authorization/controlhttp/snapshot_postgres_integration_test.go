package controlhttp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	authorizationpostgres "github.com/liuzengh/trpc-agent-service/platform/channel/authorization/postgres"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/migrations"
)

func TestAuthorizationReaderMTLSIntoAtomicPostgresSnapshot(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("GATEWAY_TEST_DATABASE_URL requires disposable PostgreSQL")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	var nonce [8]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		t.Fatal(e)
	}
	name := "snapshot_reader_" + hex.EncodeToString(nonce[:])
	schema := pgx.Identifier{name}.Sanitize()
	if _, e = admin.Exec(ctx, `CREATE SCHEMA `+schema); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if _, e := admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`); e != nil {
			t.Error(e)
		}
	}()
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = name
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	if e = migrations.Apply(ctx, pool); e != nil {
		t.Fatal(e)
	}
	store, e := authorizationpostgres.New(pool, "pool", testEpoch, authorizationpostgres.Gateway)
	if e != nil {
		t.Fatal(e)
	}
	f := authorizationFixtureFor(t, 257)
	var fail atomic.Bool
	var pages atomic.Int64
	cl, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":page") {
			index := pages.Add(1)
			if index == 2 {
				var n int
				if e := pool.QueryRow(r.Context(), `SELECT count(*) FROM gateway_authorization_staging`).Scan(&n); e != nil || n != 0 {
					t.Error("in-flight staging exposed", n, e)
				}
			}
			if fail.Load() && index == 2 {
				w.WriteHeader(409)
				return
			}
		}
		f.serve(t, w, r)
	}))
	if e = store.Refresh(ctx, f.target(), cl); e != nil {
		t.Fatal("real reader-to-store", e)
	}
	var generation int64
	var count, revoked, staged int
	var before, timeAfter time.Time
	read := func() {
		t.Helper()
		if e := pool.QueryRow(ctx, `SELECT generation,fresh_until,(SELECT count(*) FROM gateway_authorization_principals),(SELECT count(*) FROM gateway_authorization_principals WHERE state='REVOKED'),(SELECT count(*) FROM gateway_authorization_staging) FROM gateway_authorization_snapshots`).Scan(&generation, &timeAfter, &count, &revoked, &staged); e != nil {
			t.Fatal(e)
		}
	}
	read()
	if generation != 17 || count != 257 || revoked != 128 || staged != 0 {
		t.Fatal(generation, count, revoked, staged)
	}
	before = timeAfter
	pages.Store(0)
	fail.Store(true)
	if e = store.Refresh(ctx, f.target(), cl); e != ErrSnapshotChanged {
		t.Fatal("page conflict", e)
	}
	read()
	if count != 257 || revoked != 128 || staged != 0 || !timeAfter.Equal(before) {
		t.Fatal("failed read mutated active snapshot or renewed deadline")
	}
	t.Log("GATEWAY_CURRENT_SNAPSHOT=PASS real mTLS client + exact policy + 3-page proof + atomic PG install; 128 revoked retained; HTTP409 rolls back stage without renewing prior deadline")
}
