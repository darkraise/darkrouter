// Command presetgen transcribes provider data into the two files
// internal/catalog embeds.
//
// It is build-time tooling and is never linked into the gateway. It reads
// OmniRoute's provider registry for structure (kind, base URL, auth) and its
// display constants for presentation (name, website, free tier), then joins
// models.dev for metadata. Nothing it reads becomes Go structure in internal/:
// this is a data transcription.
//
//	go run ./tools/presetgen \
//	  -omniroute /root/repositories-community/OmniRoute \
//	  -modelsdev /tmp/presetgen/modelsdev.json \
//	  -out-presets internal/catalog/presets.yaml \
//	  -out-snapshot internal/catalog/models_snapshot.json \
//	  -overrides internal/catalog/presets.overrides.yaml
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/darkraise/darkrouter/internal/catalog"
)

func main() {
	omni := flag.String("omniroute", "", "path to the OmniRoute checkout")
	modelsDev := flag.String("modelsdev", "", "path to a models.dev api.json snapshot")
	outPresets := flag.String("out-presets", "internal/catalog/presets.yaml", "generated preset file")
	outSnapshot := flag.String("out-snapshot", "internal/catalog/models_snapshot.json", "generated fallback snapshot")
	overrides := flag.String("overrides", "internal/catalog/presets.overrides.yaml", "hand-reviewed corrections")
	flag.Parse()
	if *omni == "" || *modelsDev == "" {
		log.Fatal("-omniroute and -modelsdev are both required")
	}

	doc, err := readModelsDev(*modelsDev)
	if err != nil {
		log.Fatal(err)
	}
	entries, dropped, err := scrapeRegistry(filepath.Join(*omni, "open-sse/config/providers/registry"),
		droppedFamilies(filepath.Join(*omni, "src/shared/constants/providers")))
	if err != nil {
		log.Fatal(err)
	}
	display, err := scrapeDisplay(filepath.Join(*omni, "src/shared/constants/providers"))
	if err != nil {
		log.Fatal(err)
	}

	presets := make(catalog.Presets, len(entries))
	joined := 0
	for _, e := range entries {
		p := e.toPreset(display[e.id])
		if _, ok := doc[e.id]; ok {
			p.ModelsDevID, p.NoModelsDev = e.id, false
			joined++
		} else {
			// An unjoined entry is exempted rather than left silent: spec §10
			// requires one or the other, and a missing join key would
			// otherwise look identical to a forgotten one.
			p.NoModelsDev = true
		}
		presets[e.id] = p
	}

	applied, err := applyOverrides(presets, *overrides)
	if err != nil {
		log.Fatal(err)
	}
	if err := writePresets(*outPresets, presets); err != nil {
		log.Fatal(err)
	}
	if err := writeSnapshot(*outSnapshot, doc); err != nil {
		log.Fatal(err)
	}
	log.Printf("presetgen: %d presets (%d dropped, %d joined to models.dev, %d overridden), %d models in snapshot",
		len(presets), dropped, joined, applied, countModels(doc))
}

// --- OmniRoute registry ---

type entry struct {
	id, format, baseURL, modelsURL, authType, authHeader string
}

var commentLine = regexp.MustCompile(`(?m)^\s*//.*$`)

// field matches a top-level registry field. Two spaces of indentation is what
// distinguishes a provider field from a field of a nested model entry, and it
// holds for both the literal and the builder form.
func field(src, name string) string {
	re := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(name) + `: "([^"]*)"`)
	if m := re.FindStringSubmatch(src); m != nil {
		return m[1]
	}
	return ""
}

func scrapeRegistry(dir string, drop map[string]bool) ([]entry, int, error) {
	dirs, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, fmt.Errorf("read registry: %w", err)
	}
	var out []entry
	dropped := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, d.Name(), "index.ts"))
		if err != nil {
			continue // a directory without an index.ts is not a provider
		}
		// Commented-out fields would otherwise win the first-match scrape.
		src := commentLine.ReplaceAllString(string(raw), "")
		e := entry{
			id:         field(src, "id"),
			format:     field(src, "format"),
			baseURL:    field(src, "baseUrl"),
			modelsURL:  field(src, "modelsUrl"),
			authType:   field(src, "authType"),
			authHeader: field(src, "authHeader"),
		}
		if e.id == "" {
			e.id = d.Name()
		}
		if e.format == "" {
			// The builder form omits it; the builder's name says what it is.
			e.format = "openai"
		}
		if reason := dropReason(e, drop); reason != "" {
			dropped++
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, dropped, nil
}

