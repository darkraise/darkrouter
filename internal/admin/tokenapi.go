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
