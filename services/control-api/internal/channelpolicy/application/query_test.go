package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

type queryReader struct {
	doc    domain.Revision
	err    error
	calls  int
	tenant string
}

func (r *queryReader) ReadExact(_ context.Context, t string, _ domain.Kind, _ string, _ int64) (domain.Revision, error) {
	r.calls++
	r.tenant = t
	return r.doc, r.err
}
func TestDefinitionQueryRequiresCurrentOwnerAndExactIntegrity(t *testing.T) {
	ctx := context.Background()
	actor := Actor{TenantID: "tnt_a", UserID: "usr_a"}
	doc, err := domain.NewRevision(actor.TenantID, "policy-a", actor.UserID, domain.Session, 1, domain.DefaultSessionDefinition(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reader := &queryReader{doc: doc}
	access := &testAccess{allow: true}
	if _, err = NewQueryService(nil, access); err != ErrUnavailable {
		t.Fatal(err)
	}
	if _, err = NewQueryService(reader, nil); err != ErrUnavailable {
		t.Fatal(err)
	}
	q, _ := NewQueryService(reader, access)
	out, err := q.ReadExact(ctx, actor, domain.Session, "policy-a", 1)
	if err != nil || out.Digest != doc.Digest || reader.tenant != actor.TenantID {
		t.Fatal(out, err)
	}
	access.allow = false
	before := reader.calls
	if out, err = q.ReadExact(ctx, actor, domain.Session, "policy-a", 1); err != ErrPermissionDenied || out.Digest != "" || reader.calls != before {
		t.Fatal(out, err)
	}
	access.allow = true
	for _, change := range []string{"tenant", "kind", "id", "revision", "digest"} {
		reader.doc = doc
		switch change {
		case "tenant":
			reader.doc.TenantID = "tnt_other"
		case "kind":
			reader.doc.Kind = domain.Quota
		case "id":
			reader.doc.PolicyID = "other"
		case "revision":
			reader.doc.Revision = 2
		case "digest":
			reader.doc.Digest = "sha256:bad"
		}
		if out, err = q.ReadExact(ctx, actor, domain.Session, "policy-a", 1); err != ErrIntegrity || out.Digest != "" {
			t.Fatal(change, out, err)
		}
	}
	reader.doc = doc
	for _, e := range []error{ErrNotFound, ErrIntegrity, errors.New("PRIVATE_STORAGE_ERROR")} {
		reader.err = e
		_, err = q.ReadExact(ctx, actor, domain.Session, "policy-a", 1)
		want := e
		if e != ErrNotFound && e != ErrIntegrity {
			want = ErrUnavailable
		}
		if err != want {
			t.Fatal(err)
		}
	}
}
