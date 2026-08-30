package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

// playgroundPresetView is the wire shape. Config is json.RawMessage in both
// directions: the server is a courier for the console's own settings, and a
// struct here would strip any field this binary has not learned yet.
type playgroundPresetView struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Dialect   string          `json:"dialect"`
	Model     string          `json:"model"`
	Config    json.RawMessage `json:"config"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

func viewOfPreset(p store.PlaygroundPreset) playgroundPresetView {
	return playgroundPresetView{
		ID: p.ID, Name: p.Name, Dialect: p.Dialect, Model: p.Model, Config: p.Config,
		CreatedAt: p.CreatedAt.Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.Format(time.RFC3339),
	}
}

type playgroundPresetBody struct {
	Name    string          `json:"name"`
	Dialect string          `json:"dialect"`
	Model   string          `json:"model"`
	Config  json.RawMessage `json:"config"`
}

// validPlaygroundDialects mirrors the switch playgroundRequest dials on in
// playground.go. dialect-support.ts on the client has no fallback case for an
// unknown dialect, so a preset naming any other wire would crash the config
// pane's render the moment it loads.
var validPlaygroundDialects = map[string]bool{
	"openai":    true,
	"anthropic": true,
	"gemini":    true,
}

// readPresetBody decodes and validates everything the store will not.
//
// The blob's interior is never inspected beyond confirming it is an object:
// that is the one shape the client can merge, and anything inside it belongs
// to the console.
func readPresetBody(w http.ResponseWriter, r *http.Request) (playgroundPresetBody, bool) {
	var body playgroundPresetBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return playgroundPresetBody{}, false
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "a preset needs a name")
		return playgroundPresetBody{}, false
	}
	if !validPlaygroundDialects[body.Dialect] {
		writeError(w, http.StatusBadRequest, "dialect must be one of openai, anthropic, gemini")
		return playgroundPresetBody{}, false
	}
	var probe map[string]any
	if err := json.Unmarshal(body.Config, &probe); err != nil || probe == nil {
		writeError(w, http.StatusBadRequest, "config must be a JSON object")
		return playgroundPresetBody{}, false
	}
	return body, true
}

func (s *Server) handleListPlaygroundPresets(w http.ResponseWriter, r *http.Request) {
	presets, err := s.deps.DB.PlaygroundPresets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []playgroundPresetView{}
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

// playgroundConversationView is the rail's row. Config is json.RawMessage in
// both directions for the same reason a preset's is: the server is a courier
// for the console's own settings, and a struct here would strip any field this
// binary has not learned yet.
type playgroundConversationView struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Dialect   string          `json:"dialect"`
	Model     string          `json:"model"`
	Config    json.RawMessage `json:"config"`
	Preview   string          `json:"preview"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// playgroundTurnView is one stored message. RequestID is a plain string rather
