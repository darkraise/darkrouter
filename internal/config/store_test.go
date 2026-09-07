package config

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// storeOver returns a store whose loader hands back a copy of stored, so a
// test can change what the next reload sees the way an admin edit changes what
// the database answers.
func storeOver(t *testing.T, stored *Config) *Store {
	t.Helper()
	s, err := NewStoreFrom(func() (*Config, error) {
		next := *stored
		return &next, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// defaulted is the compiled default configuration, the starting point every
// fixture in this file edits.
func defaulted() *Config {
	c := &Config{}
	applyDefaults(c)
	return c
}

func TestStoreServesCurrentConfig(t *testing.T) {
	c := defaulted()
	c.Providers = []ProviderConfig{{ID: "groq"}}
	if got := NewStoreOf(c).Current().Providers[0].ID; got != "groq" {
		t.Fatalf("provider = %q, want groq", got)
	}
}

func TestReloadAppliesValidChange(t *testing.T) {
	stored := defaulted()
	stored.Providers = []ProviderConfig{{ID: "groq"}}
	s := storeOver(t, stored)

	stored.Providers = []ProviderConfig{{ID: "renamed"}}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := s.Current().Providers[0].ID; got != "renamed" {
		t.Fatalf("provider = %q; the reload did not apply", got)
	}
}

func TestReloadWarnsOnRestartOnlyChange(t *testing.T) {
	stored := defaulted()
	s := storeOver(t, stored)

	stored.Policy.Timeout.Connect = 3 * time.Second
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range s.Current().Warnings {
		if strings.Contains(w, "policy.timeout.connect") && strings.Contains(w, "restart") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a restart-required warning, got %v", s.Current().Warnings)
	}
}

func TestRestartOnlyNamesTheWorkerIntervals(t *testing.T) {
	// The catalog sync worker and the discovery sweeper each capture their
	// interval into an options struct at construction, so a reload that
	// changes one takes effect only at the next process start.
	want := []string{"catalog.sync_interval", "catalog.discovery.interval"}
	for _, field := range want {
		found := false
		for _, got := range RestartOnly {
			if got == field {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s is restart-only in behaviour but RestartOnly does not name it", field)
		}
	}
}

func TestReloadWarnsOnWorkerIntervalChange(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
		match  string
	}{
		{
			name:   "sync interval",
			change: func(c *Config) { c.Catalog.SyncInterval = 3 * time.Hour },
			match:  "catalog.sync_interval",
		},
		{
			name:   "discovery interval",
			change: func(c *Config) { c.Catalog.Discovery.Interval = 3 * time.Hour },
			match:  "catalog.discovery.interval",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := defaulted()
			s := storeOver(t, stored)
			tc.change(stored)
			if err := s.Reload(); err != nil {
				t.Fatal(err)
			}
			for _, w := range s.Current().Warnings {
				if strings.Contains(w, tc.match) && strings.Contains(w, "restart") {
					return
				}
			}
			t.Fatalf("no restart warning for %s, got %v", tc.match, s.Current().Warnings)
		})
	}
}

func TestOverlayAppliesOnEveryReload(t *testing.T) {
	// A reload that dropped the overlay would silently restore the loader's
	// aliases until the next restart, which is the whole failure the overlay
	// exists to prevent.
	stored := defaulted()
	stored.Providers = []ProviderConfig{{ID: "groq"}}
	s := storeOver(t, stored)
	s.SetOverlay(func(c *Config) error {
		c.Aliases = map[string][]string{"from-db": {"groq/llama"}}
		return nil
	})
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := s.Current().Aliases["from-db"]; len(got) != 1 {
		t.Fatalf("overlay did not reach the first reload: %v", s.Current().Aliases)
	}

	stored.Providers = []ProviderConfig{{ID: "renamed"}}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := s.Current().Aliases["from-db"]; len(got) != 1 {
		t.Fatalf("overlay was dropped by a later reload: %v", s.Current().Aliases)
	}
	if s.Current().Providers[0].ID != "renamed" {
		t.Fatal("the overlay swallowed the loader's own change")
	}
}

func TestOverlayFailureKeepsThePreviousConfig(t *testing.T) {
	stored := defaulted()
	stored.Providers = []ProviderConfig{{ID: "groq"}}
	s := storeOver(t, stored)
	s.SetOverlay(func(*Config) error { return errors.New("database unreachable") })
	if err := s.Reload(); err == nil {
		t.Fatal("expected the reload to fail")
	}
	if s.Current().Providers[0].ID != "groq" {
		t.Fatal("a failed overlay must leave the previous config live")
	}
	if s.LastError() == nil {
		t.Fatal("expected LastError to record the overlay failure")
	}
}

// The admin API and a background reload both call Reload; two at once must not
// interleave a stale snapshot over a newer one.
func TestConcurrentReloadsPublishTheLatest(t *testing.T) {
	stored := defaulted()
	stored.Providers = []ProviderConfig{{ID: "groq"}}
	s := storeOver(t, stored)

	stored.Providers = []ProviderConfig{{ID: "latest"}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Reload()
		}()
	}
	wg.Wait()
	if s.Current().Providers[0].ID != "latest" {
		t.Fatalf("published provider = %q", s.Current().Providers[0].ID)
	}
}

// restartOnlyWarnings diffs consecutive snapshots, so the next unrelated write
// clears the warning while the process is still running the old value. The
// pending set has to be measured against boot, not against the last reload.
func TestPendingRestartSurvivesAnUnrelatedReload(t *testing.T) {
	// What the loader returns next. Mutated between reloads, the way an edit
	// through the admin API changes what the database answers.
	stored := &Config{}
	applyDefaults(stored)
	stored.Catalog.SyncInterval = 12 * time.Hour

	s, err := NewStoreFrom(func() (*Config, error) {
		next := *stored
		return &next, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.PendingRestart(); len(got) != 0 {
		t.Fatalf("PendingRestart = %v at boot, want none", got)
	}

	stored.Catalog.SyncInterval = 6 * time.Hour
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingRestart(); len(got) != 1 || got[0] != "catalog.sync_interval" {
		t.Fatalf("PendingRestart = %v, want [catalog.sync_interval]", got)
	}

	// An unrelated hot-reloadable change must not clear it: the process is
	// still running the sync interval it booted with. The sync interval is
	// deliberately left where the previous reload put it, so the only thing
	// that can keep it pending is the comparison against boot.
	stored.Log.Retention = 100 * time.Hour
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingRestart(); len(got) != 1 || got[0] != "catalog.sync_interval" {
		t.Fatalf("PendingRestart = %v after an unrelated write, want it still pending", got)
	}
}

func TestNewStoreFromUsesTheInjectedLoader(t *testing.T) {
	n := 0
	s, err := NewStoreFrom(func() (*Config, error) {
		n++
		c := &Config{}
		applyDefaults(c)
		c.Log.Retention = time.Duration(n) * 100 * time.Hour
		return c, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Current().Log.Retention != 100*time.Hour {
		t.Errorf("retention = %v, want the loader's first value", s.Current().Log.Retention)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if s.Current().Log.Retention != 200*time.Hour {
		t.Errorf("retention = %v, want the loader's second value", s.Current().Log.Retention)
	}
}

// A loader that fails leaves the previous snapshot live. A reload that could
// take the gateway down is worse than one that does nothing.
func TestNewStoreFromKeepsTheOldSnapshotWhenTheLoaderFails(t *testing.T) {
	fail := false
	s, err := NewStoreFrom(func() (*Config, error) {
		if fail {
			return nil, errors.New("database is gone")
		}
		c := &Config{}
		applyDefaults(c)
		return c, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := s.Reload(); err == nil {
		t.Fatal("a failing loader must report its error")
	}
	if s.Current() == nil {
		t.Fatal("the previous snapshot must stay live")
	}
	if s.LastError() == nil {
		t.Error("the failure must reach LastError, which /readyz reads")
	}
}

// The overlay is installed after construction, so the snapshot the constructor
// captured as boot is not the one the process runs.
func TestMarkBootRebasesThePendingRestartBaseline(t *testing.T) {
	s, err := NewStoreFrom(func() (*Config, error) {
		c := &Config{}
		applyDefaults(c)
		return c, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetOverlay(func(c *Config) error {
		c.Policy.Timeout.Connect = 7 * time.Second
		return nil
	})
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingRestart(); len(got) != 1 || got[0] != "policy.timeout.connect" {
		t.Fatalf("PendingRestart() = %v, want [policy.timeout.connect] before MarkBoot", got)
	}
	s.MarkBoot()
	if got := s.PendingRestart(); len(got) != 0 {
		t.Errorf("PendingRestart() = %v, want nothing pending after MarkBoot", got)
	}
}

// The write and the snapshot it produces are one operation. A reload landing
// between them publishes a configuration the write has already superseded.
func TestUpdateBlocksAConcurrentReload(t *testing.T) {
	base := &Config{}
	ApplyDefaults(base)
	s, err := NewStoreFrom(func() (*Config, error) { dup := *base; return &dup, nil })
	if err != nil {
		t.Fatal(err)
	}

	inWrite, release := make(chan struct{}), make(chan struct{})
	s.SetWriter(func(context.Context, Patch) ([]string, error) {
		close(inWrite)
		<-release
		return []string{"log.retention"}, nil
	})

	updated := make(chan error, 1)
	go func() { _, err := s.Update(context.Background(), Patch{}); updated <- err }()
	<-inWrite

	reloaded := make(chan error, 1)
	go func() { reloaded <- s.Reload() }()
	select {
	case <-reloaded:
		t.Fatal("a reload ran while a write was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-updated; err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := <-reloaded; err != nil {
		t.Fatalf("Reload: %v", err)
	}
}

// The rows are durable whether or not the republish worked, so "the write was
// refused" and "the write landed and the old config is still serving" are two
// different answers and the caller has to be able to tell them apart.
func TestUpdateReportsAPublishFailureSeparately(t *testing.T) {
	boom := errors.New("boom")
	var loads int
	s, err := NewStoreFrom(func() (*Config, error) {
		loads++
		if loads == 1 {
			c := &Config{}
			ApplyDefaults(c)
			return c, nil
		}
		return nil, boom
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetWriter(func(context.Context, Patch) ([]string, error) {
		return []string{"log.retention"}, nil
	})

	written, err := s.Update(context.Background(), Patch{})
	var pub PublishError
	if !errors.As(err, &pub) {
		t.Fatalf("err = %v, want a PublishError", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want it to carry the load failure", err)
	}
	// Reported even so: those keys are in the database now.
	if len(written) != 1 || written[0] != "log.retention" {
		t.Errorf("written = %v, want the keys the write committed", written)
	}
}

func TestUpdateWithoutAWriterIsRefused(t *testing.T) {
	c := &Config{}
	ApplyDefaults(c)
	if _, err := NewStoreOf(c).Update(context.Background(), Patch{}); err == nil {
		t.Fatal("Update succeeded with no writer installed")
	}
}

// The other half of Update's contract. A refusal must reach the handler as a
// RejectedError so it can answer 400, and must report no committed keys: the
// write is one transaction, so an error means nothing landed.
func TestUpdateReportsNoKeysWhenTheWriteIsRefused(t *testing.T) {
	c := &Config{}
	ApplyDefaults(c)
	s := NewStoreOf(c)
	s.SetWriter(func(context.Context, Patch) ([]string, error) {
		return []string{"log.retention"}, Rejected("log.retention: no")
	})
	written, err := s.Update(context.Background(), Patch{})
	var rejected RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
	if written != nil {
		t.Errorf("written = %v, want nil: a refused write commits nothing", written)
	}
}

// Reload publishes what load returns. Returning the same pointer every time
// mutates the snapshot an in-flight request is already using.
func TestNewStoreOfPublishesAFreshSnapshotPerReload(t *testing.T) {
	c := &Config{}
	ApplyDefaults(c)
	s := NewStoreOf(c)
	before := s.Current()
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if s.Current() == before {
		t.Error("Reload republished the Config a request may already hold")
	}
}

func TestRejectedErrorIsMatchable(t *testing.T) {
	err := Rejected("policy.retry.max_attempts must be between 1 and %d", 10)
	var rejected RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
	if rejected.Error() != "policy.retry.max_attempts must be between 1 and 10" {
		t.Errorf("message = %q", rejected.Error())
	}
}

func TestBootstrapVarNamesTheVariableThatOwnsAKey(t *testing.T) {
	if name, ok := BootstrapVar("server.proxy_token"); !ok || name != "DARKROUTER_PROXY_TOKEN" {
		t.Errorf("BootstrapVar(server.proxy_token) = %q, %v", name, ok)
	}
	if _, ok := BootstrapVar("log.retention"); ok {
		t.Error("log.retention is a stored key, not a bootstrap one")
	}
}
