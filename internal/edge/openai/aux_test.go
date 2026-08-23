package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func embedReq(t *testing.T, body string) *ir.EmbeddingRequest {
	t.Helper()
	req, err := ParseEmbedding(httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(body)), 1<<20)
	if err != nil {
		t.Fatalf("ParseEmbedding(%s): %v", body, err)
	}
	return req
}

func TestParseEmbeddingNormalizesABareString(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":"hello"}`)
	if len(req.Input) != 1 || req.Input[0] != "hello" {
		t.Errorf("Input = %v, want one element", req.Input)
	}
	if len(req.Tokens) != 0 {
		t.Errorf("Tokens = %v; a string input is not tokens", req.Tokens)
	}
}

func TestParseEmbeddingNormalizesAStringArray(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":["a","b","c"]}`)
	if len(req.Input) != 3 || req.Input[2] != "c" {
		t.Errorf("Input = %v", req.Input)
	}
}

func TestParseEmbeddingCarriesAFlatTokenArray(t *testing.T) {
	// A flat array of integers is ONE token array, not many. Reading it as
	// many would send each token id as a separate embedding input.
	req := embedReq(t, `{"model":"m","input":[15496,11,995]}`)
	if len(req.Input) != 0 {
		t.Errorf("Input = %v; token ids must not become text", req.Input)
	}
	if len(req.Tokens) != 1 || len(req.Tokens[0]) != 3 || req.Tokens[0][0] != 15496 {
		t.Errorf("Tokens = %v, want one array of three", req.Tokens)
	}
}

func TestParseEmbeddingCarriesNestedTokenArrays(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":[[1,2],[3]]}`)
	if len(req.Tokens) != 2 || len(req.Tokens[1]) != 1 || req.Tokens[1][0] != 3 {
		t.Errorf("Tokens = %v", req.Tokens)
	}
}

func TestParseEmbeddingReadsTheOptionals(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":"x","encoding_format":"base64","dimensions":256,"user":"u"}`)
	if req.Model != "m" || req.Encoding != "base64" || req.Dimensions != 256 || req.User != "u" {
		t.Errorf("request = %+v", req)
	}
}

func TestParseEmbeddingRejectsAMissingOrEmptyInput(t *testing.T) {
	// An absent input is a client bug and must be a 400 rather than an
	// upstream call that fails less legibly.
	for _, body := range []string{
		`{"model":"m"}`,
		`{"model":"m","input":null}`,
		`{"model":"m","input":[]}`,
		`{"model":"m","input":7}`,
	} {
		if _, err := ParseEmbedding(httptest.NewRequest("POST", "/v1/embeddings",
			strings.NewReader(body)), 1<<20); err == nil {
			t.Errorf("ParseEmbedding(%s) accepted an unusable input", body)
		}
	}
}

func TestParseEmbeddingEnforcesTheBodyCap(t *testing.T) {
	big := `{"model":"m","input":"` + strings.Repeat("x", 200) + `"}`
	if _, err := ParseEmbedding(httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(big)), 64); err == nil {
		t.Error("an oversized body was accepted")
	}
}

