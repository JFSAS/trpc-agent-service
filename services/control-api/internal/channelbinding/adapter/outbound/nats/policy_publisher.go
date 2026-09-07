package natsadapter

import (
	"context"

	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
	"github.com/nats-io/nats.go"
)

func (p *Publisher) PublishAccessPolicy(ctx context.Context, eventID string, payload []byte) error {
	if len(payload) == 0 || len(payload) > channelv1.MaxAccessPolicyEventBytes {
		return application.ErrDependencyUnavailable
	}
	event, err := channelv1.DecodeAccessPolicyEvent(payload)
	if err != nil || event.EventID != eventID || event.EventType != channelv1.AccessPolicyPublishedEvent {
		return application.ErrDependencyUnavailable
	}
	msg := nats.NewMsg(channelv1.AccessPolicySubject)
	msg.Data = payload
	// IDs are tenant-scoped in Control. '/' is forbidden in both validated IDs.
	msg.Header.Set(nats.MsgIdHdr, event.TenantID+"/"+eventID)
	ack, err := p.js.PublishMsg(msg, nats.Context(ctx), nats.ExpectStream(channelv1.AccessPolicyStream))
	if err != nil || ack == nil || ack.Stream != channelv1.AccessPolicyStream || ack.Sequence == 0 {
		return application.ErrDependencyUnavailable
	}
	return nil
}

var _ application.AccessPolicyPublisher = (*Publisher)(nil)
