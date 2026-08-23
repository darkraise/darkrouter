package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildRerankUsesTheAbsolutePresetPath(t *testing.T) {
	// cohere's base URL is an OpenAI-compatibility shim while its rerank
	// endpoint is native. Joining them produces a URL that does not exist.
	hr, _, err := New().BuildRerank(context.Background(),
		&adapter.Target{
			BaseURL:    "https://api.cohere.com/compatibility/v1",
			APIKey:     "sk",
			Model:      "rerank-v3.5",
			RerankPath: "/v2/rerank",
		},
		&ir.RerankRequest{Query: "q", Documents: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := hr.URL.String(); got != "https://api.cohere.com/v2/rerank" {
		t.Errorf("url = %s, want the path replaced rather than appended", got)
	}
	if hr.Header.Get("Authorization") != "Bearer sk" {
		t.Errorf("auth = %q", hr.Header.Get("Authorization"))
	}
}

func TestBuildRerankJoinsARelativePresetPath(t *testing.T) {
	hr, _, err := New().BuildRerank(context.Background(),
		&adapter.Target{BaseURL: "https://x/api/", Model: "m", RerankPath: "rerank"},
		&ir.RerankRequest{Query: "q", Documents: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := hr.URL.String(); got != "https://x/api/rerank" {
		t.Errorf("url = %s", got)
	}
}

func TestBuildRerankRefusesToGuessAPath(t *testing.T) {
	// The router only produces a rerank candidate for a preset declaring the
	// surface, and that preset must declare a path. Empty is a misconfiguration
	// and posting a rerank body at a guessed URL is the worse failure.
	_, _, err := New().BuildRerank(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "m"},
		&ir.RerankRequest{Query: "q", Documents: []string{"a"}})
	if err == nil {
		t.Fatal("a target with no rerank path built a request")
	}
	if !strings.Contains(err.Error(), "rerank-path") {
		t.Errorf("err = %v; it must name the quirk an operator has to set", err)
	}
}

func TestBuildRerankOmitsUnsetTopN(t *testing.T) {
	hr, _, err := New().BuildRerank(context.Background(),
		&adapter.Target{BaseURL: "https://x", Model: "m", RerankPath: "/v2/rerank"},
		&ir.RerankRequest{Query: "q", Documents: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	_ = json.Unmarshal(raw, &body)
	if _, present := body["top_n"]; present {
		t.Error("an unset top_n was sent; zero is not a legal value")
	}
	if body["model"] != "m" {
		t.Errorf("model = %v", body["model"])
	}
}

func TestParseRerankReadsTheV2Results(t *testing.T) {
	// Cohere v2 returns index and relevance_score and nothing else.
	resp := &http.Response{
		StatusCode: 200,
		Body: io.NopCloser(strings.NewReader(`{"id":"r-1","results":[
		  {"index":2,"relevance_score":0.98},
		  {"index":0,"relevance_score":0.11}]}`)),
	}
	out, err := New().ParseRerank(resp)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "r-1" || len(out.Results) != 2 {
		t.Fatalf("response = %+v", out)
	}
	if out.Results[0].Index != 2 || out.Results[0].RelevanceScore != 0.98 {
		t.Errorf("first result = %+v", out.Results[0])
	}
	if out.Results[0].Document != "" {
		t.Errorf("document = %q; the adapter must not invent one", out.Results[0].Document)
	}
}

func TestBuildRerankNeverSendsReturnDocuments(t *testing.T) {
	// It is a v1 parameter. Sending it to v2 asks for something the endpoint
	// does not define, and the client would get results with no documents.
	hr, _, err := New().BuildRerank(context.Background(),
		&adapter.Target{BaseURL: "https://x", Model: "m", RerankPath: "/v2/rerank"},
		&ir.RerankRequest{Query: "q", Documents: []string{"a"}, ReturnDocuments: true})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	_ = json.Unmarshal(raw, &body)
	if _, present := body["return_documents"]; present {
		t.Error("return_documents was forwarded to a v2 endpoint")
	}
	for _, k := range []string{"model", "query", "documents"} {
		if _, present := body[k]; !present {
			t.Errorf("body is missing %q: %v", k, body)
		}
	}
}

func TestParseRerankRejectsAResultlessBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`{"id":"r","results":[]}`)),
	}
	if _, err := New().ParseRerank(resp); err == nil {
		t.Fatal("a rerank 200 with no ranking parsed cleanly")
	}
}
