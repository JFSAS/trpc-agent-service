package application

import (
	"context"
	"time"

	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

const PolicyOutboxIntegrity = "CHANNEL_POLICY_OUTBOX_INTEGRITY"
const PolicyPublishUnavailable = "CHANNEL_POLICY_PUBLISH_UNAVAILABLE"

type AccessPolicyClaim struct {
	TenantID, EventID, PolicyID, AccountID, SchemaVersion, PayloadDigest, ClaimToken string
	Revision                                                                         int64
	Attempt                                                                          int
	Payload                                                                          []byte
}
type AccessPolicyOutbox interface {
	ClaimAccessPolicy(context.Context) (AccessPolicyClaim, bool, error)
	FinishAccessPolicy(context.Context, AccessPolicyClaim, bool, string, time.Duration) error
}

// The publisher succeeds only after a durable ACK from the configured stream.
type AccessPolicyPublisher interface {
	PublishAccessPolicy(context.Context, string, []byte) error
}
type AccessPolicyRelay struct {
	outbox       AccessPolicyOutbox
	publisher    AccessPolicyPublisher
	scope, epoch string
}

func NewAccessPolicyRelay(outbox AccessPolicyOutbox, publisher AccessPolicyPublisher, scope, epoch string) (*AccessPolicyRelay, error) {
	if outbox == nil || publisher == nil || !domain.ValidID(scope) || !domain.ValidEpoch(epoch) {
		return nil, ErrDependencyUnavailable
	}
	return &AccessPolicyRelay{outbox: outbox, publisher: publisher, scope: scope, epoch: epoch}, nil
}
func (r *AccessPolicyRelay) Step(ctx context.Context) (bool, error) {
	c, found, err := r.outbox.ClaimAccessPolicy(ctx)
	if err != nil || !found {
		return found, err
	}
	if len(c.Payload) == 0 || len(c.Payload) > channelv1.MaxAccessPolicyEventBytes {
		return true, r.outbox.FinishAccessPolicy(ctx, c, false, PolicyOutboxIntegrity, 0)
	}
	event, err := channelv1.DecodeAccessPolicyEvent(c.Payload)
	if err != nil || event.EventType != channelv1.AccessPolicyPublishedEvent || event.ScopeID != r.scope || event.SourceEpoch != r.epoch || event.TenantID != c.TenantID || event.EventID != c.EventID || event.PolicyID != c.PolicyID || event.AccountID != c.AccountID || event.PolicyRevision != c.Revision || c.SchemaVersion != "1" {
		return true, r.outbox.FinishAccessPolicy(ctx, c, false, PolicyOutboxIntegrity, 0)
	}
	raw, digest, err := event.CanonicalJSON()
	if err != nil || digest != c.PayloadDigest {
		return true, r.outbox.FinishAccessPolicy(ctx, c, false, PolicyOutboxIntegrity, 0)
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = r.publisher.PublishAccessPolicy(call, c.EventID, raw)
	cancel()
	if err != nil {
		delay := time.Second << min(max(c.Attempt-1, 0), 6)
		return true, r.outbox.FinishAccessPolicy(ctx, c, false, PolicyPublishUnavailable, min(delay, time.Minute))
	}
	return true, r.outbox.FinishAccessPolicy(ctx, c, true, "", 0)
}
func (r *AccessPolicyRelay) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		found, err := r.Step(ctx)
		if err == nil && found {
			continue
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
