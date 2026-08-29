// Package localcli serves a provider that is a local command-line program
// rather than a remote API.
//
// Some vendors ship no HTTP endpoint at all: the operator installs a CLI,
// authenticates it themselves, and the tool talks to the vendor on their
// behalf. Augment's `auggie` is one. There is no base URL to point at and no
// credential for darkrouter to hold — the login lives in the CLI's own state
// directory, which is the point.
//
// The seam is an http.RoundTripper registered for a scheme of its own. A
// preset whose base URL is auggie://cli/v1 reaches this package instead of the
// network, and everything upstream of the transport — the openaicompat
// adapter, the executor's attempt loop, the discovery sweep, the console's
// probe — keeps working unchanged, because what comes back is an ordinary
// OpenAI-compatible response. The alternative was a second transport concept
// threaded through the adapter interface, which is HTTP-shaped end to end.
package localcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CLI is one local program presented as an OpenAI-compatible provider.
type CLI interface {
	// Scheme is the URL scheme a preset uses to name this program.
	Scheme() string
	// Models are the ids the installed program recognizes. An error is
	// reported to the caller as a failed listing, which is what the discovery
	// sweep and the console probe already know how to show.
	Models(ctx context.Context) ([]string, error)
	// Run writes the reply to out as the program produces it. Whatever has
	// been written before an error is kept: a CLI that dies mid-sentence has
	// still said something, and the executor's commit rules already cover a
	// stream that stops early.
	Run(ctx context.Context, model, prompt string, out io.Writer) error
}

// Session carries a credential the operator configured for a CLI that takes
// one. A program authenticated by its own `login` command implements only CLI;
// one that can also read a session from its environment implements this, and
// the transport hands over whatever credential the request carried.
//
// The value never reaches a network: the request that carries it is
// synthesized in this process and terminated here.
type Session interface {
	// WithSession returns a copy of the CLI that runs under this session.
	// Empty returns the receiver, so a provider with no configured account
	// keeps whatever login the program holds on disk.
	WithSession(secret string) CLI
}

// Transport answers requests addressed to a CLI's scheme.
type Transport struct{ cli CLI }

// Install registers c on t, so a client built from t serves the CLI's scheme
// alongside http and https. Registration is per transport rather than global:
// three clients in this process have their own, and mutating
// http.DefaultTransport would reach code that never asked for this.
func Install(t *http.Transport, c CLI) {
	t.RegisterProtocol(c.Scheme(), &Transport{cli: c})
}

// NewTransport wraps c for a caller that holds its own RoundTripper.
func NewTransport(c CLI) *Transport { return &Transport{cli: c} }

// cliFor resolves the program this request runs against: the configured one,
// carrying the operator's session when they supplied one.
//
// The credential arrives in the Authorization header because that is where the
// adapter writes it, and reusing that path is what lets a CLI credential be
// stored, masked, rotated and cooled by the machinery every other provider
// already uses.
func (t *Transport) cliFor(r *http.Request) CLI {
	s, ok := t.cli.(Session)
	if !ok {
		return t.cli
	}
	secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return s.WithSession(strings.TrimSpace(secret))
}

func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	// A RoundTripper must not leave the body open, and the CLI path never
	// forwards it anywhere.
	var body []byte
	if r.Body != nil {
		defer func() { _ = r.Body.Close() }()
		b, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		body = b
	}

	switch {
	case strings.HasSuffix(r.URL.Path, "/models"):
		return t.models(r)
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		return t.chat(r, body)
	}
	// A surface this program does not serve. Reported as a 404 rather than a
	// transport error so it classifies as a fatal provider answer instead of a
	// connection failure the executor would retry.
	return errorResponse(r, http.StatusNotFound,
		fmt.Sprintf("%s serves no %s", r.URL.Scheme, r.URL.Path)), nil
}

// maxRequestBody bounds what is read from a caller in this process. The
// executor has already capped the client's body; this is a second, cheap guard
// so a bug upstream cannot buffer without limit here.
const maxRequestBody = 32 << 20

func (t *Transport) models(r *http.Request) (*http.Response, error) {
	ids, err := t.cliFor(r).Models(r.Context())
	if err != nil {
		return errorResponse(r, http.StatusBadGateway, err.Error()), nil
	}
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	out := struct {
		Object string  `json:"object"`
		Data   []model `json:"data"`
	}{Object: "list", Data: make([]model, 0, len(ids))}
	for _, id := range ids {
		out.Data = append(out.Data, model{ID: id, Object: "model", OwnedBy: r.URL.Scheme})
	}
	return jsonResponse(r, http.StatusOK, out), nil
}

