package policypostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPolicyCheckpointContiguousGapReplayAndConflict(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	created := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	e, p := fixture(t, 1)
	if err := s.RecordProcessed(ctx, created, 1, e); !errors.Is(err, ErrBlocked) {
		t.Fatal("checkpoint without document", err)
	}
	if _, err := s.Apply(ctx, e, p); err != nil {
		t.Fatal(err)
	}
	if done, err := s.Processed(ctx, created, 3, e); done || !errors.Is(err, ErrGap) {
		t.Fatal(done, err)
	}
	var processed, observed int64
	if err := pool.QueryRow(ctx, `SELECT processed_sequence,observed_sequence FROM gateway_policy_sources WHERE scope_id='pool'`).Scan(&processed, &observed); err != nil || processed != 0 || observed != 3 {
		t.Fatal(processed, observed, err)
	}
	if err := s.RecordProcessed(ctx, created, 1, e); err != nil {
		t.Fatal(err)
	}
	restarted, _ := New(pool, "pool", testEpoch)
	if done, err := restarted.Processed(ctx, created, 1, e); !done || err != nil {
		t.Fatal(done, err)
	}
	foreign := e
	foreign.ScopeID = "foreign"
	foreign.SourceEpoch = "22222222-2222-4222-8222-222222222222"
	if err := s.RecordProcessed(ctx, created, 2, foreign); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordProcessed(ctx, created, 3, e); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT processed_sequence,observed_sequence FROM gateway_policy_sources WHERE scope_id='pool'`).Scan(&processed, &observed); err != nil || processed != 3 || observed != 3 {
		t.Fatal(processed, observed, err)
	}
	changed := e
	changed.EventID = "changed-event"
	if done, err := s.Processed(ctx, created, 1, changed); done || !errors.Is(err, ErrBlocked) {
		t.Fatal(done, err)
	}
	var reason string
	pool.QueryRow(ctx, `SELECT blocked_reason FROM gateway_policy_sources WHERE scope_id='pool'`).Scan(&reason)
	if reason != "POSITION_CONFLICT" {
		t.Fatal(reason)
	}
	if done, err := s.Processed(ctx, created, 1, e); done || !errors.Is(err, ErrBlocked) {
		t.Fatal("conflict cleared", done, err)
	}
}
func TestPolicyCheckpointCommitRollbackAndSQLFences(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	created := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	e, p := fixture(t, 1)
	if _, err := s.Apply(ctx, e, p); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `CREATE FUNCTION fail_policy_position_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic checkpoint commit'; END; $$; CREATE CONSTRAINT TRIGGER fail_policy_position_commit AFTER INSERT ON gateway_policy_processed_messages DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_policy_position_commit();`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordProcessed(ctx, created, 1, e); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	var count int
	pool.QueryRow(ctx, `SELECT count(*) FROM gateway_policy_processed_messages`).Scan(&count)
	if count != 0 {
		t.Fatal(count)
	}
	if done, err := s.Processed(ctx, created, 1, e); done || err != nil {
		t.Fatal(done, err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER fail_policy_position_commit ON gateway_policy_processed_messages; DROP FUNCTION fail_policy_position_commit()`); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordProcessed(ctx, created, 1, e); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`UPDATE gateway_policy_sources SET processed_sequence=0`, `UPDATE gateway_policy_sources SET observed_sequence=0`, `DELETE FROM gateway_policy_processed_messages`, `UPDATE gateway_policy_processed_messages SET event_digest=event_digest`} {
		if _, err = pool.Exec(ctx, sql); err == nil {
			t.Fatal("checkpoint mutation accepted", sql)
		}
	}
}
