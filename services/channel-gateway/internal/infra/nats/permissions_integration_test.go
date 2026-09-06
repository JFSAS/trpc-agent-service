package natsadapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/events/execution/v1"
	"github.com/nats-io/nats.go"
)

// This test is also compiled as a small Linux test binary and run on the
// dedicated Compose network; no broker ports need to be exposed to the host.
func TestBrokerPermissionsIntegration(t *testing.T) {
	url := os.Getenv("GATEWAY_TEST_AUTH_NATS_URL")
	if url == "" {
		t.Skip("GATEWAY_TEST_AUTH_NATS_URL required")
	}
	topology, err := LoadTopology(os.Getenv("GATEWAY_TEST_TOPOLOGY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, err := Connect(url, topology, Auth{User: "gateway", Password: os.Getenv("NATS_GATEWAY_PASSWORD"), InboxPrefix: "_INBOX.gateway"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err = runtime.Verify(ctx); err != nil {
		t.Fatalf("runtime read-only topology verification: %v", err)
	}
	admin, err := Connect(url, topology, Auth{User: "reconciler", Password: os.Getenv("NATS_RECONCILER_PASSWORD"), InboxPrefix: "_INBOX.reconciler"})
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err = admin.Reconcile(ctx); err != nil {
		t.Fatalf("administration permissions: %v", err)
	}
	fixturePath := os.Getenv("GATEWAY_TEST_RUN_FIXTURE_FILE")
	if fixturePath == "" {
		fixturePath = "../../../../../api/events/execution/v1/fixtures/valid/telegram-text.json"
	}
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	event, err := wire.DecodeRunRequested(fixture)
	if err != nil {
		t.Fatal(err)
	}
	event.EventID = fmt.Sprintf("acl-admission-%d", time.Now().UnixNano())
	event.AdmissionID = event.EventID
	event.RunID = "run-" + event.EventID
	payload, err := wire.EncodeRunRequested(event)
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.Publish(ctx, RunSubject, event.EventID, payload); err != nil {
		t.Fatalf("runtime publication/PubAck permissions: %v", err)
	}
	denied := make(chan error, 8)
	c, err := nats.Connect(url, nats.UserInfo("gateway", os.Getenv("NATS_GATEWAY_PASSWORD")), nats.CustomInboxPrefix("_INBOX.gateway"), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) { denied <- e }))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, subject := range []string{RouteSubject, "$JS.API.STREAM.UPDATE." + RunStream} {
		if err = c.Publish(subject, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if err = c.FlushTimeout(time.Second); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-denied:
			if !errors.Is(err, nats.ErrPermissionViolation) {
				t.Fatalf("expected permission denial: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no permission denial for %s", subject)
		}
	}
	if unexpected, connectErr := nats.Connect(url, nats.UserInfo("gateway", "wrong-password"), nats.Timeout(time.Second), nats.NoReconnect()); connectErr == nil {
		unexpected.Close()
		t.Fatal("wrong credentials accepted")
	}
	t.Log("ACL_VERIFIED: runtime read-only topology; schema-valid RunRequested PubAck; reconciler idempotence; Gateway Control-publish denied; Gateway topology-write denied; incorrect credential denied")
}