// chatRequest is the part of an OpenAI chat body a local CLI can honour. Every
// other field is dropped rather than half-applied: a sampling parameter the
// program has no flag for would otherwise look accepted.
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (t *Transport) chat(r *http.Request, body []byte) (*http.Response, error) {
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return errorResponse(r, http.StatusBadRequest, "malformed request body"), nil
	}
	prompt := Prompt(req.Messages)
	created := time.Now().Unix()
	id := "chatcmpl-" + r.URL.Scheme + "-" + fmt.Sprint(created)

	if !req.Stream {
		var buf bytes.Buffer
		if err := t.cliFor(r).Run(r.Context(), req.Model, prompt, &buf); err != nil {
			return errorResponse(r, http.StatusBadGateway, err.Error()), nil
		}
		return jsonResponse(r, http.StatusOK, completion(id, req.Model, created, buf.String())), nil
	}

	// Streaming: the CLI writes into a pipe as it produces text, and each
	// write becomes one chunk. The reply is handed back before the program has
	// finished, which is what makes the first token arrive when the CLI
	// produces it rather than when it exits.
	pr, pw := io.Pipe()
	cli := t.cliFor(r)
	go func() {
		w := &chunkWriter{w: pw, id: id, model: req.Model, created: created}
		err := cli.Run(r.Context(), req.Model, prompt, w)
		if err != nil && !w.wrote {
			// Nothing was emitted, so the failure can still be the whole
			// answer rather than a truncated one.
			_ = writeEvent(pw, errorBody(err.Error()))
			_, _ = io.WriteString(pw, "data: [DONE]\n\n")
			_ = pw.Close()
			return
		}
		if err == nil {
			_ = w.finish()
		}
		_, _ = io.WriteString(pw, "data: [DONE]\n\n")
		// A mid-stream failure closes the pipe with the error, so the executor
		// sees a truncated stream rather than a clean end it would record as a
		// complete answer.
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()

	resp := baseResponse(r, http.StatusOK)
	resp.Header.Set("Content-Type", "text/event-stream")
	resp.Body = pr
	return resp, nil
}

// chunkWriter turns each write from the program into one OpenAI stream chunk.
type chunkWriter struct {
	w       io.Writer
	id      string
	model   string
	created int64
	wrote   bool
}

func (c *chunkWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !c.wrote {
		c.wrote = true
		// The role arrives in its own chunk, as OpenAI sends it: a client
		// reading only deltas must still learn who is speaking.
		if err := writeEvent(c.w, chunk(c.id, c.model, c.created, delta{Role: "assistant"}, "")); err != nil {
			return 0, err
		}
	}
	if err := writeEvent(c.w, chunk(c.id, c.model, c.created, delta{Content: string(p)}, "")); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *chunkWriter) finish() error {
	if !c.wrote {
		// A program that said nothing still owes the client a well-formed
		// stream, or the parser waits for content that never comes.
		if err := writeEvent(c.w, chunk(c.id, c.model, c.created, delta{Role: "assistant"}, "")); err != nil {
			return err
		}
	}
	return writeEvent(c.w, chunk(c.id, c.model, c.created, delta{}, "stop"))
}

type delta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

func chunk(id, model string, created int64, d delta, finish string) any {
	type choice struct {
		Index        int     `json:"index"`
		Delta        delta   `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	}
	c := choice{Delta: d}
	if finish != "" {
		c.FinishReason = &finish
	}
	return struct {
		ID      string   `json:"id"`
		Object  string   `json:"object"`
		Created int64    `json:"created"`
		Model   string   `json:"model"`
		Choices []choice `json:"choices"`
	}{ID: id, Object: "chat.completion.chunk", Created: created, Model: model, Choices: []choice{c}}
}

func completion(id, model string, created int64, text string) any {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type choice struct {
		Index        int    `json:"index"`
		Message      msg    `json:"message"`
		FinishReason string `json:"finish_reason"`
	}
	return struct {
		ID      string   `json:"id"`
		Object  string   `json:"object"`
		Created int64    `json:"created"`
		Model   string   `json:"model"`
		Choices []choice `json:"choices"`
	}{
		ID: id, Object: "chat.completion", Created: created, Model: model,
		Choices: []choice{{Message: msg{Role: "assistant", Content: text}, FinishReason: "stop"}},
	}
}

func writeEvent(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(append([]byte("data: "), b...), '\n', '\n'))
	return err
}

func errorBody(message string) any {
	return struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}{Error: struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	}{Message: message, Type: "local_cli_error"}}
}

func baseResponse(r *http.Request, status int) *http.Response {
	return &http.Response{
		Status:     http.StatusText(status),
		StatusCode: status,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1, ProtoMinor: 1,
		Header:  make(http.Header),
		Request: r,
	}
}

func jsonResponse(r *http.Request, status int, v any) *http.Response {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(`{"error":{"message":"could not encode the reply"}}`)
		status = http.StatusInternalServerError
	}
	resp := baseResponse(r, status)
	resp.Header.Set("Content-Type", "application/json")
	resp.ContentLength = int64(len(b))
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return resp
}

func errorResponse(r *http.Request, status int, message string) *http.Response {
	return jsonResponse(r, status, errorBody(message))
}

// Prompt flattens a chat into the single prompt a one-shot CLI takes.
//
// The role labels are not decoration: the program is handed one string on
// stdin, so an unlabelled join would present the assistant's own earlier
// replies as if the user had written them.
func Prompt(messages []message) string {
	var b strings.Builder
	for _, m := range messages {
		text := textOf(m.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		switch m.Role {
		case "system":
			b.WriteString("[System]\n")
		case "assistant":
			b.WriteString("[Assistant]\n")
		default:
			b.WriteString("[User]\n")
		}
		b.WriteString(text)
	}
	if b.Len() == 0 {
		// An empty stdin makes some CLIs wait rather than answer.
		return "(empty)"
	}
	return b.String()
}

// textOf reads the text of a message whose content is either a plain string or
// the multipart array. A part that is not text — an image, a file — is dropped:
// a CLI taking one prompt on stdin has nowhere to put it, and inventing a
// placeholder would put the word "image" in the model's mouth.
func textOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// scanChunks reads whatever the program has produced so far and passes it on
// without waiting for a line ending. A CLI that prints a paragraph with no
// trailing newline would otherwise be buffered until it exits, which is the
// difference between a stream and a pause.
func scanChunks(r io.Reader, out io.Writer) error {
	br := bufio.NewReader(r)
	buf := make([]byte, 4096)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
