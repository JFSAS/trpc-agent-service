package application

import (
	"context"
	"testing"

	"github.com/liuzengh/trpc-agent-service/platform/tracecontext"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type tracedPublishFunc func(context.Context, string, string, []byte, tracecontext.Carrier) error

func (f tracedPublishFunc) PublishMessage(ctx context.Context, s, id string, b []byte, c tracecontext.Carrier) error {
	return f(ctx, s, id, b, c)
}
func TestRelayPreservesCreationAcrossPublishAttempts(t *testing.T) {
	ex := tracetest.NewInMemoryExporter()
	p := sdktrace.NewTracerProvider(sdktrace.WithSyncer(ex))
	defer p.Shutdown(context.Background())
	ctx, parent := p.Tracer("gateway").Start(context.Background(), "create execution.run-requested.v1")
	creation := tracecontext.Capture(ctx)
	parent.End()
	source := &outboxStub{found: true, message: OutboxMessage{EventID: "evt", Subject: "execution.run-requested.v1", Payload: []byte("unchanged"), Carrier: creation}}
	calls := 0
	relay := NewRelay(source, tracedPublishFunc(func(ctx context.Context, s, id string, b []byte, c tracecontext.Carrier) error {
		calls++
		if c != creation || string(b) != "unchanged" || id != "evt" {
			t.Fatal("immutable carrier/payload changed")
		}
		sc := trace.SpanContextFromContext(ctx)
		if sc.TraceID() != parent.SpanContext().TraceID() || sc.SpanID() == parent.SpanContext().SpanID() {
			t.Fatal("publish span is not independent child")
		}
		return nil
	}))
	relay.Tracer = p.Tracer("gateway")
	for i := 0; i < 2; i++ {
		if ok, err := relay.PublishNext(context.Background()); err != nil || !ok {
			t.Fatal(err)
		}
	}
	spans := ex.GetSpans()
	if calls != 2 || len(spans) != 3 {
		t.Fatal(calls, len(spans))
	}
	for _, s := range spans[1:] {
		if s.Parent.SpanID() != parent.SpanContext().SpanID() || len(s.Links) != 1 || s.Links[0].SpanContext.SpanID() != parent.SpanContext().SpanID() {
			t.Fatal("creation relationship/link missing")
		}
	}
	if spans[1].SpanContext.SpanID() == spans[2].SpanContext.SpanID() {
		t.Fatal("retry reused span id")
	}
}
