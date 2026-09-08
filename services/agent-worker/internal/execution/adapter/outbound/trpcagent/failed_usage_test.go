package trpcagent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A complete report is accounting evidence even when the empty final is rejected.
// Incomplete or conflicting evidence must stay unknown on the same failure path.
func TestFailedFinalPreservesOnlyCompleteUsage(t *testing.T) {
	for _, tc := range []struct {
		name, usage string
		done, known bool
	}{
		{"reported", `{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}`, true, true},
		{"zero", `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, true, true},
		{"missing", "", true, false},
		{"incomplete", `{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":\"stop\"}]}\n\n")
				if tc.usage != "" {
					fmt.Fprintf(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[],\"usage\":%s}\n\n", tc.usage)
				}
				if tc.done {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer server.Close()
			result, err := testExecutor().Execute(context.Background(), testRequest(server.URL))
			if !errors.Is(err, ErrFinal) || result.UsageKnown != tc.known || result.FinalText != "" || result.Snapshot != nil {
				t.Fatalf("known=%v usage=%+v err=%v", result.UsageKnown, result.Usage, err)
			}
			if tc.name == "reported" && result.Usage != (Usage{2, 3, 5}) {
				t.Fatal(result.Usage)
			}
			if !tc.known && result.Usage != (Usage{}) {
				t.Fatal("unknown report retained counters")
			}
		})
	}
}
