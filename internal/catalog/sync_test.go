package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

const syncDoc = `{"acme":{"id":"acme","models":{
  "big":{"id":"big","tool_call":true,"reasoning":true,
         "modalities":{"input":["text","image"],"output":["text"]},
         "limit":{"context":200000,"output":64000},
         "cost":{"input":5,"output":25}},
  "cheap":{"id":"cheap","tool_call":false,
           "modalities":{"input":["text"],"output":["text"]},
           "limit":{"context":8192,"output":4096},
           "cost":{"input":0.14,"output":0.28}}
}}}`

func syncFixture(t *testing.T) (*store.DB, *staticSource, *Store) {
	t.Helper()
	ctx := context.Background()
	db := discoveryDB(t, "p")
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE providers SET preset = 'acme' WHERE id = 'p'`); err != nil {
		t.Fatal(err)
	}
	// Discovery has already established what exists; the sync only enriches.
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]store.DiscoveredModel{{ModelID: "big"}, {ModelID: "cheap"}, {ModelID: "private"}}, 0, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	src := &staticSource{ps: []provider.Provider{{ID: "p", Kind: "openaicompat", Preset: "acme"}}}
	return db, src, NewStore(db, src)
}

// testPresets injects a preset the shipped file does not carry, so the sync
// test does not depend on a generated file's contents.
func testPresets() Presets {
	return Presets{"acme": {Name: "Acme", Kind: "openaicompat", ModelsDevID: "acme"}}
}

func TestSyncWritesPricesAndLimits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(syncDoc))
	}))
	defer srv.Close()

	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{URL: srv.URL, Presets: testPresets()})
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	rows, _ := db.Models(context.Background())
	byID := map[string]store.ModelRow{}
	for _, r := range rows {
		byID[r.ModelID] = r
	}
	if got := byID["cheap"].InputMicrosPerMTok; got != 140_000 {
		t.Errorf("cheap input price = %d, want 140000", got)
	}
	if got := byID["big"].ContextWindow; got != 200_000 {
		t.Errorf("big context = %d", got)
	}
	if !byID["big"].Capabilities.Vision || !byID["big"].Capabilities.Tools {
		t.Errorf("big capabilities = %+v", byID["big"].Capabilities)
	}
	if byID["big"].CapabilitiesSource != "models_dev" {
		t.Errorf("big source = %q", byID["big"].CapabilitiesSource)
	}
	// A model models.dev has never heard of keeps its inferred metadata rather
	// than acquiring somebody else's.
	if byID["private"].CapabilitiesSource != "inferred" || byID["private"].ContextWindow != 0 {
		t.Errorf("private = %+v", byID["private"])
	}
}

func TestSyncRebuildsTheSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(syncDoc))
	}))
	defer srv.Close()

	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{URL: srv.URL, Presets: testPresets()})
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, ok := cat.Snapshot().Lookup("p", "big")
	if !ok {
		t.Fatal("big is missing from the rebuilt snapshot")
	}
	if m.ContextWindow != 200_000 {
		t.Errorf("snapshot context = %d", m.ContextWindow)
	}
}

func TestSyncSurvivesEveryFailureShape(t *testing.T) {
	// Spec §4: a fetch error leaves the cache and logs a warning. All three
	// shapes must leave the already-written prices exactly as they were.
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(syncDoc))
	}))
	defer good.Close()

	db, src, cat := syncFixture(t)
	if err := NewSyncer(db, src, cat, SyncOptions{URL: good.URL, Presets: testPresets()}).
		SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	cases := map[string]http.HandlerFunc{
		"500": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
		"malformed": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"acme":{"models":`))
		},
		"empty": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) },
		"html":  func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>502</html>`)) },
	}
	for name, h := range cases {
		bad := httptest.NewServer(h)
		s := NewSyncer(db, src, cat, SyncOptions{URL: bad.URL, Presets: testPresets()})
		if err := s.SyncOnce(context.Background()); err == nil {
			t.Errorf("%s: SyncOnce returned nil, want an error", name)
		}
		bad.Close()

		rows, _ := db.Models(context.Background())
		for _, r := range rows {
			if r.ModelID == "cheap" && r.InputMicrosPerMTok != 140_000 {
				t.Errorf("%s: the cache was damaged; cheap price = %d", name, r.InputMicrosPerMTok)
			}
		}
	}
}

func TestSyncTimesOut(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{
		URL: srv.URL, Timeout: 100 * time.Millisecond, Presets: testPresets(),
	})
	start := time.Now()
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Error("a hung CDN did not time out")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v; the timeout is not applied", elapsed)
	}
}

func TestColdStartWithNoNetworkStillHasMetadata(t *testing.T) {
	// The done criterion: Darkrouter starts and serves with no outbound access
	// to models.dev. The embedded snapshot is what makes that produce real
	// prices rather than a blank catalog.
	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{
		URL: "http://127.0.0.1:1/api.json", Timeout: time.Second, Presets: testPresets(),
	})
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("an unreachable host returned no error")
	}
	// The syncer must still serve the embedded document, so the merge has
	// something to work with.
	if len(s.Doc()) < 100 {
		t.Errorf("Doc() has %d providers with no network; the fallback is not wired", len(s.Doc()))
	}
	if err := cat.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := cat.Snapshot().Lookup("p", "big"); !ok {
		t.Error("the catalog is unusable with no network")
	}
}

func TestSyncDoesNotOverwriteDiscoveredCapabilities(t *testing.T) {
	// A runtime that reported its own tool support outranks models.dev's entry
	// for a model of the same name. Both the label and the values must survive.
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(syncDoc))
	}))
	defer srv.Close()

	db, src, cat := syncFixture(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []store.DiscoveredModel{
		{ModelID: "big", Capabilities: &store.ModelCapabilities{Tools: false}},
		{ModelID: "cheap"}, {ModelID: "private"},
	}, 0, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := NewSyncer(db, src, cat, SyncOptions{URL: srv.URL, Presets: testPresets()}).
		SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Models(ctx)
	for _, r := range rows {
		if r.ModelID != "big" {
			continue
		}
		if r.CapabilitiesSource != "discovered" {
			t.Errorf("source = %q, want discovered", r.CapabilitiesSource)
		}
		if r.Capabilities.Tools {
			t.Error("models.dev overwrote a discovered capability")
		}
		// Limits and pricing are a different field and still come from
		// models.dev: precedence is per field.
		if r.ContextWindow != 200_000 {
			t.Errorf("context = %d; capability precedence leaked into limits", r.ContextWindow)
		}
	}
}

func TestSyncRunStopsOnCancel(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(syncDoc))
	}))
	defer srv.Close()

	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{
		URL: srv.URL, Interval: time.Hour, Presets: testPresets(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run(ctx) }()

	// Run syncs once immediately so a fresh install is priced without waiting
	// twelve hours.
	deadline := time.After(5 * time.Second)
	for hits.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("Run never performed its first sync")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}
