package catalogrefresh_test

import (
	"context"
	"errors"
	a "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/application/catalogrefresh"
	c "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/domain/accountcatalog"
	"sync"
	"testing"
	"time"
)

const epoch = "00000000-0000-4000-8000-000000000001"

type source struct {
	snapshot c.Snapshot
	err      error
}

func (s *source) Fetch(context.Context) (c.Snapshot, error) { return s.snapshot, s.err }

type replica struct {
	class   c.Classification
	err     error
	revoked int
	age     time.Duration
}

func (r *replica) BeginPoll(context.Context) (c.Poll, error) {
	now := time.Now().Add(-r.age)
	return c.Poll{ScopeID: "pool", SourceEpoch: epoch, InstanceID: "gw", InstanceEpoch: epoch, LocalStarted: now, StartedAt: now}, nil
}
func (r *replica) Apply(_ context.Context, p c.Poll, s c.Snapshot) (c.Classification, c.Qualification, error) {
	return r.class, c.Qualification{Generation: 1, Revision: s.Revision, ValidUntil: p.StartedAt.Add(30 * time.Second)}, r.err
}
func (r *replica) Invalidate(context.Context) error { r.revoked++; return nil }
func snap() c.Snapshot {
	s := c.Snapshot{SchemaVersion: 1, ScopeID: "pool", SourceEpoch: epoch, Revision: 1, Complete: true, Accounts: []c.Account{{ID: "cha_test", TenantID: "tnt_test", Provider: "wecom", ProviderAccountID: "bot", Revision: 1, ConnectionRevision: 1, Enabled: true, Config: c.Config{BotID: "bot"}, Credentials: []c.Credential{{Purpose: "wecom.bot_secret", ID: "ccr_test", Version: 1, Configured: true}}}}}
	s.Digest, _ = s.ComputedDigest()
	return s
}
func TestRefreshPreservesClientLifetimeOnMetadataAndFloor(t *testing.T) {
	src := &source{snapshot: snap()}
	rep := &replica{class: c.Advance}
	s, e := a.New(src, rep)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	if _, e = s.Refresh(ctx); e != nil {
		t.Fatal(e)
	}
	v, e := s.Lookup(ctx, "cha_test")
	if e != nil {
		t.Fatal(e)
	}
	src.snapshot.Revision = 2
	src.snapshot.Accounts[0].Revision = 2
	src.snapshot.Accounts[0].MinRouteGeneration = 3
	src.snapshot.Digest, _ = src.snapshot.ComputedDigest()
	if _, e = s.Refresh(ctx); e != nil {
		t.Fatal(e)
	}
	next, e := s.Lookup(ctx, "cha_test")
	if e != nil || v.Context != next.Context || v.Context.Err() != nil {
		t.Fatal("metadata/floor reconnected")
	}
	list, e := s.List(ctx)
	if e != nil || len(list) != 1 || list[0].Revision != 1 {
		t.Fatal("management revision leaked to connection")
	}
	next.Account.Credentials[0].ID = "mutated"
	again, _ := s.Lookup(ctx, "cha_test")
	if again.Account.Credentials[0].ID == "mutated" {
		t.Fatal("mutable snapshot escaped")
	}
	src.snapshot.Revision = 3
	src.snapshot.Accounts[0].Revision = 3
	src.snapshot.Accounts[0].ConnectionRevision = 2
	src.snapshot.Accounts[0].Enabled = false
	src.snapshot.Digest, _ = src.snapshot.ComputedDigest()
	if _, e = s.Refresh(ctx); e != nil {
		t.Fatal(e)
	}
	if v.Context.Err() == nil {
		t.Fatal("disable did not revoke lifetime")
	}
	list, e = s.List(ctx)
	if e != nil || len(list) != 1 || list[0].Enabled || list[0].Revision != 2 {
		t.Fatal("disabled revision not supplied")
	}
}
func TestRefreshFailureSupersessionAndAtomicApply(t *testing.T) {
	src := &source{snapshot: snap()}
	rep := &replica{class: c.Advance}
	s, _ := a.New(src, rep)
	defer s.Close()
	ctx := context.Background()
	if _, e := s.Refresh(ctx); e != nil {
		t.Fatal(e)
	}
	v, _ := s.Lookup(ctx, "cha_test")
	rep.class = c.Superseded
	if _, e := s.Refresh(ctx); e != nil || !s.Ready() {
		t.Fatal("superseded revoked healthy source")
	}
	rep.class = c.Advance
	rep.err = errors.New("synthetic store unavailable")
	if _, e := s.Refresh(ctx); e == nil || s.Ready() || rep.revoked != 1 || v.Context.Err() == nil {
		t.Fatal("uncommitted snapshot applied or failure did not revoke")
	}
	if _, e := s.List(ctx); e == nil {
		t.Fatal("failed source returned successful empty list")
	}
}
func TestIndependentFreshnessExpiry(t *testing.T) {
	src := &source{snapshot: snap()}
	rep := &replica{class: c.Advance, age: 30*time.Second - 80*time.Millisecond}
	s, _ := a.New(src, rep)
	defer s.Close()
	if _, e := s.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	v, _ := s.Lookup(context.Background(), "cha_test")
	select {
	case <-v.Context.Done():
	case <-time.After(time.Second):
		t.Fatal("watchdog required another HTTP response")
	}
	if s.Ready() {
		t.Fatal("expired source ready")
	}
}
func TestConcurrentLookupAndRefresh(t *testing.T) {
	src := &source{snapshot: snap()}
	rep := &replica{class: c.Same}
	s, _ := a.New(src, rep)
	defer s.Close()
	_, _ = s.Refresh(context.Background())
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				_, _ = s.Lookup(context.Background(), "cha_test")
				_, _ = s.List(context.Background())
			}
		}()
	}
	for range 20 {
		_, _ = s.Refresh(context.Background())
	}
	wg.Wait()
}

func TestCurrentAccountsQualifiedCloneAndDisabled(t *testing.T) {
	src := &source{snapshot: snap()}
	src.snapshot.Accounts[0].Enabled = false
	src.snapshot.Digest, _ = src.snapshot.ComputedDigest()
	s, _ := a.New(src, &replica{class: c.Advance})
	defer s.Close()
	ctx := context.Background()
	if _, err := s.CurrentAccounts(ctx); err == nil {
		t.Fatal("unqualified directory")
	}
	if _, err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := s.CurrentAccounts(ctx)
	if err != nil || len(list) != 1 || list[0].Enabled {
		t.Fatal("disabled account omitted", err)
	}
	list[0].Credentials[0].ID = "mutated"
	again, _ := s.CurrentAccounts(ctx)
	if again[0].Credentials[0].ID == "mutated" {
		t.Fatal("mutable owner state")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.CurrentAccounts(canceled); err == nil {
		t.Fatal("canceled context")
	}
	src.err = errors.New("outage")
	s.Refresh(ctx)
	if _, err := s.CurrentAccounts(ctx); err == nil {
		t.Fatal("stale directory")
	}
	s.Close()
	if _, err := s.CurrentAccounts(ctx); err == nil {
		t.Fatal("closed directory")
	}
}
