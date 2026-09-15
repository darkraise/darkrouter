package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// A transport error quotes the request URL, and a query-param key is part of
// it. The probe's answer is rendered in the browser, where the key is never
// shown after it is saved.
func TestAFailedProbeDoesNotShowAQueryParamKey(t *testing.T) {
	const secret = "sk-live/secret+value="
	var sawKey atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey.Store(r.URL.Query().Get("key") == secret)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"qp","name":"qp","kind":"openaicompat","base_url":"`+upstream.URL+
			`","auth_style":"query-param"}`); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "POST", "/api/providers/qp/keys",
		`{"label":"primary","secret":"`+secret+`"}`); w.Code != http.StatusCreated {
		t.Fatalf("key: %d %s", w.Code, w.Body.String())
	}

	got := probeProvider(t, s, cookie, token, "qp")
	if !sawKey.Load() {
		t.Fatal("the key was not sent as a query parameter, so this proves nothing")
	}
	if got.OK || got.Error == "" {
		t.Fatalf("ok = %v, error = %q; want the dropped connection reported", got.OK, got.Error)
	}
	for _, form := range []string{secret, url.QueryEscape(secret), "secret"} {
		if strings.Contains(got.Error, form) {
			t.Errorf("error %q carries the key as %q", got.Error, form)
		}
	}
}
