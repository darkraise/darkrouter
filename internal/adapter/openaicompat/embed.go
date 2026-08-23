package openaicompat

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

// maxEmbeddingBytes bounds the response. A batched float embedding is the
// largest payload any auxiliary surface carries — 2048 vectors of 3072 float64s
// is tens of megabytes — so the cap is generous, but an unbounded read from a
// misbehaving provider is the hazard it exists to stop.
const maxEmbeddingBytes = 128 << 20

func (a *Adapter) BuildEmbedding(ctx context.Context, t *adapter.Target,
	req *ir.EmbeddingRequest) (*http.Request, []ir.Warning, error) {

	body := map[string]any{
		"model": t.Model,
		// Always sent: applying the default downstream and omitting it here
		// would make the upstream request differ from the client's.
		"encoding_format": req.EncodingOrDefault(),
	}
	// A pre-tokenized input is forwarded as token ids. Rendering it as text is
	// not possible — there is no detokenizer here — and sending the text form
	// of something the client sent as tokens would be a different request.
	if len(req.Tokens) > 0 {
		body["input"] = req.Tokens
	} else {
		body["input"] = req.Input
	}
	// Neither is a legal zero, so absence needs no separate presence flag — and
	// an explicit dimensions of 0 is a 400 on OpenAI.
	if req.Dimensions > 0 {
		body["dimensions"] = req.Dimensions
	}
	if req.User != "" {
		body["user"] = req.User
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/embeddings"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build embedding request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil, nil
}

// embeddingEnvelope decodes the vector into any, because OpenAI returns an
// array of numbers under float encoding and a string under base64 — and a
// provider is free to answer in the other one whatever was asked.
type embeddingEnvelope struct {
	Model string `json:"model"`
	Data  []struct {
		Index     int `json:"index"`
		Embedding any `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func (a *Adapter) ParseEmbedding(resp *http.Response) (*ir.EmbeddingResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbeddingBytes))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	var env embeddingEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}
	if len(env.Data) == 0 {
		// A 200 carrying no vectors is a provider fault: the client asked for
		// N and got none, so failing over is the right answer rather than
		// handing back an empty list that looks like a valid response.
		return nil, errors.New("embedding response carried no vectors")
	}

	out := &ir.EmbeddingResponse{
		Model:      env.Model,
		Embeddings: make([]ir.Embedding, 0, len(env.Data)),
		// Embeddings report input tokens only, per spec §9. OutputTokens stays
		// zero rather than borrowing total_tokens, which would double-count
		// the input in the daily rollup.
		Usage: ir.Usage{InputTokens: env.Usage.PromptTokens},
	}
	for _, d := range env.Data {
		e := ir.Embedding{Index: d.Index}
		switch v := d.Embedding.(type) {
		case string:
			// Carried verbatim: the client asked for base64 to avoid the
			// decode, and round-tripping through floats would undo that.
			e.Base64 = v
		case []any:
			e.Float = make([]float32, 0, len(v))
			for _, n := range v {
				f, ok := n.(float64)
				if !ok {
					return nil, fmt.Errorf("embedding %d contains a non-numeric component", d.Index)
				}
				e.Float = append(e.Float, float32(f))
			}
		default:
			return nil, fmt.Errorf("embedding %d has an unrecognized encoding", d.Index)
		}
		out.Embeddings = append(out.Embeddings, e)
	}
	return out, nil
}

var _ adapter.Embedder = (*Adapter)(nil)
