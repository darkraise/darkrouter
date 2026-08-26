package admin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/darkraise/darkrouter/internal/edge"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
)

// countBody selects the counting dialect. There is no "openai": that wire has
// no counting endpoint, so an OpenAI-dialect count could only ever be the
// local estimate, and offering it would present an estimate as a reading.
type countBody struct {
	Dialect string `json:"dialect"`
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
}

func countRequest(ctx context.Context, body countBody) (*http.Request, edge.CountWriter, string, error) {
	if body.Model == "" || body.Prompt == "" {
		return nil, nil, "", errors.New("model and prompt are required")
	}
	var (
		d       edge.CountWriter
		path    string
		segment string
		payload map[string]any
	)
	switch body.Dialect {
	case "anthropic":
		d = anthropicedge.New()
		path = "/v1/messages/count_tokens"
		payload = map[string]any{
			"model":    body.Model,
			"messages": []map[string]any{{"role": "user", "content": body.Prompt}},
		}
	case "gemini":
		d = geminiedge.New()
		segment = body.Model + ":countTokens"
		path = "/v1beta/models/" + url.PathEscape(body.Model) + ":countTokens"
		payload = map[string]any{
			"contents": []map[string]any{
				{"role": "user", "parts": []map[string]any{{"text": body.Prompt}}},
			},
		}
	default:
		return nil, nil, "", errors.New("dialect must be anthropic or gemini")
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, "", err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, "", err
	}
	r.Header.Set("Content-Type", "application/json")
	if segment != "" {
		r.SetPathValue("model", segment)
	}
	return r, d, body.Dialect, nil
}

func (s *Server) handlePlaygroundCount(w http.ResponseWriter, r *http.Request) {
	if s.deps.Exec == nil {
		writeError(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	var body countBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	pr, d, native, err := countRequest(r.Context(), body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.deps.Exec.HandleCount(w, pr, d, native)
}

// auxPaths mirrors the proxy routes the console may exercise. The paths are
// the same strings internal/server registers, because the executor's surface
// dispatch and the log both read them.
var auxPaths = map[string]string{
	"embeddings":     "/v1/embeddings",
	"rerank":         "/v1/rerank",
	"moderations":    "/v1/moderations",
	"images":         "/v1/images/generations",
	"speech":         "/v1/audio/speech",
	"transcriptions": "/v1/audio/transcriptions",
}

type auxBody struct {
	Surface  string          `json:"surface"`
	Model    string          `json:"model,omitempty"`
	Body     json.RawMessage `json:"body,omitempty"`
	FileB64  string          `json:"file_b64,omitempty"`
	Filename string          `json:"filename,omitempty"`
}

func auxRequest(ctx context.Context, in auxBody) (*http.Request, error) {
	path, ok := auxPaths[in.Surface]
	if !ok {
		return nil, errors.New("unknown surface " + in.Surface)
	}
	if in.Surface == "transcriptions" {
		return auxMultipartRequest(ctx, path, in)
	}

	merged := map[string]any{}
	if len(in.Body) > 0 {
		if err := json.Unmarshal(in.Body, &merged); err != nil {
			return nil, errors.New("body must be a JSON object")
		}
	}
	if in.Model != "" {
		merged["model"] = in.Model
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	return r, nil
}

// auxMultipartRequest rebuilds the upload form from base64. The console cannot
// forward a browser multipart body through a JSON API, so the file arrives
// encoded and is re-encoded here into the form the transcription surface parses.
func auxMultipartRequest(ctx context.Context, path string, in auxBody) (*http.Request, error) {
	file, err := base64.StdEncoding.DecodeString(in.FileB64)
	if err != nil || len(file) == 0 {
		return nil, errors.New("file_b64 must be non-empty base64")
	}
	name := in.Filename
	if name == "" {
		name = "audio.bin"
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(file); err != nil {
		return nil, err
	}
	if err := mw.WriteField("model", in.Model); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, path, &buf)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r, nil
}

func (s *Server) handlePlaygroundAux(w http.ResponseWriter, r *http.Request) {
	if s.deps.Exec == nil {
		writeError(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	var in auxBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	pr, err := auxRequest(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Each surface has its own executor entry point and its own narrow
	// dialect. There is no shared Handle for these: an embeddings body sent
	// through the chat path parses as an empty conversation.
	oa := openaiedge.New()
	switch in.Surface {
	case "embeddings":
		s.deps.Exec.HandleEmbeddings(w, pr, oa)
	case "rerank":
		s.deps.Exec.HandleRerank(w, pr, oa)
	case "moderations":
		s.deps.Exec.HandleModerations(w, pr, oa)
	case "images":
		s.deps.Exec.HandleImages(w, pr, oa)
	case "speech":
		s.deps.Exec.HandleSpeech(w, pr, oa)
	case "transcriptions":
		s.deps.Exec.HandleTranscriptions(w, pr, oa)
	}
}
