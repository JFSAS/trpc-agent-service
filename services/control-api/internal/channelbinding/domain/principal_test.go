package domain_test

import (
	"errors"
	"fmt"
	d "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
	"strings"
	"testing"
	"time"
)

func TestPrincipalLifecyclePreservesIdentityAndUsesCAS(t *testing.T) {
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	account, err := d.NewAccount("tenant-a", "account-a", "scope-a", "owner-a", d.Telegram, "123", "Bot", "", now)
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.NewExternalPrincipal(account, "principal-a", "owner-a", "000456", now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Identity.ExternalUserID != "456" || p.Identity.AccountID != "account-a" || p.Identity.TenantID != "tenant-a" || p.State != d.PrincipalActive || p.Revision != 1 {
		t.Fatalf("bad principal: %+v", p)
	}
	revoked, changed, err := p.SetState(1, d.PrincipalRevoked, now.Add(time.Second))
	if err != nil || !changed || revoked.Revision != 2 || revoked.Identity != p.Identity {
		t.Fatalf("revoke: %+v %v %v", revoked, changed, err)
	}
	if p.State != d.PrincipalActive || p.Revision != 1 {
		t.Fatal("mutated old value")
	}
	same, changed, err := revoked.SetState(2, d.PrincipalRevoked, now.Add(2*time.Second))
	if err != nil || changed || same != revoked {
		t.Fatal("repeat revoke must preserve revision and timestamp")
	}
	_, _, err = revoked.SetState(1, d.PrincipalActive, now.Add(2*time.Second))
	var conflict *d.Error
	if !errors.As(err, &conflict) || conflict.Code != d.PrincipalRevisionConflict {
		t.Fatalf("stale CAS: %v", err)
	}
	active, changed, err := revoked.SetState(2, d.PrincipalActive, now.Add(2*time.Second))
	if err != nil || !changed || active.Revision != 3 || active.ID != p.ID || active.Identity != p.Identity {
		t.Fatal("explicit reactivation must preserve identity and advance revision")
	}
}

func TestPrincipalRejectsInvalidIdentitiesWithoutEcho(t *testing.T) {
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	for _, provider := range []d.Provider{d.Telegram, d.WeCom} {
		account, err := d.NewAccount("tenant-a", "account-a", "scope-a", "owner-a", provider, "123", "Bot", "", now)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range []string{"", " ", "private-user-canary\n", "a b", string([]byte{0xff}), strings.Repeat("a", 1025)} {
			p, err := d.NewExternalPrincipal(account, "principal-a", "owner-a", input, now)
			var failure *d.Error
			if !errors.As(err, &failure) || failure.Code != d.InputInvalid || failure.Field != "/external_user_id" || p != (d.ExternalPrincipal{}) {
				t.Fatalf("invalid identity accepted or wrong error for provider %s", provider)
			}
			if strings.Contains(err.Error(), "private-user-canary") {
				t.Fatal("error echoed external identity")
			}
		}
	}
}

func TestPrincipalLifecycleRejectsCorruptStateAndInvalidTransitions(t *testing.T) {
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	a, err := d.NewAccount("tenant-a", "account-a", "scope-a", "owner-a", d.Telegram, "123", "Bot", "", now)
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.NewExternalPrincipal(a, "principal-a", "owner-a", "456", now)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*d.ExternalPrincipal){
		"noncanonical identity": func(v *d.ExternalPrincipal) { v.Identity.ExternalUserID = "00456" },
		"missing tenant":        func(v *d.ExternalPrincipal) { v.Identity.TenantID = "" },
		"missing account":       func(v *d.ExternalPrincipal) { v.Identity.AccountID = "" },
		"unknown provider":      func(v *d.ExternalPrincipal) { v.Identity.Provider = "other" },
		"missing actor":         func(v *d.ExternalPrincipal) { v.CreatedBy = "" },
		"unknown state":         func(v *d.ExternalPrincipal) { v.State = "DELETED" },
		"invalid revision":      func(v *d.ExternalPrincipal) { v.Revision = 0 },
		"missing created time":  func(v *d.ExternalPrincipal) { v.CreatedAt = time.Time{} },
		"time reversal":         func(v *d.ExternalPrincipal) { v.UpdatedAt = now.Add(-time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := p
			mutate(&bad)
			if bad.Validate() == nil {
				t.Fatal("corrupt persisted principal accepted")
			}
			result, changed, err := bad.SetState(bad.Revision, d.PrincipalRevoked, now)
			if err == nil || changed || result != bad {
				t.Fatal("corrupt source advanced")
			}
		})
	}
	for _, state := range []d.PrincipalState{"", "DELETED", "OWNER"} {
		same, changed, err := p.SetState(1, state, now)
		if err == nil || changed || same != p {
			t.Fatal("invalid state changed identity")
		}
	}
	for _, at := range []time.Time{{}, now.Add(-time.Second)} {
		same, changed, err := p.SetState(1, d.PrincipalRevoked, at)
		if err == nil || changed || same != p {
			t.Fatal("invalid clock changed identity")
		}
	}
	p.Revision = d.MaxVersion
	same, changed, err := p.SetState(d.MaxVersion, d.PrincipalRevoked, now)
	var exhausted *d.Error
	if !errors.As(err, &exhausted) || exhausted.Code != d.VersionExhausted || changed || same != p {
		t.Fatal("version overflow")
	}
	same, changed, err = p.SetState(d.MaxVersion, d.PrincipalActive, now)
	if err != nil || changed || same != p {
		t.Fatal("current no-op at max revision should not allocate a revision")
	}
}

func TestPrincipalIdentityDoesNotMergeAcrossTenantAccountOrProvider(t *testing.T) {
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	identities := map[d.ExternalPrincipalIdentity]bool{}
	for i, scope := range []struct {
		tenant, account string
		provider        d.Provider
	}{
		{"tenant-a", "account-a", d.Telegram}, {"tenant-b", "account-a", d.Telegram},
		{"tenant-a", "account-b", d.Telegram}, {"tenant-a", "account-c", d.WeCom},
	} {
		a, err := d.NewAccount(scope.tenant, scope.account, "scope-a", "owner-a", scope.provider, "123", "Bot", "", now)
		if err != nil {
			t.Fatal(err)
		}
		p, err := d.NewExternalPrincipal(a, fmt.Sprintf("principal-%d", i), "owner-a", "456", now)
		if err != nil || identities[p.Identity] {
			t.Fatal("cross-scope identity was merged")
		}
		identities[p.Identity] = true
	}
}
