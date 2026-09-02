package main

import (
	"context"
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
