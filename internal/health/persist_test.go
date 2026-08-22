package health

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
)

type fakeStore struct {
	mu     sync.Mutex
	saves  [][]Entry
	loaded []Entry
	err    error
}

func (f *fakeStore) SaveHealth(_ context.Context, e []Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	cp := make([]Entry, len(e))
	copy(cp, e)
	f.saves = append(f.saves, cp)
	return nil
}

func (f *fakeStore) LoadHealth(context.Context) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loaded, f.err
}

func (f *fakeStore) saveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saves)
}

func (f *fakeStore) last() []Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.saves) == 0 {
		return nil
	}
	return f.saves[len(f.saves)-1]
}

func TestPersisterWritesWhenDirty(t *testing.T) {
	b := New(3, time.Minute)
	fs := &fakeStore{}
	p := NewPersister(b, fs, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})

	deadline := time.After(3 * time.Second)
	for fs.saveCount() == 0 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("the persister never wrote a dirty snapshot")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done

	last := fs.last()
	if len(last) != 1 || last[0].ConsecutiveFailures < 1 {
		t.Errorf("snapshot = %+v", last)
	}
}

// Debouncing is the point: many failures between ticks must not produce many
// writes.
func TestPersisterDoesNotWriteWhenClean(t *testing.T) {
	b := New(3, time.Minute)
	fs := &fakeStore{}
	p := NewPersister(b, fs, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	time.Sleep(60 * time.Millisecond) // many ticks, no state changes
	cancel()
	<-done

	// Only the unconditional shutdown flush may have happened.
	if n := fs.saveCount(); n > 1 {
		t.Errorf("%d writes with no state changes; the dirty flag is not being consulted", n)
	}
}

func TestPersisterFlushesOnShutdownEvenIfClean(t *testing.T) {
	b := New(3, time.Minute)
	fs := &fakeStore{}
	p := NewPersister(b, fs, time.Hour) // the ticker will never fire

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if fs.saveCount() != 1 {
		t.Fatalf("shutdown wrote %d snapshots, want 1", fs.saveCount())
	}
	if len(fs.last()) != 1 {
		t.Errorf("shutdown snapshot = %+v", fs.last())
	}
}

func TestRestoreRehydratesTheBreaker(t *testing.T) {
	b := New(3, time.Minute)
	fs := &fakeStore{loaded: []Entry{
		{Key: triple, ConsecutiveFailures: 2},
	}}
	p := NewPersister(b, fs, time.Hour)
	if err := p.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Two failures were restored, so the next one trips at trip_after 3.
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if b.Available(triple) {
		t.Fatal("restored failure counters were not applied")
	}
}
