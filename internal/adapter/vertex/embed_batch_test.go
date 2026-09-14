package vertex

import (
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// Vertex caps a :predict call at 250 input texts and 20,000 input tokens, and
// gemini-embedding-001 takes a single text per call. A request past either is
// a 400, so the executor is told how to split it.
func TestEmbeddingBatchesFollowVertexLimits(t *testing.T) {
	var _ adapter.EmbeddingBatcher = New()
	inputs := func(n, size int) *ir.EmbeddingRequest {
		req := &ir.EmbeddingRequest{}
		for range n {
			req.Input = append(req.Input, strings.Repeat("x", size))
		}
		return req
	}
	tgt := func(model string) *adapter.Target {
		return &adapter.Target{Project: "p", Location: "us-central1", Publisher: PublisherGoogle, Model: model}
	}

	if got := New().EmbeddingBatches(tgt("gemini-embedding-001"), inputs(3, 5)); !reflect.DeepEqual(got, []int{1, 1, 1}) {
		t.Errorf("gemini-embedding-001 batches = %v, want one input each", got)
	}
	if got := New().EmbeddingBatches(tgt("text-embedding-005"), inputs(600, 5)); !reflect.DeepEqual(got, []int{250, 250, 100}) {
		t.Errorf("text-embedding-005 batches = %v, want 250, 250, 100", got)
	}
	got := New().EmbeddingBatches(tgt("text-embedding-005"), inputs(5, 6000))
	sum := 0
	for _, n := range got {
		sum += n
	}
	if len(got) < 2 || sum != 5 {
		t.Errorf("batches for 30,000 bytes of text = %v, want a split under the token limit covering all 5", got)
	}
}
