package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildEmbeddingRendersTheBatchShape(t *testing.T) {
	hr, warns, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{
			BaseURL: "https://generativelanguage.googleapis.com/v1beta",
			APIKey:  "k", Model: "text-embedding-004",
		},
		&ir.EmbeddingRequest{Input: []string{"a", "b"}, Dimensions: 256})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	want := "https://generativelanguage.googleapis.com/v1beta/models/text-embedding-004:batchEmbedContents"
	if hr.URL.String() != want {
		t.Errorf("url = %s, want %s", hr.URL, want)
	}
	if hr.Header.Get("x-goog-api-key") != "k" {
		t.Errorf("auth header = %q; gemini does not use bearer", hr.Header.Get("x-goog-api-key"))
	}

	var body struct {
		Requests []struct {
			Model   string `json:"model"`
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			OutputDimensionality *int `json:"outputDimensionality"`
		} `json:"requests"`
	}
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Requests) != 2 {
		t.Fatalf("requests = %d, want one per input", len(body.Requests))
	}
	// Every element must repeat the model, prefixed. Gemini rejects the batch
	// without it even though the name is already in the URL.
	if body.Requests[0].Model != "models/text-embedding-004" {
		t.Errorf("requests[0].model = %q", body.Requests[0].Model)
	}
	if body.Requests[1].Content.Parts[0].Text != "b" {
		t.Errorf("requests[1] text = %q", body.Requests[1].Content.Parts[0].Text)
	}
	if body.Requests[0].OutputDimensionality == nil || *body.Requests[0].OutputDimensionality != 256 {
		t.Errorf("outputDimensionality = %v", body.Requests[0].OutputDimensionality)
	}
}

func TestBuildEmbeddingOmitsUnsetDimensions(t *testing.T) {
	hr, _, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1beta", Model: "m"},
		&ir.EmbeddingRequest{Input: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(hr.Body)
	if strings.Contains(string(raw), "outputDimensionality") {
		t.Errorf("body = %s; an unset dimension count was sent", raw)
	}
}

func TestBuildEmbeddingWarnsThatBase64IsUnavailable(t *testing.T) {
	// The client asked for base64 to skip a decode and will get floats. That
	// is a different response from the one it asked for.
	_, warns, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1beta", Model: "m"},
		&ir.EmbeddingRequest{Input: []string{"a"}, Encoding: "base64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Reason, "float") {
		t.Errorf("warnings = %v", warns)
	}
}

func TestBuildEmbeddingRefusesTokenInput(t *testing.T) {
	// Gemini takes text. Sending token ids as their decimal spelling would
	// embed the digits and the client could not tell.
	_, _, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1beta", Model: "m"},
		&ir.EmbeddingRequest{Tokens: [][]int{{1, 2}}})
	if err == nil {
		t.Fatal("a pre-tokenized input was accepted")
	}
}

func TestParseEmbeddingReadsTheBatchResponse(t *testing.T) {
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(
		`{"embeddings":[{"values":[0.5,-0.25]},{"values":[1]}]}`))}
	out, err := New().ParseEmbedding(resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Embeddings) != 2 {
		t.Fatalf("embeddings = %+v", out.Embeddings)
	}
	if out.Embeddings[0].Index != 0 || out.Embeddings[1].Index != 1 {
		t.Errorf("indices = %d, %d; gemini returns order only and the index is ours to assign",
			out.Embeddings[0].Index, out.Embeddings[1].Index)
	}
	if out.Embeddings[0].Float[1] != -0.25 {
		t.Errorf("vector = %v", out.Embeddings[0].Float)
	}
	if out.Usage.InputTokens != 0 {
		t.Errorf("usage = %+v; gemini reports none for embeddings", out.Usage)
	}
}

func TestParseEmbeddingRejectsAVectorlessBody(t *testing.T) {
	resp := &http.Response{StatusCode: 200,
		Body: io.NopCloser(strings.NewReader(`{"embeddings":[]}`))}
	if _, err := New().ParseEmbedding(resp); err == nil {
		t.Fatal("a 200 with no vectors parsed cleanly")
	}
}

func TestGeminiDeclaresTheEmbeddingSurface(t *testing.T) {
	// Spec §4's matrix. Without this the router filters gemini out of every
	// embedding request while its preset advertises the surface.
	got := adapter.SurfacesOf(New())
	if !got.Has(ir.SurfaceEmbedding) || !got.Has(ir.SurfaceLLM) {
		t.Errorf("surfaces = %v", got)
	}
	if got.Has(ir.SurfaceImage) {
		t.Error("gemini declares image; Imagen is out of scope for v1 per the §4 matrix")
	}
}
