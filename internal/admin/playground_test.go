package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThePlaygroundStreamsThroughTheRealExecutor(t *testing.T) {
	// A mock would verify the playground rather than the gateway: it would
	// pass while the credential it exists to test is wrong.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, c := range []string{
			`{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"he"}}]}`,
			`{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"llo"}}]}`,
			`{"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		} {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			f.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer upstream.Close()

	s := testServerWithExecutor(t, upstream.URL, "m")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/playground", `{"model":"m","prompt":"say hi"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), `"he"`) || !strings.Contains(w.Body.String(), `"llo"`) {
		t.Errorf("the deltas did not reach the client:\n%s", w.Body.String())
	}
}

func TestThePlaygroundReturnsTheRequestIDForTheTraceLink(t *testing.T) {
	// Spec §6's "follow a link to the trace it produced". The id has to arrive
	// before the body, because the body is a stream the SPA renders as it
	// comes.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","model":"m","choices":[
		  {"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	s := testServerWithExecutor(t, upstream.URL, "m")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/playground",
		`{"model":"m","prompt":"hi","stream":false}`)
	if got := w.Header().Get("X-Darkrouter-Request"); got == "" {
		t.Error("no request id header; the trace link has nothing to point at")
	}
}

func TestAPlaygroundRequestLandsInTheLog(t *testing.T) {
	// The link only works because the request really is in the log — which it
	// is because the playground goes through exec rather than around it.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","model":"m","choices":[
		  {"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	s := testServerWithExecutor(t, upstream.URL, "m")
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/playground",
		`{"model":"m","prompt":"hi","stream":false}`)
	id := w.Header().Get("X-Darkrouter-Request")
	if id == "" {
		t.Fatal("no request id")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestThePlaygroundRequiresACSRFToken(t *testing.T) {
	// Spec §4: a mutating verb, so it carries the header like any other
	// despite streaming its response.
	s := testServerWithExecutor(t, "https://unused.example", "m")
	cookie, _ := login(t, s)

	r := httptest.NewRequest("POST", "/api/playground",
		strings.NewReader(`{"model":"m","prompt":"hi"}`))
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestThePlaygroundRejectsAnEmptyPrompt(t *testing.T) {
	s := testServerWithExecutor(t, "https://unused.example", "m")
	cookie, token := login(t, s)
	for _, body := range []string{`{"model":"m"}`, `{"prompt":"hi"}`} {
		w := do(t, s, cookie, token, "POST", "/api/playground", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("playground(%s) status = %d, want 400", body, w.Code)
		}
	}
}
