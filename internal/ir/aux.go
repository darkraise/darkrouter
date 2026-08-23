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
