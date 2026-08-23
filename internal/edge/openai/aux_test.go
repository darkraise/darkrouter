package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func embedReq(t *testing.T, body string) *ir.EmbeddingRequest {
	t.Helper()
	req, err := ParseEmbedding(httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(body)), 1<<20)
	if err != nil {
		t.Fatalf("ParseEmbedding(%s): %v", body, err)
	}
	return req
}

func TestParseEmbeddingNormalizesABareString(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":"hello"}`)
	if len(req.Input) != 1 || req.Input[0] != "hello" {
		t.Errorf("Input = %v, want one element", req.Input)
	}
	if len(req.Tokens) != 0 {
		t.Errorf("Tokens = %v; a string input is not tokens", req.Tokens)
	}
}

func TestParseEmbeddingNormalizesAStringArray(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":["a","b","c"]}`)
	if len(req.Input) != 3 || req.Input[2] != "c" {
		t.Errorf("Input = %v", req.Input)
	}
}

func TestParseEmbeddingCarriesAFlatTokenArray(t *testing.T) {
	// A flat array of integers is ONE token array, not many. Reading it as
	// many would send each token id as a separate embedding input.
	req := embedReq(t, `{"model":"m","input":[15496,11,995]}`)
	if len(req.Input) != 0 {
		t.Errorf("Input = %v; token ids must not become text", req.Input)
	}
	if len(req.Tokens) != 1 || len(req.Tokens[0]) != 3 || req.Tokens[0][0] != 15496 {
		t.Errorf("Tokens = %v, want one array of three", req.Tokens)
	}
}

func TestParseEmbeddingCarriesNestedTokenArrays(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":[[1,2],[3]]}`)
	if len(req.Tokens) != 2 || len(req.Tokens[1]) != 1 || req.Tokens[1][0] != 3 {
		t.Errorf("Tokens = %v", req.Tokens)
	}
}

func TestParseEmbeddingReadsTheOptionals(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":"x","encoding_format":"base64","dimensions":256,"user":"u"}`)
	if req.Model != "m" || req.Encoding != "base64" || req.Dimensions != 256 || req.User != "u" {
		t.Errorf("request = %+v", req)
	}
}

func TestParseEmbeddingRejectsAMissingOrEmptyInput(t *testing.T) {
	// An absent input is a client bug and must be a 400 rather than an
	// upstream call that fails less legibly.
	for _, body := range []string{
		`{"model":"m"}`,
		`{"model":"m","input":null}`,
		`{"model":"m","input":[]}`,
		`{"model":"m","input":7}`,
	} {
		if _, err := ParseEmbedding(httptest.NewRequest("POST", "/v1/embeddings",
			strings.NewReader(body)), 1<<20); err == nil {
			t.Errorf("ParseEmbedding(%s) accepted an unusable input", body)
		}
	}
}

func TestParseEmbeddingEnforcesTheBodyCap(t *testing.T) {
	big := `{"model":"m","input":"` + strings.Repeat("x", 200) + `"}`
	if _, err := ParseEmbedding(httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(big)), 64); err == nil {
		t.Error("an oversized body was accepted")
	}
}

func TestWriteEmbeddingEmitsFloatVectors(t *testing.T) {
	w := httptest.NewRecorder()
	err := WriteEmbedding(w, &ir.EmbeddingResponse{
		Model: "text-embedding-3-small",
		Embeddings: []ir.Embedding{
			{Index: 0, Float: []float32{0.5, -0.25}},
			{Index: 1, Float: []float32{1}},
		},
		Usage: ir.Usage{InputTokens: 9},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Object string `json:"object"`
		Model  string `json:"model"`
		Data   []struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" || body.Model != "text-embedding-3-small" {
		t.Errorf("envelope = %+v", body)
	}
	if len(body.Data) != 2 || body.Data[0].Object != "embedding" || body.Data[1].Index != 1 {
		t.Fatalf("data = %+v", body.Data)
	}
	if body.Data[0].Embedding[1] != -0.25 {
		t.Errorf("vector = %v", body.Data[0].Embedding)
	}
	if body.Usage.PromptTokens != 9 || body.Usage.TotalTokens != 9 {
		t.Errorf("usage = %+v; embeddings report input tokens only", body.Usage)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestWriteEmbeddingCarriesBase64Verbatim(t *testing.T) {
	// The client asked for base64 to avoid the decode. Re-encoding through
	// floats would change the bytes it receives.
	w := httptest.NewRecorder()
	if err := WriteEmbedding(w, &ir.EmbeddingResponse{
		Model:      "m",
		Embeddings: []ir.Embedding{{Index: 0, Base64: "AACAPwAAAEA="}},
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), `"embedding":"AACAPwAAAEA="`) {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestWriteEmbeddingNeverEmitsANullVector(t *testing.T) {
	// An OpenAI client indexes into this array. null there is a crash, not an
	// empty vector.
	w := httptest.NewRecorder()
	if err := WriteEmbedding(w, &ir.EmbeddingResponse{
		Model:      "m",
		Embeddings: []ir.Embedding{{Index: 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "null") {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestDialectServesTheEmbeddingSurface(t *testing.T) {
	var _ interface {
		ParseEmbedding(*http.Request, int64) (*ir.EmbeddingRequest, error)
		WriteEmbedding(http.ResponseWriter, *ir.EmbeddingResponse) error
	} = New()
}
