package bootstrap

import (
	"context"
	"errors"
)

// Run manages the process lifecycle. HTTP and background jobs will be started
// here after their first implementations exist.
func (a *App) Run(ctx context.Context) error {
	if a == nil {
		return errors.New("control-api bootstrap: nil app")
	}

	<-ctx.Done()
	return nil
}