// dropReason implements spec §3.2's exclusions. It returns the empty string to
// keep an entry. Each rule is structural rather than a hand-maintained list, so
// a new OmniRoute entry of a dropped family is dropped without anyone noticing.
func dropReason(e entry, drop map[string]bool) string {
	switch {
	case drop[e.id]:
		return "family" // web-cookie, cloud-agent, upstream-proxy, system, search
	case e.authType == "cookie":
		return "cookie"
	case e.authType == "oauth":
		// An OAuth preset without a complete oauth: block is unusable — phase
		// 8's connect flow needs an authorize URL, a token URL, a client id
		// and scopes. OmniRoute carries at most a token URL, and its client
		// ids sit behind function calls, so transcribing these would mean
		// inventing endpoints. The one entry whose values are verifiable is
		// hand-written in presets.overrides.yaml.
		return "oauth"
	case strings.HasSuffix(e.id, "-web"):
		return "web"
	case e.baseURL == "":
		return "no base url"
	case !strings.HasPrefix(e.baseURL, "http://") && !strings.HasPrefix(e.baseURL, "https://"):
		return "scheme" // auggie://, devin://, zcode://, wss://
	case e.format != "openai" && e.format != "claude" && e.format != "gemini":
		return "format " + e.format
	}
	return ""
}

// droppedFamilies collects the ids of every entry in a family spec §3.2 drops
// wholesale.
func droppedFamilies(constants string) map[string]bool {
	out := map[string]bool{}
	idRe := regexp.MustCompile(`(?m)^\s{2,4}id: "([^"]+)"`)
	for _, f := range []string{"web-cookie.ts", "cloud-agent.ts", "upstream-proxy.ts", "system.ts", "search.ts"} {
		raw, err := os.ReadFile(filepath.Join(constants, f))
		if err != nil {
			continue
		}
		for _, m := range idRe.FindAllStringSubmatch(string(raw), -1) {
			out[m[1]] = true
		}
	}
	return out
}

var kindOf = map[string]string{"openai": "openaicompat", "claude": "anthropic", "gemini": "gemini"}

// chatSuffixes are the request paths OmniRoute stores on baseUrl. Darkrouter's
// base_url is the API root the adapter appends its own path to, so the suffix
// comes off. Longest first: "/v1/chat/completions" must not be trimmed to
// "/v1" by the shorter rule before the longer one is tried.
var chatSuffixes = []string{"/chat/completions", "/messages", "/responses", "/models"}

func (e entry) toPreset(d displayEntry) catalog.Preset {
	base := strings.TrimRight(e.baseURL, "/")
	for _, s := range chatSuffixes {
		if trimmed, ok := strings.CutSuffix(base, s); ok {
			base = trimmed
			break
		}
	}
	name := d.name
	if name == "" {
		name = e.id
	}
	p := catalog.Preset{
		Name:     name,
		Kind:     kindOf[e.format],
		BaseURL:  base,
		Auth:     authOf(e),
		Surfaces: []string{"llm"},
		FreeTier: d.free,
		Website:  d.website,
		Quirks:   []string{},
	}
	if e.modelsURL != "" {
		p.ModelsURL = e.modelsURL
	}
	return p
}

