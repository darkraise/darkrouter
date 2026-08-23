package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/provider"
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
	case "bearer":
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
			ID string `json:"id"`
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
		out = append(out, Discovered{ModelID: m.ID})
	}
	return out, nil
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
