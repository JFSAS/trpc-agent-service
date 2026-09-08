package runtimeadapter

import (
	"context"
	"errors"
	"sync"

	"github.com/liuzengh/trpc-agent-service/platform/telemetrytrace"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/sessionstore"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/trpcagent"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/application"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type attempt struct {
	tracer       trace.Tracer
	grant        domain.Grant
	plan         domain.Plan
	store        candidateStore
	check        func(context.Context) error
	modelKey     string
	executor     trpcagent.Executor
	capacity     int
	mu           sync.Mutex
	closed       bool
	executed     bool
	loaded       bool
	loadedDigest string
	resultDigest string
}

func (a *attempt) Load(ctx context.Context, head domain.Head) (history []byte, resultErr error) {
	ctx, span := telemetrytrace.Start(a.tracer, ctx, "worker.session.load")
	defer func() {
		if resultErr == nil {
			span.SetAttributes(attribute.Int("app.storage.bytes", len(history)))
		}
		telemetrytrace.End(span, resultErr)
	}()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || head != a.grant.Parent {
		return nil, application.ErrSessionInvalid
	}
	if err := a.check(ctx); err != nil {
		return nil, err
	}
	if head == (domain.Head{}) {
		a.loaded = true
		a.loadedDigest = domain.Digest(nil)
		return nil, nil
	}
	c, err := a.store.Load(ctx, a.plan.TenantID, a.grant.Run.SessionID, sessionstore.Head{Ref: head.Ref, Digest: head.Digest})
	if err != nil {
		return nil, sessionError(ctx, err)
	}
	a.loaded = true
	a.loadedDigest = domain.Digest(c.Snapshot)
	return append([]byte(nil), c.Snapshot...), nil
}
func (a *attempt) Execute(ctx context.Context, history []byte) (domain.RuntimeResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.executed || !a.loaded || domain.Digest(history) != a.loadedDigest {
		return domain.RuntimeResult{}, application.ErrRuntimeFailed
	}
	a.executed = true
	if err := a.check(ctx); err != nil {
		return domain.RuntimeResult{}, err
	}
	p := a.plan
	g := a.grant
	result, err := a.executor.Execute(ctx, trpcagent.Request{TenantID: p.TenantID, SessionID: g.Run.SessionID, RunID: g.Run.Request.RunID, AttemptID: g.AttemptID, NodeID: p.NodeID, Instruction: p.Instruction, InputText: g.Run.Request.Input.Text, Model: trpcagent.Model{Endpoint: p.ModelEndpoint, Name: p.ModelName, APIKey: a.modelKey, Temperature: p.Temperature, MaxOutputTokens: p.NodeMaxOutputTokens}, MaxOutputTokens: p.MaxOutputTokens, AcceptedSnapshot: history})
	if err != nil {
		if ctx.Err() != nil {
			return domain.RuntimeResult{}, ctx.Err()
		}
		if errors.Is(err, trpcagent.ErrSnapshot) || errors.Is(err, trpcagent.ErrCapacity) || errors.Is(err, trpcagent.ErrOverlay) {
			return domain.RuntimeResult{}, application.ErrSessionInvalid
		}
		if errors.Is(err, trpcagent.ErrRetryableModel) {
			return domain.RuntimeResult{}, application.ErrDependency
		}
		return domain.RuntimeResult{}, application.ErrRuntimeFailed
	}
	a.resultDigest = domain.Digest(result.Snapshot)
	return domain.RuntimeResult{FinalText: result.FinalText, Snapshot: result.Snapshot, InputTokens: int64(result.Usage.InputTokens), OutputTokens: int64(result.Usage.OutputTokens), TotalTokens: int64(result.Usage.TotalTokens)}, nil
}
func (a *attempt) Stage(ctx context.Context, snapshot []byte) (staged domain.Candidate, resultErr error) {
	ctx, span := telemetrytrace.Start(a.tracer, ctx, "worker.session.stage", trace.WithAttributes(attribute.Int("app.storage.bytes", len(snapshot))))
	defer func() {
		if resultErr == nil {
			span.SetAttributes(attribute.String("app.outcome", "candidate"))
		}
		telemetrytrace.End(span, resultErr)
	}()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || !a.executed || a.resultDigest == "" || domain.Digest(snapshot) != a.resultDigest {
		return domain.Candidate{}, application.ErrRuntimeFailed
	}
	if err := a.check(ctx); err != nil {
		return domain.Candidate{}, err
	}
	g := a.grant
	candidate := sessionstore.Candidate{Identity: sessionstore.Identity{TenantID: a.plan.TenantID, SessionID: g.Run.SessionID, RunID: g.Run.Request.RunID, AttemptID: g.AttemptID}, Parent: sessionstore.Head{Ref: g.Parent.Ref, Digest: g.Parent.Digest}, ContentVersion: sessionstore.ContentVersion, Snapshot: snapshot}
	head, err := a.store.Put(ctx, candidate)
	if err != nil {
		if errors.Is(err, sessionstore.ErrConflict) || errors.Is(err, sessionstore.ErrCapacity) || errors.Is(err, sessionstore.ErrCorrupt) {
			return domain.Candidate{}, sessionError(ctx, err)
		}
		span.SetAttributes(attribute.String("app.outcome", "UNKNOWN"))
		// An insert may commit before its response is lost. Read only the exact
		// deterministic key/digest under the same still-current authorization batch.
		if e := a.check(ctx); e != nil {
			return domain.Candidate{}, e
		}
		_, expected, e := candidate.Encode(a.capacity)
		if e != nil {
			return domain.Candidate{}, application.ErrSessionInvalid
		}
		verifyCtx, verifySpan := telemetrytrace.Start(a.tracer, ctx, "worker.session.verify")
		_, e = a.store.Load(verifyCtx, a.plan.TenantID, g.Run.SessionID, expected)
		telemetrytrace.End(verifySpan, e)
		if e != nil {
			return domain.Candidate{}, sessionError(ctx, err)
		}
		head = expected
	}
	return domain.Candidate{Ref: head.Ref, Digest: head.Digest, Parent: g.Parent}, nil
}
func (a *attempt) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	a.modelKey = ""
	a.store.Close()
	a.store = nil
}

var _ application.AttemptRuntime = (*attempt)(nil)

func sessionError(ctx context.Context, err error) error {
	if errors.Is(err, sessionstore.ErrIdentity) {
		return application.ErrSessionInvalid
	}
	return storeError(ctx, err)
}
