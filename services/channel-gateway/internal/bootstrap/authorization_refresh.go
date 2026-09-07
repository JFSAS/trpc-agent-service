package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/outbound/authorizationpostgres"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/outbound/controlpolicy"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/application/authorizationrefresh"
	ad "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
	catalog "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/application/catalogrefresh"
)

type authorizationDirectory struct{ source *catalog.Service }

func (d authorizationDirectory) AuthorizationTargets(ctx context.Context) ([]authorizationrefresh.Desired, error) {
	accounts, err := d.source.CurrentAccounts(ctx)
	if err != nil {
		return nil, authorizationrefresh.ErrDirectory
	}
	out := make([]authorizationrefresh.Desired, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, authorizationrefresh.Desired{Target: ad.AuthorizationTarget{TenantID: a.TenantID, AccountID: a.ID, Provider: a.Provider}, Revision: a.Revision})
	}
	return out, nil
}

type authorizationInstaller struct {
	store  *authorizationpostgres.Store
	reader ad.AuthorizationReader
}

// A per-call observer captures only the verified age for scheduling; it is not a
// second fetch or a database deadline. Refresh still enforces its own DB anchor.
type ageReader struct {
	reader ad.AuthorizationReader
	age    time.Duration
}

func (r *ageReader) ReadAuthorization(ctx context.Context, t ad.AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (ad.AuthorizationRead, error) {
	out, err := r.reader.ReadAuthorization(ctx, t, stage)
	if err == nil {
		r.age = time.Duration(out.Manifest.AuthorizationMaxAgeMS) * time.Millisecond
	}
	return out, err
}
func (r authorizationInstaller) Refresh(ctx context.Context, t ad.AuthorizationTarget) (time.Duration, error) {
	reader := &ageReader{reader: r.reader}
	err := r.store.Refresh(ctx, t, reader)
	if err != nil {
		return 0, err
	}
	return reader.age / 3, nil
}

func newAuthorizationRefresh(c Config, pool *pgxpool.Pool, directory *catalog.Service) (*policyProjectionRuntime, error) {
	if !c.AuthorizationRefreshEnabled {
		return nil, nil
	}
	if c.AccountSource != "control" || pool == nil || directory == nil {
		return nil, errors.New("authorization refresh requires Control account directory and PostgreSQL")
	}
	options, err := c.Control.clientOptions(c.InstanceID)
	if err != nil {
		return nil, err
	}
	reader, err := controlpolicy.New(controlpolicy.Options{BaseURL: options.BaseURL, ScopeID: options.ScopeID, SourceEpoch: options.SourceEpoch, RootCAs: options.RootCAs, Certificate: options.Certificate})
	if err != nil {
		return nil, errors.New("initialize authorization reader failed")
	}
	store, err := authorizationpostgres.New(pool, c.Control.ScopeID, c.Control.SourceEpoch)
	if err != nil {
		reader.Close()
		return nil, errors.New("initialize authorization installer failed")
	}
	loop, err := authorizationrefresh.New(authorizationDirectory{directory}, authorizationInstaller{store, reader}, authorizationrefresh.Options{})
	if err != nil {
		reader.Close()
		return nil, errors.New("initialize authorization refresher failed")
	}
	return &policyProjectionRuntime{loop: loop, close: func() { loop.Close(); reader.Close() }}, nil
}
