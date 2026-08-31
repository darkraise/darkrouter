package config

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// debounce absorbs the several write events an editor emits for one save, and
// avoids reading a half-written file.
const debounce = 100 * time.Millisecond

// Store holds the live configuration. A request takes one snapshot at entry and
// uses it for its whole lifetime, so a reload cannot change behavior underneath
// an in-flight request.
type Store struct {
	path    string
	lookup  func(string) (string, bool)
	cur     atomic.Pointer[Config]
	lastErr atomic.Pointer[error]
	overlay atomic.Pointer[func(*Config) error]
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

func NewStore(path string, lookup func(string) (string, bool)) (*Store, error) {
	s := &Store{path: path, lookup: lookup}
	c, err := Load(path, lookup)
	if err != nil {
		return nil, err
	}
	s.cur.Store(c)
	return s, nil
}

func (s *Store) Current() *Config { return s.cur.Load() }

// Path reports the file the store watches. Tests rewrite it to exercise a
// reload; nothing on the request path needs it.
func (s *Store) Path() string { return s.path }

func (s *Store) LastError() error {
	if p := s.lastErr.Load(); p != nil {
		return *p
	}
	return nil
}

// RecordError surfaces a failure that happened outside Reload — notably a
// watcher that could not start, which would otherwise leave hot reload silently
// dead for the process lifetime.
func (s *Store) RecordError(err error) {
	if err == nil {
		return
	}
	s.lastErr.Store(&err)
}

// Reload parses and validates the file in full, swapping only on success.
// A file that fails validation is rejected wholesale and the previous
// configuration stays live: a broken edit must never take the gateway down.
func (s *Store) Reload() error {
	next, err := Load(s.path, s.lookup)
	if err != nil {
		s.lastErr.Store(&err)
		return err
	}
	// Before publishing, not after: a snapshot carrying the file's aliases for
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

// Watch reloads on change until ctx is cancelled. It watches the parent
// directory and filters by filename, because a watch on the file itself is lost
// the first time an editor saves by rename.
func (s *Store) Watch(ctx context.Context) error { return s.watch(ctx, nil) }

// watch closes ready once the directory watch is established. The kernel does
// not replay events that predate the watch, so a caller that edits the file
// without waiting for this loses the notification permanently.
func (s *Store) watch(ctx context.Context, ready chan<- struct{}) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	dir, name := filepath.Split(s.path)
	if dir == "" {
		dir = "."
	}
	if err := w.Add(dir); err != nil {
		return fmt.Errorf("watch %s: %w", dir, err)
	}
	if ready != nil {
		close(ready)
	}

	var timer *time.Timer
	var fire <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if filepath.Base(ev.Name) != name {
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(debounce)
			fire = timer.C
		case <-fire:
			fire = nil
			_ = s.Reload() // the error is recorded in LastError
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			s.lastErr.Store(&err)
		}
	}
}
