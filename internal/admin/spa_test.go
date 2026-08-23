package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheSPAIsServedFromTheBinary(t *testing.T) {
	s, _ := testServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
}

func TestADeepLinkServesTheIndex(t *testing.T) {
	// A single-page app owns its routes. A browser reloading on
	// /requests/01ABC asks the server for that path, and a 404 there breaks
	// every deep link and every refresh.
	s, _ := testServer(t)
	for _, path := range []string{"/requests", "/requests/01ABC", "/settings", "/catalog"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", path, w.Code)
		}
	}
}

func TestAnUnknownAPIPathIsNotTheSPA(t *testing.T) {
	// A typo in the SPA's fetch must return a JSON 404, not HTML. Otherwise
	// the client reports a parse error and the real problem stays hidden.
	s, _ := testServer(t)
	for _, m := range []string{"GET", "POST"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(m, "/api/does-not-exist", nil))
		if w.Code == http.StatusOK {
			t.Fatalf("%s an unknown API path returned 200:\n%s", m, w.Body.String())
		}
		body := w.Body.String()
		if strings.Contains(body, "<!doctype") || strings.Contains(body, "<html") {
			t.Errorf("%s an unknown API path returned HTML:\n%s", m, body)
		}
	}
}

func TestTheEmbeddedIndexIsARealBuild(t *testing.T) {
	// A lone .gitkeep embeds cleanly and serves a broken page. This is what
	// tells the difference between "the SPA is embedded" and "a directory is
	// embedded".
	s, _ := testServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	body := w.Body.String()
	if !strings.Contains(body, `<div id="root"`) && !strings.Contains(body, "<div id='root'") {
		t.Fatalf("the served index has no mount point; the frontend was not built:\n%s", body)
	}
	if !strings.Contains(body, "<script") {
		t.Errorf("the served index loads no script; the bundle is missing:\n%s", body)
	}
}

func TestAnAssetIsServedFromTheBundle(t *testing.T) {
	// The fallback must not swallow real files: an asset request that returned
	// index.html would make every script tag load HTML.
	s, _ := testServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	body := w.Body.String()

	start := strings.Index(body, `src="/assets/`)
	if start < 0 {
		t.Skip("the index references no asset by absolute path")
	}
	rest := body[start+len(`src="`):]
	src := rest[:strings.Index(rest, `"`)]

	aw := httptest.NewRecorder()
	s.Handler().ServeHTTP(aw, httptest.NewRequest("GET", src, nil))
	if aw.Code != http.StatusOK {
		t.Fatalf("%s status = %d", src, aw.Code)
	}
	if strings.Contains(aw.Body.String(), "<div id=\"root\"") {
		t.Errorf("%s served index.html rather than the asset", src)
	}
}
