package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

// wireEmbeddingRequest holds input as raw JSON deliberately: the field is a
// union of four shapes and decoding it into any concrete Go type rejects three
// of them.
type wireEmbeddingRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	EncodingFormat string          `json:"encoding_format"`
	Dimensions     *int            `json:"dimensions"`
	User           string          `json:"user"`
}

func ParseEmbedding(r *http.Request, maxBody int64) (*ir.EmbeddingRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBody {
		return nil, &ir.Error{
			Type:    ir.ErrPayloadTooLarge,
			Message: fmt.Sprintf("request body exceeds %d bytes", maxBody),
		}
	}
	var w wireEmbeddingRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	texts, tokens, err := parseEmbeddingInput(w.Input)
	if err != nil {
		return nil, err
	}
	req := &ir.EmbeddingRequest{
		Model:    w.Model,
		Input:    texts,
		Tokens:   tokens,
		Encoding: w.EncodingFormat,
		User:     w.User,
	}
	if w.Dimensions != nil {
		req.Dimensions = *w.Dimensions
	}
	return req, nil
}

// parseEmbeddingInput normalizes OpenAI's four accepted input shapes: a bare
// string, an array of strings, a bare token array, and an array of token
// arrays.
//
// The shape is decided from the first byte because no Go type decodes all four.
// Guessing wrong is not a formatting nuisance — a token array read as text
// embeds the literal digits, the call succeeds, and the client has no way to
// detect that the vector is wrong.
func parseEmbeddingInput(raw json.RawMessage) ([]string, [][]int, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil, errors.New("input is required")
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, nil, fmt.Errorf("input: %w", err)
		}
		return []string{s}, nil, nil
	case '[':
		return parseEmbeddingArray(trimmed)
	default:
		return nil, nil, errors.New("input must be a string or an array")
	}
}

func parseEmbeddingArray(trimmed []byte) ([]string, [][]int, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, nil, fmt.Errorf("input: %w", err)
	}
	if len(items) == 0 {
		return nil, nil, errors.New("input is empty")
	}
	first := bytes.TrimSpace(items[0])
	if len(first) == 0 {
		return nil, nil, errors.New("input contains an empty element")
	}
	switch first[0] {
	case '"':
		out := make([]string, 0, len(items))
		for i, it := range items {
			var s string
			if err := json.Unmarshal(it, &s); err != nil {
				return nil, nil, fmt.Errorf("input[%d]: %w", i, err)
			}
			out = append(out, s)
		}
		return out, nil, nil
	case '[':
		out := make([][]int, 0, len(items))
		for i, it := range items {
			var toks []int
			if err := json.Unmarshal(it, &toks); err != nil {
				return nil, nil, fmt.Errorf("input[%d]: %w", i, err)
			}
			out = append(out, toks)
		}
		return nil, out, nil
	default:
		// A flat array of integers is one token array, not many: reading it as
		// many would ask for one embedding per token id.
		var toks []int
		if err := json.Unmarshal(trimmed, &toks); err != nil {
			return nil, nil, fmt.Errorf("input: %w", err)
		}
		return nil, [][]int{toks}, nil
	}
}

func WriteEmbedding(w http.ResponseWriter, resp *ir.EmbeddingResponse) error {
	data := make([]any, 0, len(resp.Embeddings))
	for _, e := range resp.Embeddings {
		row := map[string]any{"object": "embedding", "index": e.Index}
		if e.IsBase64() {
			row["embedding"] = e.Base64
		} else {
			// Never nil: an OpenAI client indexes into this array, and null
			// there is a crash rather than an empty vector.
			v := e.Float
			if v == nil {
				v = []float32{}
			}
			row["embedding"] = v
		}
		data = append(data, row)
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
		"model":  resp.Model,
		// Embeddings report input tokens only, so total equals prompt unless a
		// provider volunteered an output count. Adding them rather than
		// hardcoding equality keeps an honest total when one does.
		"usage": map[string]any{
			"prompt_tokens": resp.Usage.InputTokens,
			"total_tokens":  resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	})
}

func (d *Dialect) ParseEmbedding(r *http.Request, maxBody int64) (*ir.EmbeddingRequest, error) {
	return ParseEmbedding(r, maxBody)
}

func (d *Dialect) WriteEmbedding(w http.ResponseWriter, resp *ir.EmbeddingResponse) error {
	return WriteEmbedding(w, resp)
}

var _ edge.EmbeddingDialect = (*Dialect)(nil)