func authOf(e entry) catalog.Auth {
	if e.authType == "oauth" {
		return catalog.Auth{Style: "oauth"}
	}
	if e.authType == "none" {
		return catalog.Auth{Style: "none"}
	}
	switch strings.ToLower(e.authHeader) {
	case "bearer", "authorization", "":
		return catalog.Auth{Style: "bearer"}
	case "x-api-key":
		return catalog.Auth{Style: "x-api-key"}
	case "x-goog-api-key":
		return catalog.Auth{Style: "api-key", Header: "x-goog-api-key"}
	case "key":
		return catalog.Auth{Style: "query-param", QueryParam: "key"}
	default:
		// A one-off header name is still the api-key style; only the spelling
		// differs, and carrying it is what stops each one becoming a code
		// branch.
		return catalog.Auth{Style: "api-key", Header: e.authHeader}
	}
}

// --- OmniRoute display constants ---

type displayEntry struct {
	name, website string
	free          bool
}

func scrapeDisplay(dir string) (map[string]displayEntry, error) {
	files, err := filepath.Glob(filepath.Join(dir, "apikey", "*.ts"))
	if err != nil {
		return nil, err
	}
	for _, f := range []string{"local.ts", "noauth.ts", "oauth.ts"} {
		files = append(files, filepath.Join(dir, f))
	}
	// Each entry opens with `<key>: {` and closes at the next line with the
	// same indentation, so splitting on the id field and reading forward to the
	// next id is enough for three scalar fields.
	block := regexp.MustCompile(`(?s)id: "([^"]+)",(.*?)(?:\n  \},|\z)`)
	out := map[string]displayEntry{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		src := commentLine.ReplaceAllString(string(raw), "")
		for _, m := range block.FindAllStringSubmatch(src, -1) {
			body := m[2]
			out[m[1]] = displayEntry{
				name:    scalar(body, "name"),
				website: scalar(body, "website"),
				free:    strings.Contains(body, "hasFree: true"),
			}
		}
	}
	return out, nil
}

