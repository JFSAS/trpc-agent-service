package bootstrap

import (
	"context"
	"testing"
	"time"
)

func TestConsumptionReconciliationLoopBackoffCursorAndCancellation(t *testing.T) {
	const interval = 40 * time.Millisecond
	f := &quotaLoopFixture{calls: make(chan quotaTick, 8)}
	a := &App{consumption: f, config: Config{Timing: Timing{PollInterval: Duration(interval), OperationTimeout: Duration(time.Second)}, Limits: Limits{ScanBatch: 7}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.reconcileConsumption(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("quota loop did not stop")
		}
	})
	last := time.Time{}
	for _, cursor := range []string{"", "run-a", "run-b", ""} {
		select {
		case tick := <-f.calls:
			if tick.cursor != cursor || tick.batch != 7 || tick.deadline.IsZero() {
				t.Fatal("cursor/batch/deadline", tick)
			}
			if !last.IsZero() && tick.at.Sub(last) < interval-5*time.Millisecond {
				t.Fatal("busy polling", tick.at.Sub(last))
			}
			last = tick.at
		case <-time.After(2 * time.Second):
			t.Fatal("loop stopped making progress")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown wait was not cancelled")
	}
}

func TestConsumptionReconciliationLoopCancelsInFlightOperation(t *testing.T) {
	f := &quotaLoopFixture{calls: make(chan quotaTick, 2), mode: "timeout"}
	a := &App{consumption: f, config: Config{Timing: Timing{PollInterval: Duration(time.Second), OperationTimeout: Duration(10 * time.Second)}, Limits: Limits{ScanBatch: 1}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.reconcileConsumption(ctx) }()
	select {
	case <-f.calls:
	case <-time.After(time.Second):
		t.Fatal("no first operation")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("in-flight operation did not inherit cancellation")
	}
}

func TestConsumptionReconciliationLoopOperationDeadline(t *testing.T) {
	f := &quotaLoopFixture{calls: make(chan quotaTick, 2), mode: "timeout"}
	a := &App{consumption: f, config: Config{Timing: Timing{PollInterval: Duration(20 * time.Millisecond), OperationTimeout: Duration(30 * time.Millisecond)}, Limits: Limits{ScanBatch: 1}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.reconcileConsumption(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("loop did not stop")
		}
	})
	var first time.Time
	for n := 0; n < 2; n++ {
		select {
		case tick := <-f.calls:
			if n == 0 {
				first = tick.at
			} else if tick.at.Sub(first) < 45*time.Millisecond {
				t.Fatal("deadline/backoff bypassed")
			}
		case <-time.After(time.Second):
			t.Fatal("deadline did not permit next scan")
		}
	}
}
