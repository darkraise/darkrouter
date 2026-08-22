package catalog

import (
	"bytes"
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed presets.yaml
var presetsYAML []byte

// Preset is shipped data describing one named upstream. Adding a catalogued
// provider is then a name and a key rather than a code change, which is the
// whole premise of "breadth costs data, not code".
type Preset struct {
	Name        string   `yaml:"name"`
	Kind        string   `yaml:"kind"`
	BaseURL     string   `yaml:"base_url"`
	Auth        Auth     `yaml:"auth"`
	Surfaces    []string `yaml:"surfaces"`
	ModelsDevID string   `yaml:"models_dev_id,omitempty"`

	// NoModelsDev is the explicit exemption spec §10 requires. Without it an
	// entry that merely forgot its join key is indistinguishable from one that
	// genuinely has no models.dev counterpart.
	NoModelsDev bool     `yaml:"no_models_dev,omitempty"`
	FreeTier    bool     `yaml:"free_tier,omitempty"`
	Website     string   `yaml:"website,omitempty"`
	Quirks      []string `yaml:"quirks,omitempty"`

	// ModelsURL overrides the listing endpoint the kind would otherwise
	// derive. Some OpenAI-compatible upstreams serve chat and listing from
	// different hosts.
	ModelsURL string `yaml:"models_url,omitempty"`

	// ModelAliases maps a Darkrouter model id to the models.dev model id the
	// normalization rule failed to reach. Spec §4.1.
	ModelAliases map[string]string `yaml:"model_aliases,omitempty"`

	// ModelTraits declares per-generation request-shape facts models.dev
	// cannot express. See TraitRule.
	ModelTraits []TraitRule `yaml:"model_traits,omitempty"`

	// CapabilityProbe names a runtime that reports its own capabilities.
	// Empty means none; "ollama" means /api/show. Spec §6.
	CapabilityProbe string `yaml:"capability_probe,omitempty"`

	OAuth *OAuth `yaml:"oauth,omitempty"`
}

type Auth struct {
	Style string `yaml:"style"`
	// Header overrides the header name for the api-key style, whose spelling
	// varies per upstream.
	Header string `yaml:"header,omitempty"`
	// QueryParam names the parameter for the query-param style.
	QueryParam string `yaml:"query_param,omitempty"`
}

type OAuth struct {
	AuthorizeURL string   `yaml:"authorize_url"`
	TokenURL     string   `yaml:"token_url"`
	ClientID     string   `yaml:"client_id"`
	Scopes       []string `yaml:"scopes"`
	Redirect     Redirect `yaml:"redirect"`
}

type Redirect struct {
	Style string `yaml:"style"` // localhost or manual
	Port  int    `yaml:"port"`
}

// TraitRule declares the request-shape facts for a family of model names.
//
// It is data rather than code because models.dev cannot express it: its
// reasoning_options lists "effort" for claude-opus-4-5, but Phase 4 verified
// live that thinking:{type:"adaptive"} is a 400 on that model. The two are
// different controls sharing a word, and deriving one from the other breaks
// the most-used Anthropic model.
type TraitRule struct {
	// Match is a substring of the normalized model name. Rules are evaluated
	// longest-match-first so "opus-4-5" cannot be shadowed by "opus-4".
	Match        string `yaml:"match"`
	Adaptive     bool   `yaml:"adaptive,omitempty"`
	ManualBudget bool   `yaml:"manual_budget,omitempty"`
	FreeSampling bool   `yaml:"free_sampling,omitempty"`
}

type Presets map[string]Preset

// Kinds is every provider kind an adapter exists for, plus the two whose
// adapters arrive in phase 8. A preset naming anything else is a typo.
var Kinds = map[string]bool{
	"openaicompat": true,
	"anthropic":    true,
	"gemini":       true,
	"bedrock":      true,
	"vertex":       true,
}

// AuthStyles is the closed vocabulary from spec §3.
var AuthStyles = map[string]bool{
	"bearer": true, "x-api-key": true, "api-key": true,
	"query-param": true, "none": true, "sigv4": true,
	"gcp-sa": true, "oauth": true,
}

// bareQuirks take no value.
var bareQuirks = map[string]bool{
	"rejects-stream-options":      true,
	"requires-max-tokens":         true,
	"max-completion-tokens-name":  true,
	"no-system-role":              true,
	"no-parallel-tool-calls":      true,
	"temperature-top-p-exclusive": true,
	"strict-unknown-fields":       true,
	"no-tool-streaming":           true,
	"usage-final-chunk-only":      true,
}

// valuedQuirks require a non-empty value after '='. Tag-plus-value was chosen
// over bare tags deliberately: retrofitting values into a tag set later is the
// dumping ground with extra steps.
var valuedQuirks = map[string]func(string) bool{
	"rerank-path":      func(v string) bool { return strings.HasPrefix(v, "/") },
	"context-override": func(v string) bool { n, err := strconv.Atoi(v); return err == nil && n > 0 },
}

// Quirks is the vocabulary as a set, for callers that only need membership.
var Quirks = func() map[string]bool {
	out := make(map[string]bool, len(bareQuirks)+len(valuedQuirks))
	for q := range bareQuirks {
		out[q] = true
	}
	for q := range valuedQuirks {
		out[q] = true
	}
	return out
}()

func knownQuirk(q string) bool {
	tag, value, valued := strings.Cut(q, "=")
	if !valued {
		return bareQuirks[tag]
	}
	check, ok := valuedQuirks[tag]
	if !ok {
		return false
	}
	return value != "" && check(value)
}

// HasQuirk reports whether the preset declares a bare quirk.
func (p Preset) HasQuirk(tag string) bool {
	for _, q := range p.Quirks {
		if q == tag {
			return true
		}
	}
	return false
}

// QuirkValue returns the value of a valued quirk, and whether it was declared.
func (p Preset) QuirkValue(tag string) (string, bool) {
	prefix := tag + "="
	for _, q := range p.Quirks {
		if v, ok := strings.CutPrefix(q, prefix); ok {
			return v, true
		}
	}
	return "", false
}

func parsePresets(raw []byte) (Presets, error) {
	d := yaml.NewDecoder(bytes.NewReader(raw))
	// KnownFields turns a typo into a build-breaking test failure. Without it a
	// misspelled field is dropped and the preset serves with a setting its
	// author believed they had made.
	d.KnownFields(true)
	var out Presets
	if err := d.Decode(&out); err != nil {
		return nil, fmt.Errorf("parse presets: %w", err)
	}
	return out, nil
}

var (
	embeddedOnce sync.Once
	embedded     Presets
	embeddedErr  error
)

// LoadPresets parses the embedded file. It is parsed once and shared, because
// the result is immutable and every provider row resolves through it.
func LoadPresets() (Presets, error) {
	embeddedOnce.Do(func() { embedded, embeddedErr = parsePresets(presetsYAML) })
	return embedded, embeddedErr
}

// Embedded is LoadPresets for callers with nowhere to put an error. A parse
// failure is a build-time defect the preset test catches, so returning nil here
// degrades to "no presets" rather than panicking in a live gateway.
func Embedded() Presets {
	ps, err := LoadPresets()
	if err != nil {
		return Presets{}
	}
	return ps
}
