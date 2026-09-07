// Package policynats hydrates retained notifications into durable policy history.
// ACK proves processing/history persistence only, never current authorization.
package policynats

import (
	"context"
	"errors"
	"regexp"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

var (
	ErrInvalid = errors.New("POLICY_CONSUMER_INVALID")
	ErrSource  = errors.New("POLICY_CONSUMER_SOURCE")
	ErrEvent   = errors.New("POLICY_CONSUMER_EVENT")
	ErrRead    = errors.New("POLICY_CONSUMER_READ")
	ErrPersist = errors.New("POLICY_CONSUMER_PERSIST")
	ErrACK     = errors.New("POLICY_CONSUMER_ACK")
)

type Messages interface {
	Next(...jetstream.FetchOpt) (jetstream.Msg, error)
	Info(context.Context) (*jetstream.ConsumerInfo, error)
}
type Stream interface {
	Info(context.Context, ...jetstream.StreamInfoOpt) (*jetstream.StreamInfo, error)
}
type Reader interface {
	Fetch(context.Context, wire.AccessPolicyEvent) (wire.AccessPolicyDocument, error)
}
type Store interface {
	ObserveBroker(context.Context, time.Time, uint64, uint64) error
	Processed(context.Context, time.Time, uint64, wire.AccessPolicyEvent) (bool, error)
	RecordProcessed(context.Context, time.Time, uint64, wire.AccessPolicyEvent) error
	BindSource(context.Context, time.Time) error
	Apply(context.Context, wire.AccessPolicyEvent, wire.AccessPolicyDocument) (domain.PolicyContinuity, error)
}
type Options struct {
	ScopeID, SourceEpoch, Durable string
	StreamCreated                 time.Time
}
type Consumer struct {
	messages Messages
	stream   Stream
	reader   Reader
	store    Store
	options  Options
}

var scopePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var epochPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var durablePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func New(m Messages, s Stream, r Reader, p Store, o Options) (*Consumer, error) {
	if m == nil || s == nil || r == nil || p == nil || !scopePattern.MatchString(o.ScopeID) || !epochPattern.MatchString(o.SourceEpoch) || !durablePattern.MatchString(o.Durable) || o.Durable != DurableName(o.ScopeID) || o.StreamCreated.IsZero() {
		return nil, ErrInvalid
	}
	return &Consumer{messages: m, stream: s, reader: r, store: p, options: o}, nil
}

// DurableName gives every scope a separate full-subject subscription; replicas
// of the same scope share the same durable and the same PostgreSQL projection.
func DurableName(scope string) string { return wire.PolicyConsumerName(scope) }

// Config describes a scope-owned durable. Different scope owners must use
// distinct durable names because every durable sees the whole retained subject.
func Config(name string) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{Durable: name, DeliverPolicy: jetstream.DeliverAllPolicy, AckPolicy: jetstream.AckExplicitPolicy, FilterSubject: wire.AccessPolicySubject, AckWait: 30 * time.Second, MaxAckPending: 1, MaxDeliver: -1, ReplayPolicy: jetstream.ReplayInstantPolicy}
}
func (c *Consumer) verify(ctx context.Context) (*jetstream.StreamInfo, error) {
	// Read the ACK floor before the stream tail: replicas sharing this durable
	// can commit and ACK new messages between these independent snapshots.
	// The later tail must cover the earlier floor; database progress is still
	// checked independently by ObserveBroker below.
	ci, err := c.messages.Info(ctx)
	if err != nil || ci == nil || ci.Stream != wire.AccessPolicyStream || ci.Name != c.options.Durable {
		return nil, ErrSource
	}
	info, err := c.stream.Info(ctx)
	if err != nil || info == nil {
		return nil, ErrSource
	}
	s := info.Config
	if s.Name != wire.AccessPolicyStream || !info.Created.Equal(c.options.StreamCreated) || len(s.Subjects) != 1 || s.Subjects[0] != wire.AccessPolicySubject || s.Retention != jetstream.LimitsPolicy || s.Storage != jetstream.FileStorage || s.Discard != jetstream.DiscardNew || s.DiscardNewPerSubject || s.MaxBytes != 64<<20 || s.MaxAge != 0 || s.MaxMsgs > 0 || s.MaxMsgsPerSubject > 0 || s.MaxMsgSize != wire.MaxAccessPolicyEventBytes || !s.DenyDelete || !s.DenyPurge || s.NoAck || s.Sealed || s.AllowRollup || s.AllowMsgTTL || s.SubjectTransform != nil || s.RePublish != nil || s.Mirror != nil || len(s.Sources) > 0 {
		return nil, ErrSource
	}
	// The first implementation accepts complete retained history from sequence 1.
	// Purged/trimmed/recreated streams require a verified snapshot recovery path.
	st := info.State
	if (st.Msgs == 0 && st.LastSeq != 0) || (st.Msgs > 0 && (st.FirstSeq != 1 || st.Msgs != st.LastSeq)) {
		return nil, ErrSource
	}
	want := Config(c.options.Durable)
	got := ci.Config
	if got.Durable != want.Durable || got.DeliverPolicy != want.DeliverPolicy || got.AckPolicy != want.AckPolicy || got.FilterSubject != want.FilterSubject || len(got.FilterSubjects) > 0 || got.AckWait != want.AckWait || got.MaxAckPending != 1 || got.MaxDeliver != -1 || len(got.BackOff) > 0 || got.DeliverSubject != "" || got.HeadersOnly || got.InactiveThreshold != 0 || got.ReplayPolicy != want.ReplayPolicy {
		return nil, ErrSource
	}
	if err = c.store.BindSource(ctx, info.Created); err != nil {
		return nil, ErrSource
	}
	if err = c.store.ObserveBroker(ctx, info.Created, ci.AckFloor.Stream, info.State.LastSeq); err != nil {
		return nil, ErrSource
	}
	return info, nil
}

