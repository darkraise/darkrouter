package catalog

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

func TestProbeForBuildsFromThePreset(t *testing.T) {
	p := provider.Provider{ID: "p", Kind: "openaicompat", BaseURL: "https://api.example.com/v1", Preset: "acme"}
	pre := Preset{Kind: "openaicompat", Auth: Auth{Style: "bearer"}}
	got, err := ProbeFor(p, pre, "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "openaicompat" || got.BaseURL != "https://api.example.com/v1" || got.APIKey != "sk-test" {
		t.Errorf("probe = %+v", got)
	}
	if got.AuthStyle != "bearer" {
		t.Errorf("auth style = %q", got.AuthStyle)
	}
}

func TestProbeForPrefersTheProviderRowAuthStyle(t *testing.T) {
	// The provider row overrides its preset, per spec §7's last line.
	p := provider.Provider{ID: "p", Kind: "openaicompat", BaseURL: "https://x/v1", AuthStyle: "x-api-key"}
	got, _ := ProbeFor(p, Preset{Auth: Auth{Style: "bearer"}}, "k")
	if got.AuthStyle != "x-api-key" {
		t.Errorf("auth style = %q, want the row's x-api-key", got.AuthStyle)
	}
}

func TestProbeForRejectsUndiscoverableKinds(t *testing.T) {
	// Vertex has no practical listing API and Bedrock needs two control-plane
	// calls that arrive in phase 8. Both must be a recognizable skip rather
	// than a probe that fails on every tick and cools a credential for it.
	for _, kind := range []string{"vertex", "bedrock", "nonsense"} {
		if _, err := ProbeFor(provider.Provider{Kind: kind, BaseURL: "https://x"}, Preset{}, "k"); !errors.Is(err, ErrKindNotDiscoverable) {
			t.Errorf("kind %q: err = %v, want ErrKindNotDiscoverable", kind, err)
		}
	}
}

