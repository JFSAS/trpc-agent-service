package postgresadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
	"github.com/liuzengh/trpc-agent-service/services/control-api/migrations"
)

func policyPG(t *testing.T) (*Store, *application.Service, *pgxpool.Pool, domain.Account) {
	t.Helper()
	store, s, pool := channelPG(t, true)
	for _, name := range []string{"0002_channel_preflights.sql", "0003_telegram_receive_modes.sql", "0004_wecom_preflights.sql", "0005_channel_principals.sql", "0006_channel_access_policies.sql"} {
		raw, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(context.Background(), string(raw)); err != nil {
			t.Fatal(name, err)
		}
	}
	created := createAccount(t, s)
	// Public AccountView intentionally redacts ScopeID. Candidates must derive
	// identity from an owner-loaded internal Account, not a public DTO.
	loaded, err := store.GetAccount(context.Background(), testActor.TenantID, created.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store, s, pool, loaded.Account
}
func appendPolicy(t *testing.T, store *Store, a domain.Account, revision int64, body domain.AccessPolicyBody, event string) error {
	t.Helper()
	p, err := domain.PrepareAccessPolicyRevision(a, "policy-a", revision, testActor.UserID, body, time.Date(2026, 9, 8, 0, 0, int(revision), 0, time.UTC))
	if err != nil {
		return err
	}
	return store.WithPolicyWrite(context.Background(), application.WriteScope{ScopeID: testScope, Actor: testActor}, func(tx application.PolicyRevisionTransaction) error {
		return tx.AppendAccessPolicy(context.Background(), p, revision-1, event, event+"-audit")
	})
}
func TestAccessPolicyRevisionCASOutboxesAndRollbackAgainstPostgreSQL(t *testing.T) {
	store, _, pool, a := policyPG(t)
	ctx := context.Background()
	body := domain.DefaultAccessPolicy()
	if err := appendPolicy(t, store, a, 1, body, "first"); err != nil {
		t.Fatal(err)
	}
	var first domain.ChannelAccessPolicyRevision
	if err := store.WithPolicyWrite(ctx, application.WriteScope{ScopeID: testScope, Actor: testActor}, func(tx application.PolicyRevisionTransaction) error {
		p, found, err := tx.LoadAccessPolicy(ctx, a.ID)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("missing policy")
		}
		first = p
		return p.Validate()
	}); err != nil {
		t.Fatal("JSONB digest round trip", err)
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = appendPolicy(t, store, a, 2, body, fmt.Sprintf("next-%d", i))
		}(i)
	}
	wg.Wait()
	wins, conflicts := 0, 0
	for _, err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, application.ErrPolicyRevisionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("CAS", wins, conflicts)
	}
	assertCounts := func(revisions, outboxes int) {
		t.Helper()
		var rows, events int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM channel_access_policy_revisions),(SELECT count(*) FROM control_outbox WHERE aggregate_type='ChannelAccessPolicy')`).Scan(&rows, &events); err != nil || rows != revisions || events != outboxes {
			t.Fatal("atomic records", rows, events, err)
		}
	}
	assertCounts(2, 4)
	// Every raw revision update/delete and head rewind must fail in the DB too.
	for _, sql := range []string{`UPDATE channel_access_policy_revisions SET digest=digest`, `DELETE FROM channel_access_policy_revisions`, `UPDATE channel_access_policies SET latest_revision=1`, `UPDATE channel_access_policies SET policy_id='substituted'`} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Fatal("immutable record changed", sql)
		}
	}
	// Failure at the second outbox must remove the revision, first outbox and head advance.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_policy_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='channel.access_policy.audit.v1' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_policy_audit BEFORE INSERT ON control_outbox FOR EACH ROW EXECUTE FUNCTION fail_policy_audit()`); err != nil {
		t.Fatal(err)
	}
	if err := appendPolicy(t, store, a, 3, body, "third"); err == nil {
		t.Fatal("audit failure committed")
	}
	assertCounts(2, 4)
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_policy_audit ON control_outbox; DROP FUNCTION fail_policy_audit()`); err != nil {
		t.Fatal(err)
	}
	if err := appendPolicy(t, store, a, 3, body, "third"); err != nil {
		t.Fatal("retry after rollback", err)
	}
	assertCounts(3, 6)
	if err := store.WithPolicyWrite(ctx, application.WriteScope{ScopeID: testScope, Actor: application.Actor{TenantID: testActor.TenantID, UserID: "usr_other"}}, func(application.PolicyRevisionTransaction) error {
		t.Error("non-owner entered transaction")
		return nil
	}); !errors.Is(err, application.ErrPermissionDenied) {
		t.Fatal("OWNER", err)
	}
	cross := application.Actor{TenantID: "tnt_b", UserID: "usr_other"}
	if err := store.WithPolicyWrite(ctx, application.WriteScope{ScopeID: testScope, Actor: cross}, func(tx application.PolicyRevisionTransaction) error {
		_, _, err := tx.LoadAccessPolicy(ctx, a.ID)
		return err
	}); !errors.Is(err, application.ErrAccountNotFound) {
		t.Fatal("cross tenant", err)
	}
	var original []byte
	if err := pool.QueryRow(ctx, `SELECT document_jsonb FROM channel_access_policy_revisions WHERE revision=1`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	restored, err := domain.DecodeAccessPolicyRevision(original)
	if err != nil || restored.Digest != first.Digest {
		t.Fatal("historical revision changed", err)
	}
}

func TestAccessPolicyPrincipalScopeAndOutboxPrivacyAgainstPostgreSQL(t *testing.T) {
	store, s, pool, a := policyPG(t)
	ctx := context.Background()
	principal, err := s.RegisterExternalPrincipal(ctx, testActor, a.ID, "principal", application.RegisterPrincipalInput{ExternalUserID: "777"})
	if err != nil {
		t.Fatal(err)
	}
	body := domain.DefaultAccessPolicy()
	body.AccessMode = domain.AccessAllowlist
	body.AllowedPrincipalIDs = []string{principal.Principal.ID}
	body.AllowedOperations = []domain.ChannelOperation{domain.OperationMessageSend}
	// These references are storage fixtures, not proof of resolved Session/Quota owners.
	body.SessionPolicy = domain.PolicyRevisionReference{ID: "session-fixture", Revision: 1, Digest: "sha256:" + strings.Repeat("a", 64)}
	body.TenantQuota = domain.PolicyRevisionReference{ID: "quota-fixture", Revision: 1, Digest: "sha256:" + strings.Repeat("b", 64)}
	if err = appendPolicy(t, store, a, 1, body, "allowlist"); err != nil {
		t.Fatal(err)
	}
	var leaked int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_outbox WHERE aggregate_type='ChannelAccessPolicy' AND (payload_jsonb::text LIKE '%' || $1 || '%' OR payload_jsonb ? 'body' OR payload_jsonb ? 'allowed_principal_ids')`, principal.Principal.ID).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("outbox includes access list", leaked, err)
	}
	if _, err = s.SetExternalPrincipalState(ctx, testActor, a.ID, principal.Principal.ID, "revoke", application.SetPrincipalStateInput{ExpectedRevision: 1, State: domain.PrincipalRevoked}); err != nil {
		t.Fatal(err)
	}
	if err = appendPolicy(t, store, a, 2, body, "revoked"); !errors.Is(err, application.ErrPermissionDenied) {
		t.Fatal("revoked principal", err)
	}
	body.AllowedPrincipalIDs = []string{"missing-principal"}
	if err = appendPolicy(t, store, a, 2, body, "missing"); !errors.Is(err, application.ErrPrincipalNotFound) {
		t.Fatal("unknown principal", err)
	}

	// Even direct SQL must not commit an incomplete or extra normalized member set.
	extra, err := s.RegisterExternalPrincipal(ctx, testActor, a.ID, "extra-member", application.RegisterPrincipalInput{ExternalUserID: "778"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO channel_access_policy_principals(tenant_id,account_id,provider,policy_id,revision,principal_id) VALUES($1,$2,$3,'policy-a',1,$4)`, a.TenantID, a.ID, a.Provider, extra.Principal.ID); err == nil {
		t.Fatal("extra member committed after publication")
	}
	body.AllowedPrincipalIDs = []string{principal.Principal.ID}
	candidate, err := domain.PrepareAccessPolicyRevision(a, "policy-a", 2, testActor.UserID, body, time.Date(2026, 9, 8, 0, 0, 2, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := domain.CanonicalJSON(candidate)
	if err != nil {
		t.Fatal(err)
	}
	dbtx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dbtx.Exec(ctx, `UPDATE channel_access_policies SET latest_revision=2 WHERE tenant_id=$1 AND account_id=$2`, a.TenantID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = dbtx.Exec(ctx, `INSERT INTO channel_access_policy_revisions(tenant_id,account_id,provider,policy_id,revision,digest,document_jsonb,published_by,published_at) VALUES($1,$2,$3,'policy-a',2,$4,$5,$6,$7)`, a.TenantID, a.ID, a.Provider, candidate.Digest, raw, candidate.PublishedBy, candidate.PublishedAt); err != nil {
		t.Fatal(err)
	}
	if err = dbtx.Commit(ctx); err == nil {
		t.Fatal("missing normalized member set committed")
	}
	// Historical membership remains immutable even after the current principal is revoked.
	if _, err = pool.Exec(ctx, `DELETE FROM channel_access_policy_principals`); err == nil {
		t.Fatal("historical member removed")
	}
	var head int64
	if err = pool.QueryRow(ctx, `SELECT latest_revision FROM channel_access_policies`).Scan(&head); err != nil || head != 1 {
		t.Fatal("rejected publication changed head", head, err)
	}
}
