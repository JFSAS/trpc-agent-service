package bootstrap

import (
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/agent"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/deployment"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant"
)

// App owns the modules and process lifecycle assembled for Control API.
type App struct {
	tenant         *tenant.Module
	agent          *agent.Module
	runtimeProfile *runtimeprofile.Module
	deployment     *deployment.Module
	channelBinding *channelbinding.Module
}

// New creates shared infrastructure and composes every Control API module.
// Infrastructure dependencies will be made explicit as vertical slices arrive.
func New(Config) (*App, error) {
	return &App{
		tenant:         tenant.NewModule(tenant.Dependencies{}),
		agent:          agent.NewModule(agent.Dependencies{}),
		runtimeProfile: runtimeprofile.NewModule(runtimeprofile.Dependencies{}),
		deployment:     deployment.NewModule(deployment.Dependencies{}),
		channelBinding: channelbinding.NewModule(channelbinding.Dependencies{}),
	}, nil
}
