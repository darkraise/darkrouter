package main

import (
	"os"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// fieldOrigin records which upstream supplied one field of one preset.
type fieldOrigin struct {
	Field  string
	Source string
}

type merged struct {
	Presets   catalog.Presets
	Origins   map[string][]fieldOrigin
	Conflicts []conflict
}

// conflict is one disagreement between the two upstreams, recorded on raw
// pre-trim values so two URLs that agree only after transformation still
// surface. Precedence still resolves the merge; this is the review trail.
type conflict struct {
	ID, Field           string
	Winner, WinnerValue string
	Loser, LoserValue   string
}

const (
	srcOmni       = "omniroute"
	srcNine       = "9router"
	srcDarkrouter = "darkrouter"
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
			// A quirk is worth flagging whether or not this entry ends up
			// transcribed: the review trail is about what 9router declared,
			// not about what phase A managed to ingest. OmniRoute never
			// listed this id, so it cannot be named the winner here.
			out.Conflicts = append(out.Conflicts, quirkConflicts(n.ID, srcDarkrouter, n.Transport.Quirks)...)
			fresh, ok := n.toPreset()
			if !ok {
				continue
			}
			out.Presets[n.ID] = fresh
			out.Origins[n.ID] = originsOf(fresh, srcNine)
			continue
		}
		out.Conflicts = append(out.Conflicts, contestedFields(n.ID, rawBaseURL(omni, n.ID), n)...)
		filled := fillGaps(&p, n)
		out.Presets[n.ID] = p
		out.Origins[n.ID] = append(out.Origins[n.ID], filled...)
	}

	for id := range out.Origins {
		sort.Slice(out.Origins[id], func(i, j int) bool {
			return out.Origins[id][i].Field < out.Origins[id][j].Field
		})
	}
	sort.Slice(out.Conflicts, func(i, j int) bool {
		if out.Conflicts[i].ID != out.Conflicts[j].ID {
			return out.Conflicts[i].ID < out.Conflicts[j].ID
		}
		return out.Conflicts[i].Field < out.Conflicts[j].Field
	})
	return out
}

// contestedFields compares the two upstreams on the values they actually
// published, not on what trimming made of them.
func contestedFields(id, omniRaw string, n nineEntry) []conflict {
	var out []conflict
	if omniRaw != "" && n.Transport.BaseURL != "" && omniRaw != n.Transport.BaseURL {
		out = append(out, conflict{
			ID: id, Field: "base_url",
			Winner: srcOmni, WinnerValue: omniRaw,
			Loser: srcNine, LoserValue: n.Transport.BaseURL,
		})
	}
	out = append(out, quirkConflicts(id, srcOmni, n.Transport.Quirks)...)
	return out
}

// quirkConflicts reports a quirk darkrouter has no name for, never applying
// it: the vocabulary is closed and a guessed mapping silently changes request
// shape.
//
// winner names whichever source the review table should credit with the
// preset as it stands: srcOmni when OmniRoute's structural fields won the
// contested id, srcDarkrouter when the id is new to darkrouter and no
// upstream "won" anything.
func quirkConflicts(id, winner string, quirks map[string]bool) []conflict {
	var out []conflict
	names := make([]string, 0, len(quirks))
	for q, on := range quirks {
		if on {
			names = append(names, q)
		}
	}
	sort.Strings(names)
	for _, q := range names {
		out = append(out, conflict{
			ID: id, Field: "quirk:" + q,
			Winner: winner, WinnerValue: "(not applied)",
			Loser: srcNine, LoserValue: "declared upstream",
		})
	}
	return out
}

