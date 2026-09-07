package policynats

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/outbound/controlpolicy"
	pgstore "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/outbound/policypostgres"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/migrations"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const testEpoch = "11111111-1111-4111-8111-111111111111"

func setup(t *testing.T) (*pgstore.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to run real PostgreSQL acceptance tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{"gateway_admission_" + hex.EncodeToString(random[:])}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 32
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store, err := pgstore.New(pool, "pool", testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	return store, pool
}
func fixture(t *testing.T, rev int64) (wire.AccessPolicyEvent, wire.AccessPolicyDocument) {
	t.Helper()
	p := wire.AccessPolicyDocument{SchemaVersion: 1, TenantID: "tenant", AccountID: "account", Provider: "wecom", PolicyID: "policy", Revision: rev, PublishedBy: "owner", PublishedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), Body: wire.AccessPolicyBody{AccessMode: "DENY_ALL", AllowedPrincipalIDs: []string{}, AllowedConversationIDs: []string{}, AllowedOperations: []string{}, AuthorizationMaxAgeMS: 30000}}
	return signed(t, p)
}
func signed(t *testing.T, p wire.AccessPolicyDocument) (wire.AccessPolicyEvent, wire.AccessPolicyDocument) {
	t.Helper()
	p.Digest = ""
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = jcs.Transform(raw)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	p.Digest = "sha256:" + hex.EncodeToString(h[:])
	e := wire.AccessPolicyEvent{SchemaVersion: 1, EventID: fmt.Sprintf("event-%d", p.Revision), EventType: wire.AccessPolicyPublishedEvent, ScopeID: "pool", SourceEpoch: testEpoch, TenantID: p.TenantID, AccountID: p.AccountID, Provider: p.Provider, PolicyID: p.PolicyID, PolicyRevision: p.Revision, PolicyDigest: p.Digest, OccurredAt: p.PublishedAt}
	return e, p
}
func reference(p wire.AccessPolicyDocument) wire.PolicyReference {
	return wire.PolicyReference{ID: p.PolicyID, Revision: p.Revision, Digest: p.Digest}
}
func tlsFixture(t *testing.T, h http.Handler) (*controlpolicy.Client, *httptest.Server) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, root, root, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	issue := func(n int64, usage x509.ExtKeyUsage) tls.Certificate {
		pub, k, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		x := &x509.Certificate{SerialNumber: big.NewInt(n), Subject: pkix.Name{CommonName: "test-leaf"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{usage}, KeyUsage: x509.KeyUsageDigitalSignature}
		b, e := x509.CreateCertificate(rand.Reader, x, ca, pub, key)
		if e != nil {
			t.Fatal(e)
		}
		kb, e := x509.MarshalPKCS8PrivateKey(k)
		if e != nil {
			t.Fatal(e)
		}
		cert, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}))
		if e != nil {
			t.Fatal(e)
		}
		return cert
	}
	server := httptest.NewUnstartedServer(h)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{issue(2, x509.ExtKeyUsageServerAuth)}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	t.Cleanup(server.Close)
	client, e := controlpolicy.New(controlpolicy.Options{BaseURL: server.URL, ScopeID: "pool", SourceEpoch: testEpoch, RootCAs: roots, Certificate: issue(3, x509.ExtKeyUsageClientAuth)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	return client, server
}

func broker(t *testing.T) (jetstream.JetStream, jetstream.Stream, jetstream.Consumer, time.Time) {
	t.Helper()
	url := os.Getenv("GATEWAY_POLICY_TEST_NATS_URL")
	if url == "" {
		t.Skip("GATEWAY_POLICY_TEST_NATS_URL requires a dedicated disposable broker")
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: wire.AccessPolicyStream, Subjects: []string{wire.AccessPolicySubject}, Storage: jetstream.FileStorage, Retention: jetstream.LimitsPolicy, Discard: jetstream.DiscardNew, MaxBytes: 64 << 20, MaxMsgSize: wire.MaxAccessPolicyEventBytes, DenyDelete: true, DenyPurge: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := js.DeleteStream(context.Background(), wire.AccessPolicyStream); err != nil {
			t.Error(err)
		}
	})
	consumer, err := stream.CreateConsumer(ctx, Config(DurableName("pool")))
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return js, stream, consumer, info.Created
}
func publish(t *testing.T, js jetstream.JetStream, e wire.AccessPolicyEvent) {
	t.Helper()
	raw, _, err := e.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	ack, err := js.Publish(context.Background(), wire.AccessPolicySubject, raw)
	if err != nil || ack.Stream != wire.AccessPolicyStream {
		t.Fatal(ack, err)
	}
}
func TestPolicyNATSTLSPostgresCommitBeforeACK(t *testing.T) {
	js, stream, msgs, created := broker(t)
	store, pool := setup(t)
	ctx := context.Background()
	var failRead atomic.Bool
	failRead.Store(true)
	docs := map[int64]wire.AccessPolicyDocument{}
	for _, rev := range []int64{1, 2, 3} {
		_, docs[rev] = fixture(t, rev)
	}
	reader, _ := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failRead.Load() {
			http.Error(w, "SYNTHETIC_UPSTREAM", 503)
			return
		}
		var req wire.AccessPolicyResolveRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("invalid reader request")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(wire.AccessPolicyResolveResponse{SchemaVersion: 1, ScopeID: "pool", SourceEpoch: testEpoch, Policy: docs[req.Reference.Revision]})
	}))
	c, err := New(msgs, stream, reader, store, Options{ScopeID: "pool", SourceEpoch: testEpoch, Durable: DurableName("pool"), StreamCreated: created})
	if err != nil {
		t.Fatal(err)
	}
	e, p := fixture(t, 1)
	publish(t, js, e)
	msg, err := msgs.Next(jetstream.FetchMaxWait(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	assertPending := func() {
		t.Helper()
		info, err := msgs.Info(ctx)
		if err != nil || info.NumAckPending != 1 || info.AckFloor.Stream != 0 {
			t.Fatal(info, err)
		}
	}
	// Retry the same actual JetStream delivery after injected read/commit failures.
	// No simulated ACK is used; the broker's durable state is queried each time.
	if err = c.handle(ctx, msg); !errors.Is(err, ErrRead) {
		t.Fatal(err)
	}
	assertPending()
	failRead.Store(false)
	_, err = pool.Exec(ctx, `CREATE FUNCTION fail_policy_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic commit rejection'; END; $$; CREATE CONSTRAINT TRIGGER fail_policy_commit AFTER INSERT ON gateway_policy_projection_documents DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_policy_commit();`)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.handle(ctx, msg); !errors.Is(err, ErrPersist) {
		t.Fatal(err)
	}
	assertPending()
	var count int
	pool.QueryRow(ctx, `SELECT count(*) FROM gateway_policy_projection_documents`).Scan(&count)
	if count != 0 {
		t.Fatal("partial commit", count)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER fail_policy_commit ON gateway_policy_projection_documents; DROP FUNCTION fail_policy_commit()`); err != nil {
		t.Fatal(err)
	}
	if err = c.handle(ctx, failedACK{Msg: msg}); !errors.Is(err, ErrACK) {
		t.Fatal("expected lost ACK", err)
	}
	assertPending()
	// The checkpoint has committed, but the broker still awaits its ACK. A new
	// store/consumer must replay without Control, even while the HTTP fixture fails.
	failRead.Store(true)
	restartedStore, err := pgstore.New(pool, "pool", testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(msgs, stream, reader, restartedStore, Options{ScopeID: "pool", SourceEpoch: testEpoch, Durable: DurableName("pool"), StreamCreated: created})
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.handle(ctx, msg); err != nil {
		t.Fatal("committed replay depended on reader", err)
	}
	failRead.Store(false)
	info, err := msgs.Info(ctx)
	if err != nil || info.NumAckPending != 0 || info.AckFloor.Stream != 1 {
		t.Fatal(info, err)
	}
	if _, state, err := store.ReadExact(ctx, "tenant", "account", reference(p)); err != nil || state.ContiguousRevision != 1 {
		t.Fatal(state, err)
	}
	// Retried notification, out-of-order revision and gap fill each traverse the
	// actual public Step API: broker -> mTLS reader -> PG -> broker DoubleAck.
	for _, rev := range []int64{1, 3, 2} {
		e, _ := fixture(t, rev)
		publish(t, js, e)
		if err = c.Step(ctx); err != nil {
			t.Fatal(rev, err)
		}
	}
	_, p = fixture(t, 3)
	_, state, err := store.ReadExact(ctx, "tenant", "account", reference(p))
	if err != nil || state.ContiguousRevision != 3 || state.ObservedRevision != 3 {
		t.Fatal(state, err)
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM gateway_policy_projection_documents`).Scan(&count)
	if count != 3 {
		t.Fatal(count)
	}
	info, err = msgs.Info(ctx)
	if err != nil || info.AckFloor.Stream != 4 || info.NumAckPending != 0 {
		t.Fatal(info, err)
	}
}

type unusedReader struct{ calls atomic.Int32 }

func (r *unusedReader) Fetch(context.Context, wire.AccessPolicyEvent) (wire.AccessPolicyDocument, error) {
	r.calls.Add(1)
	return wire.AccessPolicyDocument{}, errors.New("not expected")
}
func TestPolicyConsumerScopeEpochAndSourceGuards(t *testing.T) {
	js, stream, msgs, created := broker(t)
	store, _ := setup(t)
	ctx := context.Background()
	reader := &unusedReader{}
	c, err := New(msgs, stream, reader, store, Options{ScopeID: "pool", SourceEpoch: testEpoch, Durable: DurableName("pool"), StreamCreated: created})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = New(msgs, stream, reader, store, Options{ScopeID: "pool", SourceEpoch: testEpoch, Durable: DurableName("another-pool"), StreamCreated: created}); !errors.Is(err, ErrInvalid) {
		t.Fatal("shared durable across different scopes", err)
	}
	e, _ := fixture(t, 1)
	e.ScopeID = "another-pool"
	publish(t, js, e)
	if err = c.Step(ctx); err != nil || reader.calls.Load() != 0 {
		t.Fatal(err)
	}
	info, err := msgs.Info(ctx)
	if err != nil || info.AckFloor.Stream != 1 {
		t.Fatal(info, err)
	}
	e.ScopeID = "pool"
	e.SourceEpoch = "22222222-2222-4222-8222-222222222222"
	publish(t, js, e)
	if err = c.Step(ctx); !errors.Is(err, ErrSource) || reader.calls.Load() != 0 {
		t.Fatal(err)
	}
	info, err = msgs.Info(ctx)
	if err != nil || info.NumAckPending != 1 || info.AckFloor.Stream != 1 {
		t.Fatal(info, err)
	}
	// Even the same named stream is not accepted with another creation identity.
	other, err := New(msgs, stream, reader, store, Options{ScopeID: "pool", SourceEpoch: testEpoch, Durable: DurableName("pool"), StreamCreated: created.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err = other.Step(ctx); !errors.Is(err, ErrSource) {
		t.Fatal(err)
	}
	cfg := Config(DurableName("pool"))
	cfg.MaxAckPending = 2
	if _, err = stream.UpdateConsumer(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = c.Step(ctx); !errors.Is(err, ErrSource) {
		t.Fatal("incompatible durable", err)
	}
}
func TestPolicyConsumerPoisonDocumentStaysUnacknowledged(t *testing.T) {
	js, stream, msgs, created := broker(t)
	store, _ := setup(t)
	reader := &unusedReader{}
	ctx := context.Background()
	c, err := New(msgs, stream, reader, store, Options{ScopeID: "pool", SourceEpoch: testEpoch, Durable: DurableName("pool"), StreamCreated: created})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = js.Publish(ctx, wire.AccessPolicySubject, []byte(`{"schema_version":1,"unexpected":"secret"}`)); err != nil {
		t.Fatal(err)
	}
	if err = c.Step(ctx); !errors.Is(err, ErrEvent) {
		t.Fatal(err)
	}
	info, err := msgs.Info(ctx)
	if err != nil || info.NumAckPending != 1 || info.AckFloor.Stream != 0 || reader.calls.Load() != 0 {
		t.Fatal(info, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = c.Run(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPolicyConsumerRecreatedBrokerSourceRejectedAfterRestart(t *testing.T) {
	js, stream, msgs, created := broker(t)
	store, pool := setup(t)
	ctx := context.Background()
	reader := &unusedReader{}
	opts := Options{ScopeID: "pool", SourceEpoch: testEpoch, Durable: DurableName("pool"), StreamCreated: created}
	c, err := New(msgs, stream, reader, store, opts)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := fixture(t, 1)
	e.ScopeID = "foreign"
	publish(t, js, e)
	if err = c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = js.DeleteStream(ctx, wire.AccessPolicyStream); err != nil {
		t.Fatal(err)
	}
	replacement, err := js.CreateStream(ctx, info.Config)
	if err != nil {
		t.Fatal(err)
	}
	replaced, err := replacement.Info(ctx)
	if err != nil || replaced.Created.Equal(created) {
		t.Fatal("source was not recreated", err)
	}
	next, err := replacement.CreateConsumer(ctx, Config(DurableName("pool")))
	if err != nil {
		t.Fatal(err)
	}
	restartedStore, err := pgstore.New(pool, "pool", testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	opts.StreamCreated = replaced.Created
	restarted, err := New(next, replacement, reader, restartedStore, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.Step(ctx); !errors.Is(err, ErrSource) {
		t.Fatal("restart adopted recreated source", err)
	}
	var reason string
	if err = pool.QueryRow(ctx, `SELECT blocked_reason FROM gateway_policy_sources WHERE scope_id='pool'`).Scan(&reason); err != nil || reason != "SOURCE_CHANGED" {
		t.Fatal(reason, err)
	}
	if reader.calls.Load() != 0 {
		t.Fatal("changed source reached reader")
	}
}

type failedACK struct{ jetstream.Msg }

func (failedACK) DoubleAck(context.Context) error { return errors.New("synthetic ACK loss") }

func TestPolicyConsumerRejectsBrokerACKFloorAheadOfDatabase(t *testing.T) {
	js, stream, msgs, created := broker(t)
	store, pool := setup(t)
	ctx := context.Background()
	reader := &unusedReader{}
	e, _ := fixture(t, 1)
	e.ScopeID = "foreign"
	publish(t, js, e)
	msg, err := msgs.Next(jetstream.FetchMaxWait(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a restored older database with a newer durable ACK floor.
	if err = msg.DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := New(msgs, stream, reader, store, Options{ScopeID: "pool", SourceEpoch: testEpoch, Durable: DurableName("pool"), StreamCreated: created})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Step(ctx); !errors.Is(err, ErrSource) {
		t.Fatal("idle ACK gap silently accepted", err)
	}
	var processed, observed int64
	if err = pool.QueryRow(ctx, `SELECT processed_sequence,observed_sequence FROM gateway_policy_sources WHERE scope_id='pool'`).Scan(&processed, &observed); err != nil || processed != 0 || observed != 1 {
		t.Fatal(processed, observed, err)
	}
	if reader.calls.Load() != 0 {
		t.Fatal("gap reached reader")
	}
}
