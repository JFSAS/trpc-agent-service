package bootstrap

import (
	"context"
	control "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/adapter/outbound/controlhttp"
	telegram "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/adapter/outbound/telegrampreflight"
	app "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/application/preflight"
)

// preflightRuntime has no catalog/permit dependency. Control grants short-lived
// diagnostic authority independently of the enabled-account runtime lifetime.
type preflightRuntime struct {
	runner  *app.Runner
	control *control.PreflightClient
	probe   *telegram.Client
}

func newPreflight(c Config, boot string) (*preflightRuntime, error) {
	if c.AccountSource != "control" || !c.TelegramPreflightEnabled {
		return nil, nil
	}
	cfg, err := app.NewConfig(c.Control.ScopeID, c.Control.SourceEpoch, c.Control.PublicOrigin)
	if err != nil {
		return nil, err
	}
	options, err := c.Control.clientOptions(c.InstanceID)
	if err != nil {
		return nil, err
	}
	client, err := control.NewPreflight(options)
	if err != nil {
		return nil, err
	}
	probe := telegram.New()
	runner, err := app.NewRunner(client, probe, cfg, boot)
	if err != nil {
		client.Close()
		probe.Close()
		return nil, err
	}
	return &preflightRuntime{runner: runner, control: client, probe: probe}, nil
}
func (r *preflightRuntime) Run(ctx context.Context) error { return r.runner.Run(ctx) }
func (r *preflightRuntime) Close()                        { r.control.Close(); r.probe.Close() }
