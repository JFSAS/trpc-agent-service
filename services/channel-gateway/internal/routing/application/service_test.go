package application_test

import (
	"context"
	"errors"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/routing/application"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/routing/domain"
	"testing"
)

type storeSpy struct {
	calls  int
	err    error
	route  domain.RouteSnapshot
	health domain.ProjectionHealth
}

func (s *storeSpy) BeginReplay(context.Context, domain.ReplaySource) error   { s.calls++; return s.err }
func (s *storeSpy) ObserveSource(context.Context, domain.ReplaySource) error { s.calls++; return s.err }
func (s *storeSpy) ApplyFromStream(context.Context, domain.StreamPosition, domain.RouteEvent) error {
	s.calls++
	return s.err
}
func (s *storeSpy) Quarantine(context.Context, domain.StreamPosition, domain.QuarantineReason, string) error {
	s.calls++
	return s.err
}
func (s *storeSpy) QueryProjectionHealth(context.Context) (domain.ProjectionHealth, error) {
	return s.health, s.err
}
func (s *storeSpy) Resolve(context.Context, string, string) (domain.RouteSnapshot, error) {
	s.calls++
	return s.route, s.err
}
func TestRejectUnsequencedWrites(t *testing.T) {
	store := &storeSpy{}
	service, err := application.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Apply(context.Background(), domain.RouteEvent{}); !errors.Is(err, domain.ErrStreamPositionRequired) {
		t.Fatal(err)
	}
	if err = service.ApplyFromStream(context.Background(), domain.StreamPosition{}, domain.RouteEvent{}); !errors.Is(err, domain.ErrStreamPositionRequired) {
		t.Fatal(err)
	}
	if _, err = service.Resolve(context.Background(), "telegram", ""); !errors.Is(err, domain.ErrInvalidEvent) {
		t.Fatal(err)
	}
	if store.calls != 0 {
		t.Fatal("invalid input reached persistence")
	}
	if _, err = application.NewService(nil); err == nil {
		t.Fatal("nil store accepted")
	}
}
func TestServicePreservesReadinessAndErrors(t *testing.T) {
	store := &storeSpy{health: domain.ProjectionHealth{Initialized: true}}
	service, _ := application.NewService(store)
	health, err := service.QueryProjectionHealth(context.Background())
	if err != nil || !health.Initialized {
		t.Fatalf("health=%#v %v", health, err)
	}
	store.err = domain.ErrProjectionBlocked
	p := domain.StreamPosition{StreamName: "CONTROL_ROUTES", StreamID: "2026-09-05T00:00:00Z", Sequence: 1}
	if err = service.ApplyFromStream(context.Background(), p, domain.RouteEvent{}); !errors.Is(err, domain.ErrProjectionBlocked) {
		t.Fatal(err)
	}
}
