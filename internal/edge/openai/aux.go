package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

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

// readCappedBody reads the inbound body under the configured cap, reading one
// byte past it so "exactly at the cap" is not rejected.
func readCappedBody(r *http.Request, maxBody int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBody {
		// Typed rather than a bare error: an oversized JSON body asks the
		// client for the same thing an oversized upload does, and only the
		// type can carry that distinction out to the response status.
		return nil, &ir.Error{
			Type:    ir.ErrPayloadTooLarge,
			Message: fmt.Sprintf("request body exceeds %d bytes", maxBody),
		}
	}
	return body, nil
}

func ParseEmbedding(r *http.Request, maxBody int64) (*ir.EmbeddingRequest, error) {
	body, err := readCappedBody(r, maxBody)
	if err != nil {
		return nil, err
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

type wireModerationRequest struct {
	Model string          `json:"model"`
	Input json.RawMessage `json:"input"`
}

func ParseModeration(r *http.Request, maxBody int64) (*ir.ModerationRequest, error) {
	body, err := readCappedBody(r, maxBody)
	if err != nil {
		return nil, err
	}
	var w wireModerationRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	texts, err := parseTextInput(w.Input)
	if err != nil {
		return nil, err
	}
	return &ir.ModerationRequest{Model: w.Model, Input: texts}, nil
}

// parseTextInput reads a bare string or an array of strings.
//
// omni-moderation-latest also accepts an array of content-part objects for
// image moderation. That is refused rather than half-supported: accepting it
// while dropping the image parts would moderate the text and report the whole
// input clean.
func parseTextInput(raw json.RawMessage) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, errors.New("input is required")
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("input: %w", err)
		}
		return []string{s}, nil
	case '[':
		var out []string
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return nil, fmt.Errorf("input must be text or an array of text: %w", err)
		}
		if len(out) == 0 {
			return nil, errors.New("input is empty")
		}
		return out, nil
	default:
		return nil, errors.New("input must be text or an array of text")
	}
}

func WriteModeration(w http.ResponseWriter, resp *ir.ModerationResponse) error {
	results := make([]any, 0, len(resp.Results))
	for _, r := range resp.Results {
		cats := r.Categories
		if cats == nil {
			cats = map[string]bool{}
		}
		scores := r.Scores
		if scores == nil {
			scores = map[string]float64{}
		}
		row := map[string]any{
			"flagged":         r.Flagged,
			"categories":      cats,
			"category_scores": scores,
		}
		// Omitted when the provider sent none: the older moderation models do
		// not report it and an empty object would claim they did.
		if len(r.AppliedInputTypes) > 0 {
			row["category_applied_input_types"] = r.AppliedInputTypes
		}
		results = append(results, row)
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{
		"id": resp.ID, "model": resp.Model, "results": results,
	})
}

func (d *Dialect) ParseModeration(r *http.Request, maxBody int64) (*ir.ModerationRequest, error) {
	return ParseModeration(r, maxBody)
}

func (d *Dialect) WriteModeration(w http.ResponseWriter, resp *ir.ModerationResponse) error {
	return WriteModeration(w, resp)
}

var _ edge.ModerationDialect = (*Dialect)(nil)

type wireRerankRequest struct {
	Model           string            `json:"model"`
	Query           string            `json:"query"`
	Documents       []json.RawMessage `json:"documents"`
	TopN            *int              `json:"top_n"`
	ReturnDocuments bool              `json:"return_documents"`
}

func ParseRerank(r *http.Request, maxBody int64) (*ir.RerankRequest, error) {
	body, err := readCappedBody(r, maxBody)
	if err != nil {
		return nil, err
	}
	var w wireRerankRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	if w.Query == "" {
		return nil, errors.New("query is required")
	}
	if len(w.Documents) == 0 {
		return nil, errors.New("documents is required and must not be empty")
	}
	req := &ir.RerankRequest{
		Model: w.Model, Query: w.Query, ReturnDocuments: w.ReturnDocuments,
		Documents: make([]string, 0, len(w.Documents)),
	}
	if w.TopN != nil {
		req.TopN = *w.TopN
	}
	for i, raw := range w.Documents {
		text, dropped, err := rerankDocument(raw)
		if err != nil {
			return nil, fmt.Errorf("documents[%d]: %w", i, err)
		}
		req.Documents = append(req.Documents, text)
		if len(dropped) > 0 {
			req.Warnings = append(req.Warnings, ir.Warning{
				Field:  fmt.Sprintf("documents[%d]", i),
				Target: "rerank",
				Reason: "reranked on text alone; dropped " + strings.Join(dropped, ", "),
			})
		}
	}
	return req, nil
}

// rerankDocument reads one document, which Cohere v2 allows as a string or an
// object. An object contributes its text field; every other field is reported
// so the trace can say the document was ranked on less than it carried.
func rerankDocument(raw json.RawMessage) (string, []string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", nil, errors.New("document is empty")
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", nil, err
		}
		return s, nil, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return "", nil, errors.New("document must be text or an object with a text field")
	}
	var text string
	if t, ok := obj["text"]; ok {
		if err := json.Unmarshal(t, &text); err != nil {
			return "", nil, fmt.Errorf("text: %w", err)
		}
	}
	if text == "" {
		return "", nil, errors.New("document object has no text field")
	}
	dropped := make([]string, 0, len(obj))
	for k := range obj {
		if k != "text" {
			dropped = append(dropped, k)
		}
	}
	// Sorted so the warning text is stable: map iteration order would make an
	// otherwise identical request produce a different trace line each time.
	sort.Strings(dropped)
	return text, dropped, nil
}

func WriteRerank(w http.ResponseWriter, resp *ir.RerankResponse) error {
	results := make([]any, 0, len(resp.Results))
	for _, r := range resp.Results {
		row := map[string]any{"index": r.Index, "relevance_score": r.RelevanceScore}
		// The key is omitted rather than null when the client did not ask for
		// documents: a Cohere client tests for its presence.
		if r.Document != "" {
			row["document"] = map[string]any{"text": r.Document}
		}
		results = append(results, row)
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{
		"id": resp.ID, "results": results,
	})
}

func (d *Dialect) ParseRerank(r *http.Request, maxBody int64) (*ir.RerankRequest, error) {
	return ParseRerank(r, maxBody)
}

func (d *Dialect) WriteRerank(w http.ResponseWriter, resp *ir.RerankResponse) error {
	return WriteRerank(w, resp)
}

var _ edge.RerankDialect = (*Dialect)(nil)
