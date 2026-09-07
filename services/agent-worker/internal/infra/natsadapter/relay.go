package natsadapter

import (
	"context"

	codec "github.com/liuzengh/trpc-agent-service/api/events/execution/v1"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	"github.com/nats-io/nats.go/jetstream"
)

type ReplyOutbox interface {
	PendingReplies(context.Context, int) ([]domain.OutboxItem, error)
	MarkReplyPublished(context.Context, string, string) error
}
type Publisher interface {
	Publish(context.Context, string, []byte, ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}
type ReplyRelay struct {
	source    ReplyOutbox
	publisher Publisher
	batch     int
}

func NewReplyRelay(source ReplyOutbox, publisher Publisher, batch int) (*ReplyRelay, error) {
	if source == nil || publisher == nil || batch < 1 || batch > 1000 {
		return nil, domain.ErrInvalid
	}
	return &ReplyRelay{source, publisher, batch}, nil
}

// Tick publishes the exact immutable payload with stable IntentID. A crash after
// PubAck but before marking retries the same bytes and id, never executes Agent.
func (r *ReplyRelay) Tick(ctx context.Context) (int, error) {
	rows, err := r.source.PendingReplies(ctx, r.batch)
	if err != nil {
		return 0, err
	}
	if len(rows) > r.batch {
		return 0, ErrIntegrity
	}
	published := 0
	for _, item := range rows {
		event, err := codec.DecodeReplyIntent(item.Payload)
		if err != nil {
			return published, ErrIntegrity
		}
		digest, err := codec.ReplyIntentDigest(event)
		if err != nil || digest != item.Digest || event.IntentID != item.IntentID {
			return published, ErrIntegrity
		}
		ack, err := r.publisher.Publish(ctx, ReplySubject, item.Payload, jetstream.WithMsgID(item.IntentID))
		if err != nil {
			return published, ErrUnavailable
		}
		if ack == nil || ack.Stream != ReplyStream {
			return published, ErrUnavailable
		}
		if err = r.source.MarkReplyPublished(ctx, item.IntentID, item.Digest); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}
