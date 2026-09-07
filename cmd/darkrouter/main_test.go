package main

import (
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The first signal starts the drain; the second must kill the process rather
// than be swallowed by the same context.
func TestTheSecondSignalIsNotSwallowed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	armSecondSignal(ctx, func() { close(stopped) })
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("signal handling was not released after the first signal")
	}
}

// The container image's own command still carries -config, and so does every
// operator who copied it. Rejecting the flag would refuse to start with a
// message explaining nothing.
func TestConfigFlagIsAcceptedAndIgnored(t *testing.T) {
	fs := flag.NewFlagSet("darkrouter", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath, legacyConfig, err := parseFlags(fs, []string{"-config", "/etc/darkrouter/darkrouter.yaml"})
	if err != nil {
		t.Fatalf("-config was rejected: %v", err)
	}
	if legacyConfig != "/etc/darkrouter/darkrouter.yaml" {
		t.Errorf("legacyConfig = %q, want the path passed", legacyConfig)
	}
	if dbPath != "" {
		t.Errorf("dbPath = %q; -config must not decide where the database lives", dbPath)
	}
}

// The console reads this list. A warning that only reaches the log is one an
// operator working in the console never sees, which is the same as not
// warning at all for the person the notice is for.
func TestStartupWarningsNameALeftoverFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "darkrouter.yaml")
	if err := os.WriteFile(legacy, []byte("server: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := startupWarnings(filepath.Join(dir, "darkrouter.db"), "")
	if len(got) != 1 {
		t.Fatalf("warnings = %v, want one", got)
	}
	if !strings.Contains(got[0], "darkrouter.yaml") {
		t.Errorf("the warning does not name the file: %q", got[0])
	}
	if !strings.Contains(got[0], "no longer read") {
		t.Errorf("the warning does not say what changed: %q", got[0])
	}
}

func TestStartupWarningsAreEmptyWithNoLeftoverFile(t *testing.T) {
	dir := t.TempDir()
	if got := startupWarnings(filepath.Join(dir, "darkrouter.db"), ""); len(got) != 0 {
		t.Errorf("warnings = %v, want none", got)
	}
}

// The flag is accepted as a no-op for one release, and an operator whose
// entrypoint still passes it should be told in the console rather than only in
// the log they are not reading.
func TestStartupWarningsNameAnIgnoredConfigFlag(t *testing.T) {
	dir := t.TempDir()
	got := startupWarnings(filepath.Join(dir, "darkrouter.db"), "/etc/darkrouter/darkrouter.yaml")
	if len(got) != 1 {
		t.Fatalf("warnings = %v, want one", got)
	}
	if !strings.Contains(got[0], "/etc/darkrouter/darkrouter.yaml") {
		t.Errorf("the warning does not name the flag's value: %q", got[0])
	}
}
