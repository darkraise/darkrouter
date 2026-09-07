package config

import (
	"sync"
	"sync/atomic"
)

// Store holds the live configuration. A request takes one snapshot at entry and
// uses it for its whole lifetime, so a reload cannot change behavior underneath
// an in-flight request.
type Store struct {
	cur     atomic.Pointer[Config]
	lastErr atomic.Pointer[error]
	overlay atomic.Pointer[func(*Config) error]

	// load builds a fresh Config. Injected rather than called directly,
	// because this package may not import internal/store: store already
	// imports config and the reverse edge would close a cycle.
	load func() (*Config, error)

	// boot is the snapshot the process is actually running restart-only values
	// from. Pending-restart is boot versus current, never the diff between two
	// consecutive reloads: that diff is cleared by the next unrelated save
	// while the old value is still in force.
	boot atomic.Pointer[Config]

	// reloadMu serialises Reload. Several callers reach it, and two loads
	// racing to publish could land the older one last.
	reloadMu sync.Mutex
}

// SetOverlay installs a transform applied to every freshly-loaded Config
// before it is published.
//
// It is a function rather than a direct call because this package may not
// import internal/store: store already imports config, and the reverse edge
// would close a cycle. Aliases and policy reach a snapshot from SQLite through
// here, which is what lets every reader keep using config.Store.Current().
func (s *Store) SetOverlay(fn func(*Config) error) {
	s.overlay.Store(&fn)
}

func (s *Store) applyOverlay(c *Config) error {
	p := s.overlay.Load()
	if p == nil || *p == nil {
		return nil
	}
	return (*p)(c)
}

// NewStoreOf builds a store over a fixed Config. Tests that used to write a
// temporary YAML file to get a store use this instead; nothing in production
// calls it.
func NewStoreOf(c *Config) *Store {
	s := &Store{load: func() (*Config, error) { return c, nil }}
	s.cur.Store(c)
	s.boot.Store(c)
	return s
}

// NewStoreFrom builds a store over an injected loader instead of a file.
func NewStoreFrom(load func() (*Config, error)) (*Store, error) {
	s := &Store{load: load}
	c, err := load()
	if err != nil {
		return nil, err
	}
	s.cur.Store(c)
	s.boot.Store(c)
	return s, nil
}

func (s *Store) Current() *Config { return s.cur.Load() }

// MarkBoot rebases the pending-restart baseline onto the current snapshot.
//
// The constructor cannot do this itself: the overlay that merges database-owned
// blocks is installed after construction, so the snapshot a constructor sees is
// not the one the process goes on to run. Called once, after the first
// successful Reload, it makes "pending restart" mean "differs from what this
// process is actually running".
func (s *Store) MarkBoot() { s.boot.Store(s.cur.Load()) }

// PendingRestart names every restart-only field whose stored value differs
// from the one this process started with.
func (s *Store) PendingRestart() []string {
	boot, cur := s.boot.Load(), s.cur.Load()
	if boot == nil || cur == nil {
		return nil
	}
	var out []string
	for _, f := range restartOnlyFields {
		if f.value(boot) != f.value(cur) {
			out = append(out, f.name)
		}
	}
	return out
}

func (s *Store) LastError() error {
	if p := s.lastErr.Load(); p != nil {
		return *p
	}
	return nil
}

// RecordError surfaces a failure that happened outside Reload — a startup step
// that reports nowhere else, which would otherwise leave the process looking
// healthy while something it needs never came up.
func (s *Store) RecordError(err error) {
	if err == nil {
		return
	}
	s.lastErr.Store(&err)
}

// Reload builds and validates the configuration in full, swapping only on
// success. A load that fails is rejected wholesale and the previous
// configuration stays live: a broken edit must never take the gateway down.
func (s *Store) Reload() error {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	next, err := s.loadNext()
	if err != nil {
		s.lastErr.Store(&err)
		return err
	}
	// Before publishing, not after: a snapshot carrying pre-overlay aliases for
	// even an instant is one a request could be routed by.
	if err := s.applyOverlay(next); err != nil {
		s.lastErr.Store(&err)
		return err
	}
	prev := s.cur.Load()
	next.Warnings = append(next.Warnings, restartOnlyWarnings(prev, next)...)
	s.cur.Store(next)
	s.lastErr.Store(nil)
	return nil
}

func (s *Store) loadNext() (*Config, error) { return s.load() }

// restartOnlyWarnings names every restart-only field this edit changed.
//
// Driven by the same table RestartOnly is built from, so a field cannot be
// cold in one and hot in the other.
func restartOnlyWarnings(prev, next *Config) []string {
	if prev == nil {
		return nil
	}
	var out []string
	for _, f := range restartOnlyFields {
		if f.value(prev) != f.value(next) {
			out = append(out, f.name+" changed; takes effect on restart")
		}
	}
	return out
}
