// Command presetgen transcribes provider data into the two files
// internal/catalog embeds.
//
// It is build-time tooling and is never linked into the gateway. It reads
// OmniRoute's provider registry for structure (kind, base URL, auth) and its
// display constants for presentation (name, website, free tier), then joins
// models.dev for metadata. Nothing it reads becomes Go structure in internal/:
// this is a data transcription.
//
// Quirks come from neither source. They are carried over from the presets
// file being regenerated, then presets.overrides.yaml is applied on top, so a
// run never wipes them.
//
//	go run ./tools/presetgen \
//	  -omniroute /root/repositories-community/OmniRoute \
//	  -modelsdev /tmp/presetgen/modelsdev.json \
//	  -litellm /tmp/presetgen/litellm.json \
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
	liteLLM := flag.String("litellm", "", "path to a LiteLLM model_prices_and_context_window.json snapshot")
	outPresets := flag.String("out-presets", "internal/catalog/presets.yaml", "generated preset file")
	outSnapshot := flag.String("out-snapshot", "internal/catalog/models_snapshot.json", "generated fallback snapshot")
	overrides := flag.String("overrides", "internal/catalog/presets.overrides.yaml", "hand-reviewed corrections")
	outFree := flag.String("out-free", "internal/catalog/free_models.json", "generated free-model catalog")
	outIcons := flag.String("out-icons", "web/public/providers", "directory the copied provider logos land in")
	outIconManifest := flag.String("out-icon-manifest", "web/src/features/providers/provider-assets.ts", "generated logo manifest")
	brandMarks := flag.String("brand-marks", "web/src/features/providers/brand-marks.ts", "marks already drawn from @lobehub/icons")
	nineRouter := flag.String("ninerouter", "", "path to the 9router checkout")
	outConflicts := flag.String("out-conflicts", "internal/catalog/presetgen-conflicts.md", "generated review table")
	outProvenance := flag.String("out-provenance", "internal/catalog/provenance.yaml", "generated field-origin manifest")
	omniSHA := flag.String("omniroute-sha", "", "OmniRoute commit the registry was read from")
	nineSHA := flag.String("ninerouter-sha", "", "9router commit the registry was read from")
	flag.Parse()
	if *omni == "" || *modelsDev == "" || *liteLLM == "" {
		log.Fatal("-omniroute, -modelsdev and -litellm are all required")
	}

	doc, err := readModelsDev(*modelsDev)
	if err != nil {
		log.Fatal(err)
	}
	priceIndex, err := readLiteLLMProviders(*liteLLM)
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

	var nine []nineEntry
	if *nineRouter != "" {
		nine, err = scrapeNineRouter(filepath.Join(*nineRouter, "open-sse/providers/registry"))
		if err != nil {
			log.Fatal(err)
		}
	}
	m := mergeSources(entries, display, nine)
	presets := m.Presets

	joined := 0
	for id, p := range presets {
		if _, ok := doc[id]; ok {
			p.ModelsDevID, p.NoModelsDev = id, false
			joined++
		} else {
			// An unjoined entry is exempted rather than left silent: spec §10
			// requires one or the other, and a missing join key would
			// otherwise look identical to a forgotten one.
			p.NoModelsDev = true
		}
		presets[id] = p
	}
	priced := joinPriceIndex(presets, priceIndex)

	carried, err := carryQuirks(presets, *outPresets)
	if err != nil {
		log.Fatal(err)
	}
	applied, err := applyOverrides(presets, *overrides)
	if err != nil {
		log.Fatal(err)
	}
	markOverridden(&m, *overrides)
	if err := writePresets(*outPresets, presets); err != nil {
		log.Fatal(err)
	}
	if err := writeConflicts(*outConflicts, m.Conflicts); err != nil {
		log.Fatal(err)
	}
	if err := writeProvenance(*outProvenance, m, manifestMeta{
		OmniRouteSHA:  *omniSHA,
		NineRouterSHA: *nineSHA,
	}); err != nil {
		log.Fatal(err)
	}
	log.Printf("presetgen: %d presets from 9router only, %d conflicts recorded",
		len(nine), len(m.Conflicts))
	if err := writeSnapshot(*outSnapshot, doc); err != nil {
		log.Fatal(err)
	}
	free, err := scrapeFreeCatalog(filepath.Join(*omni, "open-sse/config/freeModelCatalog.data.ts"))
	if err != nil {
		log.Fatal(err)
	}
	unmatched := 0
	for pid := range free.Providers {
		if _, ok := presets[pid]; !ok {
			unmatched++
		}
	}
	if err := writeFreeCatalog(*outFree, free); err != nil {
		log.Fatal(err)
	}
	log.Printf("presetgen: %d presets (%d dropped, %d joined to models.dev, %d joined to litellm, %d overridden, %d kept their quirks), %d models in snapshot",
		len(presets), dropped, joined, priced, applied, carried, countModels(doc))
	log.Printf("presetgen: free catalog curated %s, %d models across %d providers (%d providers match no preset)",
		free.CuratedAt, countFree(free), len(free.Providers), unmatched)

	marked, err := countMarked(*brandMarks, presets)
	if err != nil {
		log.Fatal(err)
	}
	icons, err := copyIcons(filepath.Join(*omni, "public/providers"), *outIcons, *brandMarks, presets)
	if err != nil {
		log.Fatal(err)
	}
	if err := writeIconManifest(*outIconManifest, icons); err != nil {
		log.Fatal(err)
	}
	log.Printf("presetgen: %d provider logos copied (%d presets already draw a brand mark, %d have neither)",
		len(icons), marked, len(presets)-marked-len(icons))
}

