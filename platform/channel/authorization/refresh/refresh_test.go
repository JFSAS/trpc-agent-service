package refresh

import (
	"context"
	"errors"
	"fmt"
	domain "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
	"sync"
	"testing"
	"time"
)

type directoryFunc func(context.Context) ([]Desired, error)

func (f directoryFunc) AuthorizationTargets(c context.Context) ([]Desired, error) { return f(c) }

type refreshFunc func(context.Context, domain.AuthorizationTarget) (time.Duration, error)

func (f refreshFunc) Refresh(c context.Context, t domain.AuthorizationTarget) (time.Duration, error) {
	return f(c, t)
}
func target(i int) Desired {
	return Desired{domain.AuthorizationTarget{TenantID: "tenant", AccountID: fmt.Sprintf("account-%d", i), Provider: "wecom"}, 1}
}
func testOptions() Options {
	return Options{Workers: 2, Tick: time.Millisecond, PollInterval: 5 * time.Millisecond, MinInterval: 5 * time.Millisecond, RetryMin: 20 * time.Millisecond, RetryMax: 40 * time.Millisecond}
}
func await(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition timed out")
}
func start(t *testing.T, s *Service) func() {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	return func() {
		s.Close()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("shutdown timed out")
		}
	}
}
func TestBoundedFairNonoverlappingRefresh(t *testing.T) {
	var mu sync.Mutex
	active, maxActive := 0, 0
	running := map[string]bool{}
	seen := map[string]bool{}
	overlap := false
	list := []Desired{}
	for i := 0; i < 8; i++ {
		list = append(list, target(i))
	}
	s, err := New(directoryFunc(func(context.Context) ([]Desired, error) { return list, nil }), refreshFunc(func(ctx context.Context, v domain.AuthorizationTarget) (time.Duration, error) {
		mu.Lock()
		if running[v.AccountID] {
			overlap = true
		}
		running[v.AccountID] = true
		active++
		maxActive = max(maxActive, active)
		seen[v.AccountID] = true
		mu.Unlock()
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Millisecond):
		}
		mu.Lock()
		active--
		running[v.AccountID] = false
		mu.Unlock()
		return time.Millisecond, nil
	}), testOptions())
	if err != nil {
		t.Fatal(err)
	}
	stop := start(t, s)
	defer stop()
	await(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) == 8 })
	mu.Lock()
	defer mu.Unlock()
	if overlap || maxActive > 2 {
		t.Fatalf("overlap=%v max=%d", overlap, maxActive)
	}
}
func TestDirectoryOutageCancelsAndResumes(t *testing.T) {
	var mu sync.Mutex
	unavailable := false
	revision := int64(1)
	calls, canceled := 0, 0
	s, _ := New(directoryFunc(func(context.Context) ([]Desired, error) {
		mu.Lock()
		defer mu.Unlock()
		if unavailable {
			return nil, ErrDirectory
		}
		v := target(1)
		v.Revision = revision
		return []Desired{v}, nil
	}), refreshFunc(func(ctx context.Context, _ domain.AuthorizationTarget) (time.Duration, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-ctx.Done()
		mu.Lock()
		canceled++
		mu.Unlock()
		return 0, ctx.Err()
	}), testOptions())
	stop := start(t, s)
	defer stop()
	await(t, func() bool { mu.Lock(); defer mu.Unlock(); return calls == 1 })
	mu.Lock()
	unavailable = true
	mu.Unlock()
	await(t, func() bool { return !s.Summary().DirectoryReady && s.Summary().InFlight == 0 })
	mu.Lock()
	if canceled != 1 {
		t.Error("missing cancellation")
	}
	unavailable = false
	mu.Unlock()
	await(t, func() bool { mu.Lock(); defer mu.Unlock(); return calls == 2 })
	mu.Lock()
	revision++
	mu.Unlock()
	await(t, func() bool { mu.Lock(); defer mu.Unlock(); return calls == 3 && canceled == 2 })
}
func TestRetryBackoffAndClose(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	s, _ := New(directoryFunc(func(context.Context) ([]Desired, error) { return []Desired{target(1)}, nil }), refreshFunc(func(context.Context, domain.AuthorizationTarget) (time.Duration, error) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		return 0, errors.New("failure")
	}), testOptions())
	stop := start(t, s)
	await(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(times) >= 3 })
	stop()
	mu.Lock()
	defer mu.Unlock()
	if times[1].Sub(times[0]) < 20*time.Millisecond || times[2].Sub(times[1]) < 40*time.Millisecond {
		t.Fatal("backoff violated")
	}
	if !errors.Is(s.Run(context.Background()), ErrInvalid) {
		t.Fatal("Run reused")
	}
	if s.Summary().DirectoryReady || s.Summary().InFlight != 0 {
		t.Fatal("closed summary")
	}
}
func TestInvalidDirectoryAndOptions(t *testing.T) {
	for _, list := range [][]Desired{{target(1), target(1)}, {{Target: target(1).Target, Revision: 0}}, make([]Desired, 1001)} {
		if _, err := desiredSet(list); !errors.Is(err, ErrDirectory) {
			t.Fatal("invalid directory accepted")
		}
	}
	if _, err := New(nil, nil, Options{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil dependencies")
	}
	s, _ := New(directoryFunc(func(context.Context) ([]Desired, error) { return nil, nil }), refreshFunc(func(context.Context, domain.AuthorizationTarget) (time.Duration, error) { return 0, nil }), Options{})
	s.Close()
	if !errors.Is(s.Run(context.Background()), ErrInvalid) {
		t.Fatal("closed service started")
	}
}
