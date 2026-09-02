package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

func TestEveryResponseCarriesSecurityHeaders(t *testing.T) {
	s, _ := testServer(t)
	for _, path := range []string{"/", "/api/auth/status", "/api/nosuchthing", "/requests/01ABC"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		for name, want := range map[string]string{
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "same-origin",
			"Content-Security-Policy": contentSecurityPolicy,
		} {
			if got := w.Header().Get(name); got != want {
				t.Errorf("%s: %s = %q, want %q", path, name, got, want)
			}
		}
	}
}

func TestIndexIsRevalidatedAndAssetsAreImmutable(t *testing.T) {
	s, _ := testServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", got)
	}
	body := w.Body.String()
	start := strings.Index(body, `src="/assets/`)
	if start < 0 {
		t.Skip("the index references no asset by absolute path")
	}
	rest := body[start+len(`src="`):]
	src := rest[:strings.Index(rest, `"`)]
	aw := httptest.NewRecorder()
	s.Handler().ServeHTTP(aw, httptest.NewRequest("GET", src, nil))
	if got := aw.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("asset Cache-Control = %q", got)
	}
}

func TestTheAssetDirectoryIsNotListed(t *testing.T) {
	s, _ := testServer(t)
	for _, path := range []string{"/assets/", "/assets", "/assets/nope.js"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, w.Code)
		}
		if strings.Contains(w.Body.String(), "<a href") || strings.Contains(w.Body.String(), `id="root"`) {
			t.Errorf("%s served a listing or the index:\n%s", path, w.Body.String())
		}
	}
}

func TestLoginIsRateLimitedPerAddress(t *testing.T) {
	s, _ := testServer(t)
	attempt := func(addr string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"wrong"}`))
		r.RemoteAddr = addr
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		s.Handler().ServeHTTP(w, r)
		return w
	}
	for i := 0; i < loginBurst; i++ {
		if w := attempt("10.0.0.1:1"); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, w.Code)
		}
	}
	w := attempt("10.0.0.1:2")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d = %d, want 429", loginBurst+1, w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("429 carries no Retry-After")
	}
	// Another address is unaffected.
	if w := attempt("10.0.0.2:1"); w.Code != http.StatusUnauthorized {
		t.Errorf("a second address = %d, want 401", w.Code)
	}
}

func TestTheBucketRefillsAtTheRate(t *testing.T) {
	l := newLoginLimiter(loginRate, loginBurst, loginConcurrency)
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := 0; i < loginBurst; i++ {
		if ok, _ := l.take("a"); !ok {
			t.Fatalf("take %d refused", i+1)
		}
	}
	ok, wait := l.take("a")
	if ok || wait <= 0 || wait > 13*time.Second {
		t.Fatalf("empty bucket: ok=%v wait=%s", ok, wait)
	}
	now = now.Add(12 * time.Second)
	if ok, _ := l.take("a"); !ok {
		t.Error("one token did not refill after twelve seconds")
	}
}

func TestBcryptVerificationsAreCapped(t *testing.T) {
	l := newLoginLimiter(loginRate, loginBurst, 2)
	r1, ok1 := l.acquire()
	r2, ok2 := l.acquire()
	_, ok3 := l.acquire()
	if !ok1 || !ok2 || ok3 {
		t.Fatalf("acquire = %v %v %v, want two slots", ok1, ok2, ok3)
	}
	r1()
	if _, ok := l.acquire(); !ok {
		t.Error("a released slot could not be reacquired")
	}
	r2()
}

func TestSecFetchSiteNoneIsNotSameOrigin(t *testing.T) {
	s, _ := testServer(t)
	cookie, token := login(t, s)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/logout", nil)
	r.AddCookie(cookie)
	r.Header.Set(csrfHeader, token)
	r.Header.Set("Sec-Fetch-Site", "none")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("Sec-Fetch-Site: none = %d, want 403", w.Code)
	}
}

func TestTheCookieValueIsNotStored(t *testing.T) {
	s, db := testServer(t)
	cookie, _ := login(t, s)
	var n int
	if err := db.Read.QueryRow(`SELECT count(*) FROM sessions WHERE id = ?`, cookie.Value).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the session table holds the cookie value")
	}
	if err := db.Read.QueryRow(`SELECT count(*) FROM sessions WHERE id = ?`, store.HashSessionID(cookie.Value)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("the session table holds no digest of the cookie value")
	}
}

func TestOverviewCountsCredentialsWithoutAKeyring(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	key, err := store.OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProvider(ctx, store.ProviderRow{ID: "p", Kind: "openaicompat", BaseURL: "https://x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, key, store.Credential{ProviderID: "p", Secret: "s", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	// No Key: the overview must still count and flag the credential.
	s, err := New(Deps{DB: db, PasswordHash: testHash()})
	if err != nil {
		t.Fatal(err)
	}
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/overview", "")
	var body struct {
		Providers []tileView `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Providers) != 1 || body.Providers[0].Credentials != 1 || !body.Providers[0].NeedsAuth {
		t.Errorf("tiles = %+v", body.Providers)
	}
}

func TestInternalErrorsSayNothingSpecific(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	_ = db.Close()
	for _, ep := range []struct{ method, path, body string }{
		{"GET", "/api/proxy-tokens", ""},
		{"PATCH", "/api/providers/p1", `{"name":"x"}`},
	} {
		w := do(t, s, cookie, token, ep.method, ep.path, ep.body)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("%s %s with a closed database = %d, want 500: %s", ep.method, ep.path, w.Code, w.Body.String())
		}
		if got := strings.TrimSpace(w.Body.String()); got != `{"error":"internal error"}` {
			t.Errorf("%s %s body = %s, want the fixed message", ep.method, ep.path, got)
		}
	}
}
