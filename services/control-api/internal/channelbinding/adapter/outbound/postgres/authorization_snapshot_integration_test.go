package postgresadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/adapter/outbound/credentialcrypto"
	"testing"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
	"github.com/liuzengh/trpc-agent-service/services/control-api/migrations"
)

func TestAuthorizationSnapshotCurrentHeadPagedPrincipalsAgainstPostgreSQL(t *testing.T) {
	store, service, pool, a := policyPG(t)
	ctx := context.Background()
	if err := appendPolicy(t, store, a, 1, domain.DefaultAccessPolicy(), "snapshot-policy-1"); err != nil {
		t.Fatal(err)
	}
	sql, err := migrations.Files.ReadFile("0008_channel_authorization_generation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	// This account predates the migration. A newly created account is covered too.
	empty, err := store.ReadAuthorizationManifest(ctx, testScope, a.ID)
	if err != nil || empty.PrincipalCount != 0 || empty.Generation != 1 {
		t.Fatal("seeded generation", empty, err)
	}
	input := accountInput()
	input.ProviderAccountID = "987"
	other, err := service.CreateAccount(ctx, testActor, "snapshot-other", input)
	if err != nil {
		t.Fatal(err)
	}
	var gen int64
	if err = pool.QueryRow(ctx, `SELECT generation FROM channel_authorization_generations WHERE tenant_id=$1 AND account_id=$2`, a.TenantID, other.Account.ID).Scan(&gen); err != nil || gen != 1 {
		t.Fatal("new account generation", gen, err)
	}
	if out, e := store.ReadAuthorizationManifest(ctx, testScope, other.Account.ID); !errors.Is(e, application.ErrPolicyNotFound) || out.AccountID != "" {
		t.Fatal("missing policy", out, e)
	}
	// Bulk inserts exercise SQL-level fencing, not only command helper updates.
	_, err = pool.Exec(ctx, `INSERT INTO channel_principal_bindings(tenant_id,principal_id,account_id,provider,external_user_id,state,revision,created_by,created_at,updated_at) SELECT $1,'principal_'||lpad(i::text,4,'0'),$2,'telegram',(i+1)::text,'ACTIVE',1,'usr_owner',now(),now() FROM generate_series(0,256) i`, a.TenantID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Same local principal and external IDs in another tenant/account must not
	// enter this account's digest, count or page stream.
	bInput := accountInput()
	bInput.ProviderAccountID = "765"
	bAccount, e := service.CreateAccount(ctx, application.Actor{TenantID: "tnt_b", UserID: "usr_other"}, "snapshot-tenant-b", bInput)
	if e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(ctx, `INSERT INTO channel_principal_bindings(tenant_id,principal_id,account_id,provider,external_user_id,state,revision,created_by,created_at,updated_at) VALUES('tnt_b','principal_0000',$1,'telegram','1','REVOKED',1,'usr_other',now(),now())`, bAccount.Account.ID)
	if e != nil {
		t.Fatal(e)
	}
	m, err := store.ReadAuthorizationManifest(ctx, testScope, a.ID)
	if err != nil || m.PrincipalCount != 257 || m.Generation != 258 || m.Policy.Revision != 1 {
		t.Fatal(m, err)
	}
	cipher, e := credentialcrypto.New("snapshot_test", map[string]credentialcrypto.Key{"snapshot_test": {Encryption: bytes.Repeat([]byte{1}, 32), MAC: bytes.Repeat([]byte{2}, 32)}})
	if e != nil {
		t.Fatal(e)
	}
	runtime, e := application.NewRuntimeService(store, cipher, testScope, testEpoch)
	if e != nil {
		t.Fatal(e)
	}
	workload := application.WorkloadPrincipal{PrincipalID: "spiffe://test/gateway", InstanceID: "gateway", ScopeID: testScope, Audience: application.WorkloadAudience, Consumers: []string{application.PolicyProjectionConsumer}}
	current, e := runtime.ReadAuthorizationManifest(ctx, workload, application.AuthorizationManifestRequest{SchemaVersion: 1, AccountID: a.ID})
	if e != nil || current.PrincipalDigest != m.PrincipalDigest {
		t.Fatal("runtime snapshot", e)
	}
	bad := workload
	bad.Consumers = []string{"telegram_receiver"}
	if _, e = runtime.ReadAuthorizationManifest(ctx, bad, application.AuthorizationManifestRequest{SchemaVersion: 1, AccountID: a.ID}); !errors.Is(e, application.ErrWorkloadDenied) {
		t.Fatal("runtime capability", e)
	}
	proof, err := wire.NewAuthorizationSnapshotProof(m)
	if err != nil {
		t.Fatal(err)
	}
	cursor := ""
	pages := 0
	for {
		page, e := runtime.ReadAuthorizationPage(ctx, workload, application.AuthorizationPageRequest{SchemaVersion: 1, AccountID: a.ID, SourceEpoch: testEpoch, Generation: m.Generation, AfterPrincipalID: cursor})
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(page)
		decoded, e := wire.DecodeAuthorizationSnapshotPage(raw)
		if e != nil {
			t.Fatal(e)
		}
		if e = proof.Add(decoded); e != nil {
			t.Fatal(e)
		}
		pages++
		if page.Complete {
			break
		}
		cursor = page.NextPrincipalID
	}
	if pages != 3 || proof.Finish() != nil {
		t.Fatal("complete 3-page proof", pages)
	}
	// Revocation invalidates old pages atomically; the new proof contains REVOKED.
	_, err = pool.Exec(ctx, `UPDATE channel_principal_bindings SET state='REVOKED',revision=revision+1 WHERE tenant_id=$1 AND account_id=$2 AND principal_id='principal_0000'`, a.TenantID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	old, e := store.ReadAuthorizationPage(ctx, testScope, testEpoch, a.ID, m.Generation, "")
	if !errors.Is(e, application.ErrAuthorizationSnapshotChanged) || len(old.Principals) != 0 {
		t.Fatal("old principal snapshot", old, e)
	}
	next, err := store.ReadAuthorizationManifest(ctx, testScope, a.ID)
	if err != nil || next.Generation != m.Generation+1 || next.PrincipalDigest == m.PrincipalDigest {
		t.Fatal("revocation snapshot", next, err)
	}
	page, err := store.ReadAuthorizationPage(ctx, testScope, testEpoch, a.ID, next.Generation, "")
	if err != nil || page.Principals[0].State != "REVOKED" {
		t.Fatal("revoked row", err)
	}
	// A principal write and fence bump roll back together.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM channel_principal_bindings WHERE tenant_id=$1 AND account_id=$2 AND principal_id='principal_0001'`, a.TenantID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	concurrent, err := store.ReadAuthorizationManifest(ctx, testScope, a.ID)
	if err != nil || concurrent.Generation != next.Generation || concurrent.PrincipalDigest != next.PrincipalDigest {
		t.Fatal("uncommitted state leaked", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := store.ReadAuthorizationManifest(ctx, testScope, a.ID)
	if err != nil || after.Generation != next.Generation || after.PrincipalDigest != next.PrincipalDigest {
		t.Fatal("rollback changed proof", err)
	}
	if err = appendPolicy(t, store, a, 2, domain.DefaultAccessPolicy(), "snapshot-policy-2"); err != nil {
		t.Fatal(err)
	}
	latest, err := store.ReadAuthorizationManifest(ctx, testScope, a.ID)
	if err != nil || latest.Policy.Revision != 2 || latest.Generation != next.Generation+1 {
		t.Fatal("latest policy fence", latest, err)
	}
	// Account gate changes are in the same generation, even outside command code.
	_, err = pool.Exec(ctx, `UPDATE channel_accounts SET enabled=true,account_revision=account_revision+1 WHERE tenant_id=$1 AND id=$2`, a.TenantID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	gated, err := store.ReadAuthorizationManifest(ctx, testScope, a.ID)
	if err != nil || !gated.AccountEnabled || gated.Generation != latest.Generation+1 {
		t.Fatal("account gate", gated, err)
	}
	for _, query := range []string{`UPDATE channel_authorization_generations SET generation=generation-1 WHERE tenant_id=$1 AND account_id=$2`, `DELETE FROM channel_authorization_generations WHERE tenant_id=$1 AND account_id=$2`} {
		if _, e = pool.Exec(ctx, query, a.TenantID, a.ID); e == nil {
			t.Fatal("fence reset accepted")
		}
	}
	if _, e = pool.Exec(ctx, `UPDATE channel_principal_bindings SET account_id=$3 WHERE tenant_id=$1 AND account_id=$2 AND principal_id='principal_0000'`, a.TenantID, a.ID, other.Account.ID); e == nil {
		t.Fatal("principal moved without invalidating old fence")
	}
	for _, name := range []string{"scope", "epoch", "missing", "generation", "cursor"} {
		t.Run(name, func(t *testing.T) {
			scope, epoch, id, g, c := testScope, testEpoch, a.ID, gated.Generation, ""
			switch name {
			case "scope":
				scope = "other"
			case "epoch":
				epoch = "22222222-2222-4222-8222-222222222222"
			case "missing":
				id = "missing"
			case "generation":
				g = 0
			case "cursor":
				c = "*"
			}
			out, e := store.ReadAuthorizationPage(ctx, scope, epoch, id, g, c)
			if e == nil || out.AccountID != "" || out.Principals != nil {
				t.Fatal("invalid read", out, e)
			}
		})
	}
	// Correct scope on another store still cannot find an existing foreign account.
	outsider := *store
	outsider.options.ScopeID = "another"
	if err = outsider.EnsureCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if out, e := outsider.ReadAuthorizationManifest(ctx, "another", a.ID); !errors.Is(e, application.ErrAccountNotFound) || out.AccountID != "" {
		t.Fatal("SQL scope fence", out, e)
	}
	t.Log("AUTHORIZATION_CURRENT_SNAPSHOT=PASS 257 principals/3 pages; revoke, latest policy and account gate fences; rollback; scope/epoch isolation")
}

func TestAuthorizationGenerationExhaustionRollsBackMutationAgainstPostgreSQL(t *testing.T) {
	_, _, pool, a := policyPG(t)
	ctx := context.Background()
	sql, _ := migrations.Files.ReadFile("0008_channel_authorization_generation.sql")
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	// Admin-only corruption fixture; ordinary updates cannot jump the generation.
	_, err := pool.Exec(ctx, fmt.Sprintf(`ALTER TABLE channel_authorization_generations DISABLE TRIGGER channel_authorization_generation_guard; UPDATE channel_authorization_generations SET generation=%d; ALTER TABLE channel_authorization_generations ENABLE TRIGGER channel_authorization_generation_guard`, domain.MaxVersion))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE channel_accounts SET enabled=true WHERE tenant_id=$1 AND id=$2`, a.TenantID, a.ID); err == nil {
		t.Fatal("exhausted generation allowed mutation")
	}
	var enabled bool
	if err = pool.QueryRow(ctx, `SELECT enabled FROM channel_accounts WHERE tenant_id=$1 AND id=$2`, a.TenantID, a.ID).Scan(&enabled); err != nil || enabled {
		t.Fatal("partial account update", err)
	}
}
