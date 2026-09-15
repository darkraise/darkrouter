package catalog

import (
	"context"
	"net/http"
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

// slowSignedLister signs and waits once per call, as Bedrock's lister does for
// its foundation-model call and each inference-profile page.
type slowSignedLister struct {
	calls int
	each  time.Duration
}

func (s slowSignedLister) List(ctx context.Context, p Probe) ([]Discovered, error) {
	for i := 0; i < s.calls; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://bedrock.example/", nil)
		if err != nil {
			return nil, err
		}
		if err := p.Authorize(ctx, req); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.each):
		}
	}
	return []Discovered{{ModelID: "anthropic.claude-x"}}, nil
}

// The timeout bounds one call. A listing of several calls that are each well
// within it is a healthy listing, however long the calls take together.
func TestTheProbeTimeoutBoundsEachListerCallNotTheWholeListing(t *testing.T) {
	db := discoveryDB(t, "bed")
	src := &staticSource{ps: []provider.Provider{{
		ID: "bed", Kind: "bedrock", Region: "us-east-1", AuthStyle: "sigv4",
		Credentials: []provider.Credential{{ID: "k", Secret: "AKIA:secret", Enabled: true}},
	}}}
	d := NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{
		Timeout: 200 * time.Millisecond,
		Auth:    fakeAuthResolver{header: "signed"},
		Listers: map[string]KindLister{"bedrock": slowSignedLister{calls: 3, each: 120 * time.Millisecond}},
	})
	d.SweepOnce(context.Background())

	states, err := db.DiscoveryStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st := states["bed"]; st.ConsecutiveFailures != 0 {
		t.Errorf("failures = %d (%q), want 0: no call exceeded the timeout", st.ConsecutiveFailures, st.LastError)
	}
	rows, err := db.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("rows = %+v, want the listed model", rows)
	}
}
