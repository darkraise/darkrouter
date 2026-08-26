package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// settingAdminPasswordHash holds a password set through the API. It takes
// precedence over the hash the process started with, because otherwise a
// change would be forgotten at the next restart.
const settingAdminPasswordHash = "admin.password_hash"

// currentPasswordHash is the hash logins are checked against: whatever the
// operator last set, falling back to the one supplied at startup.
func (s *Server) currentPasswordHash(ctx context.Context) string {
	if s.deps.DB != nil {
		var stored string
		err := s.deps.DB.Read.QueryRowContext(ctx,
			`SELECT value FROM settings WHERE key = ?`,
			settingAdminPasswordHash).Scan(&stored)
		if err == nil && stored != "" {
			return stored
		}
	}
	return s.deps.PasswordHash
}

// sessionIDPrefix is how much of a session id a listing may show. The id is
// the credential the cookie carries, so reproducing it in full would let a
// screenshot of the settings screen authenticate.
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	mine := sessionFrom(r.Context())
	out := []sessionView{}
	for _, row := range rows {
		prefix := row.ID
		if len(prefix) > sessionIDPrefix {
			prefix = prefix[:sessionIDPrefix]
		}
		out = append(out, sessionView{
			// A stable handle for the revoke button that is not the credential
			// itself: revoking takes the prefix, not the full id.
			ID:        prefix,
			Prefix:    prefix,
			CreatedAt: row.CreatedAt.Format(time.RFC3339),
			ExpiresAt: row.ExpiresAt.Format(time.RFC3339),
			Current:   row.ID == mine,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	want := r.PathValue("id")
	rows, err := s.deps.DB.SessionRows(r.Context(), time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, row := range rows {
		if len(row.ID) >= len(want) && row.ID[:len(want)] == want {
			if err := s.deps.DB.DeleteSession(r.Context(), row.ID); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w, http.StatusNotFound, "no session with that id")
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
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
	hash, err := HashPassword(body.New)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.deps.DB.Write.ExecContext(r.Context(),
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingAdminPasswordHash, hash); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Every other session, not this one: revoking the caller would log the
	// operator out of the screen they just used.
	revoked, err := s.deps.DB.DeleteSessionsExcept(r.Context(), sessionFrom(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
}
