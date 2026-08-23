package vertex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/ir"
)

// buildAnthropic renders through phase 4's Anthropic builder and then applies
// the two differences Vertex imposes: the model moves from the body into the
// URL, and anthropic_version becomes a body field.
//
// Rewriting the rendered body rather than reimplementing the renderer is
// deliberate. Everything phase 4 encodes — cache breakpoints, thinking modes,
// the assistant-prefill rules — is behavior the golden files pin, and a second
// implementation would drift from it silently.
func buildAnthropic(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	inner := *t
	// A placeholder: the URL is replaced below. Empty would make the builder
	// produce a relative path that http.NewRequest rejects.
	inner.BaseURL = "https://vertex.invalid"
	inner.APIKey = ""

	hr, warns, err := anthropicadapter.BuildRequest(ctx, &inner, req)
	if err != nil {
		return nil, warns, err
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		return nil, warns, err
	}
	_ = hr.Body.Close()

	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, warns, fmt.Errorf("vertex: re-read anthropic body: %w", err)
	}
	// Vertex takes the model in the URL and rejects a body that also names it.
	delete(body, "model")
	body["anthropic_version"], err = json.Marshal(AnthropicVersion)
	if err != nil {
		return nil, warns, err
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, warns, err
	}

	method := ":rawPredict"
	if req.Stream {
		method = ":streamRawPredict"
	}
	endpoint := EndpointFor(t.Project, t.Location) + "/" + PublisherAnthropic +
		"/models/" + escapePathSegment(t.Model) + method

	out, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, warns, err
	}
	out.Header.Set("Content-Type", "application/json")
	// anthropic-version is a body field here, not a header. Sending only the
	// header is a 400: Vertex reads the body.
	return out, warns, nil
}

// escapePathSegment percent-encodes everything outside RFC 3986's unreserved
// set. url.PathEscape leaves ':' alone, and while Vertex does not sign the path
// the way Bedrock does, a model id carrying one would otherwise open a path
// segment the API does not match.
func escapePathSegment(s string) string {
	var b bytes.Buffer
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// parseResponse dispatches on the payload's own shape.
//
// Neither ParseResponse nor ParseStream is handed a Target, so the publisher is
// not available. That is fine because the two shapes are unambiguous: an
// Anthropic response carries a top-level content array and type "message", a
// Gemini one carries candidates.
func parseResponse(resp *http.Response) (*ir.Response, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	closeErr := resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	restore := func() *http.Response {
		clone := *resp
		clone.Body = io.NopCloser(bytes.NewReader(raw))
		return &clone
	}
	if isAnthropicShape(raw) {
		return anthropicadapter.ParseResponse(restore())
	}
	return geminiadapter.ParseResponse(restore())
}

func isAnthropicShape(raw []byte) bool {
	var probe struct {
		Type       string          `json:"type"`
		Content    json.RawMessage `json:"content"`
		Candidates json.RawMessage `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	if len(probe.Candidates) > 0 {
		return false
	}
	return probe.Type == "message" || len(probe.Content) > 0
}

// parseStream chooses by the stream's first bytes without consuming them.
// Anthropic's SSE begins with "event:"; Vertex's Gemini SSE with "data:".
func parseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	br := bufio.NewReaderSize(r, 4<<10)
	prefix, err := br.Peek(6)
	if err != nil && len(prefix) == 0 {
		return func(yield func(ir.StreamEvent, error) bool) {
			yield(ir.StreamEvent{}, err)
		}
	}
	if string(prefix) == "event:" {
		return anthropicadapter.ParseStream(br, maxLine)
	}
	return geminiadapter.ParseStream(br, maxLine)
}

var _ = adapter.OutcomeSuccess
