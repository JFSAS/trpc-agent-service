package bootstrap

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAppRunStopsWhenContextIsCanceled(t *testing.T) {
	server := newLifecycleServerStub()
	app := &App{server: server, shutdownTimeout: time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := app.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if server.shutdownCalls != 1 {
		t.Fatalf("Shutdown calls = %d, want 1", server.shutdownCalls)
	}
}

func TestNilAppCannotRun(t *testing.T) {
	var app *App
	if err := app.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
}

func TestAppRunReturnsListenerFailure(t *testing.T) {
	wantErr := errors.New("listener failed")
	app := &App{
		server:          &lifecycleServerStub{serveErr: wantErr},
		shutdownTimeout: time.Second,
	}

	err := app.Run(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want listener failure", err)
	}
}

type lifecycleServerStub struct {
	serveErr      error
	done          chan struct{}
	shutdownCalls int
	closeOnce     sync.Once
}

func newLifecycleServerStub() *lifecycleServerStub {
	return &lifecycleServerStub{done: make(chan struct{})}
}

func (s *lifecycleServerStub) ListenAndServe() error {
	if s.serveErr != nil {
		return s.serveErr
	}
	<-s.done
	return nil
}

func (s *lifecycleServerStub) Shutdown(context.Context) error {
	s.shutdownCalls++
	if s.done != nil {
		s.closeOnce.Do(func() { close(s.done) })
	}
	return nil
}

func TestAppRunStopsBothListenersOnCancellation(t *testing.T) {
	public, internal := newLifecycleServerStub(), newLifecycleServerStub()
	app := &App{server: public, internalServer: internal, shutdownTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if public.shutdownCalls != 1 || internal.shutdownCalls != 1 {
		t.Fatal("both listeners must be stopped")
	}
}
func TestAppRunInternalFailureStopsPublicListener(t *testing.T) {
	public := newLifecycleServerStub()
	internal := &lifecycleServerStub{serveErr: errors.New("internal listener failure")}
	app := &App{server: public, internalServer: internal, shutdownTimeout: time.Second}
	if err := app.Run(context.Background()); !errors.Is(err, internal.serveErr) {
		t.Fatal("internal failure not returned", err)
	}
	if public.shutdownCalls != 1 || internal.shutdownCalls != 1 {
		t.Fatal("listener orphaned")
	}
}

type policyLifecycleStub struct {
	started, stopped chan struct{}
	release          <-chan struct{}
}

func (p *policyLifecycleStub) Run(ctx context.Context) error {
	close(p.started)
	<-ctx.Done()
	if p.release != nil {
		<-p.release
	}
	close(p.stopped)
	return nil
}
func TestPolicyRelayLifecycleStartsAndDrainsOnCancellation(t *testing.T) {
	policy := &policyLifecycleStub{started: make(chan struct{}), stopped: make(chan struct{})}
	app := &App{server: newLifecycleServerStub(), policyRelay: policy, shutdownTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	select {
	case <-policy.started:
	case <-time.After(time.Second):
		t.Fatal("policy relay not started")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown not bounded")
	}
	select {
	case <-policy.stopped:
	default:
		t.Fatal("App returned before policy relay drained")
	}
}
func TestPolicyRelayStopsOnListenerFailure(t *testing.T) {
	policy := &policyLifecycleStub{started: make(chan struct{}), stopped: make(chan struct{})}
	failure := errors.New("listener failure")
	app := &App{server: &lifecycleServerStub{serveErr: failure}, policyRelay: policy, shutdownTimeout: time.Second}
	if err := app.Run(context.Background()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	select {
	case <-policy.stopped:
	default:
		t.Fatal("policy relay orphaned after listener failure")
	}
}
func TestPolicyRelayShutdownDeadline(t *testing.T) {
	release := make(chan struct{})
	policy := &policyLifecycleStub{started: make(chan struct{}), stopped: make(chan struct{}), release: release}
	app := &App{server: newLifecycleServerStub(), policyRelay: policy, shutdownTimeout: 20 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := app.Run(ctx)
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("policy shutdown deadline not applied", err)
	}
	select {
	case <-policy.stopped:
	case <-time.After(time.Second):
		t.Fatal("test relay did not exit")
	}
}
