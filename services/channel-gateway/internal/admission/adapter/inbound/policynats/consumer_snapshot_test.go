package policynats

import (
	"context"
	"errors"
	"testing"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	pgstore "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/outbound/policypostgres"
	"github.com/nats-io/nats.go/jetstream"
)

type snapshotMessages struct {
	Messages
	info func() *jetstream.ConsumerInfo
}

func (m snapshotMessages) Info(context.Context) (*jetstream.ConsumerInfo, error) {
	return m.info(), nil
}

type snapshotStream struct{ info func() *jetstream.StreamInfo }

func (s snapshotStream) Info(context.Context, ...jetstream.StreamInfoOpt) (*jetstream.StreamInfo, error) {
	return s.info(), nil
}

type snapshotStore struct {
	Store
	observed  bool
	processed uint64
}

func (s *snapshotStore) BindSource(context.Context, time.Time) error { return nil }
func (s *snapshotStore) ObserveBroker(_ context.Context, _ time.Time, ack, last uint64) error {
	s.observed = true
	if ack > last {
		return pgstore.ErrInvalid
	}
	if ack > s.processed {
		return pgstore.ErrGap
	}
	return nil
}

// The other replica commits and ACKs a new message while ConsumerInfo is being
// read. Reading StreamInfo first would pair its old tail with the newer ACK floor.
func TestPolicyVerifyInterleavedReplicaSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name          string
		processed     uint64
		changedSource bool
		wantErr       bool
		wantObserve   bool
	}{
		{"committed_replica_progress", 1, false, false, true},
		{"ack_ahead_of_database", 0, false, true, true},
		{"recreated_source", 1, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
			durable := DurableName("pool")
			tail := uint64(0)
			messages := snapshotMessages{info: func() *jetstream.ConsumerInfo {
				// Another replica advances the durable between the independent snapshots.
				tail = 1
				return &jetstream.ConsumerInfo{Stream: wire.AccessPolicyStream, Name: durable, Config: Config(durable), AckFloor: jetstream.SequenceInfo{Stream: 1}}
			}}
			stream := snapshotStream{info: func() *jetstream.StreamInfo {
				stamp := created
				if tc.changedSource {
					stamp = stamp.Add(time.Second)
				}
				first := uint64(0)
				if tail > 0 {
					first = 1
				}
				return &jetstream.StreamInfo{Created: stamp, Config: jetstream.StreamConfig{
					Name: wire.AccessPolicyStream, Subjects: []string{wire.AccessPolicySubject}, Retention: jetstream.LimitsPolicy,
					Storage: jetstream.FileStorage, Discard: jetstream.DiscardNew, MaxBytes: 64 << 20,
					MaxMsgSize: wire.MaxAccessPolicyEventBytes, DenyDelete: true, DenyPurge: true,
				}, State: jetstream.StreamState{Msgs: tail, FirstSeq: first, LastSeq: tail}}
			}}
			store := &snapshotStore{processed: tc.processed}
			consumer := &Consumer{messages: messages, stream: stream, store: store, options: Options{ScopeID: "pool", SourceEpoch: testEpoch, Durable: durable, StreamCreated: created}}
			_, err := consumer.verify(context.Background())
			if tc.wantErr && !errors.Is(err, ErrSource) {
				t.Fatalf("want source rejection, got %v", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("committed replica progress rejected: %v", err)
			}
			if store.observed != tc.wantObserve {
				t.Fatalf("checkpoint observed=%v, want %v", store.observed, tc.wantObserve)
			}
		})
	}
}
