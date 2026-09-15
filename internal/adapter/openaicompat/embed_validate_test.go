package openaicompat

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseEmbeddingRejectsEmptyVectors(t *testing.T) {
	for _, body := range []string{
		`{"data":[{"index":0,"embedding":[]}]}`,
		`{"data":[{"index":0,"embedding":""}]}`,
	} {
		_, err := New().ParseEmbedding(&http.Response{
			StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)),
		})
		if err == nil {
			t.Errorf("%s: an empty vector is not an embedding", body)
		}
	}
}

func TestParseEmbeddingWithoutIndicesServesPositionOrder(t *testing.T) {
	for _, body := range []string{
		`{"data":[{"embedding":[1]},{"embedding":[2]},{"embedding":[3]}]}`,
		`{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[2]},{"index":0,"embedding":[3]}]}`,
	} {
		out, err := New().ParseEmbedding(&http.Response{
			StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)),
		})
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		for i, e := range out.Embeddings {
			if e.Index != i || e.Float[0] != float32(i+1) {
				t.Errorf("%s: embedding %d = index %d value %v", body, i, e.Index, e.Float)
			}
		}
	}
}

func TestParseEmbeddingRejectsGenuineDuplicateIndices(t *testing.T) {
	for _, body := range []string{
		`{"data":[{"index":1,"embedding":[1]},{"index":1,"embedding":[2]}]}`,
		`{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[2]},{"index":2,"embedding":[3]}]}`,
		`{"data":[{"index":0,"embedding":[1]},{"index":2,"embedding":[2]}]}`,
	} {
		_, err := New().ParseEmbedding(&http.Response{
			StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)),
		})
		if err == nil {
			t.Errorf("%s: duplicated or missing indices were accepted", body)
		}
	}
}
