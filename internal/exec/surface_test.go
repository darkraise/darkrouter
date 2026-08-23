package exec

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

// jsonOK is an upstream that answers every request with an empty JSON object.
func jsonOK() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}
}

// catalogWith builds a one-model live snapshot declaring the given surfaces.
func catalogWith(providerID, modelID string, surfaces ...ir.Surface) *catalog.Store {
	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: providerID, ModelID: modelID, State: catalog.StateLive,
		Surfaces: surfaces,
	}}, []string{providerID}))
	return cat
}

func TestRunAuxLogsAMalformedBody(t *testing.T) {
	// Handle opens its record before parsing, so a 400 is a logged request.
	// Six auxiliary routes parsing first would drop theirs from the only place
	// an operator can see them.
	e, rec := executorForOp(t, "http://127.0.0.1:1", nil)
	w := httptest.NewRecorder()

	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceEmbedding}}
	e.RunAux(w, httptest.NewRequest("POST", "/v1/embeddings", nil),
		"openai", ir.SurfaceEmbedding, op,
		func(*config.Config) (SurfaceOp, error) { return nil, errors.New("input is required") })

	got := rec.only(t)
	if got.Surface != string(ir.SurfaceEmbedding) {
		t.Errorf("surface = %q", got.Surface)
	}
	if got.Dialect != "openai" {
		t.Errorf("dialect = %q", got.Dialect)
	}
	if got.ErrorCode != string(ir.ErrInvalidRequest) {
		t.Errorf("error code = %q, want %q", got.ErrorCode, ir.ErrInvalidRequest)
	}
	if got.Status != "error" {
		t.Errorf("status = %q", got.Status)
	}
	if got.ID == "" || w.Header().Get("X-Darkrouter-Request") != got.ID {
		t.Errorf("request id header = %q, record id = %q",
			w.Header().Get("X-Darkrouter-Request"), got.ID)
	}
	if len(got.Attempts) != 0 {
		t.Errorf("attempts = %d; a body that never parsed reached an upstream", len(got.Attempts))
	}
}

func TestRunAuxRunsTheOpWhenTheBodyParses(t *testing.T) {
	upstream := httptest.NewServer(jsonOK())
	defer upstream.Close()

	cat := catalogWith("p", "m", ir.SurfaceEmbedding)
	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceEmbedding}}
	e, rec := executorForOp(t, upstream.URL, cat)

	e.RunAux(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/embeddings", nil),
		"openai", ir.SurfaceEmbedding, op,
		func(*config.Config) (SurfaceOp, error) { return op, nil })

	if op.builds != 1 || op.responds != 1 {
		t.Fatalf("builds = %d, responds = %d", op.builds, op.responds)
	}
	if got := rec.only(t); got.Status != "success" {
		t.Errorf("status = %q", got.Status)
	}
}
