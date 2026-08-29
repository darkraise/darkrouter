package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

// presetView is the wire shape. Config is json.RawMessage in both directions:
// the server is a courier for the console's own settings, and a struct here
// would strip any field this binary has not learned yet.
type presetView struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Dialect   string          `json:"dialect"`
	Model     string          `json:"model"`
	Config    json.RawMessage `json:"config"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

func viewOfPreset(p store.PlaygroundPreset) presetView {
	return presetView{
		ID: p.ID, Name: p.Name, Dialect: p.Dialect, Model: p.Model, Config: p.Config,
		CreatedAt: p.CreatedAt.Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.Format(time.RFC3339),
	}
}

type presetBody struct {
	Name    string          `json:"name"`
	Dialect string          `json:"dialect"`
	Model   string          `json:"model"`
	Config  json.RawMessage `json:"config"`
}

// readPresetBody decodes and validates everything the store will not.
//
// The blob's interior is never inspected beyond confirming it is an object:
// that is the one shape the client can merge, and anything inside it belongs
// to the console.
func readPresetBody(w http.ResponseWriter, r *http.Request) (presetBody, bool) {
	var body presetBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return presetBody{}, false
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "a preset needs a name")
		return presetBody{}, false
	}
	var probe map[string]any
	if err := json.Unmarshal(body.Config, &probe); err != nil || probe == nil {
		writeError(w, http.StatusBadRequest, "config must be a JSON object")
		return presetBody{}, false
	}
	return body, true
}

func (s *Server) handleListPlaygroundPresets(w http.ResponseWriter, r *http.Request) {
	presets, err := s.deps.DB.PlaygroundPresets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []presetView{}
	for _, p := range presets {
		out = append(out, viewOfPreset(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreatePlaygroundPreset(w http.ResponseWriter, r *http.Request) {
	body, ok := readPresetBody(w, r)
	if !ok {
		return
	}
	// Checked before the insert rather than caught after it: the dialog needs
	// the clashing row's id to offer an overwrite, and the unique index would
	// only tell it that something went wrong.
	if existing, found, err := s.deps.DB.PlaygroundPresetByName(r.Context(), body.Name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if found {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "a preset called " + body.Name + " already exists",
			"id":    existing.ID,
		})
		return
	}
	made, err := s.deps.DB.CreatePlaygroundPreset(
		r.Context(), body.Name, body.Dialect, body.Model, body.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, viewOfPreset(made))
}

func (s *Server) handleUpdatePlaygroundPreset(w http.ResponseWriter, r *http.Request) {
	body, ok := readPresetBody(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	moved, err := s.deps.DB.UpdatePlaygroundPreset(
		r.Context(), id, body.Name, body.Dialect, body.Model, body.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !moved {
		writeError(w, http.StatusNotFound, "no such preset")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) handleDeletePlaygroundPreset(w http.ResponseWriter, r *http.Request) {
	removed, err := s.deps.DB.DeletePlaygroundPreset(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "no such preset")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
