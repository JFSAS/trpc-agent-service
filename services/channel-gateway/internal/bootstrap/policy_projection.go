package bootstrap

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/inbound/policynats"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/outbound/controlpolicy"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/outbound/policypostgres"
	"github.com/nats-io/nats.go/jetstream"
)

type policyLoop interface{ Run(context.Context) error }
type policyProjectionRuntime struct {
	loop  policyLoop
	close func()
	once  sync.Once
}

func (r *policyProjectionRuntime) Run(ctx context.Context) error { return r.loop.Run(ctx) }
func (r *policyProjectionRuntime) Close()                        { r.once.Do(r.close) }

// newPolicyProjection reads existing topology only. Reconciler privileges must
// never be granted to a runtime just so startup can create its own durable.
// This enables historical projection, not external-principal authorization.
func newPolicyProjection(ctx context.Context, c Config, pool *pgxpool.Pool, js jetstream.JetStream) (result *policyProjectionRuntime, err error) {
	if !c.PolicyProjectionEnabled {
		return nil, nil
	}
	if ctx == nil || pool == nil || js == nil || c.AccountSource != "control" || !c.Topology.HasStream(wire.AccessPolicyStream) || !c.Topology.HasPolicyScope(c.Control.ScopeID) {
		return nil, errors.New("policy projection dependencies are required")
	}
	options, err := c.Control.clientOptions(c.InstanceID)
	if err != nil {
		return nil, err
	}
	reader, err := controlpolicy.New(controlpolicy.Options{BaseURL: options.BaseURL, ScopeID: options.ScopeID, SourceEpoch: options.SourceEpoch, RootCAs: options.RootCAs, Certificate: options.Certificate})
	if err != nil {
		return nil, errors.New("initialize policy reader failed")
	}
	defer func() {
		if err != nil {
			reader.Close()
		}
	}()
	store, err := policypostgres.New(pool, c.Control.ScopeID, c.Control.SourceEpoch)
	if err != nil {
		return nil, errors.New("initialize policy store failed")
	}
	stream, err := js.Stream(ctx, wire.AccessPolicyStream)
	if err != nil {
		return nil, errors.New("policy stream unavailable")
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return nil, errors.New("policy source unavailable")
	}
	durable := policynats.DurableName(c.Control.ScopeID)
	messages, err := stream.Consumer(ctx, durable)
	if err != nil {
		return nil, errors.New("policy durable unavailable; reconcile required")
	}
	consumer, err := policynats.New(messages, stream, reader, store, policynats.Options{ScopeID: c.Control.ScopeID, SourceEpoch: c.Control.SourceEpoch, Durable: durable, StreamCreated: info.Created})
	if err != nil {
		return nil, errors.New("initialize policy consumer failed")
	}
	if err = consumer.Initialize(ctx); err != nil {
		return nil, errors.New("policy projection initialization rejected")
	}
	return &policyProjectionRuntime{loop: consumer, close: reader.Close}, nil
}
