package openaicompat

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

const maxRerankBytes = 8 << 20

func (a *Adapter) BuildRerank(ctx context.Context, t *adapter.Target,
	req *ir.RerankRequest) (*http.Request, []ir.Warning, error) {

	if t.RerankPath == "" {
		return nil, nil, errors.New(
			"this provider declares no rerank-path quirk; rerank cannot be served without one")
	}
	// model, query, documents and top_n are the whole of Cohere v2's request.
	// return_documents is a v1 parameter v2 does not define, so it is honored
	// at the edge from the buffered request rather than forwarded here.
	body := map[string]any{
		"model": t.Model, "query": req.Query, "documents": req.Documents,
	}
	// Zero is not a legal top_n, so absence needs no separate presence flag.
	if req.TopN > 0 {
		body["top_n"] = req.TopN
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	endpoint, err := rerankURL(t.BaseURL, t.RerankPath)
	if err != nil {
		return nil, nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build rerank request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	// The inbound parse's losses ride out with the request so the loop records
	// them on the row that describes the attempt they applied to.
	return hr, req.Warnings, nil
}

// rerankURL resolves the preset-declared path against the provider's base URL.
//
// A path beginning with "/" replaces the base URL's path entirely. That is not
// a stylistic choice: cohere's base URL is
// https://api.cohere.com/compatibility/v1 — an OpenAI-compatibility shim — and
// its rerank endpoint is the native /v2/rerank. Appending would produce
// /compatibility/v1/v2/rerank, which does not exist.
func rerankURL(base, path string) (string, error) {
	if strings.HasPrefix(path, "/") {
		u, err := url.Parse(base)
		if err != nil {
			return "", fmt.Errorf("provider base URL: %w", err)
		}
		u.Path = path
		u.RawQuery = ""
		u.Fragment = ""
		return u.String(), nil
	}
	return strings.TrimRight(base, "/") + "/" + path, nil
}

// rerankEnvelope is Cohere v2's response: results carry an index and a score
// and nothing else. There is no document object to read, which is why the op
// fills documents from the request it sent.
type rerankEnvelope struct {
	ID      string `json:"id"`
	Results []struct {
		Index int     `json:"index"`
		Score float64 `json:"relevance_score"`
	} `json:"results"`
}

func (a *Adapter) ParseRerank(resp *http.Response) (*ir.RerankResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRerankBytes))
	if err != nil {
		return nil, fmt.Errorf("read rerank response: %w", err)
	}
	var env rerankEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse rerank response: %w", err)
	}
	if len(env.Results) == 0 {
		// A 200 with no ranking is a provider fault: the documents went up and
		// no ordering came back.
		return nil, errors.New("rerank response carried no results")
	}
	out := &ir.RerankResponse{ID: env.ID, Results: make([]ir.RerankResult, 0, len(env.Results))}
	for _, r := range env.Results {
		out.Results = append(out.Results, ir.RerankResult{Index: r.Index, RelevanceScore: r.Score})
	}
	return out, nil
}

var _ adapter.Reranker = (*Adapter)(nil)
