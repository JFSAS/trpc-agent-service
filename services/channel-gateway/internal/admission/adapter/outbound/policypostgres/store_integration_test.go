package policypostgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/migrations"
)

const testEpoch = "11111111-1111-4111-8111-111111111111"

func setup(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to run real PostgreSQL acceptance tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{"gateway_admission_" + hex.EncodeToString(random[:])}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 32
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store, err := New(pool, "pool", testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.BindSource(ctx, time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return store, pool
}
func fixture(t *testing.T, rev int64) (wire.AccessPolicyEvent, wire.AccessPolicyDocument) {
	t.Helper()
	p := wire.AccessPolicyDocument{SchemaVersion: 1, TenantID: "tenant", AccountID: "account", Provider: "wecom", PolicyID: "policy", Revision: rev, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), Body: wire.AccessPolicyBody{AccessMode: "DENY_ALL", AllowedPrincipalIDs: []string{}, AllowedConversationIDs: []string{}, AllowedOperations: []string{}, AuthorizationMaxAgeMS: 30000}}
	return signed(t, p)
}
func signed(t *testing.T, p wire.AccessPolicyDocument) (wire.AccessPolicyEvent, wire.AccessPolicyDocument) {
	t.Helper()
	p.Digest = ""
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = jcs.Transform(raw)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	p.Digest = "sha256:" + hex.EncodeToString(h[:])
	e := wire.AccessPolicyEvent{SchemaVersion: 1, EventID: fmt.Sprintf("event-%d", p.Revision), EventType: wire.AccessPolicyPublishedEvent, ScopeID: "pool", SourceEpoch: testEpoch, TenantID: p.TenantID, AccountID: p.AccountID, Provider: p.Provider, PolicyID: p.PolicyID, PolicyRevision: p.Revision, PolicyDigest: p.Digest, OccurredAt: p.PublishedAt}
	return e, p
}
func reference(p wire.AccessPolicyDocument) wire.PolicyReference {
	return wire.PolicyReference{ID: p.PolicyID, Revision: p.Revision, Digest: p.Digest}
}
func TestProjectionOutOfOrderGapFillReplayAndRestart(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	for _, tc := range []struct{ rev, observed, contiguous int64 }{{3, 3, 0}, {1, 3, 1}, {5, 5, 1}, {2, 5, 3}, {4, 5, 5}, {1, 5, 5}, {5, 5, 5}} {
		e, p := fixture(t, tc.rev)
		state, err := s.Apply(ctx, e, p)
		if err != nil || state.ObservedRevision != tc.observed || state.ContiguousRevision != tc.contiguous || state.HasGap() != (tc.observed != tc.contiguous) {
			t.Fatal(tc, state, err)
		}
	}
	restarted, err := New(pool, "pool", testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	_, p := fixture(t, 1)
	got, state, err := restarted.ReadExact(ctx, p.TenantID, p.AccountID, reference(p))
	if err != nil || !reflect.DeepEqual(got, p) || state.ObservedRevision != 5 || state.ContiguousRevision != 5 {
		t.Fatal(got, state, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM gateway_policy_projection_documents`).Scan(&count); err != nil || count != 5 {
		t.Fatal(count, err)
	}
	// Another tenant, source epoch or scope gets no historical document.
	if _, _, err = s.ReadExact(ctx, "other", p.AccountID, reference(p)); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, cfg := range [][2]string{{"other", testEpoch}, {"pool", "22222222-2222-4222-8222-222222222222"}} {
		other, err := New(pool, cfg[0], cfg[1])
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = other.ReadExact(ctx, p.TenantID, p.AccountID, reference(p)); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrBlocked) {
			t.Fatal(err)
		}
	}
}
func TestProjectionConcurrentReverseArrivalHasSingleHistory(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	// Prebuild fixtures on the test goroutine; duplicate publishers race first insert.
	es := make([]wire.AccessPolicyEvent, 12)
	ps := make([]wire.AccessPolicyDocument, 12)
	for i := range es {
		es[i], ps[i] = fixture(t, int64(i+1))
	}
	for i := 23; i >= 0; i-- {
		i := i % 12
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Apply(ctx, es[i], ps[i]); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	_, state, err := s.ReadExact(ctx, "tenant", "account", reference(ps[11]))
	if err != nil || state.ObservedRevision != 12 || state.ContiguousRevision != 12 {
		t.Fatal(state, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM gateway_policy_projection_documents`).Scan(&count); err != nil || count != 12 {
		t.Fatal(count, err)
	}
}
func TestProjectionConflictIsDurableAndNotClearedByReplay(t *testing.T) {
	for _, kind := range []string{"revision", "tenant", "provider", "policy"} {
		t.Run(kind, func(t *testing.T) {
			s, pool := setup(t)
			ctx := context.Background()
			e, p := fixture(t, 1)
			if _, err := s.Apply(ctx, e, p); err != nil {
				t.Fatal(err)
			}
			altered := p
			switch kind {
			case "revision":
				altered.Body.AuthorizationMaxAgeMS = 1
			case "tenant":
				altered.TenantID = "other"
			case "provider":
				altered.Provider = "telegram"
			case "policy":
				altered.PolicyID = "other"
			}
			bad, altered := signed(t, altered)
			state, err := s.Apply(ctx, bad, altered)
			if !errors.Is(err, ErrBlocked) || state != (domain.PolicyContinuity{}) {
				t.Fatal(state, err)
			}
			restarted, _ := New(pool, "pool", testEpoch)
			if _, err = restarted.Apply(ctx, e, p); !errors.Is(err, ErrBlocked) {
				t.Fatal(err)
			}
			if _, _, err = restarted.ReadExact(ctx, "tenant", "account", reference(p)); !errors.Is(err, ErrBlocked) {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `UPDATE gateway_policy_projection_heads SET blocked_reason=''`); err == nil {
				t.Fatal("cleared permanent conflict")
			}
			var reason string
			pool.QueryRow(ctx, `SELECT blocked_reason FROM gateway_policy_projection_heads`).Scan(&reason)
			want := "IDENTITY_CONFLICT"
			if kind == "revision" {
				want = "REVISION_CONFLICT"
			}
			if reason != want {
				t.Fatal(reason)
			}
		})
	}
}
func TestProjectionCommitFailureAndCancellationLeaveNoPartialState(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	e, p := fixture(t, 1)
	_, err := pool.Exec(ctx, `CREATE FUNCTION test_policy_commit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic commit rejection'; END; $$; CREATE CONSTRAINT TRIGGER test_policy_commit_failure AFTER INSERT ON gateway_policy_projection_documents DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION test_policy_commit_failure();`)
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.Apply(ctx, e, p)
	if !errors.Is(err, ErrUnavailable) || state != (domain.PolicyContinuity{}) {
		t.Fatal(state, err)
	}
	var count int
	pool.QueryRow(ctx, `SELECT count(*) FROM gateway_policy_projection_heads`).Scan(&count)
	if count != 0 {
		t.Fatal("head escaped failed commit", count)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER test_policy_commit_failure ON gateway_policy_projection_documents; DROP FUNCTION test_policy_commit_failure()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, e, p); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT 1 FROM gateway_policy_projection_heads FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	timed, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	next, doc := fixture(t, 2)
	state, err = s.Apply(timed, next, doc)
	if err == nil || state != (domain.PolicyContinuity{}) {
		t.Fatal(state, err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	_, state, err = s.ReadExact(ctx, "tenant", "account", reference(p))
	if err != nil || state.ObservedRevision != 1 || state.ContiguousRevision != 1 {
		t.Fatal(state, err)
	}
}
func TestProjectionRejectsInvalidInputAndDatabaseMutation(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	e, p := fixture(t, 1)
	for _, field := range []string{"scope", "epoch", "digest", "type"} {
		bad := e
		switch field {
		case "scope":
			bad.ScopeID = "other"
		case "epoch":
			bad.SourceEpoch = "22222222-2222-4222-8222-222222222222"
		case "digest":
			bad.PolicyDigest = "invalid"
		case "type":
			bad.EventType = wire.AccessPolicyAuditEvent
		}
		if _, err := s.Apply(ctx, bad, p); !errors.Is(err, ErrInvalid) {
			t.Fatal(field, err)
		}
	}
	if _, err := s.Apply(ctx, e, p); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`UPDATE gateway_policy_projection_documents SET digest=digest`, `DELETE FROM gateway_policy_projection_documents`, `UPDATE gateway_policy_projection_heads SET observed_revision=0,contiguous_revision=0`, `UPDATE gateway_policy_projection_heads SET tenant_id='other'`} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Fatal("mutation accepted", sql)
		}
	}
}

func TestProjectionRejectsCorruptPersistedReplayWithoutDisablingTriggers(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	e, p := fixture(t, 1)
	// Simulate an invalid first writer through INSERT; immutable UPDATE/DELETE
	// triggers stay enabled. Application must not trust an indexed digest alone.
	_, err := pool.Exec(ctx, `INSERT INTO gateway_policy_projection_heads(scope_id,source_epoch,tenant_id,account_id,provider,policy_id) VALUES('pool',$1,'tenant','account','wecom','policy')`, testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	broken := p
	broken.Body.AuthorizationMaxAgeMS = 1
	raw, _ := json.Marshal(broken)
	_, err = pool.Exec(ctx, `INSERT INTO gateway_policy_projection_documents(scope_id,source_epoch,tenant_id,account_id,provider,policy_id,revision,digest,document_jsonb) VALUES('pool',$1,'tenant','account','wecom','policy',1,$2,$3)`, testEpoch, p.Digest, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ReadExact(ctx, "tenant", "account", reference(p)); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, e, p); !errors.Is(err, ErrBlocked) {
		t.Fatal("replay trusted corrupt body", err)
	}
	var reason string
	if err = pool.QueryRow(ctx, `SELECT blocked_reason FROM gateway_policy_projection_heads`).Scan(&reason); err != nil || reason != "REVISION_CONFLICT" {
		t.Fatal(reason, err)
	}
	// Startup migration is idempotent and does not clear quarantine or overwrite history.
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, e, p); !errors.Is(err, ErrBlocked) {
		t.Fatal("migration cleared quarantine", err)
	}
}
