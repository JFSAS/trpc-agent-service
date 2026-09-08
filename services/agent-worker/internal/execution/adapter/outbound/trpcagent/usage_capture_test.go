package trpcagent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecutorActualSSEUsageProvenance(t *testing.T) {
	good := `{"prompt_tokens":37,"completion_tokens":11,"total_tokens":48}`
	zero := `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`
	for _, tc := range []struct {
		name    string
		reports []string
		done    bool
		known   bool
		want    Usage
	}{
		{"missing", nil, true, false, Usage{}},
		{"explicit_zero", []string{zero}, true, true, Usage{}},
		{"usage_only_chunk", []string{good}, true, true, Usage{37, 11, 48}},
		{"duplicate_cumulative", []string{good, good}, true, true, Usage{37, 11, 48}},
		{"conflicting", []string{good, `{"prompt_tokens":37,"completion_tokens":12,"total_tokens":49}`}, true, false, Usage{}},
		{"incomplete", []string{`{"total_tokens":48}`}, true, false, Usage{}},
		{"negative", []string{`{"prompt_tokens":-1,"completion_tokens":2,"total_tokens":1}`}, true, false, Usage{}},
		{"inconsistent", []string{`{"prompt_tokens":37,"completion_tokens":11,"total_tokens":1}`}, true, false, Usage{}},
		{"fractional", []string{`{"prompt_tokens":1.5,"completion_tokens":2,"total_tokens":3.5}`}, true, false, Usage{}},
		{"null", []string{"null"}, true, false, Usage{}},
		{"missing_done", []string{good}, false, false, Usage{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				fmt.Fprint(w, "data: {\"id\":\"usage-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"answer\"},\"finish_reason\":null}]}\n\n")
				fmt.Fprint(w, "data: {\"id\":\"usage-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
				for _, usage := range tc.reports {
					fmt.Fprintf(w, "data: {\"id\":\"usage-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture-model\",\"choices\":[],\"usage\":%s}\n\n", usage)
				}
				if tc.done {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer server.Close()
			result, err := testExecutor().Execute(context.Background(), testRequest(server.URL))
			// A structurally invalid provider chunk may be rejected by the pinned SDK
			// itself. Either way it must never produce a verified usage value.
			if err != nil {
				if tc.name == "fractional" {
					return
				}
				t.Fatal(err)
			}
			if result.FinalText != "answer" || result.UsageKnown != tc.known || result.Usage != tc.want {
				t.Fatalf("known=%v usage=%+v text=%q", result.UsageKnown, result.Usage, result.FinalText)
			}
		})
	}
}

type byteReader struct{ io.Reader }

func (r byteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}
func TestUsageCaptureIsTransparentAndBounded(t *testing.T) {
	good := "data: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\"}]}\n\n" + "data: {\"usage\":\r\ndata: {\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\r\n\r\ndata: [DONE]"
	for _, tc := range []struct {
		name, data string
		known      bool
	}{
		{"fragmented_multiline_eof", good, true},
		{"oversized", "data: " + strings.Repeat("x", maxUsageEventBytes+1) + "\n\n" + good, false},
		{"overflow", `data: {"usage":{"prompt_tokens":9007199254740992,"completion_tokens":0,"total_tokens":9007199254740992}}` + "\n\ndata: [DONE]\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &usageCapture{}
			capture.start()
			body := &usageBody{ReadCloser: io.NopCloser(byteReader{strings.NewReader(tc.data)}), capture: capture}
			raw, err := io.ReadAll(body)
			if err != nil || string(raw) != tc.data {
				t.Fatal("observer changed body", err)
			}
			value, known := capture.result()
			if known != tc.known {
				t.Fatal(value, known)
			}
			if len(body.line) > maxUsageEventBytes || len(body.event) > maxUsageEventBytes {
				t.Fatal("unbounded retained event")
			}
		})
	}
	c := &usageCapture{}
	c.start()
	c.event([]byte(`{"choices":[{"index":0,"finish_reason":"stop"}]}`))
	c.event([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`))
	c.event([]byte("[DONE]"))
	c.start()
	if _, known := c.result(); known {
		t.Fatal("multiple requests treated as one known call")
	}
}

func TestUsageCaptureRequiresFinalSameResponseReport(t *testing.T) {
	for _, mode := range []string{"early_usage", "mixed_response", "wrong_choice"} {
		t.Run(mode, func(t *testing.T) {
			c := &usageCapture{}
			c.start()
			first := `{"id":"a","choices":[{"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
			switch mode {
			case "early_usage":
				first = `{"id":"a","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
			case "wrong_choice":
				first = `{"id":"a","choices":[{"index":1,"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
			}
			c.event([]byte(first))
			if mode == "mixed_response" {
				c.event([]byte(`{"id":"b","choices":[]}`))
			}
			c.event([]byte("[DONE]"))
			if _, known := c.result(); known {
				t.Fatal("non-final or substituted report accepted")
			}
		})
	}
}
