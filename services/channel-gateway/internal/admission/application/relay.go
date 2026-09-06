package application

import (
	"context"
	"errors"
	"time"
)

type OutboxMessage struct {
	EventID, Subject, ClaimToken string
	Payload                      []byte
}
type Outbox interface {
	Claim(context.Context) (OutboxMessage, bool, error)
	Published(context.Context, OutboxMessage) error
	Retry(context.Context, OutboxMessage) error
}
type Publisher interface {
	Publish(context.Context, string, string, []byte) error
}

// Relay has at-least-once semantics. Stable event IDs remain unchanged on retries.
type Relay struct {
	ledger    Outbox
	publisher Publisher
}

func NewRelay(ledger Outbox, publisher Publisher) *Relay {
	return &Relay{ledger: ledger, publisher: publisher}
}
func (r *Relay) PublishNext(ctx context.Context) (bool, error) {
	ctx, cancelOperation := context.WithTimeout(ctx, 10*time.Second)
	defer cancelOperation()
	msg, found, err := r.ledger.Claim(ctx)
	if err != nil || !found {
		return false, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = r.publisher.Publish(callCtx, msg.Subject, msg.EventID, msg.Payload); err != nil {
		// If retry bookkeeping fails the claim lease still expires for crash recovery.
		retryErr := r.ledger.Retry(ctx, msg)
		return true, errors.Join(err, retryErr)
	}
	return true, r.ledger.Published(ctx, msg)
}
func (r *Relay) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		found, err := r.PublishNext(ctx)
		if err == nil && found {
			continue
		}
		delay := 100 * time.Millisecond
		if err != nil {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
