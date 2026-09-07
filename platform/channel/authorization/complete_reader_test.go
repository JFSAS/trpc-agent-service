package authorization

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
)

type currentFunc func(context.Context, AuthorizationTarget, func(context.Context, wire.AuthorizationSnapshotPage) error) (AuthorizationRead, error)

func (f currentFunc) ReadAuthorization(c context.Context, t AuthorizationTarget, s func(context.Context, wire.AuthorizationSnapshotPage) error) (AuthorizationRead, error) {
	return f(c, t, s)
}

type definitionsFunc func(context.Context, wire.AccessPolicyDocument) (PolicyDependencies, error)

func (f definitionsFunc) ReadPolicyDependencies(c context.Context, p wire.AccessPolicyDocument) (PolicyDependencies, error) {
	return f(c, p)
}
func TestCompleteReaderKeepsOriginalDeadlineAndRejectsPartial(t *testing.T) {
	s, q := dependency(t, "session"), dependency(t, "quota")
	result := AuthorizationRead{StartedAt: time.Now(), ExpiresAt: time.Now().Add(time.Second), Policy: wire.AccessPolicyDocument{TenantID: "tenant", Body: wire.AccessPolicyBody{AccessMode: "ALLOWLIST", SessionPolicy: wire.PolicyReference{ID: s.PolicyID, Revision: s.Revision, Digest: s.Digest}, TenantQuota: wire.PolicyReference{ID: q.PolicyID, Revision: q.Revision, Digest: q.Digest}}}}
	current := currentFunc(func(context.Context, AuthorizationTarget, func(context.Context, wire.AuthorizationSnapshotPage) error) (AuthorizationRead, error) {
		return result, nil
	})
	source := CompleteReader{Current: current, Definitions: definitionsFunc(func(ctx context.Context, p wire.AccessPolicyDocument) (PolicyDependencies, error) {
		d, ok := ctx.Deadline()
		if !ok || !d.Equal(result.ExpiresAt) {
			t.Fatal("original read deadline lost")
		}
		return PolicyDependencies{Session: s, Quota: q}, nil
	})}
	got, e := source.ReadAuthorization(context.Background(), AuthorizationTarget{}, nil)
	if e != nil || got.Dependencies == nil || !got.StartedAt.Equal(result.StartedAt) || !got.ExpiresAt.Equal(result.ExpiresAt) {
		t.Fatal(got, e)
	}
	source.Definitions = definitionsFunc(func(context.Context, wire.AccessPolicyDocument) (PolicyDependencies, error) {
		return PolicyDependencies{Session: s}, nil
	})
	if got, e = source.ReadAuthorization(context.Background(), AuthorizationTarget{}, nil); e == nil || !reflect.DeepEqual(got, AuthorizationRead{}) {
		t.Fatal("partial dependencies returned", e)
	}
	result.ExpiresAt = time.Now().Add(15 * time.Millisecond)
	source.Definitions = definitionsFunc(func(ctx context.Context, _ wire.AccessPolicyDocument) (PolicyDependencies, error) {
		<-ctx.Done()
		return PolicyDependencies{Session: s, Quota: q}, nil
	})
	if got, e = source.ReadAuthorization(context.Background(), AuthorizationTarget{}, nil); !errors.Is(e, ErrDependencyExpired) || !reflect.DeepEqual(got, AuthorizationRead{}) {
		t.Fatal("late success renewed expiry", e)
	}
	result.ExpiresAt = time.Now().Add(time.Second)
	result.Policy.Body.AccessMode = "DENY_ALL"
	source.Definitions = definitionsFunc(func(context.Context, wire.AccessPolicyDocument) (PolicyDependencies, error) {
		t.Fatal("DENY_ALL fetched dependencies")
		return PolicyDependencies{}, nil
	})
	if got, e = source.ReadAuthorization(context.Background(), AuthorizationTarget{}, nil); e != nil || got.Dependencies != nil {
		t.Fatal(got, e)
	}
}
