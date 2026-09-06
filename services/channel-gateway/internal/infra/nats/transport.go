// Package natsadapter owns transport mechanics, not Gateway business decisions.
package natsadapter

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	RouteSubject  = "control.channel-route.v1"
	RunSubject    = "execution.run-requested.v1"
	RouteStream   = "CHANNEL_ROUTES_V1"
	RunStream     = "RUN_REQUESTS_V1"
	RouteConsumer = "channel-gateway-routes-v1"
)

type Auth struct{ User, Password, InboxPrefix string }
type Transport struct {
	Conn     *nats.Conn
	JS       jetstream.JetStream
	topology Topology
}

func Connect(url string, topology Topology, auth Auth) (*Transport, error) {
	if err := topology.Validate(); err != nil {
		return nil, err
	}
	if auth.InboxPrefix == "" {
		auth.InboxPrefix = "_INBOX.gateway"
	}
	opts := []nats.Option{nats.Name("channel-gateway"), nats.Timeout(5 * time.Second), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second), nats.CustomInboxPrefix(auth.InboxPrefix)}
	if auth.User != "" {
		opts = append(opts, nats.UserInfo(auth.User, auth.Password))
	}
	// Do not log server URLs or authentication material from asynchronous errors.
	opts = append(opts, nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) {}))
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, errors.New("connect NATS failed")
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, errors.New("initialize JetStream failed")
	}
	return &Transport{Conn: nc, JS: js, topology: topology}, nil
}
func (t *Transport) Close() { t.Conn.Close() }
func (t *Transport) Publish(ctx context.Context, subject, id string, payload []byte) error {
	if subject != RunSubject || id == "" {
		return errors.New("unsupported outbox publication")
	}
	ack, err := t.JS.Publish(ctx, subject, payload, jetstream.WithMsgID(id))
	if err != nil {
		return err
	}
	if ack.Stream != RunStream {
		return errors.New("unexpected publish acknowledgment stream")
	}
	return nil
}
func (t *Transport) Reconcile(ctx context.Context) error {
	for _, want := range t.topology.configs() {
		_, err := t.JS.Stream(ctx, want.Name)
		if errors.Is(err, jetstream.ErrStreamNotFound) {
			if _, err = t.JS.CreateStream(ctx, want); err != nil && !errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	if err := t.verifyStreams(ctx); err != nil {
		return err
	}
	stream, err := t.JS.Stream(ctx, RouteStream)
	if err != nil {
		return err
	}
	_, err = stream.Consumer(ctx, RouteConsumer)
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		_, err = stream.CreateConsumer(ctx, consumerConfig())
	}
	if err != nil {
		return err
	}
	return t.Verify(ctx)
}
func consumerConfig() jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{Durable: RouteConsumer, AckPolicy: jetstream.AckExplicitPolicy, DeliverPolicy: jetstream.DeliverAllPolicy, FilterSubject: RouteSubject, AckWait: 30 * time.Second, MaxAckPending: 64, MaxDeliver: -1, ReplayPolicy: jetstream.ReplayInstantPolicy}
}
func (t *Transport) verifyStreams(ctx context.Context) error {
	for _, want := range t.topology.configs() {
		stream, err := t.JS.Stream(ctx, want.Name)
		if err != nil {
			return err
		}
		info, err := stream.Info(ctx)
		if err != nil {
			return err
		}
		got := info.Config
		if !reflect.DeepEqual(got.Subjects, want.Subjects) || got.Storage != want.Storage || got.Retention != want.Retention || got.Discard != want.Discard || got.MaxBytes != want.MaxBytes || got.MaxMsgSize != want.MaxMsgSize || got.Replicas != want.Replicas || got.MaxAge != 0 || got.MaxMsgs > 0 || got.MaxMsgsPerSubject > 0 || got.Duplicates != want.Duplicates || got.NoAck || got.Sealed || !got.DenyDelete || !got.DenyPurge || got.AllowRollup || got.AllowMsgTTL || got.SubjectTransform != nil || got.RePublish != nil || got.Mirror != nil || len(got.Sources) > 0 || got.DiscardNewPerSubject {
			return fmt.Errorf("incompatible stream %s; explicit migration required", want.Name)
		}
	}
	return nil
}
func (t *Transport) Verify(ctx context.Context) error {
	if err := t.verifyStreams(ctx); err != nil {
		return err
	}
	c, err := t.JS.Consumer(ctx, RouteStream, RouteConsumer)
	if err != nil {
		return err
	}
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	want := consumerConfig()
	got := info.Config
	if got.AckPolicy != want.AckPolicy || got.DeliverPolicy != want.DeliverPolicy || got.FilterSubject != want.FilterSubject || len(got.FilterSubjects) > 0 || got.Durable != want.Durable || got.MaxAckPending != want.MaxAckPending || got.AckWait != want.AckWait || got.DeliverSubject != "" || got.MaxDeliver != -1 || len(got.BackOff) > 0 || got.ReplayPolicy != want.ReplayPolicy || got.InactiveThreshold != 0 || got.HeadersOnly {
		return errors.New("incompatible route consumer")
	}
	return nil
}
