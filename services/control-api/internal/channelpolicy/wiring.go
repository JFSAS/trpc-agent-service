// Package channelpolicy composes Control-owned Session/Quota definition publishing.
package channelpolicy

import (
	"crypto/rand"
	"encoding/base64"
	"github.com/gin-gonic/gin"
	httpadapter "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/adapter/inbound/http"
	postgresadapter "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/adapter/outbound/postgres"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/application"
)

type DB interface {
	postgresadapter.WriteDB
	postgresadapter.DB
}

type Dependencies struct {
	DB                    DB
	Routes                gin.IRouter
	Authenticate          gin.HandlerFunc
	Access                application.TenantAccess
	TransactionAuthorizer postgresadapter.TenantAuthorizer
	Signer                application.RequestSigner
}
type Module struct {
	Publisher *application.Publisher
	Queries   *application.QueryService
}

func NewModule(d Dependencies) (*Module, error) {
	if d.Routes == nil || d.Authenticate == nil {
		return nil, application.ErrUnavailable
	}
	store, err := postgresadapter.NewPublicationStore(d.DB, d.TransactionAuthorizer)
	if err != nil {
		return nil, err
	}
	publisher, err := application.NewPublisher(application.PublisherDependencies{Store: store, Access: d.Access, Signer: d.Signer, NewID: generateID})
	if err != nil {
		return nil, err
	}
	reader, err := postgresadapter.NewReader(d.DB)
	if err != nil {
		return nil, err
	}
	queries, err := application.NewQueryService(reader, d.Access)
	if err != nil {
		return nil, err
	}
	routes := d.Routes.Group("", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() }, d.Authenticate)
	httpadapter.NewHandler(publisher).Register(routes)
	httpadapter.NewQueryHandler(queries).Register(routes)
	return &Module{Publisher: publisher, Queries: queries}, nil
}

func generateID(prefix string) (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", application.ErrUnavailable
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(value), nil
}
