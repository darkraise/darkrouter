package adapter

import (
	"encoding/base64"
	"fmt"

	"github.com/darkraise/darkrouter/internal/ir"
)

// EmbeddingBatcher is implemented by an Embedder whose upstream caps what one
// request may carry. EmbeddingBatches returns the input count of each
// consecutive sub-batch, in order and summing to req.InputCount(); the executor
// sends each as its own request and joins the vectors. Nil means one request.
type EmbeddingBatcher interface {
	EmbeddingBatches(t *Target, req *ir.EmbeddingRequest) []int
}

// ValidateEmbeddings rejects a parsed batch that decoded cleanly but cannot be
// a real answer: an empty or undecodable vector, vectors of differing
// dimensions, or indices that are not each position of the batch exactly once.
// Any of these served as a success would silently corrupt a client's index.
func ValidateEmbeddings(es []ir.Embedding) error {
	seen := make([]bool, len(es))
	dims := -1
	var buf []byte
	for pos, e := range es {
		if e.Index < 0 || e.Index >= len(es) || seen[e.Index] {
			return fmt.Errorf("embedding %d carries index %d, which is duplicated or outside the batch", pos, e.Index)
		}
		seen[e.Index] = true

		var n int
		switch {
		case e.IsFloat():
			n = len(e.Float)
		case e.IsBase64():
			buf = buf[:0]
			if need := base64.StdEncoding.DecodedLen(len(e.Base64)); cap(buf) < need {
				buf = make([]byte, need)
			}
			m, err := base64.StdEncoding.Decode(buf[:cap(buf)], []byte(e.Base64))
			if err != nil {
				return fmt.Errorf("embedding %d is not valid base64: %w", e.Index, err)
			}
			if m%4 != 0 {
				return fmt.Errorf("embedding %d decodes to %d bytes, not a whole number of float32s", e.Index, m)
			}
			n = m / 4
		}
		if n == 0 {
			return fmt.Errorf("embedding %d is empty", e.Index)
		}
		if dims >= 0 && n != dims {
			return fmt.Errorf("embedding %d has %d dimensions and an earlier one has %d", e.Index, n, dims)
		}
		dims = n
	}
	return nil
}
