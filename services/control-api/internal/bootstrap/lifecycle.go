package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Run manages the HTTP server, PostgreSQL pool, and graceful shutdown.
func (a *App) Run(ctx context.Context) error {
	if a == nil {
		return errors.New("control-api bootstrap: nil app")
	}
	if a.server == nil {
		return errors.New("control-api bootstrap: nil HTTP server")
	}
	if a.shutdownTimeout <= 0 {
		a.shutdownTimeout = 10 * time.Second
	}
	if a.database != nil {
		defer a.database.Close()
	}

	serveResult := make(chan error, 1)
	go func() {
		serveResult <- a.server.ListenAndServe()
	}()

	select {
	case err := <-serveResult:
		if err != nil {
			return fmt.Errorf("serve control API: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown control API: %w", err)
		}
		if err := <-serveResult; err != nil {
			return fmt.Errorf("serve control API during shutdown: %w", err)
		}
		return nil
	}
}
