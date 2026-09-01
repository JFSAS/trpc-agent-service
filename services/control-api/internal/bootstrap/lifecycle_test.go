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
