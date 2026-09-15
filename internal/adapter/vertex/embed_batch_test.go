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
	got := New().EmbeddingBatches(tgt("text-embedding-005"), inputs(15, 6000))
	sum := 0
	for _, n := range got {
		sum += n
	}
	if len(got) < 2 || sum != 15 {
		t.Errorf("batches for 90,000 bytes of text = %v, want a split under the token limit covering all 15", got)
	}
}

func sumOf(ns []int) int {
	s := 0
	for _, n := range ns {
		s += n
	}
	return s
}

// Google documents about four characters per token. Ordinary prose is packed
// at a 2x margin on that ratio rather than one token per byte, which split
// typical English at a quarter of the real limit.
func TestEmbeddingBatchesPackOrdinaryProseCloserToTheLimit(t *testing.T) {
	sentence := "The quick brown fox jumps over the lazy dog, twice. "
	text := strings.Repeat(sentence, 400/len(sentence)+1)[:400]
	req := &ir.EmbeddingRequest{}
	for range 250 {
		req.Input = append(req.Input, text)
	}
	tgt := &adapter.Target{Project: "p", Location: "l", Publisher: PublisherGoogle, Model: "text-embedding-005"}
	got := New().EmbeddingBatches(tgt, req)
	if len(got) > 3 || sumOf(got) != 250 {
		t.Errorf("batches for 100,000 bytes of English = %v, want at most 3 covering all 250", got)
	}
}

// Text a tokenizer may break into a token per byte keeps the byte bound: digits,
// punctuation, long letter runs such as encoded blobs, and non-ASCII.
func TestEmbeddingBatchesKeepTheByteBoundForDenseText(t *testing.T) {
	tgt := &adapter.Target{Project: "p", Location: "l", Publisher: PublisherGoogle, Model: "text-embedding-005"}
	for name, text := range map[string]string{
		"digits":      strings.Repeat("7", 1000),
		"punctuation": strings.Repeat("{}", 500),
		"letter blob": strings.Repeat("qZx", 334)[:1000],
		"no vowels":   strings.Repeat("qz xk ", 167)[:1000],
		"spaces":      strings.Repeat(" ", 1000),
		"non-ASCII":   strings.Repeat("é", 500),
	} {
		t.Run(name, func(t *testing.T) {
			req := &ir.EmbeddingRequest{}
			for range 60 {
				req.Input = append(req.Input, text)
			}
			got := New().EmbeddingBatches(tgt, req)
			if sumOf(got) != 60 {
				t.Fatalf("batches = %v, want all 60 inputs covered", got)
			}
			for _, n := range got {
				if n*len(text) > maxEmbeddingTokens {
					t.Errorf("batches = %v; a batch of %d holds %d bytes of text that may be a token per byte",
						got, n, n*len(text))
				}
			}
		})
	}
}

// A text past a model's per-input limit is rejected on its own, since
// autoTruncate is off, so no valid input costs more than that limit. An
// unrecognized model's limit is unknown and gets no such cap.
func TestEmbeddingBatchesCapATextAtTheModelInputLimit(t *testing.T) {
	req := &ir.EmbeddingRequest{}
	for range 9 {
		req.Input = append(req.Input, strings.Repeat("7", 5000))
	}
	known := &adapter.Target{Project: "p", Location: "l", Publisher: PublisherGoogle, Model: "text-multilingual-embedding-002"}
	if got := New().EmbeddingBatches(known, req); !reflect.DeepEqual(got, []int{9}) {
		t.Errorf("batches = %v, want one: nine texts of at most 2,048 tokens fit 20,000", got)
	}
	unknown := &adapter.Target{Project: "p", Location: "l", Publisher: PublisherGoogle, Model: "some-future-embedding"}
	if got := New().EmbeddingBatches(unknown, req); len(got) < 3 {
		t.Errorf("batches = %v, want the byte bound for a model whose input limit is unknown", got)
	}
}
