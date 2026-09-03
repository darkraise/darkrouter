package main

import (
	"sort"
	"strings"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// fieldOrigin records which upstream supplied one field of one preset.
type fieldOrigin struct {
	Field  string
	Source string
}

type merged struct {
	Presets catalog.Presets
	Origins map[string][]fieldOrigin
}

const (
	srcOmni = "omniroute"
	srcNine = "9router"
)

// mergeSources folds both upstreams into one preset set.
//
// OmniRoute wins every contested structural field: its transcription has been
// reviewed across nine phases and 9router's has not. 9router fills what
// OmniRoute does not carry, and supplies providers OmniRoute never listed.
func mergeSources(omni []entry, display map[string]displayEntry, nine []nineEntry) merged {
	out := merged{Presets: catalog.Presets{}, Origins: map[string][]fieldOrigin{}}

	for _, e := range omni {
		p := e.toPreset(display[e.id])
		out.Presets[e.id] = p
		out.Origins[e.id] = originsOf(p, srcOmni)
	}

	for _, n := range nine {
		if !n.routable() {
			continue
		}
		p, ok := out.Presets[n.ID]
		if !ok {
			fresh, ok := n.toPreset()
			if !ok {
				continue
			}
			out.Presets[n.ID] = fresh
			out.Origins[n.ID] = originsOf(fresh, srcNine)
			continue
		}
		filled := fillGaps(&p, n)
		out.Presets[n.ID] = p
		out.Origins[n.ID] = append(out.Origins[n.ID], filled...)
	}

	for id := range out.Origins {
		sort.Slice(out.Origins[id], func(i, j int) bool {
			return out.Origins[id][i].Field < out.Origins[id][j].Field
		})
	}
	return out
}

// fillGaps takes from 9router only what the winning source left empty, and
// reports which fields it filled.
func fillGaps(p *catalog.Preset, n nineEntry) []fieldOrigin {
	var filled []fieldOrigin
	set := func(field string, dst *string, val string) {
		if *dst == "" && val != "" {
			*dst = val
			filled = append(filled, fieldOrigin{Field: field, Source: srcNine})
		}
	}
	set("name", &p.Name, n.Display.Name)
	set("website", &p.Website, n.Display.Website)
	set("api_key_url", &p.APIKeyURL, n.Display.Notice.APIKeyURL)
	set("base_url", &p.BaseURL, trimAPISuffix(n.Transport.BaseURL))
	return filled
}

// toPreset builds a preset from a 9router entry alone, for a provider
// OmniRoute never listed. The second result is false for an entry this phase
// cannot transcribe faithfully.
//
// Skipping beats guessing. A provider that is absent is obviously absent; one
// shipped with an empty base URL, or with a plausible-but-wrong auth style,
// passes every check the preset file makes and then fails at request time,
// where the operator has no way to tell a transcription error from a provider
// that is simply down.
func (e nineEntry) toPreset() (catalog.Preset, bool) {
	// Some entries carry no transport block at all: their endpoint lives in a
	// per-surface config a later phase reads. Without it there is no preset.
	base := trimAPISuffix(e.Transport.BaseURL)
	if base == "" {
		return catalog.Preset{}, false
	}
	style, ok := e.authStyle()
	if !ok {
		return catalog.Preset{}, false
	}
	p := catalog.Preset{
		Name:      e.Name(),
		Kind:      "openaicompat",
		BaseURL:   base,
		Surfaces:  []string{"llm"},
		Website:   e.Display.Website,
		APIKeyURL: e.Display.Notice.APIKeyURL,
		Auth:      catalog.Auth{Style: style},
	}
	for _, k := range e.ServiceKinds {
		if k == "embedding" {
			p.Surfaces = []string{"embedding"}
		}
	}
	// A preset with no models.dev counterpart must say so explicitly; a
	// missing join key and a forgotten one look identical otherwise.
	p.NoModelsDev = true
	return p, true
}

// authStyle maps a 9router entry onto darkrouter's closed auth vocabulary,
// reporting false where no member of it is the truth.
//
// The wire style is the transport's authHeader. 9router's top-level authType
// is a category rather than a header: jina-ai is "apikey" there and "bearer"
// on the wire. So the header is read first, and the category only decides
// whether the headerless default is safe -- "oauth" needs an oauth block this
// phase does not generate, and "cookie" is not a darkrouter style at all.
func (e nineEntry) authStyle() (string, bool) {
	switch strings.ToLower(e.Transport.AuthHeader) {
	case "bearer", "authorization":
		return "bearer", true
	case "x-api-key":
		return "x-api-key", true
	case "":
		if e.AuthType == "" || e.AuthType == "apikey" {
			return "bearer", true
		}
	}
	return "", false
}

// Name falls back to the id when upstream carries no display name, so a preset
// never renders as an empty string in the picker.
func (e nineEntry) Name() string {
	if e.Display.Name != "" {
		return e.Display.Name
	}
	return e.ID
}

// trimAPISuffix strips the endpoint path so what remains is the API root the
// adapter appends its own path to. Longest first, for the reason chatSuffixes
// documents.
func trimAPISuffix(u string) string {
	base := strings.TrimRight(u, "/")
	for _, s := range chatSuffixes {
		if trimmed, ok := strings.CutSuffix(base, s); ok {
			return trimmed
		}
	}
	return base
}

func originsOf(p catalog.Preset, source string) []fieldOrigin {
	var out []fieldOrigin
	for field, val := range map[string]string{
		"name":        p.Name,
		"base_url":    p.BaseURL,
		"website":     p.Website,
		"api_key_url": p.APIKeyURL,
	} {
		if val != "" {
			out = append(out, fieldOrigin{Field: field, Source: source})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}
