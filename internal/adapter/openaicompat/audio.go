package openaicompat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func (a *Adapter) BuildTranscription(ctx context.Context, t *adapter.Target,
	body []byte, contentType string) (*http.Request, []ir.Warning, error) {

	url := strings.TrimRight(t.BaseURL, "/") + "/audio/transcriptions"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build transcription request: %w", err)
	}
	// The rendered form's own boundary, not a fresh one: the body and the
	// header have to agree or the provider sees no parts at all.
	hr.Header.Set("Content-Type", contentType)
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	hr.ContentLength = int64(len(body))
	// Set here rather than left to makeReplayable so a transport-level retry
	// resends the upload instead of an empty body.
	hr.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return hr, nil, nil
}

var _ adapter.Transcriber = (*Adapter)(nil)
