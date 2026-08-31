package bootstrap

import (
	"context"
	"testing"
)

func TestAppRunStopsWhenContextIsCanceled(t *testing.T) {
	app, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := app.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestNilAppCannotRun(t *testing.T) {
	var app *App
	if err := app.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
}
