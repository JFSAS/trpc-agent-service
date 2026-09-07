package application

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

func TestPrincipalCommandsReplayCASAndRevocation(t *testing.T) {
	s, m, _, _ := setup(t)
	a := create(t, s).Account
	ctx := context.Background()
	in := RegisterPrincipalInput{ExternalUserID: "00456"}
	r, err := s.RegisterExternalPrincipal(ctx, owner, a.ID, "register", in)
	if err != nil || r.Principal == nil {
		t.Fatal(r, err)
	}
	p := *r.Principal
	if p.Identity.ExternalUserID != "456" || r.Distribution != "NOT_EMITTED" || len(m.data.events) != 0 {
		t.Fatal(r)
	}
	again, err := s.RegisterExternalPrincipal(ctx, owner, a.ID, "register", in)
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatal("replay", again, err)
	}
	if _, err = s.RegisterExternalPrincipal(ctx, owner, a.ID, "register", RegisterPrincipalInput{ExternalUserID: "789"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("key payload conflict", err)
	}
	if _, err = s.RegisterExternalPrincipal(ctx, owner, a.ID, "duplicate", in); !errors.Is(err, ErrPrincipalIdentityConflict) {
		t.Fatal("duplicate", err)
	}
	r, err = s.SetExternalPrincipalState(ctx, owner, a.ID, p.ID, "revoke", SetPrincipalStateInput{ExpectedRevision: 1, State: domain.PrincipalRevoked})
	if err != nil || r.Principal.State != domain.PrincipalRevoked || r.Principal.Revision != 2 {
		t.Fatal(r, err)
	}
	if _, err = s.RegisterExternalPrincipal(ctx, owner, a.ID, "reregister", in); !errors.Is(err, ErrPrincipalIdentityConflict) {
		t.Fatal("revoked registration", err)
	}
	if _, err = s.SetExternalPrincipalState(ctx, owner, a.ID, p.ID, "stale", SetPrincipalStateInput{ExpectedRevision: 1, State: domain.PrincipalActive}); err == nil || err.Error() != domain.PrincipalRevisionConflict {
		t.Fatal("CAS", err)
	}
	noOp, err := s.SetExternalPrincipalState(ctx, owner, a.ID, p.ID, "noop", SetPrincipalStateInput{ExpectedRevision: 2, State: domain.PrincipalRevoked})
	if err != nil || !reflect.DeepEqual(r, noOp) {
		t.Fatal("noop", noOp, err)
	}
}

func TestPrincipalCommandsAreOwnerScopedAndAtomic(t *testing.T) {
	for _, mode := range []string{"member", "revoked_owner", "other_tenant", "receipt_failure"} {
		t.Run(mode, func(t *testing.T) {
			s, m, access, _ := setup(t)
			a := create(t, s).Account
			actor := owner
			switch mode {
			case "member":
				access.owner = false
			case "revoked_owner":
				m.beforeTx = func() { m.owner = false }
			case "other_tenant":
				actor.TenantID = "tnt_other"
			case "receipt_failure":
				m.failReceipt = true
			}
			_, err := s.RegisterExternalPrincipal(context.Background(), actor, a.ID, "register", RegisterPrincipalInput{ExternalUserID: "456"})
			if err == nil {
				t.Fatal("expected rejection")
			}
			if len(m.data.principals) != 0 {
				t.Fatal("principal survived failed transaction")
			}
		})
	}
}

func (t *memoryTx) LoadPrincipal(_ context.Context, account, id string) (domain.ExternalPrincipal, error) {
	p, ok := t.data.principals[t.scope.Actor.TenantID+"/"+id]
	if !ok || p.Identity.AccountID != account {
		return domain.ExternalPrincipal{}, ErrPrincipalNotFound
	}
	return p, nil
}
func (t *memoryTx) SavePrincipal(_ context.Context, p domain.ExternalPrincipal, expected int64) error {
	key := t.scope.Actor.TenantID + "/" + p.ID
	if t.data.principals == nil {
		t.data.principals = map[string]domain.ExternalPrincipal{}
	}
	for k, old := range t.data.principals {
		if k != key && old.Identity == p.Identity {
			return ErrPrincipalIdentityConflict
		}
	}
	old, exists := t.data.principals[key]
	if expected == 0 && exists || expected != 0 && (!exists || old.Revision != expected) {
		return &domain.Error{Code: domain.PrincipalRevisionConflict}
	}
	t.data.principals[key] = p
	return nil
}
