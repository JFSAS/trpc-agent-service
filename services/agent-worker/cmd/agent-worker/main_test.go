package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadinessProbeAcceptsPositionalLocalURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			t.Error("path")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	var out bytes.Buffer
	if err := run([]string{"probe", server.URL + "/readyz"}, &out); err != nil || out.String() != "WORKER_READY=PASS\n" {
		t.Fatalf("%s %v", out.String(), err)
	}
}
func TestProbeFailureAndPreparationRequireExplicitInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	if run([]string{"probe", "--url=" + server.URL + "/readyz"}, &bytes.Buffer{}) == nil {
		t.Fatal("unready probe accepted")
	}
	t.Setenv("SESSION_MIGRATION_DATABASE_URL", "")
	if run([]string{"prepare-session"}, &bytes.Buffer{}) == nil {
		t.Fatal("runtime used implicit migration URL")
	}
	if run([]string{"probe", "http://remote.invalid/readyz"}, &bytes.Buffer{}) == nil {
		t.Fatal("remote probe accepted")
	}
}
