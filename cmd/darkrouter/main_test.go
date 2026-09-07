package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"log/slog"
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
// message explaining nothing; accepting it silently would leave them believing
// the file is still read.
func TestConfigFlagIsAcceptedIgnoredAndWarnedAbout(t *testing.T) {
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

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	warnIgnoredConfigFlag(legacyConfig)
	if !strings.Contains(buf.String(), "/etc/darkrouter/darkrouter.yaml") {
		t.Errorf("no warning naming the ignored file; log was %q", buf.String())
	}

	buf.Reset()
	warnIgnoredConfigFlag("")
	if buf.Len() != 0 {
		t.Errorf("a run without -config warned anyway: %q", buf.String())
	}
}
