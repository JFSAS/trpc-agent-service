// Package runtimeadapter composes the credential protocol, immutable Session
// store and pinned SDK behind Execution-owned application interfaces.
package runtimeadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/sessionstore"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/adapter/outbound/trpcagent"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/application"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
)

var ErrAlreadyPrepared = errors.New("attempt credential initialization already started")

type Options struct {
	BaseURL string
	// Client is a borrowed mTLS client configured by bootstrap with trust roots
	// and the Workload certificate. The factory never closes the shared transport.
	Client                *http.Client
	RequestTimeout        time.Duration
	MaxResponseBytes      int64
	SnapshotCapacityBytes int
	DrainTimeout          time.Duration
	MaxTrackedAttempts    int
	Observer              application.Observer
}
type Factory struct {
	options   Options
	client    *http.Client
	mu        sync.Mutex
	started   map[attemptKey]time.Time
	openStore func(context.Context, string, sessionstore.Target, int) (candidateStore, error)
}
type attemptKey struct {
	TenantID, RunID, AttemptID string
	LeaseEpoch                 int64
}
type candidateStore interface {
	sessionstore.Store
	Close()
}

func New(o Options) (*Factory, error) {
	u, err := url.Parse(o.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || o.Client == nil || o.RequestTimeout <= 0 || o.MaxResponseBytes <= 0 || o.SnapshotCapacityBytes <= 0 || o.DrainTimeout <= 0 || o.MaxTrackedAttempts <= 0 {
		return nil, domain.ErrInvalid
	}
	client := *o.Client
	// Never forward the execution token or an initialized credential batch to a
	// redirect target, including same-origin redirects.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	f := &Factory{options: o, client: &client, started: make(map[attemptKey]time.Time)}
	f.openStore = func(ctx context.Context, dsn string, t sessionstore.Target, capacity int) (candidateStore, error) {
		return sessionstore.Open(ctx, dsn, t, capacity)
	}
	return f, nil
}

func (f *Factory) Prepare(ctx context.Context, g domain.Grant, p domain.Plan, check func(context.Context) error) (application.AttemptRuntime, error) {
	if check == nil || g.Run.ExecutionDeadline == nil || g.Token == "" || g.AttemptID == "" || g.WorkerID == "" || g.LeaseEpoch <= 0 || p.TenantID != g.Run.Request.Route.TenantID || p.ManifestID != g.Run.Request.Route.ManifestRef || p.ManifestDigest != g.Run.Request.Route.ManifestDigest || p.DeploymentRevisionID != g.Run.Request.Route.DeploymentRevisionID || p.ProfileID == "" || p.ProfileRevision <= 0 {
		return nil, application.ErrManifestInvalid
	}
	if err := check(ctx); err != nil {
		return nil, err
	}
	if err := f.start(g); err != nil {
		return nil, err
	}
	uses, err := requiredUses(p)
	if err != nil {
		return nil, err
	}
	resolveStart := time.Now()
	batch, err := f.resolve(ctx, g, p, uses)
	f.observe(ctx, "credential_resolve", g, resolveStart, err)
	if err != nil {
		return nil, err
	}
	// This map only lives during initialization. Retain just the initialized
	// model value in the attempt; Session credentials live in its owned pool.
	defer func() {
		for key := range batch {
			delete(batch, key)
		}
	}()
	if err = check(ctx); err != nil {
		return nil, err
	}
	t := p.SessionTarget
	storeStart := time.Now()
	target := sessionstore.Target{Host: t.Host, Port: t.Port, Database: t.Database, Username: t.Username, SSLMode: t.SSLMode}
	// Control stores only the DSN password. The published destination, never the
	// credential value, determines the Session connection target.
	dsn, err := sessionstore.CredentialDSN(target, batch[p.SessionCredential])
	if err != nil {
		mapped := storeError(ctx, err)
		f.observe(ctx, "session_open", g, storeStart, mapped)
		return nil, mapped
	}
	store, err := f.openStore(ctx, dsn, target, f.options.SnapshotCapacityBytes)
	if err != nil {
		mapped := storeError(ctx, err)
		stage, _, _ := sessionstore.OpenFailure(err)
		f.observe(ctx, "session_open", g, storeStart, mapped, stage)
		return nil, mapped
	}
	f.observe(ctx, "session_open", g, storeStart, nil)
	return &attempt{grant: g, plan: p, store: store, check: check, modelKey: batch[p.ModelCredential], executor: trpcagent.Executor{CapacityBytes: f.options.SnapshotCapacityBytes, DrainTimeout: f.options.DrainTimeout}, capacity: f.options.SnapshotCapacityBytes}, nil
}
func (f *Factory) observe(ctx context.Context, operation string, g domain.Grant, start time.Time, err error, stages ...string) {
	if f.options.Observer != nil {
		stage := ""
		if len(stages) == 1 {
			stage = stages[0]
		}
		f.options.Observer.Observe(ctx, application.Observation{Stage: stage, Operation: operation, Result: application.ObservationResult(err), TenantID: g.Run.Request.Route.TenantID, RunID: g.Run.Request.RunID, AttemptID: g.AttemptID, Duration: time.Since(start)})
	}
}
func (f *Factory) start(g domain.Grant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for key, until := range f.started {
		if now.After(until) {
			delete(f.started, key)
		}
	}
	key := attemptKey{g.Run.Request.Route.TenantID, g.Run.Request.RunID, g.AttemptID, g.LeaseEpoch}
	if _, ok := f.started[key]; ok {
		return ErrAlreadyPrepared
	}
	if len(f.started) >= f.options.MaxTrackedAttempts {
		return application.ErrDependency
	}
	if !g.Run.ExecutionDeadline.After(now) {
		return context.DeadlineExceeded
	}
	// Keep the tombstone on failure and on Close. A new Attempt is required after
	// response loss; the original Attempt must not silently mix credential values.
	f.started[key] = *g.Run.ExecutionDeadline
	return nil
}

func requiredUses(p domain.Plan) ([]domain.CredentialUse, error) {
	if p.SessionCredential.CredentialID == "" || p.SessionCredential.Purpose != "dsn" {
		return nil, application.ErrManifestInvalid
	}
	uses := []domain.CredentialUse{}
	if p.ModelCredential.CredentialID != "" {
		if p.ModelCredential.Purpose != "api_key" {
			return nil, application.ErrManifestInvalid
		}
		uses = append(uses, p.ModelCredential)
	} else if p.ModelCredential.Purpose != "" || p.ModelCredential.AudienceDigest != "" {
		return nil, application.ErrManifestInvalid
	}
	uses = append(uses, p.SessionCredential)
	seen := map[domain.CredentialUse]bool{}
	for _, use := range uses {
		if !domain.DigestValid(use.AudienceDigest) || seen[use] {
			return nil, application.ErrManifestInvalid
		}
		seen[use] = true
	}
	return uses, nil
}

type useWire struct {
	CredentialID   string `json:"credential_id"`
	Purpose        string `json:"purpose"`
	AudienceDigest string `json:"audience_digest"`
}
type resolveRequest struct {
	ExecutionToken string    `json:"execution_token"`
	ManifestID     string    `json:"manifest_id"`
	ManifestDigest string    `json:"manifest_digest"`
	Uses           []useWire `json:"uses"`
}
type credentialWire struct {
	CredentialID       string `json:"credential_id"`
	Purpose            string `json:"purpose"`
	AudienceDigest     string `json:"audience_digest"`
	CredentialRevision int64  `json:"credential_revision"`
	Value              string `json:"value"`
}
type batchWire struct {
	TenantID        string           `json:"tenant_id"`
	ProfileID       string           `json:"profile_id"`
	ProfileRevision int64            `json:"profile_revision_number"`
	RunID           string           `json:"run_id"`
	AttemptID       string           `json:"attempt_id"`
	WorkerID        string           `json:"worker_id"`
	LeaseEpoch      int64            `json:"lease_epoch"`
	ManifestID      string           `json:"manifest_id"`
	ManifestDigest  string           `json:"manifest_digest"`
	Credentials     []credentialWire `json:"credentials"`
}

func (f *Factory) resolve(ctx context.Context, g domain.Grant, p domain.Plan, uses []domain.CredentialUse) (map[domain.CredentialUse]string, error) {
	values := make([]useWire, 0, len(uses))
	for _, use := range uses {
		values = append(values, useWire{use.CredentialID, use.Purpose, use.AudienceDigest})
	}
	body, err := json.Marshal(resolveRequest{g.Token, p.ManifestID, p.ManifestDigest, values})
	if err != nil {
		return nil, application.ErrManifestInvalid
	}
	defer clear(body)
	requestCtx, cancel := context.WithTimeout(ctx, f.options.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, strings.TrimRight(f.options.BaseURL, "/")+"/internal/v1/runtime-profiles/credentials/resolve", bytes.NewReader(body))
	if err != nil {
		return nil, application.ErrDependency
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := f.client.Do(req)
	if err != nil {
		return nil, dependencyError(ctx)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return nil, dependencyError(ctx)
		}
		return nil, application.ErrCredentialDenied
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, application.ErrCredentialDenied
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, f.options.MaxResponseBytes+1))
	defer clear(raw)
	if err != nil {
		return nil, dependencyError(ctx)
	}
	if int64(len(raw)) > f.options.MaxResponseBytes {
		return nil, application.ErrCredentialDenied
	}
	var batch batchWire
	if err = decodeBatch(raw, &batch); err != nil {
		return nil, application.ErrCredentialDenied
	}
	if batch.TenantID != p.TenantID || batch.ProfileID != p.ProfileID || batch.ProfileRevision != p.ProfileRevision || batch.RunID != g.Run.Request.RunID || batch.AttemptID != g.AttemptID || batch.WorkerID != g.WorkerID || batch.LeaseEpoch != g.LeaseEpoch || batch.ManifestID != p.ManifestID || batch.ManifestDigest != p.ManifestDigest || len(batch.Credentials) != len(uses) {
		return nil, application.ErrCredentialDenied
	}
	expected := map[domain.CredentialUse]bool{}
	for _, use := range uses {
		expected[use] = true
	}
	out := make(map[domain.CredentialUse]string, len(uses))
	for i, c := range batch.Credentials {
		use := domain.CredentialUse{CredentialID: c.CredentialID, Purpose: c.Purpose, AudienceDigest: c.AudienceDigest}
		if !expected[use] || c.CredentialRevision <= 0 || strings.TrimSpace(c.Value) == "" || strings.ContainsAny(c.Value, "\x00\r\n") {
			return nil, application.ErrCredentialDenied
		}
		if _, exists := out[use]; exists {
			return nil, application.ErrCredentialDenied
		}
		out[use] = c.Value
		batch.Credentials[i].Value = ""
	}
	return out, nil
}
func dependencyError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return application.ErrDependency
}
func storeError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case errors.Is(err, sessionstore.ErrPreparation):
		return application.ErrSessionPreparation
	case errors.Is(err, sessionstore.ErrIdentity):
		return application.ErrCredentialDenied
	case errors.Is(err, sessionstore.ErrCorrupt), errors.Is(err, sessionstore.ErrConflict), errors.Is(err, sessionstore.ErrCapacity):
		return application.ErrSessionInvalid
	default:
		return application.ErrDependency
	}
}

var _ application.RuntimeFactory = (*Factory)(nil)
