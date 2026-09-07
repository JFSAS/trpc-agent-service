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
			var body struct {
				URL    string `json:"url"`
				Secret string `json:"secret_token"`
				Drop   *bool  `json:"drop_pending_updates"`
			}
			if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid explicit protocol request")
			}
			if body.URL != "https://ingress.example/v1/telegram/account" || body.Secret != "joint_webhook_secret_fixture" || body.Drop == nil || *body.Drop {
				t.Error("registration context or pending-update policy changed")
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

// The Worker configurable API origin must also route explicit polling calls,
// without restoring the SDK-owned update loop or dropping unknown Update data.
func TestExplicitBotAPIOriginRetainsPollingAndWebhookRemoval(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/bot987654:synthetic-joint-fixture/getUpdates":
			var body struct {
				Offset  int64 `json:"offset"`
				Limit   int   `json:"limit"`
				Timeout int   `json:"timeout"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Offset != 42 || body.Limit != 100 || body.Timeout != 3 {
				t.Error("durable polling parameters changed")
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":42,"future":{"preserved":true}}]}`))
		case "/bot987654:synthetic-joint-fixture/deleteWebhook":
			var body struct {
				Drop *bool `json:"drop_pending_updates"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Drop == nil || *body.Drop {
				t.Error("pending updates discarded")
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			t.Error("unexpected API operation")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := (Factory{ServerURL: server.URL}).New("987654:synthetic-joint-fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if calls != 0 {
		t.Fatal("constructor performed network I/O")
	}
	updates, err := client.Poll(context.Background(), 42, 3)
	if err != nil || len(updates) != 1 || !strings.Contains(string(updates[0]), `"future":{"preserved":true}`) {
		t.Fatalf("raw update changed: %v", err)
	}
	if err = client.DeleteWebhook(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}
