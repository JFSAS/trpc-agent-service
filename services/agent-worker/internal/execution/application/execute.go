package application

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

var (
	ErrManifestMissing     = errors.New("fixed manifest has not arrived")
	ErrManifestInvalid     = errors.New("fixed manifest failed validation")
	ErrManifestUnsupported = errors.New("fixed manifest is not supported by Worker V1")
	ErrDependency          = errors.New("execution dependency temporarily unavailable")
	ErrCredentialDenied    = errors.New("execution credentials rejected")
	ErrRuntimeFailed       = errors.New("agent runtime failed")
	ErrSessionInvalid      = errors.New("session snapshot failed validation")
	ErrSessionPreparation  = errors.New("session store requires explicit preparation")
)

type ManifestReader interface {
	Resolve(context.Context, domain.Route) (domain.Plan, error)
}
type RuntimeFactory interface {
	// Prepare resolves one complete credential batch, invokes check after batch
	// receipt and before constructors, and never retries Resolve transparently.
	Prepare(context.Context, domain.Grant, domain.Plan, func(context.Context) error) (AttemptRuntime, error)
}
type AttemptRuntime interface {
	Load(context.Context, domain.Head) ([]byte, error)
	Execute(context.Context, []byte) (domain.RuntimeResult, error)
	Stage(context.Context, []byte) (domain.Candidate, error)
	Close()
}
type Processor struct {
	modelAdmission ModelAdmission
	ledger         Ledger
	manifests      ManifestReader
	runtime        RuntimeFactory
	worker         string
	maxActive      int
	observer       Observer
}

func NewProcessor(ledger Ledger, manifests ManifestReader, runtime RuntimeFactory, worker string, maxActive int, observers ...Observer) (*Processor, error) {
	if ledger == nil || manifests == nil || runtime == nil || worker == "" || maxActive < 1 || len(observers) > 1 {
		return nil, domain.ErrInvalid
	}
	p := &Processor{ledger: ledger, manifests: manifests, runtime: runtime, worker: worker, maxActive: maxActive}
	if len(observers) == 1 {
		p.observer = observers[0]
	}
	return p, nil
}

