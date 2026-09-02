// Package vertex renders the IR to Google Vertex AI.
//
// One adapter kind, two request builders, selected by the publisher recorded on
// the catalog entry — master design §6.2. An earlier draft claimed the Gemini
// payload covers both and that only transport and auth differ; that is false
// for exactly the models that justify supporting two URL forms, and following
// it 400s on every Claude call.
//
// Neither payload is reimplemented here. The Google half hands phase 4's Gemini
// builder a base URL that already ends in the publisher segment; the Anthropic
// half calls phase 4's Anthropic builder and rewrites what Vertex moves.
package vertex

import (
	"context"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/ir"
)

const (
	PublisherGoogle    = "publishers/google"
	PublisherAnthropic = "publishers/anthropic"

	// AnthropicVersion is mandatory in the body on the rawPredict route, and
	// is a different value from the anthropic-version header the direct API
	// takes.
	AnthropicVersion = "vertex-2023-10-16"
)

type Adapter struct {
	// media renders the Google publisher's payload. Its only state is the
	// media fetcher, which is safe for concurrent use and expensive enough not
	// to rebuild per request.
	//
	// Injectable because the fetcher is the operator's decision: media.inline
	// governs whether the gateway fetches an image URL on a client's behalf,
	// and a Vertex adapter holding a fetcher of its own honoured that setting
	// on the direct Gemini route while ignoring it here. The golden suite
	// needs the same seam to keep its promise that no fixture reaches the
	// network.
	media *geminiadapter.Adapter
}

func New() *Adapter { return &Adapter{} }

// NewWithFetcher builds the adapter against a media fetcher of the caller's.
func NewWithFetcher(f *geminiadapter.Fetcher) *Adapter {
	return &Adapter{media: geminiadapter.NewWithFetcher(f)}
}

// gemini is the renderer for the Google publisher, defaulted on first use so
// that a zero Adapter — which every caller of New() holds — still works.
func (a *Adapter) gemini() *geminiadapter.Adapter {
	if a.media == nil {
		return defaultGemini
	}
	return a.media
}

var defaultGemini = geminiadapter.New()

func (a *Adapter) Kind() string { return "vertex" }

// Surfaces is llm and embedding. The Google publisher serves text embeddings
// on the :predict route; the Anthropic publisher has no embedding model and
// refuses the surface at build time.
func (a *Adapter) Surfaces() adapter.SurfaceSet {
	return adapter.SurfaceSet{ir.SurfaceLLM: true, ir.SurfaceEmbedding: true}
}

// EndpointFor builds the regional host and project path. Location appears
// twice: once in the hostname and once in the path, which is Vertex's shape and
// not a mistake. The global endpoint is the exception: its host carries no
// location prefix, and only the path names it.
func EndpointFor(project, location string) string {
	host := location + "-aiplatform.googleapis.com"
	if location == "global" {
		host = "aiplatform.googleapis.com"
	}
	return fmt.Sprintf("https://%s/v1/projects/%s/locations/%s", host, project, location)
}

// baseFor is EndpointFor unless the provider row names a host of its own.
//
// An explicit base URL wins for the same reason it does for bedrock: a private
// service endpoint, or a test server, has to be reachable. Without this the
// adapter could only ever talk to googleapis.com, which also makes it
// untestable above the unit level.
func baseFor(t *adapter.Target) string {
	if b := strings.TrimRight(t.BaseURL, "/"); b != "" {
		return b
	}
	return EndpointFor(t.Project, t.Location)
}

func publisherOf(t *adapter.Target) string {
	if t.Publisher == "" {
		// A catalog row seeded before publisher was populated. Google is the
		// safe default: it is what the vertex preset declares.
		return PublisherGoogle
	}
	return t.Publisher
}

func (a *Adapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	if t.Project == "" || t.Location == "" {
		return nil, nil, fmt.Errorf("vertex target needs a project and a location")
	}
	switch publisherOf(t) {
	case PublisherGoogle:
		return a.buildGoogle(ctx, t, req)
	case PublisherAnthropic:
		return buildAnthropic(ctx, t, req)
	}
	// Llama and Mistral MaaS use endpoints/openapi/chat/completions and are
	// out of scope for v1. Guessing one of the two implemented shapes would
	// 400 with a message about the wrong payload, which is worse than saying so.
	return nil, nil, fmt.Errorf("vertex publisher %q is not supported", t.Publisher)
}

// ParseResponse dispatches on the publisher named by the request the response
// answers, falling back to the payload's shape when no request is attached.
// ParseStream is handed a bare reader and dispatches on the stream's first
// bytes.
func (a *Adapter) ParseResponse(resp *http.Response) (*ir.Response, error) {
	return parseResponse(resp)
}

func (a *Adapter) ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return parseStream(r, maxLine)
}

func (a *Adapter) Classify(resp *http.Response, err error) adapter.Outcome {
	return adapter.ClassifyStatus(resp, err)
}

var _ adapter.Adapter = (*Adapter)(nil)
