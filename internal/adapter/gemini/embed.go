package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

const maxGeminiEmbeddingBytes = 32 << 20

type geminiEmbedRequest struct {
	Requests []geminiEmbedItem `json:"requests"`
}

type geminiEmbedItem struct {
	// Model is repeated per item, prefixed with "models/". Gemini rejects the
	// batch without it even though the name is already in the URL.
	Model   string `json:"model"`
	Content struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"content"`
	OutputDimensionality *int `json:"outputDimensionality,omitempty"`
}

// BuildEmbedding renders batchEmbedContents rather than embedContent. The IR
// request is batched, and the single-item endpoint would turn a 96-input batch
// into 96 round trips through the attempt loop, each separately failable.
func (a *Adapter) BuildEmbedding(ctx context.Context, t *adapter.Target,
	req *ir.EmbeddingRequest) (*http.Request, []ir.Warning, error) {

	if len(req.Tokens) > 0 {
		// Gemini takes text. Sending token ids as their decimal spelling would
		// embed the digits, succeed, and give the client no way to notice.
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

	name := "models/" + t.Model
	body := geminiEmbedRequest{Requests: make([]geminiEmbedItem, 0, len(req.Input))}
	for _, text := range req.Input {
		item := geminiEmbedItem{Model: name}
		item.Content.Parts = append(item.Content.Parts, struct {
			Text string `json:"text"`
		}{Text: text})
		if req.Dimensions > 0 {
			d := req.Dimensions
			item.OutputDimensionality = &d
		}
		body.Requests = append(body.Requests, item)
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	endpoint := strings.TrimRight(t.BaseURL, "/") + "/models/" +
		url.PathEscape(t.Model) + ":batchEmbedContents"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build embedding request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("x-goog-api-key", t.APIKey)
	}
	return hr, warns, nil
}

func (a *Adapter) ParseEmbedding(resp *http.Response) (*ir.EmbeddingResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxGeminiEmbeddingBytes))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	var env struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}
	if len(env.Embeddings) == 0 {
		return nil, errors.New("embedding response carried no vectors")
	}
	out := &ir.EmbeddingResponse{
		Embeddings: make([]ir.Embedding, 0, len(env.Embeddings)),
		// Gemini reports no usage for embeddings. Zero here means unreported,
		// and the request row records it as such rather than as free.
	}
	for i, e := range env.Embeddings {
		// The index is ours to assign: the batch response carries order only.
		out.Embeddings = append(out.Embeddings, ir.Embedding{Index: i, Float: e.Values})
	}
	return out, nil
}

var _ adapter.Embedder = (*Adapter)(nil)
