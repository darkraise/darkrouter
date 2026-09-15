package gemini

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseEmbeddingRejectsAnEmptyVector(t *testing.T) {
	_, err := New().ParseEmbedding(&http.Response{
		StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"embeddings":[{}]}`)),
	})
	if err == nil {
		t.Fatal("an embedding with no values is not a vector")
	}
}
