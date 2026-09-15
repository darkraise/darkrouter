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

	if err := CheckEndpoint(t.Project, t.Location); err != nil {
		return nil, nil, err
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

// maxInputTokens is the per-text limit of the models Vertex documents at 2,048
// tokens. With autoTruncate off a longer text is rejected on its own, so no
// text in a batch that can succeed costs more.
const maxInputTokens = 2048

var cappedInputModels = []string{
	"text-embedding-004", "text-embedding-005", "text-multilingual-embedding-002",
	"textembedding-gecko",
}

// EmbeddingBatches splits a request to fit one :predict call each.
//
// Darkrouter has no tokenizer for Google's embedding models, so tokens are
// estimated high: over-splitting costs a request, under-splitting the batch.
func (a *Adapter) EmbeddingBatches(t *adapter.Target, req *ir.EmbeddingRequest) []int {
	limit := maxEmbeddingInputs
	if strings.Contains(t.Model, "gemini-embedding-001") {
		// This model takes a single input text per request.
		limit = 1
	}
	perText := 0
	for _, m := range cappedInputModels {
		if strings.Contains(t.Model, m) {
			perText = maxInputTokens
		}
	}
	var out []int
	n, tokens := 0, 0
	for _, text := range req.Input {
		cost := estimateTokens(text)
		if perText > 0 && cost > perText {
			cost = perText
		}
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

// maxWordLetters is the longest letter run still read as a word. A longer run
// is more likely an encoded blob or identifier than prose.
const maxWordLetters = 20

// estimateTokens over-counts a text's tokens.
//
// Google documents about four characters per token. A word — a run of ASCII
// letters no longer than maxWordLetters holding a vowel — and the single space
// before it are counted at two characters per token, a 2x margin on that ratio.
// Everything a subword tokenizer may split a token per byte — digits,
// punctuation, runs of whitespace, vowel-less or long letter runs, and
// non-ASCII text through byte fallback — is counted at one token per UTF-8
// byte. One more covers a leading marker.
func estimateTokens(text string) int {
	var (
		prose, dense int
		run, space   int
		vowel        bool
	)
	endRun := func() {
		if run > 0 && run <= maxWordLetters && vowel {
			prose += run + space
		} else {
			dense += run + space
		}
		run, space, vowel = 0, 0, false
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z':
			run++
			vowel = vowel || strings.IndexByte("aeiouyAEIOUY", c) >= 0
		case c == ' ' && (i == 0 || text[i-1] != ' ') && (i+1 == len(text) || text[i+1] != ' '):
			endRun()
			space = 1
		default:
			endRun()
			dense++
		}
	}
	endRun()
	return (prose+1)/2 + dense + 1
}

var (
	_ adapter.Embedder         = (*Adapter)(nil)
	_ adapter.EmbeddingBatcher = (*Adapter)(nil)
)
