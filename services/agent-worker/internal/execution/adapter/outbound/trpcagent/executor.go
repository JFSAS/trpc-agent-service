// Package trpcagent adapts one fixed Worker V1 LLM to a pinned SDK Runner.
// Execution retains credential authorization, candidate durability and Completion.
package trpcagent

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	replycodec "github.com/liuzengh/trpc-agent-service/api/events/execution/v1"
	replywire "github.com/liuzengh/trpc-agent-service/gen/events/execution/v1"
	openaiapi "github.com/openai/openai-go"
	openaioption "github.com/openai/openai-go/option"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const SDKVersion = "v1.11.2"

var ErrModel = errors.New("model execution failed")
var ErrRetryableModel = errors.New("model dependency temporarily unavailable")
var ErrFinal = errors.New("model returned no valid complete text final")
var ErrDrain = errors.New("SDK cancellation drain timeout")

type Model struct {
	Endpoint    string
	Name        string
	APIKey      string
	Temperature *float64
	// Nil uses the published per-response MaxOutputTokens cap, never a private quota.
	MaxOutputTokens *int64
}
type Request struct {
	TenantID, SessionID, RunID, AttemptID string
	NodeID, Instruction, InputText        string
	Model                                 Model
	MaxOutputTokens                       int64
	AcceptedSnapshot                      []byte
}
type Usage struct{ InputTokens, OutputTokens, TotalTokens int }
type Result struct {
	FinalText  string
	Snapshot   []byte
	Usage      Usage
	UsageKnown bool
}

type Executor struct {
	// BeforeModel inspects the actual outbound SDK request once before network I/O.
	// Nil retains legacy execution; this seam alone does not enable managed budgets.
	BeforeModel   ModelCallGate
	CapacityBytes int
	DrainTimeout  time.Duration
	beforeAppend  func(*event.Event) error
}

func (e Executor) Execute(ctx context.Context, req Request) (result Result, err error) {
	if e.CapacityBytes <= 0 || e.DrainTimeout <= 0 {
		return result, errors.New("positive snapshot capacity and drain timeout required")
	}
	if req.TenantID == "" || req.SessionID == "" || req.RunID == "" || req.AttemptID == "" || req.NodeID == "" || strings.TrimSpace(req.InputText) == "" || req.Model.Name == "" {
		return result, errors.New("invalid single LLM request")
	}
	endpoint, parseErr := url.Parse(req.Model.Endpoint)
	if parseErr != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return result, errors.New("invalid model endpoint")
	}
	maxTokens := req.MaxOutputTokens
	if req.Model.MaxOutputTokens != nil {
		maxTokens = *req.Model.MaxOutputTokens
	}
	// The node generation schema has its own ceiling, validated with the full
	// Manifest before this adapter. It is not a ceiling on the published
	// execution.max_output_tokens. Only guard the effective value and the SDK's
	// native-int representation here; never add a private model-token policy.
	if maxTokens <= 0 || maxTokens > req.MaxOutputTokens || int64(int(maxTokens)) != maxTokens {
		return result, errors.New("invalid published per-response output limit")
	}
	if req.Model.Temperature != nil && (*req.Model.Temperature < 0 || *req.Model.Temperature > 2) {
		return result, errors.New("invalid temperature")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	local, err := newOverlay(req.TenantID, req.SessionID, req.AcceptedSnapshot, e.CapacityBytes)
	if err != nil {
		return result, err
	}
	local.beforeAppend = e.beforeAppend
	// Owned transport closes in every return path; no model client carries credentials
	// beyond this attempt. Retries are explicitly zero; Execution owns retry policy.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	httpState := &modelTransport{base: transport}
	if e.BeforeModel != nil {
		httpState.gate = &dispatchGate{owner: e.BeforeModel, request: req, maximum: maxTokens}
	}
	// Registered before runner cleanup so this runs after Close. A failed result
	// carries accounting only, never a publishable final or candidate snapshot.
	defer func() {
		if err != nil {
			result = Result{}
		}
		// A drain timeout can leave SDK work in flight. A cancelled grant cannot
		// authorize persistence; leave its maximum held instead of taking a snapshot
		// of potentially changing evidence.
		if ctx.Err() == nil && !errors.Is(err, ErrDrain) {
			result.Usage, result.UsageKnown = httpState.usage.result()
		}
	}()
	max := int(maxTokens)
	clientOptions := []openaioption.RequestOption{openaioption.WithMaxRetries(0), openaioption.WithAPIKey(req.Model.APIKey), openaioption.WithOrganization(""), openaioption.WithProject(""), openaioption.WithHTTPClient(&http.Client{Transport: httpState})}
	if req.Model.APIKey == "" {
		clientOptions = append(clientOptions, openaioption.WithHeaderDel("Authorization"))
	}
	m := openai.New(req.Model.Name, openai.WithAPIKey(req.Model.APIKey), openai.WithVariant(openai.VariantOpenAI), openai.WithOptimizeForCache(false), openai.WithBaseURL(req.Model.Endpoint), openai.WithEnableTokenTailoring(false), openai.WithOpenAIOptions(clientOptions...),
		openai.WithChatRequestCallback(func(_ context.Context, request *openaiapi.ChatCompletionNewParams) {
			// SDK v1.11.2 clamps known model names even with tailoring disabled.
			// Its public typed callback runs after conversion and before send.
			// Preserve the validated Manifest value; the provider may reject it,
			// but neither a private cap nor a lower-limit retry is our contract.
			// Worker V1 sets no extra fields that could override this parameter.
			request.MaxCompletionTokens = openaiapi.Int(maxTokens)
		}))
	a := llmagent.New(req.NodeID, llmagent.WithModel(m), llmagent.WithInstruction(req.Instruction), llmagent.WithGenerationConfig(model.GenerationConfig{MaxTokens: &max, Temperature: req.Model.Temperature, Stream: true}), llmagent.WithEnableCodeExecutionResponseProcessor(false), llmagent.WithCodeExecutor(nil), llmagent.WithPreloadMemory(0), llmagent.WithAddSessionSummary(false), llmagent.WithSyncSummaryIntraRun(false), llmagent.WithMaxHistoryRuns(0), llmagent.WithPreserveSameBranch(true))
	r := runner.NewRunner(local.key.AppName, a, runner.WithSessionService(local), runner.WithMemoryService(nil))
	defer func() {
		if closeErr := r.Close(); err == nil && closeErr != nil {
			result = Result{}
			err = fmt.Errorf("runner close: %w", closeErr)
		}
	}()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := r.Run(runCtx, local.key.UserID, local.key.SessionID, model.NewUserMessage(req.InputText), agent.WithDetachedCancel(false))
	if err != nil {
		return Result{}, fmt.Errorf("%w: SDK run initialization", ErrModel)
	}
	var observed error
	for {
		select {
		case <-ctx.Done():
			cancel()
			if !drain(events, e.DrainTimeout) {
				return Result{}, errors.Join(ctx.Err(), ErrDrain)
			}
			return Result{}, ctx.Err()
		case evt, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					return Result{}, ctx.Err()
				}
				if observed != nil {
					return Result{}, observed
				}
				if err = local.Err(); err != nil {
					return Result{}, err
				}
				if !validFinalText(result.FinalText) {
					return Result{}, ErrFinal
				}
				result.Snapshot, err = local.Snapshot()
				if err != nil {
					return Result{}, err
				}
				return result, nil
			}
			if evt == nil || evt.Response == nil {
				continue
			}
			if evt.Error != nil {
				observed = ErrModel
				if httpState.admissionDenied.Load() {
					observed = ErrModelAdmission
				} else if httpState.retryable() {
					observed = ErrRetryableModel
				}
				cancel()
			}
			for _, choice := range evt.Choices {
				if len(choice.Message.ToolCalls) > 0 || len(choice.Delta.ToolCalls) > 0 {
					observed = ErrFinal
					cancel()
					continue
				}
				if !evt.IsPartial && choice.Message.Role == model.RoleAssistant && len(choice.Message.ContentParts) == 0 && choice.Message.Content != "" {
					result.FinalText = choice.Message.Content
				}
			}
			if observed != nil {
				if !drain(events, e.DrainTimeout) {
					return Result{}, errors.Join(observed, ErrDrain)
				}
				return Result{}, observed
			}
		}
	}
}
func drain(events <-chan *event.Event, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return true
			}
		case <-timer.C:
			return false
		}
	}
}

