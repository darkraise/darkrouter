// Package gemini speaks Google's generateContent wire format to an upstream.
package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/ir"
)

// targetName labels the warnings this kind produces.
const targetName = "gemini"

// DefaultMaxInlineBytes bounds what one URL may contribute to a request.
const DefaultMaxInlineBytes int64 = 20 << 20

var (
	errUnsupportedScheme = errors.New("gemini: only http and https URLs are inlined")
	errFetchStatus       = errors.New("gemini: the URL did not return 2xx")
	errTooLarge          = errors.New("gemini: the URL exceeded the inline cap")
)

// Fetcher downloads a public URL so it can be inlined as base64.
//
// Gemini's fileData.fileUri accepts Files API URIs and YouTube URLs only, so a
// public image address has to be fetched by the gateway. That makes Darkrouter
// issue requests to client-supplied addresses, and the constraints here are
// what keep that from being server-side request forgery: http and https only,
// a dial-time check that the resolved address is on the public internet, no
// redirects, a byte cap enforced on the reader rather than read off
// Content-Length, and a short timeout.
type Fetcher struct {
	Client   *http.Client
	MaxBytes int64
	// Inline gates the outbound fetch. False drops a remote URL rather than
	// retrieving it; everything that needs no request is unaffected.
	//
	// The zero value is false, so a Fetcher built as a struct literal instead
	// of via NewFetcher silently disables inlining unless this is set
	// explicitly — a fixture that meant to exercise the fetch path can end up
	// exercising the disabled-inlining path instead.
	Inline bool
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		Client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				// A redirect to an internal address is the standard bypass.
				return http.ErrUseLastResponse
			},
			Transport: guardedTransport(),
		},
		MaxBytes: DefaultMaxInlineBytes,
		Inline:   true,
	}
}

// guardedTransport refuses to connect to anything off the public internet.
// Cloned from the default so the pooling and proxy behaviour a bare transport
// would lose stays intact.
func guardedTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   guardConn,
	}).DialContext
	return t
}

// passthroughURI reports whether Gemini accepts this URI as fileData. Anything
// else has to be inlined, because the API rejects a fileUri it cannot resolve.
func passthroughURI(u string) bool {
	if strings.HasPrefix(u, "gs://") {
		return true
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return false
	}
	switch parsed.Host {
	case "generativelanguage.googleapis.com",
		"www.youtube.com", "youtube.com", "m.youtube.com", "youtu.be":
		return true
	}
	return false
}

// part renders one media block. It returns nil when the block cannot be
// expressed, always with a warning: the model can still answer about the rest
// of the prompt, and the trace records what did not arrive.
func (f *Fetcher) part(ctx context.Context, m *ir.Media, field string) (map[string]any, []ir.Warning) {
	drop := func(reason string) (map[string]any, []ir.Warning) {
		return nil, []ir.Warning{{
			Field: "messages[]." + field, Target: targetName, Reason: reason,
		}}
	}
	if m == nil {
		return drop("media block carried nothing")
	}
	switch {
	case m.Data != "":
		return map[string]any{"inlineData": map[string]any{
			"mimeType": m.MIME, "data": m.Data,
		}}, nil

	case m.FileID != "":
		return map[string]any{"fileData": map[string]any{
			"mimeType": m.MIME, "fileUri": m.FileID,
		}}, nil

	case m.URL == "":
		return drop("media block carried neither data nor a URL")

	case passthroughURI(m.URL):
		return map[string]any{"fileData": map[string]any{
			"mimeType": m.MIME, "fileUri": m.URL,
		}}, nil
	}

	if !f.Inline {
		return drop("media inlining is disabled")
	}
	mime, data, err := f.inline(ctx, m.URL)
	if err != nil {
		return drop("could not inline the URL: " + err.Error())
	}
	if m.MIME != "" {
		mime = m.MIME
	}
	return map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": data}}, nil
}

func (f *Fetcher) inline(ctx context.Context, raw string) (mime, data string, err error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", errUnsupportedScheme
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", errFetchStatus
	}

	// Read one byte past the cap so an oversized body is detected rather than
	// silently truncated into a corrupt image.
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.MaxBytes+1))
	if err != nil {
		return "", "", err
	}
	if int64(len(body)) > f.MaxBytes {
		return "", "", errTooLarge
	}

	mime = resp.Header.Get("Content-Type")
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return mime, base64.StdEncoding.EncodeToString(body), nil
}
