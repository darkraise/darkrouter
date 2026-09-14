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
