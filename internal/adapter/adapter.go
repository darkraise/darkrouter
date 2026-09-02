// Package adapter holds the outbound provider kinds.
package adapter

import (
	"context"
	"io"
	"iter"
	"net/http"
	"net/url"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

// ModelInfo is what the catalog knows about the model this target names.
//
// It is a plain struct rather than a catalog type on purpose. health imports
// adapter, and catalog imports health so discovery can cool a credential on a
// rejected probe; an adapter that imported catalog would close the cycle. The
// translation happens at the exec boundary, exactly as Target already carries a
// base URL rather than importing provider.
//
// The zero value means the catalog knows nothing, and every adapter reading it
// must then honor what the client asked for rather than act on a half-filled
// guess.
type ModelInfo struct {
	ContextWindow   int
	MaxOutputTokens int

	// The three per-generation request-shape facts. TraitsKnown gates all
	// three: an unrecognized or proxied model reaches here with TraitsKnown
	// false, which is the honest answer.
	Adaptive     bool
	ManualBudget bool
	FreeSampling bool
	TraitsKnown  bool

	// The request shapes a generation refuses outright. The adapter reshapes
	// and warns rather than forwarding a guaranteed 400.
	NoPrefill          bool
	ThinkingAlwaysOn   bool
	NoForcedToolChoice bool
}

type Target struct {
	BaseURL string
	APIKey  string
	Model   string
	Info    ModelInfo

	// RerankPath is the preset-declared Cohere-v2 path, spec §3.1, resolved by
	// the executor because the adapter is handed a target and knows nothing
	// about presets. Empty for a provider with no preset, which is a
	// misconfiguration for this surface rather than a licence to guess a URL.
	RerankPath string

	// Region, Project and Location are endpoint properties rather than parts
	// of the model identifier: what carries a geo prefix on Bedrock is the
	// inference profile, not the region, and Vertex's project and location
	// live on the provider row.
	Region   string
	Project  string
	Location string

	// Publisher selects Vertex's request builder. Empty for every other kind.
	Publisher string
}

// Outcome is the classification that drives failover. The full taxonomy is
// defined here so that adding a failover destination never means revisiting
// every adapter.
type Outcome string

const (
	OutcomeSuccess             Outcome = "success"
	OutcomeRetryableProvider   Outcome = "retryable_provider"
	OutcomeRetryableCredential Outcome = "retryable_credential"
	OutcomeRetryableModel      Outcome = "retryable_model"
	OutcomeFatal               Outcome = "fatal"
	OutcomeClientCancelled     Outcome = "client_cancelled"
)

type Adapter interface {
	Kind() string
	// BuildRequest returns the rendered HTTP request and every IR field this
	// kind could not express. Master design §5: a dropped field is a fact the
	// trace view must be able to show.
	BuildRequest(ctx context.Context, t *Target, req *ir.Request) (*http.Request, []ir.Warning, error)
	// ParseResponse takes ownership of resp.Body and always closes it.
	ParseResponse(resp *http.Response) (*ir.Response, error)
	ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error]
	Classify(resp *http.Response, err error) Outcome
}

// BodyClassifier refines Classify for the one case a status line cannot
// express: a 400 that means "I do not have that model". An adapter implements
// it only when its upstreams distinguish the two, and exec type-asserts.
type BodyClassifier interface {
	ClassifyBody(resp *http.Response, body []byte, err error) Outcome
}

// SurfaceSet is the set of surfaces an adapter implements.
type SurfaceSet map[ir.Surface]bool

// Has is nil-safe, because the zero value has to be usable — a map field is
// easy to leave unset and a panic on the routing path is not an acceptable way
// to find out.
func (s SurfaceSet) Has(x ir.Surface) bool { return s[x] }

// SurfaceProvider is implemented by an adapter serving more than chat.
//
// Optional rather than a method on Adapter, matching BodyClassifier and
// TokenCounter above: an adapter that says nothing serves llm only, which is
// the honest default and keeps a kind whose auxiliary support arrives in a
// later phase compiling untouched.
type SurfaceProvider interface {
	Surfaces() SurfaceSet
}

// SurfacesOf reports what an adapter implements.
//
// The default is llm alone rather than everything. Master design §5.1 makes an
// unimplemented surface a routing filter, not a runtime error — an operator
// reading "no provider offers this model on this surface" learns more than one
// reading a 404 the provider produced.
func SurfacesOf(a Adapter) SurfaceSet {
	if sp, ok := a.(SurfaceProvider); ok {
		return sp.Surfaces()
	}
	return SurfaceSet{ir.SurfaceLLM: true}
}

// TokenCounter is implemented by an adapter whose upstream offers a native
// token count. OpenAI has no such endpoint, so this is optional rather than a
// method on Adapter that two thirds of implementations would stub out.
type TokenCounter interface {
	BuildCountRequest(ctx context.Context, t *Target, req *ir.Request) (*http.Request, error)
	// ParseCountResponse takes ownership of resp.Body and always closes it.
	ParseCountResponse(resp *http.Response) (int, error)
}

