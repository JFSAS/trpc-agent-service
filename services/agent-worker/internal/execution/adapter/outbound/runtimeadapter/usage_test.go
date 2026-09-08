package runtimeadapter

import (
	"context"
	"errors"
	"fmt"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/application"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/trpcagent"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

// Exercises actual AttemptRuntime -> pinned SDK -> local HTTP -> RuntimeResult.
// The credential check/loaded history are fixtures, not a production credential test.
func TestRuntimePreservesActualUsageKnownState(t *testing.T) {
	for _, tc := range []struct {
		name, usage string
		known       bool
		total       int64
	}{
		{"missing", "", false, 0},
		{"zero", `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, true, 0},
		{"reported", `{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}`, true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"answer\"},\"finish_reason\":null}]}\n\n")
				fmt.Fprint(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
				if tc.usage != "" {
					fmt.Fprintf(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[],\"usage\":%s}\n\n", tc.usage)
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			a := &attempt{plan: domain.Plan{TenantID: "tenant", NodeID: "root", ModelName: "fixture", ModelEndpoint: server.URL + "/v1", MaxOutputTokens: 100}, grant: domain.Grant{AttemptID: "attempt", Run: domain.Run{SessionID: "session", Request: domain.Requested{RunID: "run", Input: domain.Input{Text: "hello"}}}}, loaded: true, loadedDigest: domain.Digest(nil), check: func(context.Context) error { return nil }, executor: trpcagent.Executor{CapacityBytes: 1024 * 1024, DrainTimeout: time.Second}}
			result, err := a.Execute(context.Background(), nil)
			if err != nil || result.UsageKnown != tc.known || result.TotalTokens != tc.total || result.FinalText != "answer" {
				t.Fatal(result.UsageKnown, result.TotalTokens, result.FinalText, err)
			}
		})
	}
}

func TestRuntimeFailurePreservesUsageAndForbidsStage(t *testing.T) {
	for _, tc := range []struct {
		name, usage string
		known       bool
		total       int64
	}{
		{"missing", "", false, 0},
		{"zero", `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, true, 0},
		{"reported", `{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}`, true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n")
				fmt.Fprint(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
				if tc.usage != "" {
					fmt.Fprintf(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[],\"usage\":%s}\n\n", tc.usage)
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			a := &attempt{plan: domain.Plan{TenantID: "tenant", NodeID: "root", ModelName: "fixture", ModelEndpoint: server.URL + "/v1", MaxOutputTokens: 100}, grant: domain.Grant{AttemptID: "attempt", Run: domain.Run{SessionID: "session", Request: domain.Requested{RunID: "run", Input: domain.Input{Text: "hello"}}}}, loaded: true, loadedDigest: domain.Digest(nil), check: func(context.Context) error { return nil }, executor: trpcagent.Executor{CapacityBytes: 1024 * 1024, DrainTimeout: time.Second}}
			result, err := a.Execute(context.Background(), nil)
			if !errors.Is(err, application.ErrRuntimeFailed) || result.UsageKnown != tc.known || result.TotalTokens != tc.total || result.FinalText != "" || result.Snapshot != nil {
				t.Fatal(result.UsageKnown, result.TotalTokens, result.FinalText, err)
			}
			if _, stageErr := a.Stage(context.Background(), nil); !errors.Is(stageErr, application.ErrRuntimeFailed) {
				t.Fatal("failed result staged", stageErr)
			}
		})
	}
}

func TestGovernedRuntimeUsesActualSDKGate(t *testing.T) {
	for _, tc := range []struct {
		name, usage string
		known       bool
		total       int64
	}{
		{"missing", "", false, 0},
		{"zero", `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, true, 0},
		{"reported", `{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}`, true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var admitted atomic.Bool
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if !admitted.Load() {
					t.Error("model sent before admission")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"answer\"},\"finish_reason\":null}]}\n\n")
				fmt.Fprint(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
				if tc.usage != "" {
					fmt.Fprintf(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[],\"usage\":%s}\n\n", tc.usage)
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			a := &attempt{plan: domain.Plan{TenantID: "tenant", NodeID: "root", ModelName: "fixture", ModelEndpoint: server.URL + "/v1", MaxOutputTokens: 100}, grant: domain.Grant{AttemptID: "attempt", Run: domain.Run{SessionID: "session", Request: domain.Requested{RunID: "run", Input: domain.Input{Text: "hello"}}}}, loaded: true, loadedDigest: domain.Digest(nil), check: func(context.Context) error { return nil }, executor: trpcagent.Executor{CapacityBytes: 1024 * 1024, DrainTimeout: time.Second}}
			a.grant.Run.Request.Authorization = &domain.AdmissionAuthorization{}
			if _, err := a.Execute(context.Background(), nil); !errors.Is(err, domain.ErrNotReady) {
				t.Fatal("managed legacy bypass", err)
			}
			if _, err := a.ExecuteGuarded(context.Background(), nil, nil); !errors.Is(err, domain.ErrNotReady) {
				t.Fatal("missing callback bypass", err)
			}
			if calls.Load() != 0 {
				t.Fatal("unadmitted request sent")
			}
			result, err := a.ExecuteGuarded(context.Background(), nil, func(ctx context.Context, call domain.ModelCall) error {
				if call.RequestDigest != domain.Digest(call.Body) || call.MaxOutputTokens != 100 || call.AttemptID != a.grant.AttemptID {
					t.Error("actual call lost")
				}
				admitted.Store(true)
				return nil
			})
			if calls.Load() != 1 || err != nil || result.UsageKnown != tc.known || result.TotalTokens != tc.total || result.FinalText != "answer" {
				t.Fatal(result.UsageKnown, result.TotalTokens, result.FinalText, err)
			}
		})
	}
}
