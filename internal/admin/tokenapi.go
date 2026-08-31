package admin

import (
	"encoding/json"
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateProxyToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	tok, err := s.deps.DB.CreateProxyToken(r.Context(), body.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
	providerID, keyID := r.PathValue("id"), r.PathValue("keyId")
	var body struct {
		Secret  *string `json:"secret"`
		Enabled *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Secret == nil && body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "nothing to change")
		return
	}

	changed := map[string]any{}
	if body.Enabled != nil {
		found, err := s.deps.DB.SetCredentialEnabled(r.Context(), providerID, keyID, *body.Enabled)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "no credential with that id")
			return
		}
		changed["enabled"] = *body.Enabled
	}
	if body.Secret != nil {
		if err := s.deps.DB.ReplaceCredentialSecret(r.Context(), s.deps.Key,
			keyID, *body.Secret, nil); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Reported as a fact, not as a value.
		changed["secret"] = "replaced"
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
	s.reloadProviders(r.Context())
	writeJSON(w, http.StatusOK, changed)
}
