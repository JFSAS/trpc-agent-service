package bootstrap

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	source "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
	"github.com/liuzengh/trpc-agent-service/platform/channel/authorization/controlhttp"
	install "github.com/liuzengh/trpc-agent-service/platform/channel/authorization/postgres"
	scheduler "github.com/liuzengh/trpc-agent-service/platform/channel/authorization/refresh"
)

// Omission disables acquisition. URL is explicit: the channel internal listener
// need not be the same listener as Worker's existing manifest/runtime API.
type AuthorizationConfig struct {
	URL         string `json:"url"`
	ScopeID     string `json:"scope_id"`
	SourceEpoch string `json:"source_epoch"`
}

var authorizationScope = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var authorizationEpoch = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (c AuthorizationConfig) validate() error {
	u, e := url.Parse(c.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || !authorizationScope.MatchString(c.ScopeID) || !authorizationEpoch.MatchString(c.SourceEpoch) {
		return errors.New("Worker authorization source configuration invalid")
	}
	return nil
}

type authorizationTargets struct {
	pool  *pgxpool.Pool
	scope string
}

func (d authorizationTargets) AuthorizationTargets(ctx context.Context) ([]scheduler.Desired, error) {
	if ctx == nil || d.pool == nil {
		return nil, scheduler.ErrDirectory
	}
	rows, e := d.pool.Query(ctx, `SELECT request_json->'Route'->>'TenantID',request_json->'Route'->>'AccountID',request_json->'Route'->>'Provider',max((request_json->'Authorization'->>'generation')::bigint) FROM execution_runs WHERE status IN ('QUEUED','RUNNING','RETRY_WAIT') AND request_json->'Authorization'->>'scope_id'=$1 GROUP BY 1,2,3 ORDER BY 1,2,3 LIMIT 1001`, d.scope)
	if e != nil {
		return nil, scheduler.ErrDirectory
	}
	defer rows.Close()
	out := []scheduler.Desired{}
	for rows.Next() {
		var d scheduler.Desired
		if e = rows.Scan(&d.Target.TenantID, &d.Target.AccountID, &d.Target.Provider, &d.Revision); e != nil {
			return nil, scheduler.ErrDirectory
		}
		out = append(out, d)
		if len(out) > 1000 {
			return nil, scheduler.ErrDirectory
		}
	}
	if rows.Err() != nil {
		return nil, scheduler.ErrDirectory
	}
	return out, nil
}

type authorizationObservedReader struct {
	reader source.AuthorizationReader
	age    time.Duration
}

func (r *authorizationObservedReader) ReadAuthorization(ctx context.Context, t source.AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (source.AuthorizationRead, error) {
	v, e := r.reader.ReadAuthorization(ctx, t, stage)
	if e == nil {
		r.age = time.Duration(v.Manifest.AuthorizationMaxAgeMS) * time.Millisecond
	}
	return v, e
}

type authorizationInstaller struct {
	store  *install.Store
	reader source.AuthorizationReader
}

func (i authorizationInstaller) Refresh(ctx context.Context, t source.AuthorizationTarget) (time.Duration, error) {
	r := &authorizationObservedReader{reader: i.reader}
	if e := i.store.Refresh(ctx, t, r); e != nil {
		return 0, e
	}
	return r.age / 3, nil
}

type authorizationRuntime struct {
	loop   *scheduler.Service
	reader *controlhttp.Client
	once   sync.Once
}

func (r *authorizationRuntime) Run(ctx context.Context) error { return r.loop.Run(ctx) }
func (r *authorizationRuntime) Close()                        { r.once.Do(func() { r.loop.Close(); r.reader.Close() }) }
func newAuthorizationRuntime(c Config, pool *pgxpool.Pool) (*authorizationRuntime, error) {
	if c.Authorization == nil {
		return nil, nil
	}
	a := *c.Authorization
	if e := a.validate(); e != nil {
		return nil, e
	}
	if pool == nil {
		return nil, errors.New("Worker authorization database required")
	}
	cert, roots, e := certificate(c.ControlTLS.CertFile, c.ControlTLS.KeyFile, c.ControlTLS.CAFile)
	if e != nil {
		return nil, e
	}
	reader, e := controlhttp.New(controlhttp.Options{BaseURL: a.URL, ScopeID: a.ScopeID, SourceEpoch: a.SourceEpoch, RootCAs: roots, Certificate: cert})
	if e != nil {
		return nil, errors.New("Worker authorization reader invalid")
	}
	store, e := install.New(pool, a.ScopeID, a.SourceEpoch, install.Worker)
	if e != nil {
		reader.Close()
		return nil, e
	}
	loop, e := scheduler.New(authorizationTargets{pool, a.ScopeID}, authorizationInstaller{store, reader}, scheduler.Options{})
	if e != nil {
		reader.Close()
		return nil, e
	}
	return &authorizationRuntime{loop: loop, reader: reader}, nil
}
