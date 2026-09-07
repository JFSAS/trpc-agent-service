package telegramregistration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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
			var params struct {
				URL    string `json:"url"`
				Secret string `json:"secret_token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
				t.Error(err)
			}
			if params.URL != "https://ingress.example/v1/telegram/account" || params.Secret != "joint_webhook_secret_fixture" {
				t.Error("registration context changed")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
		default:
			t.Error("unexpected protocol operation")
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

func TestFactoryCustomOriginPreservesReceiveModes(t *testing.T) {
	const token = "123456:synthetic-fixture"
	calls := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/bot"+token+"/") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		method := strings.TrimPrefix(r.URL.Path, "/bot"+token+"/")
		calls <- method
		var params map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			t.Errorf("decode params: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getMe":
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true}}`)
		case "getWebhookInfo":
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
		case "setWebhook", "deleteWebhook":
			if string(params["drop_pending_updates"]) != "false" {
				t.Errorf("%s must preserve pending updates: %s", method, params["drop_pending_updates"])
			}
			fmt.Fprint(w, `{"ok":true,"result":true}`)
		case "getUpdates":
			if string(params["offset"]) != "17" {
				t.Errorf("poll offset: %s", params["offset"])
			}
			fmt.Fprint(w, `{"ok":true,"result":[{"update_id":17}]}`)
		default:
			t.Errorf("unexpected method: %s", method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	remote, err := (Factory{ServerURL: server.URL + "/"}).New(token)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	if len(calls) != 0 {
		t.Fatal("constructor made a Telegram request")
	}
	ctx := context.Background()
	if id, err := remote.Identity(ctx); err != nil || id != "123456" {
		t.Fatalf("identity=%q err=%v", id, err)
	}
	if url, err := remote.Webhook(ctx); err != nil || url != "" {
		t.Fatalf("webhook=%q err=%v", url, err)
	}
	if ok, err := remote.Register(ctx, "https://gateway.example/webhook", "synthetic-secret"); err != nil || !ok {
		t.Fatalf("register=%v err=%v", ok, err)
	}
	if err := remote.DeleteWebhook(ctx); err != nil {
		t.Fatal(err)
	}
	if updates, err := remote.Poll(ctx, 17, 1); err != nil || len(updates) != 1 {
		t.Fatalf("updates=%v err=%v", updates, err)
	}
	want := []string{"getMe", "getWebhookInfo", "setWebhook", "deleteWebhook", "getUpdates"}
	var got []string
	for len(calls) > 0 {
		got = append(got, <-calls)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls=%v want=%v", got, want)
	}
}