// than a pointer: the client treats a missing trace as ordinary, so "" and
// absent mean the same thing and a null would be a third state to handle.
type playgroundTurnView struct {
	Seq       int    `json:"seq"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	RequestID string `json:"request_id"`
	CreatedAt string `json:"created_at"`
}

type playgroundConversationDetail struct {
	playgroundConversationView
	Messages []playgroundTurnView `json:"messages"`
}

func viewOfConversation(c store.PlaygroundConversation) playgroundConversationView {
	return playgroundConversationView{
		ID: c.ID, Title: c.Title, Dialect: c.Dialect, Model: c.Model,
		Config: c.Config, Preview: c.Preview,
		CreatedAt: c.CreatedAt.Format(time.RFC3339),
		UpdatedAt: c.UpdatedAt.Format(time.RFC3339),
	}
}

type playgroundConversationBody struct {
	Title   string          `json:"title"`
	Dialect string          `json:"dialect"`
	Model   string          `json:"model"`
	Config  json.RawMessage `json:"config"`
}

// readConversationBody decodes and validates everything the store will not.
//
// The blob's interior is never inspected beyond confirming it is an object:
// that is the one shape the client can merge, and anything inside it belongs
// to the console.
func readConversationBody(w http.ResponseWriter, r *http.Request) (playgroundConversationBody, bool) {
	var body playgroundConversationBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return playgroundConversationBody{}, false
	}
	if body.Title == "" {
		writeError(w, http.StatusBadRequest, "a conversation needs a title")
		return playgroundConversationBody{}, false
	}
	if !validPlaygroundDialects[body.Dialect] {
		writeError(w, http.StatusBadRequest, "dialect must be one of openai, anthropic, gemini")
		return playgroundConversationBody{}, false
	}
	var probe map[string]any
	if err := json.Unmarshal(body.Config, &probe); err != nil || probe == nil {
		writeError(w, http.StatusBadRequest, "config must be a JSON object")
		return playgroundConversationBody{}, false
	}
	return body, true
}

type playgroundTurnBody struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// The request whose trace explains this turn, when there is one. Empty is
	// ordinary rather than an error: the log writer batches on a timer.
	RequestID string `json:"request_id"`
}

func readTurnBody(w http.ResponseWriter, r *http.Request) (playgroundTurnBody, bool) {
	var body playgroundTurnBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return playgroundTurnBody{}, false
	}
	if body.Role != "user" && body.Role != "assistant" {
		writeError(w, http.StatusBadRequest, "role must be user or assistant")
		return playgroundTurnBody{}, false
	}
	return body, true
}

// reapAge is how long an empty conversation is left alone before the listing
// removes it. A conversation with no turns means a client that died between
// creating one and sending its first message; a conversation created seconds
// ago in another tab is the ordinary case and must survive.
const reapAge = time.Hour

func (s *Server) handleListPlaygroundConversations(w http.ResponseWriter, r *http.Request) {
	// Housekeeping, not the caller's business: a rail that fails to load
	// because a stale empty row could not be removed would be a worse
	// outcome than the row staying one more minute.
	_, _ = s.deps.DB.ReapEmptyPlaygroundConversations(r.Context(), time.Now().UTC().Add(-reapAge))

	conversations, err := s.deps.DB.PlaygroundConversations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []playgroundConversationView{}
	for _, c := range conversations {
		out = append(out, viewOfConversation(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreatePlaygroundConversation(w http.ResponseWriter, r *http.Request) {
	body, ok := readConversationBody(w, r)
	if !ok {
		return
	}
	made, err := s.deps.DB.CreatePlaygroundConversation(
		r.Context(), body.Title, body.Dialect, body.Model, body.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, viewOfConversation(made))
}

func (s *Server) handleGetPlaygroundConversation(w http.ResponseWriter, r *http.Request) {
	c, turns, found, err := s.deps.DB.PlaygroundConversationByID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no such conversation")
		return
	}
	messages := []playgroundTurnView{}
	for _, t := range turns {
		messages = append(messages, playgroundTurnView{
			Seq: t.Seq, Role: t.Role, Content: t.Content, RequestID: t.RequestID,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, playgroundConversationDetail{
		playgroundConversationView: viewOfConversation(c),
		Messages:                   messages,
	})
}

func (s *Server) handleUpdatePlaygroundConversation(w http.ResponseWriter, r *http.Request) {
	body, ok := readConversationBody(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	moved, err := s.deps.DB.UpdatePlaygroundConversation(
		r.Context(), id, body.Title, body.Dialect, body.Model, body.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !moved {
		writeError(w, http.StatusNotFound, "no such conversation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) handleDeletePlaygroundConversation(w http.ResponseWriter, r *http.Request) {
	removed, err := s.deps.DB.DeletePlaygroundConversation(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "no such conversation")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAppendPlaygroundTurn(w http.ResponseWriter, r *http.Request) {
	body, ok := readTurnBody(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	// Checked before the insert rather than caught after it: the foreign key
	// would answer with a constraint failure, which reads as a server fault
	// rather than as the missing conversation it is.
	_, _, found, err := s.deps.DB.PlaygroundConversationByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no such conversation")
		return
	}
	turn, err := s.deps.DB.AppendPlaygroundTurn(
		r.Context(), id, body.Role, body.Content, body.RequestID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"seq": turn.Seq})
}

// requireConversationSaving refuses a write when the operator has turned
// playground.save_conversations off.
//
// Reads and deletes stay open deliberately. The key governs what the playground
// may keep from here on; an operator who has just turned it off still needs to
// see what was kept before and to remove it, and a switch that also hid the
// history would leave prompt text on disk with no way to reach it.
func (s *Server) requireConversationSaving(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Config != nil && !s.deps.Config.Current().SaveConversations() {
			writeError(w, http.StatusForbidden,
				"playground.save_conversations is off, so conversations are not saved")
			return
		}
		next(w, r)
	}
}

// handlePurgePlaygroundConversations empties both tables.
//
// It is the settings screen's action rather than a side effect of the config
// value changing: config is file-backed and reloadable, and a setting whose
// reload deleted data would mean an edit to a file on disk silently destroying
// the operator's history.
func (s *Server) handlePurgePlaygroundConversations(w http.ResponseWriter, r *http.Request) {
	n, err := s.deps.DB.PurgePlaygroundConversations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": n})
}
