package postgresadapter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
	"github.com/liuzengh/trpc-agent-service/services/control-api/migrations"
)

func TestPrincipalCommandsAgainstPostgreSQL(t *testing.T) {
	store, s, pool := channelPG(t)
	ctx := context.Background()
	migration, err := migrations.Files.ReadFile("0005_channel_principals.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	a := createAccount(t, s).Account
	in := application.RegisterPrincipalInput{ExternalUserID: "000456"}
	first, err := s.RegisterExternalPrincipal(ctx, testActor, a.ID, "register", in)
	if err != nil {
		t.Fatal("register", err)
	}
	p := first.Principal
	replay, err := s.RegisterExternalPrincipal(ctx, testActor, a.ID, "register", in)
	if err != nil || replay.Principal.ID != p.ID {
		t.Fatal("replay", err)
	}
	// Two writers compete on the same revision; exactly one advances it.
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = s.SetExternalPrincipalState(ctx, testActor, a.ID, p.ID, fmt.Sprintf("revoke-%d", i), application.SetPrincipalStateInput{ExpectedRevision: 1, State: domain.PrincipalRevoked})
		}(i)
	}
	wg.Wait()
	wins, conflicts := 0, 0
	for _, err := range results {
		if err == nil {
			wins++
		} else if err.Error() == domain.PrincipalRevisionConflict {
			conflicts++
		} else {
			t.Fatal("unexpected", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("CAS", wins, conflicts)
	}
	if _, err = s.RegisterExternalPrincipal(ctx, testActor, a.ID, "recreate", in); !errors.Is(err, application.ErrPrincipalIdentityConflict) {
		t.Fatal("revoke bypass", err)
	}
	// The injected application authorizer is permissive; real transaction ownership wins.
	outsider := application.Actor{TenantID: testActor.TenantID, UserID: "usr_other"}
	if _, err = s.RegisterExternalPrincipal(ctx, outsider, a.ID, "outsider", application.RegisterPrincipalInput{ExternalUserID: "999"}); !errors.Is(err, application.ErrPermissionDenied) {
		t.Fatal("transaction OWNER", err)
	}
	cross := application.Actor{TenantID: "tnt_b", UserID: "usr_other"}
	if _, err = s.RegisterExternalPrincipal(ctx, cross, a.ID, "cross", in); !errors.Is(err, application.ErrAccountNotFound) {
		t.Fatal("cross tenant", err)
	}
	// Repository boundaries reject forged identity fields even with a valid owner.
	for _, field := range []string{"external_user", "created_by", "tenant", "provider"} {
		err = store.WithWrite(ctx, application.WriteScope{ScopeID: testScope, Actor: testActor}, func(tx application.Transaction) error {
			old, err := tx.LoadPrincipal(ctx, a.ID, p.ID)
			if err != nil {
				return err
			}
			next, _, err := old.SetState(2, domain.PrincipalActive, old.UpdatedAt)
			if err != nil {
				return err
			}
			switch field {
			case "external_user":
				next.Identity.ExternalUserID = "777"
			case "created_by":
				next.CreatedBy = "usr_other"
			case "tenant":
				next.Identity.TenantID = "tnt_b"
			case "provider":
				next.Identity.Provider = domain.WeCom
			}
			return tx.SavePrincipal(ctx, next, 2)
		})
		if err == nil {
			t.Fatal("forged identity persisted", field)
		}
	}
	// A distinct Bot under the same tenant has a distinct principal for the same user.
	ai := accountInput()
	ai.ProviderAccountID = "789"
	second, err := s.CreateAccount(ctx, testActor, "second-account", ai)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.RegisterExternalPrincipal(ctx, testActor, second.Account.ID, "register", in)
	if err != nil || other.Principal.ID == p.ID {
		t.Fatal("account isolation", err)
	}
	if _, err = s.SetExternalPrincipalState(ctx, testActor, second.Account.ID, p.ID, "wrong-account", application.SetPrincipalStateInput{ExpectedRevision: 2, State: domain.PrincipalActive}); !errors.Is(err, application.ErrPrincipalNotFound) {
		t.Fatal("principal cross account", err)
	}
	// Same provider user under a different tenant/account remains independent.
	ai.ProviderAccountID = "790"
	tenantAccount, err := s.CreateAccount(ctx, cross, "tenant-b-account", ai)
	if err != nil {
		t.Fatal(err)
	}
	tenantPrincipal, err := s.RegisterExternalPrincipal(ctx, cross, tenantAccount.Account.ID, "register", in)
	if err != nil || tenantPrincipal.Principal.ID == p.ID || tenantPrincipal.Principal.Identity.TenantID != "tnt_b" {
		t.Fatal("tenant identity isolation", err)
	}
	// Fail receipt persistence after the principal insert, then retry the same key.
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_principal_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation = 'RegisterChannelPrincipal' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_principal_receipt BEFORE INSERT ON channel_command_receipts FOR EACH ROW EXECUTE FUNCTION reject_principal_receipt()`)
	if err != nil {
		t.Fatal(err)
	}
	failInput := application.RegisterPrincipalInput{ExternalUserID: "997"}
	if _, err = s.RegisterExternalPrincipal(ctx, testActor, a.ID, "receipt-fails", failInput); err == nil {
		t.Fatal("receipt failure committed")
	}
	var receiptFailedCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM channel_principal_bindings WHERE external_user_id='997'`).Scan(&receiptFailedCount); err != nil || receiptFailedCount != 0 {
		t.Fatal("principal survived receipt failure", err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_principal_receipt ON channel_command_receipts; DROP FUNCTION reject_principal_receipt()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RegisterExternalPrincipal(ctx, testActor, a.ID, "receipt-fails", failInput); err != nil {
		t.Fatal("retry after rollback", err)
	}
	// A failure after a row write must roll back, including its unique identity key.
	sentinel := errors.New("injected transaction failure")
	err = store.WithWrite(ctx, application.WriteScope{ScopeID: testScope, Actor: testActor}, func(tx application.Transaction) error {
		account, err := tx.LoadAccount(ctx, a.ID)
		if err != nil {
			return err
		}
		next, err := domain.NewExternalPrincipal(account.Account, "prn_rollback", testActor.UserID, "998", p.CreatedAt)
		if err != nil {
			return err
		}
		if err = tx.SavePrincipal(ctx, next, 0); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal("rollback injection", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM channel_principal_bindings WHERE external_user_id='998'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rollback", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM tenant_memberships`).Scan(&count); err != nil || count != 2 {
		t.Fatal("implicit membership", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM control_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unexpected runtime publication", count, err)
	}
}
