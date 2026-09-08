package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

type observations struct{ events []Observation }

func (o *observations) Observe(_ context.Context, e Observation) { o.events = append(o.events, e) }

type observationLedger struct {
	Ledger
	run               domain.Run
	completed, failed int
	reason            string
	usageRecords      int
	usageError        error
}

func (l *observationLedger) Terminalize(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (l *observationLedger) Claim(context.Context, domain.ClaimRequest) (domain.Grant, error) {
	return domain.Grant{Run: l.run, AttemptID: "attempt", WorkerID: "worker", Token: "secret-token"}, nil
}
func (l *observationLedger) Check(context.Context, domain.Grant) error         { return nil }
func (l *observationLedger) MarkExecuting(context.Context, domain.Grant) error { return nil }
func (l *observationLedger) RecordModelUsage(context.Context, domain.Grant, domain.RuntimeResult) error {
	l.usageRecords++
	return l.usageError
}
func (l *observationLedger) Complete(context.Context, domain.Finish) (domain.Completion, error) {
	l.completed++
	return domain.Completion{}, nil
}
func (l *observationLedger) FailAttempt(_ context.Context, _ domain.Grant, reason string, _ bool) error {
	l.failed++
	l.reason = reason
	return nil
}

type observationManifest struct{ plan domain.Plan }

func (m observationManifest) Resolve(context.Context, domain.Route) (domain.Plan, error) {
	return m.plan, nil
}

type observationRuntime struct {
	stageCalls     int
	resultOverride *domain.RuntimeResult
	stageError     error
	executeError   error
	calls          int
}

func (r *observationRuntime) Prepare(context.Context, domain.Grant, domain.Plan, func(context.Context) error) (AttemptRuntime, error) {
	return r, nil
}
func (r *observationRuntime) Load(context.Context, domain.Head) ([]byte, error) {
	return []byte("private-history"), nil
}
func (r *observationRuntime) Execute(context.Context, []byte) (domain.RuntimeResult, error) {
	r.calls++
	if r.resultOverride != nil {
		return *r.resultOverride, r.executeError
	}
	return domain.RuntimeResult{UsageKnown: true, Snapshot: []byte("private-transcript"), FinalText: "private-final", InputTokens: 200, OutputTokens: 300, TotalTokens: 500}, nil
}
func (r *observationRuntime) Stage(context.Context, []byte) (domain.Candidate, error) {
	r.stageCalls++
	return domain.Candidate{}, r.stageError
}
func (r *observationRuntime) Close() {}
func TestObservedProcessorPreservesExecutionAndSanitizesSignals(t *testing.T) {
	for _, failStage := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "stage_failure"}[failStage], func(t *testing.T) {
			deadline := time.Now().Add(time.Minute)
			run := domain.Run{Request: domain.Requested{RunID: "run", Route: domain.Route{TenantID: "tenant", ManifestRef: "manifest", ManifestDigest: "digest", DeploymentRevisionID: "revision"}, Input: domain.Input{Text: "private-input"}}, ExecutionDeadline: &deadline, Policy: domain.Policy{RenewalInterval: time.Hour}}
			ledger := &observationLedger{run: run}
			runtime := &observationRuntime{}
			if failStage {
				runtime.stageError = ErrDependency
			}
			events := &observations{}
			processor, err := NewProcessor(ledger, observationManifest{domain.Plan{TenantID: "tenant", ManifestID: "manifest", ManifestDigest: "digest", DeploymentRevisionID: "revision"}}, runtime, "worker", 1, events)
			if err != nil {
				t.Fatal(err)
			}
			err = processor.Advance(context.Background(), run)
			if failStage {
				if !errors.Is(err, ErrDependency) || ledger.failed != 1 || ledger.completed != 0 || ledger.reason != "DEPENDENCY_UNAVAILABLE" {
					t.Fatal(ledger, err)
				}
			} else if err != nil || ledger.completed != 1 || ledger.failed != 0 {
				t.Fatal(ledger, err)
			}
			if ledger.usageRecords != 1 {
				t.Fatal("known usage not persisted before stage", ledger)
			}
			if runtime.calls != 1 {
				t.Fatal("telemetry changed execution count")
			}
			seen := map[string]int{}
			for _, e := range events.events {
				seen[e.Operation]++
				if e.Operation == "usage" && e.TotalTokens != 500 {
					t.Fatal(e)
				}
			}
			for _, phase := range []string{"claim", "prepare", "session_load", "execute", "session_stage", "usage", "advance"} {
				if seen[phase] != 1 {
					t.Fatal("missing or duplicate phase", phase, seen)
				}
			}
			raw, _ := json.Marshal(events.events)
			for _, secret := range []string{"private-input", "private-history", "private-transcript", "private-final", "secret-token"} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("sensitive payload entered typed observations")
				}
			}
		})
	}
}

