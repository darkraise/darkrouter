package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

const maxEmbeddingBytes = 32 << 20

// BuildEmbedding renders the Google publisher's :predict request. Vertex
// serves text embeddings through the generic prediction route rather than
// the Gemini API's batchEmbedContents. Each call carries one sub-batch the
// executor split by EmbeddingBatches.
func (a *Adapter) BuildEmbedding(ctx context.Context, t *adapter.Target,
	req *ir.EmbeddingRequest) (*http.Request, []ir.Warning, error) {

	if t.Project == "" || t.Location == "" {
		return nil, nil, fmt.Errorf("vertex target needs a project and a location")
	}
	if publisherOf(t) != PublisherGoogle {
		return nil, nil, fmt.Errorf("vertex publisher %q serves no embedding model", t.Publisher)
	}
	if len(req.Tokens) > 0 {
		// Vertex takes text. Sending token ids as their decimal spelling
		// would embed the digits, succeed, and give the client no way to
		// notice.
		return nil, nil, errors.New(
			"this provider takes text, and the request carried pre-tokenized input")
	}
	if len(req.Input) == 0 {
		return nil, nil, errors.New("input is required")
	}

	var warns []ir.Warning
	if req.Encoding == "base64" {
		warns = append(warns, ir.Warning{
			Field: "encoding_format", Target: t.Model,
			Reason: "this provider returns float vectors only; base64 was requested",
		})
	}

	instances := make([]map[string]any, 0, len(req.Input))
	for _, text := range req.Input {
		instances = append(instances, map[string]any{"content": text})
	}
	// autoTruncate defaults to true, which embeds only the prefix of an input
	// past the model's token limit. The OpenAI contract rejects such an input,
	// and false makes Vertex do the same.
	params := map[string]any{"autoTruncate": false}
	if req.Dimensions > 0 {
		params["outputDimensionality"] = req.Dimensions
	}
	body := map[string]any{"instances": instances, "parameters": params}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}

	endpoint := baseFor(t) + "/" + PublisherGoogle + "/models/" +
		adapter.EscapePathSegment(t.Model) + ":predict"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build embedding request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	// The bearer token comes from internal/auth; no credential is written here.
	return hr, warns, nil
}

func (a *Adapter) ParseEmbedding(resp *http.Response) (*ir.EmbeddingResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbeddingBytes))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	var env struct {
		Predictions []struct {
			Embeddings struct {
				Values     []float32 `json:"values"`
				Statistics struct {
					TokenCount int  `json:"token_count"`
					Truncated  bool `json:"truncated"`
				} `json:"statistics"`
			} `json:"embeddings"`
		} `json:"predictions"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}
	if len(env.Predictions) == 0 {
		return nil, errors.New("embedding response carried no vectors")
	}
	out := &ir.EmbeddingResponse{Embeddings: make([]ir.Embedding, 0, len(env.Predictions))}
	for i, p := range env.Predictions {
		if p.Embeddings.Statistics.Truncated {
			return nil, fmt.Errorf("embedding %d was computed from a truncated input", i)
		}
		// The index is ours to assign: predictions carry order only.
		out.Embeddings = append(out.Embeddings, ir.Embedding{Index: i, Float: p.Embeddings.Values})
		out.Usage.InputTokens += p.Embeddings.Statistics.TokenCount
	}
	if err := adapter.ValidateEmbeddings(out.Embeddings); err != nil {
		return nil, err
	}
	return out, nil
}

// Vertex's per-request embedding limits, from its text-embedding docs: 250
// input texts and 20,000 input tokens, past which :predict answers 400.
const (
	maxEmbeddingInputs = 250
	maxEmbeddingTokens = 20000
)

// EmbeddingBatches splits a request to fit one :predict call each.
//
// Tokens are bounded by bytes because Darkrouter has no tokenizer for Google's
// embedding models. A subword tokenizer with byte fallback emits at most one
// token per UTF-8 byte plus a leading marker, so the bound over-splits rather
// than under-splits: over-splitting costs a request, under-splitting the batch.
func (a *Adapter) EmbeddingBatches(t *adapter.Target, req *ir.EmbeddingRequest) []int {
	limit := maxEmbeddingInputs
	if strings.Contains(t.Model, "gemini-embedding-001") {
		// This model takes a single input text per request.
		limit = 1
	}
	var out []int
	n, tokens := 0, 0
	for _, text := range req.Input {
		cost := len(text) + 1
		if n > 0 && (n == limit || tokens+cost > maxEmbeddingTokens) {
			out = append(out, n)
			n, tokens = 0, 0
		}
		n++
		tokens += cost
	}
	if n > 0 {
		out = append(out, n)
	}
	return out
}

var (
	_ adapter.Embedder         = (*Adapter)(nil)
	_ adapter.EmbeddingBatcher = (*Adapter)(nil)
)
