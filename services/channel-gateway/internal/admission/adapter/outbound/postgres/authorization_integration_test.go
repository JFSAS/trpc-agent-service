package postgresadapter_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	shared "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
	"sync"
	"testing"
	"time"

	"github.com/gowebpki/jcs"

	"github.com/jackc/pgx/v5/pgxpool"
	eventwire "github.com/liuzengh/trpc-agent-service/api/events/execution/v1"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	authpg "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/outbound/authorizationpostgres"
	admissionpg "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/outbound/postgres"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
)

const authorizationEpoch = "11111111-1111-4111-8111-111111111111"

type authReaderFunc func(context.Context, domain.AuthorizationTarget, func(context.Context, wire.AuthorizationSnapshotPage) error) (domain.AuthorizationRead, error)

func (f authReaderFunc) ReadAuthorization(c context.Context, t domain.AuthorizationTarget, s func(context.Context, wire.AuthorizationSnapshotPage) error) (domain.AuthorizationRead, error) {
	return f(c, t, s)
}
func authorizationFixture(t *testing.T, generation int64, state, mode string, age int64) (domain.AuthorizationRead, []wire.AuthorizationSnapshotPage) {
	t.Helper()
	session, quota := authorizationDefinition(t, "tenant", "session"), authorizationDefinition(t, "tenant", "quota")
	sr := wire.PolicyReference{ID: session.PolicyID, Revision: 1, Digest: session.Digest}
	qr := wire.PolicyReference{ID: quota.PolicyID, Revision: 1, Digest: quota.Digest}
	p := wire.AccessPolicyDocument{SchemaVersion: 1, TenantID: "tenant", AccountID: "account", Provider: "telegram", PolicyID: "policy", Revision: 1, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), Body: wire.AccessPolicyBody{AccessMode: mode, AllowedPrincipalIDs: []string{"principal"}, AllowedConversationIDs: []string{"-200"}, AllowedOperations: []string{"message.send"}, AuthorizationMaxAgeMS: age, SessionPolicy: sr, TenantQuota: qr}}
	if mode == "PUBLIC_LIMITED" {
		p.Body.AllowedPrincipalIDs = []string{}
	}
	if mode == "DENY_ALL" {
		p.Body.AllowedPrincipalIDs = []string{}
		p.Body.AllowedConversationIDs = []string{}
		p.Body.AllowedOperations = []string{}
		p.Body.SessionPolicy = wire.PolicyReference{}
		p.Body.TenantQuota = wire.PolicyReference{}
	}
	raw, _ := json.Marshal(p)
	raw, e := jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	p.Digest = "sha256:" + hex.EncodeToString(sum[:])
	id := wire.AuthorizationSnapshotIdentity{SchemaVersion: 1, ScopeID: "pool", SourceEpoch: authorizationEpoch, TenantID: "tenant", AccountID: "account", Provider: "telegram", Generation: generation}
	principal := wire.AuthorizationPrincipal{PrincipalID: "principal", ExternalUserID: "100", State: state, Revision: generation}
	d := wire.NewPrincipalSetDigest()
	if e = d.Add(principal); e != nil {
		t.Fatal(e)
	}
	n, root := d.Result()
	m := wire.AuthorizationSnapshotManifest{AuthorizationSnapshotIdentity: id, AccountRevision: 1, AccountEnabled: true, Policy: wire.PolicyReference{ID: p.PolicyID, Revision: 1, Digest: p.Digest}, CapturedAt: time.Now().UTC(), AuthorizationMaxAgeMS: age, PrincipalCount: n, PrincipalDigest: root}
	var pair *shared.PolicyDependencies
	if mode != "DENY_ALL" {
		pair = &shared.PolicyDependencies{Session: session, Quota: quota}
	}
	return domain.AuthorizationRead{Manifest: m, Policy: p, Dependencies: pair}, []wire.AuthorizationSnapshotPage{{AuthorizationSnapshotIdentity: id, Principals: []wire.AuthorizationPrincipal{principal}, Complete: true}}
}
func installAuthorization(t *testing.T, store *authpg.Store, generation int64, state, mode string, age int64) {
	t.Helper()
	r, p := authorizationFixture(t, generation, state, mode, age)
	if e := store.Refresh(context.Background(), domain.AuthorizationTarget{TenantID: "tenant", AccountID: "account", Provider: "telegram"}, authReaderFunc(func(c context.Context, _ domain.AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (domain.AuthorizationRead, error) {
		for _, page := range p {
			if e := stage(c, page); e != nil {
				return domain.AuthorizationRead{}, e
			}
		}
		return r, nil
	})); e != nil {
		t.Fatal(e)
	}
}
func authorizationSetup(t *testing.T) (*admissionpg.Store, *pgxpool.Pool, *authpg.Store) {
	s, p, _ := setup(t)
	g, e := authpg.New(p, "pool", authorizationEpoch)
	if e != nil {
		t.Fatal(e)
	}
	return s.WithAuthorizationGuard(g), p, g
}
func authAcceptance(id string) domain.Acceptance {
	c := acceptance(id)
	c.Input.ConversationKind = "private"
	return c
}
func countAuthorizationRows(t *testing.T, p *pgxpool.Pool, inbox, runs, facts, audits int) {
	t.Helper()
	for table, want := range map[string]int{"gateway_inbox": inbox, "gateway_admissions": runs, "gateway_outbox": runs, "gateway_admission_authorizations": facts, "gateway_authorization_audit_outbox": audits} {
		var n int
		if e := p.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); e != nil || n != want {
			t.Fatalf("%s=%d want %d err=%v", table, n, want, e)
		}
	}
}
func TestAuthorizationReceiptFirstDenialAndRevocation(t *testing.T) {
	s, p, g := authorizationSetup(t)
	installAuthorization(t, g, 1, "ACTIVE", "ALLOWLIST", 30000)
	c := authAcceptance("allowed")
	r, e := s.Commit(context.Background(), c)
	if e != nil || r.Decision != "admit-run" {
		t.Fatal(r, e)
	}
	if _, err := s.ReadReplySnapshot(context.Background(), c.Receipt.AdmissionID, c.Receipt.RunID); err != nil {
		t.Fatal("authorization metadata broke reply contract", err)
	}
	var payload []byte
	if err := p.QueryRow(context.Background(), `SELECT payload FROM gateway_outbox WHERE event_id=$1`, c.Receipt.AdmissionID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	event, err := eventwire.DecodeRunRequested(payload)
	if err != nil || event.Authorization == nil || event.Authorization.PrincipalID != "principal" || event.Authorization.ExternalUserID != "100" || event.Authorization.RouteGeneration != 1 {
		t.Fatal("outbox lost authorization", event, err)
	}
	denied := authAcceptance("unknown")
	denied.Input.SenderID = "200"
	r, e = s.Commit(context.Background(), denied)
	if e != nil || r.Decision != "denied" || r.Reason != "PRINCIPAL_UNKNOWN" {
		t.Fatal(r, e)
	}
	installAuthorization(t, g, 2, "REVOKED", "ALLOWLIST", 30000)
	r, e = s.Commit(context.Background(), authAcceptance("revoked"))
	if e != nil || r.Decision != "denied" || r.Reason != "PRINCIPAL_REVOKED" {
		t.Fatal(r, e)
	}
	// Stale/blocked projection does not change the first durable interpretation.
	if _, e = p.Exec(context.Background(), `UPDATE gateway_authorization_snapshots SET blocked_reason='SOURCE_CHANGED'`); e != nil {
		t.Fatal(e)
	}
	r, e = s.Commit(context.Background(), c)
	if e != nil || r != c.Receipt {
		t.Fatal("old receipt not replayed", r, e)
	}
	r, e = s.Commit(context.Background(), denied)
	if e != nil || r.Reason != "PRINCIPAL_UNKNOWN" {
		t.Fatal("denial changed", r, e)
	}
	if _, e = s.Commit(context.Background(), authAcceptance("blocked")); !errors.Is(e, domain.ErrUnavailable) {
		t.Fatal(e)
	}
	countAuthorizationRows(t, p, 3, 1, 3, 3)
	var principal, decision string
	if e = p.QueryRow(context.Background(), `SELECT decision_fact->>'principal_id',decision_fact->>'decision' FROM gateway_admission_authorizations WHERE event_id=$1`, c.Input.Key.EventID).Scan(&principal, &decision); e != nil || principal != "principal" || decision != "ALLOW" {
		t.Fatal(principal, decision, e)
	}
}
func TestAuthorizationUnknownAndGroupScope(t *testing.T) {
	for _, mode := range []string{"missing", "expired", "tenant", "group", "unknown_kind", "deny_all", "public_limited", "allowed_group"} {
		t.Run(mode, func(t *testing.T) {
			s, p, g := authorizationSetup(t)
			age := int64(30000)
			policy := "ALLOWLIST"
			if mode == "expired" {
				age = 1000
			}
			if mode == "public_limited" {
				policy = "PUBLIC_LIMITED"
			}
			if mode == "deny_all" {
				policy = "DENY_ALL"
			}
			if mode != "missing" {
				installAuthorization(t, g, 1, "ACTIVE", policy, age)
			}
			c := authAcceptance(mode)
			if mode == "expired" {
				time.Sleep(1100 * time.Millisecond)
			}
			if mode == "tenant" {
				c.Route.TenantID = "other"
			}
			if mode == "allowed_group" {
				c.Input.ConversationKind = "group"
				c.Input.ConversationID = "-200"
				c.Input.ReplyContext = json.RawMessage(`{"chat_id":"-200","source_message_id":"42"}`)
			}
			if mode == "group" {
				c.Input.ConversationKind = "group"
			}
			if mode == "unknown_kind" {
				c.Input.ConversationKind = ""
			}
			r, e := s.Commit(context.Background(), c)
			if mode == "allowed_group" {
				if e != nil || r.Decision != "admit-run" {
					t.Fatal(r, e)
				}
				countAuthorizationRows(t, p, 1, 1, 1, 1)
			} else if mode == "group" || mode == "deny_all" {
				if e != nil || r.Decision != "denied" {
					t.Fatal(r, e)
				}
				countAuthorizationRows(t, p, 1, 0, 1, 1)
			} else {
				if e == nil {
					t.Fatal("unknown authorized", r)
				}
				countAuthorizationRows(t, p, 0, 0, 0, 0)
			}
		})
	}
}
func TestAuthorizationAtomicFailureAndTimeRecheck(t *testing.T) {
	for _, mode := range []string{"audit_failure", "expired_during_commit"} {
		t.Run(mode, func(t *testing.T) {
			s, p, g := authorizationSetup(t)
			age := int64(30000)
			if mode == "expired_during_commit" {
				age = 1000
			}
			installAuthorization(t, g, 1, "ACTIVE", "ALLOWLIST", age)
			sql := `CREATE FUNCTION fail_auth_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit failure'; END $$; CREATE TRIGGER fail_auth_audit BEFORE INSERT ON gateway_authorization_audit_outbox FOR EACH ROW EXECUTE FUNCTION fail_auth_audit()`
			if mode == "expired_during_commit" {
				sql = `CREATE FUNCTION slow_auth_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(1.1); RETURN NEW; END $$; CREATE TRIGGER slow_auth_receipt BEFORE INSERT ON gateway_inbox FOR EACH ROW EXECUTE FUNCTION slow_auth_receipt()`
			}
			if _, e := p.Exec(context.Background(), sql); e != nil {
				t.Fatal(e)
			}
			if _, e := s.Commit(context.Background(), authAcceptance(mode)); e == nil {
				t.Fatal("failed/expired acceptance committed")
			}
			countAuthorizationRows(t, p, 0, 0, 0, 0)
		})
	}
}
func TestAuthorizationConcurrentDeduplication(t *testing.T) {
	s, p, g := authorizationSetup(t)
	installAuthorization(t, g, 1, "ACTIVE", "ALLOWLIST", 30000)
	c := authAcceptance("race")
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.Commit(context.Background(), c)
			if e == nil && r != c.Receipt {
				e = errors.New("different receipt")
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	countAuthorizationRows(t, p, 1, 1, 1, 1)
}
func TestAuthorizationHeadLockFencesInstallation(t *testing.T) {
	_, p, g := authorizationSetup(t)
	installAuthorization(t, g, 1, "ACTIVE", "ALLOWLIST", 30000)
	ctx := context.Background()
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	c := authAcceptance("locked")
	if _, e = g.VerifyAuthorization(ctx, tx, c.Input, *c.Route); e != nil {
		t.Fatal(e)
	}
	other, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Rollback(ctx)
	_, e = other.Exec(ctx, `SELECT account_id FROM gateway_authorization_snapshots WHERE scope_id='pool' AND account_id='account' FOR UPDATE NOWAIT`)
	var pgerr interface{ SQLState() string }
	if !errors.As(e, &pgerr) || pgerr.SQLState() != "55P03" {
		t.Fatal("head not fenced", e)
	}
}

func authorizationDefinition(t *testing.T, tenant, kind string) wire.PolicyDefinitionDocument {
	t.Helper()
	d := wire.PolicyDefinitionDocument{SchemaVersion: 1, TenantID: tenant, PolicyID: kind, Kind: kind, Revision: 1, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), Definition: wire.PolicyDefinition{Enabled: true}}
	if kind == "session" {
		d.Definition.Session = &wire.SessionDefinition{Partition: "per_user_in_conversation"}
	} else {
		d.Definition.Quota = &wire.QuotaDefinition{MaxConcurrentRuns: 1, MaxRunsPerMinute: 5}
	}
	signAuthorizationDefinition(t, &d)
	return d
}
func signAuthorizationDefinition(t *testing.T, d *wire.PolicyDefinitionDocument) {
	t.Helper()
	d.Digest = ""
	raw, _ := json.Marshal(d)
	raw, e := jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	d.Digest = "sha256:" + hex.EncodeToString(sum[:])
}

func TestAuthorizationDependencyUnknownVersusDisabled(t *testing.T) {
	for _, mode := range []string{"missing", "disabled_session", "disabled_quota"} {
		t.Run(mode, func(t *testing.T) {
			s, p, g := authorizationSetup(t)
			read, pages := authorizationFixture(t, 1, "ACTIVE", "ALLOWLIST", 30000)
			if mode == "missing" {
				read.Dependencies = nil
			} else {
				doc := &read.Dependencies.Session
				if mode == "disabled_quota" {
					doc = &read.Dependencies.Quota
				}
				doc.Definition.Enabled = false
				signAuthorizationDefinition(t, doc)
				if mode == "disabled_session" {
					read.Policy.Body.SessionPolicy.Digest = doc.Digest
				} else {
					read.Policy.Body.TenantQuota.Digest = doc.Digest
				}
				read.Policy.Digest = ""
				raw, _ := json.Marshal(read.Policy)
				raw, e := jcs.Transform(raw)
				if e != nil {
					t.Fatal(e)
				}
				sum := sha256.Sum256(raw)
				read.Policy.Digest = "sha256:" + hex.EncodeToString(sum[:])
				read.Manifest.Policy.Digest = read.Policy.Digest
			}
			e := g.Refresh(context.Background(), domain.AuthorizationTarget{TenantID: "tenant", AccountID: "account", Provider: "telegram"}, authReaderFunc(func(ctx context.Context, _ domain.AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (domain.AuthorizationRead, error) {
				for _, page := range pages {
					if e := stage(ctx, page); e != nil {
						return domain.AuthorizationRead{}, e
					}
				}
				return read, nil
			}))
			if e != nil {
				t.Fatal(e)
			}
			input := authAcceptance(mode)
			receipt, e := s.Commit(context.Background(), input)
			if mode == "missing" {
				if !errors.Is(e, domain.ErrUnavailable) {
					t.Fatal(receipt, e)
				}
				countAuthorizationRows(t, p, 0, 0, 0, 0)
			} else {
				if e != nil || receipt.Decision != "denied" || receipt.Reason != "POLICY_DEPENDENCY_DENIED" {
					t.Fatal(receipt, e)
				}
				again, e := s.Commit(context.Background(), input)
				if e != nil || again != receipt {
					t.Fatal("denial replay", again, e)
				}
				countAuthorizationRows(t, p, 1, 0, 1, 1)
			}
		})
	}
}
