package application

import (
	"context"
	"errors"
	"sort"

	managementv1 "github.com/liuzengh/trpc-agent-service/api/runtime/management/v1"
)

var (
	ErrForbidden   = errors.New("run management forbidden")
	ErrNotFound    = errors.New("run not found")
	ErrInvalidPage = errors.New("run management page invalid")
	ErrUnavailable = errors.New("run management unavailable")
)

type TenantAccess interface {
	IsActiveMember(context.Context, string, string) (bool, error)
}
type RuntimeReader interface {
	ListRuns(context.Context, string, int, int) (managementv1.RunPage, error)
	GetRun(context.Context, string, string) (managementv1.RunDetail, error)
	ListAudit(context.Context, string, int, int) (managementv1.AuditPage, error)
}
type ControlAuditReader interface {
	List(context.Context, string, int, int) (managementv1.AuditPage, error)
}

type Service struct {
	access  TenantAccess
	runtime RuntimeReader
	audit   ControlAuditReader
}

func New(access TenantAccess, runtime RuntimeReader, audit ControlAuditReader) (*Service, error) {
	if access == nil || runtime == nil || audit == nil {
		return nil, ErrUnavailable
	}
	return &Service{access: access, runtime: runtime, audit: audit}, nil
}

func (s *Service) authorize(ctx context.Context, tenant, user string) error {
	if tenant == "" || user == "" {
		return ErrForbidden
	}
	ok, err := s.access.IsActiveMember(ctx, tenant, user)
	if err != nil {
		return ErrUnavailable
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

func validPage(offset, limit int) bool {
	return offset >= 0 && limit > 0 && limit <= managementv1.MaxPageSize
}

func (s *Service) ListRuns(ctx context.Context, tenant, user string, offset, limit int) (managementv1.RunPage, error) {
	if !validPage(offset, limit) {
		return managementv1.RunPage{}, ErrInvalidPage
	}
	if err := s.authorize(ctx, tenant, user); err != nil {
		return managementv1.RunPage{}, err
	}
	page, err := s.runtime.ListRuns(ctx, tenant, offset, limit)
	if err != nil {
		return managementv1.RunPage{}, ErrUnavailable
	}
	return page, nil
}

func (s *Service) GetRun(ctx context.Context, tenant, user, runID string) (managementv1.RunDetail, error) {
	if runID == "" {
		return managementv1.RunDetail{}, ErrNotFound
	}
	if err := s.authorize(ctx, tenant, user); err != nil {
		return managementv1.RunDetail{}, err
	}
	run, err := s.runtime.GetRun(ctx, tenant, runID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return managementv1.RunDetail{}, ErrNotFound
		}
		return managementv1.RunDetail{}, ErrUnavailable
	}
	return run, nil
}

func (s *Service) ListAudit(ctx context.Context, tenant, user string, offset, limit int) (managementv1.AuditPage, error) {
	if !validPage(offset, limit) {
		return managementv1.AuditPage{}, ErrInvalidPage
	}
	if err := s.authorize(ctx, tenant, user); err != nil {
		return managementv1.AuditPage{}, err
	}
	want := offset + limit
	if want > managementv1.MaxPageSize {
		return managementv1.AuditPage{}, ErrInvalidPage
	}
	control, err := s.audit.List(ctx, tenant, 0, want)
	if err != nil {
		return managementv1.AuditPage{}, ErrUnavailable
	}
	runtime, err := s.runtime.ListAudit(ctx, tenant, 0, want)
	if err != nil {
		return managementv1.AuditPage{}, ErrUnavailable
	}
	events := append(append([]managementv1.AuditEvent{}, control.Events...), runtime.Events...)
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].OccurredAt.Equal(events[j].OccurredAt) {
			return events[i].EventID > events[j].EventID
		}
		return events[i].OccurredAt.After(events[j].OccurredAt)
	})
	end := min(len(events), want)
	start := min(offset, end)
	return managementv1.AuditPage{Events: events[start:end], Offset: offset, Limit: limit, Total: control.Total + runtime.Total}, nil
}
