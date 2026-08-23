package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestAStatelessResponsesRequestServes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","model":"gpt-4o","choices":[
		  {"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],
		  "usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "gpt-4o", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.Handle(w, httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"gpt-4o","input":"hi"}`)), openaiedge.NewResponses())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Object     string `json:"object"`
		Status     string `json:"status"`
		OutputText string `json:"output_text"`
		Store      bool   `json:"store"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "response" || body.Status != "completed" || body.OutputText != "hello" {
		t.Errorf("body = %s", w.Body.String())
	}
	if body.Store {
		t.Error("store = true; the id is not resumable and the client must be told")
	}
	got := rec.only(t)
	if got.Dialect != "openai-responses" {
		t.Errorf("dialect = %q", got.Dialect)
	}
	if got.Surface != "llm" || got.Status != "success" || got.TokensIn != 3 {
		t.Errorf("record = %+v", got)
	}
}

func TestAStatefulResponsesRequestIsRejectedAndLogged(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a stateful request reached an upstream")
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "gpt-4o", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.Handle(w, httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"gpt-4o","input":"hi","previous_response_id":"resp_dr_x"}`)),
		openaiedge.NewResponses())

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "previous_response_id") {
		t.Errorf("body = %s; the error must name what was refused", w.Body.String())
	}
	got := rec.only(t)
	if got.ErrorCode != string(ir.ErrInvalidRequest) || len(got.Attempts) != 0 {
		t.Errorf("record = %+v", got)
	}
}

func TestAResponsesRequestStreamsSemanticEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, chunk := range []string{
			`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
			`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"he"}}]}`,
			`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"llo"}}]}`,
			`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		} {
			_, _ = w.Write([]byte("data: " + chunk + "\n\n"))
			f.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer upstream.Close()

	e, _ := executorForOp(t, upstream.URL, catalogWith("p", "gpt-4o", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.Handle(w, httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"gpt-4o","input":"hi","stream":true}`)), openaiedge.NewResponses())

	body := w.Body.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.output_item.added",
		"event: response.output_text.delta",
		"event: response.output_item.done",
		"event: response.completed",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stream is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "[DONE]") {
		t.Errorf("the chat sentinel leaked into a responses stream:\n%s", body)
	}
	if !strings.Contains(body, `"delta":"he"`) || !strings.Contains(body, `"delta":"llo"`) {
		t.Errorf("the deltas did not reach the client:\n%s", body)
	}
}

func TestAResponsesParseWarningReachesTheRequestRow(t *testing.T) {
	// The dropped reasoning item is invisible in the response body, so the
	// request row is the only place it can be seen.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","model":"m","choices":[
		  {"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "gpt-4o", ir.SurfaceLLM))
	e.Handle(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"gpt-4o","input":[
		  {"type":"reasoning","id":"rs_1","summary":[]},{"role":"user","content":"hi"}]}`)),
		openaiedge.NewResponses())

	got := rec.only(t)
	if len(got.Warnings) == 0 ||
		!strings.Contains(strings.Join(got.Warnings, " "), "reasoning") {
		t.Errorf("warnings = %v", got.Warnings)
	}
}
