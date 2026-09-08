package application

import (
	"context"
	"errors"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"reflect"
	"testing"
)

type dispatchLedger struct {
	Ledger
	order   *[]string
	markErr error
}

func (l *dispatchLedger) Check(context.Context, domain.Grant) error {
	*l.order = append(*l.order, "check")
	return nil
}
func (l *dispatchLedger) MarkExecuting(context.Context, domain.Grant) error {
	*l.order = append(*l.order, "executing")
	return l.markErr
}

type dispatchAdmission struct {
	order *[]string
	err   error
}

func (a dispatchAdmission) AdmitModel(context.Context, domain.Grant, domain.Plan, domain.ModelCall) error {
	*a.order = append(*a.order, "reserve")
	return a.err
}

type guardedRuntime struct {
	*observationRuntime
	call      domain.ModelCall
	order     *[]string
	duplicate bool
	bypass    bool
}

func (r *guardedRuntime) ExecuteGuarded(c context.Context, _ []byte, admit func(context.Context, domain.ModelCall) error) (domain.RuntimeResult, error) {
	if r.bypass {
		return domain.RuntimeResult{FinalText: "unadmitted"}, nil
	}
	if err := admit(c, r.call); err != nil {
		return domain.RuntimeResult{}, err
	}
	if r.duplicate {
		if err := admit(c, r.call); !errors.Is(err, domain.ErrFenced) {
			return domain.RuntimeResult{}, errors.New("duplicate callback accepted")
		}
	}
	*r.order = append(*r.order, "send")
	return domain.RuntimeResult{FinalText: "answer"}, nil
}
func TestGovernedDispatchOrderAndNoFallback(t *testing.T) {
	for _, mode := range []string{"allow", "deny", "mark_failure", "missing_admission", "legacy_runtime", "duplicate", "changed_body", "bypass"} {
		t.Run(mode, func(t *testing.T) {
			order := []string{}
			ledger := &dispatchLedger{order: &order}
			p := &Processor{ledger: ledger, modelAdmission: dispatchAdmission{order: &order}}
			g := domain.Grant{AttemptID: "attempt", Run: domain.Run{SessionID: "session", Request: domain.Requested{RunID: "run", Route: domain.Route{TenantID: "tenant"}, Authorization: &domain.AdmissionAuthorization{}}}}
			plan := domain.Plan{ModelName: "fixture", ModelEndpoint: "http://127.0.0.1/v1", MaxOutputTokens: 10}
			body := []byte(`{"messages":[]}`)
			runtime := &guardedRuntime{observationRuntime: &observationRuntime{}, order: &order, call: domain.ModelCall{TenantID: "tenant", RunID: "run", AttemptID: "attempt", SessionID: "session", Model: plan.ModelName, Endpoint: plan.ModelEndpoint + "/chat/completions", MaxOutputTokens: 10, Body: body, RequestDigest: domain.Digest(body)}}
			var rt AttemptRuntime = runtime
			switch mode {
			case "deny":
				p.modelAdmission = dispatchAdmission{order: &order, err: ErrDependency}
			case "mark_failure":
				ledger.markErr = domain.ErrFenced
			case "missing_admission":
				p.modelAdmission = nil
			case "legacy_runtime":
				rt = &observationRuntime{}
			case "duplicate":
				runtime.duplicate = true
			case "bypass":
				runtime.bypass = true
			case "changed_body":
				runtime.call.RequestDigest = domain.Digest([]byte("other"))
			}
			_, err := p.executeModel(context.Background(), g, plan, rt, nil)
			want := []string{}
			switch mode {
			case "allow", "duplicate":
				want = []string{"check", "reserve", "executing", "send"}
				if err != nil {
					t.Fatal(err)
				}
			case "deny":
				want = []string{"check", "reserve"}
			case "mark_failure":
				want = []string{"check", "reserve", "executing"}
			}
			if !reflect.DeepEqual(order, want) {
				t.Fatal("invalid dispatch order", order, want)
			}
			if mode != "allow" && mode != "duplicate" && err == nil {
				t.Fatal("failure bypassed gate")
			}
		})
	}
}
