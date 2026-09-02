package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

// settingAdminPasswordHash holds a password set through the API. It takes
// precedence over the hash the process started with, unless the environment
// hash has changed since the row was written — see reconcilePasswordHash.
const settingAdminPasswordHash = "admin.password_hash"

// settingPasswordEnvFingerprint records which environment hash was current
// when the stored password was last reconciled, so a changed environment can
// be told from an unchanged one without keeping the hash itself twice.
const settingPasswordEnvFingerprint = "admin.password_env_fingerprint"

func fingerprint(hash string) string {
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:])
}

// reconcilePasswordHash decides which hash wins at startup.
//
// A password changed through the console outlives a restart, which is what
// the settings row is for. But an operator who then sets a new
// DARKROUTER_ADMIN_PASSWORD_HASH — the documented way to recover a lost
// password — expects it to take effect, and a row that silently won would
// leave them locked out with the environment saying otherwise. The
// environment's fingerprint is recorded when the row is first seen; if it
// later differs, the environment is newer and the row goes.
func (s *Server) reconcilePasswordHash(ctx context.Context) error {
	db := s.deps.DB
	env := s.deps.PasswordHash
	if env == "" {
		return nil
	}
	_, stored, err := db.GetSetting(ctx, settingAdminPasswordHash)
	if err != nil {
		return err
	}
	if !stored {
		return nil
	}
	seen, hasSeen, err := db.GetSetting(ctx, settingPasswordEnvFingerprint)
	if err != nil {
		return err
	}
	current := fingerprint(env)
	if !hasSeen {
		return db.PutSetting(ctx, settingPasswordEnvFingerprint, current)
	}
	if seen == current {
		return nil
	}
	if err := db.DeleteSetting(ctx, settingAdminPasswordHash); err != nil {
		return err
	}
	slog.Warn("DARKROUTER_ADMIN_PASSWORD_HASH changed since the password was last set in the console; the environment's hash is now in effect")
	return db.PutSetting(ctx, settingPasswordEnvFingerprint, current)
}

// currentPasswordHash is the hash logins are checked against: whatever the
// operator last set, falling back to the one supplied at startup.
func (s *Server) currentPasswordHash(ctx context.Context) string {
	if s.deps.DB != nil {
		if stored, ok, err := s.deps.DB.GetSetting(ctx, settingAdminPasswordHash); err == nil && ok && stored != "" {
			return stored
		}
	}
	return s.deps.PasswordHash
}

// sessionIDPrefix is how much of a stored session id a listing shows and the
// least a revoke may name. The stored id is a digest rather than the cookie
// value, but eight hex characters is still enough to tell rows apart and too
// little to guess one.
const sessionIDPrefix = 8

type sessionView struct {
	ID        string `json:"id"`
	Prefix    string `json:"prefix"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	Current   bool   `json:"current"`
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.DB.SessionRows(r.Context(), time.Now())
	if err != nil {
		internalError(w, r, err)
		return
	}
	mine := store.HashSessionID(sessionFrom(r.Context()))
	out := []sessionView{}
	for _, row := range rows {
		prefix := row.ID
		if len(prefix) > sessionIDPrefix {
			prefix = prefix[:sessionIDPrefix]
		}
		out = append(out, sessionView{
			// A stable handle for the revoke button that is not the credential
			// itself: revoking takes the prefix, not the cookie value.
			ID:        prefix,
			Prefix:    prefix,
			CreatedAt: row.CreatedAt.Format(time.RFC3339),
			ExpiresAt: row.ExpiresAt.Format(time.RFC3339),
			Current:   row.ID == mine,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// handleDeleteSession revokes by the full stored id or by a prefix of at
// least sessionIDPrefix characters that names exactly one row. A shorter
// prefix, or one two rows share, is refused rather than resolved by luck.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	want := r.PathValue("id")
	if len(want) < sessionIDPrefix {
		writeError(w, http.StatusBadRequest, "a session id needs at least 8 characters")
		return
	}
	rows, err := s.deps.DB.SessionRows(r.Context(), time.Now())
	if err != nil {
		internalError(w, r, err)
		return
	}
	var matches []string
	for _, row := range rows {
		if row.ID == want {
			matches = []string{row.ID}
			break
		}
		if len(row.ID) > len(want) && row.ID[:len(want)] == want {
			matches = append(matches, row.ID)
		}
	}
	switch len(matches) {
	case 0:
		writeError(w, http.StatusNotFound, "no session with that id")
		return
	case 1:
	default:
		writeError(w, http.StatusConflict, "that prefix matches more than one session")
		return
	}
	removed, err := s.deps.DB.RevokeSession(r.Context(), matches[0])
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "no session with that id")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decodeJSON(w, r, 4<<10, &body) {
		return
	}
	// Required even though the caller already holds a session: without it, a
	// stolen cookie becomes a permanent takeover rather than one that expires.
	if !VerifyPassword(s.currentPasswordHash(r.Context()), body.Current) {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	if len(body.New) < 12 {
		writeError(w, http.StatusBadRequest, "the new password must be at least 12 characters")
		return
	}
	if len(body.New) > maxPasswordBytes {
		writeError(w, http.StatusBadRequest, "the new password must be at most 72 bytes")
		return
	}
	hash, err := HashPassword(body.New)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := s.deps.DB.PutSetting(r.Context(), settingAdminPasswordHash, hash); err != nil {
		internalError(w, r, err)
		return
	}
	// The environment hash in force now is the one this row was set beside;
	// only a later change to it should override the row.
	if s.deps.PasswordHash != "" {
		if err := s.deps.DB.PutSetting(r.Context(), settingPasswordEnvFingerprint,
			fingerprint(s.deps.PasswordHash)); err != nil {
			internalError(w, r, err)
			return
		}
	}
	// Every other session, not this one: revoking the caller would log the
	// operator out of the screen they just used.
	revoked, err := s.deps.DB.DeleteSessionsExcept(r.Context(), sessionFrom(r.Context()))
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
}