func TestProcessorNeverPromotesUnknownUsageToReported(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown_positive", true: "known_zero"}[known], func(t *testing.T) {
			deadline := time.Now().Add(time.Minute)
			run := domain.Run{Request: domain.Requested{RunID: "run", Route: domain.Route{TenantID: "tenant", ManifestRef: "manifest", ManifestDigest: "digest", DeploymentRevisionID: "revision"}, Input: domain.Input{Text: "input"}}, ExecutionDeadline: &deadline, Policy: domain.Policy{RenewalInterval: time.Hour}}
			ledger := &observationLedger{run: run}
			result := domain.RuntimeResult{UsageKnown: known, FinalText: "answer"}
			if !known {
				result.InputTokens = 20
				result.OutputTokens = 30
				result.TotalTokens = 50
			}
			runtime := &observationRuntime{resultOverride: &result}
			events := &observations{}
			processor, err := NewProcessor(ledger, observationManifest{domain.Plan{TenantID: "tenant", ManifestID: "manifest", ManifestDigest: "digest", DeploymentRevisionID: "revision"}}, runtime, "worker", 1, events)
			if err != nil {
				t.Fatal(err)
			}
			if err = processor.Advance(context.Background(), run); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, event := range events.events {
				if event.Operation == "usage" {
					count++
					if event.TotalTokens != 0 {
						t.Fatal("unknown counters reported", event)
					}
				}
			}
			want := 0
			if known {
				want = 1
			}
			if ledger.usageRecords != want {
				t.Fatal("unknown usage persisted or known zero lost", ledger)
			}
			if count != want || ledger.completed != 1 {
				t.Fatal("usage state altered execution or accounting", count, ledger)
			}
		})
	}
}

func TestUsagePersistenceFailurePreventsStageAndCompletion(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	run := domain.Run{Request: domain.Requested{RunID: "run", Route: domain.Route{TenantID: "tenant", ManifestRef: "manifest", ManifestDigest: "digest", DeploymentRevisionID: "revision"}, Input: domain.Input{Text: "input"}}, ExecutionDeadline: &deadline, Policy: domain.Policy{RenewalInterval: time.Hour}}
	failure := errors.New("usage persistence unavailable")
	ledger := &observationLedger{run: run, usageError: failure}
	runtime := &observationRuntime{}
	processor, err := NewProcessor(ledger, observationManifest{domain.Plan{TenantID: "tenant", ManifestID: "manifest", ManifestDigest: "digest", DeploymentRevisionID: "revision"}}, runtime, "worker", 1)
	if err != nil {
		t.Fatal(err)
	}
	err = processor.Advance(context.Background(), run)
	if !errors.Is(err, failure) || ledger.usageRecords != 1 || runtime.stageCalls != 0 || ledger.completed != 0 || runtime.calls != 1 {
		t.Fatal(err, ledger, runtime)
	}
}

func TestFailedRuntimePersistsUsageWithoutPromotingResult(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			deadline := time.Now().Add(time.Minute)
			run := domain.Run{Request: domain.Requested{RunID: "run", Route: domain.Route{TenantID: "tenant", ManifestRef: "manifest", ManifestDigest: "digest", DeploymentRevisionID: "revision"}}, ExecutionDeadline: &deadline, Policy: domain.Policy{RenewalInterval: time.Hour}}
			ledger := &observationLedger{run: run}
			runtime := &observationRuntime{executeError: ErrRuntimeFailed, resultOverride: &domain.RuntimeResult{UsageKnown: known, InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}
			processor, err := NewProcessor(ledger, observationManifest{domain.Plan{TenantID: "tenant", ManifestID: "manifest", ManifestDigest: "digest", DeploymentRevisionID: "revision"}}, runtime, "worker", 1)
			if err != nil {
				t.Fatal(err)
			}
			err = processor.Advance(context.Background(), run)
			want := 0
			if known {
				want = 1
			}
			if !errors.Is(err, ErrRuntimeFailed) || ledger.usageRecords != want || ledger.failed != 1 || ledger.completed != 0 || runtime.stageCalls != 0 || runtime.calls != 1 {
				t.Fatal(err, ledger, runtime)
			}
		})
	}
}
