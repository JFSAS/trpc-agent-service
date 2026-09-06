// Package telegramruntime reconciles local webhook material and a separately
// fenced remote registration. No Worker, Binding or prior webhook is required.
package telegramruntime

import (
	"context"
	use "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/application/accountuse"
	refresh "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/application/catalogrefresh"
	c "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/domain/accountcatalog"
	"sync"
	"time"
)

type Directory interface {
	Accounts() []c.Account
	Lookup(context.Context, string) (refresh.View, error)
}
type Installer interface {
	Install(c.Account, context.Context, string, int64) error
	Remove(string)
}
type Operation struct {
	ID, AccountID, InstanceID, InstanceEpoch string
	Revision, Epoch                          int64
}
type Registrations interface {
	State(context.Context, *c.Permit) (string, error)
	Acquire(context.Context, *c.Permit) (Operation, bool, error)
	Check(context.Context, *c.Permit, Operation) error
	BeginCall(context.Context, *c.Permit, Operation) error
	Finish(context.Context, *c.Permit, Operation, string) error
}
type Remote interface {
	Identity(context.Context) (string, error)
	Register(context.Context, string, string) (bool, error)
	Close()
}
type RemoteFactory interface{ New(string) (Remote, error) }
type Status struct {
	AccountID     string
	Revision      int64
	State, Reason string
}
type installed struct {
	revision, generation int64
	ctx                  context.Context
}
type Runtime struct {
	directory     Directory
	use           *use.Service
	installer     Installer
	registrations Registrations
	factory       RemoteFactory
	origin        string
	mu            sync.Mutex
	entries       map[string]installed
	statuses      map[string]Status
	generation    int64
}

func New(d Directory, u *use.Service, i Installer, r Registrations, f RemoteFactory, origin string) (*Runtime, error) {
	if d == nil || u == nil || i == nil || r == nil || f == nil || origin == "" {
		return nil, c.ErrInvalid
	}
	return &Runtime{directory: d, use: u, installer: i, registrations: r, factory: f, origin: origin, entries: map[string]installed{}, statuses: map[string]Status{}}, nil
}
func (s *Runtime) set(a c.Account, state, reason string) {
	s.mu.Lock()
	s.statuses[a.ID] = Status{a.ID, a.ConnectionRevision, state, reason}
	s.mu.Unlock()
}
func (s *Runtime) Status(id string) Status { s.mu.Lock(); defer s.mu.Unlock(); return s.statuses[id] }
func (s *Runtime) Ready() bool {
	for _, a := range s.directory.Accounts() {
		if a.Provider == "telegram" && a.Enabled {
			st := s.Status(a.ID)
			if st.Revision != a.ConnectionRevision || st.State != "READY" {
				return false
			}
		}
	}
	return true
}

