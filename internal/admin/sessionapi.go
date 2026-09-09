package admin

import (
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

// minPasswordChars is the floor the setup page and a password change share.
const minPasswordChars = 12

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
	rows, err := s.deps.DB.SessionRows(r.Context(), userFrom(r.Context()), time.Now())
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
	rows, err := s.deps.DB.SessionRows(r.Context(), userFrom(r.Context()), time.Now())
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
	removed, err := s.deps.DB.RevokeSession(r.Context(), userFrom(r.Context()), matches[0])
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
	uid := userFrom(r.Context())
	user, ok, err := s.deps.DB.UserByID(r.Context(), uid)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !ok {
		// The guard proved the session; the account behind it is gone.
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	// Required even though the caller already holds a session: without it, a
	// stolen cookie becomes a permanent takeover rather than one that expires.
	if !VerifyPassword(user.PasswordHash, body.Current) {
		writeError(w, http.StatusUnauthorized, "the current password is wrong")
		return
	}
	if len(body.New) < minPasswordChars {
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
	if err := s.deps.DB.SetUserPassword(r.Context(), uid, hash); err != nil {
		internalError(w, r, err)
		return
	}
	// Every other browser signed in as this account, and no other account:
	// revoking the caller would log the operator out of the screen they just
	// used, and revoking a stranger would sign out someone whose password did
	// not change.
	revoked, err := s.deps.DB.DeleteSessionsExcept(r.Context(), uid, sessionFrom(r.Context()))
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
}
