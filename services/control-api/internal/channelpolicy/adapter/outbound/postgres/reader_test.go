package postgresadapter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

type readerDB struct {
	row   readerRow
	args  []any
	calls int
}

func (d *readerDB) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	d.calls++
	d.args = args
	return d.row
}

type readerRow struct {
	raw    []byte
	digest string
	err    error
}

func (r readerRow) Scan(out ...any) error {
	if r.err != nil {
		return r.err
	}
	*out[0].(*[]byte) = r.raw
	*out[1].(*string) = r.digest
	return nil
}
func TestOwnerReaderExactScopeAndSanitizedErrors(t *testing.T) {
	doc, err := domain.NewRevision("tenant-a", "session-a", "owner-a", domain.Session, 1, domain.DefaultSessionDefinition(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	db := &readerDB{row: readerRow{raw: raw, digest: doc.Digest}}
	reader, err := NewReader(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ReadExact(context.Background(), doc.TenantID, domain.Session, doc.PolicyID, 1); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(db.args, []any{doc.TenantID, domain.Session, doc.PolicyID, int64(1)}) {
		t.Fatal("query lost exact reference", db.args)
	}
	if _, err = reader.ReadExact(context.Background(), "tenant-b", domain.Session, doc.PolicyID, 1); !errors.Is(err, application.ErrIntegrity) {
		t.Fatal("cross tenant row trusted", err)
	}
	db.row.err = errors.New("PRIVATE_DATABASE_ERROR")
	if _, err = reader.ReadExact(context.Background(), doc.TenantID, domain.Session, doc.PolicyID, 1); err != application.ErrUnavailable {
		t.Fatal("raw database error exposed", err)
	}
	db.row.err = pgx.ErrNoRows
	if _, err = reader.ReadExact(context.Background(), doc.TenantID, domain.Session, doc.PolicyID, 1); err != application.ErrNotFound {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	db.row.err = errors.New("private cancel detail")
	if _, err = reader.ReadExact(ctx, doc.TenantID, domain.Session, doc.PolicyID, 1); err != context.Canceled {
		t.Fatal(err)
	}
	calls := db.calls
	if _, err = reader.ReadExact(context.Background(), "bad tenant", domain.Session, doc.PolicyID, 1); err != application.ErrNotFound || db.calls != calls {
		t.Fatal("invalid lookup touched DB", err)
	}
	if _, err = NewReader(nil); err != application.ErrUnavailable {
		t.Fatal(err)
	}
}
