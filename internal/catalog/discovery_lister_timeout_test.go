package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/provider"
)

type hangingLister struct{ release chan struct{} }

func (h hangingLister) List(ctx context.Context, _ Probe) ([]Discovered, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.release:
		return nil, context.Canceled
	}
}

// The sweep waits for every probe and the worker is one loop, so a lister
// that never answers would stop every later sweep and trigger.
func TestAHungListerIsBoundedByTheProbeTimeout(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	db := discoveryDB(t, "bed")
	src := &staticSource{ps: []provider.Provider{{
		ID: "bed", Kind: "bedrock", Region: "us-east-1", AuthStyle: "none",
	}}}
	d := NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{
		Timeout: 50 * time.Millisecond,
		Listers: map[string]KindLister{"bedrock": hangingLister{release: release}},
	})

	done := make(chan struct{})
	go func() {
		d.SweepOnce(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep did not finish; the lister ran without the probe timeout")
	}
	states, err := db.DiscoveryStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if states["bed"].ConsecutiveFailures != 1 {
		t.Errorf("failures = %d, want 1: a timed-out listing is a failure", states["bed"].ConsecutiveFailures)
	}
}
