package ir

import "testing"

func TestEmbeddingCarriesEitherEncoding(t *testing.T) {
	// A client asking for base64 did so to avoid the decode. Holding the
	// string verbatim preserves the bytes and skips two conversions on the
	// largest payload these surfaces carry.
	f := Embedding{Index: 0, Float: []float32{0.1, 0.2}}
	if !f.IsFloat() || f.IsBase64() {
		t.Errorf("float embedding misreported: %+v", f)
	}
	b := Embedding{Index: 1, Base64: "AACAPwAAAEA="}
	if b.IsFloat() || !b.IsBase64() {
		t.Errorf("base64 embedding misreported: %+v", b)
	}
	var empty Embedding
	if empty.IsFloat() || empty.IsBase64() {
		t.Error("an empty embedding claimed an encoding")
	}
}

func TestEncodingOrDefault(t *testing.T) {
	// OpenAI's default is float when the field is absent, and a request that
	// omitted it must not be forwarded with an empty encoding_format.
	if got := (&EmbeddingRequest{}).EncodingOrDefault(); got != "float" {
		t.Errorf("default = %q, want float", got)
	}
	if got := (&EmbeddingRequest{Encoding: "base64"}).EncodingOrDefault(); got != "base64" {
		t.Errorf("explicit = %q", got)
	}
}

func TestEmbeddingRequestInputCount(t *testing.T) {
	// Logged per spec §9 as the input item count, and it is what makes a
	// batched call distinguishable from a single one in the trace.
	if got := (&EmbeddingRequest{Input: []string{"a", "b", "c"}}).InputCount(); got != 3 {
		t.Errorf("count = %d, want 3", got)
	}
	if got := (&EmbeddingRequest{}).InputCount(); got != 0 {
		t.Errorf("count = %d, want 0", got)
	}
}
