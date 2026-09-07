package authorizationpostgres

import (
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
)

func TestOnlyCompiledNamespaces(t *testing.T) {
	pool := &pgxpool.Pool{} // constructor performs no I/O
	for _, namespace := range []Namespace{"", "custom", "gateway;DROP TABLE x", "worker.other"} {
		if _, err := New(pool, "scope", "11111111-1111-4111-8111-111111111111", namespace); !errors.Is(err, ErrInvalid) {
			t.Fatal("arbitrary SQL namespace accepted", namespace, err)
		}
	}
	for _, namespace := range []Namespace{Gateway, Worker} {
		s, err := New(pool, "scope", "11111111-1111-4111-8111-111111111111", namespace)
		if err != nil {
			t.Fatal(err)
		}
		sql := s.query("SELECT * FROM gateway_authorization_snapshots WHERE account_id=$1")
		if !strings.Contains(sql, string(namespace)+"_authorization_snapshots") || !strings.HasSuffix(sql, "account_id=$1") {
			t.Fatal(sql)
		}
	}
	if _, err := New(nil, "scope", "11111111-1111-4111-8111-111111111111", Worker); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil pool accepted")
	}
}
