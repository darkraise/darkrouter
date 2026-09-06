package admin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
)

// mintSetupToken generates the claim for a console that has no password yet.
//
// A console nobody can log into has to be claimable, and the alternative --
// letting whoever reaches the port first set the password -- would make an
// empty hash open the port rather than close it, which is the inverse of what
// VerifyPassword promises. Requiring a token from the startup log means the
// claim needs host access rather than merely a route to the port.
func (s *Server) mintSetupToken(ctx context.Context) error {
	if s.currentPasswordHash(ctx) != "" {
		return nil
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	s.setupMu.Lock()
	s.setupToken = base64.RawURLEncoding.EncodeToString(b)
	token := s.setupToken
	s.setupMu.Unlock()

	// slog only. startupWarnings reaches unauthenticated /healthz and the
	// config endpoint, and a claim token printed there would be readable by
	// exactly the caller it exists to keep out.
	slog.Warn("no admin password is set; claim the console with this setup token",
		"setup_token", token)
	return nil
}

// PasswordConfigured reports whether the console can be logged into at all.
// Read at request time rather than remembered from startup, because the setup
// page changes the answer while the process runs.
func (s *Server) PasswordConfigured(ctx context.Context) bool {
	return s.currentPasswordHash(ctx) != ""
}

func (s *Server) currentSetupToken() string {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return s.setupToken
}

// spendSetupToken clears the claim, so the log line cannot be replayed against
// a console whose password has since been changed.
func (s *Server) spendSetupToken() {
	s.setupMu.Lock()
	s.setupToken = ""
	s.setupMu.Unlock()
}

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
	// Before the token is looked at, so a claimed console never compares one.
	if s.currentPasswordHash(r.Context()) != "" {
		writeError(w, http.StatusConflict, "the console has already been set up")
		return
	}
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, 4<<10, &body) {
		return
	}
	token := s.currentSetupToken()
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(body.Token)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid setup token")
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
	hash, err := HashPassword(body.Password)
	if err != nil {
		internalError(w, r, err)
		return
	}
	// Insert-or-ignore rather than a write: two claims arriving together must
	// not leave the loser believing it set the password. bcrypt salts every
	// hash, so the winner is the one whose own hash came back.
	stored, err := s.deps.DB.InitSetting(r.Context(), settingAdminPasswordHash, hash)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if stored != hash {
		writeError(w, http.StatusConflict, "the console has already been set up")
		return
	}
	if err := s.recordPasswordEnv(r.Context()); err != nil {
		internalError(w, r, err)
		return
	}
	s.spendSetupToken()
	slog.Info("the admin password was set from the setup page")
	writeJSON(w, http.StatusOK, map[string]any{"configured": true})
}
