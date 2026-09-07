// Package authorizationrefresh schedules bounded current-state refresh work.
// Scheduling/installation observations are not an Admission readiness decision.
package refresh

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"

	domain "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
)

var ErrInvalid = errors.New("AUTHORIZATION_REFRESH_INVALID")
var ErrDirectory = errors.New("AUTHORIZATION_REFRESH_DIRECTORY_UNAVAILABLE")

type Desired struct {
	Target   domain.AuthorizationTarget
	Revision int64
}
type Directory interface {
	AuthorizationTargets(context.Context) ([]Desired, error)
}

// Refresh returns the desired start-to-start cadence after successful install.
// The scheduler caps cadence and applies backoff on errors; it never grants use.
type Refresher interface {
	Refresh(context.Context, domain.AuthorizationTarget) (time.Duration, error)
}
type Options struct {
	Workers                                             int
	PollInterval, Tick, MinInterval, RetryMin, RetryMax time.Duration
}

func (o Options) defaults() Options {
	if o.Workers == 0 {
		o.Workers = 4
	}
	if o.PollInterval == 0 {
		o.PollInterval = time.Second
	}
	if o.Tick == 0 {
		o.Tick = 100 * time.Millisecond
	}
	if o.MinInterval == 0 {
		o.MinInterval = 100 * time.Millisecond
	}
	if o.RetryMin == 0 {
		o.RetryMin = time.Second
	}
	if o.RetryMax == 0 {
		o.RetryMax = 30 * time.Second
	}
	return o
}

type Summary struct {
	DirectoryReady    bool
	Desired, InFlight int
	Succeeded, Failed uint64
}
type Service struct {
	directory       Directory
	refresher       Refresher
	options         Options
	mu              sync.Mutex
	started, closed bool
	cancel          context.CancelFunc
	summary         Summary
}

func New(d Directory, r Refresher, o Options) (*Service, error) {
	o = o.defaults()
	if d == nil || r == nil || o.Workers < 1 || o.Workers > 16 || o.Tick < time.Millisecond || o.Tick > time.Second || o.PollInterval < o.Tick || o.PollInterval > 5*time.Second || o.MinInterval < time.Millisecond || o.MinInterval > 5*time.Second || o.RetryMin < o.MinInterval || o.RetryMax < o.RetryMin || o.RetryMax > time.Minute {
		return nil, ErrInvalid
	}
	return &Service{directory: d, refresher: r, options: o}, nil
}
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	c := s.cancel
	s.mu.Unlock()
	if c != nil {
		c()
	}
}
func (s *Service) Summary() Summary { s.mu.Lock(); defer s.mu.Unlock(); return s.summary }

var id = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func desiredSet(list []Desired) (map[string]Desired, error) {
	if len(list) > 1000 {
		return nil, ErrDirectory
	}
	out := make(map[string]Desired, len(list))
	for _, v := range list {
		t := v.Target
		if !id.MatchString(t.AccountID) || !id.MatchString(t.TenantID) || (t.Provider != "telegram" && t.Provider != "wecom") || v.Revision < 1 || v.Revision > 9007199254740991 {
			return nil, ErrDirectory
		}
		if _, ok := out[t.AccountID]; ok {
			return nil, ErrDirectory
		}
		out[t.AccountID] = v
	}
	return out, nil
}

type entry struct {
	desired          Desired
	present, running bool
	due              time.Time
	failures         int
	version          uint64
	cancel           context.CancelFunc
}
type job struct {
	account string
	desired Desired
	version uint64
	ctx     context.Context
	started time.Time
}
type completion struct {
	job   job
	delay time.Duration
	err   error
}

func (s *Service) Run(parent context.Context) error {
	if parent == nil {
		return ErrInvalid
	}
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return ErrInvalid
	}
	s.started = true
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.mu.Unlock()
	jobs := make(chan job)
	results := make(chan completion, s.options.Workers)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
		s.mu.Lock()
		s.closed = true
		s.summary.DirectoryReady = false
		s.summary.InFlight = 0
		s.mu.Unlock()
	}()
	for i := 0; i < s.options.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case j := <-jobs:
					delay, err := s.refresher.Refresh(j.ctx, j.desired.Target)
					select {
					case results <- completion{j, delay, err}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	entries := map[string]*entry{}
	var version uint64
	nextPoll := time.Time{}
	healthy := false
	active := 0
	publish := func() {
		n := 0
		for _, e := range entries {
			if e.present {
				n++
			}
		}
		s.mu.Lock()
		s.summary.DirectoryReady = healthy
		s.summary.Desired = n
		s.summary.InFlight = active
		s.mu.Unlock()
	}
	reconcile := func() {
		call, stop := context.WithTimeout(ctx, 2*time.Second)
		list, err := s.directory.AuthorizationTargets(call)
		stop()
		set, e := desiredSet(list)
		if err != nil || e != nil {
			healthy = false
			set = map[string]Desired{}
		} else {
			healthy = true
		}
		for account, old := range entries {
			want, ok := set[account]
			if !ok || want != old.desired {
				if old.cancel != nil {
					old.cancel()
				}
				old.present = false
			}
			if !ok && !old.running {
				delete(entries, account)
			}
		}
		for account, want := range set {
			old, ok := entries[account]
			if !ok {
				entries[account] = &entry{desired: want, present: true}
				continue
			}
			if old.desired != want || !old.present {
				old.desired = want
				old.present = true
				old.due = time.Time{}
				old.failures = 0
			}
		}
		nextPoll = time.Now().Add(s.options.PollInterval)
		publish()
	}
	dispatch := func() {
		if !healthy {
			return
		}
		now := time.Now()
		keys := make([]string, 0, len(entries))
		for k, e := range entries {
			if e.present && !e.running && !now.Before(e.due) {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := entries[keys[i]], entries[keys[j]]
			if a.due.Equal(b.due) {
				return keys[i] < keys[j]
			}
			return a.due.Before(b.due)
		})
		for _, key := range keys {
			if active >= s.options.Workers {
				break
			}
			e := entries[key]
			call, stop := context.WithTimeout(ctx, 30*time.Second)
			version++
			j := job{key, e.desired, version, call, time.Now()}
			select {
			case jobs <- j:
				e.running = true
				e.version = version
				e.cancel = stop
				active++
			case <-ctx.Done():
				stop()
				return
			}
		}
		publish()
	}
	ticker := time.NewTicker(s.options.Tick)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !time.Now().Before(nextPoll) {
			reconcile()
		}
		dispatch()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case result := <-results:
			active--
			e := entries[result.job.account]
			if e == nil || !e.running || e.version != result.job.version {
				return ErrInvalid
			}
			interrupted := errors.Is(result.job.ctx.Err(), context.Canceled)
			e.running = false
			e.cancel()
			e.cancel = nil
			if !e.present {
				delete(entries, result.job.account)
			} else if e.desired != result.job.desired || interrupted {
				e.due = time.Time{}
			} else if result.err != nil {
				e.failures++
				delay := s.options.RetryMin
				for n := 1; n < e.failures && delay < s.options.RetryMax; n++ {
					delay = min(delay*2, s.options.RetryMax)
				}
				e.due = time.Now().Add(delay)
				s.mu.Lock()
				s.summary.Failed++
				s.mu.Unlock()
			} else {
				e.failures = 0
				delay := max(s.options.MinInterval, min(5*time.Second, result.delay))
				e.due = result.job.started.Add(delay)
				e.due = maxTime(e.due, time.Now().Add(s.options.MinInterval))
				s.mu.Lock()
				s.summary.Succeeded++
				s.mu.Unlock()
			}
			publish()
		}
	}
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
