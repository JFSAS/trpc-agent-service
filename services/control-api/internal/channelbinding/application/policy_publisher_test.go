package application

import (
	"context"
	"errors"
	"testing"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

type untouchedPolicyStore struct{ calls int }

func (s *untouchedPolicyStore) WithPolicyWrite(context.Context, WriteScope, func(PolicyRevisionTransaction) error) error {
	s.calls++
	return ErrDependencyUnavailable
}

type unusedPolicyValidator struct{}

func (unusedPolicyValidator) ValidatePublishedPolicy(context.Context, Actor, domain.Account, domain.AccessPolicyBody) error {
	return errors.New("unexpected validator call")
}
func TestPolicyPublisherRequiresAllDependencies(t *testing.T) {
	base, _, _, _ := setup(t)
	deps := PolicyPublisherDependencies{Store: &untouchedPolicyStore{}, Access: base.deps.TenantAccess, Cipher: base.deps.Cipher, References: unusedPolicyValidator{}, ScopeID: base.deps.ScopeID, NewID: base.deps.NewID}
	for _, field := range []string{"store", "access", "cipher", "references", "scope", "id"} {
		t.Run(field, func(t *testing.T) {
			bad := deps
			switch field {
			case "store":
				bad.Store = nil
			case "access":
				bad.Access = nil
			case "cipher":
				bad.Cipher = nil
			case "references":
				bad.References = nil
			case "scope":
				bad.ScopeID = ""
			case "id":
				bad.NewID = nil
			}
			if _, err := NewPolicyPublisher(bad); !errors.Is(err, ErrDependencyUnavailable) {
				t.Fatal(err)
			}
		})
	}
}
func TestPolicyPublisherRejectsBeforeStorage(t *testing.T) {
	base, _, access, _ := setup(t)
	store := &untouchedPolicyStore{}
	s, err := NewPolicyPublisher(PolicyPublisherDependencies{Store: store, Access: access, Cipher: base.deps.Cipher, References: unusedPolicyValidator{}, ScopeID: base.deps.ScopeID, NewID: base.deps.NewID})
	if err != nil {
		t.Fatal(err)
	}
	in := PublishAccessPolicyInput{Body: domain.DefaultAccessPolicy()}
	access.owner = false
	if _, err = s.Publish(context.Background(), owner, "account-a", "key", in); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal(err)
	}
	access.owner = true
	for _, kind := range []string{"account", "key", "revision", "exhausted", "body"} {
		request := in
		account, key := "account-a", "key"
		switch kind {
		case "account":
			account = "bad account"
		case "key":
			key = ""
		case "revision":
			request.ExpectedRevision = -1
		case "exhausted":
			request.ExpectedRevision = domain.MaxVersion
		case "body":
			request.Body.AccessMode = "UNKNOWN"
		}
		out, err := s.Publish(context.Background(), owner, account, key, request)
		if err == nil || out != (PolicyPublishResult{}) {
			t.Fatal(kind, out, err)
		}
		if kind == "exhausted" && err.Error() != domain.VersionExhausted {
			t.Fatal(err)
		}
	}
	if store.calls != 0 {
		t.Fatal("invalid request reached storage")
	}
}