func TestBuildListRequestPerKind(t *testing.T) {
	ctx := context.Background()

	oa, err := BuildListRequest(ctx, Probe{Kind: "openaicompat", BaseURL: "https://api.example.com/v1/", AuthStyle: "bearer", APIKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	if oa.URL.String() != "https://api.example.com/v1/models" {
		t.Errorf("openaicompat url = %s", oa.URL)
	}
	if oa.Header.Get("Authorization") != "Bearer sk" {
		t.Errorf("openaicompat auth = %q", oa.Header.Get("Authorization"))
	}

	an, err := BuildListRequest(ctx, Probe{Kind: "anthropic", BaseURL: "https://api.anthropic.com/v1", AuthStyle: "x-api-key", APIKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	if an.URL.String() != "https://api.anthropic.com/v1/models" {
		t.Errorf("anthropic url = %s", an.URL)
	}
	if an.Header.Get("x-api-key") != "sk" {
		t.Errorf("anthropic key header = %q", an.Header.Get("x-api-key"))
	}
	// Anthropic requires the version header on every request, listing
	// included; without it the probe is a 400 that looks like a bad key.
	if an.Header.Get("anthropic-version") == "" {
		t.Error("anthropic-version header missing")
	}

	gm, err := BuildListRequest(ctx, Probe{
		Kind: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta",
		AuthStyle: "query-param", AuthQueryParam: "key", APIKey: "sk",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gm.URL.Path != "/v1beta/models" {
		t.Errorf("gemini path = %s", gm.URL.Path)
	}
	if gm.URL.Query().Get("key") != "sk" {
		t.Errorf("gemini key query = %q", gm.URL.Query().Get("key"))
	}
	// The key must never reach a header when it is a query parameter, and it
	// must never appear twice.
	if gm.Header.Get("Authorization") != "" {
		t.Error("gemini sent an Authorization header alongside the query key")
	}
}

func TestBuildListRequestHonorsAModelsURLOverride(t *testing.T) {
	// Some OpenAI-compatible upstreams serve chat and listing from different
	// hosts; the preset says so.
	r, err := BuildListRequest(context.Background(), Probe{
		Kind: "openaicompat", BaseURL: "https://chat.example.com/v1",
		ModelsURL: "https://catalog.example.com/v1/models", AuthStyle: "bearer", APIKey: "sk",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.String() != "https://catalog.example.com/v1/models" {
		t.Errorf("url = %s", r.URL)
	}
}

func TestBuildListRequestCustomAPIKeyHeader(t *testing.T) {
	r, _ := BuildListRequest(context.Background(), Probe{
		Kind: "openaicompat", BaseURL: "https://x/v1",
		AuthStyle: "api-key", AuthHeader: "api-key", APIKey: "sk",
	})
	if r.Header.Get("api-key") != "sk" {
		t.Errorf("api-key header = %q", r.Header.Get("api-key"))
	}
}

func TestBuildListRequestNoAuth(t *testing.T) {
	// A local runtime takes no key. Sending an empty Bearer header makes some
	// of them 401 rather than serving.
	r, _ := BuildListRequest(context.Background(), Probe{Kind: "openaicompat", BaseURL: "http://localhost:11434/v1", AuthStyle: "none"})
	if r.Header.Get("Authorization") != "" {
		t.Errorf("Authorization = %q, want empty", r.Header.Get("Authorization"))
	}
}

func TestBuildListRequestSendsTheKeyForEveryKeylessStyleThatHasOne(t *testing.T) {
	// The listing request is what discovery and the console probe both send.
	// An anonymous provider has a key by definition and an optional one may
	// have been given one, and neither reached the wire while these styles
	// fell through to the unsigned default: the sweep then reported whatever
	// an unauthenticated listing returns.
	for _, style := range []string{"anonymous", "optional"} {
		r, err := BuildListRequest(context.Background(), Probe{
			Kind: "openaicompat", BaseURL: "https://oai.example.net/v1",
			AuthStyle: style, APIKey: "0000000000",
		})
		if err != nil {
			t.Fatalf("%s: %v", style, err)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer 0000000000" {
			t.Errorf("%s: Authorization = %q, want the key", style, got)
		}
	}

	// An optional provider with no key still sends nothing: an empty Bearer
	// header is what several of them 401 on.
	r, _ := BuildListRequest(context.Background(), Probe{
		Kind: "openaicompat", BaseURL: "https://oai.example.net/v1", AuthStyle: "optional",
	})
	if got := r.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}

func TestParseOpenAICompatList(t *testing.T) {
	got, err := ParseList("openaicompat",
		[]byte(`{"object":"list","data":[{"id":"gpt-4o","object":"model"},{"id":"gpt-4o-mini"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ModelID != "gpt-4o" || got[1].ModelID != "gpt-4o-mini" {
		t.Errorf("got %+v", got)
	}
}

func TestParseAnthropicList(t *testing.T) {
	got, err := ParseList("anthropic",
		[]byte(`{"data":[{"type":"model","id":"claude-opus-4-5","display_name":"Claude Opus 4.5"}],"has_more":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ModelID != "claude-opus-4-5" {
		t.Errorf("got %+v", got)
	}
}

func TestParseGeminiListStripsThePrefixAndKeepsLimits(t *testing.T) {
	// Gemini names models "models/x" and reports real token limits, which is
	// metadata no other probe supplies.
	got, err := ParseList("gemini", []byte(`{"models":[
	  {"name":"models/gemini-2.5-pro","inputTokenLimit":1048576,"outputTokenLimit":65536,
	   "supportedGenerationMethods":["generateContent"]},
	  {"name":"models/embedding-001","supportedGenerationMethods":["embedContent"]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models", len(got))
	}
	if got[0].ModelID != "gemini-2.5-pro" {
		t.Errorf("id = %q, want the models/ prefix stripped", got[0].ModelID)
	}
	if got[0].ContextWindow != 1048576 || got[0].MaxOutputTokens != 65536 {
		t.Errorf("limits = (%d, %d)", got[0].ContextWindow, got[0].MaxOutputTokens)
	}
}

func TestParseListRejectsGarbage(t *testing.T) {
	// An HTML error page or a truncated body must be an error. Reading it as
	// an empty listing is the input that makes discovery retire every model
	// the provider serves.
	for _, body := range []string{"", "<html>502</html>", "{}", `{"data":[]}`} {
		if _, err := ParseList("openaicompat", []byte(body)); err == nil {
			t.Errorf("%q parsed as a valid listing", body)
		}
	}
}

func TestParseListRejectsEntriesWithoutIDs(t *testing.T) {
	// A model with no id cannot be routed to and must not occupy a row.
	got, err := ParseList("openaicompat", []byte(`{"data":[{"id":"a"},{"object":"model"},{"id":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ModelID != "a" {
		t.Errorf("got %+v", got)
	}
}

// The four price tests below read listings captured verbatim from the live
// endpoints on 2026-09-03 and decoded through the production parser.

func TestParseListHarvestsStringQuotedPrices(t *testing.T) {
	body, err := os.ReadFile("testdata/listing-hackclub.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseList("openaicompat", body)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Discovered{}
	for _, m := range got {
		by[m.ModelID] = m
	}

	// "prompt": "0.00000075" is $0.75 per million tokens, 750000 micros.
	priced := by["google/gemini-3.8-flash"]
	if priced.Pricing == nil {
		t.Fatalf("gemini-3.8-flash carried no price")
	}
	want := store.ModelPricing{
		InputMicrosPerMTok:      750_000,
		OutputMicrosPerMTok:     3_750_000,
		CacheReadMicrosPerMTok:  75_000,
		CacheWriteMicrosPerMTok: 41_667,
	}
	if *priced.Pricing != want {
		t.Errorf("pricing = %+v, want %+v", *priced.Pricing, want)
	}

	// A free model's zeroes are a price, not an absence.
	free := by["inclusionai/ling-3.0-flash-fin:free"]
	if free.Pricing == nil {
		t.Fatalf("a free model must carry a known price of zero")
	}
	if *free.Pricing != (store.ModelPricing{}) {
		t.Errorf("free pricing = %+v, want all zero", *free.Pricing)
	}

	// -1 is openrouter's "the auto-router decides", not a rate.
	if auto := by["openrouter/auto"]; auto.Pricing != nil {
		t.Errorf("openrouter/auto priced at %+v from a -1 rate", *auto.Pricing)
	}
}

func TestParseListHarvestsNumericPerTokenPrices(t *testing.T) {
	body, err := os.ReadFile("testdata/listing-naga.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseList("openaicompat", body)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Discovered{}
	for _, m := range got {
		by[m.ModelID] = m
	}

	priced := by["qwen3.8-flash"]
	if priced.Pricing == nil {
		t.Fatalf("qwen3.8-flash carried no price")
	}
	want := store.ModelPricing{
		InputMicrosPerMTok:     75_000,
		OutputMicrosPerMTok:    235_000,
		CacheReadMicrosPerMTok: 8_000,
	}
	if *priced.Pricing != want {
		t.Errorf("pricing = %+v, want %+v", *priced.Pricing, want)
	}

	if free := by["dots-3-note-preview:free"]; free.Pricing == nil {
		t.Errorf("a free model must carry a known price of zero")
	}

	// An image model quotes per_output_image_token alone. It has no token
	// price, and a zeroed one would report it as free.
	if img := by["seedream-5-lite"]; img.Pricing != nil {
		t.Errorf("seedream-5-lite priced at %+v from an image-only rate", *img.Pricing)
	}
}

func TestParseListLeavesAnUnpricedListingNil(t *testing.T) {
	body, err := os.ReadFile("testdata/listing-opencode.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseList("openaicompat", body)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Pricing != nil {
			t.Errorf("%s: pricing = %+v, want nil for a listing with no price",
				m.ModelID, *m.Pricing)
		}
	}
}

func TestParseListRefusesAmbiguousNumericPromptRates(t *testing.T) {
	// chutes.ai reuses openrouter's "prompt"/"completion" names for numbers
	// that mean dollars per million, not per token. Reading them as per-token
	// would price every one of its models a million times over.
	body, err := os.ReadFile("testdata/listing-chutes.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseList("openaicompat", body)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Pricing != nil {
			t.Errorf("%s: pricing = %+v, want nil for an ambiguous unit",
				m.ModelID, *m.Pricing)
		}
	}
}