// Reconcile is single-flight per process. Run uses eight bounded workers and
// waits for them; it never starts another sweep while an old one is in flight.
func (s *Runtime) reconcile(ctx context.Context, a c.Account) {
	v, e := s.directory.Lookup(ctx, a.ID)
	if e != nil || !a.Enabled {
		s.installer.Remove(a.ID)
		s.mu.Lock()
		delete(s.entries, a.ID)
		s.mu.Unlock()
		if !a.Enabled {
			s.set(a, "DISABLED", "NONE")
		} else {
			s.set(a, "ERROR", "SOURCE_UNAVAILABLE")
		}
		return
	}
	a = v.Account
	s.mu.Lock()
	old, ok := s.entries[a.ID]
	s.mu.Unlock()
	if !ok || old.revision != a.ConnectionRevision || old.ctx.Err() != nil || old.ctx != v.Context {
		s.installer.Remove(a.ID)
		s.set(a, "CONNECTING", "CREDENTIAL_UNAVAILABLE")
		s.mu.Lock()
		s.generation++
		generation := s.generation
		s.mu.Unlock()
		p, account, e := s.use.Open(ctx, a.ID, "telegram_webhook", a.ConnectionRevision, generation)
		if e != nil {
			return
		}
		values, e := s.use.Resolve(ctx, p, account, nil, nil)
		p.Revoke()
		if e != nil || len(values) != 1 {
			return
		}
		// Cancellation and generation are checked again by the installer/Acceptor.
		if v.Context.Err() != nil {
			return
		}
		if e = s.installer.Install(a, v.Context, values[0].Value, generation); e != nil {
			s.set(a, "ERROR", "CONFIG_INVALID")
			return
		}
		values[0].Value = ""
		old = installed{a.ConnectionRevision, generation, v.Context}
		s.mu.Lock()
		s.entries[a.ID] = old
		s.mu.Unlock()
		s.set(a, "CONFIG_APPLIED", "REGISTRATION_PENDING")
	}
	p, account, e := s.use.Open(ctx, a.ID, "telegram_registration", a.ConnectionRevision, old.generation)
	if e != nil {
		return
	}
	defer p.Revoke()
	op, acquired, e := s.registrations.Acquire(ctx, p)
	if e != nil {
		s.set(a, "ERROR", "REGISTRATION_UNKNOWN")
		return
	}
	if !acquired {
		state, e := s.registrations.State(ctx, p)
		if e == nil && state == "READY" {
			s.set(a, "READY", "NONE")
		} else {
			s.set(a, "CONFIG_APPLIED", "REGISTRATION_PENDING")
		}
		return
	}
	result := "UNKNOWN"
	// Completion is evidence, so canceled preparation cannot suppress it.
	defer func() {
		settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		e := s.registrations.Finish(settle, p, op, result)
		if e != nil || result != "READY" {
			reason := "REGISTRATION_UNKNOWN"
			if result == "IDENTITY_MISMATCH" {
				reason = "REMOTE_IDENTITY_MISMATCH"
			}
			s.set(a, "ERROR", reason)
		} else {
			s.set(a, "READY", "NONE")
		}
	}()
	if e = s.registrations.Check(ctx, p, op); e != nil {
		return
	}
	values, e := s.use.Resolve(ctx, p, account, nil, &op.Epoch)
	if e != nil || len(values) != 2 {
		return
	}
	remote, e := s.factory.New(values[0].Value)
	if e != nil {
		return
	}
	defer remote.Close()
	opctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(p.Context(), cancel)
	defer stop()
	id, e := remote.Identity(opctx)
	if e != nil {
		return
	}
	if id != a.ProviderAccountID {
		s.set(a, "ERROR", "REMOTE_IDENTITY_MISMATCH")
		result = "IDENTITY_MISMATCH"
		return
	}
	if e = s.registrations.Check(opctx, p, op); e != nil {
		return
	}
	if e = s.registrations.BeginCall(opctx, p, op); e != nil {
		return
	}
	if p.Check() != nil || opctx.Err() != nil {
		result = "NOT_SENT"
		return
	}
	yes, e := remote.Register(opctx, s.origin+a.Config.WebhookPath, values[1].Value)
	values[0].Value = ""
	values[1].Value = ""
	if e == nil && yes {
		result = "READY"
	}
}
func (s *Runtime) Run(ctx context.Context) error {
	defer func() {
		for _, a := range s.directory.Accounts() {
			if a.Provider == "telegram" {
				s.installer.Remove(a.ID)
				s.set(a, "ERROR", "SHUTDOWN")
			}
		}
	}()
	for ctx.Err() == nil {
		accounts := s.directory.Accounts()
		present := map[string]bool{}
		for _, a := range accounts {
			present[a.ID] = true
		}
		s.mu.Lock()
		for id := range s.entries {
			if !present[id] {
				s.installer.Remove(id)
				delete(s.entries, id)
				delete(s.statuses, id)
			}
		}
		s.mu.Unlock()
		jobs := make(chan c.Account)
		var wg sync.WaitGroup
		for n := 0; n < 8; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for a := range jobs {
					s.reconcile(ctx, a)
				}
			}()
		}
		for _, a := range accounts {
			if a.Provider == "telegram" {
				select {
				case jobs <- a:
				case <-ctx.Done():
				}
			}
		}
		close(jobs)
		wg.Wait()
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}
