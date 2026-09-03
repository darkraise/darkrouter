package main

import (
	"slices"
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// OmniRoute wins a contested structural field: its transcription has been
// reviewed across nine phases, 9router's has not.
func TestOmniRouteWinsAContestedField(t *testing.T) {
	omni := []entry{{id: "groq", baseURL: "https://api.groq.com/openai/v1"}}
	nine := []nineEntry{{ID: "groq", Transport: nineTransport{BaseURL: "https://groq.example/v1"}}}

	got := mergeSources(omni, map[string]displayEntry{}, nine)
	if got.Presets["groq"].BaseURL != "https://api.groq.com/openai/v1" {
		t.Errorf("BaseURL = %q, want OmniRoute's", got.Presets["groq"].BaseURL)
	}
	if !hasOrigin(got, "groq", "base_url", "omniroute") {
		t.Errorf("origins = %v, want base_url from omniroute", got.Origins["groq"])
	}
}

// A field OmniRoute does not carry is taken from 9router rather than dropped.
func TestNineRouterFillsAFieldOmniRouteLacks(t *testing.T) {
	omni := []entry{{id: "groq", baseURL: "https://api.groq.com/openai/v1"}}
	nine := []nineEntry{{ID: "groq", Display: nineDisplay{
		Notice: nineNotice{APIKeyURL: "https://console.groq.com/keys"}}}}

	got := mergeSources(omni, map[string]displayEntry{}, nine)
	if got.Presets["groq"].APIKeyURL != "https://console.groq.com/keys" {
		t.Errorf("APIKeyURL = %q, want 9router's", got.Presets["groq"].APIKeyURL)
	}
	if !hasOrigin(got, "groq", "api_key_url", "9router") {
		t.Errorf("origins = %v, want api_key_url from 9router", got.Origins["groq"])
	}
}

// A provider only 9router knows is ingested outright.
func TestNineRouterOnlyProviderIsAdded(t *testing.T) {
	nine := []nineEntry{{
		ID:        "kimchi",
		Display:   nineDisplay{Name: "Kimchi", Website: "https://kimchi.example"},
		AuthType:  "apikey",
		Transport: nineTransport{BaseURL: "https://api.kimchi.example/v1/chat/completions"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	p, ok := got.Presets["kimchi"]
	if !ok {
		t.Fatal("kimchi absent from the merge")
	}
	if p.BaseURL != "https://api.kimchi.example/v1" {
		t.Errorf("BaseURL = %q, want the trimmed root", p.BaseURL)
	}
	if p.Name != "Kimchi" {
		t.Errorf("Name = %q", p.Name)
	}
}

// Phase A ingests llm and embedding only.
func TestNonLLMProviderIsSkipped(t *testing.T) {
	nine := []nineEntry{{
		ID:           "elevenlabs",
		ServiceKinds: []string{"tts"},
		Transport:    nineTransport{BaseURL: "https://api.elevenlabs.io/v1/text-to-speech"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if _, ok := got.Presets["elevenlabs"]; ok {
		t.Error("a tts-only provider was ingested; phase C owns those")
	}
}

func TestEmbeddingProviderIsIngested(t *testing.T) {
	nine := []nineEntry{{
		ID:           "voyage-ai",
		ServiceKinds: []string{"embedding"},
		Transport:    nineTransport{BaseURL: "https://api.voyageai.com/v1/embeddings"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if _, ok := got.Presets["voyage-ai"]; !ok {
		t.Error("an embedding provider was skipped")
	}
}

func hasOrigin(m merged, id, field, source string) bool {
	for _, o := range m.Origins[id] {
		if o.Field == field && o.Source == source {
			return true
		}
	}
	return false
}

var _ = catalog.Preset{}

// A 9router-only entry whose baseUrl lives in a per-surface config block this
// phase does not read would otherwise ship with no base URL at all.
func TestNineRouterEntryWithoutBaseURLIsSkipped(t *testing.T) {
	nine := []nineEntry{{ID: "voyage-ai", AuthType: "apikey"}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if _, ok := got.Presets["voyage-ai"]; ok {
		t.Errorf("emitted a preset with base_url %q; an untranscribable entry must be skipped",
			got.Presets["voyage-ai"].BaseURL)
	}
}

// A published transport.auth header beats the authType category, which names a
// credential kind rather than a wire header.
func TestNineRouterAuthHeaderPicksTheStyle(t *testing.T) {
	for _, tc := range []struct {
		name string
		auth nineAuth
		want string
	}{
		{"flat", nineAuth{Header: "x-api-key"}, "x-api-key"},
		{"nested", nineAuth{APIKey: nineAuthKind{Header: "x-api-key"}}, "x-api-key"},
		{"nested wins over flat", nineAuth{
			Header: "Authorization", APIKey: nineAuthKind{Header: "x-api-key"},
		}, "x-api-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nine := []nineEntry{{
				ID:       "keyheader",
				AuthType: "apikey",
				Transport: nineTransport{
					BaseURL: "https://api.keyheader.example/v1", Auth: tc.auth,
				},
			}}
			got := mergeSources(nil, map[string]displayEntry{}, nine)
			if got.Presets["keyheader"].Auth.Style != tc.want {
				t.Errorf("Auth.Style = %q, want %q",
					got.Presets["keyheader"].Auth.Style, tc.want)
			}
		})
	}
}

// A header darkrouter has no style for must drop the entry: shipping it under
// a header the upstream never reads produces a preset that authenticates
// nowhere, and the closed vocabulary has no member for it.
func TestNineRouterUnknownAuthHeaderIsSkipped(t *testing.T) {
	nine := []nineEntry{{
		ID:       "goog",
		AuthType: "apikey",
		Transport: nineTransport{
			BaseURL: "https://api.goog.example/v1",
			Auth:    nineAuth{APIKey: nineAuthKind{Header: "x-goog-api-key"}},
		},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if p, ok := got.Presets["goog"]; ok {
		t.Errorf("emitted style %q for an unmappable header", p.Auth.Style)
	}
}

// An OAuth preset needs an oauth block this phase does not generate, and
// "cookie" is not in darkrouter's closed vocabulary at all.
func TestNineRouterUnmappableAuthIsSkipped(t *testing.T) {
	for _, authType := range []string{"oauth", "cookie"} {
		nine := []nineEntry{{
			ID:        "unmappable",
			AuthType:  authType,
			Transport: nineTransport{BaseURL: "https://api.unmappable.example/v1"},
		}}
		got := mergeSources(nil, map[string]displayEntry{}, nine)
		if p, ok := got.Presets["unmappable"]; ok {
			t.Errorf("authType %q was ingested as style %q; it maps to no darkrouter style",
				authType, p.Auth.Style)
		}
	}
}

// The ordinary case: no transport.auth and no authType is a bearer provider.
func TestNineRouterDefaultsToBearer(t *testing.T) {
	nine := []nineEntry{{
		ID:        "plain",
		Transport: nineTransport{BaseURL: "https://api.plain.example/v1"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if got.Presets["plain"].Auth.Style != "bearer" {
		t.Errorf("Auth.Style = %q, want bearer", got.Presets["plain"].Auth.Style)
	}
}

// A dual-surface provider serves both, and preset surfaces beat discovered
// rows -- so dropping llm here would silently unroute its chat models.
func TestNineRouterDualSurfaceKeepsBoth(t *testing.T) {
	nine := []nineEntry{{
		ID:           "bothkinds",
		ServiceKinds: []string{"llm", "embedding"},
		Transport:    nineTransport{BaseURL: "https://api.bothkinds.example/v1"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	want := []string{"llm", "embedding"}
	if diff := got.Presets["bothkinds"].Surfaces; !slices.Equal(diff, want) {
		t.Errorf("Surfaces = %v, want %v", diff, want)
	}
}

func TestNineRouterEmbeddingOnlySurface(t *testing.T) {
	nine := []nineEntry{{
		ID:           "embedonly",
		ServiceKinds: []string{"embedding"},
		Transport:    nineTransport{BaseURL: "https://api.embedonly.example/v1"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if diff := got.Presets["embedonly"].Surfaces; !slices.Equal(diff, []string{"embedding"}) {
		t.Errorf("Surfaces = %v, want [embedding]", diff)
	}
}

// A skipped entry must leave no trace at all: an origin recorded before the
// skip check would claim provenance for a preset that does not exist.
func TestSkippedEntryRecordsNoOrigin(t *testing.T) {
	nine := []nineEntry{
		{ID: "nobase", AuthType: "apikey"},
		{ID: "noauth", AuthType: "oauth", Transport: nineTransport{BaseURL: "https://api.noauth.example/v1"}},
	}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	for _, id := range []string{"nobase", "noauth"} {
		if o, ok := got.Origins[id]; ok {
			t.Errorf("%s was skipped but recorded origins %v", id, o)
		}
	}
}

// The hazard the spec names: two base URLs that differ only in the endpoint
// path agree after trimming. Comparing trimmed values would call that
// resolved and ship a wrong root silently -- these trim to the same
// "https://api.example.com/v1" and must still produce a conflict.
func TestConflictIsDetectedOnRawValues(t *testing.T) {
	omni := []entry{{id: "p", baseURL: "https://api.example.com/v1/chat/completions"}}
	nine := []nineEntry{{ID: "p", Transport: nineTransport{BaseURL: "https://api.example.com/v1/messages"}}}

	got := mergeSources(omni, map[string]displayEntry{}, nine)
	if len(got.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1: %v", len(got.Conflicts), got.Conflicts)
	}
	c := got.Conflicts[0]
	if c.Field != "base_url" || c.Winner != "omniroute" {
		t.Errorf("conflict = %+v", c)
	}
	if c.LoserValue != "https://api.example.com/v1/messages" {
		t.Errorf("LoserValue = %q, want the raw upstream value", c.LoserValue)
	}
}

// The simpler shape, kept alongside the trim-agreement case above: a base URL
// that differs even before trimming must also be reported.
func TestConflictDetectedWhenBothRawAndTrimmedDiffer(t *testing.T) {
	omni := []entry{{id: "p", baseURL: "https://api.example.com/v1/chat/completions"}}
	nine := []nineEntry{{ID: "p", Transport: nineTransport{BaseURL: "https://api.example.com/v2/messages"}}}

	got := mergeSources(omni, map[string]displayEntry{}, nine)
	if len(got.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1: %v", len(got.Conflicts), got.Conflicts)
	}
	if c := got.Conflicts[0]; c.LoserValue != "https://api.example.com/v2/messages" {
		t.Errorf("LoserValue = %q, want the raw upstream value", c.LoserValue)
	}
}

// A 9router entry that would fail toPreset on its own (unmappable auth) and
// that OmniRoute never listed still surfaces its declared quirk: the review
// trail is about what 9router declared, not about what phase A managed to
// ingest. This exercises the `!ok` branch specifically -- an id already in
// omni would record the same conflict count via contestedFields instead, and
// would not actually prove this branch runs.
func TestConflictRecordedEvenWhenEntryFailsToPreset(t *testing.T) {
	nine := []nineEntry{{
		ID:       "unmappable",
		AuthType: "oauth",
		Transport: nineTransport{
			BaseURL: "https://api.example.com/v1/messages",
			Quirks:  map[string]bool{"dropClientMetadata": true},
		},
	}}

	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if len(got.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1: %v", len(got.Conflicts), got.Conflicts)
	}
	if _, ok := got.Presets["unmappable"]; ok {
		t.Error("an entry with unmappable auth must not produce a preset")
	}
}

// A quirk declared on a provider new to darkrouter must still reach the
// review trail: the closed vocabulary bars it from Preset.Quirks, but that is
// not a reason to drop it from the artifact too.
func TestNewProviderQuirkIsReportedNotApplied(t *testing.T) {
	nine := []nineEntry{{ID: "freshquirk", Transport: nineTransport{
		BaseURL: "https://api.freshquirk.example/v1",
		Quirks:  map[string]bool{"dropClientMetadata": true},
	}}}

	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if len(got.Presets["freshquirk"].Quirks) != 0 {
		t.Errorf("Quirks = %v, want none applied", got.Presets["freshquirk"].Quirks)
	}
	var found bool
	for _, c := range got.Conflicts {
		if c.Field == "quirk:dropClientMetadata" {
			found = true
		}
	}
	if !found {
		t.Errorf("conflicts = %v, want the unmapped quirk reported", got.Conflicts)
	}
}

func TestAgreeingSourcesProduceNoConflict(t *testing.T) {
	omni := []entry{{id: "p", baseURL: "https://api.example.com/v1/chat/completions"}}
	nine := []nineEntry{{ID: "p", Transport: nineTransport{BaseURL: "https://api.example.com/v1/chat/completions"}}}

	if got := mergeSources(omni, map[string]displayEntry{}, nine); len(got.Conflicts) != 0 {
		t.Errorf("got %v, want no conflicts", got.Conflicts)
	}
}

// A quirk 9router declares that darkrouter's closed vocabulary has no name for
// must reach the reviewer and never reach Preset.Quirks.
func TestUnmappedQuirkIsReportedNotApplied(t *testing.T) {
	omni := []entry{{id: "p", baseURL: "https://api.example.com/v1"}}
	nine := []nineEntry{{ID: "p", Transport: nineTransport{
		BaseURL: "https://api.example.com/v1",
		Quirks:  map[string]bool{"dropClientMetadata": true},
	}}}

	got := mergeSources(omni, map[string]displayEntry{}, nine)
	if len(got.Presets["p"].Quirks) != 0 {
		t.Errorf("Quirks = %v, want none applied", got.Presets["p"].Quirks)
	}
	var found bool
	for _, c := range got.Conflicts {
		if c.Field == "quirk:dropClientMetadata" {
			found = true
		}
	}
	if !found {
		t.Errorf("conflicts = %v, want the unmapped quirk reported", got.Conflicts)
	}
}

// For a provider OmniRoute never listed, naming OmniRoute as the winner of a
// quirk it never saw asserts something untrue in the review table.
func TestNineRouterOnlyQuirkWinnerIsNotOmniRoute(t *testing.T) {
	nine := []nineEntry{{ID: "kimchi", Transport: nineTransport{
		BaseURL: "https://api.kimchi.example/v1",
		Quirks:  map[string]bool{"dropClientMetadata": true},
	}}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	var found bool
	for _, c := range got.Conflicts {
		if c.Field == "quirk:dropClientMetadata" {
			found = true
			if c.Winner == srcOmni {
				t.Errorf("Winner = %q, OmniRoute never listed kimchi", c.Winner)
			}
		}
	}
	if !found {
		t.Fatal("expected a quirk conflict for kimchi")
	}
}

// A custom scheme must be rejected the same way OmniRoute's dropReason
// rejects one, or an entry with otherwise-valid auth becomes an uncallable
// preset.
func TestNineRouterNonHTTPSchemeIsSkipped(t *testing.T) {
	nine := []nineEntry{{
		ID:        "devin-cli",
		AuthType:  "apikey",
		Transport: nineTransport{BaseURL: "devin://acp/stdio"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if _, ok := got.Presets["devin-cli"]; ok {
		t.Error("a non-http(s) scheme was ingested as a preset")
	}
}

// The "openaicompat" kind can only serve the plain OpenAI-compatible dialect.
// "claude" is the Anthropic Messages shape -- serving it as openaicompat
// would send the wrong request body entirely.
func TestNineRouterClaudeFormatIsSkipped(t *testing.T) {
	nine := []nineEntry{{
		ID:        "claude",
		Category:  "apikey",
		Transport: nineTransport{BaseURL: "https://api.anthropic.com/v1/messages", Format: "claude"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if _, ok := got.Presets["claude"]; ok {
		t.Error("a claude-format entry was ingested as openaicompat")
	}
}

// "openai-responses" is OpenAI's newer Responses API, a different request
// shape from the chat-completions dialect openaicompat speaks.
func TestNineRouterOpenAIResponsesFormatIsSkipped(t *testing.T) {
	nine := []nineEntry{{
		ID:        "codex",
		Category:  "apikey",
		Transport: nineTransport{BaseURL: "https://chatgpt.com/backend-api/codex/responses", Format: "openai-responses"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if _, ok := got.Presets["codex"]; ok {
		t.Error("an openai-responses entry was ingested as openaicompat")
	}
}

// An oauth-category entry needs a credential flow this phase does not
// generate. Shipping it as bearer auth would invite an operator to paste an
// API key into a provider that only accepts an OAuth token.
func TestNineRouterOAuthCategoryIsSkipped(t *testing.T) {
	nine := []nineEntry{{
		ID:        "github",
		Category:  "oauth",
		Transport: nineTransport{BaseURL: "https://api.githubcopilot.com/chat/completions"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if _, ok := got.Presets["github"]; ok {
		t.Error("an oauth-category entry was ingested with bearer auth")
	}
}

// A webCookie-category entry needs the same credential flow oauth does; it is
// not a darkrouter auth style at all.
func TestNineRouterWebCookieCategoryIsSkipped(t *testing.T) {
	nine := []nineEntry{{
		ID:        "grok-web",
		Category:  "webCookie",
		Transport: nineTransport{BaseURL: "https://grok.com/rest/app-chat/conversations/new"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if _, ok := got.Presets["grok-web"]; ok {
		t.Error("a webCookie-category entry was ingested with bearer auth")
	}
}

// The ordinary case the filter must not reject: a plain OpenAI-compatible
// entry with an apikey category and an explicit "openai" format.
func TestNineRouterPlainOpenAIFormatIsKept(t *testing.T) {
	nine := []nineEntry{{
		ID:        "plainformat",
		Category:  "apikey",
		Transport: nineTransport{BaseURL: "https://api.plainformat.example/v1", Format: "openai"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if _, ok := got.Presets["plainformat"]; !ok {
		t.Error("an explicit openai-format apikey entry was skipped")
	}
}