// --- OmniRoute registry ---

type entry struct {
	id, format, baseURL, modelsURL, authType, authHeader, anonKey string
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
			anonKey:    field(src, "anonymousApiKey"),
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
// keep an entry. Every rule but one is structural rather than a
// hand-maintained list, so a new OmniRoute entry of a dropped family is
// dropped without anyone noticing.
func dropReason(e entry, drop map[string]bool) string {
	switch {
	case drop[e.id]:
		return "family" // web-cookie, cloud-agent, upstream-proxy, system, search
	case unservable[e.id] != "":
		return "unservable: " + unservable[e.id]
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
var chatSuffixes = []string{"/chat/completions", "/embeddings", "/messages", "/responses", "/models"}

func (e entry) toPreset(d displayEntry) catalog.Preset {
	base := trimAPISuffix(e.baseURL)
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
	// Live evidence outranks the upstream field. See keyRequired.
	if keyRequired[e.id] != "" {
		return catalog.Auth{Style: "bearer"}
	}
	if e.authType == "none" {
		return catalog.Auth{Style: "none"}
	}
	// A published credential, not a secret: OmniRoute's anonymousApiKey is the
	// string the vendor documents so that anybody can call. Transcribing it
	// keeps the provider addable with nothing to paste, which is the whole
	// difference between AI Horde being a no-auth provider and being a
	// provider whose key an operator has to go and find.
	if e.anonKey != "" {
		return catalog.Auth{Style: "anonymous", Key: e.anonKey}
	}
	// A provider that serves an unauthenticated request and serves a
	// credentialled one better. Transcribed rather than flattened to bearer:
	// as bearer the console demands a key for a gateway that answers without
	// one, which is a string the operator has to invent.
	if e.authType == "optional" {
		return catalog.Auth{Style: "optional"}
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

// --- quirks carried across runs ---

// carryQuirks copies each preset's quirks from the file being regenerated.
// Neither source the generator reads knows what a quirk is, so without this
// every run silently wiped them; an override still wins where it declares
// any. The count of presets that kept quirks is returned for the log line.
func carryQuirks(presets catalog.Presets, existingPath string) (int, error) {
	raw, err := os.ReadFile(existingPath)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read existing presets: %w", err)
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	var existing catalog.Presets
	if err := d.Decode(&existing); err != nil {
		return 0, fmt.Errorf("parse existing presets: %w", err)
	}
	carried := 0
	for id, old := range existing {
		p, ok := presets[id]
		if !ok || len(old.Quirks) == 0 || len(p.Quirks) > 0 {
			continue
		}
		p.Quirks = append([]string(nil), old.Quirks...)
		presets[id] = p
		carried++
	}
	return carried, nil
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
		if o.LiteLLMID != "" {
			base.LiteLLMID, base.NoLiteLLM = o.LiteLLMID, false
		}
		if o.NoLiteLLM {
			base.NoLiteLLM, base.LiteLLMID = true, ""
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
		if o.Publisher != "" {
			base.Publisher = o.Publisher
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
# quirks is a closed vocabulary; an unknown entry fails the preset test. The
# quirks in this file survive a regeneration: the generator carries them over
# from the previous copy of the file, and presets.overrides.yaml wins where it
# declares any.

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
		Input      *float64 `json:"input"`
		Output     *float64 `json:"output"`
		CacheRead  *float64 `json:"cache_read"`
		CacheWrite *float64 `json:"cache_write"`
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

// readLiteLLMProviders collects the litellm_provider values the price index
// publishes, which is the set a preset's join key can name.
func readLiteLLMProviders(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read litellm snapshot: %w", err)
	}
	var entries map[string]struct {
		Provider string `json:"litellm_provider"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse litellm snapshot: %w", err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		if e.Provider != "" {
			out[e.Provider] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("litellm snapshot names no providers")
	}
	return out, nil
}

// joinPriceIndex sets each preset's LiteLLM key from the index's own provider
// set, exactly as the models.dev key is set from that document.
//
// Exempting every preset and letting the overrides file clear the exemption
// would make the exemption meaningless: a preset added by a regeneration would
// arrive already excused, and the test that demands a key or a deliberate
// exemption could never fail. Where the two indexes spell a vendor differently
// — fireworks is fireworks_ai there — the miss is real and the overrides file
// supplies the key, which is a correction rather than a blanket.
func joinPriceIndex(presets catalog.Presets, providers map[string]bool) int {
	joined := 0
	for id, p := range presets {
		if providers[id] {
			p.LiteLLMID, p.NoLiteLLM = id, false
			joined++
		} else {
			p.LiteLLMID, p.NoLiteLLM = "", true
		}
		presets[id] = p
	}
	return joined
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
		InputMicros      int64 `json:"i,omitempty"`
		OutputMicros     int64 `json:"o,omitempty"`
		CacheReadMicros  int64 `json:"c,omitempty"`
		CacheWriteMicros int64 `json:"x,omitempty"`
		Context          int   `json:"w,omitempty"`
		MaxOutput        int   `json:"m,omitempty"`
		Tools            bool  `json:"t,omitempty"`
		Reasoning        bool  `json:"r,omitempty"`
		Vision           bool  `json:"v,omitempty"`
		// ZeroPriced says models.dev published a price and it was zero. Every
		// other field is omitempty to keep the snapshot under a megabyte,
		// which erases the difference between a free model and one nobody has
		// priced -- and the free-models filter turns on exactly that
		// difference. Carried only for the handful that need it.
		ZeroPriced bool `json:"z,omitempty"`
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
			if m.Cost.CacheWrite != nil {
				o.CacheWriteMicros = int64(*m.Cost.CacheWrite*1_000_000 + 0.5)
			}
			// Priced, and the price rounds to nothing. The same test
			// ParseModelsDev applies to the live document: a cost key that is
			// present is knowledge, whatever number it carries.
			o.ZeroPriced = (m.Cost.Input != nil || m.Cost.Output != nil) &&
				o.InputMicros == 0 && o.OutputMicros == 0
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

// --- the curated free-model catalog ---

// scrapeFreeCatalog transcribes the hand-curated free-tier catalog.
//
// Free-tier membership is a curated fact rather than a derived one: no price
// index can say which models a vendor's free tier covers, because the tier is
// a property of the account and not of the model. OmniRoute maintains that
// list against provider documentation, and this copies it rather than
// inventing a second one that would drift from it.
//
// Parsed by internal/catalog, which reads the same file at runtime when the
// daily sync fetches it from GitHub.
func scrapeFreeCatalog(path string) (catalog.FreeCatalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return catalog.FreeCatalog{}, fmt.Errorf("read free catalog: %w", err)
	}
	c, err := catalog.ParseFreeCatalog(raw)
	if err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func countFree(c catalog.FreeCatalog) int {
	n := 0
	for _, models := range c.Providers {
		n += len(models)
	}
	return n
}

func writeFreeCatalog(path string, c catalog.FreeCatalog) error {
	buf, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode free catalog: %w", err)
	}
	return os.WriteFile(path, buf, 0o644)
}

// --- provider logos ---

// unservable names registry entries darkrouter cannot serve through its
// generic adapters. Each is `format: openai` upstream, but OmniRoute reaches
// it through a bespoke executor — so the preset it would generate is a
// provider that cannot answer a request the console makes.
//
// Verified 2026-08-28 by fetching the exact URL darkrouter's discovery calls,
// `GET {base_url}/models`:
//
//	chipotle               certificate for another host, then 404
//	cloudflare-playground  200 text/html — the playground page
//	theoldllm              text/html
//
// The g4f.space gateways list models fine but refuse every completion, which
// discovery alone does not reveal. Verified 2026-08-29 by posting a chat
// completion with no credential, the way an authType: optional preset is
// connected:
//
//	{"error":{"message":"No cake credits. Bake proof-of-work cakes at
//	g4f.dev/chat to earn anonymous usage…","type":"insufficient_credits"}}
//
// The credit is minted by a proof-of-work run in their browser chat, so there
// is no credential an operator can paste and no key-less path either. The
// preset would sit in the console's no-auth list promising a provider that
// answers nothing.
//
// Listing them by hand rather than by a rule: "has a bespoke executor" also
// covers pollinations, which answers a model listing perfectly well.
// keyRequired names entries OmniRoute records as authType: optional whose
// upstream refuses an uncredentialled completion.
//
// Optional means one thing in OmniRoute, where a pool of accounts may or may
// not be attached, and another in darkrouter's console, where it puts a
// provider in the No auth group and promises an operator that adding it is the
// whole of the setup. A provider that 401s the first request has broken that
// promise before the operator has done anything wrong.
//
// A public model listing is not evidence of a usable provider — that was the
// mistake the g4f gateways were removed for, and every entry here lists its
// models to anyone who asks. What settles it is a completion with no
// credential, verified 2026-08-29:
//
//	hackclub      401 Authentication required
//	kilo-gateway  401 PAID_MODEL_AUTH_REQUIRED: You need to sign in to use this model
//	naga-ac       401 authentication_required (its :free models too)
//	pollinations  401 A valid API key is required. Get one at enter.pollinations.ai/key
//
// bearer rather than removed: each is perfectly usable with a key, unlike the
// g4f gateways whose credential could not be obtained at all. The console asks
// for that key instead of pretending none is needed.
var keyRequired = map[string]string{
	"hackclub":     "401s an uncredentialled completion",
	"kilo-gateway": "401 PAID_MODEL_AUTH_REQUIRED without an account",
	"naga-ac":      "401s an uncredentialled completion, free models included",
	"pollinations": "401s without a key from enter.pollinations.ai",
}

var unservable = map[string]string{
	"chipotle":              "serves an Azure gateway 404 under a certificate for another host",
	"cloudflare-playground": "serves the playground web app, not an API",
	"theoldllm":             "serves HTML, not an API",
	"g4f-gemini":            "completions need proof-of-work credits, not a credential",
	"g4f-groq":              "completions need proof-of-work credits, not a credential",
	"g4f-nvidia":            "completions need proof-of-work credits, not a credential",
	"g4f-ollama":            "completions need proof-of-work credits, not a credential",
	"g4f-pollinations":      "completions need proof-of-work credits, not a credential",
}

// markEntry matches one line of the generated brand-marks map. A preset that
// already draws a mark from @lobehub/icons needs no file copied: the mark wins
// at render time, and shipping the logo too would be a megabyte of assets
// nothing ever requests.
var markEntry = regexp.MustCompile(`(?m)^\s+"?([a-zA-Z0-9._-]+)"?:\s*\{\s*Mark`)

// darkInk matches a colour token that is black or near enough. An SVG drawn
// only in those is monochrome, and monochrome dark ink disappears on the dark
// canvas -- which is what iconAsset.Mono exists to tell the console.
var darkInk = regexp.MustCompile(`(?i)#(000|111|222|000000|111111|1a1a1a|222222)\b`)

var anyColour = regexp.MustCompile(`(?i)#[0-9a-f]{3,8}\b`)

type iconAsset struct {
	Preset string
	File   string
	Mono   bool
}

// copyIcons brings OmniRoute's provider logos across for the presets that have
// no brand mark of their own.
//
// A logo is a fact about a vendor that nobody can derive: OmniRoute collected
// them by hand, and the alternative to copying is 110 monogram tiles on a
// screen whose whole job is recognition.
func copyIcons(src, dst, brandMarksPath string, presets catalog.Presets) ([]iconAsset, error) {
	marks := map[string]bool{}
	if raw, err := os.ReadFile(brandMarksPath); err == nil {
		for _, m := range markEntry.FindAllSubmatch(raw, -1) {
			marks[string(m[1])] = true
		}
	} else {
		return nil, fmt.Errorf("read brand marks: %w", err)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, fmt.Errorf("read logos: %w", err)
	}
	available := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		// One file per preset, and .svg wins: it is the one that stays sharp
		// at every size the console draws a tile at.
		if existing, ok := available[stem]; ok && filepath.Ext(existing) == ".svg" {
			continue
		}
		available[stem] = name
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, fmt.Errorf("make logo directory: %w", err)
	}
	// Cleared rather than merged: a preset dropped from the registry must not
	// leave its logo behind to be embedded in every future binary.
	old, _ := os.ReadDir(dst)
	for _, e := range old {
		if !e.IsDir() {
			_ = os.Remove(filepath.Join(dst, e.Name()))
		}
	}

	ids := make([]string, 0, len(presets))
	for id := range presets {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := []iconAsset{}
	for _, id := range ids {
		if marks[id] {
			continue
		}
		name, ok := available[id]
		if !ok {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return nil, fmt.Errorf("read logo %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dst, name), raw, 0o644); err != nil {
			return nil, fmt.Errorf("write logo %s: %w", name, err)
		}
		out = append(out, iconAsset{Preset: id, File: name, Mono: monochromeDark(name, raw)})
	}
	return out, nil
}

// monochromeDark reports whether an SVG is drawn only in black ink, which the
// console has to invert on its dark canvas or the logo is a black square on a
// near-black tile. A raster file is never claimed: inverting a photograph
// produces a negative, not a legible mark.
func monochromeDark(name string, raw []byte) bool {
	if filepath.Ext(name) != ".svg" {
		return false
	}
	colours := anyColour.FindAll(raw, -1)
	if len(colours) == 0 {
		// No colour token at all means the ink is currentColor or the default
		// black, both of which need the same treatment.
		return true
	}
	for _, c := range colours {
		if !darkInk.Match(c) {
			return false
		}
	}
	return true
}

func writeIconManifest(path string, icons []iconAsset) error {
	var b bytes.Buffer
	b.WriteString(`// Generated by tools/presetgen. Do not edit.
//
// Which presets have a logo file in web/public/providers, so ProviderIcon can
// reach for one without a request that might 404. A preset absent here has
// either a brand mark from @lobehub/icons or the monogram, in that order.
//
// Mono marks an SVG drawn only in black ink, which the dark canvas inverts:
// the file carries its own colour and cannot adapt the way a currentColor mark
// does, and a black mark on a near-black tile is an empty tile.

export type ProviderAsset = { file: string; mono?: boolean }

export const PROVIDER_ASSETS: Record<string, ProviderAsset> = {
`)
	for _, a := range icons {
		b.WriteString(fmt.Sprintf("  %q: { file: %q", a.Preset, a.File))
		if a.Mono {
			b.WriteString(", mono: true")
		}
		b.WriteString(" },\n")
	}
	b.WriteString("}\n")
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// countMarked is how many presets already draw a mark from @lobehub/icons.
func countMarked(brandMarksPath string, presets catalog.Presets) (int, error) {
	raw, err := os.ReadFile(brandMarksPath)
	if err != nil {
		return 0, fmt.Errorf("read brand marks: %w", err)
	}
	n := 0
	for _, m := range markEntry.FindAllSubmatch(raw, -1) {
		if _, ok := presets[string(m[1])]; ok {
			n++
		}
	}
	return n, nil
}
