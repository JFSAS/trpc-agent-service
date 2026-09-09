package workerhttp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPagedRequestsKeepPathAndQuerySeparate(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if r.Method != http.MethodGet || r.URL.Query().Get("offset") != "7" || r.URL.Query().Get("limit") != "19" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		seen[key] = true
		w.Header().Set("Content-Type", "application/json")
		switch key {
		case "/internal/v1/management/tenants/tenant-a/runs":
			_, _ = fmt.Fprint(w, `{"runs":[],"offset":7,"limit":19,"total":0}`)
		case "/internal/v1/management/tenants/tenant-a/audit-events":
			_, _ = fmt.Fprint(w, `{"events":[],"offset":7,"limit":19,"total":0}`)
		default:
			t.Errorf("unexpected escaped path %q; raw query %q", key, r.URL.RawQuery)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ListRuns(context.Background(), "tenant-a", 7, 19); err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if _, err = client.ListAudit(context.Background(), "tenant-a", 7, 19); err != nil {
		t.Fatalf("list audit: %v", err)
	}
	for _, path := range []string{
		"/internal/v1/management/tenants/tenant-a/runs",
		"/internal/v1/management/tenants/tenant-a/audit-events",
	} {
		if !seen[path] {
			t.Errorf("route not reached: %s", path)
		}
	}
}
