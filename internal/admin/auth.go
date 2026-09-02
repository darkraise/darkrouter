package admin

import (
	"context"
	"net/http"
	"net/url"
)

// sessionKeyType is the context key carrying the authenticated session id, so a
// handler that needs it does not re-read the cookie and re-validate.
type sessionKeyType struct{}

var sessionKey sessionKeyType

func sessionFrom(ctx context.Context) string {
	s, _ := ctx.Value(sessionKey).(string)
	return s
}

// requireSession validates the cookie against the database and slides the
// expiry. A missing or expired session is 401, which is what tells the SPA to
// render the login screen.
func (s *Server) requireSession(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		ok, terr := s.deps.DB.TouchSession(r.Context(), c.Value, sessionTTL)
		if terr != nil {
			// A database failure is not a logout. Saying 401 here would send
			// the operator to a login screen that cannot work either.
			internalError(w, r, terr)
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), sessionKey, c.Value)))
	}
}

// requireCSRF wraps requireSession and adds the two checks a state-changing
// request needs. It wraps rather than sits beside it so a mutating route cannot
// get one check without the other.
func (s *Server) requireCSRF(h http.HandlerFunc) http.HandlerFunc {
	return s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		if !s.csrf.Valid(sessionFrom(r.Context()), r.Header.Get(csrfHeader)) {
			writeError(w, http.StatusForbidden, "invalid csrf token")
			return
		}
		h(w, r)
	})
}

// sameOrigin reports whether a state-changing request came from the SPA.
//
// Sec-Fetch-Site is checked first because it is unforgeable by page script and
// present in every browser that matters. Origin is the fallback for a client
// that does not send it. A request presenting neither is refused: treating "no
// header at all" as same-origin is how the check becomes decorative, and the
// SPA always sends at least one.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "same-site", "cross-site", "none":
		// "none" is a user-initiated navigation — but a form the operator was
		// tricked into submitting from a bookmarklet or a data: URL arrives
		// the same way, and none of the SPA's own mutations do.
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Host, not scheme: the default homelab posture is plain HTTP, so requiring
	// https here would refuse every legitimate request.
	return u.Host == r.Host
}
