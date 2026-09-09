// Package trpcagent adapts one fixed Worker V1 LLM to a pinned SDK Runner.
// Execution retains credential authorization, candidate durability and Completion.
package trpcagent

import (
	"context"
	"errors"
	"fmt"
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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
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

// SummaryConfig contains only the fixed, authorized manifest selection.
type SummaryConfig struct {
	Model             Model
	EventThreshold    int64
	AddSessionSummary bool
}
type MemoryConfig struct {
	BoundKey     memory.UserKey
	Entries      []*memory.Entry
	BaseRevision uint64
	Tools        []string
	PreloadLimit int
}
type Request struct {
	Artifact                              *ArtifactConfig
	Memory                                *MemoryConfig
	MaxToolCalls                          int64
	Summary                               *SummaryConfig
	TenantID, SessionID, RunID, AttemptID string
	NodeID, Instruction, InputText        string
	Model                                 Model
	MaxOutputTokens                       int64
	AcceptedSnapshot                      []byte
}
type Usage struct{ InputTokens, OutputTokens, TotalTokens int }
type Result struct {
	Memory    *MemoryCandidate
	FinalText string
	Snapshot  []byte
	Usage     Usage
}

type Executor struct {
	Tracer        trace.Tracer
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
	if req.Summary != nil {
		sm := req.Summary.Model
		ep, parseErr := url.Parse(sm.Endpoint)
		if parseErr != nil || (ep.Scheme != "http" && ep.Scheme != "https") || ep.Host == "" || ep.User != nil || ep.RawQuery != "" || ep.ForceQuery || strings.Contains(sm.Endpoint, "#") || strings.TrimSpace(sm.Name) == "" || sm.MaxOutputTokens != nil || sm.Temperature != nil || req.Summary.EventThreshold <= 0 || req.Summary.EventThreshold > 9007199254740991 || int64(int(req.Summary.EventThreshold)) != req.Summary.EventThreshold {
			return result, errors.New("invalid fixed summary configuration")
		}
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if e.Tracer != nil {
		var span trace.Span
		ctx, span = e.Tracer.Start(ctx, "worker.runner.run", trace.WithAttributes(
			attribute.String("app.run.id", req.RunID), attribute.String("app.attempt.id", req.AttemptID), attribute.String("app.session.id", req.SessionID)))
		defer func() {
			if err != nil {
				kind := "failed"
				if errors.Is(err, context.Canceled) {
					kind = "cancelled"
				} else if errors.Is(err, context.DeadlineExceeded) {
					kind = "deadline"
				}
				span.SetStatus(codes.Error, "")
				span.SetAttributes(attribute.String("error.type", kind))
			}
			span.End()
		}()
	}
	// Both models own attempt-local transports and use the published per-response
	// output limit. There is no aggregate token reservation or hidden cap.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	httpState := &modelTransport{base: transport}
	m := newFixedModel(req.Model, maxTokens, httpState)
	var summaryModel *summaryUsageModel
	var local *overlay
	if req.Summary == nil {
		// Disabling summary generation does not invalidate the same Session.
		// Retain previously accepted summaries, but do not consume or update them.
		local, err = newOverlayState(req.TenantID, req.SessionID, req.AcceptedSnapshot, e.CapacityBytes, true)
	} else {
		summaryTransport := http.DefaultTransport.(*http.Transport).Clone()
		defer summaryTransport.CloseIdleConnections()
		summaryState := &modelTransport{base: summaryTransport}
		summaryModel = &summaryUsageModel{Model: newFixedModel(req.Summary.Model, req.MaxOutputTokens, summaryState), transport: summaryState}
		summarizer := summary.NewSummarizer(summaryModel, summary.WithEventThreshold(int(req.Summary.EventThreshold)))
		local, err = newSummaryOverlay(req.TenantID, req.SessionID, req.AcceptedSnapshot, e.CapacityBytes, summarizer)
	}
	if err != nil {
		return result, err
	}
	local.beforeAppend = e.beforeAppend
	local.tracer = e.Tracer
	defer func() {
		appends, bytes := local.stats()
		trace.SpanFromContext(ctx).SetAttributes(attribute.Int64("app.session.overlay.appends", appends), attribute.Int64("app.session.overlay.bytes", bytes))
	}()
	max := int(maxTokens)
	agentOptions := []llmagent.Option{llmagent.WithModel(m), llmagent.WithInstruction(req.Instruction), llmagent.WithGenerationConfig(model.GenerationConfig{MaxTokens: &max, Temperature: req.Model.Temperature, Stream: true}), llmagent.WithEnableCodeExecutionResponseProcessor(false), llmagent.WithCodeExecutor(nil), llmagent.WithPreloadMemory(0), llmagent.WithAddSessionSummary(req.Summary != nil && req.Summary.AddSessionSummary), llmagent.WithSyncSummaryIntraRun(false), llmagent.WithMaxHistoryRuns(0), llmagent.WithPreserveSameBranch(true)}
	runnerOptions := []runner.Option{runner.WithSessionService(local), runner.WithMemoryService(nil)}
	var memoryAttempt *MemoryAttempt
	var toolState *memoryToolState
	var artifactState *artifactTools
	cfg := CapabilityConfig{AddSessionSummary: req.Summary != nil && req.Summary.AddSessionSummary}
	services := CapabilityServices{}
	names := []string{}
	if req.Memory != nil {
		if req.Memory.BoundKey.AppName != req.TenantID || req.MaxToolCalls < 1 {
			return Result{}, ErrMemoryScope
		}
		memoryAttempt, err = NewMemoryAttempt(ctx, memory.UserKey{AppName: local.key.AppName, UserID: local.key.UserID}, req.Memory.BoundKey, req.Memory.Entries, req.Memory.BaseRevision)
		if err != nil {
			return Result{}, err
		}
		defer memoryAttempt.Close()
		service, traceErr := TraceMemoryService(memoryAttempt, e.Tracer)
		if traceErr != nil {
			return Result{}, traceErr
		}
		cfg.MemoryTools = req.Memory.Tools
		cfg.MemoryPreloadLimit = req.Memory.PreloadLimit
		services.Memory = service
		names = append(names, req.Memory.Tools...)
	}
	if req.Artifact != nil {
		if req.Artifact.Service == nil || req.Artifact.MaxBytes < 1 || req.MaxToolCalls < 1 {
			return Result{}, ErrArtifact
		}
		artifactState = &artifactTools{tracer: e.Tracer, maxBytes: req.Artifact.MaxBytes}
		cfg.Artifact = true
		services.Artifact = req.Artifact.Service
		services.ArtifactTools = artifactState.tools()
		names = append(names, ArtifactToolNames...)
	}
	if req.Memory != nil || req.Artifact != nil {
		options, optionErr := BuildCapabilityOptions(cfg, services)
		if optionErr != nil {
			return Result{}, optionErr
		}
		toolState = newMemoryToolState(names, req.MaxToolCalls, e.Tracer)
		agentOptions = append(agentOptions, options.Agent...)
		agentOptions = append(agentOptions, llmagent.WithToolCallbacks(toolState.callbacks()))
		runnerOptions = append(runnerOptions, options.Runner...)
	}
	a := llmagent.New(req.NodeID, agentOptions...)
	r := runner.NewRunner(local.key.AppName, a, runnerOptions...)
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
					if summaryModel != nil && summaryModel.err() != nil {
						return Result{}, summaryModel.err()
					}
					return Result{}, err
				}
				if artifactState != nil && artifactState.failed.Load() {
					return Result{}, ErrArtifact
				}
				if toolState != nil && toolState.failed.Load() {
					return Result{}, ErrMemoryTool
				}
				if !validFinalText(result.FinalText) {
					return Result{}, ErrFinal
				}
				result.Snapshot, err = local.Snapshot()
				if err != nil {
					return Result{}, err
				}
				if summaryModel != nil {
					usage := summaryModel.usage()
					result.Usage.InputTokens += usage.InputTokens
					result.Usage.OutputTokens += usage.OutputTokens
					result.Usage.TotalTokens += usage.TotalTokens
				}
				if memoryAttempt != nil {
					candidate, sealErr := memoryAttempt.Seal(ctx)
					if sealErr != nil {
						return Result{}, sealErr
					}
					result.Memory = &candidate
				}
				return result, nil
			}
			if evt == nil || evt.Response == nil {
				continue
			}
			if evt.Error != nil {
				observed = ErrModel
				if httpState.retryable() {
					observed = ErrRetryableModel
				}
				cancel()
			}
			for _, choice := range evt.Choices {
				if !evt.IsPartial && len(choice.Message.ToolCalls) > 0 {
					result.FinalText = ""
					for _, call := range choice.Message.ToolCalls {
						if toolState == nil || !toolState.allowed[call.Function.Name] {
							observed = ErrMemoryTool
							cancel()
						}
					}
				}
				if toolState == nil && (len(choice.Message.ToolCalls) > 0 || len(choice.Delta.ToolCalls) > 0) {
					observed = ErrFinal
					cancel()
					continue
				}
				if !evt.IsPartial && choice.Message.Role == model.RoleAssistant && len(choice.Message.ToolCalls) == 0 && len(choice.Message.ContentParts) == 0 && choice.Message.Content != "" {
					result.FinalText = choice.Message.Content
				}
			}
			if !evt.IsPartial && evt.Usage != nil {
				result.Usage.InputTokens += evt.Usage.PromptTokens
				result.Usage.OutputTokens += evt.Usage.CompletionTokens
				result.Usage.TotalTokens += evt.Usage.TotalTokens
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
	base           *http.Transport
	code           atomic.Int32
	networkFailure atomic.Bool
}

func (t *modelTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err != nil {
		t.networkFailure.Store(true)
	}
	if response != nil {
		t.code.Store(int32(response.StatusCode))
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

func newFixedModel(spec Model, maxTokens int64, httpState *modelTransport) model.Model {
	clientOptions := []openaioption.RequestOption{openaioption.WithMaxRetries(0), openaioption.WithAPIKey(spec.APIKey), openaioption.WithOrganization(""), openaioption.WithProject(""), openaioption.WithHTTPClient(&http.Client{Transport: httpState})}
	if spec.APIKey == "" {
		clientOptions = append(clientOptions, openaioption.WithHeaderDel("Authorization"))
	}
	return openai.New(spec.Name, openai.WithAPIKey(spec.APIKey), openai.WithVariant(openai.VariantOpenAI), openai.WithOptimizeForCache(false), openai.WithBaseURL(spec.Endpoint), openai.WithEnableTokenTailoring(false), openai.WithOpenAIOptions(clientOptions...),
		openai.WithChatRequestCallback(func(_ context.Context, request *openaiapi.ChatCompletionNewParams) {
			// SDK v1.11.2 clamps known model names even with tailoring disabled.
			// Its public typed callback runs after conversion and before send.
			// Preserve the validated Manifest value; the provider may reject it,
			// but neither a private cap nor a lower-limit retry is our contract.
			// Worker V1 sets no extra fields that could override this parameter.
			request.MaxCompletionTokens = openaiapi.Int(maxTokens)
		}))

}
