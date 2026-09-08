package trpcagent

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/liuzengh/trpc-agent-service/platform/telemetrytrace"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var ErrMemoryTool = errors.New("memory tool execution failed")

// These callbacks enforce the existing published call budget and observe SDK
// errors. They do not implement tool CRUD, parse arguments, or export contents.
type memoryToolState struct {
	allowed map[string]bool
	limit   int64
	calls   atomic.Int64
	failed  atomic.Bool
	tracer  trace.Tracer
}

func newMemoryToolState(names []string, limit int64, tracer trace.Tracer) *memoryToolState {
	s := &memoryToolState{allowed: make(map[string]bool), limit: limit, tracer: tracer}
	for _, name := range names {
		s.allowed[name] = true
	}
	return s
}
func (s *memoryToolState) callbacks() *tool.Callbacks {
	return &tool.Callbacks{
		BeforeTool: []tool.BeforeToolCallbackStructured{func(ctx context.Context, a *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
			if a == nil || !s.allowed[a.ToolName] || s.calls.Add(1) > s.limit {
				s.failed.Store(true)
				return nil, ErrMemoryTool
			}
			ctx, _ = telemetrytrace.Start(s.tracer, ctx, "worker.tool.call", trace.WithAttributes(attribute.String("app.tool.name", a.ToolName)))
			return &tool.BeforeToolResult{Context: ctx}, nil
		}},
		AfterTool: []tool.AfterToolCallbackStructured{func(ctx context.Context, a *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
			var err error
			if a == nil || a.Error != nil {
				err = ErrMemoryTool
			}
			telemetrytrace.End(trace.SpanFromContext(ctx), err)
			// Preserve SDK tool-level errors for the model's correction loop.
			// Only selection/budget violations fail the whole Attempt here.
			return nil, nil
		}},
	}
}
