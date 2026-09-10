package admin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

// requireAdmin answers whether the caller may manage accounts.
//
// Checked in the handler rather than as a fourth routeAuth tier: only three
// endpoints care, and a guard tier implies a permission model the rest of the
// console does not have. Every other route stays reachable by every account.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	u, ok, err := s.deps.DB.UserByID(r.Context(), userFrom(r.Context()))
	if err != nil {
		internalError(w, r, err)
		return false
	}
	if !ok || u.Role != store.RoleAdmin {
		writeError(w, http.StatusForbidden, "only an administrator can manage accounts")
		return false
	}
	return true
}

type userView struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	users, err := s.deps.DB.Users(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := []userView{}
	for _, u := range users {
		out = append(out, userView{
			ID: u.ID, Username: u.Username, Role: u.Role,
			CreatedAt: u.CreatedAt.Format(time.RFC3339),
		})
	}
	// The console has no other way to learn which row is the caller's own:
	// /api/auth/status is routePublic and cannot carry it, and the listing is
	// the one admin-only response every client of this route already fetches.
	// requireAdmin has already resolved the caller above, so this is free.
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "me": userFrom(r.Context())})
}

// handleCreateUser adds an account, validating exactly as the founding claim
// does so the two ways an account can come into existence cannot disagree
// about what a usable username or password is.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decodeJSON(w, r, 4<<10, &body) {
		return
	}
	username, msg, ok := validateCredentials(body.Username, body.Password)
	if !ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// The column has no CHECK constraint and the store validates nothing, so
	// this is the only thing standing between a misspelled role and a row that
	// AdminCount cannot see -- which is what keeps the last administrator from
	// deleting themselves into an unmanageable console.
	if body.Role != store.RoleAdmin && body.Role != store.RoleMember {
		writeError(w, http.StatusBadRequest, "the role must be admin or member")
		return
	}
	// Before hashing: bcrypt is deliberately expensive, and a request that is
	// going to be refused should not pay for it.
	if _, taken, err := s.deps.DB.UserByUsername(r.Context(), username); err != nil {
		internalError(w, r, err)
		return
	} else if taken {
		writeError(w, http.StatusConflict, "that username is already taken")
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
	if err := s.deps.DB.CreateUser(r.Context(), id, username, hash, body.Role); err != nil {
		internalError(w, r, err)
		return
	}
	slog.Info("an account was created", "username", username, "role", body.Role)
	writeJSON(w, http.StatusCreated, userView{
		ID: id, Username: username, Role: body.Role,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// handleDeleteUser removes an account, taking its sessions with it through the
// cascade on the sessions table.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	target, ok, err := s.deps.DB.UserByID(r.Context(), r.PathValue("id"))
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such account")
		return
	}
	if target.Role == store.RoleAdmin {
		n, err := s.deps.DB.AdminCount(r.Context())
		if err != nil {
			internalError(w, r, err)
			return
		}
		// There is no recovery path: a console with no administrator cannot be
		// managed again by any route the code provides.
		if n <= 1 {
			writeError(w, http.StatusConflict, "the last administrator cannot be removed")
			return
		}
	}
	if _, err := s.deps.DB.DeleteUser(r.Context(), target.ID); err != nil {
		internalError(w, r, err)
		return
	}
	slog.Info("an account was removed", "username", target.Username)
	w.WriteHeader(http.StatusNoContent)
}