// Initialize verifies the complete source/configuration and durable database
// position before Bootstrap starts any background policy work.
func (c *Consumer) Initialize(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.verify(call)
	return err
}

// Step confirms a message only after validated, exact content is committed.
// Any fetch/store/integrity error leaves it unacknowledged and stops the caller;
// no TERM or automatic poison-message skipping loses retained evidence.
func (c *Consumer) Step(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, err := c.verify(check)
	cancel()
	if err != nil {
		return err
	}
	msg, err := c.messages.Next(jetstream.FetchMaxWait(time.Second))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return ErrSource
	}
	return c.handle(ctx, msg)
}

func (c *Consumer) handle(ctx context.Context, msg jetstream.Msg) error {
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	info, err := c.verify(call)
	if err != nil {
		return err
	}
	md, err := msg.Metadata()
	if err != nil || md == nil || md.Stream != wire.AccessPolicyStream || md.Consumer != c.options.Durable || md.Sequence.Stream == 0 || md.Sequence.Stream > info.State.LastSeq || msg.Subject() != wire.AccessPolicySubject || len(msg.Data()) > wire.MaxAccessPolicyEventBytes {
		return ErrEvent
	}
	event, err := wire.DecodeAccessPolicyEvent(msg.Data())
	if err != nil || event.EventType != wire.AccessPolicyPublishedEvent {
		return ErrEvent
	}
	if event.ScopeID == c.options.ScopeID && event.SourceEpoch != c.options.SourceEpoch {
		return ErrSource
	}
	done, err := c.store.Processed(call, info.Created, md.Sequence.Stream, event)
	if err != nil {
		return ErrPersist
	}
	if !done {
		if event.ScopeID == c.options.ScopeID {
			doc, err := c.reader.Fetch(call, event)
			if err != nil {
				return ErrRead
			}
			if _, err = c.store.Apply(call, event, doc); err != nil {
				return ErrPersist
			}
		}
		if err = c.store.RecordProcessed(call, info.Created, md.Sequence.Stream, event); err != nil {
			return ErrPersist
		}
	}
	if _, err = c.verify(call); err != nil {
		return err
	}
	// Foreign scopes are ACKed only after closed event/source validation. They
	// belong to another scope's separate durable and must never reach this store.
	ack, finish := context.WithTimeout(call, 5*time.Second)
	defer finish()
	if msg.DoubleAck(ack) != nil {
		return ErrACK
	}
	return nil
}
func (c *Consumer) Run(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	for ctx.Err() == nil {
		if err := c.Step(ctx); err != nil {
			return err
		}
	}
	return ctx.Err()
}