// Embedder is implemented by an adapter serving the embedding surface. Optional
// for the same reason TokenCounter is: two of the five kinds have no embedding
// endpoint, and a method on Adapter would make them stub it out.
type Embedder interface {
	BuildEmbedding(ctx context.Context, t *Target, req *ir.EmbeddingRequest) (*http.Request, []ir.Warning, error)
	// ParseEmbedding takes ownership of resp.Body and always closes it.
	ParseEmbedding(resp *http.Response) (*ir.EmbeddingResponse, error)
}

// Moderator is implemented by an adapter serving the moderation surface.
type Moderator interface {
	BuildModeration(ctx context.Context, t *Target, req *ir.ModerationRequest) (*http.Request, []ir.Warning, error)
	// ParseModeration takes ownership of resp.Body and always closes it.
	ParseModeration(resp *http.Response) (*ir.ModerationResponse, error)
}

// Reranker is implemented by an adapter serving the rerank surface.
type Reranker interface {
	BuildRerank(ctx context.Context, t *Target, req *ir.RerankRequest) (*http.Request, []ir.Warning, error)
	// ParseRerank takes ownership of resp.Body and always closes it.
	ParseRerank(resp *http.Response) (*ir.RerankResponse, error)
}

// ImageGenerator is implemented by an adapter serving the image surface.
type ImageGenerator interface {
	BuildImage(ctx context.Context, t *Target, req *ir.ImageRequest) (*http.Request, []ir.Warning, error)
	// ParseImage takes ownership of resp.Body and always closes it.
	ParseImage(resp *http.Response) (*ir.ImageResponse, error)
}

// Transcriber is implemented by an adapter serving the stt surface.
//
// It takes rendered bytes rather than a parsed form because the form type lives
// in the executor and importing it here would be a cycle. The split is right
// regardless: the executor owns the in-form model rewrite, the adapter owns the
// URL and the credential. There is no Parse counterpart — a transcription
// response is forwarded to the client verbatim, since parsing it into an IR
// would drop the per-segment timings and log-probabilities verbose_json carries.
type Transcriber interface {
	BuildTranscription(ctx context.Context, t *Target, body []byte, contentType string) (*http.Request, []ir.Warning, error)
}

// Speaker is implemented by an adapter serving the tts surface. Like
// Transcriber it has no Parse counterpart: the response is audio, forwarded
// without being read.
type Speaker interface {
	BuildSpeech(ctx context.Context, t *Target, req *ir.SpeechRequest) (*http.Request, []ir.Warning, error)
}

// Forward is one passthrough attempt's outbound request, already rewritten.
//
// Body is final: the executor has done the model rewrite and any permitted
// injection, and a builder that re-encodes it defeats the phase. Header is the
// inbound allowlist from spec §5.3, filtered before it arrives, and a builder
// overrides only what the client must not be able to dictate — the credential
// and the content type.
type Forward struct {
	Body   []byte
	Header http.Header
	Stream bool

	// Method and Query serve the kinds whose model lives in the URL. Method is
	// the operation suffix; Query is the inbound query with the inbound
	// credential already removed. Both are empty for a body-carried kind.
	Method string
	Query  url.Values
}

// RawEvent is what one forwarded SSE event means to the commit rule and to
// accounting.
//
// Deliberately not an ir.StreamEvent: the fast path never reconstructs IR, and
// a type that could be mistaken for one would invite a future change to start.
type RawEvent struct {
	// Content marks a content-bearing event — text, thinking, or tool-input
	// content. Pings, comments, message_start and role-only deltas are not,
	// because committing on a keepalive forfeits failover for nothing.
	Content bool
	// ErrPayload is a non-empty in-stream error, whatever the status line said.
	// Anthropic delivers overloaded_error this way under a 200.
	ErrPayload string
	// Usage is what this event alone reported. Anthropic splits it across two
	// events, so the caller merges rather than assigns.
	Usage *ir.Usage
	// UsageOnly marks the extra final chunk an injected stream_options
	// produced, which is stripped when Darkrouter asked for it and the client
	// did not.
	UsageOnly bool
}

// Forwarder is implemented by an adapter whose wire format is close enough to
// an inbound dialect that a body can be forwarded rather than re-rendered.
//
// Optional, like TokenCounter and Embedder above, and for a stronger reason:
// master design §4.1 excludes bedrock because SigV4 signs a payload hash, and
// vertex because its URL encodes both publisher and model. Neither implements
// this interface, so neither can be made eligible by an oversight in a
// predicate somewhere else, and a sixth kind is ineligible until someone
// deliberately writes its builder.
//
// The two Recognize methods live here rather than in the executor because the
// usage wire shape already does. A second copy in exec would be the same field
// sets maintained twice, drifting the first time a vendor adds a category.
type Forwarder interface {
	BuildForward(ctx context.Context, t *Target, f *Forward) (*http.Request, error)
	// RecognizeEvent reads SSE structure only and never builds IR.
	RecognizeEvent(ev sse.Event) RawEvent
	// RecognizeUsage reads a complete unary body. A nil return means the body
	// carried no usage, which is logged as unknown rather than estimated.
	RecognizeUsage(body []byte) *ir.Usage
}