// Keep transport status separate from provider error text. Retry classification
// never parses, returns or logs an arbitrary model response body.
type modelTransport struct {
	gate            *dispatchGate
	admissionDenied atomic.Bool
	usage           usageCapture
	base            *http.Transport
	code            atomic.Int32
	networkFailure  atomic.Bool
}

func (t *modelTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.gate != nil {
		if err := t.gate.check(r); err != nil {
			t.admissionDenied.Store(true)
			return nil, err
		}
	}
	t.usage.start()
	response, err := t.base.RoundTrip(r)
	if err != nil {
		t.networkFailure.Store(true)
	}
	if response != nil {
		t.code.Store(int32(response.StatusCode))
		media, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if response.StatusCode == http.StatusOK && parseErr == nil && media == "text/event-stream" && response.Body != nil {
			response.Body = &usageBody{ReadCloser: response.Body, capture: &t.usage}
		} else {
			t.usage.reject()
		}
	}
	return response, err
}
func (t *modelTransport) retryable() bool {
	code := t.code.Load()
	return t.networkFailure.Load() || code == 408 || code == 429 || code >= 500
}

// Validate against the existing shared Final codec before writing a candidate.
// This is a reply-protocol boundary, not a new model-token cap or truncation.
func validFinalText(text string) bool {
	if len(text) > replycodec.MaxFinalTextBytes || !utf8.ValidString(text) {
		return false
	}
	_, err := replycodec.EncodeReplyIntent(replywire.ReplyIntent{
		SchemaVersion: 1, IntentID: "validation", AdmissionID: "validation", RunID: "validation",
		Execution: replywire.ReplyExecution{AttemptID: "validation", Generation: 1, CompletionID: "validation"},
		Sequence:  1, Kind: "final", Content: replywire.FinalTextContent{Type: "text", Text: text}, Deadline: "2000-01-01T00:00:00Z",
	})
	return err == nil
}