func scalar(body, name string) string {
	re := regexp.MustCompile(regexp.QuoteMeta(name) + `:\s*"([^"]*)"`)
	if m := re.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

// --- overrides ---

// applyOverrides merges the hand-reviewed file over the generated set, field by
// field where the override is non-zero. An override for an id the generator did
// not produce is added outright, which is how hand-written entries such as the
// phase-8 kinds enter the file.
func applyOverrides(presets catalog.Presets, path string) (int, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read overrides: %w", err)
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	var over catalog.Presets
	if err := d.Decode(&over); err != nil {
		return 0, fmt.Errorf("parse overrides: %w", err)
	}
	var incomplete []string
	for id, o := range over {
		base, ok := presets[id]
		if !ok {
			// A whole entry may be added outright — that is how the phase-8
			// kinds and the local runtimes enter the file. A partial one
			// cannot: it means the correction named a preset the generator
			// never produced, and inserting it yields a nameless, kindless
			// entry that the preset test then rejects a hundred lines later.
			if o.Name == "" || o.Kind == "" || o.Auth.Style == "" || len(o.Surfaces) == 0 {
				incomplete = append(incomplete, id)
				continue
			}
			presets[id] = o
			continue
		}
		if o.Name != "" {
			base.Name = o.Name
		}
		if o.Kind != "" {
			base.Kind = o.Kind
		}
		if o.BaseURL != "" {
			base.BaseURL = o.BaseURL
		}
		if o.Auth.Style != "" {
			base.Auth = o.Auth
		}
		if len(o.Surfaces) > 0 {
			base.Surfaces = o.Surfaces
		}
		if o.ModelsDevID != "" {
			base.ModelsDevID, base.NoModelsDev = o.ModelsDevID, false
		}
		if o.NoModelsDev {
			base.NoModelsDev, base.ModelsDevID = true, ""
		}
		if o.Website != "" {
			base.Website = o.Website
		}
		if o.FreeTier {
			base.FreeTier = true
		}
		if len(o.Quirks) > 0 {
			base.Quirks = o.Quirks
		}
		if o.ModelsURL != "" {
			base.ModelsURL = o.ModelsURL
		}
		if len(o.ModelAliases) > 0 {
			base.ModelAliases = o.ModelAliases
		}
		if len(o.ModelTraits) > 0 {
			base.ModelTraits = o.ModelTraits
		}
		if o.CapabilityProbe != "" {
			base.CapabilityProbe = o.CapabilityProbe
		}
		if o.OAuth != nil {
			base.OAuth = o.OAuth
		}
		presets[id] = base
	}
	if len(incomplete) > 0 {
		sort.Strings(incomplete)
		return 0, fmt.Errorf(
			"overrides target presets the generator did not produce, and are too "+
				"partial to stand alone: %s. Either correct the id or write a "+
				"complete entry (name, kind, auth.style, surfaces)",
			strings.Join(incomplete, ", "))
	}
	return len(over), nil
}

// --- output ---

const presetHeader = `# Generated by tools/presetgen. Do not hand-edit.
#
# Structure (kind, base URL, auth) is transcribed from OmniRoute's provider
# registry; presentation (name, website, free tier) from its display constants;
# the models.dev join key from https://models.dev/api.json. Corrections live in
# presets.overrides.yaml and are re-applied on every run.
#
# quirks is a closed vocabulary; an unknown entry fails the preset test.

`

func writePresets(path string, p catalog.Presets) error {
	var buf bytes.Buffer
	buf.WriteString(presetHeader)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(p); err != nil {
		return fmt.Errorf("encode presets: %w", err)
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// --- models.dev ---

type mdModel struct {
	Cost struct {
		Input     *float64 `json:"input"`
		Output    *float64 `json:"output"`
		CacheRead *float64 `json:"cache_read"`
	} `json:"cost"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	ToolCall   bool `json:"tool_call"`
	Reasoning  bool `json:"reasoning"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
}

type mdProvider struct {
	Models map[string]mdModel `json:"models"`
}

func readModelsDev(path string) (map[string]mdProvider, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read models.dev snapshot: %w", err)
	}
	var doc map[string]mdProvider
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse models.dev snapshot: %w", err)
	}
	if len(doc) == 0 {
		return nil, fmt.Errorf("models.dev snapshot is empty")
	}
	return doc, nil
}

func countModels(doc map[string]mdProvider) int {
	n := 0
	for _, p := range doc {
		n += len(p.Models)
	}
	return n
}

// writeSnapshot emits the trimmed fallback. It carries only the seven fields
// the merge reads, which is what keeps a 4.2 MB document under a megabyte
// embedded — and it is what makes "starts with no access to models.dev" true
// on a first run rather than only after one successful sync.
func writeSnapshot(path string, doc map[string]mdProvider) error {
	type out struct {
		InputMicros     int64 `json:"i,omitempty"`
		OutputMicros    int64 `json:"o,omitempty"`
		CacheReadMicros int64 `json:"c,omitempty"`
		Context         int   `json:"w,omitempty"`
		MaxOutput       int   `json:"m,omitempty"`
		Tools           bool  `json:"t,omitempty"`
		Reasoning       bool  `json:"r,omitempty"`
		Vision          bool  `json:"v,omitempty"`
	}
	trimmed := map[string]map[string]out{}
	for pid, p := range doc {
		models := map[string]out{}
		for mid, m := range p.Models {
			o := out{
				Context:   m.Limit.Context,
				MaxOutput: m.Limit.Output,
				Tools:     m.ToolCall,
				Reasoning: m.Reasoning,
			}
			// Dollars per million to micro-dollars per million. Rounding
			// rather than truncating: 0.0000005 differences in the source
			// float must not lose a whole micro-dollar.
			if m.Cost.Input != nil {
				o.InputMicros = int64(*m.Cost.Input*1_000_000 + 0.5)
			}
			if m.Cost.Output != nil {
				o.OutputMicros = int64(*m.Cost.Output*1_000_000 + 0.5)
			}
			if m.Cost.CacheRead != nil {
				o.CacheReadMicros = int64(*m.Cost.CacheRead*1_000_000 + 0.5)
			}
			for _, in := range m.Modalities.Input {
				if in == "image" {
					o.Vision = true
				}
			}
			models[mid] = o
		}
		if len(models) > 0 {
			trimmed[pid] = models
		}
	}
	buf, err := json.Marshal(trimmed)
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	return os.WriteFile(path, buf, 0o644)
}
