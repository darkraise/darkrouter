package ir

// The auxiliary surfaces' request and response types.
//
// Each is deliberately narrow. Forcing a six-field embedding call through the
// content-block message model would obscure both shapes and buy nothing, so
// these do not reuse Request or Response — only Usage, which every surface that
// reports tokens reports in the same units.

// EmbeddingRequest is one batched embedding call.
type EmbeddingRequest struct {
	Model string
	// Input is the text form, always a slice. OpenAI accepts a bare string or
	// an array of strings and the edge flattens both here, so nothing
	// downstream branches on the inbound shape.
	Input []string
	// Tokens is the pre-tokenized form. OpenAI also accepts an array of
	// integers or an array of integer arrays, and those cannot be folded into
	// Input: Darkrouter has no detokenizer, and rendering token ids as text
	// would send a different request from the one the client made. Exactly one
	// of Input and Tokens is ever populated.
	Tokens [][]int
	// Encoding is "float" or "base64". Empty means the client did not say.
	Encoding string
	// Dimensions is 0 when unset. Zero is not a legal value, so it needs no
	// separate presence flag.
	Dimensions int
	User       string
}

// EncodingOrDefault is the encoding to send upstream. OpenAI's default when the
// field is absent is float, and forwarding an empty encoding_format would be a
// different request from the one the client made.
func (r *EmbeddingRequest) EncodingOrDefault() string {
	if r.Encoding == "" {
		return "float"
	}
	return r.Encoding
}

// InputCount is the batched item count, recorded on the request row per spec §9.
// It counts whichever form the client sent, because the row records how many
// items were embedded rather than which encoding carried them.
func (r *EmbeddingRequest) InputCount() int {
	if len(r.Tokens) > 0 {
		return len(r.Tokens)
	}
	return len(r.Input)
}

// Embedding is one vector. Exactly one of Float and Base64 is populated.
//
// A client that asked for base64 did so to avoid the decode, so the string is
// carried verbatim rather than decoded to floats and re-encoded on the way out:
// that preserves the bytes exactly and skips two conversions on the largest
// payload any auxiliary surface carries.
type Embedding struct {
	Index  int
	Float  []float32
	Base64 string
}

func (e Embedding) IsFloat() bool  { return len(e.Float) > 0 }
func (e Embedding) IsBase64() bool { return e.Base64 != "" }

type EmbeddingResponse struct {
	// Model is what the provider actually served, which may differ from what
	// was asked for. Spec §8: a failover to a different model returns vectors
	// from a different vector space, so the served name is not decoration.
	Model      string
	Embeddings []Embedding
	Usage      Usage
}

// ModerationRequest is one moderation call. Input is always a slice: OpenAI
// accepts a bare string or an array of strings and the edge flattens both.
type ModerationRequest struct {
	Model string
	Input []string
}

// InputCount is the batched item count, recorded on the request row per spec §9.
func (r *ModerationRequest) InputCount() int { return len(r.Input) }

// ModerationResult is one verdict.
//
// Categories and Scores are maps rather than a fixed field set because the
// category list is provider-defined and has grown repeatedly. A struct would
// silently drop every category added after it was written, and a dropped
// category on a moderation endpoint is a safety signal the client never sees.
type ModerationResult struct {
	Flagged    bool
	Categories map[string]bool
	Scores     map[string]float64
	// AppliedInputTypes is omni-moderation's per-category record of which
	// input modality triggered it. Dropping it would leave a client unable to
	// tell a flagged image from flagged text.
	AppliedInputTypes map[string][]string
}

type ModerationResponse struct {
	ID    string
	Model string
	// Results is parallel to the request's Input, one verdict per item.
	Results []ModerationResult
	// Usage is zero for every known provider: the endpoint reports none. It is
	// carried anyway so a provider that starts reporting is recorded rather
	// than discarded.
	Usage Usage
}

// RerankRequest is one Cohere-v2 rerank call. OpenAI defines no rerank
// endpoint, so this shape is both the inbound contract and the outbound one.
type RerankRequest struct {
	Model string
	Query string
	// Documents is always text. Cohere v2 accepts document objects too; the
	// edge takes their text field and warns about the rest, because a document
	// reranked on its text alone is not something the response reveals.
	Documents       []string
	TopN            int // 0 when unset; zero is not a legal value
	ReturnDocuments bool
	// Warnings are what the inbound parse could not express. They ride on the
	// request because the edge, not the adapter, is where the loss happens.
	Warnings []Warning
}

// DocumentCount is recorded on the request row per spec §9.
func (r *RerankRequest) DocumentCount() int { return len(r.Documents) }

type RerankResult struct {
	Index          int
	RelevanceScore float64
	// Document is populated only when the client set return_documents.
	Document string
}

type RerankResponse struct {
	ID      string
	Model   string
	Results []RerankResult
	// Usage is zero: Cohere bills rerank in search units, not tokens.
	Usage Usage
}

// ImageRequest is one generation call.
//
// ResponseFormat is carried and forwarded verbatim although gpt-image-1 rejects
// it and the dall-e models require it. A client sending it to gpt-image-1 gets
// the same 400 talking to the provider directly, and translating it here would
// make Darkrouter behave differently from the upstream for no gain.
type ImageRequest struct {
	Model  string
	Prompt string
	// N is 0 when unset. Zero images is not a request anyone makes, so it needs
	// no separate presence flag.
	N              int
	Size           string
	Quality        string
	Style          string
	ResponseFormat string
	Background     string
	OutputFormat   string
	// Moderation and OutputCompression are gpt-image-1 parameters. They are
	// carried rather than dropped because a client that set them gets
	// different images without them, and nothing in the response would say so.
	Moderation        string
	OutputCompression int
	User              string
}

// ImageCount is what was asked for, recorded on the request row per spec §9.
// An unset n means one image, which is OpenAI's own default.
func (r *ImageRequest) ImageCount() int {
	if r.N <= 0 {
		return 1
	}
	return r.N
}

// Image is one generated image. Exactly one of URL and Base64 is populated,
// chosen by the provider rather than by the request: gpt-image-1 always returns
// base64 whatever response_format said.
type Image struct {
	URL    string
	Base64 string
	// RevisedPrompt is what the provider actually generated from, when it says.
	RevisedPrompt string
}

type ImageResponse struct {
	Created int64
	Model   string
	Images  []Image

	Usage Usage
	// UsageReported distinguishes "the provider reported zero" from "the
	// provider reported nothing". gpt-image-1 returns a usage object; the
	// dall-e models return none, and recording their calls as zero-cost would
	// be a confident lie rather than a missing value.
	UsageReported bool
}
