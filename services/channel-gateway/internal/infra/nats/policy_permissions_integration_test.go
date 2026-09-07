package natsadapter

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestPolicyScopeRealReconcileAndACL(t *testing.T) {
	address, path := os.Getenv("GATEWAY_TEST_AUTH_NATS_URL"), os.Getenv("GATEWAY_TEST_TOPOLOGY_FILE")
	if address == "" || path == "" {
		t.Skip("dedicated authenticated NATS and scoped topology required")
	}
	top, err := LoadTopology(path)
	if err != nil {
		t.Fatal(err)
	}
	if !top.HasPolicyScope("scope_fixture") || !top.HasPolicyScope("other_scope") {
		t.Fatal("requires two explicit test scopes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := Connect(address, top, Auth{User: "reconciler", Password: os.Getenv("NATS_RECONCILER_PASSWORD"), InboxPrefix: "_INBOX.reconciler"})
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err = admin.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	consumer, err := admin.JS.Consumer(ctx, wire.AccessPolicyStream, wire.PolicyConsumerName("scope_fixture"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := consumer.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = admin.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := consumer.Info(ctx)
	if err != nil || !before.Created.Equal(after.Created) {
		t.Fatal("reconcile recreated durable", err)
	}
	bad := policyConsumerConfig("scope_fixture")
	bad.MaxAckPending = 2
	st, err := admin.JS.Stream(ctx, wire.AccessPolicyStream)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.UpdateConsumer(ctx, bad); err != nil {
		t.Fatal(err)
	}
	if err = admin.Reconcile(ctx); err == nil {
		t.Fatal("incompatible durable silently adopted")
	}
	actual, err := consumer.Info(ctx)
	if err != nil || actual.Config.MaxAckPending != 2 {
		t.Fatal("reconcile mutated incompatible durable", err)
	}
	if _, err = st.UpdateConsumer(ctx, policyConsumerConfig("scope_fixture")); err != nil {
		t.Fatal(err)
	}
	denied := make(chan error, 8)
	nc, err := nats.Connect(address, nats.UserInfo("gateway", os.Getenv("NATS_GATEWAY_PASSWORD")), nats.CustomInboxPrefix("_INBOX.gateway"), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { denied <- err }))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	own, err := js.Consumer(ctx, wire.AccessPolicyStream, wire.PolicyConsumerName("scope_fixture"))
	if err != nil {
		t.Fatal(err)
	}
	control, err := nats.Connect(address, nats.UserInfo("control", os.Getenv("NATS_CONTROL_PASSWORD")), nats.CustomInboxPrefix("_INBOX.control"))
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	publisher, err := jetstream.New(control)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := publisher.Publish(ctx, wire.AccessPolicySubject, []byte(`{"acl_fixture":true}`))
	if err != nil || ack.Stream != wire.AccessPolicyStream {
		t.Fatal(ack, err)
	}
	// The declared durable survives reconciliation and may contain notifications
	// from earlier tests. Confirm every retained position through this publish,
	// rather than assuming the first pending message is this test's fixture.
	for {
		if err = ctx.Err(); err != nil {
			t.Fatal(err)
		}
		msg, nextErr := own.Next(jetstream.FetchMaxWait(time.Second))
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		metadata, metadataErr := msg.Metadata()
		if metadataErr != nil || metadata.Sequence.Stream > ack.Sequence {
			t.Fatal("missed fixture sequence", metadata, metadataErr)
		}
		if err = msg.DoubleAck(ctx); err != nil {
			t.Fatal(err)
		}
		if metadata.Sequence.Stream == ack.Sequence {
			if string(msg.Data()) != `{"acl_fixture":true}` {
				t.Fatal("unexpected fixture payload")
			}
			break
		}
	}
	info, err := own.Info(ctx)
	if err != nil || info.NumAckPending != 0 || info.AckFloor.Stream != ack.Sequence {
		t.Fatal(info, err)
	}
	for _, subject := range []string{wire.AccessPolicySubject, "$JS.API.CONSUMER.INFO." + wire.AccessPolicyStream + "." + wire.PolicyConsumerName("other_scope"), "$JS.API.CONSUMER.MSG.NEXT." + wire.AccessPolicyStream + "." + wire.PolicyConsumerName("other_scope"), "$JS.API.STREAM.UPDATE." + wire.AccessPolicyStream} {
		if err = nc.Publish(subject, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if err = nc.FlushTimeout(time.Second); err != nil {
			t.Fatal(err)
		}
		select {
		case e := <-denied:
			if !errors.Is(e, nats.ErrPermissionViolation) {
				t.Fatal(e)
			}
		case <-time.After(time.Second):
			t.Fatal("expected denial", subject)
		}
	}
	t.Log("POLICY_SCOPED_ACL=PASS own INFO/NEXT/DoubleAck; other scope INFO/NEXT, policy publication and topology mutation denied; reconcile idempotent and rejects drift")
}
