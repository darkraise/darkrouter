package health

import (
	"context"
	"log/slog"
	"time"
)

// Store is the durable side of the breaker. internal/store satisfies it; the
// interface exists so this package does not depend on SQLite.
type Store interface {
	SaveHealth(ctx context.Context, entries []Entry) error
	LoadHealth(ctx context.Context) ([]Entry, error)
}

// Persister copies breaker state to the database on an interval, and once more
// on shutdown.
type Persister struct {
	b        *Breaker
	s        Store
	interval time.Duration
}

func NewPersister(b *Breaker, s Store, interval time.Duration) *Persister {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Persister{b: b, s: s, interval: interval}
}

// Restore rehydrates the breaker at startup, so a restart does not stampede a
// provider that is still rate-limited.
func (p *Persister) Restore(ctx context.Context) error {
	entries, err := p.s.LoadHealth(ctx)
	if err != nil {
		return err
	}
	p.b.Rehydrate(entries)
	return nil
}

// Run writes changed state until ctx is cancelled, then flushes unconditionally.
func (p *Persister) Run(ctx context.Context) error {
	t := time.NewTicker(p.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			// A background context, because the shutdown context is already
			// cancelled and would abort the write this flush exists to perform.
			return p.s.SaveHealth(context.Background(), p.b.Snapshot())
		case <-t.C:
			// The interval is the debounce. A provider failing fast changes
			// state many times between ticks and costs one write.
			if !p.b.TakeDirty() {
				continue
			}
			if err := p.s.SaveHealth(ctx, p.b.Snapshot()); err != nil {
				// Logged rather than fatal: losing a health write costs a
				// restart's worth of accuracy, not correctness.
				slog.Error("health persister failed", "err", err)
			}
		}
	}
}
