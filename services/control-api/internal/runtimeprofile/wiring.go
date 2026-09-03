// Package runtimeprofile composes Runtime Profile authoring, validation, and
// immutable publication capabilities.
package runtimeprofile

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"

	httpadapter "github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/adapter/inbound/http"
	postgresadapter "github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/adapter/outbound/postgres"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runtimeprofile/application"
)

// Dependencies lists process-owned dependencies required by the Runtime
// Profile module.
type Dependencies struct {
	DB           postgresadapter.DB
	Routes       gin.IRouter
	Authenticate gin.HandlerFunc
	TenantAccess application.TenantAccess
}

// Module is the assembled Runtime Profile module.
type Module struct {
	Service *application.Service
}

// NewModule assembles the Runtime Profile module without starting process
// resources.
func NewModule(deps Dependencies) (*Module, error) {
	if deps.DB == nil || deps.Routes == nil || deps.Authenticate == nil || deps.TenantAccess == nil {
		return nil, errors.New("runtime profile: database, routes, authentication, and tenant access are required")
	}
	store := postgresadapter.NewStore(deps.DB)
	service := application.NewService(application.Dependencies{
		Store: store, TenantAccess: deps.TenantAccess,
		NewProfileID:  func() (string, error) { return generateID("rpf") },
		NewRevisionID: func() (string, error) { return generateID("rpr") },
		Now:           time.Now,
	})
	protected := deps.Routes.Group("", deps.Authenticate)
	httpadapter.NewHandler(service).Register(protected)
	return &Module{Service: service}, nil
}
