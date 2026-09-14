package vertex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestEmbeddingsDisableSilentTruncation(t *testing.T) {
	// Vertex defaults autoTruncate to true and embeds only the prefix of an
	// over-long input; the OpenAI contract the client speaks rejects it.
	hr, _, err := New().BuildEmbedding(context.Background(), googleTarget(),
		&ir.EmbeddingRequest{Input: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(hr.Body)
	var body struct {
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if v, ok := body.Parameters["autoTruncate"]; !ok || v != false {
		t.Fatalf("parameters = %v, want autoTruncate false", body.Parameters)
	}
}

func TestParseEmbeddingRejectsATruncatedInput(t *testing.T) {
	body := `{"predictions":[{"embeddings":{"values":[0.1],"statistics":{"token_count":2048,"truncated":true}}}]}`
	_, err := New().ParseEmbedding(&http.Response{
		StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)),
	})
	if err == nil {
		t.Fatal("a vector for a truncated input must not be served as the input's")
	}
}
