package catalog

import "testing"

func TestNormalizeModelID(t *testing.T) {
	cases := []struct{ in, want string }{
		// Ollama's tag separator.
		{"llama3.3:70b", "llama3.3-70b"},
		// Fireworks' account path prefix.
		{"accounts/fireworks/models/llama-v3p3-70b", "llama-v3p3-70b"},
		// OpenRouter's vendor prefix.
		{"meta-llama/Llama-3.3-70B-Instruct", "llama-3.3-70b-instruct"},
		// Case only.
		{"GPT-4O-Mini", "gpt-4o-mini"},
		// Already normal.
		{"claude-opus-4-5", "claude-opus-4-5"},
		// A deep path keeps only the leaf.
		{"a/b/c/d", "d"},
		// Surrounding whitespace from a hand-edited config.
		{"  gpt-4o  ", "gpt-4o"},
		// Empty stays empty rather than becoming a match-anything key.
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeModelID(c.in); got != c.want {
			t.Errorf("NormalizeModelID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func joinDoc() Doc {
	return Doc{"togetherai": {
		"llama-3.3-70b-instruct": {ContextWindow: 131072, PriceKnown: true, InputMicrosPerMTok: 880_000},
	}}
}

func TestJoinFindsTheExactID(t *testing.T) {
	p := Preset{ModelsDevID: "togetherai"}
	m, ok := Join(p, joinDoc(), "llama-3.3-70b-instruct")
	if !ok || m.ContextWindow != 131072 {
		t.Fatalf("exact join failed: %+v %v", m, ok)
	}
}

func TestJoinNormalizesBeforeMatching(t *testing.T) {
	p := Preset{ModelsDevID: "togetherai"}
	for _, id := range []string{
		"meta-llama/Llama-3.3-70B-Instruct",
		"Llama-3.3-70B-Instruct",
		"llama-3.3:70b-instruct",
	} {
		if _, ok := Join(p, joinDoc(), id); !ok {
			t.Errorf("%q did not join", id)
		}
	}
}

func TestJoinUsesAnExplicitAlias(t *testing.T) {
	// The alias is the escape hatch for the forms normalization cannot reach:
	// Fireworks spells the same family llama-v3p3-70b, which shares no
	// normalized form with llama-3.3-70b.
	p := Preset{
		ModelsDevID:  "togetherai",
		ModelAliases: map[string]string{"accounts/fireworks/models/llama-v3p3-70b": "llama-3.3-70b-instruct"},
	}
	m, ok := Join(p, joinDoc(), "accounts/fireworks/models/llama-v3p3-70b")
	if !ok || m.ContextWindow != 131072 {
		t.Fatalf("alias join failed: %+v %v", m, ok)
	}
}

func TestJoinMissIsNotAnError(t *testing.T) {
	// Spec §4.1: a model that fails to join is not an error — it carries
	// inferred capabilities. The caller needs the miss reported, not a zero
	// value that looks like a zero-cost, zero-context model.
	p := Preset{ModelsDevID: "togetherai"}
	if _, ok := Join(p, joinDoc(), "some-private-finetune"); ok {
		t.Error("an unknown model reported a join")
	}
}

func TestJoinSkipsExemptPresets(t *testing.T) {
	// An exempt preset has no join key. Falling through to a normalized lookup
	// against the empty string would match whatever the document keys as "".
	p := Preset{NoModelsDev: true}
	if _, ok := Join(p, joinDoc(), "llama-3.3-70b-instruct"); ok {
		t.Error("an exempt preset joined anyway")
	}
}
