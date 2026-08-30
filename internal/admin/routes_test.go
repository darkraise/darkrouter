package admin

import (
	"net/http"
	"strings"
	"testing"
)

func TestUnknownAPIPathAnswersJSONOnEveryVerb(t *testing.T) {
	// The fallback exists so a mistyped API path reports the missing route
	// rather than returning the SPA's index.html, which the client reports as
	// a JSON parse error. Registered for GET and POST only, it did exactly
	// what it exists to prevent for the other four verbs.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	for _, method := range []string{"GET", "POST", "PATCH", "PUT", "DELETE"} {
		w := do(t, s, cookie, token, method, "/api/nosuchthing", "{}")
		if w.Code != http.StatusNotFound {
			t.Errorf("%s /api/nosuchthing = %d, want 404", method, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s /api/nosuchthing content-type = %q, want JSON", method, ct)
		}
	}
}
