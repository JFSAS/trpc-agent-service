package postgresadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/migrations"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func quotaDocument(t *testing.T, tenant string, concurrent, rate int64) wire.PolicyDefinitionDocument {
	t.Helper()
	d := wire.PolicyDefinitionDocument{SchemaVersion: 1, TenantID: tenant, PolicyID: "quota", Kind: "quota", Revision: 1, Definition: wire.PolicyDefinition{Enabled: true, Quota: &wire.QuotaDefinition{MaxConcurrentRuns: concurrent, MaxRunsPerMinute: rate}}, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}
	signQuota(t, &d)
	return d
}
func signQuota(t *testing.T, d *wire.PolicyDefinitionDocument) {
	t.Helper()
	d.Digest = ""
	b, e := json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	b, e = jcs.Transform(b)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(b)
	d.Digest = "sha256:" + hex.EncodeToString(sum[:])
}
func TestSharedRunQuotaPostgres(t *testing.T) {
	mu, ru := os.Getenv("WORKER_TEST_MIGRATION_URL"), os.Getenv("WORKER_TEST_RUNTIME_URL")
	if mu == "" || ru == "" {
		t.Skip("requires owned Worker PostgreSQL roles")
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
	qa, e := NewRunQuota(a, 1000)
	if e != nil {
		t.Fatal(e)
	}
	qb, e := NewRunQuota(b, 1000)
	if e != nil {
		t.Fatal(e)
	}
	prefix := fmt.Sprintf("quota%d", time.Now().UnixNano())
	request := func(tenant, run string) domain.RunQuotaRequest {
		return domain.RunQuotaRequest{TenantID: tenant, RunID: prefix + run, InputDigest: "sha256:" + strings.Repeat("a", 64)}
	}
	doc := quotaDocument(t, prefix, 1, 10)
	type answer struct {
		value domain.RunQuotaReservation
		err   error
	}
	out := make(chan answer, 8)
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			q := qa
			if n%2 == 1 {
				q = qb
			}
			v, e := q.ReserveRunQuota(ctx, request(prefix, fmt.Sprint(n)), doc)
			out <- answer{v, e}
		}(n)
	}
	wg.Wait()
	close(out)
	wins, denied := 0, 0
	var winner domain.RunQuotaReservation
	for v := range out {
		if v.err == nil {
			wins++
			winner = v.value
		} else if errors.Is(v.err, domain.ErrQuota) {
			denied++
		} else {
			t.Fatal(v.err)
		}
	}
	if wins != 1 || denied != 7 {
		t.Fatal("multi-instance quota multiplied", wins, denied)
	}
	retry := domain.RunQuotaRequest{TenantID: prefix, RunID: winner.RunID, InputDigest: "sha256:" + strings.Repeat("a", 64)}
	tiny, e := NewRunQuota(b, 1)
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 8; n++ {
		v, e := tiny.ReserveRunQuota(ctx, retry, doc)
		if e != nil || v.Fingerprint != winner.Fingerprint || !v.ReservedAt.Equal(winner.ReservedAt) {
			t.Fatal("retry recounted/refreshed reservation", v, e)
		}
	}
	changed := retry
	changed.InputDigest = "sha256:" + strings.Repeat("b", 64)
	if _, e = qa.ReserveRunQuota(ctx, changed, doc); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("identity mutation", e)
	}
	higherRevision := doc
	higherRevision.Revision++
	signQuota(t, &higherRevision)
	if _, e = qa.ReserveRunQuota(ctx, retry, higherRevision); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("reservation reinterpreted policy", e)
	}
	otherPolicy := doc
	otherPolicy.PolicyID = "quota-new"
	signQuota(t, &otherPolicy)
	if _, e = qa.ReserveRunQuota(ctx, request(prefix, "new-policy"), otherPolicy); !errors.Is(e, domain.ErrQuota) {
		t.Fatal("policy ID reset tenant quota", e)
	}
	otherTenant := prefix + "other"
	otherDoc := quotaDocument(t, otherTenant, 10, 10)
	if _, e = qa.ReserveRunQuota(ctx, request(otherTenant, "other"), otherDoc); e != nil {
		t.Fatal("tenant isolation", e)
	}
	if _, e = tiny.ReserveRunQuota(ctx, request(otherTenant, "storage"), otherDoc); !errors.Is(e, domain.ErrCapacity) {
		t.Fatal("storage bound", e)
	}
	foreign := retry
	foreign.TenantID = otherTenant
	if _, e = qa.ReserveRunQuota(ctx, foreign, otherDoc); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("cross-tenant RunID reuse", e)
	}
	for _, mode := range []string{"zero", "disabled", "digest", "tenant"} {
		bad := quotaDocument(t, prefix+mode, 2, 2)
		r := request(prefix+mode, mode)
		want := domain.ErrQuota
		switch mode {
		case "zero":
			bad.Definition.Quota.MaxConcurrentRuns = 0
			signQuota(t, &bad)
		case "disabled":
			bad.Definition.Enabled = false
			signQuota(t, &bad)
		case "digest":
			bad.Digest = "sha256:" + strings.Repeat("0", 64)
			want = domain.ErrInvalid
		case "tenant":
			r.TenantID = "foreign"
			want = domain.ErrInvalid
		}
		if _, e = qa.ReserveRunQuota(ctx, r, bad); !errors.Is(e, want) {
			t.Fatal(mode, e, want)
		}
	}
	rateTenant := prefix + "rate"
	rateDoc := quotaDocument(t, rateTenant, 10, 1)
	if _, e = qa.ReserveRunQuota(ctx, request(rateTenant, "rate1"), rateDoc); e != nil {
		t.Fatal(e)
	}
	if _, e = qb.ReserveRunQuota(ctx, request(rateTenant, "rate2"), rateDoc); !errors.Is(e, domain.ErrQuota) {
		t.Fatal("sliding minute quota", e)
	}
	// Explicit historical SQL fixture, not a claim that this test waited a minute.
	historical := func(tenant string, concurrency int64) {
		d := quotaDocument(t, tenant, concurrency, 1)
		r := request(tenant, "historical"+tenant)
		ref := domain.QuotaReference{ID: d.PolicyID, Revision: d.Revision, Digest: d.Digest}
		fingerprint, e := r.Fingerprint(ref)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(d)
		if _, e = owner.Exec(ctx, `INSERT INTO worker_run_quota_reservations(run_id,tenant_id,input_digest,fingerprint,quota_id,quota_revision,quota_digest,quota_jsonb,reserved_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp()-interval '61 seconds')`, r.RunID, r.TenantID, r.InputDigest, fingerprint, ref.ID, ref.Revision, ref.Digest, raw); e != nil {
			t.Fatal(e)
		}
		_, e = qa.ReserveRunQuota(ctx, request(tenant, "after-window"), d)
		if concurrency == 1 && !errors.Is(e, domain.ErrQuota) {
			t.Fatal("unknown/old reservation refunded", e)
		}
		if concurrency > 1 && e != nil {
			t.Fatal("old rate window retained", e)
		}
	}
	historical(prefix+"oldheld", 1)
	historical(prefix+"oldrate", 10)
	rollbackTenant := prefix + "rollback"
	rollbackDoc := quotaDocument(t, rollbackTenant, 1, 1)
	r := request(rollbackTenant, "rollback")
	tx, e := a.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = qa.ReserveRunQuotaInTransaction(ctx, tx, r, rollbackDoc); e != nil {
		rollback(tx)
		t.Fatal(e)
	}
	var n int
	if e = b.QueryRow(ctx, `SELECT count(*) FROM worker_run_quota_reservations WHERE run_id=$1`, r.RunID).Scan(&n); e != nil || n != 0 {
		rollback(tx)
		t.Fatal("owner tx committed early", n, e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = qb.ReserveRunQuota(ctx, r, rollbackDoc); e != nil {
		t.Fatal("rollback leaked quota", e)
	}
	for _, sql := range []string{`UPDATE worker_run_quota_reservations SET reserved_at=clock_timestamp() WHERE run_id=$1`, `DELETE FROM worker_run_quota_reservations WHERE run_id=$1`} {
		if _, e = a.Exec(ctx, sql, winner.RunID); e == nil {
			t.Fatal("reservation mutable/refundable without owner proof")
		}
	}
	t.Log("SHARED_RUN_QUOTA=PASS two database pools; concurrent cap1 gives1 winner/7 denials; immutable replay; tenant/policy/storage bounds; rate window; unknown held; outer rollback (historical timestamps are fixtures, no model-cost claim)")
}
