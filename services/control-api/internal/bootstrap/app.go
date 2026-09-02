package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/admin"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/agent"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/deployment"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/infra/httpserver"
	sharedpostgres "github.com/liuzengh/trpc-agent-service/services/control-api/internal/infra/postgres"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant"
	"github.com/liuzengh/trpc-agent-service/services/control-api/migrations"
)

type serverLifecycle interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

// App owns the modules and process lifecycle assembled for Control API.
type App struct {
	database        *pgxpool.Pool
	server          serverLifecycle
	shutdownTimeout time.Duration
	identity        *identity.Module
	admin           *admin.Module
	tenant          *tenant.Module
	agent           *agent.Module
	runtimeProfile  *runtimeprofile.Module
	deployment      *deployment.Module
	channelBinding  *channelbinding.Module
}

// New creates shared infrastructure and composes every Control API module.
func New(ctx context.Context, config Config) (*App, error) {
	pool, err := sharedpostgres.Open(ctx, config.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := sharedpostgres.Migrate(ctx, pool, migrations.Files); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate control database: %w", err)
	}

	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		pool.Close()
		return nil, fmt.Errorf("configure HTTP proxy trust: %w", err)
	}
	// Gin's default debug recovery dumps request headers, including Cookie.
	// Recovery remains silent until telemetry provides a structured redactor.
	router.Use(gin.RecoveryWithWriter(nil))
	router.GET("/healthz", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	identityModule, err := identity.NewModule(identity.Dependencies{
		DB:              pool,
		Routes:          router,
		SessionLifetime: config.SessionLifetime,
		CookieName:      config.SessionCookieName,
		CookieDomain:    config.SessionCookieDomain,
		CookieSecure:    config.SessionCookieSecure,
		CookieSameSite:  http.SameSiteLaxMode,
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("assemble identity: %w", err)
	}
	tenantModule, err := tenant.NewModule(tenant.Dependencies{
		DB:           pool,
		Routes:       router,
		Authenticate: identityModule.AuthenticationMiddleware(),
		Accounts:     activeAccountLookup{accounts: identityModule.Accounts},
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("assemble tenant: %w", err)
	}
	if err := ensureInitialPlatformOperator(ctx, pool, config); err != nil {
		pool.Close()
		return nil, err
	}
	adminModule, err := admin.NewModule(admin.Dependencies{
		DB:           pool,
		Routes:       router,
		Authenticate: identityModule.AuthenticationMiddleware(),
		Accounts:     identityModule.Accounts,
		Tenants:      tenantModule.Service,
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("assemble admin: %w", err)
	}
	agentModule, err := agent.NewModule(agent.Dependencies{
		DB: pool, Routes: router,
		Authenticate: identityModule.AuthenticationMiddleware(),
		TenantAccess: activeTenantMemberLookup{tenants: tenantModule.Service},
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("assemble agent: %w", err)
	}

	return &App{
		database:        pool,
		server:          httpserver.New(config.HTTPAddress, router),
		shutdownTimeout: config.ShutdownTimeout,
		identity:        identityModule,
		admin:           adminModule,
		tenant:          tenantModule,
		agent:           agentModule,
		runtimeProfile:  runtimeprofile.NewModule(runtimeprofile.Dependencies{}),
		deployment:      deployment.NewModule(deployment.Dependencies{}),
		channelBinding:  channelbinding.NewModule(channelbinding.Dependencies{}),
	}, nil
}
