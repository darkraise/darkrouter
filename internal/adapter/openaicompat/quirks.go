package openaicompat

import (
	"sort"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/catalog"
)

// quirkSet is the bare quirks a target's preset declares, as a set.
//
// The vocabulary this adapter consumes:
//
//   - max-completion-tokens-name: the cap is spelled max_completion_tokens.
//   - requires-max-tokens: a cap is mandatory; one is substituted when unset.
//   - no-system-role: system content is folded into the first user turn.
//   - no-parallel-tool-calls: the parallel_tool_calls field is not accepted.
//   - temperature-top-p-exclusive: only one of the two samplers may be sent.
//   - strict-unknown-fields: the upstream rejects fields it does not know, so
//     stream_options, reasoning_effort, metadata and parallel_tool_calls are
//     withheld.
//   - no-tool-streaming: a request carrying tools is sent unary.
//   - usage-final-chunk-only: usage arrives on the last chunk alone. This is
//     already how the stream parser works — every usage-bearing chunk becomes
//     a message_delta and the last one wins — so the quirk is accepted as
//     documentation and changes nothing here.
//   - echo-reasoning-content: an assistant turn's thinking is sent back as
//     reasoning_content, which DeepSeek's thinking mode requires inside a tool
//     loop and OpenRouter forwards to models that need it.
type quirkSet map[string]bool

func (q quirkSet) has(tag string) bool { return q[tag] }

// QuirksFor resolves a preset's bare quirks by id. Valued quirks are left out:
// none of them shapes a chat request.
func QuirksFor(preset string) quirkSet {
	if preset == "" {
		return nil
	}
	return quirksOf(catalog.Embedded()[preset])
}

func quirksOf(p catalog.Preset) quirkSet {
	if len(p.Quirks) == 0 {
		return nil
	}
	out := make(quirkSet, len(p.Quirks))
	for _, q := range p.Quirks {
		if !strings.Contains(q, "=") {
			out[q] = true
		}
	}
	return out
}

// quirksForTarget resolves the quirks of the preset serving a target.
//
// The target carries no preset id, so the upstream is identified by its base
// URL: a quirk is a fact about the server behind that address, and a provider
// an operator configured by hand against api.mistral.ai needs Mistral's quirks
// exactly as much as the preset does. Presets sharing one address are the same
// upstream, so their quirks are unioned, walked in id order so the result is
// stable.
func quirksForTarget(t *adapter.Target) quirkSet {
	if t == nil {
		return nil
	}
	if t.Preset != "" {
		return QuirksFor(t.Preset)
	}
	if t.BaseURL == "" {
		return nil
	}
	want := normalizeBase(t.BaseURL)
	presets := catalog.Embedded()
	ids := make([]string, 0, len(presets))
	for id, p := range presets {
		if p.Kind == "openaicompat" && len(p.Quirks) > 0 && normalizeBase(p.BaseURL) == want {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	out := quirkSet{}
	for _, id := range ids {
		for q := range quirksOf(presets[id]) {
			out[q] = true
		}
	}
	return out
}

func normalizeBase(u string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(u), "/"))
}
