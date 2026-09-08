package trpcagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type modelGateFunc func(context.Context, ModelCall) error

func (f modelGateFunc) AuthorizeModelCall(c context.Context, m ModelCall) error { return f(c, m) }
func TestActualSDKDispatchGate(t *testing.T) {
	for _, mode := range []string{"allow", "deny", "mutate", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var calls, checks atomic.Int32
			var inspected []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				if !bytes.Equal(body, inspected) {
					t.Error("wire differs from admitted bytes")
				}
				if mode == "redirect" {
					w.Header().Set("Location", "/v1/chat/completions")
					w.WriteHeader(http.StatusTemporaryRedirect)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			req := testRequest(server.URL)
			executor := testExecutor()
			executor.BeforeModel = modelGateFunc(func(ctx context.Context, c ModelCall) error {
				checks.Add(1)
				if calls.Load() != 0 {
					t.Error("gate ran after network")
				}
				hash := sha256.Sum256(c.Body)
				if c.RequestDigest != "sha256:"+hex.EncodeToString(hash[:]) || c.TenantID != req.TenantID || c.RunID != req.RunID || c.AttemptID != req.AttemptID || c.Model != req.Model.Name {
					t.Error("wrong exact identity")
				}
				var body map[string]any
				if json.Unmarshal(c.Body, &body) != nil || body["max_completion_tokens"] != float64(c.MaxOutputTokens) || len(body["messages"].([]any)) == 0 {
					t.Error("missing effective request")
				}
				inspected = bytes.Clone(c.Body)
				if mode == "deny" {
					return errors.New("PRIVATE_POLICY_DETAIL")
				}
				if mode == "mutate" {
					c.Body[0] = 'x'
				}
				return nil
			})
			result, err := executor.Execute(context.Background(), req)
			if checks.Load() != 1 {
				t.Fatal("gate not exactly once", checks.Load())
			}
			want := int32(1)
			if mode == "deny" || mode == "mutate" {
				want = 0
			}
			if calls.Load() != want {
				t.Fatal("unexpected physical model calls", calls.Load(), want)
			}
			if mode == "allow" {
				if err != nil || result.FinalText != "answer" {
					t.Fatal(err)
				}
			} else {
				if err == nil || strings.Contains(err.Error(), "PRIVATE_POLICY_DETAIL") || result.FinalText != "" || result.Snapshot != nil {
					t.Fatal("denied result escaped", err)
				}
			}
		})
	}
}

func TestDispatchGateRejectsUnboundedOrCancelledRequests(t *testing.T) {
	for _, mode := range []string{"oversized", "wrong_model", "wrong_limit", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			var calls int
			req := testRequest("http://127.0.0.1:1")
			gate := &dispatchGate{request: req, maximum: 10, owner: modelGateFunc(func(context.Context, ModelCall) error { calls++; return nil })}
			body := `{"model":"` + req.Model.Name + `","max_completion_tokens":10,"messages":[]}`
			switch mode {
			case "oversized":
				body = strings.Repeat("x", maxModelRequestBytes+1)
			case "wrong_model":
				body = `{"model":"other","max_completion_tokens":10}`
			case "wrong_limit":
				body = `{"model":"` + req.Model.Name + `","max_completion_tokens":11}`
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			r, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(req.Model.Endpoint, "/")+"/chat/completions", strings.NewReader(body))
			if err := gate.check(r); !errors.Is(err, ErrModelAdmission) {
				t.Fatal(err)
			}
			if r.Body != nil {
				r.Body.Close()
			}
			if calls != 0 {
				t.Fatal("invalid request reached authority", calls)
			}
		})
	}
}