// Advance performs one bounded attempt. Intake and retry scheduling remain
// durable ledger facts, so a process exit never loses an accepted Run.
func (p *Processor) Advance(ctx context.Context, r domain.Run) (advanceErr error) {
	advanceStart := time.Now()
	defer func() { p.observe(ctx, "advance", r, "", advanceStart, advanceErr) }()
	terminalStart := time.Now()
	if terminal, err := p.ledger.Terminalize(ctx, r.Request.Route.TenantID, r.Request.RunID, ""); err != nil || terminal {
		p.observe(ctx, "terminalize", r, "", terminalStart, err)
		return err
	}
	start := time.Now()
	plan, err := p.manifests.Resolve(ctx, r.Request.Route)
	p.observe(ctx, "manifest", r, "", start, err)
	if err != nil {
		if errors.Is(err, ErrManifestInvalid) || errors.Is(err, ErrManifestUnsupported) {
			reason := "MANIFEST_INVALID"
			if errors.Is(err, ErrManifestUnsupported) {
				reason = "UNSUPPORTED_MANIFEST"
			}
			_, e := p.ledger.Terminalize(ctx, r.Request.Route.TenantID, r.Request.RunID, reason)
			return e
		}
		return err
	}
	if plan.TenantID != r.Request.Route.TenantID || plan.ManifestID != r.Request.Route.ManifestRef || plan.ManifestDigest != r.Request.Route.ManifestDigest || plan.DeploymentRevisionID != r.Request.Route.DeploymentRevisionID {
		_, err = p.ledger.Terminalize(ctx, r.Request.Route.TenantID, r.Request.RunID, "MANIFEST_INVALID")
		return err
	}
	start = time.Now()
	g, err := p.ledger.Claim(ctx, domain.ClaimRequest{TenantID: r.Request.Route.TenantID, RunID: r.Request.RunID, WorkerID: p.worker, MaxRunSeconds: plan.MaxRunSeconds, MaxActive: p.maxActive})
	p.observe(ctx, "claim", r, g.AttemptID, start, err)
	if err != nil {
		return err
	}
	if g.Run.ExecutionDeadline == nil {
		return domain.ErrInvalid
	}
	attemptCtx, cancel := context.WithDeadline(ctx, *g.Run.ExecutionDeadline)
	defer cancel()
	stopRenew := make(chan struct{})
	renewDone := make(chan struct{})
	var renewMu sync.Mutex
	var renewErr error
	go func() {
		defer close(renewDone)
		timer := time.NewTicker(g.Run.Policy.RenewalInterval)
		defer timer.Stop()
		for {
			select {
			case <-stopRenew:
				return
			case <-attemptCtx.Done():
				return
			case <-timer.C:
				renewStart := time.Now()
				_, e := p.ledger.Renew(attemptCtx, g)
				p.observe(attemptCtx, "renew", r, g.AttemptID, renewStart, e)
				if e != nil {
					renewMu.Lock()
					renewErr = e
					renewMu.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(stopRenew); <-renewDone }()
	check := func(ctx context.Context) error {
		start := time.Now()
		err := p.ledger.Check(ctx, g)
		p.observe(ctx, "fence", r, g.AttemptID, start, err)
		return err
	}
	fail := func(cause error) error {
		renewMu.Lock()
		lost := renewErr
		renewMu.Unlock()
		if lost != nil || errors.Is(cause, domain.ErrFenced) {
			return domain.ErrFenced
		}
		// A canceled/deadline attempt loses eligibility through the same durable
		// lease; recovery does not release it early while external calls may run.
		if attemptCtx.Err() != nil {
			return attemptCtx.Err()
		}
		reason, retry := "RUNTIME_FAILED", false
		switch {
		case errors.Is(cause, ErrSessionPreparation):
			reason, retry = "SESSION_PREPARATION_REQUIRED", true
		case errors.Is(cause, ErrDependency):
			reason, retry = "DEPENDENCY_UNAVAILABLE", true
		case errors.Is(cause, ErrCredentialDenied):
			reason = "CREDENTIAL_DENIED"
		case errors.Is(cause, ErrSessionInvalid):
			reason = "SESSION_INVALID"
		}
		if e := p.ledger.FailAttempt(attemptCtx, g, reason, retry); e != nil {
			return e
		}
		return cause
	}
	start = time.Now()
	runtime, err := p.runtime.Prepare(attemptCtx, g, plan, check)
	p.observe(attemptCtx, "prepare", r, g.AttemptID, start, err)
	if err != nil {
		return fail(err)
	}
	defer runtime.Close()
	if err = check(attemptCtx); err != nil {
		return fail(err)
	}
	start = time.Now()
	history, err := runtime.Load(attemptCtx, g.Parent)
	p.observe(attemptCtx, "session_load", r, g.AttemptID, start, err)
	if err != nil {
		return fail(err)
	}
	start = time.Now()
	result, err := p.executeModel(attemptCtx, g, plan, runtime, history)
	p.observe(attemptCtx, "execute", r, g.AttemptID, start, err)
	if result.UsageKnown {
		usageStart := time.Now()
		usageErr := p.ledger.RecordModelUsage(attemptCtx, g, result)
		p.observe(attemptCtx, "usage_record", r, g.AttemptID, usageStart, usageErr)
		if usageErr != nil {
			return fail(usageErr)
		}
	}
	if p.observer != nil && result.UsageKnown {
		p.observer.Observe(attemptCtx, Observation{Operation: "usage", Result: "ok", TenantID: r.Request.Route.TenantID, RunID: r.Request.RunID, AttemptID: g.AttemptID, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, TotalTokens: result.TotalTokens})
	}
	if err != nil {
		return fail(err)
	}
	if err = check(attemptCtx); err != nil {
		return fail(err)
	}
	start = time.Now()
	candidate, err := runtime.Stage(attemptCtx, result.Snapshot)
	p.observe(attemptCtx, "session_stage", r, g.AttemptID, start, err)
	if err != nil {
		return fail(err)
	}
	finish := domain.Finish{Grant: g, Status: domain.Succeeded, Candidate: candidate, FinalText: result.FinalText}
	start = time.Now()
	_, err = p.ledger.Complete(attemptCtx, finish)
	p.observe(attemptCtx, "complete", r, g.AttemptID, start, err)
	if err == nil {
		return nil
	}
	// Query stable identity after uncertain Completion before attempting any
	// other state change. A retry uses the identical in-memory candidate/result.
	proofCtx, proofCancel := context.WithTimeout(context.WithoutCancel(ctx), g.Run.Policy.LeaseTTL)
	defer proofCancel()
	if committed, e := p.ledger.FindCompletion(proofCtx, r.Request.Route.TenantID, r.Request.RunID); e == nil {
		if committed.AttemptID == g.AttemptID && committed.Status == domain.Succeeded && committed.ResultDigest == domain.FinishDigest(finish) {
			return nil
		}
		return domain.ErrFenced
	}
	if errors.Is(err, domain.ErrFenced) || errors.Is(err, domain.ErrConflict) {
		return err
	}
	if e := check(attemptCtx); e != nil {
		return e
	}
	start = time.Now()
	_, err = p.ledger.Complete(attemptCtx, finish)
	p.observe(attemptCtx, "complete", r, g.AttemptID, start, err)
	return err
}
