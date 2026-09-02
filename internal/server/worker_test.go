package server

import (
	"context"
	"errors"
	"testing"
)

// A worker that panics is restarted rather than taking its job down with it
// for the rest of the process lifetime.
func TestRunWorkerRestartsAfterAPanic(t *testing.T) {
	calls := 0
	err := runWorker(context.Background(), "flaky", func(context.Context) error {
		calls++
		if calls == 1 {
			panic("boom")
		}
		return nil
	}, 0)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if calls != 2 {
		t.Errorf("worker ran %d times, want 2", calls)
	}
}

func TestRunWorkerStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := runWorker(ctx, "flaky", func(context.Context) error {
		calls++
		cancel()
		panic("boom")
	}, 0)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 {
		t.Errorf("a cancelled worker must not be restarted: %d runs", calls)
	}
}

func TestRunWorkerReturnsAnOrdinaryError(t *testing.T) {
	want := errors.New("stop")
	if err := runWorker(context.Background(), "w", func(context.Context) error { return want }, 0); err != want {
		t.Fatalf("err = %v", err)
	}
}
