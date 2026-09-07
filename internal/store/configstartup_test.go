package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// startupStore reproduces the sequence cmd/darkrouter runs: load from the
// database, install the overlay, reload, rebase the boot baseline. Anything
// this produces is what an operator sees on /healthz at the first request.
func startupStore(t *testing.T, d *DB) *config.Store {
	t.Helper()
	ctx := context.Background()
	boot := config.BootstrapFrom(func(string) (string, bool) { return "", false })
	s, err := config.NewStoreFrom(func() (*config.Config, error) {
		return LoadConfig(ctx, d, boot)
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetOverlay(func(c *config.Config) error { return OverlayConfig(ctx, d, c) })
	if err := s.Reload(); err != nil {
		t.Fatalf("the first reload must not fail: %v", err)
	}
	s.MarkBoot()
	return s
}

// The first reload diffs a pre-overlay snapshot against a post-overlay one, so
// anything the overlay changes reads as an edit that needs a restart. On a
// clean start it must change nothing: a warning here tells an operator to
// restart a process that has been running the right values all along.
func TestACleanStartWarnsAboutNoRestart(t *testing.T) {
	s := startupStore(t, migrated(t))
	for _, w := range s.Current().Warnings {
		if strings.Contains(w, "takes effect on restart") {
			t.Errorf("a clean start warned %q", w)
		}
	}
	if got := s.PendingRestart(); len(got) != 0 {
		t.Errorf("PendingRestart = %v on a clean start, want none", got)
	}
}

// The same, with a restart-only policy key stored. LoadConfig reads it from
// the registry and OverlayConfig reapplies it from the same row, so the two
// snapshots have to agree; if they ever stop agreeing, every start warns.
func TestAStoredPolicyValueDoesNotReadAsARestartPendingEdit(t *testing.T) {
	d := migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, d.Write, "policy.timeout.connect", "3s"); err != nil {
		t.Fatal(err)
	}

	s := startupStore(t, d)
	if got := s.Current().Policy.Timeout.Connect.String(); got != "3s" {
		t.Fatalf("connect = %s, want the stored 3s", got)
	}
	for _, w := range s.Current().Warnings {
		if strings.Contains(w, "takes effect on restart") {
			t.Errorf("a stored value the process is already running warned %q", w)
		}
	}
	if got := s.PendingRestart(); len(got) != 0 {
		t.Errorf("PendingRestart = %v, want none", got)
	}
}

// Settings content must never fail startup. A policy row that will not parse
// is reverted by LoadConfig with a warning, and the overlay must not then
// reintroduce it as a hard error out of the same row.
func TestAnUnparseablePolicyRowDoesNotFailStartup(t *testing.T) {
	d := migrated(t)
	if err := putSetting(context.Background(), d.Write,
		"policy.timeout.connect", "not-a-duration"); err != nil {
		t.Fatal(err)
	}

	s := startupStore(t, d)
	if got := s.Current().Policy.Timeout.Connect; got != 10*time.Second {
		t.Errorf("connect = %v, want the compiled default", got)
	}
}
