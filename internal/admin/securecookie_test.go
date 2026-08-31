package admin

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loginFrom performs a login with the request shaped by the caller, and
// returns the session cookie it set along with the CSRF token.
func loginFrom(t *testing.T, s *Server, shape func(*http.Request)) (*http.Cookie, string) {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"`+testPassword+`"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	shape(r)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login set no cookie")
	}
	var body struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return cookies[0], body.CSRF
}

func TestSessionCookieIsSecureOverDirectTLS(t *testing.T) {
	s, _ := testServerFull(t)
	c, _ := loginFrom(t, s, func(r *http.Request) { r.TLS = &tls.ConnectionState{} })
	if !c.Secure {
		t.Error("a cookie issued over TLS is not marked Secure")
	}
}

// The plain-HTTP LAN default. Marking the cookie Secure there makes the
// browser drop it and login silently never works.
func TestSessionCookieIsNotSecureOverPlainHTTP(t *testing.T) {
	s, _ := testServerFull(t)
	c, _ := loginFrom(t, s, func(*http.Request) {})
	if c.Secure {
		t.Error("a cookie issued over plain HTTP is marked Secure")
	}
}

// The shipped Caddyfile terminates TLS and proxies plain HTTP to the admin
// port, so r.TLS is nil for a deployment that is HTTPS end to end from the
// browser's side. The cookie has to be Secure anyway.
func TestSessionCookieIsSecureBehindATerminatingProxy(t *testing.T) {
	s, _ := testServerFull(t)
	c, _ := loginFrom(t, s, func(r *http.Request) {
		r.RemoteAddr = "172.18.0.4:44212"
		r.Header.Set("X-Forwarded-Proto", "https")
	})
	if !c.Secure {
		t.Error("a cookie issued behind a TLS-terminating proxy is not marked Secure")
	}
}

// The header is only evidence when it comes from a proxy. A client that can
// reach the port directly must not be able to set it, or anyone on the LAN
// can lock an operator out of their own console.
func TestAForwardedProtoFromAPublicPeerIsIgnored(t *testing.T) {
	s, _ := testServerFull(t)
	c, _ := loginFrom(t, s, func(r *http.Request) {
		r.RemoteAddr = "203.0.113.7:9000"
		r.Header.Set("X-Forwarded-Proto", "https")
	})
	if c.Secure {
		t.Error("a forwarded scheme from a public peer was trusted")
	}
}

// The deletion cookie has to carry the same attributes as the one it clears.
// A browser that will not accept a non-Secure Set-Cookie for a name it holds
// as Secure leaves the operator logged in after clicking Log out.
func TestTheLogoutCookieMatchesTheSessionCookie(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := loginFrom(t, s, func(r *http.Request) { r.TLS = &tls.ConnectionState{} })

	r := httptest.NewRequest("POST", "/api/auth/logout", nil)
	r.TLS = &tls.ConnectionState{}
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set(csrfHeader, token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", w.Code, w.Body.String())
	}
	cleared := w.Result().Cookies()[0]
	if cleared.MaxAge >= 0 {
		t.Fatalf("logout did not clear the cookie: MaxAge = %d", cleared.MaxAge)
	}
	if !cleared.Secure {
		t.Error("the deletion cookie is not Secure while the session cookie is")
	}
}
