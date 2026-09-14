package vertex

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseEmbeddingRejectsAnEmptyVector(t *testing.T) {
	body := `{"predictions":[{"embeddings":{"values":[0.1]}},{"embeddings":{}}]}`
	_, err := New().ParseEmbedding(&http.Response{
		StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)),
	})
	if err == nil {
		t.Fatal("a prediction with no values is not a vector")
	}
}