func TestWriteEmbeddingEmitsFloatVectors(t *testing.T) {
	w := httptest.NewRecorder()
	err := WriteEmbedding(w, &ir.EmbeddingResponse{
		Model: "text-embedding-3-small",
		Embeddings: []ir.Embedding{
			{Index: 0, Float: []float32{0.5, -0.25}},
			{Index: 1, Float: []float32{1}},
		},
		Usage: ir.Usage{InputTokens: 9},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Object string `json:"object"`
		Model  string `json:"model"`
		Data   []struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" || body.Model != "text-embedding-3-small" {
		t.Errorf("envelope = %+v", body)
	}
	if len(body.Data) != 2 || body.Data[0].Object != "embedding" || body.Data[1].Index != 1 {
		t.Fatalf("data = %+v", body.Data)
	}
	if body.Data[0].Embedding[1] != -0.25 {
		t.Errorf("vector = %v", body.Data[0].Embedding)
	}
	if body.Usage.PromptTokens != 9 || body.Usage.TotalTokens != 9 {
		t.Errorf("usage = %+v; embeddings report input tokens only", body.Usage)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestWriteEmbeddingCarriesBase64Verbatim(t *testing.T) {
	// The client asked for base64 to avoid the decode. Re-encoding through
	// floats would change the bytes it receives.
	w := httptest.NewRecorder()
	if err := WriteEmbedding(w, &ir.EmbeddingResponse{
		Model:      "m",
		Embeddings: []ir.Embedding{{Index: 0, Base64: "AACAPwAAAEA="}},
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), `"embedding":"AACAPwAAAEA="`) {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestWriteEmbeddingNeverEmitsANullVector(t *testing.T) {
	// An OpenAI client indexes into this array. null there is a crash, not an
	// empty vector.
	w := httptest.NewRecorder()
	if err := WriteEmbedding(w, &ir.EmbeddingResponse{
		Model:      "m",
		Embeddings: []ir.Embedding{{Index: 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "null") {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestDialectServesTheEmbeddingSurface(t *testing.T) {
	var _ interface {
		ParseEmbedding(*http.Request, int64) (*ir.EmbeddingRequest, error)
		WriteEmbedding(http.ResponseWriter, *ir.EmbeddingResponse) error
	} = New()
}

func TestParseModerationNormalizesBothInputShapes(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []string
	}{
		{`{"model":"m","input":"hello"}`, []string{"hello"}},
		{`{"model":"m","input":["a","b"]}`, []string{"a", "b"}},
	} {
		req, err := ParseModeration(httptest.NewRequest("POST", "/v1/moderations",
			strings.NewReader(tc.body)), 1<<20)
		if err != nil {
			t.Fatalf("ParseModeration(%s): %v", tc.body, err)
		}
		if len(req.Input) != len(tc.want) || req.Input[0] != tc.want[0] {
			t.Errorf("ParseModeration(%s).Input = %v, want %v", tc.body, req.Input, tc.want)
		}
	}
}

func TestParseModerationRejectsContentParts(t *testing.T) {
	// Accepting this while dropping the image parts would moderate the text
	// and report the whole input clean, which is worse than refusing it.
	_, err := ParseModeration(httptest.NewRequest("POST", "/v1/moderations",
		strings.NewReader(`{"model":"m","input":[{"type":"image_url","image_url":{"url":"x"}}]}`)), 1<<20)
	if err == nil {
		t.Fatal("a content-part input was accepted")
	}
	if !strings.Contains(err.Error(), "text") {
		t.Errorf("err = %v; it must say what is supported", err)
	}
}

func TestWriteModerationCarriesEveryCategory(t *testing.T) {
	// The category list is provider-defined and grows. A dropped category on a
	// moderation endpoint is a safety signal the client never sees.
	w := httptest.NewRecorder()
	if err := WriteModeration(w, &ir.ModerationResponse{
		ID: "modr-1", Model: "omni-moderation-latest",
		Results: []ir.ModerationResult{{
			Flagged:    true,
			Categories: map[string]bool{"harassment": true, "a-category-invented-later": false},
			Scores:     map[string]float64{"harassment": 0.91, "a-category-invented-later": 0.01},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var body struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Results []struct {
			Flagged    bool               `json:"flagged"`
			Categories map[string]bool    `json:"categories"`
			Scores     map[string]float64 `json:"category_scores"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "modr-1" || len(body.Results) != 1 || !body.Results[0].Flagged {
		t.Fatalf("body = %s", w.Body.String())
	}
	if _, ok := body.Results[0].Categories["a-category-invented-later"]; !ok {
		t.Errorf("categories = %v; an unknown category was dropped", body.Results[0].Categories)
	}
	if body.Results[0].Scores["harassment"] != 0.91 {
		t.Errorf("scores = %v", body.Results[0].Scores)
	}
}

func TestParseRerankReadsBothDocumentForms(t *testing.T) {
	req, err := ParseRerank(httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"rerank-v3.5","query":"q","documents":["plain",{"text":"boxed"}],
		  "top_n":1,"return_documents":true}`)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "rerank-v3.5" || req.Query != "q" {
		t.Errorf("request = %+v", req)
	}
	if len(req.Documents) != 2 || req.Documents[0] != "plain" || req.Documents[1] != "boxed" {
		t.Errorf("documents = %v", req.Documents)
	}
	if req.TopN != 1 || !req.ReturnDocuments {
		t.Errorf("top_n = %d, return_documents = %v", req.TopN, req.ReturnDocuments)
	}
	if len(req.Warnings) != 0 {
		t.Errorf("warnings = %v; neither document lost a field", req.Warnings)
	}
}

func TestParseRerankWarnsOnDroppedDocumentFields(t *testing.T) {
	// A document object with structured fields is reranked on its text alone.
	// The client cannot tell that from the response, so the trace must.
	req, err := ParseRerank(httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"m","query":"q","documents":[{"text":"t","title":"T","url":"u"}]}`)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one", req.Warnings)
	}
	if !strings.Contains(req.Warnings[0].Reason, "title") ||
		!strings.Contains(req.Warnings[0].Reason, "url") {
		t.Errorf("warning = %q; it must name the dropped fields", req.Warnings[0].Reason)
	}
}

func TestParseRerankRejectsAnUnusableRequest(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","documents":["a"]}`,
		`{"model":"m","query":"q"}`,
		`{"model":"m","query":"q","documents":[]}`,
		`{"model":"m","query":"q","documents":[{"title":"no text"}]}`,
	} {
		if _, err := ParseRerank(httptest.NewRequest("POST", "/v1/rerank",
			strings.NewReader(body)), 1<<20); err == nil {
			t.Errorf("ParseRerank(%s) accepted an unusable request", body)
		}
	}
}

func TestWriteRerankEmitsCohereV2(t *testing.T) {
	w := httptest.NewRecorder()
	if err := WriteRerank(w, &ir.RerankResponse{
		ID: "r-1", Model: "rerank-v3.5",
		Results: []ir.RerankResult{
			{Index: 2, RelevanceScore: 0.98, Document: "kept"},
			{Index: 0, RelevanceScore: 0.11},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var body struct {
		ID      string `json:"id"`
		Results []struct {
			Index    int     `json:"index"`
			Score    float64 `json:"relevance_score"`
			Document *struct {
				Text string `json:"text"`
			} `json:"document"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "r-1" || len(body.Results) != 2 {
		t.Fatalf("body = %s", w.Body.String())
	}
	if body.Results[0].Index != 2 || body.Results[0].Score != 0.98 {
		t.Errorf("first result = %+v", body.Results[0])
	}
	if body.Results[0].Document == nil || body.Results[0].Document.Text != "kept" {
		t.Errorf("document = %v; a returned document was lost", body.Results[0].Document)
	}
	if body.Results[1].Document != nil {
		t.Errorf("document = %v; a result with no document must omit the key entirely",
			body.Results[1].Document)
	}
}
