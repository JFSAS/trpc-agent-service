package application

import (
	"context"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"strings"
	"sync/atomic"
)

// ModelAdmission must verify current authority and an enforceable bound and commit
// the exact operation reservation/link while the grant is PREPARING. It grants
// only this dispatch, never permission to replay a physical request.
type ModelAdmission interface {
	AdmitModel(context.Context, domain.Grant, domain.Plan, domain.ModelCall) error
}
type GuardedAttemptRuntime interface {
	ExecuteGuarded(context.Context, []byte, func(context.Context, domain.ModelCall) error) (domain.RuntimeResult, error)
}

func NewProcessorWithModelAdmission(l Ledger, m ManifestReader, r RuntimeFactory, admission ModelAdmission, worker string, maxActive int, observers ...Observer) (*Processor, error) {
	if admission == nil {
		return nil, domain.ErrInvalid
	}
	p, err := NewProcessor(l, m, r, worker, maxActive, observers...)
	if err != nil {
		return nil, err
	}
	p.modelAdmission = admission
	return p, nil
}
func (p *Processor) executeModel(ctx context.Context, g domain.Grant, plan domain.Plan, runtime AttemptRuntime, history []byte) (domain.RuntimeResult, error) {
	if g.Run.Request.Authorization == nil {
		if err := p.ledger.MarkExecuting(ctx, g); err != nil {
			return domain.RuntimeResult{}, err
		}
		return runtime.Execute(ctx, history)
	}
	guarded, ok := runtime.(GuardedAttemptRuntime)
	if !ok || p.modelAdmission == nil {
		return domain.RuntimeResult{}, domain.ErrNotReady
	}
	var entered, approved atomic.Bool
	result, err := guarded.ExecuteGuarded(ctx, history, func(callCtx context.Context, call domain.ModelCall) error {
		if !entered.CompareAndSwap(false, true) {
			return domain.ErrFenced
		}
		maximum := plan.MaxOutputTokens
		if plan.NodeMaxOutputTokens != nil {
			maximum = *plan.NodeMaxOutputTokens
		}
		if len(call.Body) == 0 || call.RequestDigest != domain.Digest(call.Body) || call.Model != plan.ModelName || call.Endpoint != strings.TrimRight(plan.ModelEndpoint, "/")+"/chat/completions" || maximum <= 0 || maximum > plan.MaxOutputTokens || call.MaxOutputTokens != maximum {
			return domain.ErrInvalid
		}
		if call.TenantID != g.Run.Request.Route.TenantID || call.RunID != g.Run.Request.RunID || call.AttemptID != g.AttemptID || call.SessionID != g.Run.SessionID {
			return domain.ErrInvalid
		}
		if err := p.ledger.Check(callCtx, g); err != nil {
			return err
		}
		if err := p.modelAdmission.AdmitModel(callCtx, g, plan, call); err != nil {
			return err
		}
		// The committed reservation remains conservative if this transition fails.
		// MarkExecuting rechecks the live grant; no external request is sent on error.
		if err := p.ledger.MarkExecuting(callCtx, g); err != nil {
			return err
		}
		approved.Store(true)
		return nil
	})
	if !approved.Load() {
		if err != nil {
			return domain.RuntimeResult{}, err
		}
		return domain.RuntimeResult{}, domain.ErrNotReady
	}
	return result, err
}
