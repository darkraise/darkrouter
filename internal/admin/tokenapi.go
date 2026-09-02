package admin

import (
	"net/http"
	"time"
)

type proxyTokenView struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	// Secret is populated only by the creation response. A listing that
	// reproduced it would defeat the point of hashing the column.
	Secret string `json:"secret,omitempty"`
}

func (s *Server) handleListProxyTokens(w http.ResponseWriter, r *http.Request) {
	toks, err := s.deps.DB.ProxyTokens(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := []proxyTokenView{}
	for _, t := range toks {
		view := proxyTokenView{
			ID: t.ID, Name: t.Name, Prefix: t.Prefix,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		}
		if t.LastUsedAt != nil {
			used := t.LastUsedAt.Format(time.RFC3339)
			view.LastUsedAt = &used
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func (s *Server) handleCreateProxyToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, 8<<10, &body) {
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	tok, err := s.deps.DB.CreateProxyToken(r.Context(), body.Name)
	if err != nil {
		internalError(w, r, err)
		return
	}
	// The only response that carries the secret. It is not stored in plaintext,
	// so this is the operator's one chance to copy it.
	writeJSON(w, http.StatusCreated, proxyTokenView{
		ID: tok.ID, Name: tok.Name, Prefix: tok.Prefix,
		CreatedAt: tok.CreatedAt.Format(time.RFC3339),
		Secret:    tok.Secret,
	})
}

func (s *Server) handleDeleteProxyToken(w http.ResponseWriter, r *http.Request) {
	removed, err := s.deps.DB.DeleteProxyToken(r.Context(), r.PathValue("id"))
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "no proxy token with that id")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePatchCredential enables, disables or replaces one credential.
//
// It never echoes a secret, per phase 7 §4.1: the response says what changed,
// not what it changed to.
func (s *Server) handlePatchCredential(w http.ResponseWriter, r *http.Request) {
	if s.deps.Key == nil {
		writeError(w, http.StatusServiceUnavailable, "no keyring")
		return
	}
	providerID, keyID := r.PathValue("id"), r.PathValue("keyId")
	var body struct {
		Secret  *string `json:"secret"`
		Enabled *bool   `json:"enabled"`
	}
	if !decodeJSON(w, r, 16<<10, &body) {
		return
	}
	if body.Secret == nil && body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "nothing to change")
		return
	}
	if body.Secret != nil && *body.Secret == "" {
		writeError(w, http.StatusBadRequest, "secret must not be empty")
		return
	}

	if body.Enabled != nil {
		found, err := s.deps.DB.SetCredentialEnabled(r.Context(), providerID, keyID, *body.Enabled)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "no credential with that id")
			return
		}
	}
	if body.Secret != nil {
		if err := s.deps.DB.ReplaceProviderCredentialSecret(r.Context(), s.deps.Key,
			providerID, keyID, *body.Secret, nil); err != nil {
			writeStoreError(w, r, err)
			return
		}
	}
	// A changed credential invalidates whatever the breaker learned from the
	// old one, so the provider gets a clean slate rather than inheriting a
	// cooldown earned by a key that no longer exists.
	s.clearCooldowns(r.Context(), providerID, keyID)
	// And whatever the auth manager holds for it: an OAuth account is cached
	// under the credential id, so a replaced secret would otherwise keep
	// presenting the token the old one minted, and a credential that was
	// terminally refused would stay refused for the life of the process.
	s.forgetCredential(keyID)
	// Disabling a credential is the emergency revocation control and replacing
	// one is a rotation. Both are worthless if the decrypted set the router
	// serves from keeps the old value until an unrelated mutation reloads it.
	s.reloadProviders(afterCommit(r))
	// The updated credential as the listing would show it: the mask, never
	// the value.
	creds, err := s.deps.DB.Credentials(r.Context(), s.deps.Key, providerID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	for _, c := range creds {
		if c.ID == keyID {
			writeJSON(w, http.StatusOK, s.credentialView(providerID, c))
			return
		}
	}
	writeError(w, http.StatusNotFound, "no credential with that id")
}
