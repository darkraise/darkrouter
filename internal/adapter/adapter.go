// Package adapter holds the outbound provider kinds.
package adapter

import (
	"context"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/ir"
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
// must behave as it did before phase 6 in that case: honor what the client
// asked for rather than acting on a half-filled guess.
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
}

// Outcome is the classification that drives failover. Phase 1 has nowhere to
// fail over to, but defining the full taxonomy now keeps Phase 3 from having to
// revisit every adapter.
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
