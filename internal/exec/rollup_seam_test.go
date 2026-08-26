package exec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// TestRollupSeesTokensTheExecutorActuallyLogged drives a real request through
// the executor's own HTTP handling and logging path -- not a hand-built
// store.RequestRecord -- so that a regression in the code between "the
// provider reported usage" and "usage_daily reflects it" fails this test
// instead of ten reviews of fixtures that never exercised that seam.
func TestRollupSeesTokensTheExecutorActuallyLogged(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"message":
			{"content":"pong"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1000,"completion_tokens":500}}`))
	}))
	defer up.Close()

	db := store.MigratedForTest(t)
	logWriter := store.NewLogWriter(db, store.LogOptions{BatchSize: 1})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		_ = logWriter.Run(ctx)
		close(runDone)
	}()

	cat := catalogOf(catalog.Model{
		ProviderID: "groq", ModelID: "m",
		Surfaces: []ir.Surface{ir.SurfaceLLM},
		Pricing: catalog.Pricing{
			InputMicrosPerMTok: 3_000_000, OutputMicrosPerMTok: 3_000_000, Known: true,
		},
	})

	fleet := []provider.Provider{{
		ID: "groq", BaseURL: up.URL, Kind: "openaicompat", Models: []string{"m"},
		Credentials: []provider.Credential{{ID: "k1", Secret: "k1", Enabled: true}},
	}}

	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	if err := os.WriteFile(path,
		[]byte("server:\n  proxy_listen: :0\n  admin_listen: :0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	b := health.New(3, 15*time.Minute)
	e := New(cfgStore, &fleetSource{ps: fleet},
		map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{
			Log: logWriter, Health: b, Fleet: b, Catalog: cat,
		})

	rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Cancelling drains the channel and flushes synchronously before Run
	// returns, so by the time this closes the request row is committed --
	// no sleep-and-hope needed to observe an asynchronous writer.
	cancel()
	<-runDone

	if err := db.Rollup(context.Background(), time.Now()); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	var requests, attempts, tokensIn, tokensOut int64
	var costMicros *int64
	row := db.Read.QueryRowContext(context.Background(),
		`SELECT requests, attempts, tokens_in, tokens_out, cost_micros
		   FROM usage_daily
		  WHERE provider_id = 'groq' AND model = 'm'`)
	if err := row.Scan(&requests, &attempts, &tokensIn, &tokensOut, &costMicros); err != nil {
		t.Fatalf("usage_daily row: %v", err)
	}

	if tokensIn != 1000 {
		t.Errorf("tokens_in = %d, want 1000", tokensIn)
	}
	if tokensOut != 500 {
		t.Errorf("tokens_out = %d, want 500", tokensOut)
	}
	if costMicros == nil {
		t.Error("cost_micros is NULL, want non-NULL: the catalog prices this model")
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}
