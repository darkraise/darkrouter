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

// validateCredentials is the shared floor for every path that creates an
// account: the founding claim and the account-management endpoint. One function
// rather than the same three checks written twice, because the two paths
// agreeing about what a usable username or password is has to be structural --
// a copy drifts the first time one side is edited.
//
// It returns the trimmed username, since the display name is stored as given
// and the caller is the only thing that trims it. The confirmation check stays
// at handleSetup's call site: only the claim has a second password field.
func validateCredentials(username, password string) (string, string, bool) {
	username = strings.TrimSpace(username)
	if username == "" || len([]rune(username)) > maxUsernameChars {
		return "", "a username is required", false
	}
	if len(password) < minPasswordChars {
		return "", "the password must be at least 12 characters", false
	}
	if len(password) > maxPasswordBytes {
		return "", "the password must be at most 72 bytes", false
	}
	return username, "", true
}

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
	username, msg, ok := validateCredentials(body.Username, body.Password)
	if !ok {
		writeError(w, http.StatusBadRequest, msg)
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
