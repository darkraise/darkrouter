package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// maxImageBytes is far above the JSON default because a b64_json response
// carrying four 1024x1024 PNGs is several megabytes of base64, and a cap that
// rejects a legitimate response is worse than no cap at all.
const maxImageBytes = 64 << 20

func (a *Adapter) BuildImage(ctx context.Context, t *adapter.Target,
	req *ir.ImageRequest) (*http.Request, []ir.Warning, error) {

	body := map[string]any{"model": t.Model, "prompt": req.Prompt}
	// Each is omitted rather than sent empty: an explicit null or "" is a 400
	// on several of them, and n=0 asks for no images at all.
	if req.N > 0 {
		body["n"] = req.N
	}
	for k, v := range map[string]string{
		"size":            req.Size,
		"quality":         req.Quality,
		"style":           req.Style,
		"response_format": req.ResponseFormat,
		"background":      req.Background,
		"output_format":   req.OutputFormat,
		"moderation":      req.Moderation,
		"user":            req.User,
	} {
		if v != "" {
			body[k] = v
		}
	}
	if req.OutputCompression > 0 {
		body["output_compression"] = req.OutputCompression
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/images/generations"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build image request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil, nil
}

// imageEnvelope keeps usage as a pointer so an absent object stays
// distinguishable from a reported zero.
type imageEnvelope struct {
	Created int64 `json:"created"`
	Data    []struct {
		URL           string `json:"url"`
		B64           string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (a *Adapter) ParseImage(resp *http.Response) (*ir.ImageResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return nil, fmt.Errorf("read image response: %w", err)
	}
	var env imageEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse image response: %w", err)
	}
	if len(env.Data) == 0 {
		// A 200 with no images is a provider fault: the prompt was accepted and
		// nothing came back.
		return nil, errors.New("image response carried no images")
	}
	out := &ir.ImageResponse{
		Created: env.Created,
		Images:  make([]ir.Image, 0, len(env.Data)),
	}
	for _, d := range env.Data {
		out.Images = append(out.Images, ir.Image{
			URL: d.URL, Base64: d.B64, RevisedPrompt: d.RevisedPrompt,
		})
	}
	if env.Usage != nil {
		out.UsageReported = true
		out.Usage = ir.Usage{
			InputTokens:  env.Usage.InputTokens,
			OutputTokens: env.Usage.OutputTokens,
		}
	}
	return out, nil
}

var _ adapter.ImageGenerator = (*Adapter)(nil)
