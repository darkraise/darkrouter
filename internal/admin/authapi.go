package admin

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// newSessionID mints an opaque id. 32 bytes of randomness, base64url: the id is
// the bearer of the whole session, so it is not derived from anything and
// carries no structure worth guessing.
func newSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	authed := false
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if ok, terr := s.deps.DB.TouchSession(r.Context(), c.Value, sessionTTL); terr == nil && ok {
			authed = true
		}
	}
	// Whether a password exists at all, so a fresh install can explain itself
	// instead of showing a login that refuses every password.
	//
	// This is narrower than it looks and does not weaken §3. An absent hash
	// means no login can succeed, so disclosing it tells a caller the admin
	// API is unusable rather than that it is open -- which is why the login
	// handler still answers "invalid password" either way, and never says
	// which of the two it was.
	body := map[string]any{
		"authenticated": authed,
		"configured":    s.currentPasswordHash(r.Context()) != "",
	}
	if authed {
		// The SPA needs the token to make its first mutating call after a
		// reload, and re-issuing it here saves a second round trip.
		c, _ := r.Cookie(sessionCookie)
		body["csrf_token"] = s.csrf.Token(c.Value)
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Login CSRF is minor but real, and the SPA is same-origin, so the check
	// costs nothing here either.
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !VerifyPassword(s.currentPasswordHash(r.Context()), body.Password) {
		// One message for both a wrong password and an unconfigured hash: an
		// operator reading "no password is set" learns the port is open.
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	// Spec §3: login rotates the session id, so a fixated id cannot survive an
	// authentication.
	if old, err := r.Cookie(sessionCookie); err == nil && old.Value != "" {
		_ = s.deps.DB.DeleteSession(r.Context(), old.Value)
	}
	id, err := newSessionID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create a session")
		return
	}
	if err := s.deps.DB.CreateSession(r.Context(), id, sessionTTL); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create a session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		// Lax rather than Strict: phase 8's OAuth callback is a state-changing
		// GET reached by a cross-site top-level redirect, which Strict blocks.
		SameSite: http.SameSiteLaxMode,
		// Only over TLS. On the plain-HTTP LAN default this must stay false or
		// the browser drops the cookie and login silently never works.
		Secure: servedOverTLS(r),
		MaxAge: int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"csrf_token":    s.csrf.Token(id),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.DB.DeleteSession(r.Context(), sessionFrom(r.Context())); err != nil {
		writeError(w, http.StatusInternalServerError, "could not end the session")
		return
	}
	// Same attributes as the cookie it clears: a browser holding a Secure
	// cookie under this name will not accept a non-Secure Set-Cookie for it,
	// which would leave the operator logged in after clicking Log out.
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
		Secure: servedOverTLS(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}

// servedOverTLS reports whether the browser's own connection is HTTPS.
//
// r.TLS answers it for a direct connection and is nil behind a reverse proxy
// that terminates TLS — which the shipped Caddyfile does, proxying plain HTTP
// to the admin port. Reading the scheme off the connection alone therefore
// left the intended HTTPS deployment issuing session cookies without Secure.
//
// The forwarded header is evidence only when the peer that set it is a proxy
// rather than a client, so it is read only from a loopback or private address.
// Without that, anyone able to reach the port directly could set the flag and
// lock an operator out of a console the browser would then refuse to send the
// cookie to.
func servedOverTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !peerIsLocal(r.RemoteAddr) {
		return false
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func peerIsLocal(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}
