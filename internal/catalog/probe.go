package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// anthropicVersion is required on every Anthropic request, listing included.
// Omitting it is a 400 that reads like a rejected key.
const anthropicVersion = "2023-06-01"

// ErrKindNotDiscoverable marks a kind with no usable listing endpoint.
//
// Vertex has no practical API for listing which models a project may actually
// call, so its entries come from presets and models.dev instead. Bedrock needs
// two control-plane calls that arrive with its adapter in phase 8. Both must be
// a recognizable skip: a probe that fails on every tick would cool the
// credential for a listing the provider was never going to serve.
var ErrKindNotDiscoverable = errors.New("kind has no listing endpoint")

// Discovered is one model a listing reported.
type Discovered struct {
	ModelID string
	// ContextWindow and MaxOutputTokens are populated only where the listing
	// carries them, which today is Gemini alone.
	ContextWindow   int
	MaxOutputTokens int

	// Pricing is nil unless the listing quoted a rate this parser can read in
	// a unit it is sure of. Nil means "the provider did not say", which must
	// not overwrite what another source already knows.
	Pricing *store.ModelPricing
}

// Probe is everything a listing request needs, with no live collaborators. The
// worker supplies the client and the clock.
type Probe struct {
	ProviderID     string
	Kind           string
	BaseURL        string
	ModelsURL      string
	APIKey         string
	AuthStyle      string
	AuthHeader     string
	AuthQueryParam string

	// Region and Authorize are what a signed control-plane listing needs. Both
	// are empty for every kind whose listing is an unsigned GET with a bearer
	// token, which is all of them before phase 8.
	Region    string
	Authorize func(context.Context, *http.Request) error

	// Lister replaces the generic /models call for a kind that has none.
	// Bedrock needs two calls against a different host; vertex has no listing
	// at all and registers nothing, so it stays undiscoverable.
	Lister KindLister
}

// KindLister discovers a kind whose model list does not come from one GET.
//
// An interface rather than a switch on kind because catalog must not import
// internal/adapter/bedrock: the concrete lister is registered by the server,
// which already imports both.
type KindLister interface {
	List(ctx context.Context, p Probe) ([]Discovered, error)
}

// ProbeForKind is ProbeFor with the lister registry consulted first.
//
// ProbeFor stays as it is and is what every existing caller and test keeps
// using, so nothing before phase 8 changes behavior.
func ProbeForKind(p provider.Provider, preset Preset, apiKey string,
	listers map[string]KindLister) (Probe, error) {

	if l, ok := listers[p.Kind]; ok {
		base := p.BaseURL
		if base == "" {
			base = preset.BaseURL
		}
		return Probe{
			ProviderID: p.ID, Kind: p.Kind, BaseURL: base,
			Region: p.Region, Lister: l,
		}, nil
	}
	return ProbeFor(p, preset, apiKey)
}

// ProbeFor resolves a provider and its preset into a probe. The provider row
// wins over the preset wherever both speak, per spec §7.
func ProbeFor(p provider.Provider, preset Preset, apiKey string) (Probe, error) {
	switch p.Kind {
	case "openaicompat", "anthropic", "gemini":
	default:
		return Probe{}, fmt.Errorf("%w: %s", ErrKindNotDiscoverable, p.Kind)
	}
	style := p.AuthStyle
	if style == "" {
		style = preset.Auth.Style
	}
	if style == "" {
		style = "bearer"
	}
	base := p.BaseURL
	if base == "" {
		base = preset.BaseURL
	}
	if base == "" {
		return Probe{}, fmt.Errorf("provider %q has no base url", p.ID)
	}
	return Probe{
		ProviderID:     p.ID,
		Kind:           p.Kind,
		BaseURL:        base,
		ModelsURL:      preset.ModelsURL,
		APIKey:         apiKey,
		AuthStyle:      style,
		AuthHeader:     preset.Auth.Header,
		AuthQueryParam: preset.Auth.QueryParam,
	}, nil
}

// BuildListRequest renders the listing request for one probe.
func BuildListRequest(ctx context.Context, p Probe) (*http.Request, error) {
	url := p.ModelsURL
	if url == "" {
		switch p.Kind {
		case "openaicompat", "anthropic", "gemini":
			url = strings.TrimRight(p.BaseURL, "/") + "/models"
		default:
			return nil, fmt.Errorf("%w: %s", ErrKindNotDiscoverable, p.Kind)
		}
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build listing request: %w", err)
	}
	r.Header.Set("Accept", "application/json")
	if p.Kind == "anthropic" {
		r.Header.Set("anthropic-version", anthropicVersion)
	}
	applyAuth(r, p)
	return r, nil
}

// applyAuth attaches the credential exactly once, in exactly one place. A key
// sent as both a header and a query parameter is rejected by some upstreams and
// logged by more of them.
func applyAuth(r *http.Request, p Probe) {
	if p.APIKey == "" || p.AuthStyle == "none" {
		return
	}
	switch p.AuthStyle {
	// optional and anonymous are bearer with a different rule about when there
	// is a key at all: one may have none, the other ships its own. By the time
	// a key has been resolved they are written the same way, and falling
	// through to the default instead sent the listing request unauthenticated
	// while the caller believed it had been credentialled.
	case "bearer", "optional", "anonymous":
		r.Header.Set("Authorization", "Bearer "+p.APIKey)
	case "x-api-key":
		r.Header.Set("x-api-key", p.APIKey)
	case "api-key":
		header := p.AuthHeader
		if header == "" {
			header = "api-key"
		}
		r.Header.Set(header, p.APIKey)
	case "query-param":
		param := p.AuthQueryParam
		if param == "" {
			param = "key"
		}
		q := r.URL.Query()
		q.Set(param, p.APIKey)
		r.URL.RawQuery = q.Encode()
	default:
		// sigv4, gcp-sa and oauth are phase 8's. ProbeFor already refused
		// their kinds, so reaching here means an unsigned request that will be
		// rejected — which is the honest outcome, not a silent bearer guess.
	}
}

