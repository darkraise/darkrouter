package openaicompat

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

func TestBuildEmbeddingRendersTheOpenAIShape(t *testing.T) {
	hr, warns, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://api.example.com/v1/", APIKey: "sk", Model: "text-embedding-3-small"},
		&ir.EmbeddingRequest{
			Input: []string{"a", "b"}, Encoding: "base64", Dimensions: 256, User: "u",
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v; nothing in this request is lossy", warns)
	}
	if hr.URL.String() != "https://api.example.com/v1/embeddings" {
		t.Errorf("url = %s", hr.URL)
	}
	if hr.Header.Get("Authorization") != "Bearer sk" {
		t.Errorf("auth = %q", hr.Header.Get("Authorization"))
	}

	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "text-embedding-3-small" {
		t.Errorf("model = %v; the target's name must be sent, not the client's", body["model"])
	}
	if body["encoding_format"] != "base64" {
		t.Errorf("encoding_format = %v", body["encoding_format"])
	}
	if body["dimensions"].(float64) != 256 {
		t.Errorf("dimensions = %v", body["dimensions"])
	}
	in, ok := body["input"].([]any)
	if !ok || len(in) != 2 {
		t.Errorf("input = %v", body["input"])
	}
}

func TestBuildEmbeddingForwardsTokenInput(t *testing.T) {
	// A client that sent token ids gets token ids sent upstream. There is no
	// detokenizer here, so the alternative is not "render as text" — it is
	// silently embedding something else.
	hr, _, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "m"},
		&ir.EmbeddingRequest{Tokens: [][]int{{1, 2, 3}, {4}}})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	outer, ok := body["input"].([]any)
	if !ok || len(outer) != 2 {
		t.Fatalf("input = %v, want two token arrays", body["input"])
	}
	inner, ok := outer[0].([]any)
	if !ok || len(inner) != 3 || inner[0].(float64) != 1 {
		t.Errorf("input[0] = %v, want [1,2,3]", outer[0])
	}
}

func TestBuildEmbeddingOmitsUnsetOptionals(t *testing.T) {
	// dimensions is not a legal zero and an explicit 0 is a 400 on OpenAI.
	hr, _, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "m"},
		&ir.EmbeddingRequest{Input: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	_ = json.Unmarshal(raw, &body)
	if _, present := body["dimensions"]; present {
		t.Error("an unset dimensions was sent")
	}
	if _, present := body["user"]; present {
		t.Error("an unset user was sent")
	}
	// The encoding is always sent: omitting it would be a different request
	// from the one the client made once the default is applied downstream.
	if body["encoding_format"] != "float" {
		t.Errorf("encoding_format = %v, want the applied default", body["encoding_format"])
	}
}

func jsonResp(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestParseEmbeddingReadsFloatVectors(t *testing.T) {
	out, err := New().ParseEmbedding(jsonResp(`{
	  "object":"list","model":"text-embedding-3-small",
	  "data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]},
	          {"object":"embedding","index":1,"embedding":[0.3]}],
	  "usage":{"prompt_tokens":7,"total_tokens":7}}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Model != "text-embedding-3-small" {
		t.Errorf("model = %q; the served name is what spec §8 needs", out.Model)
	}
	if len(out.Embeddings) != 2 {
		t.Fatalf("got %d vectors", len(out.Embeddings))
	}
	if !out.Embeddings[0].IsFloat() || len(out.Embeddings[0].Float) != 2 {
		t.Errorf("vector 0 = %+v", out.Embeddings[0])
	}
	if out.Embeddings[1].Index != 1 {
		t.Errorf("index = %d; the order is the client's batch order", out.Embeddings[1].Index)
	}
	// ir.Usage is Darkrouter's vocabulary, not OpenAI's: prompt_tokens maps to
	// InputTokens, which is what the rollup and the cost calculation read.
	if out.Usage.InputTokens != 7 {
		t.Errorf("usage = %+v", out.Usage)
	}
}

func TestParseEmbeddingReadsBase64Vectors(t *testing.T) {
	// A provider may answer in base64 whatever was asked, so the parser reads
	// the shape it received rather than the shape it requested.
	out, err := New().ParseEmbedding(jsonResp(`{
	  "object":"list","model":"m",
	  "data":[{"object":"embedding","index":0,"embedding":"AACAPwAAAEA="}],
	  "usage":{"prompt_tokens":2,"total_tokens":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Embeddings[0].IsBase64() {
		t.Fatalf("vector = %+v, want base64", out.Embeddings[0])
	}
	if out.Embeddings[0].Base64 != "AACAPwAAAEA=" {
		t.Errorf("base64 = %q; it must survive verbatim", out.Embeddings[0].Base64)
	}
}

func TestParseEmbeddingRejectsAnEmptyList(t *testing.T) {
	// A 200 carrying no vectors is a provider fault, not an empty answer: the
	// client asked for N and got none, and failing over is the right response.
	if _, err := New().ParseEmbedding(jsonResp(`{"object":"list","data":[]}`)); err == nil {
		t.Error("an empty data array parsed cleanly")
	}
}

func TestParseEmbeddingClosesTheBody(t *testing.T) {
	// The interface says ParseEmbedding takes ownership. A leaked body holds a
	// connection out of the pool for the process's lifetime.
	rc := &closeTracker{Reader: strings.NewReader(`{"data":[{"index":0,"embedding":[1]}]}`)}
	resp := &http.Response{StatusCode: 200, Body: rc, Header: http.Header{}}
	if _, err := New().ParseEmbedding(resp); err != nil {
		t.Fatal(err)
	}
	if !rc.closed {
		t.Error("ParseEmbedding did not close the body")
	}
}

type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error { c.closed = true; return nil }