func rawBaseURL(omni []entry, id string) string {
	for _, e := range omni {
		if e.id == id {
			return e.baseURL
		}
	}
	return ""
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

// unservableNine names 9router ids whose transcribed preset would be wrong
// in a way no structural rule catches, verified by hand against the
// registry:
//
//	vertex-partner  baseUrl is "https://aiplatform.googleapis.com", byte for
//	                byte the losing value the "vertex" conflict already
//	                records for lacking the required regional host prefix.
//	mimo-free, mmf  byte-identical entries whose baseUrl ends in "/chat",
//	                a path chatSuffixes does not trim, so the adapter would
//	                request .../chat/chat/completions. Upstream's own
//	                comment marks the free channel they serve as ended.
var unservableNine = map[string]bool{
	"vertex-partner": true,
	"mimo-free":      true,
	"mmf":            true,
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
	// The "openaicompat" kind speaks one dialect: plain chat-completions.
	// "claude" (Anthropic Messages), "openai-responses", "ollama", "cursor",
	// "kiro", "gemini-cli" and "commandcode" are different wire protocols
	// entirely -- transcribing them as openaicompat would ship a preset whose
	// request body the upstream never accepts.
	if f := strings.ToLower(e.Transport.Format); f != "" && f != "openai" {
		return catalog.Preset{}, false
	}
	// oauth and webCookie need a credential flow this phase does not
	// generate -- carrying them through as bearer auth (authType/authHeader
	// are usually absent on these entries, and authStyle's default is
	// bearer) would invite an operator to paste an API key into a provider
	// that only accepts an OAuth token or a browser session cookie.
	switch e.Category {
	case "oauth", "webCookie":
		return catalog.Preset{}, false
	}
	// A hand-verified list of ids a structural rule above cannot catch:
	// vertex-partner ships the same regionless host the vertex conflict
	// already rejected as the losing value, and mimo-free/mmf are
	// byte-identical entries whose endpoint path ends in "/chat", which
	// chatSuffixes does not trim, and whose upstream comment marks the free
	// channel they serve as ended.
	if unservableNine[e.ID] {
		return catalog.Preset{}, false
	}
	// Some entries carry no transport block at all: their endpoint lives in a
	// per-surface config a later phase reads. Without it there is no preset.
	base := trimAPISuffix(e.Transport.BaseURL)
	if base == "" {
		return catalog.Preset{}, false
	}
	// OmniRoute's dropReason has rejected a non-http(s) scheme since day one;
	// 9router needs the same guard or a custom scheme with otherwise-valid
	// auth slips through as an uncallable preset.
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
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
		Surfaces:  e.surfaces(),
		Website:   e.Display.Website,
		APIKeyURL: e.Display.Notice.APIKeyURL,
		Auth:      catalog.Auth{Style: style},
	}
	// A preset with no models.dev counterpart must say so explicitly; a
	// missing join key and a forgotten one look identical otherwise.
	p.NoModelsDev = true
	return p, true
}

// surfaces accumulates rather than replaces: a provider declaring both kinds
// serves both, and preset surfaces beat discovered rows, so dropping one here
// would unroute a whole surface with nothing to show for it. Ordered llm
// first so the generated file is byte-stable across runs.
func (e nineEntry) surfaces() []string {
	var out []string
	if len(e.ServiceKinds) == 0 || slices.Contains(e.ServiceKinds, "llm") {
		out = append(out, "llm")
	}
	if slices.Contains(e.ServiceKinds, "embedding") {
		out = append(out, "embedding")
	}
	return out
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
		switch strings.ToLower(e.AuthType) {
		case "", "apikey":
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

// markOverridden re-attributes every field the overrides file declares. An
// override outranks both upstreams, so the manifest must not keep crediting
// the scraper whose value was replaced.
func markOverridden(m *merged, overridesPath string) {
	raw, err := os.ReadFile(overridesPath)
	if err != nil {
		return
	}
	var declared map[string]map[string]any
	if err := yaml.Unmarshal(raw, &declared); err != nil {
		return
	}
	for id, fields := range declared {
		for field := range fields {
			replaced := false
			for i := range m.Origins[id] {
				if m.Origins[id][i].Field == field {
					m.Origins[id][i].Source = "override"
					replaced = true
				}
			}
			if !replaced {
				m.Origins[id] = append(m.Origins[id], fieldOrigin{Field: field, Source: "override"})
			}
		}
		sort.Slice(m.Origins[id], func(i, j int) bool {
			return m.Origins[id][i].Field < m.Origins[id][j].Field
		})
	}
}
