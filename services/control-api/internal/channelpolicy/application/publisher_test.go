package application

import (
	"context"
	"errors"
	"testing"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

type testAccess struct {
	allow bool
	err   error
}

func (a *testAccess) IsActiveOwner(context.Context, string, string) (bool, error) {
	return a.allow, a.err
}

type testStore struct{ calls int }

func (s *testStore) WithPublish(context.Context, WriteScope, func(PublishTransaction) error) error {
	s.calls++
	return ErrUnavailable
}

type testSigner struct{}

func (testSigner) SignRequest(context.Context, []byte) (string, string, error) {
	return "", "", ErrUnavailable
}
func (testSigner) VerifyRequest(context.Context, string, string, []byte) (bool, error) {
	return false, ErrUnavailable
}
func TestDefinitionPublisherRequiresDependenciesAndRejectsBeforeStorage(t *testing.T) {
	store := &testStore{}
	access := &testAccess{allow: true}
	deps := PublisherDependencies{Store: store, Access: access, Signer: testSigner{}, NewID: func(prefix string) (string, error) { return prefix + "_test", nil }}
	for _, field := range []string{"store", "access", "signer", "id"} {
		bad := deps
		switch field {
		case "store":
			bad.Store = nil
		case "access":
			bad.Access = nil
		case "signer":
			bad.Signer = nil
		case "id":
			bad.NewID = nil
		}
		if _, err := NewPublisher(bad); !errors.Is(err, ErrUnavailable) {
			t.Fatal(field, err)
		}
	}
	publisher, err := NewPublisher(deps)
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{TenantID: "tenant-a", UserID: "owner-a"}
	input := PublishInput{Definition: domain.DefaultSessionDefinition()}
	access.err = errors.New("PRIVATE_AUTH_ERROR")
	if out, err := publisher.Publish(context.Background(), actor, domain.Session, "session-a", "key", input); err != ErrUnavailable || out != (PublishResult{}) {
		t.Fatal(out, err)
	}
	access.err = nil
	access.allow = false
	if _, err := publisher.Publish(context.Background(), actor, domain.Session, "session-a", "key", input); err != ErrPermissionDenied {
		t.Fatal(err)
	}
	access.allow = true
	for _, kind := range []string{"tenant", "id", "key", "kind", "revision", "definition"} {
		a, in := actor, input
		k, id, key := domain.Session, "session-a", "key"
		switch kind {
		case "tenant":
			a.TenantID = ""
		case "id":
			id = "bad id"
		case "key":
			key = ""
		case "kind":
			k = "unknown"
		case "revision":
			in.ExpectedRevision = domain.MaxRevision
		case "definition":
			in.Definition = domain.Definition{}
		}
		if _, err = publisher.Publish(context.Background(), a, k, id, key, in); err == nil {
			t.Fatal(kind)
		}
	}
	if store.calls != 0 {
		t.Fatal("rejected request reached DB")
	}
}
