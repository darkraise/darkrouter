package admin

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/auth"
)

// routePath substitutes sample values for a pattern's wildcards.
func routePath(pattern string, params map[string]string) string {
	out := pattern
	for name, val := range params {
		out = strings.ReplaceAll(out, "{"+name+"}", val)
	}
	return out
}

var sampleParams = map[string]string{
	"id": "p1", "keyId": "k1", "provider": "p1", "model": "m",
}

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

func TestEveryRouteCarriesItsGuard(t *testing.T) {
	// Walked from the route table rather than a hand-kept list, so a route
	// added without a guard fails here rather than shipping open.
	s, _ := testServerFull(t)
	s.deps.Flows = auth.NewFlowStore(0)
	s.routes()
	cookie, _ := login(t, s)

	public := 0
	for _, rt := range s.routeTable() {
		path := routePath(rt.pattern, sampleParams)

		// No cookie at all.
		w := httptest.NewRecorder()
		r := httptest.NewRequest(rt.method, path, strings.NewReader("{}"))
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Content-Type", "application/json")
		s.Handler().ServeHTTP(w, r)
		switch rt.auth {
		case routePublic:
			public++
			// Login answers 401 to a wrong password; what a public route
			// must not do is demand a session.
			if strings.Contains(w.Body.String(), "not authenticated") {
				t.Errorf("%s %s is public but demanded a session", rt.method, rt.pattern)
			}
		default:
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s without a session = %d, want 401", rt.method, rt.pattern, w.Code)
			}
		}

		// A cookie but no CSRF token: only a same-origin mutation is refused.
		if rt.auth == routeCSRF {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(rt.method, path, strings.NewReader("{}"))
			r.AddCookie(cookie)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s without a csrf token = %d, want 403", rt.method, rt.pattern, w.Code)
			}
		}
	}
	if public != 2 {
		t.Errorf("%d public routes, want exactly status and login", public)
	}
}

func TestOAuthRoutesExistOnlyWithAFlowStore(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "POST", "/api/providers/p1/oauth/start", "{}"); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), "no such endpoint") {
		t.Errorf("without a flow store: %d %s, want the missing-route 404", w.Code, w.Body.String())
	}
	s.deps.Flows = auth.NewFlowStore(0)
	s.routes()
	if w := do(t, s, cookie, token, "POST", "/api/providers/p1/oauth/start", "{}"); strings.Contains(w.Body.String(), "no such endpoint") {
		t.Errorf("with a flow store the route is still missing: %d %s", w.Code, w.Body.String())
	}
}

func TestNoRouteReturnsCredentialMaterial(t *testing.T) {
	// Spec §4.1: never plaintext, never ciphertext, not for editing, not for
	// export. Every route in the table, so the one nobody has written yet is
	// covered the moment it lands. Deletes run last so the credential is
	// still there for everything that could leak it.
	s, _ := testServerFull(t)
	s.deps.Flows = auth.NewFlowStore(0)
	s.routes()
	cookie, token := login(t, s)

	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`); w.Code != http.StatusCreated {
		t.Fatalf("create provider = %d, body = %s", w.Code, w.Body.String())
	}
	w := do(t, s, cookie, token, "POST", "/api/providers/p1/keys",
		`{"label":"primary","secret":"`+theSecret+`"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("add credential = %d, body = %s", w.Code, w.Body.String())
	}
	keyID := strings.Trim(strings.Split(strings.Split(w.Body.String(), `"id":`)[1], ",")[0], `" }`)
	params := map[string]string{"id": "p1", "keyId": keyID, "provider": "p1", "model": "m"}
	bodies := map[string]string{
		"PATCH /api/providers/{id}/keys/{keyId}": `{"enabled":true}`,
		"PATCH /api/providers/{id}":              `{"name":"renamed"}`,
		"POST /api/providers/{id}/oauth/start":   `{"label":"x"}`,
	}

	routes := s.routeTable()
	sort.SliceStable(routes, func(i, j int) bool {
		return routes[i].method != "DELETE" && routes[j].method == "DELETE"
	})
	b64 := base64.StdEncoding.EncodeToString([]byte(theSecret))
	for _, rt := range routes {
		body := bodies[rt.method+" "+rt.pattern]
		if body == "" {
			body = "{}"
		}
		w := do(t, s, cookie, token, rt.method, routePath(rt.pattern, params), body)
		got := w.Body.String()
		if strings.Contains(got, theSecret) {
			t.Errorf("%s %s returned the secret in plaintext:\n%s", rt.method, rt.pattern, got)
		}
		if strings.Contains(got, b64) {
			t.Errorf("%s %s returned the secret base64-encoded:\n%s", rt.method, rt.pattern, got)
		}
		// The masked form is the only thing that may carry any of it, and only
		// the last four characters.
		if strings.Contains(got, "DARKROUTERLEAKCANARY") {
			t.Errorf("%s %s returned more than the masked suffix:\n%s", rt.method, rt.pattern, got)
		}
	}
}
