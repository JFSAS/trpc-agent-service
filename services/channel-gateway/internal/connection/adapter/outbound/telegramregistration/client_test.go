package telegramregistration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestExplicitBotAPIOriginRetainsIdentityAndRegistration(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/bot987654:synthetic-joint-fixture/getMe":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":987654,"is_bot":true,"first_name":"fixture"}}`))
		case "/bot987654:synthetic-joint-fixture/setWebhook":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			if r.FormValue("url") != "https://ingress.example/v1/telegram/account" || r.FormValue("secret_token") != "joint_webhook_secret_fixture" {
				t.Error("registration context changed")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
		default:
			t.Error("unexpected SDK operation")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := (Factory{ServerURL: server.URL + "/"}).New("987654:synthetic-joint-fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	id, err := client.Identity(context.Background())
	if err != nil || id != "987654" {
		t.Fatalf("identity %q %v", id, err)
	}
	ok, err := client.Register(context.Background(), "https://ingress.example/v1/telegram/account", "joint_webhook_secret_fixture")
	if !ok || err != nil {
		t.Fatalf("registration %v %v", ok, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || !strings.HasSuffix(calls[0], "/getMe") || !strings.HasSuffix(calls[1], "/setWebhook") {
		t.Fatalf("calls %v", calls)
	}
}