// ParseList decodes a listing.
//
// An empty listing is an error rather than an empty result. That distinction is
// load-bearing: spec §5.1 makes a *successful* listing that omits a model the
// evidence that retires it, so an HTML error page read as "zero models" would
// retire everything the provider serves.
func ParseList(kind string, body []byte) ([]Discovered, error) {
	var out []Discovered
	var err error
	switch kind {
	case "gemini":
		out, err = parseGeminiList(body)
	case "openaicompat", "anthropic":
		out, err = parseDataList(body)
	default:
		return nil, fmt.Errorf("%w: %s", ErrKindNotDiscoverable, kind)
	}
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("listing reported no models")
	}
	return out, nil
}

func parseDataList(body []byte) ([]Discovered, error) {
	var doc struct {
		Data []struct {
			ID      string        `json:"id"`
			Pricing listedPricing `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse listing: %w", err)
	}
	out := make([]Discovered, 0, len(doc.Data))
	for _, m := range doc.Data {
		if m.ID == "" {
			continue // a model with no id cannot be routed to
		}
		out = append(out, Discovered{ModelID: m.ID, Pricing: m.Pricing.rates()})
	}
	return out, nil
}

// listedPricing is the price block of an OpenAI-compatible listing, in the two
// shapes that publish one. Every field is optional and every one of them is
// absent from most listings.
type listedPricing struct {
	// OpenRouter's names, and hackclub proxies them verbatim. Read only when
	// quoted as a JSON string: chutes.ai publishes the same two names as
	// numbers meaning dollars per million tokens, so taking a number here
	// would price its models a million times over.
	Prompt     listedRate `json:"prompt"`
	Completion listedRate `json:"completion"`
	CacheRead  listedRate `json:"input_cache_read"`
	CacheWrite listedRate `json:"input_cache_write"`

	// naga.ac's names, quoted as JSON numbers. They carry their unit in the
	// name, so either form is unambiguous.
	PerInput       listedRate `json:"per_input_token"`
	PerOutput      listedRate `json:"per_output_token"`
	PerCachedInput listedRate `json:"per_cached_input_token"`
}

// rates renders the block into the catalog's unit, or nil when the listing
// quoted nothing this parser trusts. A zero rate is a real price — free models
// are most of what these aggregators serve — so absence has to be a nil rather
// than a zeroed struct.
//
// Both a prompt and a completion rate are required. A block quoting one side
// alone, or an image or audio rate alone, prices a modality this parser does
// not read, and filling the missing half with a zero would report it as free.
func (l listedPricing) rates() *store.ModelPricing {
	in, inOK := firstRate(l.Prompt, l.PerInput)
	out, outOK := firstRate(l.Completion, l.PerOutput)
	if !inOK || !outOK {
		return nil
	}
	cacheRead, _ := firstRate(l.CacheRead, l.PerCachedInput)
	cacheWrite, _ := l.CacheWrite.quotedDollars()
	return &store.ModelPricing{
		InputMicrosPerMTok:      perMTok(in),
		OutputMicrosPerMTok:     perMTok(out),
		CacheReadMicrosPerMTok:  perMTok(cacheRead),
		CacheWriteMicrosPerMTok: perMTok(cacheWrite),
	}
}

// firstRate takes whichever of the two shapes quoted this rate. A listing
// speaks one of them, never both.
func firstRate(quoted, numeric listedRate) (float64, bool) {
	if v, ok := quoted.quotedDollars(); ok {
		return v, true
	}
	return numeric.dollars()
}

// UnmarshalJSON reads only an object. A provider that puts a string or a
// number where the price block goes still has a listing worth recording, and
// failing the decode would retire every model it serves.
func (l *listedPricing) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || b[0] != '{' {
		return nil
	}
	type plain listedPricing
	var v plain
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	*l = listedPricing(v)
	return nil
}

// listedRate is one rate as a listing quoted it, remembering whether it
// arrived as a JSON string. It never fails: a price field in a shape this
// parser does not know reads as absent, because a listing is worth having for
// its model ids even when its prices are unreadable.
type listedRate struct {
	value  float64
	known  bool
	quoted bool
}

func (r *listedRate) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if json.Unmarshal(b, &s) != nil {
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil
		}
		r.value, r.known, r.quoted = v, true, true
		return nil
	}
	var v float64
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	r.value, r.known = v, true
	return nil
}

// dollars reports the rate in dollars per token. A negative rate is
// openrouter's "the auto-router decides", which is not a price.
func (r listedRate) dollars() (float64, bool) {
	if !r.known || r.value < 0 {
		return 0, false
	}
	return r.value, true
}

func (r listedRate) quotedDollars() (float64, bool) {
	if !r.quoted {
		return 0, false
	}
	return r.dollars()
}

func parseGeminiList(body []byte) ([]Discovered, error) {
	var doc struct {
		Models []struct {
			Name             string `json:"name"`
			InputTokenLimit  int    `json:"inputTokenLimit"`
			OutputTokenLimit int    `json:"outputTokenLimit"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse listing: %w", err)
	}
	out := make([]Discovered, 0, len(doc.Models))
	for _, m := range doc.Models {
		// Gemini names models "models/x"; the routable identifier is the leaf.
		id := strings.TrimPrefix(m.Name, "models/")
		if id == "" {
			continue
		}
		out = append(out, Discovered{
			ModelID:         id,
			ContextWindow:   m.InputTokenLimit,
			MaxOutputTokens: m.OutputTokenLimit,
		})
	}
	return out, nil
}
