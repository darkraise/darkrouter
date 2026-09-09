package admin

import (
	"log/slog"
	"net/http"
	"strings"
)

// maxUsernameChars bounds what a claim may store. Long enough for any name
// somebody will actually pick, short enough that the column is not a place to
// put arbitrary data.
const maxUsernameChars = 64

// handleSetup claims an unclaimed console: the first account created becomes
// admin.
//
// There is no token. Whoever reaches the console first claims it, which is a
// deliberate reversal of the rule this file used to enforce -- see
// docs/plan/decisions.md. What stands in for the token is the startup warning
// on a populated database and the note in deploy.md, both weaker than proving
// host access, which is why the reversal is written down rather than assumed.
//
// It mints no session. The screen spends the password on a real login straight
// afterwards, so one code path issues cookies and the stored hash is exercised
// before the operator relies on it. With no recovery path that ordering is
// what keeps a bad hash a retyped password rather than a lost console.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	// Before the body is read, as on login: a flood costs a map lookup.
	if ok, wait := s.logins.take(clientAddr(r.RemoteAddr)); !ok {
		writeRateLimited(w, wait)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	if !decodeJSON(w, r, 4<<10, &body) {
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" || len([]rune(username)) > maxUsernameChars {
		writeError(w, http.StatusBadRequest, "a username is required")
		return
	}
	if len(body.Password) < minPasswordChars {
		writeError(w, http.StatusBadRequest, "the password must be at least 12 characters")
		return
	}
	if len(body.Password) > maxPasswordBytes {
		writeError(w, http.StatusBadRequest, "the password must be at most 72 bytes")
		return
	}
	// Checked on the server, not only in the screen: with no recovery path a
	// typo in the founding password is unrecoverable, and an API client must
	// not be able to skip the guard the screen enforces.
	if body.Password != body.Confirm {
		writeError(w, http.StatusBadRequest, "the two passwords do not match")
		return
	}
	hash, err := HashPassword(body.Password)
	if err != nil {
		internalError(w, r, err)
		return
	}
	id, err := newSessionID() // 32 bytes of entropy; reused as an opaque row id
	if err != nil {
		internalError(w, r, err)
		return
	}
	// The emptiness test is inside the INSERT, so two claims arriving together
	// cannot both be told they won.
	won, err := s.deps.DB.ClaimFirstUser(r.Context(), id, username, hash)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !won {
		writeError(w, http.StatusConflict, "the console has already been set up")
		return
	}
	slog.Info("the console was claimed", "username", username)
	writeJSON(w, http.StatusOK, map[string]any{"configured": true})
}
