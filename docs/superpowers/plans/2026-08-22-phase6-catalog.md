# Darkrouter Phase 6 — Catalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Turn "what each provider offers" from a guess into data — shipped presets, synced models.dev metadata, and live discovery merged into one immutable snapshot the router consults — so capability filtering, pricing, context windows, and Anthropic's per-generation request shape all read facts instead of parsing model names.

**Architecture:** Three sources feed one table. Embedded `presets.yaml` supplies kind, base URL, auth style, quirks and surfaces per named upstream; a 12-hourly models.dev sync supplies capabilities, context windows and pricing; a 15-minute discovery sweep supplies existence. All three land in the `models` table, which a `catalog.Store` reads into an immutable `Snapshot` swapped atomically behind an `atomic.Pointer`. The router takes one snapshot per request, which is what keeps `Resolve` pure. The three model-generation booleans that `internal/adapter/anthropic/build.go` currently reads off the model name move onto the catalog entry and travel to the adapter through `adapter.Target`, so the name-matching table is deleted rather than extended.

**Tech Stack:** Go 1.26.1, the Phase 1–5 dependencies. `gopkg.in/yaml.v3` for the preset file (already a dependency). No new modules. `CGO_ENABLED=0` for the shipped binary; `CGO_ENABLED=1` locally so `-race` works.

**Spec:** `docs/superpowers/specs/2026-08-22-darkrouter-phase6-catalog.md` (master design: `docs/superpowers/specs/2026-08-22-darkrouter-design.md`; the design wins wherever they disagree). Read `docs/superpowers/specs/2026-08-22-spec-review-findings.md` §5.7 when a decision looks arbitrary.

## Global Constraints

- Go 1.26. Module path `github.com/darkraise/darkrouter`. The shipped binary builds with `CGO_ENABLED=0`.
- English only in code, comments, commits, and errors.
- Commits are `<type>(<scope>): <subject>`, subject at most 50 characters, imperative, no trailing period.
- **Every task ends green.** `export PATH=$PATH:/usr/local/go/bin` first; the toolchain is not on `PATH`. Run `go test ./... -race -count=1`, `go vet ./...`, and `gofmt -l .` before committing. `gofmt -l .` must print nothing.
- **Ports 8080 and 8081 are occupied by an unrelated application.** Every smoke run binds 18080 (proxy) and 18081 (admin). Never kill a process this plan did not start.
- **`DARKROUTER_MASTER_KEY` must be set for any run of the binary**, including smoke tests. Credentials are encrypted at rest from Phase 2 on and startup fails without it. A throwaway value is fine: `DARKROUTER_MASTER_KEY=phase6-smoke`.
- **Darkrouter must start and serve with no outbound access to models.dev.** A metadata CDN being down is never a reason the gateway will not boot. Every sync failure path leaves the previous cache in place and logs a warning.
- **A discovery failure never changes a model's state.** Only a *successful* listing that omits a previously seen model is evidence of removal. Confusing the two either breaks every alias on one flaky probe or leaves retired models routable forever.
- **Inferred capabilities pass the router's capability filter with a warning**, per master design §6.4. Hard-filtering on guessed metadata makes every discovered local model refuse the tool requests Claude Code always sends. Known capabilities filter normally.
- **Prices are integer micro-dollars per million tokens.** models.dev publishes USD per million as a float. Micro-dollars *per token* truncates a $0.14/M model to zero, which is why master design §11 fixes the unit. `$0.14/M` must round-trip to `140000`.
- **The quirk vocabulary is closed.** An unknown quirk in `presets.yaml` fails the preset test rather than being ignored at runtime. Growing it is deliberately a two-part change: a vocabulary entry plus an adapter branch.
- **No OmniRoute code, structure, or abstraction crosses over.** Phase 6 transcribes data only. The generator under `tools/` is build-time and reads OmniRoute's files; nothing it reads becomes Go structure in `internal/`.
- Every new package gets a package comment. Comments explain why, never what.

## Where the source data actually lives

The spec's §3.2 names `src/shared/constants/providers/` as the transcription source. That directory is real but it is only the **display half** — `id`, `alias`, `name`, `icon`, `color`, `website`, `serviceKinds`, `hasFree`, and prose `authHint`/`apiHint`. It carries no base URL and no structured auth style.

The **structural half** is `open-sse/config/providers/registry/<id>/index.ts`, one directory per provider, carrying `id`, `alias`, `format`, `executor`, `baseUrl`, `authType`, `authHeader`, optional `modelsUrl`, optional `unsupportedParams`, and a `models:` array. The generator reads both trees and joins them on `id`.

Verified on 2026-08-22 at `/root/repositories-community/OmniRoute` (MIT licensed, `Copyright (c) 2026 diegosouzapw`):

| Fact | Value |
|---|---|
| Registry provider directories | 246 |
| `format: "openai"` / `"claude"` / `"gemini"` | 200 / 14 / 4 |
| `authType: "apikey"` / `"oauth"` / `"optional"` / `"none"` / `"cookie"` | 181 / 22 / 11 / 9 / 1 |
| Entries written as a plain literal (`: RegistryEntry = {`) | ~199 |
| Entries written through `buildOpenAiCompatibleRegistryEntry({`, same 2-space field indentation | 47 |
| Display-tree ids that join to a registry id | 228 |

`https://models.dev/api.json` was fetched the same day: 4,264,826 bytes, 193 providers, 167 with an `api` base URL, 7,246 models of which 6,824 carry `cost` and 7,119 carry `limit.context`. 51 registry ids join to a models.dev provider key exactly; the rest need either an explicit `models_dev_id` or an exemption.

## Two facts that decide the traits design

**models.dev cannot express Anthropic's thinking shape.** Its `reasoning_options` lists `effort` for `claude-opus-4-5`, but Phase 4 verified live on 2026-08-22 that `thinking: {type:"adaptive"}` returns a 400 on Claude 4.5 and earlier. The two are different controls that share a word: Opus 4.5 accepts `output_config.effort` without accepting adaptive thinking. Deriving `adaptive` from `reasoning_options` would therefore break a live-verified behavior on the single most-used Anthropic model. **Traits are preset-declared data, not a models.dev derivation.**

**Sampling freedom, by contrast, has three independent sources that agree.** models.dev's `temperature: false`, OmniRoute's `unsupportedParams: ["temperature","top_p","top_k"]`, and Darkrouter's own `freeSampling` table name exactly the same five generations: `fable-5`, `opus-5`, `opus-4-7`, `opus-4-8`, `sonnet-5`. The Anthropic preset's `model_traits` block transcribes the Phase 4 table verbatim; the agreement is the evidence it is right.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/catalog/catalog.go` | Existing `Source`, `Capabilities`, `Model`, `Reader`; grows `State`, `Traits`, `Pricing`, limits |
| `internal/catalog/preset.go` | Preset schema, the embedded `presets.yaml`, the closed quirk vocabulary, `LoadPresets` |
| `internal/catalog/presets.yaml` | Generated then reviewed. Provider-level data only |
| `internal/catalog/modelsdev.go` | models.dev document shape, field mapping, the embedded fallback snapshot |
| `internal/catalog/models_snapshot.json` | Generated. Trimmed models.dev fallback so a cold start with no network still knows prices and limits |
| `internal/catalog/normalize.go` | Model-ID normalization and the `(models_dev_id, normalized_id)` join |
| `internal/catalog/merge.go` | The per-field precedence table from spec §7 |
| `internal/catalog/snapshot.go` | Immutable `Snapshot`, the `atomic.Pointer` `Store`, `Lookup` / `Offering` / `Search` |
| `internal/catalog/probe.go` | Per-kind discovery request building and response parsing. Pure |
| `internal/catalog/discovery.go` | The discovery worker: global concurrency cap, credential choice, 401 cooling, on-demand trigger |
| `internal/catalog/lifecycle.go` | The three-strike state machine over `live` / `stale` / `removed_upstream` |
| `internal/catalog/sync.go` | The 12-hourly models.dev sync worker |
| `internal/store/migrations/0002_catalog.sql` | `state` vocabulary, the surfaces default, discovery bookkeeping columns |
| `internal/store/catalog.go` | Every SQL statement touching `models`, `model_overrides`, and `provider_discovery` |
| `internal/provider/provider.go` | `Provider` grows `Preset` and `AuthStyle` |
| `internal/adapter/adapter.go` | `Target` grows `Info ModelInfo` — a plain struct, so `adapter` never imports `catalog` |
| `internal/adapter/anthropic/build.go` | Reads `t.Info`; the `generations` table and `traitsFor` are deleted |
| `internal/adapter/xlate/params.go` | `RequiredMaxTokens` takes the model's real cap; `EffortBudget`'s clamp goes live |
| `internal/router/filter.go` | Capability filtering becomes selective and emits the inferred-pass warning |
| `internal/server/server.go` | `/v1/models` and `/v1beta/models` read the catalog; the two workers join `Run` |
| `tools/presetgen/` | Build-time generator. Reads OmniRoute plus a models.dev snapshot, emits both generated files |

## Why `adapter` must not import `catalog`

`health` imports `adapter` (for `adapter.Outcome` on `health.Signal`). Discovery needs `health` to cool a credential on a 401, so `catalog` imports `health`. An `adapter` that imported `catalog` would close the cycle `adapter → catalog → health → adapter`.

`adapter.ModelInfo` is therefore a plain struct declared in `adapter`, with no catalog import, and `internal/exec` fills it from the snapshot. This mirrors how `adapter.Target` already carries `BaseURL` and `APIKey` as plain fields rather than importing `provider`.

## What this phase deliberately does not do

- **No admin UI.** Browsing the catalog, editing overrides, and purging `removed_upstream` rows are Phase 7. Phase 6 supplies the data and the store methods those screens will call.
- **No Bedrock or Vertex adapters.** Their presets are transcribed here and their `oauth:` blocks defined, but `probe.go` returns "unsupported kind" for both, and Vertex is skipped by design — it has no practical API for listing what a project may call.
- **No passthrough eligibility.** Phase 9 reads the quirk declarations this phase ships; nothing in Phase 6 consumes them beyond validating the vocabulary.
- **No usage columns on `request_attempts`.** Failed attempts still burn tokens invisibly. That debt is untouched here and stays on the carried-forward list.

---

### Task 1: Preset schema, embedded file, and the closed vocabularies

**Files:**
- Create: `internal/catalog/preset.go`
- Create: `internal/catalog/presets.yaml`
- Test: `internal/catalog/preset_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `catalog.Preset`, `catalog.Auth`, `catalog.OAuth`, `catalog.TraitRule`, `catalog.Presets` (a `map[string]Preset`), `catalog.LoadPresets() (Presets, error)`, `catalog.Embedded() Presets`, and the exported vocabularies `catalog.AuthStyles`, `catalog.Quirks`, `catalog.Kinds`.

**Implementer:** dcc-superpower-companions:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 0 = 1
**Approach:** inline - skip 2: the schema is dictated field-for-field by spec §3 and the loader follows the `go:embed` pattern `internal/store/migrate.go` already uses.

The file this task hand-writes holds eight entries — enough to exercise every branch of the validator. Task 2 replaces it with the generated 200-plus-entry file, which is why the validator is written first: it is the review gate the generated output has to pass.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/preset_test.go`:

```go
package catalog

import (
	"strings"
	"testing"
)

func TestEmbeddedPresetsValidate(t *testing.T) {
	ps, err := LoadPresets()
	if err != nil {
		t.Fatalf("embedded presets do not load: %v", err)
	}
	if len(ps) == 0 {
		t.Fatal("embedded presets are empty")
	}
	for id, p := range ps {
		if p.Name == "" {
			t.Errorf("%s: no name", id)
		}
		if !Kinds[p.Kind] {
			t.Errorf("%s: kind %q is not a real kind", id, p.Kind)
		}
		if !AuthStyles[p.Auth.Style] {
			t.Errorf("%s: auth style %q is not in the vocabulary", id, p.Auth.Style)
		}
		if len(p.Surfaces) == 0 {
			t.Errorf("%s: declares no surfaces", id)
		}
		for _, q := range p.Quirks {
			if !knownQuirk(q) {
				t.Errorf("%s: quirk %q is not in the closed vocabulary", id, q)
			}
		}
		// Spec §10: every entry has a models_dev_id or an explicit exemption.
		// Silence is the failure mode this catches — an entry that simply
		// forgot the join key falls back to inferred metadata forever and
		// nothing says so.
		if p.ModelsDevID == "" && !p.NoModelsDev {
			t.Errorf("%s: neither models_dev_id nor no_models_dev", id)
		}
		if p.Auth.Style == "oauth" {
			if p.OAuth == nil {
				t.Errorf("%s: auth style oauth with no oauth block", id)
				continue
			}
			if p.OAuth.AuthorizeURL == "" || p.OAuth.TokenURL == "" ||
				p.OAuth.ClientID == "" || len(p.OAuth.Scopes) == 0 {
				t.Errorf("%s: incomplete oauth block", id)
			}
			if p.OAuth.Redirect.Style != "localhost" && p.OAuth.Redirect.Style != "manual" {
				t.Errorf("%s: redirect style %q", id, p.OAuth.Redirect.Style)
			}
			if p.OAuth.Redirect.Style == "localhost" && p.OAuth.Redirect.Port == 0 {
				t.Errorf("%s: localhost redirect with no port", id)
			}
		}
	}
}

func TestDuplicateIDIsRejected(t *testing.T) {
	// yaml.v3 rejects a repeated mapping key outright, which is what makes the
	// spec's map shape enforce "no id repeats" without a separate check.
	_, err := parsePresets([]byte("groq:\n  name: A\n  kind: openaicompat\ngroq:\n  name: B\n  kind: openaicompat\n"))
	if err == nil {
		t.Fatal("a duplicate id parsed cleanly")
	}
	if !strings.Contains(err.Error(), "already defined") {
		t.Fatalf("error does not name the duplicate: %v", err)
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	// A typo in a generated file is otherwise silently dropped, and the preset
	// serves with a field the author believed they had set.
	_, err := parsePresets([]byte("groq:\n  name: Groq\n  kind: openaicompat\n  base_yurl: x\n"))
	if err == nil {
		t.Fatal("an unknown field parsed cleanly")
	}
}

func TestValuedQuirksParse(t *testing.T) {
	for _, q := range []string{"rerank-path=/v2/rerank", "context-override=8192"} {
		if !knownQuirk(q) {
			t.Errorf("%q rejected", q)
		}
	}
	for _, q := range []string{"rerank-path", "context-override", "invented-quirk", "rerank-path="} {
		if knownQuirk(q) {
			t.Errorf("%q accepted", q)
		}
	}
}

func TestBareQuirkWithValueIsRejected(t *testing.T) {
	// A bare tag given a value is a different quirk from the one the
	// vocabulary declares, and accepting it would let a preset silently
	// configure nothing.
	if knownQuirk("no-system-role=true") {
		t.Error("a bare quirk accepted a value")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestEmbeddedPresets|TestDuplicateID|TestUnknownField|TestValuedQuirks|TestBareQuirk' -v
```

Expected: FAIL to build — `undefined: LoadPresets`, `undefined: Kinds`, `undefined: parsePresets`, `undefined: knownQuirk`.

- [ ] **Step 3: Write the schema and loader**

Create `internal/catalog/preset.go`:

```go
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
	ModelsDevID string   `yaml:"models_dev_id"`

	// NoModelsDev is the explicit exemption spec §10 requires. Without it an
	// entry that merely forgot its join key is indistinguishable from one that
	// genuinely has no models.dev counterpart.
	NoModelsDev bool     `yaml:"no_models_dev"`
	FreeTier    bool     `yaml:"free_tier"`
	Website     string   `yaml:"website"`
	Quirks      []string `yaml:"quirks"`

	// ModelsURL overrides the listing endpoint the kind would otherwise
	// derive. Some OpenAI-compatible upstreams serve chat and listing from
	// different hosts.
	ModelsURL string `yaml:"models_url"`

	// ModelAliases maps a Darkrouter model id to the models.dev model id the
	// normalization rule failed to reach. Spec §4.1.
	ModelAliases map[string]string `yaml:"model_aliases"`

	// ModelTraits declares per-generation request-shape facts models.dev
	// cannot express. See TraitRule.
	ModelTraits []TraitRule `yaml:"model_traits"`

	// CapabilityProbe names a runtime that reports its own capabilities.
	// Empty means none; "ollama" means /api/show. Spec §6.
	CapabilityProbe string `yaml:"capability_probe"`

	OAuth *OAuth `yaml:"oauth"`
}

type Auth struct {
	Style string `yaml:"style"`
	// Header overrides the header name for the api-key style, whose spelling
	// varies per upstream.
	Header string `yaml:"header"`
	// QueryParam names the parameter for the query-param style.
	QueryParam string `yaml:"query_param"`
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
	Adaptive     bool   `yaml:"adaptive"`
	ManualBudget bool   `yaml:"manual_budget"`
	FreeSampling bool   `yaml:"free_sampling"`
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
```

- [ ] **Step 4: Write the seed preset file**

Create `internal/catalog/presets.yaml`. Task 2 regenerates this with the full set; these eight cover every validator branch — the three kinds with adapters, both phase-8 kinds, an OAuth entry, an exemption, a valued quirk, and the trait rules.

```yaml
# Shipped provider presets. Generated by tools/presetgen from OmniRoute's
# provider registry and models.dev, then reviewed by hand.
#
# quirks is a closed vocabulary; see bareQuirks and valuedQuirks in preset.go.
# An unknown entry fails TestEmbeddedPresetsValidate rather than being ignored.

groq:
  name: Groq
  kind: openaicompat
  base_url: https://api.groq.com/openai/v1
  auth:
    style: bearer
  surfaces: [llm]
  models_dev_id: groq
  free_tier: true
  website: https://console.groq.com
  quirks: []

openai:
  name: OpenAI
  kind: openaicompat
  base_url: https://api.openai.com/v1
  auth:
    style: bearer
  surfaces: [llm, embeddings, images, audio, moderations]
  models_dev_id: openai
  website: https://platform.openai.com
  quirks: [max-completion-tokens-name]

anthropic:
  name: Anthropic
  kind: anthropic
  base_url: https://api.anthropic.com/v1
  auth:
    style: x-api-key
  surfaces: [llm]
  models_dev_id: anthropic
  website: https://platform.claude.com
  quirks: [requires-max-tokens]
  model_traits:
    - {match: mythos-preview, adaptive: true, manual_budget: true}
    - {match: opus-4-7, adaptive: true}
    - {match: opus-4-8, adaptive: true}
    - {match: opus-5, adaptive: true}
    - {match: sonnet-5, adaptive: true}
    - {match: fable-5, adaptive: true}
    - {match: mythos-5, adaptive: true}
    - {match: opus-4-6, adaptive: true, manual_budget: true, free_sampling: true}
    - {match: sonnet-4-6, adaptive: true, manual_budget: true, free_sampling: true}
    - {match: opus-4-5, manual_budget: true, free_sampling: true}
    - {match: sonnet-4-5, manual_budget: true, free_sampling: true}
    - {match: haiku-4-5, manual_budget: true, free_sampling: true}
    - {match: opus-4-1, manual_budget: true, free_sampling: true}
    - {match: opus-4, manual_budget: true, free_sampling: true}
    - {match: sonnet-4, manual_budget: true, free_sampling: true}
    - {match: claude-3, manual_budget: true, free_sampling: true}

gemini:
  name: Gemini (Google AI Studio)
  kind: gemini
  base_url: https://generativelanguage.googleapis.com/v1beta
  auth:
    style: query-param
    query_param: key
  surfaces: [llm, embeddings]
  models_dev_id: google
  free_tier: true
  website: https://aistudio.google.com
  quirks: []

ollama:
  name: Ollama
  kind: openaicompat
  base_url: http://localhost:11434/v1
  auth:
    style: none
  surfaces: [llm, embeddings]
  no_models_dev: true
  website: https://ollama.com
  capability_probe: ollama
  quirks: [usage-final-chunk-only]

cohere:
  name: Cohere
  kind: openaicompat
  base_url: https://api.cohere.ai/compatibility/v1
  auth:
    style: bearer
  surfaces: [llm, embeddings, rerank]
  models_dev_id: cohere
  website: https://docs.cohere.com
  quirks: [rerank-path=/v2/rerank]

bedrock:
  name: Amazon Bedrock
  kind: bedrock
  base_url: ""
  auth:
    style: sigv4
  surfaces: [llm]
  models_dev_id: amazon-bedrock
  website: https://aws.amazon.com/bedrock
  quirks: []

anthropic-oauth:
  name: Anthropic (Claude subscription)
  kind: anthropic
  base_url: https://api.anthropic.com/v1
  auth:
    style: oauth
  surfaces: [llm]
  models_dev_id: anthropic
  website: https://claude.com
  quirks: [requires-max-tokens]
  oauth:
    authorize_url: https://claude.ai/oauth/authorize
    token_url: https://console.anthropic.com/v1/oauth/token
    client_id: 9d1c250a-e61b-44d9-88ed-5944d1962f5e
    scopes: [org:create_api_key, user:profile, user:inference]
    redirect:
      style: localhost
      port: 54545
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestEmbeddedPresets|TestDuplicateID|TestUnknownField|TestValuedQuirks|TestBareQuirk' -v
```

Expected: PASS, five tests.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog/preset.go internal/catalog/presets.yaml internal/catalog/preset_test.go
git commit -m "feat(catalog): add preset schema and vocabularies"
```

---

### Task 2: The preset generator and the generated data

**Files:**
- Create: `tools/presetgen/main.go`
- Create: `internal/catalog/presets.overrides.yaml`
- Create: `internal/catalog/models_snapshot.json` (generated)
- Create: `THIRD_PARTY_NOTICES.md`
- Modify: `internal/catalog/presets.yaml` (regenerated, replacing the eight-entry seed)

**Interfaces:**
- Consumes: `catalog.Preset` and the vocabularies from Task 1.
- Produces: the generated `presets.yaml` and `models_snapshot.json` that Tasks 5, 11 and 16 read. No new Go API in `internal/`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 2 - spec 0 - coupling 0 - risk 1 = 3
**Approach:** inline - skip 2: the extraction rules below were validated against the real OmniRoute tree on 2026-08-22 and the field-scraping shape is dictated by the source files.

The generator is build-time and never ships. Its output is reviewed through `presets.overrides.yaml`, which is merged over the generated set on every run — so the review survives regeneration instead of being a one-shot hand-edit that the next run silently discards.

**Verified extraction facts** (2026-08-22, against `/root/repositories-community/OmniRoute`):

- 246 registry directories, each holding exactly one `index.ts` with one provider.
- Top-level fields are always at exactly two spaces of indentation, in both the plain-literal form (`export const xProvider: RegistryEntry = {`, ~199 files) and the builder form (`buildOpenAiCompatibleRegistryEntry({`, 47 files). Scraping `^  <field>: "..."` handles both.
- `//` comment lines must be stripped before scraping: several files carry commented-out fields that would otherwise win.
- `format` is absent in the 46 builder-form files and defaults to `openai`.
- Applying the drop rules below leaves **203 presets**, of which **49** join a models.dev provider key by exact id.

- [ ] **Step 1: Fetch the models.dev snapshot the generator reads**

```bash
mkdir -p /tmp/presetgen
curl -fsS https://models.dev/api.json -o /tmp/presetgen/modelsdev.json
ls -l /tmp/presetgen/modelsdev.json
```

Expected: roughly 4.2 MB. If the fetch fails, stop and report — this task cannot be completed offline, and no later task depends on the network.

- [ ] **Step 2: Write the generator**

Create `tools/presetgen/main.go`:

```go
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
	for id, o := range over {
		base, ok := presets[id]
		if !ok {
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
```

- [ ] **Step 3: Write the hand-reviewed overrides**

Create `internal/catalog/presets.overrides.yaml`. This is the human pass spec §3.2 requires: models.dev join keys the id match misses, the model traits models.dev cannot express, surfaces beyond `llm`, quirks, and the two phase-8 kinds the registry has no usable structure for.

```yaml
# Hand-reviewed corrections merged over the generated presets on every
# presetgen run. A non-zero field here wins; an id absent from the generated
# set is added outright.

anthropic:
  models_dev_id: anthropic
  quirks: [requires-max-tokens]
  # models.dev lists "effort" among claude-opus-4-5's reasoning_options, but
  # phase 4 verified live on 2026-08-22 that thinking:{type:"adaptive"} is a
  # 400 on that model — output_config.effort and adaptive thinking are
  # different controls sharing a word. These rules are therefore declared
  # rather than derived. Longest match wins.
  model_traits:
    - {match: mythos-preview, adaptive: true, manual_budget: true}
    - {match: opus-4-7, adaptive: true}
    - {match: opus-4-8, adaptive: true}
    - {match: opus-5, adaptive: true}
    - {match: sonnet-5, adaptive: true}
    - {match: fable-5, adaptive: true}
    - {match: mythos-5, adaptive: true}
    - {match: opus-4-6, adaptive: true, manual_budget: true, free_sampling: true}
    - {match: sonnet-4-6, adaptive: true, manual_budget: true, free_sampling: true}
    - {match: opus-4-5, manual_budget: true, free_sampling: true}
    - {match: sonnet-4-5, manual_budget: true, free_sampling: true}
    - {match: haiku-4-5, manual_budget: true, free_sampling: true}
    - {match: opus-4-1, manual_budget: true, free_sampling: true}
    - {match: opus-4, manual_budget: true, free_sampling: true}
    - {match: sonnet-4, manual_budget: true, free_sampling: true}
    - {match: claude-3, manual_budget: true, free_sampling: true}

claude:
  # OmniRoute's OAuth-flavoured Anthropic entry.
  models_dev_id: anthropic
  quirks: [requires-max-tokens]

gemini:
  models_dev_id: google
  surfaces: [llm, embeddings]

openai:
  models_dev_id: openai
  surfaces: [llm, embeddings, images, audio, moderations]
  quirks: [max-completion-tokens-name]

groq:
  models_dev_id: groq
  free_tier: true

cohere:
  models_dev_id: cohere
  surfaces: [llm, embeddings, rerank]
  quirks: [rerank-path=/v2/rerank]

mistral:
  models_dev_id: mistral
  surfaces: [llm, embeddings]

deepseek:
  models_dev_id: deepseek

cerebras:
  models_dev_id: cerebras

xai:
  models_dev_id: xai

perplexity:
  models_dev_id: perplexity

together:
  models_dev_id: togetherai

fireworks:
  models_dev_id: fireworks-ai
  # Fireworks prefixes every id with an account path that no other source uses.
  model_aliases:
    accounts/fireworks/models/llama-v3p3-70b-instruct: llama-3.3-70b

openrouter:
  models_dev_id: openrouter

azure:
  models_dev_id: azure
  auth:
    style: api-key
    header: api-key

vercel:
  models_dev_id: vercel

venice:
  models_dev_id: venice

nvidia:
  models_dev_id: nvidia

ollama:
  name: Ollama
  kind: openaicompat
  base_url: http://localhost:11434/v1
  auth:
    style: none
  surfaces: [llm, embeddings]
  no_models_dev: true
  website: https://ollama.com
  # Ollama reports whether a model's template advertises tools, which is what
  # makes the local story honest rather than a guess. Spec §6.
  capability_probe: ollama
  quirks: [usage-final-chunk-only]

lmstudio:
  name: LM Studio
  kind: openaicompat
  base_url: http://localhost:1234/v1
  auth:
    style: none
  surfaces: [llm, embeddings]
  no_models_dev: true
  website: https://lmstudio.ai
  quirks: []

bedrock:
  name: Amazon Bedrock
  kind: bedrock
  base_url: ""
  auth:
    style: sigv4
  surfaces: [llm]
  models_dev_id: amazon-bedrock
  website: https://aws.amazon.com/bedrock
  quirks: []

vertex:
  name: Google Vertex AI
  kind: vertex
  base_url: ""
  auth:
    style: gcp-sa
  surfaces: [llm, embeddings]
  models_dev_id: google-vertex
  website: https://cloud.google.com/vertex-ai
  quirks: []

vertex-anthropic:
  name: Google Vertex AI (Anthropic publisher)
  kind: vertex
  base_url: ""
  auth:
    style: gcp-sa
  surfaces: [llm]
  models_dev_id: google-vertex-anthropic
  website: https://cloud.google.com/vertex-ai
  quirks: [requires-max-tokens]

anthropic-oauth:
  name: Anthropic (Claude subscription)
  kind: anthropic
  base_url: https://api.anthropic.com/v1
  auth:
    style: oauth
  surfaces: [llm]
  models_dev_id: anthropic
  website: https://claude.com
  quirks: [requires-max-tokens]
  oauth:
    authorize_url: https://claude.ai/oauth/authorize
    token_url: https://console.anthropic.com/v1/oauth/token
    client_id: 9d1c250a-e61b-44d9-88ed-5944d1962f5e
    scopes: [org:create_api_key, user:profile, user:inference]
    redirect:
      style: localhost
      port: 54545
```

- [ ] **Step 4: Run the generator**

```bash
export PATH=$PATH:/usr/local/go/bin
go run ./tools/presetgen \
  -omniroute /root/repositories-community/OmniRoute \
  -modelsdev /tmp/presetgen/modelsdev.json \
  -out-presets internal/catalog/presets.yaml \
  -out-snapshot internal/catalog/models_snapshot.json \
  -overrides internal/catalog/presets.overrides.yaml
```

Expected on stderr: `presetgen: 2NN presets (4N dropped, 4N joined to models.dev, 2N overridden), 7NNN models in snapshot`. The preset count should be a little over 200 — 203 registry survivors plus the override-only entries. If it reports fewer than 150, the extraction broke; stop and diagnose rather than committing a thin file.

- [ ] **Step 5: Read the generated output rather than trusting the counts**

A count proves nothing about correctness. Read actual entries:

```bash
head -40 internal/catalog/presets.yaml
python3 -c "import json;d=json.load(open('internal/catalog/models_snapshot.json'));print(len(d),'providers');print(d['anthropic']['claude-opus-4-5'])"
ls -l internal/catalog/models_snapshot.json
```

Expected: the YAML header, then entries in alphabetical order each carrying `name`, `kind`, `base_url`, `auth`, `surfaces`. The Anthropic entry must print `{'i': 5000000, 'o': 25000000, 'c': 500000, 'w': 200000, 'm': 64000, 't': True, 'r': True, 'v': True}` — `$5/M` in, `$25/M` out. The snapshot file should be roughly 600 KB.

Spot-check that base URLs lost their request path:

```bash
grep -A1 '^groq:' internal/catalog/presets.yaml
grep -c 'chat/completions' internal/catalog/presets.yaml
```

Expected: Groq's `base_url` is `https://api.groq.com/openai/v1`, and the `chat/completions` count is `0`. A non-zero count means `chatSuffixes` missed a form and the adapter would build a doubled path.

- [ ] **Step 6: Run the preset validator against the generated file**

This is the review gate Task 1 existed to build.

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run TestEmbeddedPresets -v
```

Expected: PASS. A failure names the offending id and field; fix it in `presets.overrides.yaml`, re-run Step 4, and re-test. Never edit `presets.yaml` directly — the next generator run discards it.

- [ ] **Step 7: Write the third-party notices**

Create `THIRD_PARTY_NOTICES.md`:

```markdown
# Third-party notices

Darkrouter embeds data transcribed from the projects below. No third-party code
is linked into the binary; these are data attributions.

## OmniRoute

`internal/catalog/presets.yaml` transcribes provider structure and presentation
from OmniRoute. Generated by `tools/presetgen`, then reviewed through
`internal/catalog/presets.overrides.yaml`.

- Project: OmniRoute
- Licence: MIT
- Copyright (c) 2026 diegosouzapw

## models.dev

`internal/catalog/models_snapshot.json` is a trimmed snapshot of
`https://models.dev/api.json`, taken 2026-08-22. It carries per-model pricing,
context and output limits, and capability flags, and exists so Darkrouter starts
and serves with no outbound access to models.dev. The live sync refreshes the
same fields every twelve hours.

- Project: models.dev
- Source: https://models.dev/api.json
- Snapshot taken: 2026-08-22
```

- [ ] **Step 8: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing. `go vet` now covers `tools/presetgen` too.

- [ ] **Step 9: Commit**

```bash
git add tools/presetgen/main.go internal/catalog/presets.yaml \
  internal/catalog/presets.overrides.yaml internal/catalog/models_snapshot.json \
  THIRD_PARTY_NOTICES.md
git commit -m "feat(catalog): generate presets and metadata snapshot"
```

---

### Task 3: Migration 0002 — the state vocabulary and discovery bookkeeping

**Files:**
- Create: `internal/store/migrations/0002_catalog.sql`
- Modify: `internal/store/migrate_test.go` (the hardcoded schema version, plus the upgrade-path tests)
- Modify: `internal/provider/sqlsource.go:96` (the `state` predicate)

**Interfaces:**
- Consumes: nothing.
- Produces: the `models.missing_streak` and `models.last_seen_at` columns and the `provider_discovery` table that Tasks 4, 9 and 10 read and write. The `state` vocabulary becomes `live` / `stale` / `removed_upstream`, and `surfaces` defaults to `["llm"]`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5
**Approach:** inline - skip 2: migration 0001 sets the file naming, transaction handling, and STRICT convention this follows exactly.

Risk is 3 because this is a schema migration against a live database. Two existing defects are corrected here rather than worked around later:

- **`state` defaults to `'active'`**, which is not in spec §5.1's vocabulary. `internal/provider/sqlsource.go:96` filters on it, so leaving the rename to a later task would empty every provider's model list the moment discovery started writing `'live'`.
- **`surfaces` defaults to `'["chat"]'`**, which `ir.ParseSurface` rejects — the surface is spelled `llm`. Nothing reads the column today, which is why the mismatch survived; Task 12's snapshot is the first reader and would find every model declaring an unparseable surface.

**The table is rebuilt rather than altered.** SQLite cannot change a column default in place, so `ALTER TABLE ... ADD COLUMN` would leave both wrong defaults live — and a row inserted by any future writer that omitted them would silently reintroduce exactly the two values this migration exists to remove. Nothing references `models` (`model_overrides` references `providers`), so the rebuild has no dependents to fix up.

- [ ] **Step 1: Write the failing tests**

Add to `internal/store/migrate_test.go`:

```go
// atVersionOne builds a database carrying only migration 0001, so the upgrade
// path itself is exercised rather than a fresh install that never had the old
// values. Using the embedded migration rather than a copy is what keeps this
// honest when 0001 changes.
func atVersionOne(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	db := openTest(t)
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx, ms[0].sql); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigration0002RewritesLegacyRows(t *testing.T) {
	ctx := context.Background()
	db := atVersionOne(t)

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	// Exactly how a phase 2 row was written: both values come from the old
	// defaults, which is the case an ADD COLUMN migration would have missed.
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id, capabilities_source) VALUES ('p', 'legacy', 'inferred')`); err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("upgrade from version 1: %v", err)
	}

	var state, surfaces, source string
	if err := db.Read.QueryRowContext(ctx,
		`SELECT state, surfaces, capabilities_source FROM models WHERE model_id = 'legacy'`).
		Scan(&state, &surfaces, &source); err != nil {
		t.Fatal(err)
	}
	if state != "live" {
		t.Errorf("state = %q, want live", state)
	}
	if surfaces != `["llm"]` {
		t.Errorf("surfaces = %q, want [\"llm\"]", surfaces)
	}
	// The rebuild must carry every pre-existing column across, not just the
	// two it rewrites.
	if source != "inferred" {
		t.Errorf("capabilities_source = %q, want inferred", source)
	}
}

func TestMigration0002FixesTheDefaults(t *testing.T) {
	// A row written after the migration must not be able to reintroduce the
	// old values. This is what the rebuild buys over ADD COLUMN.
	ctx := context.Background()
	db := migrated(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id) VALUES ('p', 'm')`); err != nil {
		t.Fatal(err)
	}
	var state, surfaces string
	var streak int
	var lastSeen *int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT state, surfaces, missing_streak, last_seen_at
		   FROM models WHERE model_id = 'm'`).
		Scan(&state, &surfaces, &streak, &lastSeen); err != nil {
		t.Fatal(err)
	}
	if state != "live" || surfaces != `["llm"]` {
		t.Errorf("defaults = (%q, %q), want (live, [\"llm\"])", state, surfaces)
	}
	if streak != 0 || lastSeen != nil {
		t.Errorf("new columns = (%d, %v), want (0, nil)", streak, lastSeen)
	}
}

func TestMigration0002CreatesProviderDiscovery(t *testing.T) {
	var name string
	err := migrated(t).Read.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name='provider_discovery'`).Scan(&name)
	if err != nil {
		t.Fatalf("provider_discovery missing: %v", err)
	}
}
```

Also change the hardcoded version in `TestMigrateIsIdempotent`:

```go
		if v != 2 {
			t.Errorf("run %d: version = %d, want 2", i, v)
		}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run 'TestMigration0002|TestMigrateIsIdempotent' -v
```

Expected: the two `Migration0002` scans fail with `no such column: missing_streak`, `TestMigration0002CreatesProviderDiscovery` fails with no rows, and `TestMigrateIsIdempotent` fails with `version = 1, want 2`.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/0002_catalog.sql`:

```sql
-- Phase 6 schema, per spec sections 5.1 and 6.
--
-- models is rebuilt rather than altered. Two of its phase 2 defaults are wrong
-- and SQLite cannot change a default in place:
--
--   state defaulted to 'active', which is not in spec 5.1's vocabulary
--   (live | stale | removed_upstream). internal/provider filters on this
--   column, so a row keeping the old value silently stops being routable.
--
--   surfaces defaulted to '["chat"]', which ir.ParseSurface rejects — the
--   surface is spelled 'llm'. Nothing read the column before phase 6, which is
--   why the mismatch survived unnoticed.
--
-- Nothing references models (model_overrides references providers), so the
-- rebuild has no dependents to fix up.

CREATE TABLE models_new (
  provider_id                      TEXT    NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  model_id                         TEXT    NOT NULL,
  publisher                        TEXT    NOT NULL DEFAULT '',
  surfaces                         TEXT    NOT NULL DEFAULT '["llm"]',
  capabilities                     TEXT    NOT NULL DEFAULT '{}',
  capabilities_source              TEXT    NOT NULL DEFAULT 'inferred',
  context_window                   INTEGER,
  max_output_tokens                INTEGER,
  input_price_micros_per_mtok      INTEGER,
  output_price_micros_per_mtok     INTEGER,
  cache_read_price_micros_per_mtok INTEGER,
  discovered_at                    INTEGER,
  state                            TEXT    NOT NULL DEFAULT 'live',

  -- missing_streak counts consecutive *successful* listings that omitted this
  -- model. A failed probe never touches it: spec 5.1 makes that asymmetry the
  -- whole point, because a provider timing out must not empty its catalog.
  missing_streak                   INTEGER NOT NULL DEFAULT 0,
  last_seen_at                     INTEGER,

  PRIMARY KEY (provider_id, model_id)
) STRICT;

INSERT INTO models_new (
  provider_id, model_id, publisher, surfaces, capabilities, capabilities_source,
  context_window, max_output_tokens, input_price_micros_per_mtok,
  output_price_micros_per_mtok, cache_read_price_micros_per_mtok,
  discovered_at, state
)
SELECT provider_id, model_id, publisher,
       CASE WHEN surfaces = '["chat"]' THEN '["llm"]' ELSE surfaces END,
       capabilities, capabilities_source,
       context_window, max_output_tokens, input_price_micros_per_mtok,
       output_price_micros_per_mtok, cache_read_price_micros_per_mtok,
       discovered_at,
       CASE WHEN state = 'active' THEN 'live' ELSE state END
  FROM models;

DROP TABLE models;
ALTER TABLE models_new RENAME TO models;

CREATE INDEX idx_models_state ON models(state);

-- Per-provider discovery bookkeeping. Separate from providers because it is
-- worker state overwritten on every tick, and mixing it into the row an
-- operator edits invites a write conflict on every save.
CREATE TABLE provider_discovery (
  provider_id          TEXT    PRIMARY KEY REFERENCES providers(id) ON DELETE CASCADE,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  last_attempt_at      INTEGER,
  last_success_at      INTEGER,
  last_error           TEXT    NOT NULL DEFAULT ''
) STRICT;
```

- [ ] **Step 4: Move `internal/provider` to the new vocabulary**

In `internal/provider/sqlsource.go`, the models query changes. `stale` is included deliberately: spec §5.1 keeps stale models routable, because the breaker rather than the catalog is what avoids a broken provider.

```go
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT model_id FROM models
		  WHERE provider_id = ? AND state IN ('live', 'stale')
		  ORDER BY model_id`,
		providerID)
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ ./internal/provider/ -race -count=1 -v
```

Expected: PASS. If an import or sqlsource test asserted on the old `'active'` value, read the failure and move the expectation to `'live'` rather than reintroducing the old vocabulary.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/store/migrations/0002_catalog.sql internal/store/migrate_test.go   internal/provider/sqlsource.go
git commit -m "feat(store): add catalog migration 0002"
```

---

### Task 4: The catalog store — row types and the read side

**Files:**
- Create: `internal/store/catalog.go`
- Test: `internal/store/catalog_test.go`

**Interfaces:**
- Consumes: `store.DB` and migration 0002 from Task 3.
- Produces: `store.ModelRow`, `store.ModelCapabilities`, `store.ModelOverride`, `store.MetadataRow`, and the methods `(*DB).Models(ctx) ([]ModelRow, error)`, `(*DB).ModelOverrides(ctx) ([]ModelOverride, error)`, `(*DB).UpsertMetadata(ctx, []MetadataRow) error`. Tasks 8, 10, 11 and 13 all read this API.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: `internal/store/log.go` and `rollup.go` already fix the row-struct, scan-loop and upsert conventions this follows.

`UpsertMetadata` is the sync worker's only writer. It touches **metadata columns only** — never `state`, `missing_streak` or `last_seen_at`, which belong to discovery. Mixing them would let a models.dev refresh resurrect a model discovery had retired.

- [ ] **Step 1: Write the failing test**

Create `internal/store/catalog_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"
)

func catalogDB(t *testing.T) *DB {
	t.Helper()
	db := migrated(t)
	if _, err := db.Write.ExecContext(context.Background(),
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestModelsReadsEveryColumn(t *testing.T) {
	ctx := context.Background()
	db := catalogDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id, publisher, surfaces, capabilities,
		    capabilities_source, context_window, max_output_tokens,
		    input_price_micros_per_mtok, output_price_micros_per_mtok,
		    cache_read_price_micros_per_mtok, state, missing_streak, last_seen_at)
		 VALUES ('p', 'm', 'meta', '["llm","embeddings"]',
		    '{"tools":true,"vision":false,"reasoning":true}', 'models_dev',
		    200000, 64000, 5000000, 25000000, 500000, 'stale', 2, 1700000000000)`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.ProviderID != "p" || r.ModelID != "m" || r.Publisher != "meta" {
		t.Errorf("identity = %+v", r)
	}
	if len(r.Surfaces) != 2 || r.Surfaces[0] != "llm" || r.Surfaces[1] != "embeddings" {
		t.Errorf("surfaces = %v", r.Surfaces)
	}
	if !r.Capabilities.Tools || r.Capabilities.Vision || !r.Capabilities.Reasoning {
		t.Errorf("capabilities = %+v", r.Capabilities)
	}
	if r.CapabilitiesSource != "models_dev" || r.State != "stale" || r.MissingStreak != 2 {
		t.Errorf("state = (%q, %q, %d)", r.CapabilitiesSource, r.State, r.MissingStreak)
	}
	if r.ContextWindow != 200000 || r.MaxOutputTokens != 64000 {
		t.Errorf("limits = (%d, %d)", r.ContextWindow, r.MaxOutputTokens)
	}
	if r.InputMicrosPerMTok != 5_000_000 || r.OutputMicrosPerMTok != 25_000_000 ||
		r.CacheReadMicrosPerMTok != 500_000 {
		t.Errorf("pricing = %+v", r)
	}
	if !r.LastSeenAt.Equal(time.UnixMilli(1700000000000).UTC()) {
		t.Errorf("last seen = %v", r.LastSeenAt)
	}
}

func TestModelsLeavesUnknownMetadataZero(t *testing.T) {
	// NULL means "we never found out" and must not read back as a real zero
	// price. A model priced at zero and a model of unknown price are different
	// facts, and the UI shows them differently.
	ctx := context.Background()
	db := catalogDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id) VALUES ('p', 'm')`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r.ContextWindow != 0 || r.MaxOutputTokens != 0 || r.InputMicrosPerMTok != 0 {
		t.Errorf("NULL columns did not read back as zero: %+v", r)
	}
	if r.PriceKnown {
		t.Error("PriceKnown is true with every price column NULL")
	}
	if !r.LastSeenAt.IsZero() {
		t.Errorf("last seen = %v, want zero", r.LastSeenAt)
	}
}

func TestUpsertMetadataRoundTripsCentPrices(t *testing.T) {
	// $0.14 per million must survive as 140000 micro-dollars. Storing
	// micro-dollars per *token* would truncate it to zero, which is the
	// specific bug master design section 11 fixes the unit to avoid.
	ctx := context.Background()
	db := catalogDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id) VALUES ('p', 'm')`); err != nil {
		t.Fatal(err)
	}
	err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "m",
		ContextWindow: 8192, MaxOutputTokens: 4096,
		InputMicrosPerMTok: 140_000, OutputMicrosPerMTok: 280_000,
		Capabilities:       ModelCapabilities{Tools: true},
		CapabilitiesSource: "models_dev",
	}})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Models(ctx)
	r := rows[0]
	if r.InputMicrosPerMTok != 140_000 {
		t.Errorf("input price = %d, want 140000", r.InputMicrosPerMTok)
	}
	if r.OutputMicrosPerMTok != 280_000 || !r.PriceKnown {
		t.Errorf("output price = %d, known = %v", r.OutputMicrosPerMTok, r.PriceKnown)
	}
	if r.ContextWindow != 8192 || !r.Capabilities.Tools || r.CapabilitiesSource != "models_dev" {
		t.Errorf("metadata = %+v", r)
	}
}

func TestUpsertMetadataNeverTouchesLifecycle(t *testing.T) {
	// A models.dev refresh must not resurrect a model discovery retired, nor
	// reset the streak that retired it.
	ctx := context.Background()
	db := catalogDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id, state, missing_streak, last_seen_at)
		 VALUES ('p', 'm', 'removed_upstream', 3, 42)`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "m", ContextWindow: 4096,
		CapabilitiesSource: "models_dev",
	}}); err != nil {
		t.Fatal(err)
	}
	var state string
	var streak int
	var seen int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT state, missing_streak, last_seen_at FROM models WHERE model_id = 'm'`).
		Scan(&state, &streak, &seen); err != nil {
		t.Fatal(err)
	}
	if state != "removed_upstream" || streak != 3 || seen != 42 {
		t.Errorf("lifecycle changed: (%q, %d, %d)", state, streak, seen)
	}
}

func TestUpsertMetadataIgnoresUnknownModels(t *testing.T) {
	// models.dev knows models this provider does not offer. Inserting them
	// would make the catalog claim reachability it has no evidence for.
	ctx := context.Background()
	db := catalogDB(t)
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "never-discovered", ContextWindow: 4096,
	}}); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Models(ctx)
	if len(rows) != 0 {
		t.Errorf("upsert invented %d rows", len(rows))
	}
}

func TestModelOverridesReadPartially(t *testing.T) {
	// An override sets one field and leaves the rest to the merge. A nil
	// pointer is "not overridden"; a zero value is a real override to zero.
	ctx := context.Background()
	db := catalogDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO model_overrides (provider_id, model_id, capabilities)
		 VALUES ('p', 'm', '{"tools":true,"vision":true,"reasoning":false}')`); err != nil {
		t.Fatal(err)
	}
	ovs, err := db.ModelOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ovs) != 1 {
		t.Fatalf("got %d overrides", len(ovs))
	}
	o := ovs[0]
	if o.Capabilities == nil || !o.Capabilities.Tools || !o.Capabilities.Vision {
		t.Errorf("capabilities = %+v", o.Capabilities)
	}
	if o.ContextWindow != nil {
		t.Errorf("context window overridden to %v, want nil", *o.ContextWindow)
	}
	if o.Surfaces != nil {
		t.Errorf("surfaces overridden to %v, want nil", o.Surfaces)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run 'TestModels|TestUpsertMetadata|TestModelOverrides' -v
```

Expected: FAIL to build — `undefined: MetadataRow`, `db.Models undefined`, `db.ModelOverrides undefined`.

- [ ] **Step 3: Write the store**

Create `internal/store/catalog.go`:

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ModelCapabilities is the JSON shape of the models.capabilities column.
type ModelCapabilities struct {
	Tools     bool `json:"tools"`
	Vision    bool `json:"vision"`
	Reasoning bool `json:"reasoning"`
}

// ModelRow is one models row, decoded.
type ModelRow struct {
	ProviderID         string
	ModelID            string
	Publisher          string
	Surfaces           []string
	Capabilities       ModelCapabilities
	CapabilitiesSource string
	ContextWindow      int
	MaxOutputTokens    int

	InputMicrosPerMTok     int64
	OutputMicrosPerMTok    int64
	CacheReadMicrosPerMTok int64
	// PriceKnown separates "free" from "we never found out". Both read back as
	// zero, and the UI shows them differently.
	PriceKnown bool

	State         string
	MissingStreak int
	LastSeenAt    time.Time
	DiscoveredAt  time.Time
}

// ModelOverride is one model_overrides row. Every field is a pointer or a nil
// slice, because the table is per-field: an override that sets capabilities
// must not silently reset the context window.
type ModelOverride struct {
	ProviderID    string
	ModelID       string
	Surfaces      []string
	Capabilities  *ModelCapabilities
	ContextWindow *int
}

// MetadataRow is what the models.dev sync writes. It carries no lifecycle
// fields by construction: a metadata refresh must not resurrect a model
// discovery retired.
type MetadataRow struct {
	ProviderID             string
	ModelID                string
	Publisher              string
	Surfaces               []string
	Capabilities           ModelCapabilities
	CapabilitiesSource     string
	ContextWindow          int
	MaxOutputTokens        int
	InputMicrosPerMTok     int64
	OutputMicrosPerMTok    int64
	CacheReadMicrosPerMTok int64
}

const modelColumns = `provider_id, model_id, publisher, surfaces, capabilities,
	capabilities_source, context_window, max_output_tokens,
	input_price_micros_per_mtok, output_price_micros_per_mtok,
	cache_read_price_micros_per_mtok, discovered_at, state,
	missing_streak, last_seen_at`

// Models returns every catalogued model, including the ones discovery has
// retired. The snapshot filters; the store reports.
func (d *DB) Models(ctx context.Context) ([]ModelRow, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT `+modelColumns+` FROM models ORDER BY provider_id, model_id`)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()

	var out []ModelRow
	for rows.Next() {
		var (
			r                          ModelRow
			surfaces, caps             string
			ctxWin, maxOut             sql.NullInt64
			inPrice, outPrice, cachePr sql.NullInt64
			discovered, lastSeen       sql.NullInt64
		)
		if err := rows.Scan(&r.ProviderID, &r.ModelID, &r.Publisher, &surfaces, &caps,
			&r.CapabilitiesSource, &ctxWin, &maxOut, &inPrice, &outPrice, &cachePr,
			&discovered, &r.State, &r.MissingStreak, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan model: %w", err)
		}
		// A malformed JSON column is a corrupt row, not a fatal error: one bad
		// row must not make the whole catalog unreadable. It degrades to the
		// zero value, which the merge then treats as unknown.
		_ = json.Unmarshal([]byte(surfaces), &r.Surfaces)
		_ = json.Unmarshal([]byte(caps), &r.Capabilities)

		r.ContextWindow = int(ctxWin.Int64)
		r.MaxOutputTokens = int(maxOut.Int64)
		r.InputMicrosPerMTok = inPrice.Int64
		r.OutputMicrosPerMTok = outPrice.Int64
		r.CacheReadMicrosPerMTok = cachePr.Int64
		r.PriceKnown = inPrice.Valid || outPrice.Valid
		if lastSeen.Valid {
			r.LastSeenAt = time.UnixMilli(lastSeen.Int64).UTC()
		}
		if discovered.Valid {
			r.DiscoveredAt = time.UnixMilli(discovered.Int64).UTC()
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ModelOverrides returns the operator's per-(provider, model) corrections.
func (d *DB) ModelOverrides(ctx context.Context) ([]ModelOverride, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT provider_id, model_id, surfaces, capabilities, context_window
		   FROM model_overrides ORDER BY provider_id, model_id`)
	if err != nil {
		return nil, fmt.Errorf("list model overrides: %w", err)
	}
	defer rows.Close()

	var out []ModelOverride
	for rows.Next() {
		var (
			o              ModelOverride
			surfaces, caps sql.NullString
			ctxWin         sql.NullInt64
		)
		if err := rows.Scan(&o.ProviderID, &o.ModelID, &surfaces, &caps, &ctxWin); err != nil {
			return nil, fmt.Errorf("scan model override: %w", err)
		}
		if surfaces.Valid {
			_ = json.Unmarshal([]byte(surfaces.String), &o.Surfaces)
		}
		if caps.Valid {
			var c ModelCapabilities
			if json.Unmarshal([]byte(caps.String), &c) == nil {
				o.Capabilities = &c
			}
		}
		if ctxWin.Valid {
			n := int(ctxWin.Int64)
			o.ContextWindow = &n
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpsertMetadata writes the models.dev half of a model's record.
//
// It is an UPDATE rather than an upsert on purpose. models.dev knows models a
// given provider does not offer, and inserting them would make the catalog
// claim reachability it has no evidence for — existence is discovery's to
// decide, per spec section 7. The columns it does not name are equally
// deliberate: state, missing_streak and last_seen_at belong to discovery.
func (d *DB) UpsertMetadata(ctx context.Context, rows []MetadataRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE models SET
		    publisher = ?, surfaces = ?, capabilities = ?, capabilities_source = ?,
		    context_window = ?, max_output_tokens = ?,
		    input_price_micros_per_mtok = ?, output_price_micros_per_mtok = ?,
		    cache_read_price_micros_per_mtok = ?
		  WHERE provider_id = ? AND model_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare metadata write: %w", err)
	}
	defer stmt.Close()

	for _, r := range rows {
		surfaces, err := json.Marshal(nonEmptySurfaces(r.Surfaces))
		if err != nil {
			return err
		}
		caps, err := json.Marshal(r.Capabilities)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx,
			r.Publisher, string(surfaces), string(caps), r.CapabilitiesSource,
			nullableInt(r.ContextWindow), nullableInt(r.MaxOutputTokens),
			nullableInt64(r.InputMicrosPerMTok), nullableInt64(r.OutputMicrosPerMTok),
			nullableInt64(r.CacheReadMicrosPerMTok),
			r.ProviderID, r.ModelID); err != nil {
			return fmt.Errorf("write metadata for %s/%s: %w", r.ProviderID, r.ModelID, err)
		}
	}
	return tx.Commit()
}

// nonEmptySurfaces keeps the column's invariant: every model serves at least
// the llm surface unless something said otherwise.
func nonEmptySurfaces(s []string) []string {
	if len(s) == 0 {
		return []string{"llm"}
	}
	return s
}

// nullableInt writes NULL for zero, because zero context window and unknown
// context window are different facts and the column is how they stay apart.
func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullableInt64(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run 'TestModels|TestUpsertMetadata|TestModelOverrides' -race -count=1 -v
```

Expected: PASS, six tests.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/store/catalog.go internal/store/catalog_test.go
git commit -m "feat(store): add catalog row types and reads"
```

---

### Task 5: The models.dev document and its field mapping

**Files:**
- Create: `internal/catalog/modelsdev.go`
- Test: `internal/catalog/modelsdev_test.go`

**Interfaces:**
- Consumes: `internal/catalog/models_snapshot.json` from Task 2.
- Produces: `catalog.Metadata`, `catalog.Doc`, `catalog.ParseModelsDev([]byte) (Doc, error)`, `catalog.FallbackDoc() Doc`, and `(Doc).Metadata(modelsDevID, modelID string) (Metadata, bool)`. Tasks 7, 13 and 16 read these.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: the field mapping is spec §4's table verbatim and the two document shapes are both fixed by Task 2's output.

There are two shapes and one output type. The **live** document is `https://models.dev/api.json` as models.dev publishes it. The **fallback** is the trimmed `models_snapshot.json` Task 2 generated, which exists so a first run with no network still knows prices and limits. A test asserts the two agree on a known model, because a divergence would mean the gateway's numbers changed the first time it reached the network.

Field mapping, from spec §4:

| models.dev | Darkrouter |
|---|---|
| `cost.input` × 1,000,000 | `InputMicrosPerMTok` |
| `cost.output` × 1,000,000 | `OutputMicrosPerMTok` |
| `cost.cache_read` × 1,000,000 | `CacheReadMicrosPerMTok` |
| `limit.context` | `ContextWindow` |
| `limit.output` | `MaxOutputTokens` |
| `tool_call` | capability `Tools` |
| `reasoning` | capability `Reasoning` |
| `modalities.input` contains `image` | capability `Vision` |

There is no `vision` flag in models.dev; it is expressed through `modalities.input`, which is the single most likely thing to get wrong by assumption.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/modelsdev_test.go`:

```go
package catalog

import "testing"

const liveSample = `{
  "anthropic": {
    "id": "anthropic",
    "models": {
      "claude-opus-4-5": {
        "id": "claude-opus-4-5",
        "tool_call": true,
        "reasoning": true,
        "modalities": {"input": ["text", "image", "pdf"], "output": ["text"]},
        "limit": {"context": 200000, "output": 64000},
        "cost": {"input": 5, "output": 25, "cache_read": 0.5, "cache_write": 6.25}
      },
      "cheap-model": {
        "id": "cheap-model",
        "tool_call": false,
        "reasoning": false,
        "modalities": {"input": ["text"], "output": ["text"]},
        "limit": {"context": 8192, "output": 4096},
        "cost": {"input": 0.14, "output": 0.28}
      },
      "no-price": {
        "id": "no-price",
        "modalities": {"input": ["text"], "output": ["text"]},
        "limit": {"context": 4096, "output": 0}
      }
    }
  }
}`

func TestCentPriceSurvivesAsMicroDollars(t *testing.T) {
	// $0.14 per million is the case that fails if prices are stored per token:
	// it truncates to integer zero and the model reads as free.
	doc, err := ParseModelsDev([]byte(liveSample))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := doc.Metadata("anthropic", "cheap-model")
	if !ok {
		t.Fatal("cheap-model not found")
	}
	if m.InputMicrosPerMTok != 140_000 {
		t.Errorf("input = %d, want 140000", m.InputMicrosPerMTok)
	}
	if m.OutputMicrosPerMTok != 280_000 {
		t.Errorf("output = %d, want 280000", m.OutputMicrosPerMTok)
	}
	if !m.PriceKnown {
		t.Error("PriceKnown is false for a priced model")
	}
}

func TestVisionComesFromModalitiesNotAFlag(t *testing.T) {
	// models.dev has no vision field. Reading one would leave every
	// vision-capable model marked text-only.
	doc, _ := ParseModelsDev([]byte(liveSample))
	m, _ := doc.Metadata("anthropic", "claude-opus-4-5")
	if !m.Capabilities.Vision {
		t.Error("vision not derived from modalities.input")
	}
	if !m.Capabilities.Tools || !m.Capabilities.Reasoning {
		t.Errorf("capabilities = %+v", m.Capabilities)
	}
	if !m.Capabilities.Known {
		t.Error("capabilities from models.dev are not marked known")
	}
	cheap, _ := doc.Metadata("anthropic", "cheap-model")
	if cheap.Capabilities.Vision {
		t.Error("text-only model reported vision")
	}
	if !cheap.Capabilities.Known {
		t.Error("a models.dev negative is still knowledge and must be marked known")
	}
}

func TestLimitsAndUnknownPrice(t *testing.T) {
	doc, _ := ParseModelsDev([]byte(liveSample))
	m, _ := doc.Metadata("anthropic", "claude-opus-4-5")
	if m.ContextWindow != 200_000 || m.MaxOutputTokens != 64_000 {
		t.Errorf("limits = (%d, %d)", m.ContextWindow, m.MaxOutputTokens)
	}
	if m.CacheReadMicrosPerMTok != 500_000 {
		t.Errorf("cache read = %d, want 500000", m.CacheReadMicrosPerMTok)
	}
	np, _ := doc.Metadata("anthropic", "no-price")
	if np.PriceKnown {
		t.Error("an unpriced model reports a known price")
	}
	if np.MaxOutputTokens != 0 {
		t.Errorf("max output = %d, want 0 for an absent limit", np.MaxOutputTokens)
	}
}

func TestMissRatherThanZeroValue(t *testing.T) {
	doc, _ := ParseModelsDev([]byte(liveSample))
	if _, ok := doc.Metadata("anthropic", "not-a-model"); ok {
		t.Error("an unknown model reported a hit")
	}
	if _, ok := doc.Metadata("not-a-provider", "claude-opus-4-5"); ok {
		t.Error("an unknown provider reported a hit")
	}
}

func TestMalformedDocumentIsAnError(t *testing.T) {
	// A truncated CDN response must not read as an empty catalog, which would
	// wipe every price on the next sync.
	for _, bad := range []string{"", "not json", "[]", "{}"} {
		if _, err := ParseModelsDev([]byte(bad)); err == nil {
			t.Errorf("%q parsed cleanly", bad)
		}
	}
}

func TestEmbeddedFallbackAgreesWithTheLiveShape(t *testing.T) {
	// The two documents have different shapes and must produce identical
	// numbers. A divergence means the gateway's prices change the first time
	// it reaches the network, which nobody would attribute to a parser.
	fb := FallbackDoc()
	got, ok := fb.Metadata("anthropic", "claude-opus-4-5")
	if !ok {
		t.Fatal("claude-opus-4-5 missing from the embedded snapshot")
	}
	live, _ := ParseModelsDev([]byte(liveSample))
	want, _ := live.Metadata("anthropic", "claude-opus-4-5")
	if got != want {
		t.Errorf("fallback = %+v\nlive     = %+v", got, want)
	}
}

func TestEmbeddedFallbackIsPopulated(t *testing.T) {
	fb := FallbackDoc()
	if len(fb) < 100 {
		t.Fatalf("embedded snapshot has %d providers; regenerate with tools/presetgen", len(fb))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestCentPrice|TestVision|TestLimits|TestMiss|TestMalformed|TestEmbeddedFallback' -v
```

Expected: FAIL to build — `undefined: ParseModelsDev`, `undefined: FallbackDoc`.

- [ ] **Step 3: Write the mapping**

Create `internal/catalog/modelsdev.go`:

```go
package catalog

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

//go:embed models_snapshot.json
var snapshotJSON []byte

// Metadata is what a metadata source knows about one model. It is deliberately
// flat and comparable, so a test can assert two sources agree with ==.
type Metadata struct {
	ContextWindow   int
	MaxOutputTokens int

	InputMicrosPerMTok     int64
	OutputMicrosPerMTok    int64
	CacheReadMicrosPerMTok int64
	// PriceKnown separates a free model from an unpriced one. Both are zero.
	PriceKnown bool

	Capabilities Capabilities
}

// Doc is a metadata source keyed by models.dev provider id, then model id.
type Doc map[string]map[string]Metadata

// Metadata looks one model up. The miss is reported rather than returning a
// zero value, because a zero context window is a fact the merge acts on.
func (d Doc) Metadata(modelsDevID, modelID string) (Metadata, bool) {
	models, ok := d[modelsDevID]
	if !ok {
		return Metadata{}, false
	}
	m, ok := models[modelID]
	return m, ok
}

// --- the live document ---

type liveModel struct {
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

type liveProvider struct {
	Models map[string]liveModel `json:"models"`
}

// ParseModelsDev reads the document as models.dev publishes it.
//
// An empty result is an error rather than an empty catalog: a truncated CDN
// response would otherwise wipe every price on the next sync, and the sync's
// whole failure contract is that a bad fetch leaves the cache alone.
func ParseModelsDev(raw []byte) (Doc, error) {
	var doc map[string]liveProvider
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse models.dev document: %w", err)
	}
	out := make(Doc, len(doc))
	for pid, p := range doc {
		if len(p.Models) == 0 {
			continue
		}
		models := make(map[string]Metadata, len(p.Models))
		for mid, m := range p.Models {
			models[mid] = m.metadata()
		}
		out[pid] = models
	}
	if len(out) == 0 {
		return nil, errors.New("models.dev document contains no providers")
	}
	return out, nil
}

func (m liveModel) metadata() Metadata {
	out := Metadata{
		ContextWindow:   m.Limit.Context,
		MaxOutputTokens: m.Limit.Output,
		Capabilities: Capabilities{
			Tools:     m.ToolCall,
			Reasoning: m.Reasoning,
			// models.dev has no vision flag; it is expressed through the input
			// modalities. Looking for one leaves every multimodal model marked
			// text-only.
			Vision: hasImageInput(m.Modalities.Input),
			// A models.dev negative is knowledge, not absence of it, so the
			// router filters on it rather than admitting with a warning.
			Known: true,
		},
	}
	if m.Cost.Input != nil {
		out.InputMicrosPerMTok, out.PriceKnown = micros(*m.Cost.Input), true
	}
	if m.Cost.Output != nil {
		out.OutputMicrosPerMTok, out.PriceKnown = micros(*m.Cost.Output), true
	}
	if m.Cost.CacheRead != nil {
		out.CacheReadMicrosPerMTok = micros(*m.Cost.CacheRead)
	}
	return out
}

func hasImageInput(inputs []string) bool {
	for _, in := range inputs {
		if in == "image" {
			return true
		}
	}
	return false
}

// micros converts USD per million tokens to micro-dollars per million tokens.
// Rounded rather than truncated: the source is a float, and 0.14 arriving as
// 0.13999999999999999 must not lose a micro-dollar.
func micros(usdPerMTok float64) int64 {
	if usdPerMTok < 0 {
		return 0
	}
	return int64(usdPerMTok*1_000_000 + 0.5)
}

// --- the embedded fallback ---

// fallbackModel is the trimmed shape tools/presetgen emits. The short keys are
// what keeps a 4.2 MB document under a megabyte in the binary.
type fallbackModel struct {
	InputMicros     int64 `json:"i,omitempty"`
	OutputMicros    int64 `json:"o,omitempty"`
	CacheReadMicros int64 `json:"c,omitempty"`
	Context         int   `json:"w,omitempty"`
	MaxOutput       int   `json:"m,omitempty"`
	Tools           bool  `json:"t,omitempty"`
	Reasoning       bool  `json:"r,omitempty"`
	Vision          bool  `json:"v,omitempty"`
}

var (
	fallbackOnce sync.Once
	fallback     Doc
)

// FallbackDoc is the snapshot embedded at build time. It is what makes
// "Darkrouter starts and serves with no outbound access to models.dev" true on
// a first run, rather than only after one successful sync.
//
// A parse failure degrades to an empty Doc rather than panicking: the embedded
// file is generated, its shape is asserted by this package's tests, and a live
// gateway refusing to boot over it would be exactly the failure mode spec §4
// exists to prevent.
func FallbackDoc() Doc {
	fallbackOnce.Do(func() {
		var doc map[string]map[string]fallbackModel
		if err := json.Unmarshal(snapshotJSON, &doc); err != nil {
			fallback = Doc{}
			return
		}
		fallback = make(Doc, len(doc))
		for pid, models := range doc {
			out := make(map[string]Metadata, len(models))
			for mid, m := range models {
				out[mid] = Metadata{
					ContextWindow:          m.Context,
					MaxOutputTokens:        m.MaxOutput,
					InputMicrosPerMTok:     m.InputMicros,
					OutputMicrosPerMTok:    m.OutputMicros,
					CacheReadMicrosPerMTok: m.CacheReadMicros,
					PriceKnown:             m.InputMicros != 0 || m.OutputMicros != 0,
					Capabilities: Capabilities{
						Tools: m.Tools, Reasoning: m.Reasoning, Vision: m.Vision, Known: true,
					},
				}
			}
			fallback[pid] = out
		}
	})
	return fallback
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestCentPrice|TestVision|TestLimits|TestMiss|TestMalformed|TestEmbeddedFallback' -race -count=1 -v
```

Expected: PASS, seven tests. If `TestEmbeddedFallbackAgreesWithTheLiveShape` fails, the generator's rounding and this file's `micros` disagree — fix `tools/presetgen`, regenerate, and re-run. Do not adjust the test to match.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/modelsdev.go internal/catalog/modelsdev_test.go
git commit -m "feat(catalog): map models.dev metadata fields"
```

---

### Task 6: Model-ID normalization and the models.dev join

**Files:**
- Create: `internal/catalog/normalize.go`
- Test: `internal/catalog/normalize_test.go`

**Interfaces:**
- Consumes: `catalog.Preset` (Task 1), `catalog.Doc` and `catalog.Metadata` (Task 5).
- Produces: `catalog.NormalizeModelID(string) string` and `catalog.Join(p Preset, doc Doc, modelID string) (Metadata, bool)`. Task 7's merge and Task 13's sync both call `Join`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: spec §4.1 states the rule — lowercase, strip a known provider path prefix, replace `:` with `-` — and the alias escape hatch.

Without a join rule the merge silently fails and every model falls back to inferred capabilities. That failure is invisible: nothing errors, prices are just absent everywhere.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/normalize_test.go`:

```go
package catalog

import "testing"

func TestNormalizeModelID(t *testing.T) {
	cases := []struct{ in, want string }{
		// Ollama's tag separator.
		{"llama3.3:70b", "llama3.3-70b"},
		// Fireworks' account path prefix.
		{"accounts/fireworks/models/llama-v3p3-70b", "llama-v3p3-70b"},
		// OpenRouter's vendor prefix.
		{"meta-llama/Llama-3.3-70B-Instruct", "llama-3.3-70b-instruct"},
		// Case only.
		{"GPT-4O-Mini", "gpt-4o-mini"},
		// Already normal.
		{"claude-opus-4-5", "claude-opus-4-5"},
		// A deep path keeps only the leaf.
		{"a/b/c/d", "d"},
		// Surrounding whitespace from a hand-edited config.
		{"  gpt-4o  ", "gpt-4o"},
		// Empty stays empty rather than becoming a match-anything key.
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeModelID(c.in); got != c.want {
			t.Errorf("NormalizeModelID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func joinDoc() Doc {
	return Doc{"togetherai": {
		"llama-3.3-70b-instruct": {ContextWindow: 131072, PriceKnown: true, InputMicrosPerMTok: 880_000},
	}}
}

func TestJoinFindsTheExactID(t *testing.T) {
	p := Preset{ModelsDevID: "togetherai"}
	m, ok := Join(p, joinDoc(), "llama-3.3-70b-instruct")
	if !ok || m.ContextWindow != 131072 {
		t.Fatalf("exact join failed: %+v %v", m, ok)
	}
}

func TestJoinNormalizesBeforeMatching(t *testing.T) {
	p := Preset{ModelsDevID: "togetherai"}
	for _, id := range []string{
		"meta-llama/Llama-3.3-70B-Instruct",
		"Llama-3.3-70B-Instruct",
		"llama-3.3:70b-instruct",
	} {
		if _, ok := Join(p, joinDoc(), id); !ok {
			t.Errorf("%q did not join", id)
		}
	}
}

func TestJoinUsesAnExplicitAlias(t *testing.T) {
	// The alias is the escape hatch for the forms normalization cannot reach:
	// Fireworks spells the same family llama-v3p3-70b, which shares no
	// normalized form with llama-3.3-70b.
	p := Preset{
		ModelsDevID:  "togetherai",
		ModelAliases: map[string]string{"accounts/fireworks/models/llama-v3p3-70b": "llama-3.3-70b-instruct"},
	}
	m, ok := Join(p, joinDoc(), "accounts/fireworks/models/llama-v3p3-70b")
	if !ok || m.ContextWindow != 131072 {
		t.Fatalf("alias join failed: %+v %v", m, ok)
	}
}

func TestJoinMissIsNotAnError(t *testing.T) {
	// Spec §4.1: a model that fails to join is not an error — it carries
	// inferred capabilities. The caller needs the miss reported, not a zero
	// value that looks like a zero-cost, zero-context model.
	p := Preset{ModelsDevID: "togetherai"}
	if _, ok := Join(p, joinDoc(), "some-private-finetune"); ok {
		t.Error("an unknown model reported a join")
	}
}

func TestJoinSkipsExemptPresets(t *testing.T) {
	// An exempt preset has no join key. Falling through to a normalized lookup
	// against the empty string would match whatever the document keys as "".
	p := Preset{NoModelsDev: true}
	if _, ok := Join(p, joinDoc(), "llama-3.3-70b-instruct"); ok {
		t.Error("an exempt preset joined anyway")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestNormalize|TestJoin' -v
```

Expected: FAIL to build — `undefined: NormalizeModelID`, `undefined: Join`.

- [ ] **Step 3: Write the join**

Create `internal/catalog/normalize.go`:

```go
package catalog

import "strings"

// NormalizeModelID reduces the identifier forms three ecosystems use for the
// same model to one key.
//
// Discovery reports llama3.3:70b from Ollama and
// accounts/fireworks/models/llama-v3p3-70b from Fireworks; models.dev calls the
// family llama-3.3-70b. Without a join rule the merge fails silently — nothing
// errors, prices are simply absent everywhere and every model falls back to
// inferred capabilities.
func NormalizeModelID(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	// Keep only the leaf: every known prefix form (vendor/, accounts/x/models/)
	// is a path, and the leaf is what the metadata sources key on.
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	// Ollama separates a model from its tag with a colon where every other
	// source uses a dash.
	return strings.ReplaceAll(s, ":", "-")
}

// Join resolves one model's metadata through its provider's preset.
//
// The order is: explicit alias, exact id, normalized id. The alias comes first
// because it exists precisely for the forms the rule cannot reach, and a rule
// that happened to match would otherwise silently outrank the operator.
func Join(p Preset, doc Doc, modelID string) (Metadata, bool) {
	if p.NoModelsDev || p.ModelsDevID == "" {
		return Metadata{}, false
	}
	if alias, ok := p.ModelAliases[modelID]; ok {
		if m, ok := doc.Metadata(p.ModelsDevID, alias); ok {
			return m, true
		}
	}
	if m, ok := doc.Metadata(p.ModelsDevID, modelID); ok {
		return m, true
	}
	want := NormalizeModelID(modelID)
	if want == "" {
		return Metadata{}, false
	}
	for id, m := range doc[p.ModelsDevID] {
		if NormalizeModelID(id) == want {
			return m, true
		}
	}
	return Metadata{}, false
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestNormalize|TestJoin' -race -count=1 -v
```

Expected: PASS, six tests.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/normalize.go internal/catalog/normalize_test.go
git commit -m "feat(catalog): join model ids to models.dev"
```

---

### Task 7: Providers carry their preset

**Files:**
- Modify: `internal/provider/provider.go`
- Modify: `internal/provider/sqlsource.go`
- Test: `internal/provider/sqlsource_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `provider.Provider.Preset` and `provider.Provider.AuthStyle`, populated from the `providers` table. Tasks 8, 12, 13 and 20 all resolve a preset from a provider row through these.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: two columns that already exist in the schema are added to a struct that already selects four of its neighbours.

The `providers` table has carried `preset` and `auth_style` since migration 0001, but `SQLSource.Reload` never selected them. Everything downstream of Phase 6 — merge, discovery, quirks — needs to reach a provider's preset, and there is no path to it without this.

- [ ] **Step 1: Write the failing test**

Add to `internal/provider/sqlsource_test.go`:

```go
func TestReloadCarriesPresetAndAuthStyle(t *testing.T) {
	ctx := context.Background()
	db, key := sqlSourceDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, preset, kind, base_url, auth_style, priority, created_at)
		 VALUES ('p1', 'groq', 'openaicompat', 'http://x', 'bearer', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertCredential(ctx, key, store.Credential{
		ProviderID: "p1", Kind: "static", Secret: "s", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	src := NewSQLSource(db, key)
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	ps, _ := src.Providers(ctx)
	if len(ps) != 1 {
		t.Fatalf("got %d providers", len(ps))
	}
	if ps[0].Preset != "groq" {
		t.Errorf("preset = %q, want groq", ps[0].Preset)
	}
	if ps[0].AuthStyle != "bearer" {
		t.Errorf("auth style = %q, want bearer", ps[0].AuthStyle)
	}
}

func TestReloadTolerationsForUncataloguedProviders(t *testing.T) {
	// An uncatalogued provider is a base URL and a key with no preset at all.
	// It must load, not fail: master design section 6 makes that the whole
	// point of the two-tier model.
	ctx := context.Background()
	db, key := sqlSourceDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p1', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertCredential(ctx, key, store.Credential{
		ProviderID: "p1", Kind: "static", Secret: "s", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	src := NewSQLSource(db, key)
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	ps, _ := src.Providers(ctx)
	if len(ps) != 1 || ps[0].Preset != "" {
		t.Fatalf("providers = %+v", ps)
	}
	if ps[0].AuthStyle != "bearer" {
		t.Errorf("auth style = %q, want the column default bearer", ps[0].AuthStyle)
	}
}
```

If `sqlSourceDB` and the credential-insert helper are spelled differently in the existing file, use whatever that file already uses — read it before writing, and do not add a second helper alongside an equivalent one.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/provider/ -run TestReload -v
```

Expected: FAIL to build — `ps[0].Preset undefined`.

- [ ] **Step 3: Add the fields**

In `internal/provider/provider.go`, extend the struct:

```go
type Provider struct {
	ID      string
	Kind    string
	BaseURL string

	// Preset names the shipped entry this provider was created from, or is
	// empty for an uncatalogued one. It is how quirks, surfaces, model traits
	// and the models.dev join key are reached at request time.
	Preset string
	// AuthStyle is the provider row's override of its preset's style.
	AuthStyle string

	// Credentials are every enabled credential, ordered by id. Credential
	// rotation happens before advancing to the next provider, so the router
	// needs all of them rather than a chosen one.
	Credentials []Credential

	Priority int
	Models   []string
}
```

In `internal/provider/sqlsource.go`, select and carry them:

```go
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT id, preset, kind, base_url, auth_style, priority
		   FROM providers
		  WHERE enabled = 1
		  ORDER BY priority DESC, id`)
```

```go
	type row struct {
		id, preset, kind, baseURL, authStyle string
		priority                             int
	}
```

```go
		if err := rows.Scan(&r.id, &r.preset, &r.kind, &r.baseURL, &r.authStyle, &r.priority); err != nil {
			return fmt.Errorf("scan provider: %w", err)
		}
```

```go
		out = append(out, Provider{
			ID: r.id, Kind: r.kind, BaseURL: r.baseURL,
			Preset: r.preset, AuthStyle: r.authStyle,
			Credentials: enabled,
			Priority:    r.priority, Models: models,
		})
```

Also extend `revisionOf` so a preset change invalidates a consumer's cache. Without it, changing a provider's preset in the UI leaves every cached snapshot serving the old quirks:

```go
	for _, p := range sorted {
		_, _ = h.Write([]byte(p.ID))
		_, _ = h.Write([]byte(p.BaseURL))
		_, _ = h.Write([]byte(p.Preset))
		_, _ = h.Write([]byte(strconv.Itoa(p.Priority)))
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/provider/ -race -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/provider/provider.go internal/provider/sqlsource.go internal/provider/sqlsource_test.go
git commit -m "feat(provider): carry preset and auth style"
```

---

### Task 8: The merge

**Files:**
- Modify: `internal/catalog/catalog.go`
- Create: `internal/catalog/merge.go`
- Test: `internal/catalog/merge_test.go`

**Interfaces:**
- Consumes: `catalog.Presets` (Task 1), `catalog.Doc` / `Join` (Tasks 5, 6), `store.ModelRow` / `store.ModelOverride` (Task 4), `provider.Provider.Preset` (Task 7).
- Produces: the extended `catalog.Model` (gaining `State`, `ContextWindow`, `MaxOutputTokens`, `Traits`, `Pricing`), `catalog.Traits`, `catalog.Pricing`, `catalog.State`, and `catalog.Merge(MergeInput) []Model`. Task 9's snapshot is the only caller.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: spec §7's table fixes the winner for every field, and the traits precedence is settled in this plan's "Two facts" section.

Spec §7, resolved **per field, not per record**:

| Field | Winner |
|---|---|
| Model exists / is routable | Live discovery, then models.dev for kinds without discovery (Vertex) |
| Capabilities | Override, then models.dev, then discovered, then inferred |
| Context window, max output, pricing | models.dev, then preset, then unknown |
| Base URL, auth style, quirks, surfaces | Provider row override, then preset |

Presets declare no model lists, so they are **not** an existence source. An earlier draft's table cited them for exactly that and could not be implemented; findings ledger X13 records the correction.

- [ ] **Step 1: Extend the model type**

In `internal/catalog/catalog.go`, add the new types and fields. `Capabilities`, `Source` and `Reader` are unchanged.

```go
// State is a model's lifecycle position. Spec §5.1.
type State string

const (
	StateLive            State = "live"
	StateStale           State = "stale"
	StateRemovedUpstream State = "removed_upstream"
)

// Traits are the per-generation request-shape facts an adapter needs and no
// metadata source can supply.
//
// models.dev lists "effort" among claude-opus-4-5's reasoning_options, but
// phase 4 verified live that thinking:{type:"adaptive"} is a 400 on that model:
// output_config.effort and adaptive thinking are different controls sharing a
// word. Traits are therefore declared in a preset and never derived.
//
// Known is what an adapter checks before letting these decide a wire shape. An
// unknown set means the request is shaped by what the client asked for, which
// is the right answer for a proxied or self-hosted endpoint whose name says
// nothing about its generation.
type Traits struct {
	Adaptive     bool
	ManualBudget bool
	FreeSampling bool
	Known        bool
}

// Pricing is micro-dollars per million tokens. Known separates a free model
// from an unpriced one; both are zero.
type Pricing struct {
	InputMicrosPerMTok     int64
	OutputMicrosPerMTok    int64
	CacheReadMicrosPerMTok int64
	Known                  bool
}

// Model is one model as offered by one provider.
type Model struct {
	ProviderID   string
	ModelID      string
	Publisher    string
	Surfaces     []ir.Surface
	Capabilities Capabilities
	Source       Source

	State           State
	ContextWindow   int
	MaxOutputTokens int
	Traits          Traits
	Pricing         Pricing
}

// Routable reports whether the router may attempt this model. A stale model is
// routable: the breaker rather than the catalog is what avoids a provider that
// is actually broken, and emptying the catalog on a flaky probe would break
// every alias pointing at it.
func (m Model) Routable() bool { return m.State != StateRemovedUpstream }
```

`FromProviders` stays as it is — it is Phase 3's fallback for a gateway with no catalog store wired, and Task 15 keeps it as the nil-Store path. Give its models `State: StateLive` so `Routable` is true for them:

```go
			models[id] = Model{
				ProviderID: p.ID,
				ModelID:    id,
				Surfaces:   []ir.Surface{ir.SurfaceLLM},
				Source:     SourceInferred,
				State:      StateLive,
			}
```

- [ ] **Step 2: Write the failing test**

Create `internal/catalog/merge_test.go`:

```go
package catalog

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

func mergeInput() MergeInput {
	return MergeInput{
		Providers: []provider.Provider{{ID: "p", Kind: "openaicompat", Preset: "acme"}},
		Presets: Presets{"acme": {
			Name: "Acme", Kind: "openaicompat", ModelsDevID: "acme",
			Surfaces: []string{"llm", "embeddings"},
			ModelTraits: []TraitRule{
				{Match: "big", Adaptive: true},
				{Match: "big-v2", Adaptive: true, ManualBudget: true, FreeSampling: true},
			},
		}},
		Doc: Doc{"acme": {
			"big": {
				ContextWindow: 200_000, MaxOutputTokens: 64_000,
				InputMicrosPerMTok: 5_000_000, PriceKnown: true,
				Capabilities: Capabilities{Tools: true, Vision: true, Known: true},
			},
		}},
		Rows: []store.ModelRow{
			{ProviderID: "p", ModelID: "big", State: "live", CapabilitiesSource: "inferred"},
		},
	}
}

func find(t *testing.T, ms []Model, id string) Model {
	t.Helper()
	for _, m := range ms {
		if m.ModelID == id {
			return m
		}
	}
	t.Fatalf("model %q not in %d results", id, len(ms))
	return Model{}
}

func TestMergeTakesLimitsAndPricingFromModelsDev(t *testing.T) {
	m := find(t, Merge(mergeInput()), "big")
	if m.ContextWindow != 200_000 || m.MaxOutputTokens != 64_000 {
		t.Errorf("limits = (%d, %d)", m.ContextWindow, m.MaxOutputTokens)
	}
	if m.Pricing.InputMicrosPerMTok != 5_000_000 || !m.Pricing.Known {
		t.Errorf("pricing = %+v", m.Pricing)
	}
	if m.Source != SourceModelsDev {
		t.Errorf("source = %q, want models_dev", m.Source)
	}
	if !m.Capabilities.Known || !m.Capabilities.Tools || !m.Capabilities.Vision {
		t.Errorf("capabilities = %+v", m.Capabilities)
	}
}

func TestMergePrefersDiscoveredOverModelsDevForCapabilities(t *testing.T) {
	// A runtime that reports its own capabilities outranks a directory's guess
	// about a model of the same name.
	in := mergeInput()
	in.Rows[0].CapabilitiesSource = "discovered"
	in.Rows[0].Capabilities = store.ModelCapabilities{Tools: false}
	m := find(t, Merge(in), "big")
	if m.Capabilities.Tools {
		t.Error("models.dev outranked a discovered capability")
	}
	if m.Source != SourceDiscovered {
		t.Errorf("source = %q, want discovered", m.Source)
	}
	// Metadata still comes from models.dev: precedence is per field.
	if m.ContextWindow != 200_000 {
		t.Errorf("context window = %d; capability precedence leaked into limits", m.ContextWindow)
	}
}

func TestMergeOverrideBeatsEverything(t *testing.T) {
	in := mergeInput()
	in.Rows[0].CapabilitiesSource = "discovered"
	in.Rows[0].Capabilities = store.ModelCapabilities{Tools: false}
	ctxWin := 999
	in.Overrides = []store.ModelOverride{{
		ProviderID: "p", ModelID: "big",
		Capabilities:  &store.ModelCapabilities{Tools: true, Reasoning: true},
		ContextWindow: &ctxWin,
		Surfaces:      []string{"llm"},
	}}
	m := find(t, Merge(in), "big")
	if !m.Capabilities.Tools || !m.Capabilities.Reasoning || m.Capabilities.Vision {
		t.Errorf("capabilities = %+v", m.Capabilities)
	}
	if m.Source != SourceOverride {
		t.Errorf("source = %q, want override", m.Source)
	}
	if m.ContextWindow != 999 {
		t.Errorf("context window = %d, want 999", m.ContextWindow)
	}
	if len(m.Surfaces) != 1 || m.Surfaces[0] != ir.SurfaceLLM {
		t.Errorf("surfaces = %v", m.Surfaces)
	}
}

func TestMergeFallsBackToInferred(t *testing.T) {
	// A model nothing knows about still exists and still routes. Spec §6: the
	// local-model story depends on it.
	in := mergeInput()
	in.Rows = append(in.Rows, store.ModelRow{
		ProviderID: "p", ModelID: "private-finetune", State: "live", CapabilitiesSource: "inferred",
	})
	m := find(t, Merge(in), "private-finetune")
	if m.Source != SourceInferred || m.Capabilities.Known {
		t.Errorf("source = %q, known = %v", m.Source, m.Capabilities.Known)
	}
	if m.ContextWindow != 0 || m.Pricing.Known {
		t.Errorf("invented metadata: %+v", m)
	}
	if !m.Routable() {
		t.Error("an inferred model is not routable")
	}
}

func TestMergeTakesSurfacesFromThePreset(t *testing.T) {
	m := find(t, Merge(mergeInput()), "big")
	if len(m.Surfaces) != 2 || m.Surfaces[0] != ir.SurfaceLLM || m.Surfaces[1] != ir.SurfaceEmbeddings {
		t.Errorf("surfaces = %v", m.Surfaces)
	}
}

func TestMergeTakesTraitsFromTheLongestPresetMatch(t *testing.T) {
	// "big-v2" contains "big". Shortest-first would give it the wrong wire
	// shape, which is a 400 on every reasoning request.
	in := mergeInput()
	in.Rows = append(in.Rows, store.ModelRow{
		ProviderID: "p", ModelID: "big-v2", State: "live", CapabilitiesSource: "inferred",
	})
	v2 := find(t, Merge(in), "big-v2")
	if !v2.Traits.Known || !v2.Traits.ManualBudget || !v2.Traits.FreeSampling {
		t.Errorf("big-v2 traits = %+v", v2.Traits)
	}
	big := find(t, Merge(in), "big")
	if !big.Traits.Known || big.Traits.ManualBudget {
		t.Errorf("big traits = %+v", big.Traits)
	}
}

func TestMergeLeavesTraitsUnknownWithoutAPreset(t *testing.T) {
	// An uncatalogued provider declares nothing. The adapter must then honor
	// what the client asked for rather than acting on a guess.
	in := mergeInput()
	in.Providers[0].Preset = ""
	m := find(t, Merge(in), "big")
	if m.Traits.Known {
		t.Errorf("traits invented for a presetless provider: %+v", m.Traits)
	}
}

func TestMergeCarriesStateThrough(t *testing.T) {
	in := mergeInput()
	in.Rows[0].State = "removed_upstream"
	m := find(t, Merge(in), "big")
	if m.State != StateRemovedUpstream || m.Routable() {
		t.Errorf("state = %q, routable = %v", m.State, m.Routable())
	}
	in.Rows[0].State = "stale"
	stale := find(t, Merge(in), "big")
	if !stale.Routable() {
		t.Error("a stale model is not routable; the breaker is what avoids a broken provider")
	}
}

func TestMergeIgnoresRowsOfUnknownProviders(t *testing.T) {
	// A provider deleted between the snapshot read and the merge leaves orphan
	// rows. Emitting them would offer models nothing can route to.
	in := mergeInput()
	in.Rows = append(in.Rows, store.ModelRow{ProviderID: "gone", ModelID: "x", State: "live"})
	for _, m := range Merge(in) {
		if m.ProviderID == "gone" {
			t.Error("an orphan row survived the merge")
		}
	}
}

func TestMergeIsDeterministic(t *testing.T) {
	// Two runs over the same input must produce the same order, or a snapshot
	// rebuild silently reorders the candidate list a request sees.
	a, b := Merge(mergeInput()), Merge(mergeInput())
	if len(a) != len(b) {
		t.Fatalf("lengths differ: %d and %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ProviderID != b[i].ProviderID || a[i].ModelID != b[i].ModelID {
			t.Fatalf("order differs at %d: %v and %v", i, a[i], b[i])
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run TestMerge -v
```

Expected: FAIL to build — `undefined: MergeInput`, `undefined: Merge`.

- [ ] **Step 4: Write the merge**

Create `internal/catalog/merge.go`:

```go
package catalog

import (
	"sort"
	"strings"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// MergeInput is every source the catalog resolves between.
type MergeInput struct {
	Providers []provider.Provider
	Presets   Presets
	Doc       Doc
	Rows      []store.ModelRow
	Overrides []store.ModelOverride
}

// Merge resolves spec §7's precedence table per field.
//
// Per field rather than per record is the whole point: a model whose
// capabilities are discovered still takes its context window from models.dev,
// and letting one source win a whole record would throw away three quarters of
// what is known about it.
func Merge(in MergeInput) []Model {
	byID := make(map[string]provider.Provider, len(in.Providers))
	for _, p := range in.Providers {
		byID[p.ID] = p
	}
	overrides := make(map[[2]string]store.ModelOverride, len(in.Overrides))
	for _, o := range in.Overrides {
		overrides[[2]string{o.ProviderID, o.ModelID}] = o
	}

	out := make([]Model, 0, len(in.Rows))
	for _, row := range in.Rows {
		p, ok := byID[row.ProviderID]
		if !ok {
			// A provider deleted between the read and the merge leaves orphan
			// rows. Offering them would advertise models nothing can route to.
			continue
		}
		preset := in.Presets[p.Preset] // the zero Preset for an uncatalogued provider
		out = append(out, mergeOne(row, p, preset, in.Doc, overrides[[2]string{row.ProviderID, row.ModelID}]))
	}
	// Deterministic order: a snapshot rebuild must not reorder the candidate
	// list a request sees.
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out
}

func mergeOne(row store.ModelRow, p provider.Provider, preset Preset,
	doc Doc, override store.ModelOverride) Model {

	m := Model{
		ProviderID: row.ProviderID,
		ModelID:    row.ModelID,
		Publisher:  row.Publisher,
		State:      state(row.State),
		Surfaces:   surfaces(row, preset, override),
		Traits:     traitsFor(preset, row.ModelID),
	}

	// Capabilities and limits: models.dev, then what the row already carries.
	meta, joined := Join(preset, doc, row.ModelID)
	if joined {
		m.Capabilities = meta.Capabilities
		m.Source = SourceModelsDev
		m.ContextWindow = meta.ContextWindow
		m.MaxOutputTokens = meta.MaxOutputTokens
		m.Pricing = Pricing{
			InputMicrosPerMTok:     meta.InputMicrosPerMTok,
			OutputMicrosPerMTok:    meta.OutputMicrosPerMTok,
			CacheReadMicrosPerMTok: meta.CacheReadMicrosPerMTok,
			Known:                  meta.PriceKnown,
		}
	} else {
		m.ContextWindow = row.ContextWindow
		m.MaxOutputTokens = row.MaxOutputTokens
		m.Pricing = Pricing{
			InputMicrosPerMTok:     row.InputMicrosPerMTok,
			OutputMicrosPerMTok:    row.OutputMicrosPerMTok,
			CacheReadMicrosPerMTok: row.CacheReadMicrosPerMTok,
			Known:                  row.PriceKnown,
		}
		m.Source = SourceInferred
	}

	// A runtime that reports its own capabilities outranks a directory's guess
	// about a model of the same name — that is what makes Ollama's tool
	// support a fact rather than an inference.
	if row.CapabilitiesSource == string(SourceDiscovered) {
		m.Capabilities = Capabilities{
			Tools:     row.Capabilities.Tools,
			Vision:    row.Capabilities.Vision,
			Reasoning: row.Capabilities.Reasoning,
			Known:     true,
		}
		m.Source = SourceDiscovered
	}

	// The operator's override wins outright. Per (provider, model), never per
	// provider: one Ollama instance serves models with wildly different tool
	// support.
	if override.Capabilities != nil {
		m.Capabilities = Capabilities{
			Tools:     override.Capabilities.Tools,
			Vision:    override.Capabilities.Vision,
			Reasoning: override.Capabilities.Reasoning,
			Known:     true,
		}
		m.Source = SourceOverride
	}
	if override.ContextWindow != nil {
		m.ContextWindow = *override.ContextWindow
	}

	// context-override= is the preset's answer for an upstream that serves a
	// smaller window than the model's own. It loses to an operator override
	// and wins over models.dev, because a preset saying so is evidence about
	// this upstream rather than about the model.
	if v, ok := preset.QuirkValue("context-override"); ok && override.ContextWindow == nil {
		if n := atoiOrZero(v); n > 0 {
			m.ContextWindow = n
		}
	}
	return m
}

func state(s string) State {
	switch State(s) {
	case StateStale:
		return StateStale
	case StateRemovedUpstream:
		return StateRemovedUpstream
	default:
		// An unrecognized value routes rather than disappearing. A row written
		// by a newer binary must not become invisible on a rollback.
		return StateLive
	}
}

// surfaces resolves the override, then the row, then the preset, then llm.
// A model that declares nothing still serves chat, which is what every
// discovery probe that reports a bare id has actually told us.
func surfaces(row store.ModelRow, preset Preset, override store.ModelOverride) []ir.Surface {
	for _, candidate := range [][]string{override.Surfaces, row.Surfaces, preset.Surfaces} {
		if parsed := parseSurfaces(candidate); len(parsed) > 0 {
			return parsed
		}
	}
	return []ir.Surface{ir.SurfaceLLM}
}

func parseSurfaces(raw []string) []ir.Surface {
	out := make([]ir.Surface, 0, len(raw))
	for _, s := range raw {
		if parsed, ok := ir.ParseSurface(s); ok {
			out = append(out, parsed)
		}
	}
	return out
}

// traitsFor picks the longest matching rule. Longest rather than first because
// "big-v2" contains "big", and the shorter rule would otherwise decide a wire
// shape that is a 400 on every reasoning request.
func traitsFor(preset Preset, modelID string) Traits {
	name := strings.ReplaceAll(strings.ToLower(modelID), ".", "-")
	best := -1
	var out Traits
	for _, rule := range preset.ModelTraits {
		match := strings.ReplaceAll(strings.ToLower(rule.Match), ".", "-")
		if match == "" || !strings.Contains(name, match) {
			continue
		}
		if len(match) > best {
			best = len(match)
			out = Traits{
				Adaptive:     rule.Adaptive,
				ManualBudget: rule.ManualBudget,
				FreeSampling: rule.FreeSampling,
				Known:        true,
			}
		}
	}
	return out
}

func atoiOrZero(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run TestMerge -race -count=1 -v
```

Expected: PASS, ten tests.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing. `internal/router` and `internal/exec` still compile because `Model` only gained fields.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog/catalog.go internal/catalog/merge.go internal/catalog/merge_test.go
git commit -m "feat(catalog): resolve source precedence per field"
```

---

### Task 9: The snapshot and its atomic store

**Files:**
- Create: `internal/catalog/snapshot.go`
- Test: `internal/catalog/snapshot_test.go`

**Interfaces:**
- Consumes: `catalog.Merge` (Task 8), `store.DB.Models` / `ModelOverrides` (Task 4), `provider.Source` (Task 7).
- Produces: `catalog.Snapshot` (implementing `catalog.Reader`), `catalog.Filter`, `catalog.NewSnapshot([]Model, []string) *Snapshot`, and `catalog.Store` with `NewStore(*store.DB, provider.Source) *Store`, `(*Store).Snapshot() *Snapshot`, `(*Store).Rebuild(context.Context) error`. Tasks 12, 14, 15 and 19 all use these.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 2: spec §8 fixes the three methods and the immutable-plus-atomic-swap shape; `internal/health/availability.go` is the existing precedent for a frozen view.

Risk is 3 because this is the concurrency boundary. The snapshot is **immutable once built** and replaced wholesale, never mutated in place — that is what lets `router.Resolve` stay a pure function of its arguments while two background workers rewrite the catalog underneath it. A rebuild that mutated a live snapshot would be a data race the router could not defend against.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/snapshot_test.go`:

```go
package catalog

import (
	"sync"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func snapModels() []Model {
	return []Model{
		{ProviderID: "a", ModelID: "shared", State: StateLive, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "b", ModelID: "shared", State: StateStale, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "b", ModelID: "gone", State: StateRemovedUpstream, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "b", ModelID: "embed", State: StateLive, Surfaces: []ir.Surface{ir.SurfaceEmbeddings}},
	}
}

func TestSnapshotLookup(t *testing.T) {
	s := NewSnapshot(snapModels(), []string{"b", "a"})
	m, ok := s.Lookup("a", "shared")
	if !ok || m.State != StateLive {
		t.Fatalf("lookup = %+v %v", m, ok)
	}
	if _, ok := s.Lookup("a", "nope"); ok {
		t.Error("an unknown model reported a hit")
	}
	// A retired model is still looked up: the trace view and the UI show it
	// with provenance. Only routing excludes it.
	gone, ok := s.Lookup("b", "gone")
	if !ok {
		t.Fatal("a removed_upstream model vanished from Lookup")
	}
	if gone.Routable() {
		t.Error("a removed_upstream model reports routable")
	}
}

func TestOfferingIsInProviderOrderAndExcludesRemoved(t *testing.T) {
	// The provider order is priority order, and it is what decides which
	// provider a bare model name is attempted against first.
	s := NewSnapshot(snapModels(), []string{"b", "a"})
	got := s.Offering("shared")
	if len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Errorf("Offering(shared) = %v, want [b a]", got)
	}
	// Stale stays offered; the breaker is what avoids a broken provider.
	if len(s.Offering("gone")) != 0 {
		t.Errorf("Offering(gone) = %v, want empty", s.Offering("gone"))
	}
}

func TestSearchFilters(t *testing.T) {
	s := NewSnapshot(snapModels(), []string{"a", "b"})
	if got := s.Search(Filter{Surface: ir.SurfaceEmbeddings}); len(got) != 1 || got[0].ModelID != "embed" {
		t.Errorf("surface filter = %v", got)
	}
	if got := s.Search(Filter{ProviderID: "a"}); len(got) != 1 || got[0].ProviderID != "a" {
		t.Errorf("provider filter = %v", got)
	}
	if got := s.Search(Filter{Query: "SHAR"}); len(got) != 2 {
		t.Errorf("query filter matched %d, want 2 (case-insensitive substring)", len(got))
	}
	// Removed models are excluded by default and reachable on request, because
	// the UI has to show them to offer a purge.
	if got := s.Search(Filter{}); len(got) != 3 {
		t.Errorf("default search returned %d, want 3", len(got))
	}
	if got := s.Search(Filter{IncludeRemoved: true}); len(got) != 4 {
		t.Errorf("IncludeRemoved returned %d, want 4", len(got))
	}
}

func TestSnapshotSatisfiesReader(t *testing.T) {
	var _ Reader = NewSnapshot(nil, nil)
}

func TestStoreServesAnEmptySnapshotBeforeFirstRebuild(t *testing.T) {
	// A request arriving before the first rebuild must get an answer, not a
	// nil dereference.
	var st Store
	s := st.Snapshot()
	if s == nil {
		t.Fatal("zero Store returned a nil snapshot")
	}
	if _, ok := s.Lookup("a", "b"); ok {
		t.Error("the empty snapshot reported a hit")
	}
	if len(s.Offering("b")) != 0 {
		t.Error("the empty snapshot offered something")
	}
}

func TestStoreSwapIsRaceFree(t *testing.T) {
	// The whole design: readers hold an immutable snapshot while a worker
	// replaces it. Run under -race; without the atomic swap this fails.
	var st Store
	st.Set(NewSnapshot(snapModels(), []string{"a", "b"}))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s := st.Snapshot()
				_, _ = s.Lookup("a", "shared")
				_ = s.Offering("shared")
				_ = s.Search(Filter{Surface: ir.SurfaceLLM})
			}
		}()
	}
	for i := 0; i < 200; i++ {
		st.Set(NewSnapshot(snapModels(), []string{"a", "b"}))
	}
	close(stop)
	wg.Wait()
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestSnapshot|TestOffering|TestSearch|TestStore' -race -v
```

Expected: FAIL to build — `undefined: NewSnapshot`, `undefined: Store`, `undefined: Filter`.

- [ ] **Step 3: Write the snapshot**

Create `internal/catalog/snapshot.go`:

```go
package catalog

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// Snapshot is the catalog frozen at one instant.
//
// It is built once and never mutated, which is what lets router.Resolve stay a
// pure function of its arguments while two background workers rewrite the
// catalog underneath it. Updating one in place would be a data race the router
// has no way to defend against.
type Snapshot struct {
	byProvider map[string]map[string]Model
	offering   map[string][]string
	all        []Model
}

// Filter narrows a Search. The zero value returns every routable model.
type Filter struct {
	Surface    ir.Surface
	ProviderID string
	// Query is a case-insensitive substring match on the model id.
	Query string
	// IncludeRemoved admits models discovery has retired. The UI needs them to
	// show provenance and offer a purge; routing never does.
	IncludeRemoved bool
}

// NewSnapshot indexes a merged model set. providerOrder is the provider set's
// own order, which is priority order — it decides which provider a bare model
// name is attempted against first, so it cannot be recovered by sorting.
func NewSnapshot(models []Model, providerOrder []string) *Snapshot {
	s := &Snapshot{
		byProvider: make(map[string]map[string]Model),
		offering:   make(map[string][]string),
		all:        models,
	}
	for _, m := range models {
		byModel, ok := s.byProvider[m.ProviderID]
		if !ok {
			byModel = make(map[string]Model)
			s.byProvider[m.ProviderID] = byModel
		}
		byModel[m.ModelID] = m
	}
	// Walking providerOrder rather than models is what puts the offering list
	// in priority order.
	for _, pid := range providerOrder {
		for _, m := range models {
			if m.ProviderID != pid || !m.Routable() {
				continue
			}
			s.offering[m.ModelID] = append(s.offering[m.ModelID], pid)
		}
	}
	return s
}

// Lookup returns the model as offered by one provider, retired ones included.
// The trace view and the catalog screen both show a retired model with its
// provenance; only routing excludes it, through Routable.
func (s *Snapshot) Lookup(providerID, modelID string) (Model, bool) {
	byModel, ok := s.byProvider[providerID]
	if !ok {
		return Model{}, false
	}
	m, ok := byModel[modelID]
	return m, ok
}

// Offering returns the provider ids offering modelID in priority order,
// excluding the ones where it has been retired upstream.
func (s *Snapshot) Offering(modelID string) []string { return s.offering[modelID] }

// Search is the catalog screen's query. It is not on the request path.
func (s *Snapshot) Search(f Filter) []Model {
	q := strings.ToLower(f.Query)
	out := make([]Model, 0, len(s.all))
	for _, m := range s.all {
		if !f.IncludeRemoved && !m.Routable() {
			continue
		}
		if f.ProviderID != "" && m.ProviderID != f.ProviderID {
			continue
		}
		if f.Surface != "" && !m.DeclaresSurface(f.Surface) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(m.ModelID), q) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// All returns every model including retired ones, in merge order.
func (s *Snapshot) All() []Model { return s.all }

var _ Reader = (*Snapshot)(nil)

// Store holds the live snapshot. The zero value is usable and serves an empty
// snapshot, so a request arriving before the first rebuild gets an answer
// rather than a nil dereference.
type Store struct {
	db  *store.DB
	src provider.Source
	cur atomic.Pointer[Snapshot]
}

func NewStore(db *store.DB, src provider.Source) *Store {
	return &Store{db: db, src: src}
}

// empty is shared: it is immutable, so one instance serves every Store that
// has not rebuilt yet.
var empty = NewSnapshot(nil, nil)

// Snapshot returns the live view. The router takes exactly one per request,
// which is what keeps a request's routing decision internally consistent even
// when a worker swaps the catalog mid-flight.
func (s *Store) Snapshot() *Snapshot {
	if cur := s.cur.Load(); cur != nil {
		return cur
	}
	return empty
}

// Set replaces the live snapshot. Exported for tests and for callers that
// build a snapshot from something other than the database.
func (s *Store) Set(snap *Snapshot) { s.cur.Store(snap) }

// Rebuild reads every source and swaps in a new snapshot.
//
// It swaps only on full success: a failed read leaves the previous snapshot
// serving, which is the same rule the config store applies to a broken edit and
// the sync worker applies to an unreachable CDN.
func (s *Store) Rebuild(ctx context.Context) error {
	if s.db == nil || s.src == nil {
		return nil
	}
	providers, err := s.src.Providers(ctx)
	if err != nil {
		return fmt.Errorf("catalog rebuild: providers: %w", err)
	}
	rows, err := s.db.Models(ctx)
	if err != nil {
		return fmt.Errorf("catalog rebuild: models: %w", err)
	}
	overrides, err := s.db.ModelOverrides(ctx)
	if err != nil {
		return fmt.Errorf("catalog rebuild: overrides: %w", err)
	}

	order := make([]string, 0, len(providers))
	for _, p := range providers {
		order = append(order, p.ID)
	}
	merged := Merge(MergeInput{
		Providers: providers,
		Presets:   Embedded(),
		// The live document is written into the models rows by the sync
		// worker, so the merge reads the embedded fallback here. That is what
		// makes a cold start with no network produce real prices rather than
		// waiting twelve hours for the first sync.
		Doc:       FallbackDoc(),
		Rows:      rows,
		Overrides: overrides,
	})
	s.Set(NewSnapshot(merged, order))
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestSnapshot|TestOffering|TestSearch|TestStore' -race -count=1 -v
```

Expected: PASS, six tests. `TestStoreSwapIsRaceFree` must pass **under `-race`** specifically; without the race detector it proves nothing.

- [ ] **Step 5: Run the swap test repeatedly**

A single race-detector pass only observes the interleavings that run happened to schedule.

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run TestStoreSwapIsRaceFree -race -count=10
```

Expected: `ok`, no `DATA RACE` reports.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog/snapshot.go internal/catalog/snapshot_test.go
git commit -m "feat(catalog): add immutable snapshot and store"
```

---

### Task 10: Discovery probes, per kind

**Files:**
- Create: `internal/catalog/probe.go`
- Test: `internal/catalog/probe_test.go`

**Interfaces:**
- Consumes: `catalog.Preset` (Task 1), `provider.Provider` (Task 7).
- Produces: `catalog.Probe`, `catalog.Discovered`, `catalog.ProbeFor(provider.Provider, Preset, string) (Probe, error)`, `catalog.BuildListRequest(context.Context, Probe) (*http.Request, error)`, `catalog.ParseList(kind string, body []byte) ([]Discovered, error)`, and `catalog.ErrKindNotDiscoverable`. Task 12's worker is the only caller.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: spec §5 names the endpoint per kind and the three response shapes are the ones the Phase 4 adapters already parse.

Pure functions only — no HTTP client, no database, no clock. The worker in Task 12 supplies all three. Splitting them is what makes the three response shapes testable from a byte slice.

Per spec §5: `/v1/models` for `openaicompat`; the models endpoint for `anthropic` and `gemini`; **Vertex is skipped** because it offers no practical API for listing what a project may call, and Bedrock needs two control-plane calls that arrive with its adapter in Phase 8.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/probe_test.go`:

```go
package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/darkraise/darkrouter/internal/provider"
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
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestProbe|TestBuildList|TestParse' -v
```

Expected: FAIL to build — `undefined: ProbeFor`, `undefined: BuildListRequest`, `undefined: ParseList`.

- [ ] **Step 3: Write the probes**

Create `internal/catalog/probe.go`:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestProbe|TestBuildList|TestParse' -race -count=1 -v
```

Expected: PASS, twelve tests.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/probe.go internal/catalog/probe_test.go
git commit -m "feat(catalog): build and parse listing probes"
```

---

### Task 11: The model lifecycle

**Files:**
- Create: `internal/store/catalog_lifecycle.go`
- Test: `internal/store/catalog_lifecycle_test.go`

**Interfaces:**
- Consumes: migration 0002 (Task 3), `store.ModelCapabilities` (Task 4).
- Produces: `store.DiscoveredModel`, `store.DiscoveryState`, `store.FailuresBeforeStale`, `store.OmissionsBeforeRemoved`, and the methods `(*DB).DiscoveryStates(ctx)`, `(*DB).RecordDiscoverySuccess(ctx, providerID, []DiscoveredModel, time.Time)`, `(*DB).RecordDiscoveryFailure(ctx, providerID, time.Time, string)`. Task 12's worker is the only caller.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 2: spec §5.1 states both counters, both thresholds, and the asymmetry between them.

Risk is 3 because getting this wrong loses catalogue data. Spec §5.1's asymmetry is the whole task:

- **A discovery failure never changes a model's state** until three consecutive failures, and then only to `stale` — still routable. A provider that times out once must not empty its half of the catalog and break every alias pointing at it.
- **A successful listing that omits a model is different evidence.** After three consecutive successful listings without it, the model becomes `removed_upstream`: not routable, still displayed with provenance.

Union-forever would leave retired models routable indefinitely, and because a 404 classifies as `RetryableModel` and never penalizes the provider, nothing would ever stop the wasted attempt on every request. Replace-on-success would break aliases on one flaky listing. Three successful confirmations is the middle.

- [ ] **Step 1: Write the failing test**

Create `internal/store/catalog_lifecycle_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"
)

func lifecycleDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := migrated(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func modelState(t *testing.T, db *DB, id string) (string, int) {
	t.Helper()
	var state string
	var streak int
	if err := db.Read.QueryRowContext(context.Background(),
		`SELECT state, missing_streak FROM models WHERE provider_id = 'p' AND model_id = ?`, id).
		Scan(&state, &streak); err != nil {
		t.Fatalf("model %q: %v", id, err)
	}
	return state, streak
}

var t0 = time.Unix(1_700_000_000, 0).UTC()

func TestSuccessInsertsNewModels(t *testing.T) {
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{
		{ModelID: "a", ContextWindow: 8192, MaxOutputTokens: 4096},
		{ModelID: "b"},
	}, t0); err != nil {
		t.Fatal(err)
	}
	if state, streak := modelState(t, db, "a"); state != "live" || streak != 0 {
		t.Errorf("a = (%q, %d)", state, streak)
	}
	rows, _ := db.Models(ctx)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, r := range rows {
		if r.ModelID == "a" && (r.ContextWindow != 8192 || r.MaxOutputTokens != 4096) {
			t.Errorf("a limits = (%d, %d)", r.ContextWindow, r.MaxOutputTokens)
		}
		if r.DiscoveredAt.IsZero() {
			t.Errorf("%s: discovered_at not stamped", r.ModelID)
		}
		if !r.LastSeenAt.Equal(t0) {
			t.Errorf("%s: last seen = %v, want %v", r.ModelID, r.LastSeenAt, t0)
		}
	}
}

func TestThreeFailuresMarkStaleAndNeverRemove(t *testing.T) {
	// The case this whole asymmetry exists for: a provider that times out must
	// not empty its catalog. Stale is still routable.
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, t0); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 2; i++ {
		if err := db.RecordDiscoveryFailure(ctx, "p", t0, "dial timeout"); err != nil {
			t.Fatal(err)
		}
		if state, _ := modelState(t, db, "a"); state != "live" {
			t.Fatalf("after %d failures state = %q, want live", i, state)
		}
	}
	if err := db.RecordDiscoveryFailure(ctx, "p", t0, "dial timeout"); err != nil {
		t.Fatal(err)
	}
	state, streak := modelState(t, db, "a")
	if state != "stale" {
		t.Errorf("after 3 failures state = %q, want stale", state)
	}
	// A failure is not evidence of removal, so the omission counter must not
	// have moved.
	if streak != 0 {
		t.Errorf("missing_streak = %d after failures only, want 0", streak)
	}

	// A fourth failure must not escalate stale to removed.
	if err := db.RecordDiscoveryFailure(ctx, "p", t0, "dial timeout"); err != nil {
		t.Fatal(err)
	}
	if state, _ := modelState(t, db, "a"); state != "stale" {
		t.Errorf("after 4 failures state = %q, want stale", state)
	}
}

func TestThreeOmissionsRemoveButOneDoesNot(t *testing.T) {
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}, t0); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, t0); err != nil {
			t.Fatal(err)
		}
		state, streak := modelState(t, db, "b")
		if state != "live" || streak != i {
			t.Fatalf("after %d omissions b = (%q, %d)", i, state, streak)
		}
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, t0); err != nil {
		t.Fatal(err)
	}
	if state, streak := modelState(t, db, "b"); state != "removed_upstream" || streak != 3 {
		t.Errorf("after 3 omissions b = (%q, %d)", state, streak)
	}
	if state, _ := modelState(t, db, "a"); state != "live" {
		t.Errorf("a = %q; a listed model was affected by its neighbour", state)
	}
}

func TestReappearanceClearsBothCounters(t *testing.T) {
	// Spec §5.1: recovery clears both. A model that comes back must route
	// again, and a provider that recovers must not carry its failure count
	// into the next outage.
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}, t0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, t0); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordDiscoveryFailure(ctx, "p", t0, "boom"); err != nil {
		t.Fatal(err)
	}
	if state, _ := modelState(t, db, "b"); state != "removed_upstream" {
		t.Fatalf("setup failed: b = %q", state)
	}

	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if state, streak := modelState(t, db, "b"); state != "live" || streak != 0 {
		t.Errorf("after reappearance b = (%q, %d), want (live, 0)", state, streak)
	}
	states, err := db.DiscoveryStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if states["p"].ConsecutiveFailures != 0 {
		t.Errorf("consecutive failures = %d after a success, want 0", states["p"].ConsecutiveFailures)
	}
	if !states["p"].LastSuccessAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("last success = %v", states["p"].LastSuccessAt)
	}
}

func TestSuccessRestoresStaleModels(t *testing.T) {
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, t0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := db.RecordDiscoveryFailure(ctx, "p", t0, "boom"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, t0); err != nil {
		t.Fatal(err)
	}
	if state, _ := modelState(t, db, "a"); state != "live" {
		t.Errorf("state = %q after recovery, want live", state)
	}
}

func TestFailureCountIsPerProvider(t *testing.T) {
	db, ctx := lifecycleDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('q', 'openaicompat', 'http://y', 0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "q", []DiscoveredModel{{ModelID: "z"}}, t0); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, t0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := db.RecordDiscoveryFailure(ctx, "p", t0, "boom"); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	if err := db.Read.QueryRowContext(ctx,
		`SELECT state FROM models WHERE provider_id = 'q'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "live" {
		t.Errorf("provider q went %q because provider p failed", state)
	}
}

func TestDiscoveredCapabilitiesAreRecordedAsDiscovered(t *testing.T) {
	// Spec §6: a runtime that reports its own capabilities produces a fact,
	// not an inference, and the router filters on it.
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{
		{ModelID: "a", Capabilities: &ModelCapabilities{Tools: true}},
		{ModelID: "b"},
	}, t0); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Models(ctx)
	for _, r := range rows {
		switch r.ModelID {
		case "a":
			if r.CapabilitiesSource != "discovered" || !r.Capabilities.Tools {
				t.Errorf("a = (%q, %+v)", r.CapabilitiesSource, r.Capabilities)
			}
		case "b":
			// A probe that reported nothing must not overwrite whatever the
			// sync knows with a confident-looking false.
			if r.CapabilitiesSource != "inferred" {
				t.Errorf("b source = %q, want inferred", r.CapabilitiesSource)
			}
		}
	}
}

func TestSuccessDoesNotClobberSyncedMetadata(t *testing.T) {
	// A probe that reports a bare id must leave models.dev's prices and limits
	// alone. Overwriting them with zeroes on every tick is a silent loss that
	// only shows up as an empty price column.
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, t0); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "a", ContextWindow: 200_000,
		InputMicrosPerMTok: 5_000_000, CapabilitiesSource: "models_dev",
		Capabilities: ModelCapabilities{Tools: true},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Models(ctx)
	r := rows[0]
	if r.ContextWindow != 200_000 || r.InputMicrosPerMTok != 5_000_000 {
		t.Errorf("discovery clobbered synced metadata: %+v", r)
	}
	if r.CapabilitiesSource != "models_dev" || !r.Capabilities.Tools {
		t.Errorf("discovery clobbered synced capabilities: (%q, %+v)", r.CapabilitiesSource, r.Capabilities)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run 'TestSuccess|TestThree|TestReappearance|TestFailureCount|TestDiscoveredCapabilities' -v
```

Expected: FAIL to build — `undefined: DiscoveredModel`, `db.RecordDiscoverySuccess undefined`.

- [ ] **Step 3: Write the lifecycle**

Create `internal/store/catalog_lifecycle.go`:

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// The two thresholds from spec §5.1. Three successful confirmations is the
// middle between union-forever, which leaves retired models routable and
// wastes an attempt on every request, and replace-on-success, which breaks
// every alias on one flaky listing.
const (
	FailuresBeforeStale    = 3
	OmissionsBeforeRemoved = 3
)

// DiscoveredModel is one model a probe reported. Capabilities is nil unless a
// capability probe actually read them from the runtime; a nil is "the probe did
// not say", which must not overwrite what the sync knows.
type DiscoveredModel struct {
	ModelID         string
	ContextWindow   int
	MaxOutputTokens int
	Capabilities    *ModelCapabilities
}

// DiscoveryState is one provider's probe bookkeeping.
type DiscoveryState struct {
	ProviderID          string
	ConsecutiveFailures int
	LastAttemptAt       time.Time
	LastSuccessAt       time.Time
	LastError           string
}

// DiscoveryStates returns every provider's bookkeeping, keyed by provider id.
func (d *DB) DiscoveryStates(ctx context.Context) (map[string]DiscoveryState, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT provider_id, consecutive_failures, last_attempt_at, last_success_at, last_error
		   FROM provider_discovery`)
	if err != nil {
		return nil, fmt.Errorf("read discovery state: %w", err)
	}
	defer rows.Close()

	out := map[string]DiscoveryState{}
	for rows.Next() {
		var (
			s                DiscoveryState
			attempt, success sql.NullInt64
		)
		if err := rows.Scan(&s.ProviderID, &s.ConsecutiveFailures, &attempt, &success, &s.LastError); err != nil {
			return nil, fmt.Errorf("scan discovery state: %w", err)
		}
		if attempt.Valid {
			s.LastAttemptAt = time.UnixMilli(attempt.Int64).UTC()
		}
		if success.Valid {
			s.LastSuccessAt = time.UnixMilli(success.Int64).UTC()
		}
		out[s.ProviderID] = s
	}
	return out, rows.Err()
}

// RecordDiscoverySuccess applies one successful listing.
//
// Everything listed becomes live with its counters cleared, including a model
// previously retired — spec §5.1's "recovery clears both". Everything the
// provider still has a row for and this listing omitted has its omission
// counter advanced, and crosses to removed_upstream on the third.
//
// The whole update is one transaction: a crash between the upserts and the
// omission sweep would otherwise leave models both listed and counted absent.
func (d *DB) RecordDiscoverySuccess(ctx context.Context, providerID string,
	seen []DiscoveredModel, at time.Time) error {

	ms := at.UTC().UnixMilli()
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin discovery write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The upsert names only the columns discovery owns. context_window and
	// max_output_tokens are written on insert and refreshed only when the
	// probe actually carried them, so a probe reporting a bare id cannot
	// overwrite models.dev's numbers with zeroes.
	up, err := tx.PrepareContext(ctx,
		`INSERT INTO models (provider_id, model_id, state, surfaces, missing_streak,
		                     last_seen_at, discovered_at, context_window, max_output_tokens)
		 VALUES (?, ?, 'live', '["llm"]', 0, ?, ?, ?, ?)
		 ON CONFLICT(provider_id, model_id) DO UPDATE SET
		     state             = 'live',
		     missing_streak    = 0,
		     last_seen_at      = excluded.last_seen_at,
		     discovered_at     = coalesce(models.discovered_at, excluded.discovered_at),
		     context_window    = coalesce(excluded.context_window, models.context_window),
		     max_output_tokens = coalesce(excluded.max_output_tokens, models.max_output_tokens)`)
	if err != nil {
		return fmt.Errorf("prepare model upsert: %w", err)
	}
	defer up.Close()

	caps, err := tx.PrepareContext(ctx,
		`UPDATE models SET capabilities = ?, capabilities_source = 'discovered'
		  WHERE provider_id = ? AND model_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare capability write: %w", err)
	}
	defer caps.Close()

	ids := make([]any, 0, len(seen))
	for _, m := range seen {
		if m.ModelID == "" {
			continue
		}
		if _, err := up.ExecContext(ctx, providerID, m.ModelID, ms, ms,
			nullableInt(m.ContextWindow), nullableInt(m.MaxOutputTokens)); err != nil {
			return fmt.Errorf("upsert model %q: %w", m.ModelID, err)
		}
		if m.Capabilities != nil {
			blob, err := json.Marshal(*m.Capabilities)
			if err != nil {
				return err
			}
			if _, err := caps.ExecContext(ctx, string(blob), providerID, m.ModelID); err != nil {
				return fmt.Errorf("write capabilities for %q: %w", m.ModelID, err)
			}
		}
		ids = append(ids, m.ModelID)
	}

	// The omission sweep. A model this listing did not name has its counter
	// advanced; the third omission retires it. Building the NOT IN list from
	// placeholders rather than string concatenation keeps a model id that
	// contains a quote from becoming SQL.
	args := append([]any{providerID}, ids...)
	sweep := `UPDATE models
	             SET missing_streak = missing_streak + 1,
	                 state = CASE WHEN missing_streak + 1 >= ` + itoa(OmissionsBeforeRemoved) + `
	                              THEN 'removed_upstream' ELSE state END
	           WHERE provider_id = ?`
	if len(ids) > 0 {
		sweep += ` AND model_id NOT IN (` + placeholders(len(ids)) + `)`
	}
	if _, err := tx.ExecContext(ctx, sweep, args...); err != nil {
		return fmt.Errorf("sweep omitted models: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO provider_discovery (provider_id, consecutive_failures, last_attempt_at, last_success_at, last_error)
		 VALUES (?, 0, ?, ?, '')
		 ON CONFLICT(provider_id) DO UPDATE SET
		     consecutive_failures = 0,
		     last_attempt_at      = excluded.last_attempt_at,
		     last_success_at      = excluded.last_success_at,
		     last_error           = ''`,
		providerID, ms, ms); err != nil {
		return fmt.Errorf("record discovery success: %w", err)
	}
	return tx.Commit()
}

// RecordDiscoveryFailure applies one failed probe.
//
// It never touches missing_streak and never retires anything. A provider that
// times out must not empty its half of the catalog: after three consecutive
// failures its live models go stale, which is a display state rather than a
// routing one. The breaker, not the catalog, is what avoids a provider that is
// actually broken.
func (d *DB) RecordDiscoveryFailure(ctx context.Context, providerID string,
	at time.Time, cause string) error {

	ms := at.UTC().UnixMilli()
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin discovery write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO provider_discovery (provider_id, consecutive_failures, last_attempt_at, last_error)
		 VALUES (?, 1, ?, ?)
		 ON CONFLICT(provider_id) DO UPDATE SET
		     consecutive_failures = provider_discovery.consecutive_failures + 1,
		     last_attempt_at      = excluded.last_attempt_at,
		     last_error           = excluded.last_error`,
		providerID, ms, cause); err != nil {
		return fmt.Errorf("record discovery failure: %w", err)
	}

	var failures int
	if err := tx.QueryRowContext(ctx,
		`SELECT consecutive_failures FROM provider_discovery WHERE provider_id = ?`,
		providerID).Scan(&failures); err != nil {
		return fmt.Errorf("read failure count: %w", err)
	}
	if failures >= FailuresBeforeStale {
		// Only live rows move. A removed_upstream model stays removed: a
		// failed probe is not evidence it came back.
		if _, err := tx.ExecContext(ctx,
			`UPDATE models SET state = 'stale' WHERE provider_id = ? AND state = 'live'`,
			providerID); err != nil {
			return fmt.Errorf("mark models stale: %w", err)
		}
	}
	return tx.Commit()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, 0, n*3)
	for i := 0; i < n; i++ {
		if i > 0 {
			buf = append(buf, ',', ' ')
		}
		buf = append(buf, '?')
	}
	return string(buf)
}

// itoa keeps the threshold constant readable inside the SQL rather than
// hard-coding 3 in two places that could drift apart.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run 'TestSuccess|TestThree|TestReappearance|TestFailureCount|TestDiscoveredCapabilities' -race -count=1 -v
```

Expected: PASS, eight tests.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/store/catalog_lifecycle.go internal/store/catalog_lifecycle_test.go
git commit -m "feat(store): apply the model lifecycle rules"
```

---

### Task 12: The discovery worker

**Files:**
- Create: `internal/catalog/discovery.go`
- Test: `internal/catalog/discovery_test.go`

**Interfaces:**
- Consumes: `catalog.ProbeFor` / `BuildListRequest` / `ParseList` (Task 10), `store.RecordDiscoverySuccess` / `RecordDiscoveryFailure` (Task 11), `catalog.Store.Rebuild` (Task 9), `health.Breaker` through a narrow interface.
- Produces: `catalog.Discoverer`, `catalog.DiscoveryOptions`, `catalog.Health` (the narrow breaker interface), `NewDiscoverer(...)`, `(*Discoverer).Run(context.Context) error`, `(*Discoverer).Trigger(providerID string)`, `(*Discoverer).SweepOnce(context.Context)`. Task 15 wires `Run` into `server.Run`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 2: spec §5 fixes the interval, the global cap, the credential rule and the on-demand trigger; `internal/store/rollup.go` is the existing ticker-worker shape.

Risk is 3 because this is a concurrent worker writing shared state. Three rules from spec §5 that are easy to get subtly wrong:

- **The concurrency cap is global across the discovery fleet, not per provider.** A per-provider cap would not stop forty providers opening forty simultaneous connections on boot, which was the stated goal. Findings ledger X14 records the correction.
- **Discovery uses the least-recently-used non-cooling credential**, and **a 401 on a probe cools that credential exactly as a request would** — it is the same evidence.
- **Discovery also runs on demand** when a provider is created or its credential changes, so the UI shows models immediately rather than after the next tick.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/discovery_test.go`:

```go
package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// fakeHealth records what discovery told the breaker, without a real one.
type fakeHealth struct {
	mu       sync.Mutex
	signals  []health.Signal
	keys     []health.Key
	cooling  map[health.CredKey]bool
	lastUsed map[health.CredKey]time.Time
}

func (f *fakeHealth) Record(k health.Key, s health.Signal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, k)
	f.signals = append(f.signals, s)
}

func (f *fakeHealth) Available(k health.Key) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.cooling[health.CredKey{ProviderID: k.ProviderID, KeyID: k.KeyID}]
}

func (f *fakeHealth) LastUsedSnapshot() map[health.CredKey]time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[health.CredKey]time.Time, len(f.lastUsed))
	for k, v := range f.lastUsed {
		out[k] = v
	}
	return out
}

func (f *fakeHealth) MarkUsed(ck health.CredKey, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastUsed == nil {
		f.lastUsed = map[health.CredKey]time.Time{}
	}
	f.lastUsed[ck] = at
}

// staticSource serves a fixed provider set.
type staticSource struct{ ps []provider.Provider }

func (s *staticSource) Providers(context.Context) ([]provider.Provider, error) { return s.ps, nil }
func (s *staticSource) Revision() uint64                                       { return 1 }

func discoveryDB(t *testing.T, ids ...string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(t.TempDir() + "/d.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO providers (id, kind, base_url, created_at) VALUES (?, 'openaicompat', 'http://x', 0)`,
			id); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestSweepRecordsWhatItListed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("probed %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-1" {
			t.Errorf("auth = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}]}`))
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{{ID: "k1", Secret: "sk-1", Enabled: true}},
	}}}
	d := NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{})
	d.SweepOnce(context.Background())

	rows, err := db.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d models, want 2", len(rows))
	}
	if rows[0].State != "live" {
		t.Errorf("state = %q", rows[0].State)
	}
}

func TestSweepCoolsTheCredentialOnA401(t *testing.T) {
	// A 401 on a probe is the same evidence as a 401 on a request, so it must
	// cool the credential rather than being logged and forgotten.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{{ID: "k1", Secret: "sk", Enabled: true}},
	}}}
	h := &fakeHealth{}
	NewDiscoverer(db, src, NewStore(db, src), h, DiscoveryOptions{}).SweepOnce(context.Background())

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.signals) != 1 {
		t.Fatalf("recorded %d signals, want 1", len(h.signals))
	}
	if h.signals[0].Outcome != adapter.OutcomeRetryableCredential || h.signals[0].StatusCode != 401 {
		t.Errorf("signal = %+v", h.signals[0])
	}
	if h.keys[0].ProviderID != "p" || h.keys[0].KeyID != "k1" {
		t.Errorf("key = %+v", h.keys[0])
	}
	// A failed probe must still be recorded as a failure, not silently
	// dropped, or the three-strike ladder never advances.
	states, _ := db.DiscoveryStates(context.Background())
	if states["p"].ConsecutiveFailures != 1 {
		t.Errorf("failures = %d, want 1", states["p"].ConsecutiveFailures)
	}
}

func TestSweepSkipsCoolingCredentialsAndPicksLRU(t *testing.T) {
	var seen atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{
			{ID: "hot", Secret: "sk-hot", Enabled: true},
			{ID: "cold", Secret: "sk-cold", Enabled: true},
			{ID: "cooling", Secret: "sk-cooling", Enabled: true},
		},
	}}}
	h := &fakeHealth{
		cooling:  map[health.CredKey]bool{{ProviderID: "p", KeyID: "cooling"}: true},
		lastUsed: map[health.CredKey]time.Time{{ProviderID: "p", KeyID: "hot"}: time.Now()},
	}
	NewDiscoverer(db, src, NewStore(db, src), h, DiscoveryOptions{}).SweepOnce(context.Background())

	// "cold" has never been used, so it sorts before "hot"; "cooling" is
	// excluded outright.
	if got := seen.Load(); got != "Bearer sk-cold" {
		t.Errorf("probed with %v, want the least-recently-used credential", got)
	}
}

func TestSweepSkipsProvidersWithNoUsableCredential(t *testing.T) {
	var probed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probed.Store(true)
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{{ID: "k", Secret: "sk", Enabled: true}},
	}}}
	h := &fakeHealth{cooling: map[health.CredKey]bool{{ProviderID: "p", KeyID: "k"}: true}}
	NewDiscoverer(db, src, NewStore(db, src), h, DiscoveryOptions{}).SweepOnce(context.Background())

	if probed.Load() {
		t.Error("probed with a cooling credential")
	}
	// Every credential cooling is not a discovery failure. Counting it would
	// walk the provider to stale for a reason that has nothing to do with its
	// listing endpoint.
	states, _ := db.DiscoveryStates(context.Background())
	if states["p"].ConsecutiveFailures != 0 {
		t.Errorf("failures = %d, want 0", states["p"].ConsecutiveFailures)
	}
}

func TestGlobalConcurrencyCapHoldsOnAColdStart(t *testing.T) {
	// Spec §5: the cap is global across the fleet. A per-provider cap would
	// not stop forty providers opening forty connections at once, which is the
	// case this asserts.
	var live, peak atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := live.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		live.Add(-1)
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer srv.Close()

	ids := make([]string, 40)
	ps := make([]provider.Provider, 40)
	for i := range ids {
		ids[i] = "p" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		ps[i] = provider.Provider{
			ID: ids[i], Kind: "openaicompat", BaseURL: srv.URL + "/v1",
			Credentials: []provider.Credential{{ID: "k", Secret: "sk", Enabled: true}},
		}
	}
	db := discoveryDB(t, ids...)
	src := &staticSource{ps: ps}
	d := NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{Concurrency: 4})
	d.SweepOnce(context.Background())

	if got := peak.Load(); got > 4 {
		t.Errorf("peak concurrency = %d, want at most 4", got)
	}
	rows, _ := db.Models(context.Background())
	if len(rows) != 40 {
		t.Errorf("catalogued %d models, want 40 — the cap dropped work", len(rows))
	}
}

func TestSweepSkipsUndiscoverableKindsWithoutFailing(t *testing.T) {
	// Vertex has no listing API. Probing it every tick would walk it to stale
	// and, with a real breaker, cool a credential for a call that was never
	// going to work.
	db := discoveryDB(t, "v")
	src := &staticSource{ps: []provider.Provider{{
		ID: "v", Kind: "vertex", BaseURL: "https://example.invalid",
		Credentials: []provider.Credential{{ID: "k", Secret: "sk", Enabled: true}},
	}}}
	h := &fakeHealth{}
	NewDiscoverer(db, src, NewStore(db, src), h, DiscoveryOptions{}).SweepOnce(context.Background())

	states, _ := db.DiscoveryStates(context.Background())
	if states["v"].ConsecutiveFailures != 0 {
		t.Errorf("failures = %d for an undiscoverable kind, want 0", states["v"].ConsecutiveFailures)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.signals) != 0 {
		t.Errorf("recorded %d health signals for a kind it never probed", len(h.signals))
	}
}

func TestSweepRebuildsTheSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{{ID: "k", Secret: "sk", Enabled: true}},
	}}}
	cat := NewStore(db, src)
	NewDiscoverer(db, src, cat, &fakeHealth{}, DiscoveryOptions{}).SweepOnce(context.Background())

	// Without the rebuild the router keeps serving the previous snapshot and
	// the newly discovered model is invisible until something else swaps it.
	if _, ok := cat.Snapshot().Lookup("p", "m1"); !ok {
		t.Error("the snapshot was not rebuilt after discovery")
	}
}

func TestTriggerProbesOneProviderPromptly(t *testing.T) {
	// Spec §5: on-demand discovery so the UI shows models immediately rather
	// than after the next fifteen-minute tick.
	done := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{{ID: "k", Secret: "sk", Enabled: true}},
	}}}
	// A long interval, so anything that arrives came from the trigger.
	d := NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{Interval: time.Hour})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	d.Trigger("p")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the trigger did not produce a probe")
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Run returned %v", err)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	db := discoveryDB(t)
	src := &staticSource{}
	d := NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestSweep|TestGlobal|TestTrigger|TestRunStops' -race -v
```

Expected: FAIL to build — `undefined: NewDiscoverer`, `undefined: DiscoveryOptions`.

- [ ] **Step 3: Write the worker**

Create `internal/catalog/discovery.go`:

```go
package catalog

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// Health is the slice of the breaker discovery needs. Narrow rather than the
// whole Breaker so a test can supply four methods instead of a live one.
type Health interface {
	Record(k health.Key, s health.Signal)
	Available(k health.Key) bool
	LastUsedSnapshot() map[health.CredKey]time.Time
	MarkUsed(ck health.CredKey, at time.Time)
}

// DiscoveryOptions configures the worker. The zero value is the shipped
// default.
type DiscoveryOptions struct {
	// Interval between sweeps. Spec §5 fixes fifteen minutes.
	Interval time.Duration
	// Concurrency is the cap across the whole fleet, not per provider. A
	// per-provider cap would not stop forty providers opening forty
	// simultaneous connections on boot, which was the stated goal.
	Concurrency int
	// Timeout bounds one probe.
	Timeout time.Duration
}

func (o DiscoveryOptions) withDefaults() DiscoveryOptions {
	if o.Interval <= 0 {
		o.Interval = 15 * time.Minute
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 8
	}
	if o.Timeout <= 0 {
		o.Timeout = 15 * time.Second
	}
	return o
}

// Discoverer probes each enabled provider's listing endpoint.
type Discoverer struct {
	db     *store.DB
	src    provider.Source
	cat    *Store
	health Health
	opts   DiscoveryOptions

	client *http.Client
	// sem is the global cap. One buffered channel shared by every probe is
	// what makes the cap fleet-wide rather than per provider.
	sem     chan struct{}
	trigger chan string
}

func NewDiscoverer(db *store.DB, src provider.Source, cat *Store,
	h Health, opts DiscoveryOptions) *Discoverer {

	opts = opts.withDefaults()
	return &Discoverer{
		db: db, src: src, cat: cat, health: h, opts: opts,
		client: &http.Client{
			Timeout: opts.Timeout,
			// A listing endpoint that redirects is misconfigured; following it
			// would send the credential to whatever host it names.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		sem: make(chan struct{}, opts.Concurrency),
		// Buffered so a caller creating a provider never blocks on the worker,
		// and a full buffer drops the request rather than stalling the UI — the
		// next tick covers it either way.
		trigger: make(chan string, 32),
	}
}

// Trigger asks for one provider to be probed now. It never blocks: a dropped
// trigger costs at most one interval, and blocking the admin handler that
// created a provider would cost more.
func (d *Discoverer) Trigger(providerID string) {
	select {
	case d.trigger <- providerID:
	default:
	}
}

// Run sweeps on an interval until ctx is cancelled.
func (d *Discoverer) Run(ctx context.Context) error {
	// An immediate first sweep, so a fresh install shows models without
	// waiting a quarter of an hour.
	d.SweepOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case id := <-d.trigger:
			d.probeProvider(ctx, id)
		case <-time.After(jitter(d.opts.Interval)):
			d.SweepOnce(ctx)
		}
	}
}

// SweepOnce probes every enabled provider, bounded by the global cap.
func (d *Discoverer) SweepOnce(ctx context.Context) {
	ps, err := d.src.Providers(ctx)
	if err != nil {
		log.Printf("discovery: providers: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, p := range ps {
		wg.Add(1)
		go func(p provider.Provider) {
			defer wg.Done()
			select {
			case d.sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-d.sem }()
			d.probe(ctx, p)
		}(p)
	}
	wg.Wait()
	d.rebuild(ctx)
}

// probeProvider is the on-demand path: one named provider, then a rebuild.
func (d *Discoverer) probeProvider(ctx context.Context, providerID string) {
	ps, err := d.src.Providers(ctx)
	if err != nil {
		log.Printf("discovery: providers: %v", err)
		return
	}
	for _, p := range ps {
		if p.ID != providerID {
			continue
		}
		select {
		case d.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		d.probe(ctx, p)
		<-d.sem
		d.rebuild(ctx)
		return
	}
}

func (d *Discoverer) rebuild(ctx context.Context) {
	if err := d.cat.Rebuild(ctx); err != nil {
		log.Printf("discovery: rebuild: %v", err)
	}
}

// probe runs one provider's listing and applies the result.
func (d *Discoverer) probe(ctx context.Context, p provider.Provider) {
	preset := Embedded()[p.Preset]

	cred, ok := d.pickCredential(p)
	if !ok {
		// Every credential cooling is not a discovery failure. Recording one
		// would walk the provider to stale for a reason that has nothing to do
		// with its listing endpoint.
		return
	}

	pr, err := ProbeFor(p, preset, cred.Secret)
	if err != nil {
		// An undiscoverable kind is a permanent, known fact rather than a
		// failure. Counting it would retire Vertex's catalogue on the third
		// tick and cool its credential for a call it never made.
		return
	}

	now := time.Now().UTC()
	d.health.MarkUsed(health.CredKey{ProviderID: p.ID, KeyID: cred.ID}, now)

	models, err := d.list(ctx, pr, p.ID, cred.ID)
	if err != nil {
		if ctx.Err() != nil {
			// Shutdown is not a provider failure.
			return
		}
		if rerr := d.db.RecordDiscoveryFailure(context.WithoutCancel(ctx), p.ID, now, err.Error()); rerr != nil {
			log.Printf("discovery: %s: record failure: %v", p.ID, rerr)
		}
		return
	}

	seen := make([]store.DiscoveredModel, 0, len(models))
	for _, m := range models {
		seen = append(seen, store.DiscoveredModel{
			ModelID:         m.ModelID,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
		})
	}
	if err := d.db.RecordDiscoverySuccess(context.WithoutCancel(ctx), p.ID, seen, now); err != nil {
		log.Printf("discovery: %s: record success: %v", p.ID, err)
	}
}

// list performs the request and classifies the response.
func (d *Discoverer) list(ctx context.Context, pr Probe, providerID, keyID string) ([]Discovered, error) {
	req, err := BuildListRequest(ctx, pr)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// A rejected key on a probe is the same evidence as a rejected key on
		// a request, so it cools the credential across every model it serves.
		d.health.Record(
			health.Key{ProviderID: providerID, KeyID: keyID},
			health.Signal{Outcome: adapter.OutcomeRetryableCredential, StatusCode: resp.StatusCode},
		)
		return nil, fmt.Errorf("listing rejected the credential: %s", resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("listing returned %s", resp.Status)
	}

	// Bounded: a listing endpoint that streams unbounded data must not be able
	// to exhaust memory on a background worker nobody is watching.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return ParseList(pr.Kind, body)
}

// pickCredential returns the least-recently-used credential that is not
// cooling. Least-recently-used is what spreads probes across quotas instead of
// spending the first key's budget on listing.
func (d *Discoverer) pickCredential(p provider.Provider) (provider.Credential, bool) {
	lastUsed := d.health.LastUsedSnapshot()

	usable := make([]provider.Credential, 0, len(p.Credentials))
	for _, c := range p.Credentials {
		if !c.Enabled {
			continue
		}
		if !d.health.Available(health.Key{ProviderID: p.ID, KeyID: c.ID}) {
			continue
		}
		usable = append(usable, c)
	}
	if len(usable) == 0 {
		return provider.Credential{}, false
	}
	sort.SliceStable(usable, func(i, j int) bool {
		ti := lastUsed[health.CredKey{ProviderID: p.ID, KeyID: usable[i].ID}]
		tj := lastUsed[health.CredKey{ProviderID: p.ID, KeyID: usable[j].ID}]
		if ti.Equal(tj) {
			// A total order, so two never-used credentials do not swap on
			// every sweep and produce a different probe each time.
			return usable[i].ID < usable[j].ID
		}
		return ti.Before(tj)
	})
	return usable[0], true
}

// jitter spreads sweeps so a fleet restarted together does not resynchronize
// onto the same instant every interval.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d/2 + time.Duration(rand.Int63n(int64(d)))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestSweep|TestGlobal|TestTrigger|TestRunStops' -race -count=1 -v
```

Expected: PASS, nine tests.

- [ ] **Step 5: Run the concurrency tests repeatedly**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestGlobalConcurrencyCap|TestTrigger' -race -count=5
```

Expected: `ok`, no `DATA RACE` reports and no flakes. A flake here means the cap or the trigger has a real ordering bug; do not paper over it with a longer sleep.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog/discovery.go internal/catalog/discovery_test.go
git commit -m "feat(catalog): probe providers on a jittered sweep"
```

---

### Task 13: Discovered capabilities from a local runtime

**Files:**
- Create: `internal/catalog/capability.go`
- Modify: `internal/catalog/discovery.go` (call the probe when the preset declares one)
- Test: `internal/catalog/capability_test.go`

**Interfaces:**
- Consumes: `catalog.Probe` (Task 10), `store.DiscoveredModel.Capabilities` (Task 11), the discovery worker (Task 12).
- Produces: `catalog.BuildCapabilityRequest(context.Context, Probe, string) (*http.Request, error)`, `catalog.ParseOllamaShow([]byte) (store.ModelCapabilities, bool)`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: spec §6 names `/api/show` and the preset field carrying it already exists from Task 1.

This is what keeps the local-model story honest. Ollama reports whether a model's template advertises tools, so the common local case is read from the runtime rather than guessed. Without it every discovered Ollama model is `inferred`, and while an inferred model still routes, its capabilities never become facts the router can actually filter on.

Two response shapes, because Ollama changed: newer builds return a `capabilities` array, older ones only a `template` whose text mentions `.Tools`. Supporting both is one extra branch and avoids the probe silently reporting nothing on an older install.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/capability_test.go`:

```go
package catalog

import (
	"context"
	"encoding/json"
	"io"
	"testing"
)

func TestBuildCapabilityRequestTargetsApiShow(t *testing.T) {
	// /api/show sits beside the OpenAI-compatible /v1, not under it.
	r, err := BuildCapabilityRequest(context.Background(),
		Probe{Kind: "openaicompat", BaseURL: "http://localhost:11434/v1", AuthStyle: "none"}, "llama3.3:70b")
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.String() != "http://localhost:11434/api/show" {
		t.Errorf("url = %s", r.URL)
	}
	if r.Method != "POST" {
		t.Errorf("method = %s, want POST", r.Method)
	}
	body, _ := io.ReadAll(r.Body)
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	// The tag must survive verbatim: normalizing it here would ask Ollama
	// about a model it does not have.
	if payload["model"] != "llama3.3:70b" {
		t.Errorf("payload = %v", payload)
	}
}

func TestParseOllamaShowReadsTheCapabilitiesArray(t *testing.T) {
	caps, ok := ParseOllamaShow([]byte(`{"capabilities":["completion","tools","vision"]}`))
	if !ok {
		t.Fatal("reported no capabilities")
	}
	if !caps.Tools || !caps.Vision {
		t.Errorf("caps = %+v", caps)
	}
	if caps.Reasoning {
		t.Error("reasoning invented from a runtime that did not report it")
	}
}

func TestParseOllamaShowReadsThinkingAsReasoning(t *testing.T) {
	caps, ok := ParseOllamaShow([]byte(`{"capabilities":["completion","thinking"]}`))
	if !ok || !caps.Reasoning {
		t.Errorf("caps = %+v, ok = %v", caps, ok)
	}
}

func TestParseOllamaShowFallsBackToTheTemplate(t *testing.T) {
	// Older builds report no capabilities array; the template is the only
	// evidence, and spec §6 describes exactly that signal.
	caps, ok := ParseOllamaShow([]byte(`{"template":"{{ if .Tools }}{{ range .Tools }}{{ end }}{{ end }}"}`))
	if !ok || !caps.Tools {
		t.Errorf("caps = %+v, ok = %v", caps, ok)
	}
	plain, ok := ParseOllamaShow([]byte(`{"template":"{{ .System }}{{ .Prompt }}"}`))
	if !ok {
		t.Fatal("a template with no tools reported nothing at all")
	}
	if plain.Tools {
		t.Error("tools claimed for a template that does not mention them")
	}
}

func TestParseOllamaShowReportsNothingWhenItKnowsNothing(t *testing.T) {
	// A response with neither signal must report "did not say" rather than a
	// confident all-false, which would overwrite what models.dev knows.
	for _, body := range []string{`{}`, `{"details":{}}`, "", "not json"} {
		if _, ok := ParseOllamaShow([]byte(body)); ok {
			t.Errorf("%q reported capabilities", body)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestBuildCapability|TestParseOllama' -v
```

Expected: FAIL to build — `undefined: BuildCapabilityRequest`, `undefined: ParseOllamaShow`.

- [ ] **Step 3: Write the capability probe**

Create `internal/catalog/capability.go`:

```go
package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/store"
)

// BuildCapabilityRequest asks a local runtime what one model can do.
//
// Ollama's /api/show sits beside its OpenAI-compatible surface rather than
// under it, so the /v1 suffix comes off. The model id is sent exactly as
// discovery reported it: normalizing the tag separator here would ask about a
// model the runtime does not have.
func BuildCapabilityRequest(ctx context.Context, p Probe, modelID string) (*http.Request, error) {
	root := strings.TrimSuffix(strings.TrimRight(p.BaseURL, "/"), "/v1")
	body, err := json.Marshal(map[string]string{"model": modelID})
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, root+"/api/show", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build capability request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	applyAuth(r, p)
	return r, nil
}

// ParseOllamaShow reads what the runtime reported.
//
// The bool is "the runtime said something", not "the model can do something".
// A response carrying neither signal must not become a confident all-false: it
// would outrank models.dev in the merge and mark a capable model incapable.
func ParseOllamaShow(body []byte) (store.ModelCapabilities, bool) {
	var doc struct {
		Capabilities []string `json:"capabilities"`
		Template     string   `json:"template"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return store.ModelCapabilities{}, false
	}

	if len(doc.Capabilities) > 0 {
		var caps store.ModelCapabilities
		for _, c := range doc.Capabilities {
			switch strings.ToLower(c) {
			case "tools":
				caps.Tools = true
			case "vision":
				caps.Vision = true
			case "thinking", "reasoning":
				caps.Reasoning = true
			}
		}
		return caps, true
	}

	// Older builds report no array. The template is the only evidence, and it
	// is exactly the signal spec §6 describes: a template that renders .Tools
	// is a template for a model that takes them.
	if doc.Template != "" {
		return store.ModelCapabilities{Tools: strings.Contains(doc.Template, ".Tools")}, true
	}
	return store.ModelCapabilities{}, false
}
```

- [ ] **Step 4: Call it from the worker**

In `internal/catalog/discovery.go`, inside `probe`, replace the loop that builds `seen` so it consults the runtime when the preset declares a probe:

```go
	seen := make([]store.DiscoveredModel, 0, len(models))
	for _, m := range models {
		dm := store.DiscoveredModel{
			ModelID:         m.ModelID,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
		}
		if preset.CapabilityProbe == "ollama" {
			if caps, ok := d.showCapabilities(ctx, pr, m.ModelID); ok {
				dm.Capabilities = &caps
			}
		}
		seen = append(seen, dm)
	}
```

And add the helper below `list`:

```go
// showCapabilities asks a local runtime about one model. A failure is silent:
// the listing already succeeded, and turning "this one model did not answer"
// into a provider-wide discovery failure would retire a working catalogue.
func (d *Discoverer) showCapabilities(ctx context.Context, pr Probe, modelID string) (store.ModelCapabilities, bool) {
	req, err := BuildCapabilityRequest(ctx, pr, modelID)
	if err != nil {
		return store.ModelCapabilities{}, false
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return store.ModelCapabilities{}, false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return store.ModelCapabilities{}, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return store.ModelCapabilities{}, false
	}
	return ParseOllamaShow(body)
}
```

- [ ] **Step 5: Write the worker-level test**

Add to `internal/catalog/discovery_test.go`. It needs a preset declaring `capability_probe`, which the shipped file has under `ollama` — so the provider row names that preset.

```go
func TestSweepReadsCapabilitiesFromTheRuntime(t *testing.T) {
	// Spec §6 and its done criterion: a local model's tool support is read
	// from the runtime rather than guessed, so it becomes a fact the router
	// can filter on.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"llama3.3:70b"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{"capabilities":["completion","tools"]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	db := discoveryDB(t, "local")
	src := &staticSource{ps: []provider.Provider{{
		ID: "local", Kind: "openaicompat", BaseURL: srv.URL + "/v1", Preset: "ollama",
		Credentials: []provider.Credential{{ID: "k", Secret: "", Enabled: true}},
	}}}
	NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{}).SweepOnce(context.Background())

	rows, err := db.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].CapabilitiesSource != "discovered" || !rows[0].Capabilities.Tools {
		t.Errorf("row = (%q, %+v)", rows[0].CapabilitiesSource, rows[0].Capabilities)
	}
}
```

- [ ] **Step 6: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestBuildCapability|TestParseOllama|TestSweepReadsCapabilities' -race -count=1 -v
```

Expected: PASS, six tests. If `TestSweepReadsCapabilitiesFromTheRuntime` fails with the probe never reaching `/api/show`, the shipped `presets.yaml` lost its `ollama` entry's `capability_probe` — fix it in `presets.overrides.yaml` and regenerate rather than hard-coding the preset in the test.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/catalog/capability.go internal/catalog/capability_test.go \
  internal/catalog/discovery.go internal/catalog/discovery_test.go
git commit -m "feat(catalog): read capabilities from local runtimes"
```

---

### Task 14: The models.dev sync worker

**Files:**
- Create: `internal/catalog/sync.go`
- Test: `internal/catalog/sync_test.go`

**Interfaces:**
- Consumes: `catalog.ParseModelsDev` / `FallbackDoc` (Task 5), `catalog.Join` (Task 6), `store.UpsertMetadata` / `Models` (Task 4), `catalog.Store.Rebuild` (Task 9).
- Produces: `catalog.Syncer`, `catalog.SyncOptions`, `NewSyncer(...)`, `(*Syncer).Run(context.Context) error`, `(*Syncer).SyncOnce(context.Context) error`, `(*Syncer).Doc() Doc`. Task 15 wires `Run` into `server.Run`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: spec §4 fixes the URL, the interval, the failure contract, and the field mapping is already implemented in Task 5.

The failure contract is the point. **A fetch error leaves the cache and logs a warning.** Darkrouter must start and serve with no access to models.dev at all, falling back to the embedded snapshot. A gateway that will not start because a metadata CDN is down is a worse gateway.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/sync_test.go`:

```go
package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

const syncDoc = `{"acme":{"id":"acme","models":{
  "big":{"id":"big","tool_call":true,"reasoning":true,
         "modalities":{"input":["text","image"],"output":["text"]},
         "limit":{"context":200000,"output":64000},
         "cost":{"input":5,"output":25}},
  "cheap":{"id":"cheap","tool_call":false,
           "modalities":{"input":["text"],"output":["text"]},
           "limit":{"context":8192,"output":4096},
           "cost":{"input":0.14,"output":0.28}}
}}}`

func syncFixture(t *testing.T) (*store.DB, *staticSource, *Store) {
	t.Helper()
	ctx := context.Background()
	db := discoveryDB(t, "p")
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE providers SET preset = 'acme' WHERE id = 'p'`); err != nil {
		t.Fatal(err)
	}
	// Discovery has already established what exists; the sync only enriches.
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]store.DiscoveredModel{{ModelID: "big"}, {ModelID: "cheap"}, {ModelID: "private"}},
		time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	src := &staticSource{ps: []provider.Provider{{ID: "p", Kind: "openaicompat", Preset: "acme"}}}
	return db, src, NewStore(db, src)
}

// testPresets injects a preset the shipped file does not carry, so the sync
// test does not depend on a generated file's contents.
func testPresets() Presets {
	return Presets{"acme": {Name: "Acme", Kind: "openaicompat", ModelsDevID: "acme"}}
}

func TestSyncWritesPricesAndLimits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(syncDoc))
	}))
	defer srv.Close()

	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{URL: srv.URL, Presets: testPresets()})
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	rows, _ := db.Models(context.Background())
	byID := map[string]store.ModelRow{}
	for _, r := range rows {
		byID[r.ModelID] = r
	}
	if got := byID["cheap"].InputMicrosPerMTok; got != 140_000 {
		t.Errorf("cheap input price = %d, want 140000", got)
	}
	if got := byID["big"].ContextWindow; got != 200_000 {
		t.Errorf("big context = %d", got)
	}
	if !byID["big"].Capabilities.Vision || !byID["big"].Capabilities.Tools {
		t.Errorf("big capabilities = %+v", byID["big"].Capabilities)
	}
	if byID["big"].CapabilitiesSource != "models_dev" {
		t.Errorf("big source = %q", byID["big"].CapabilitiesSource)
	}
	// A model models.dev has never heard of keeps its inferred metadata rather
	// than acquiring somebody else's.
	if byID["private"].CapabilitiesSource != "inferred" || byID["private"].ContextWindow != 0 {
		t.Errorf("private = %+v", byID["private"])
	}
}

func TestSyncRebuildsTheSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(syncDoc))
	}))
	defer srv.Close()

	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{URL: srv.URL, Presets: testPresets()})
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, ok := cat.Snapshot().Lookup("p", "big")
	if !ok {
		t.Fatal("big is missing from the rebuilt snapshot")
	}
	if m.ContextWindow != 200_000 {
		t.Errorf("snapshot context = %d", m.ContextWindow)
	}
}

func TestSyncSurvivesEveryFailureShape(t *testing.T) {
	// Spec §4: a fetch error leaves the cache and logs a warning. All three
	// shapes must leave the already-written prices exactly as they were.
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(syncDoc))
	}))
	defer good.Close()

	db, src, cat := syncFixture(t)
	if err := NewSyncer(db, src, cat, SyncOptions{URL: good.URL, Presets: testPresets()}).
		SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	cases := map[string]http.HandlerFunc{
		"500": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
		"malformed": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"acme":{"models":`))
		},
		"empty": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) },
		"html":  func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>502</html>`)) },
	}
	for name, h := range cases {
		bad := httptest.NewServer(h)
		s := NewSyncer(db, src, cat, SyncOptions{URL: bad.URL, Presets: testPresets()})
		if err := s.SyncOnce(context.Background()); err == nil {
			t.Errorf("%s: SyncOnce returned nil, want an error", name)
		}
		bad.Close()

		rows, _ := db.Models(context.Background())
		for _, r := range rows {
			if r.ModelID == "cheap" && r.InputMicrosPerMTok != 140_000 {
				t.Errorf("%s: the cache was damaged; cheap price = %d", name, r.InputMicrosPerMTok)
			}
		}
	}
}

func TestSyncTimesOut(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{
		URL: srv.URL, Timeout: 100 * time.Millisecond, Presets: testPresets(),
	})
	start := time.Now()
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Error("a hung CDN did not time out")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v; the timeout is not applied", elapsed)
	}
}

func TestColdStartWithNoNetworkStillHasMetadata(t *testing.T) {
	// The done criterion: Darkrouter starts and serves with no outbound access
	// to models.dev. The embedded snapshot is what makes that produce real
	// prices rather than a blank catalog.
	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{
		URL: "http://127.0.0.1:1/api.json", Timeout: time.Second, Presets: testPresets(),
	})
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("an unreachable host returned no error")
	}
	// The syncer must still serve the embedded document, so the merge has
	// something to work with.
	if len(s.Doc()) < 100 {
		t.Errorf("Doc() has %d providers with no network; the fallback is not wired", len(s.Doc()))
	}
	if err := cat.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := cat.Snapshot().Lookup("p", "big"); !ok {
		t.Error("the catalog is unusable with no network")
	}
}

func TestSyncRunStopsOnCancel(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(syncDoc))
	}))
	defer srv.Close()

	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{
		URL: srv.URL, Interval: time.Hour, Presets: testPresets(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run(ctx) }()

	// Run syncs once immediately so a fresh install is priced without waiting
	// twelve hours.
	deadline := time.After(5 * time.Second)
	for hits.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("Run never performed its first sync")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestSync|TestColdStart' -race -v
```

Expected: FAIL to build — `undefined: NewSyncer`, `undefined: SyncOptions`.

- [ ] **Step 3: Write the syncer**

Create `internal/catalog/sync.go`:

```go
package catalog

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// ModelsDevURL is the published document. Spec §4.
const ModelsDevURL = "https://models.dev/api.json"

// maxDocBytes bounds the fetch. The real document is around four megabytes; a
// cap two orders above that stops a compromised or broken CDN exhausting
// memory on a worker nobody is watching.
const maxDocBytes = 64 << 20

// SyncOptions configures the worker. The zero value is the shipped default.
type SyncOptions struct {
	URL      string
	Interval time.Duration
	Timeout  time.Duration
	// Presets overrides the embedded set. Tests use it; production does not.
	Presets Presets
}

func (o SyncOptions) withDefaults() SyncOptions {
	if o.URL == "" {
		o.URL = ModelsDevURL
	}
	if o.Interval <= 0 {
		o.Interval = 12 * time.Hour
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Presets == nil {
		o.Presets = Embedded()
	}
	return o
}

// Syncer refreshes model metadata from models.dev.
type Syncer struct {
	db   *store.DB
	src  provider.Source
	cat  *Store
	opts SyncOptions

	client *http.Client
	// doc is the newest document successfully parsed, starting at the embedded
	// fallback. It is replaced wholesale and read without a lock, which is what
	// makes a failed fetch a no-op rather than a window where it is empty.
	doc atomic.Pointer[Doc]
}

func NewSyncer(db *store.DB, src provider.Source, cat *Store, opts SyncOptions) *Syncer {
	opts = opts.withDefaults()
	s := &Syncer{
		db: db, src: src, cat: cat, opts: opts,
		client: &http.Client{Timeout: opts.Timeout},
	}
	fb := FallbackDoc()
	s.doc.Store(&fb)
	return s
}

// Doc returns the newest metadata the syncer holds — the embedded snapshot
// until a fetch succeeds. It never returns nil, so a gateway with no outbound
// network still has prices and context windows.
func (s *Syncer) Doc() Doc {
	if d := s.doc.Load(); d != nil {
		return *d
	}
	return Doc{}
}

// Run syncs on a jittered interval until ctx is cancelled.
func (s *Syncer) Run(ctx context.Context) error {
	// An immediate first sync, so a fresh install is priced without waiting
	// half a day. A failure here is expected on an offline install and must
	// not stop the worker.
	if err := s.SyncOnce(ctx); err != nil {
		log.Printf("models.dev sync: %v (serving embedded metadata)", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jitter(s.opts.Interval)):
			if err := s.SyncOnce(ctx); err != nil {
				log.Printf("models.dev sync: %v (previous metadata retained)", err)
			}
		}
	}
}

// SyncOnce fetches, maps, and writes.
//
// Every failure path returns before touching the database. That is the whole
// contract from spec §4: a fetch error leaves the cache alone and logs a
// warning, because a gateway that loses its prices when a CDN has a bad minute
// is worse than one running on yesterday's numbers.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	doc, err := s.fetch(ctx)
	if err != nil {
		return err
	}
	s.doc.Store(&doc)

	providers, err := s.src.Providers(ctx)
	if err != nil {
		return fmt.Errorf("models.dev sync: providers: %w", err)
	}
	presetOf := make(map[string]Preset, len(providers))
	for _, p := range providers {
		presetOf[p.ID] = s.opts.Presets[p.Preset]
	}

	rows, err := s.db.Models(ctx)
	if err != nil {
		return fmt.Errorf("models.dev sync: models: %w", err)
	}

	updates := make([]store.MetadataRow, 0, len(rows))
	for _, r := range rows {
		preset, ok := presetOf[r.ProviderID]
		if !ok {
			continue // an orphan row; the merge drops it anyway
		}
		meta, joined := Join(preset, doc, r.ModelID)
		if !joined {
			// Spec §4.1: a model that fails to join is not an error. Leaving
			// its row untouched is what keeps its inferred metadata inferred
			// rather than acquiring somebody else's numbers.
			continue
		}
		// A runtime that read its own capabilities, or an operator who set
		// them, outranks a directory's entry for a model of the same name. The
		// row then keeps both its label and its values — keeping only the
		// label would be worse than either.
		source := sourceAfterSync(r.CapabilitiesSource)
		caps := r.Capabilities
		if source == string(SourceModelsDev) {
			caps = store.ModelCapabilities{
				Tools:     meta.Capabilities.Tools,
				Vision:    meta.Capabilities.Vision,
				Reasoning: meta.Capabilities.Reasoning,
			}
		}

		updates = append(updates, store.MetadataRow{
			ProviderID:      r.ProviderID,
			ModelID:         r.ModelID,
			Publisher:       r.Publisher,
			Surfaces:        r.Surfaces,
			ContextWindow:   meta.ContextWindow,
			MaxOutputTokens: meta.MaxOutputTokens,
			// Limits and pricing always come from models.dev when the join
			// succeeded: precedence is per field, not per record.
			Capabilities:           caps,
			CapabilitiesSource:     source,
			InputMicrosPerMTok:     meta.InputMicrosPerMTok,
			OutputMicrosPerMTok:    meta.OutputMicrosPerMTok,
			CacheReadMicrosPerMTok: meta.CacheReadMicrosPerMTok,
		})
	}

	if len(updates) > 0 {
		if err := s.db.UpsertMetadata(ctx, updates); err != nil {
			return fmt.Errorf("models.dev sync: write: %w", err)
		}
	}
	if err := s.cat.Rebuild(ctx); err != nil {
		return fmt.Errorf("models.dev sync: rebuild: %w", err)
	}
	return nil
}

// sourceAfterSync keeps a discovered source discovered. Capabilities a runtime
// reported about itself are better evidence than a directory's entry for a
// model of the same name, and the merge encodes the same order.
func sourceAfterSync(current string) string {
	if current == string(SourceDiscovered) || current == string(SourceOverride) {
		return current
	}
	return string(SourceModelsDev)
}

func (s *Syncer) fetch(ctx context.Context) (Doc, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.opts.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("models.dev sync: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models.dev sync: fetch: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("models.dev sync: fetch returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocBytes))
	if err != nil {
		return nil, fmt.Errorf("models.dev sync: read: %w", err)
	}
	return ParseModelsDev(body)
}
```

`UpsertMetadata` writes whatever it is given, deliberately: the precedence decision belongs here, beside the rest of the table, rather than being split across two packages.

- [ ] **Step 4: Write the test for the discovered-capability interaction**

Add to `internal/catalog/sync_test.go`:

```go
func TestSyncDoesNotOverwriteDiscoveredCapabilities(t *testing.T) {
	// A runtime that reported its own tool support outranks models.dev's entry
	// for a model of the same name. Both the label and the values must survive.
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(syncDoc))
	}))
	defer srv.Close()

	db, src, cat := syncFixture(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []store.DiscoveredModel{
		{ModelID: "big", Capabilities: &store.ModelCapabilities{Tools: false}},
		{ModelID: "cheap"}, {ModelID: "private"},
	}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := NewSyncer(db, src, cat, SyncOptions{URL: srv.URL, Presets: testPresets()}).
		SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Models(ctx)
	for _, r := range rows {
		if r.ModelID != "big" {
			continue
		}
		if r.CapabilitiesSource != "discovered" {
			t.Errorf("source = %q, want discovered", r.CapabilitiesSource)
		}
		if r.Capabilities.Tools {
			t.Error("models.dev overwrote a discovered capability")
		}
		// Limits and pricing are a different field and still come from
		// models.dev: precedence is per field.
		if r.ContextWindow != 200_000 {
			t.Errorf("context = %d; capability precedence leaked into limits", r.ContextWindow)
		}
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestSync|TestColdStart' -race -count=1 -v
```

Expected: PASS, seven tests.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog/sync.go internal/catalog/sync_test.go
git commit -m "feat(catalog): sync metadata from models.dev"
```

---

### Task 15: The catalog configuration block

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/load.go`
- Modify: `darkrouter.example.yaml`
- Test: `internal/config/load_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.CatalogConfig` on `config.Config.Catalog`, with defaults applied by `applyDefaults`. Tasks 16 and 17 read it.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: every neighbouring block in `config.go` and its default in `applyDefaults` is the pattern, verbatim.

`config.Parse` uses `KnownFields(true)`, so a `catalog:` block in a user's file is a **hard parse failure** until this struct exists. That is the reason this task comes before the two that consume it, rather than after.

`discovery.enabled` exists because master design and the carried-forward list both note that discovery is outbound traffic the gateway initiates on a user's behalf. An operator on a locked-down network needs an off switch that is not "delete every provider".

- [ ] **Step 1: Write the failing test**

Add to `internal/config/load_test.go`:

```go
func TestCatalogDefaults(t *testing.T) {
	c, err := Parse([]byte("server:\n  proxy_listen: \":8080\"\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Catalog.ModelsDevURL != "https://models.dev/api.json" {
		t.Errorf("url = %q", c.Catalog.ModelsDevURL)
	}
	if c.Catalog.SyncInterval != 12*time.Hour {
		t.Errorf("sync interval = %v, want 12h", c.Catalog.SyncInterval)
	}
	if c.Catalog.Discovery.Interval != 15*time.Minute {
		t.Errorf("discovery interval = %v, want 15m", c.Catalog.Discovery.Interval)
	}
	if c.Catalog.Discovery.Concurrency != 8 {
		t.Errorf("concurrency = %d, want 8", c.Catalog.Discovery.Concurrency)
	}
	// Enabled is a pointer so "absent" and "explicitly false" stay apart; the
	// default is on.
	if c.Catalog.Discovery.Enabled == nil || !*c.Catalog.Discovery.Enabled {
		t.Errorf("discovery enabled = %v, want true", c.Catalog.Discovery.Enabled)
	}
}

func TestCatalogBlockParses(t *testing.T) {
	c, err := Parse([]byte(`
server:
  proxy_listen: ":8080"
catalog:
  models_dev_url: https://example.invalid/api.json
  sync_interval: 1h
  discovery:
    enabled: false
    interval: 90s
    concurrency: 2
    timeout: 5s
`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Catalog.ModelsDevURL != "https://example.invalid/api.json" {
		t.Errorf("url = %q", c.Catalog.ModelsDevURL)
	}
	if c.Catalog.SyncInterval != time.Hour {
		t.Errorf("sync interval = %v", c.Catalog.SyncInterval)
	}
	if c.Catalog.Discovery.Enabled == nil || *c.Catalog.Discovery.Enabled {
		t.Errorf("discovery enabled = %v, want an explicit false", c.Catalog.Discovery.Enabled)
	}
	if c.Catalog.Discovery.Interval != 90*time.Second || c.Catalog.Discovery.Concurrency != 2 {
		t.Errorf("discovery = %+v", c.Catalog.Discovery)
	}
	if c.Catalog.Discovery.Timeout != 5*time.Second {
		t.Errorf("timeout = %v", c.Catalog.Discovery.Timeout)
	}
}

func TestUnknownCatalogFieldIsRejected(t *testing.T) {
	// KnownFields(true) is what turns a typo into a startup error rather than
	// a setting that silently did nothing.
	_, err := Parse([]byte("catalog:\n  sync_intervals: 1h\n"), nil)
	if err == nil {
		t.Fatal("a misspelled catalog field parsed cleanly")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/config/ -run TestCatalog -v
```

Expected: FAIL to build — `c.Catalog undefined`.

- [ ] **Step 3: Add the block**

In `internal/config/config.go`, add the field to `Config` alongside `Policy`, `Log` and `Capture`:

```go
	Catalog CatalogConfig `yaml:"catalog"`
```

and the types:

```go
// CatalogConfig governs the two background workers that keep the model catalog
// current.
type CatalogConfig struct {
	ModelsDevURL string        `yaml:"models_dev_url"`
	SyncInterval time.Duration `yaml:"sync_interval"`
	SyncTimeout  time.Duration `yaml:"sync_timeout"`

	Discovery DiscoveryConfig `yaml:"discovery"`
}

type DiscoveryConfig struct {
	// Enabled is a pointer so an explicit false is distinguishable from an
	// absent key, which is what lets the default be on. Discovery is outbound
	// traffic the gateway initiates on the operator's behalf, so it needs an
	// off switch that is not "delete every provider".
	Enabled  *bool         `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`
	// Concurrency is the cap across the whole discovery fleet, not per
	// provider: forty providers must not open forty connections on boot.
	Concurrency int `yaml:"concurrency"`
}
```

In `internal/config/load.go`, inside `applyDefaults`:

```go
	if c.Catalog.ModelsDevURL == "" {
		c.Catalog.ModelsDevURL = "https://models.dev/api.json"
	}
	if c.Catalog.SyncInterval == 0 {
		c.Catalog.SyncInterval = 12 * time.Hour
	}
	if c.Catalog.SyncTimeout == 0 {
		c.Catalog.SyncTimeout = 30 * time.Second
	}
	if c.Catalog.Discovery.Enabled == nil {
		on := true
		c.Catalog.Discovery.Enabled = &on
	}
	if c.Catalog.Discovery.Interval == 0 {
		c.Catalog.Discovery.Interval = 15 * time.Minute
	}
	if c.Catalog.Discovery.Timeout == 0 {
		c.Catalog.Discovery.Timeout = 15 * time.Second
	}
	if c.Catalog.Discovery.Concurrency == 0 {
		c.Catalog.Discovery.Concurrency = 8
	}
```

- [ ] **Step 4: Document it in the example configuration**

Append to `darkrouter.example.yaml`, after the `capture:` block:

```yaml
# The model catalog. Both workers are optional: Darkrouter starts and serves
# with no outbound access to models.dev, falling back to the metadata snapshot
# embedded in the binary.
catalog:
  models_dev_url: https://models.dev/api.json
  sync_interval: 12h
  sync_timeout: 30s
  discovery:
    # Discovery probes each provider's model-listing endpoint. It is outbound
    # traffic the gateway initiates on your behalf; set false to disable it and
    # rely on presets and models.dev alone.
    enabled: true
    interval: 15m
    timeout: 15s
    # A cap across the whole fleet, not per provider.
    concurrency: 8
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/config/ -race -count=1 -v
```

Expected: PASS. If a test parses `darkrouter.example.yaml` and now fails, read the error: it means the example file and the struct disagree, which is exactly what that test is for.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/load.go \
  internal/config/load_test.go darkrouter.example.yaml
git commit -m "feat(config): add the catalog block"
```

---

### Task 16: The executor reads the catalog snapshot

**Files:**
- Modify: `internal/exec/exec.go`
- Modify: `internal/exec/count.go`
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `catalog.Store` / `catalog.Snapshot` (Task 9).
- Produces: `exec.CatalogSource` on `exec.Deps.Catalog`. Task 17 supplies it from `server.New`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: `Deps` already carries three optional collaborators with the same nil-means-disabled contract.

Until now `internal/exec` built a throwaway `catalog.FromProviders(providers)` on every request — Phase 3's placeholder, where every model's capabilities were inferred and nothing knew a context window. This replaces it with the live snapshot while keeping the old path as the nil fallback, so a `Deps` with no catalog still works exactly as it did.

- [ ] **Step 1: Write the failing test**

Add to `internal/exec/exec_test.go`:

```go
func TestExecutorUsesTheCatalogSnapshotWhenSupplied(t *testing.T) {
	// The router filters on what the catalog says, so a model the snapshot
	// does not carry must not route — that is the difference between phase 3's
	// admit-everything placeholder and a real catalog.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the upstream was called for a model the catalog does not carry")
		w.WriteHeader(500)
	}))
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "known", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}}, []string{"p"}))

	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [known]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Catalog: cat})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"unknown","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest {
		t.Errorf("status = %d for a model outside the catalog", w.Code)
	}
}

func TestExecutorFallsBackWithoutACatalog(t *testing.T) {
	// A zero Deps must behave exactly as phase 3 did: every configured model
	// routes, with inferred capabilities.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [known]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"known","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
}
```

`internal/exec/exec_test.go` already has `newExecutor(t, upstreamURL)` and `newExecutorWith(t, upstreamURL, deps, total)`, but both hard-code one provider named `fake` serving one model named `m`, which none of the catalog cases can use. Add a sibling beside them:

```go
// executorFor builds an executor over an arbitrary configuration body.
// newExecutorWith cannot: it fixes the provider id, the kind, and the model
// list, and the catalog cases need all three to vary.
func executorFor(t *testing.T, body string, adapters map[string]adapter.Adapter, deps Deps) *Executor {
	t.Helper()
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return New(cfgStore, provider.NewYAMLSource(cfgStore), adapters, deps)
}
```

`filepath`, `os` and `config` are already imported by that file.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestExecutorUsesTheCatalog|TestExecutorFallsBack' -v
```

Expected: FAIL to build — `unknown field Catalog in struct literal of type Deps`.

- [ ] **Step 3: Add the dependency**

In `internal/exec/exec.go`, beside the other collaborator interfaces:

```go
// CatalogSource supplies the live catalog. It is an interface rather than
// *catalog.Store so a test can hand over a fixed snapshot, and it is optional:
// a nil one falls back to phase 3's provider-derived view, where every model's
// capabilities are inferred.
type CatalogSource interface {
	Snapshot() *catalog.Snapshot
}
```

and on `Deps`:

```go
type Deps struct {
	Log     Logger
	Health  HealthRecorder
	Fleet   Fleet
	Catalog CatalogSource
}
```

Add the resolver method on `Executor`:

```go
// catalogFor returns the live snapshot, or phase 3's provider-derived view
// when no catalog is wired. The fallback is what keeps a zero Deps usable.
func (e *Executor) catalogFor(providers []provider.Provider) catalog.Reader {
	if e.deps.Catalog != nil {
		return e.deps.Catalog.Snapshot()
	}
	return catalog.FromProviders(providers)
}
```

In `Handle`, replace the snapshot's catalog line:

```go
	snap := router.Snapshot{
		At:        start,
		Providers: providers,
		Catalog:   e.catalogFor(providers),
		Config:    cfg,
	}
```

And in `internal/exec/count.go`, the same substitution at line 63:

```go
		Catalog: e.catalogFor(providers), Config: cfg,
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -race -count=1 -v
```

Expected: PASS, including every pre-existing test — they all construct `Deps` without a catalog and must keep working unchanged.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/exec/exec.go internal/exec/count.go internal/exec/exec_test.go
git commit -m "feat(exec): route against the catalog snapshot"
```

---

### Task 17: The server starts both workers

**Files:**
- Modify: `internal/server/server.go`
- Test: `internal/server/run_test.go`

**Interfaces:**
- Consumes: `catalog.NewStore` / `Rebuild` (Task 9), `catalog.NewDiscoverer` (Task 12), `catalog.NewSyncer` (Task 14), `config.CatalogConfig` (Task 15), `exec.Deps.Catalog` (Task 16).
- Produces: `(*Server).Catalog() *catalog.Store` and `(*Server).Discoverer() *catalog.Discoverer`, which Tasks 21 and 22 use.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: `Run` already has a `startWorker` helper and five workers using it; these are the sixth and seventh.

Two ordering facts that are easy to get wrong:

- **The catalog is rebuilt once, synchronously, before either listener binds.** Otherwise a request arriving in the first second routes against an empty snapshot and 404s on a model the database has known about since the last run.
- **Both workers take `workerCtx`, not `ctx`.** `Run` already keeps worker lifetime separate from request lifetime; a discovery sweep cancelled the instant SIGTERM arrives would leave `provider_discovery` recording a failure that was really a shutdown.

- [ ] **Step 1: Write the failing test**

Add to `internal/server/run_test.go`:

```go
func TestNewRebuildsTheCatalogBeforeServing(t *testing.T) {
	// A request arriving in the first second must route against what the
	// database already knows, not against an empty snapshot.
	ctx := context.Background()
	db, key, cfgStore := serverFixture(t)

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertCredential(ctx, key, store.Credential{
		ProviderID: "p", Kind: "static", Secret: "sk", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]store.DiscoveredModel{{ModelID: "already-known"}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	srv, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.Catalog().Snapshot().Lookup("p", "already-known"); !ok {
		t.Error("New did not rebuild the catalog; the first request would 404")
	}
}

func TestRunStartsAndStopsTheCatalogWorkers(t *testing.T) {
	// The real assertion is the absence of a leak: Run must return, and the
	// race detector must see no worker still touching the database after it.
	db, key, cfgStore := serverFixture(t)
	srv, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return; a catalog worker is not honouring its context")
	}
}

func TestDiscoveryCanBeDisabled(t *testing.T) {
	// Discovery is outbound traffic the gateway initiates on the operator's
	// behalf. An operator on a locked-down network needs an off switch.
	db, key, cfgStore := serverFixtureWith(t, `
server:
  proxy_listen: "127.0.0.1:0"
  admin_listen: "127.0.0.1:0"
catalog:
  discovery:
    enabled: false
`)
	srv, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if srv.Discoverer() != nil {
		t.Error("a discoverer was built with discovery disabled")
	}
}
```

`internal/server/run_test.go` already has `serverBackedBy(t, cfgStore)`, which opens a database, migrates it, opens the keyring, imports the configured providers, and calls `New`. It returns only the `*Server`, so these tests need the database and key too. Add two helpers beside it:

```go
// storeFor writes a configuration body and returns its store.
func storeFor(t *testing.T, body string) *config.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return cfgStore
}

// serverFixtureWith is serverBackedBy with the database and key handed back, so
// a test can seed catalog rows before New reads them.
func serverFixtureWith(t *testing.T, body string) (*store.DB, *crypto.Key, *config.Store) {
	t.Helper()
	cfgStore := storeFor(t, body)
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, err := store.OpenKeyring(ctx, db, "test-master")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportFromConfig(ctx, db, key, cfgStore.Current()); err != nil {
		t.Fatal(err)
	}
	return db, key, cfgStore
}

func serverFixture(t *testing.T) (*store.DB, *crypto.Key, *config.Store) {
	t.Helper()
	return serverFixtureWith(t, "server:\n  proxy_listen: \"127.0.0.1:0\"\n  admin_listen: \"127.0.0.1:0\"\n")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/server/ -run 'TestNewRebuilds|TestRunStartsAndStops|TestDiscoveryCanBe' -v
```

Expected: FAIL to build — `srv.Catalog undefined`, `srv.Discoverer undefined`.

- [ ] **Step 3: Build the catalog in `New`**

In `internal/server/server.go`, add the fields:

```go
type Server struct {
	store   *config.Store
	db      *store.DB
	src     *provider.SQLSource
	ex      *exec.Executor
	logw    *store.LogWriter
	breaker *health.Breaker
	persist *health.Persister

	cat  *catalog.Store
	disc *catalog.Discoverer
	sync *catalog.Syncer

	started  time.Time
	warnings []string
}

// Catalog exposes the live snapshot holder. The listing handlers read it, and
// phase 7's admin API will too.
func (s *Server) Catalog() *catalog.Store { return s.cat }

// Discoverer exposes the worker so a provider change can trigger an immediate
// probe. It is nil when discovery is disabled.
func (s *Server) Discoverer() *catalog.Discoverer { return s.disc }
```

and build them in `New`, after the breaker and before the `Executor`:

```go
	cat := catalog.NewStore(db, src)
	// Rebuilt synchronously, before anything binds a listener. A request
	// arriving in the first second must route against what the database
	// already knows rather than against an empty snapshot.
	if err := cat.Rebuild(context.Background()); err != nil {
		// Not fatal: an unreadable catalog costs routing precision, and
		// refusing to serve over it would be worse. It reaches /healthz
		// through the same channel a bad config edit does.
		startupWarnings = append(startupWarnings, fmt.Sprintf("catalog: %v", err))
	}

	var disc *catalog.Discoverer
	if e := cfg.Catalog.Discovery.Enabled; e == nil || *e {
		disc = catalog.NewDiscoverer(db, src, cat, breaker, catalog.DiscoveryOptions{
			Interval:    cfg.Catalog.Discovery.Interval,
			Concurrency: cfg.Catalog.Discovery.Concurrency,
			Timeout:     cfg.Catalog.Discovery.Timeout,
		})
	}
	syncer := catalog.NewSyncer(db, src, cat, catalog.SyncOptions{
		URL:      cfg.Catalog.ModelsDevURL,
		Interval: cfg.Catalog.SyncInterval,
		Timeout:  cfg.Catalog.SyncTimeout,
	})
```

Then carry them onto the returned `Server` and hand the catalog to the executor:

```go
	return &Server{
		store: cfgStore, db: db, src: src, logw: logw, breaker: breaker,
		persist: health.NewPersister(breaker, db, 5*time.Second),
		cat:     cat, disc: disc, sync: syncer,
		ex: exec.New(cfgStore, src, map[string]adapter.Adapter{
			"openaicompat": openaicompat.New(),
			"anthropic":    anthropicadapter.New(),
			"gemini":       geminiadapter.New(),
		}, exec.Deps{
			Log: logw, Health: breaker, Fleet: breaker, Catalog: cat,
		}),
		started:  time.Now(),
		warnings: startupWarnings,
	}, nil
```

Add `"github.com/darkraise/darkrouter/internal/catalog"` to the imports.

- [ ] **Step 4: Start them in `Run`**

In `internal/server/server.go`, beside the existing `startWorker` calls:

```go
	// Both take workerCtx rather than ctx. Run already keeps worker lifetime
	// separate from request lifetime, and a sweep cancelled the instant
	// SIGTERM arrives would record a provider failure that was really a
	// shutdown — three of those mark its whole catalogue stale.
	if s.disc != nil {
		startWorker("discovery", s.disc.Run)
	}
	startWorker("models.dev sync", s.sync.Run)
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/server/ -race -count=1 -v
```

Expected: PASS. `TestRunStartsAndStopsTheCatalogWorkers` is the one that matters: if `Run` hangs, a worker is ignoring its context.

Both workers will try to reach the network during these tests. That is intentional and harmless — the sync logs a warning and serves embedded metadata, and discovery has no providers to probe in most fixtures. If a test becomes slow, set `catalog.discovery.enabled: false` and a bogus `models_dev_url` in that fixture's configuration rather than adding a sleep.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Run the server package repeatedly**

The shutdown path grew two goroutines.

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/server/ -race -count=5
```

Expected: `ok`, no `DATA RACE` reports and no hangs.

- [ ] **Step 8: Commit**

```bash
git add internal/server/server.go internal/server/run_test.go
git commit -m "feat(server): run the catalog workers"
```

---

### Task 18: The adapter target carries the model's facts

**Files:**
- Modify: `internal/adapter/adapter.go`
- Modify: `internal/exec/exec.go`
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `catalog.Model` (Task 8), `exec.CatalogSource` (Task 16).
- Produces: `adapter.ModelInfo` and `adapter.Target.Info`. Tasks 19 and 20 read `t.Info` inside the Anthropic adapter.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: `Target` is already a plain struct of scalars filled at one call site, and this adds one field filled at the same place.

`ModelInfo` is a plain struct declared in `adapter`, with **no import of `catalog`**. `health` imports `adapter`, and `catalog` imports `health` so discovery can cool a credential; an `adapter` that imported `catalog` would close the cycle `adapter → catalog → health → adapter`. Translating at the `exec` boundary is what keeps the graph acyclic, and it mirrors how `Target` already carries `BaseURL` rather than importing `provider`.

- [ ] **Step 1: Write the failing test**

Add to `internal/exec/exec_test.go`:

```go
func TestTargetCarriesTheCatalogFacts(t *testing.T) {
	// The adapter has to learn the model's real maximum and its request shape
	// from somewhere, and reading the name is what phase 6 exists to stop.
	var got adapter.Target
	capturing := &captureAdapter{onBuild: func(tgt *adapter.Target) { got = *tgt }}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces:        []ir.Surface{ir.SurfaceLLM},
		ContextWindow:   200_000,
		MaxOutputTokens: 64_000,
		Traits:          catalog.Traits{Adaptive: true, FreeSampling: false, Known: true},
	}}, []string{"p"}))

	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: capture
    base_url: `+upstream.URL+`
    api_key: sk
    models: [m]
`, map[string]adapter.Adapter{"capture": capturing}, Deps{Catalog: cat})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if got.Info.MaxOutputTokens != 64_000 || got.Info.ContextWindow != 200_000 {
		t.Errorf("limits = %+v", got.Info)
	}
	if !got.Info.TraitsKnown || !got.Info.Adaptive || got.Info.FreeSampling {
		t.Errorf("traits = %+v", got.Info)
	}
}

func TestTargetInfoIsZeroWithoutACatalogEntry(t *testing.T) {
	// A model nothing knows about must reach the adapter with an empty Info,
	// so the adapter honors what the client asked for rather than acting on a
	// half-filled guess.
	var got adapter.Target
	capturing := &captureAdapter{onBuild: func(tgt *adapter.Target) { got = *tgt }}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: capture
    base_url: `+upstream.URL+`
    api_key: sk
    models: [m]
`, map[string]adapter.Adapter{"capture": capturing}, Deps{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if got.Info != (adapter.ModelInfo{}) {
		t.Errorf("Info = %+v, want the zero value", got.Info)
	}
}
```

These use `executorFor` from Task 16. `captureAdapter` is a test double that records the `Target` it was handed and otherwise delegates to `openaicompat`; add it beside `executorFor`:

```go
// captureAdapter records the Target it was built with and otherwise behaves
// exactly as an OpenAI-compatible adapter.
type captureAdapter struct {
	onBuild func(*adapter.Target)
}

func (c *captureAdapter) Kind() string { return "capture" }

func (c *captureAdapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	if c.onBuild != nil {
		c.onBuild(t)
	}
	return openaicompat.New().BuildRequest(ctx, t, req)
}

func (c *captureAdapter) ParseResponse(resp *http.Response) (*ir.Response, error) {
	return openaicompat.New().ParseResponse(resp)
}

func (c *captureAdapter) ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return openaicompat.New().ParseStream(r, maxLine)
}

func (c *captureAdapter) Classify(resp *http.Response, err error) adapter.Outcome {
	return openaicompat.New().Classify(resp, err)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run TestTarget -v
```

Expected: FAIL to build — `got.Info undefined`.

- [ ] **Step 3: Add the type**

In `internal/adapter/adapter.go`:

```go
// ModelInfo is what the catalog knows about the model this target names.
//
// It is a plain struct rather than a catalog type on purpose. health imports
// adapter, and catalog imports health so discovery can cool a credential on a
// rejected probe; an adapter that imported catalog would close the cycle. The
// translation happens at the exec boundary, exactly as Target already carries a
// base URL rather than importing provider.
//
// The zero value means the catalog knows nothing, and every adapter reading it
// must behave as it did before phase 6 in that case: honor what the client
// asked for rather than acting on a half-filled guess.
type ModelInfo struct {
	ContextWindow   int
	MaxOutputTokens int

	// The three per-generation request-shape facts. TraitsKnown gates all
	// three: an unrecognized or proxied model reaches here with TraitsKnown
	// false, which is the honest answer.
	Adaptive     bool
	ManualBudget bool
	FreeSampling bool
	TraitsKnown  bool
}

type Target struct {
	BaseURL string
	APIKey  string
	Model   string
	Info    ModelInfo
}
```

- [ ] **Step 4: Fill it at the one call site**

In `internal/exec/exec.go`, `runAttempts` builds the target. Thread the catalog reader through so the attempt can look the model up, then fill `Info`:

```go
	tgt := &adapter.Target{
		BaseURL: p.BaseURL, APIKey: secretOf(p, c.KeyID), Model: c.Model,
		Info: modelInfo(cat, c.ProviderID, c.Model),
	}
```

and add the translator beside it:

```go
// modelInfo copies the catalog's view of one model into the adapter's plain
// struct. A miss leaves the zero value, which every adapter reads as "the
// catalog knows nothing".
func modelInfo(cat catalog.Reader, providerID, modelID string) adapter.ModelInfo {
	if cat == nil {
		return adapter.ModelInfo{}
	}
	m, ok := cat.Lookup(providerID, modelID)
	if !ok {
		return adapter.ModelInfo{}
	}
	return adapter.ModelInfo{
		ContextWindow:   m.ContextWindow,
		MaxOutputTokens: m.MaxOutputTokens,
		Adaptive:        m.Traits.Adaptive,
		ManualBudget:    m.Traits.ManualBudget,
		FreeSampling:    m.Traits.FreeSampling,
		TraitsKnown:     m.Traits.Known,
	}
}
```

The `catalog.Reader` reaches the attempt the same way `byID` does: `Handle` already builds it for the router snapshot, so pass `snap.Catalog` into `runAttempts` alongside `byID` and on to the per-attempt function. Do not call `e.catalogFor` a second time inside the attempt — a request must see one catalog for its whole lifetime, or a mid-request rebuild could change the model's maximum between two failover attempts.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -race -count=1 -v
```

Expected: PASS, including every pre-existing test.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing. `internal/golden` still passes because `Target.Info` defaults to the zero value there.

- [ ] **Step 7: Commit**

```bash
git add internal/adapter/adapter.go internal/exec/exec.go internal/exec/exec_test.go
git commit -m "feat(adapter): carry catalog facts on the target"
```

---

### Task 19: The max-tokens substitution and the effort clamp go live

**Files:**
- Modify: `internal/adapter/xlate/params.go`
- Modify: `internal/adapter/anthropic/build.go` (the two call sites)
- Test: `internal/adapter/xlate/params_test.go`

**Interfaces:**
- Consumes: `adapter.Target.Info.MaxOutputTokens` (Task 18).
- Produces: `xlate.RequiredMaxTokens(req *ir.Request, target string, catalogMax int) (int, []ir.Warning)` — a **changed signature**. `xlate.EffortBudget` keeps its signature; only its callers change.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: both functions exist with the parameter already designed in and documented as waiting for phase 6.

Two carried-forward debts close here:

- **The Anthropic `max_tokens` substitution is a hardcoded 4096.** Phase 4 recorded that "a model whose real cap is lower will still 400". With the catalog it becomes the model's real maximum.
- **`xlate.EffortBudget`'s clamp is inert** because every caller passes `maxOut: 0`. The parameter was written for this task.

A third case is closed at the same time and is a behavior change worth stating: **a client asking for more output than the model can produce is clamped with a warning rather than forwarded to a 400.** That matches what Phase 4 already does to a thinking budget that exceeds `max_tokens` — keeping a servable request servable, and recording the substitution so a truncated answer is traceable.

- [ ] **Step 1: Write the failing test**

Replace the `RequiredMaxTokens` tests in `internal/adapter/xlate/params_test.go` and add the clamp cases:

```go
func TestRequiredMaxTokensUsesTheCatalogMaximum(t *testing.T) {
	// The carried-forward debt: 4096 was a constant because nothing knew the
	// model's real cap.
	got, warns := RequiredMaxTokens(&ir.Request{}, "anthropic", 64_000)
	if got != 64_000 {
		t.Errorf("max tokens = %d, want the catalog's 64000", got)
	}
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warns))
	}
	if warns[0].Field != "max_tokens" {
		t.Errorf("warning field = %q", warns[0].Field)
	}
	// The substitution has to be visible, or a truncated answer looks like the
	// model stopping early.
	if !strings.Contains(warns[0].Reason, "64000") {
		t.Errorf("warning does not name the substituted value: %q", warns[0].Reason)
	}
}

func TestRequiredMaxTokensFallsBackWhenTheCatalogIsSilent(t *testing.T) {
	got, warns := RequiredMaxTokens(&ir.Request{}, "anthropic", 0)
	if got != DefaultMaxTokens {
		t.Errorf("max tokens = %d, want %d", got, DefaultMaxTokens)
	}
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warns))
	}
}

func TestRequiredMaxTokensKeepsTheClientValue(t *testing.T) {
	n := 1000
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic", 64_000)
	if got != 1000 {
		t.Errorf("max tokens = %d, want the client's 1000", got)
	}
	if len(warns) != 0 {
		t.Errorf("warned about a request that needed no substitution: %v", warns)
	}
}

func TestRequiredMaxTokensClampsAnImpossibleAsk(t *testing.T) {
	// Forwarding this is a 400 the client cannot diagnose. Clamping keeps a
	// servable request servable, and the warning is what makes the shorter
	// answer traceable to the substitution.
	n := 200_000
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic", 64_000)
	if got != 64_000 {
		t.Errorf("max tokens = %d, want the clamp to 64000", got)
	}
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warns))
	}
	if !strings.Contains(warns[0].Reason, "64000") {
		t.Errorf("warning does not name the clamp: %q", warns[0].Reason)
	}
}

func TestRequiredMaxTokensDoesNotClampAgainstAnUnknownMaximum(t *testing.T) {
	n := 200_000
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic", 0)
	if got != 200_000 || len(warns) != 0 {
		t.Errorf("got (%d, %v); an unknown maximum must not clamp", got, warns)
	}
}

func TestEffortBudgetClampIsLive(t *testing.T) {
	// The parameter existed from phase 4 and every caller passed 0, which
	// disabled it.
	if got := EffortBudget("high", 8192); got != 8192 {
		t.Errorf("EffortBudget(high, 8192) = %d, want the clamp to 8192", got)
	}
	if got := EffortBudget("high", 0); got != 32768 {
		t.Errorf("EffortBudget(high, 0) = %d, want the unclamped 32768", got)
	}
	if got := EffortBudget("low", 65536); got != 4096 {
		t.Errorf("EffortBudget(low, 65536) = %d, want 4096", got)
	}
}
```

Add `"strings"` to the test file's imports if it is not already there.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/xlate/ -run 'TestRequiredMaxTokens|TestEffortBudget' -v
```

Expected: FAIL to build — `not enough arguments in call to RequiredMaxTokens`.

- [ ] **Step 3: Change the function**

In `internal/adapter/xlate/params.go`, replace `RequiredMaxTokens` and update `DefaultMaxTokens`'s comment:

```go
// DefaultMaxTokens is the cap substituted when a target requires one and
// neither the request nor the catalog supplies it. Every substitution carries a
// warning, so a truncated answer is traceable to it rather than looking like
// the model stopping early.
const DefaultMaxTokens = 4096

// RequiredMaxTokens supplies the cap a target demands.
//
// catalogMax is the model's real maximum output, or 0 when the catalog does not
// know it. Three cases, each warned about when it changes what the client sent:
//
//   - No cap in the request: the catalog's maximum, or DefaultMaxTokens.
//   - A cap above the model's maximum: clamped. Forwarding it is a 400 the
//     client cannot diagnose, and keeping a servable request servable is the
//     same choice phase 4 made for a thinking budget that exceeded max_tokens.
//   - A cap the model can honor: passed through untouched and unwarned.
func RequiredMaxTokens(req *ir.Request, target string, catalogMax int) (int, []ir.Warning) {
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		asked := *req.MaxTokens
		if catalogMax > 0 && asked > catalogMax {
			return catalogMax, []ir.Warning{{
				Field:  "max_tokens",
				Target: target,
				Reason: "above the model's maximum output; clamped to " + strconv.Itoa(catalogMax),
			}}
		}
		return asked, nil
	}
	if catalogMax > 0 {
		return catalogMax, []ir.Warning{{
			Field:  "max_tokens",
			Target: target,
			Reason: "required by the target and absent from the request; substituted " +
				"the model's maximum, " + strconv.Itoa(catalogMax),
		}}
	}
	return DefaultMaxTokens, []ir.Warning{{
		Field:  "max_tokens",
		Target: target,
		Reason: "required by the target and absent from the request, and the catalog " +
			"does not know the model's maximum; substituted " + strconv.Itoa(DefaultMaxTokens),
	}}
}
```

- [ ] **Step 4: Update the two Anthropic call sites**

In `internal/adapter/anthropic/build.go`:

```go
	maxTok, w := xlate.RequiredMaxTokens(req, targetName, t.Info.MaxOutputTokens)
```

and in the `modeManual` branch:

```go
		budget := req.Reasoning.Budget
		if budget == 0 {
			budget = xlate.EffortBudget(req.Reasoning.Effort, t.Info.MaxOutputTokens)
		}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/... -race -count=1 -v
```

Expected: PASS. Existing Anthropic build tests construct a `Target` with a zero `Info`, so they take the `catalogMax == 0` path and their expectations are unchanged. If one asserts on the old warning text, update the expectation — the text now names the substituted value, which is the improvement.

- [ ] **Step 6: Regenerate and read the golden files**

The warning text changed, so the golden warning files are stale. Regenerate, then **read the diff** rather than only re-running:

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/golden/ -update
git diff --stat internal/golden/testdata
git diff internal/golden/testdata | head -80
```

Expected: only `warnings/anthropic.json` files change, and only in the `max_tokens` reason string. A change to a `rendered/` file means the substitution altered a request body, which it must not for a zero `Info` — stop and diagnose.

If `-update` is spelled differently in this suite, read `internal/golden/golden_test.go` for the flag it defines.

```bash
go test ./internal/golden/ -race -count=1
```

Expected: `ok`.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/adapter/xlate/params.go internal/adapter/xlate/params_test.go \
  internal/adapter/anthropic/build.go internal/golden/testdata
git commit -m "feat(xlate): substitute the model's real max tokens"
```

---

### Task 20: Retire the Anthropic model-name heuristic

**Files:**
- Modify: `internal/adapter/anthropic/build.go`
- Test: `internal/adapter/anthropic/build_test.go`

**Interfaces:**
- Consumes: `adapter.Target.Info` (Task 18), the `model_traits` preset data (Task 2).
- Produces: nothing new. `generations` and `traitsFor` are **deleted**; `modelTraits` keeps its name and gains `traitsOf(adapter.ModelInfo)`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4
**Approach:** inline - skip 2: the replacement is a field read where a table lookup was, and the traits already reached the target in Task 18.

This is the debt Phase 4 booked with an expiry date. `traitsFor` reads the model name to decide the thinking mode and the sampling rules, because there was no catalog. It needs a new entry every time Anthropic ships a generation, and it is **wrong for an aliased or proxied model** whose name says nothing about its generation — a gateway in front of Anthropic that renames `claude-opus-4-5` to `default` gets the permissive fallback and a 400 on every reasoning request.

The catalog fixes the identity problem, not just the maintenance one: traits now resolve through `(provider, model)` against that provider's preset, so a proxied model is either declared or honestly unknown, rather than being pattern-matched against a table it was never meant to match.

**The behavior for an unknown model does not change.** `TraitsKnown == false` gives the same permissive set the old fallback did, and the same warning fires. Every Phase 4 test that constructs a bare `Target` therefore keeps passing, which is the evidence the swap is behavior-preserving.

- [ ] **Step 1: Write the failing test**

Add to `internal/adapter/anthropic/build_test.go`:

```go
func TestTraitsComeFromTheTargetNotTheName(t *testing.T) {
	// The case the name heuristic gets wrong: a proxied model whose name says
	// nothing, whose catalog entry says everything.
	tgt := &adapter.Target{
		BaseURL: "https://proxy.example.com/v1", Model: "default",
		Info: adapter.ModelInfo{Adaptive: true, TraitsKnown: true, MaxOutputTokens: 64_000},
	}
	req := &ir.Request{
		Model:     "default",
		Messages:  []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Reasoning: &ir.Reasoning{Effort: "high"},
	}
	hr, warns, err := BuildRequest(context.Background(), tgt, req)
	if err != nil {
		t.Fatal(err)
	}
	body := decodeBody(t, hr)

	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("no thinking block: %v", body)
	}
	if thinking["type"] != "adaptive" {
		t.Errorf("thinking type = %v, want adaptive", thinking["type"])
	}
	// A known model must not carry the unrecognized-name warning.
	for _, w := range warns {
		if strings.Contains(w.Reason, "unrecognized") {
			t.Errorf("warned about a model the catalog knows: %v", w)
		}
	}
}

func TestManualBudgetComesFromTheTarget(t *testing.T) {
	tgt := &adapter.Target{
		BaseURL: "https://x/v1", Model: "house-blend",
		Info: adapter.ModelInfo{ManualBudget: true, FreeSampling: true, TraitsKnown: true, MaxOutputTokens: 64_000},
	}
	temp := 0.5
	req := &ir.Request{
		Model:       "house-blend",
		Messages:    []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Reasoning:   &ir.Reasoning{Budget: 8192},
		Temperature: &temp,
	}
	hr, _, err := BuildRequest(context.Background(), tgt, req)
	if err != nil {
		t.Fatal(err)
	}
	body := decodeBody(t, hr)

	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("no thinking block: %v", body)
	}
	if thinking["type"] != "enabled" {
		t.Errorf("thinking type = %v, want enabled", thinking["type"])
	}
	// Sampling is rejected alongside thinking even on a free-sampling model.
	if _, present := body["temperature"]; present {
		t.Error("temperature survived alongside manual thinking")
	}
}

func TestSealedSamplingComesFromTheTarget(t *testing.T) {
	// The newest generation rejects any non-default sampling value on every
	// request, thinking or not.
	tgt := &adapter.Target{
		BaseURL: "https://x/v1", Model: "sealed",
		Info: adapter.ModelInfo{Adaptive: true, FreeSampling: false, TraitsKnown: true},
	}
	temp := 0.5
	req := &ir.Request{
		Model:       "sealed",
		Messages:    []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Temperature: &temp,
	}
	hr, warns, err := BuildRequest(context.Background(), tgt, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := decodeBody(t, hr)["temperature"]; present {
		t.Error("temperature reached a model that rejects it")
	}
	var found bool
	for _, w := range warns {
		if w.Field == "temperature" {
			found = true
		}
	}
	if !found {
		t.Error("a dropped field produced no warning")
	}
}

func TestUnknownTraitsStayPermissiveAndWarn(t *testing.T) {
	// The behavior for an unknown model is unchanged from phase 4: the request
	// is shaped by what the client asked for, and the guess is warned about.
	tgt := &adapter.Target{BaseURL: "https://x/v1", Model: "who-knows"}
	req := &ir.Request{
		Model:     "who-knows",
		Messages:  []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Reasoning: &ir.Reasoning{Budget: 8192},
	}
	_, warns, err := BuildRequest(context.Background(), tgt, req)
	if err != nil {
		t.Fatal(err)
	}
	var warned bool
	for _, w := range warns {
		if w.Field == "model" && strings.Contains(w.Reason, "unrecognized") {
			warned = true
		}
	}
	if !warned {
		t.Error("an unrecognized model produced no warning")
	}
}

func TestTheNameHeuristicIsGone(t *testing.T) {
	// A real Anthropic model name with an empty Info must be treated as
	// unknown. If this passes as "adaptive" the name table is still being
	// consulted somewhere.
	tgt := &adapter.Target{BaseURL: "https://x/v1", Model: "claude-opus-4-5-20251101"}
	req := &ir.Request{
		Model:    "claude-opus-4-5-20251101",
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
	}
	_, _, err := BuildRequest(context.Background(), tgt, req)
	if err != nil {
		t.Fatal(err)
	}
	// The behavioral assertion is above; this one is structural.
	if _, err := os.Stat("build.go"); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("build.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"traitsFor", "var generations"} {
		if strings.Contains(string(src), gone) {
			t.Errorf("%s is still in build.go; the heuristic was not removed", gone)
		}
	}
}
```

These tests need `os`, `strings`, `io`, `encoding/json` and `net/http` in the file's imports. `decodeBody` is a helper this file may already have; if not, add:

```go
func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/anthropic/ -run 'TestTraits|TestManualBudget|TestSealedSampling|TestUnknownTraits|TestTheNameHeuristic' -v
```

Expected: `TestTheNameHeuristicIsGone` fails on both strings; the others may pass by accident because the fallback is permissive. That accident is exactly why the structural assertion is there.

- [ ] **Step 3: Delete the table**

In `internal/adapter/anthropic/build.go`, delete `var generations` and `func traitsFor`. Keep the `modelTraits` struct exactly as it is — its four fields are what the whole function body reads — and replace the lookup with a translator from the target:

```go
// traitsOf reads the request-shape facts off the catalog entry the executor
// attached to the target.
//
// Phase 4 matched fragments of the model name here, because there was no
// catalog. That table needed a new entry every time Anthropic shipped a
// generation, and it was wrong for an aliased or proxied model whose name says
// nothing about its generation — a gateway that renames claude-opus-4-5 to
// "default" got the permissive fallback and a 400 on every reasoning request.
//
// The permissive fallback survives for a model the catalog does not know, which
// is the honest answer for a self-hosted Anthropic-compatible endpoint: shape
// the request the way the client asked, and warn that the shape was guessed.
func traitsOf(info adapter.ModelInfo) modelTraits {
	if !info.TraitsKnown {
		return modelTraits{adaptive: true, manualBudget: true, freeSampling: true}
	}
	return modelTraits{
		adaptive:     info.Adaptive,
		manualBudget: info.ManualBudget,
		freeSampling: info.FreeSampling,
		known:        true,
	}
}
```

In `BuildRequest`, one line changes:

```go
	traits := traitsOf(t.Info)
```

`thinkingMode` keeps its `modelTraits` parameter and its whole body, and so does every sampling branch. That is what makes this a swap of the traits *source* rather than a rewrite of the logic they drive — and why the Phase 4 tests are the regression suite for it.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/anthropic/ -race -count=1 -v
```

Expected: PASS, every test in the package. The Phase 4 tests that construct a bare `Target` still pass unchanged — that is the evidence the swap preserved behavior for an unknown model.

- [ ] **Step 5: Confirm the table is really gone**

```bash
export PATH=$PATH:/usr/local/go/bin
rg -n 'traitsFor|generations|modelTraits' internal/adapter/
```

Expected: no matches. `internal/catalog/merge.go` has its own unexported `traitsFor`, which is a different package and is the data-driven replacement; the search above is scoped to `internal/adapter/` so it should not appear.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/adapter/anthropic/build.go internal/adapter/anthropic/build_test.go
git commit -m "refactor(anthropic): read traits from the catalog"
```

---

### Task 21: Capability filtering becomes selective

**Files:**
- Modify: `internal/router/types.go`
- Modify: `internal/router/filter.go`
- Test: `internal/router/filter_test.go`

**Interfaces:**
- Consumes: `catalog.Model.Routable` and `Capabilities.Known` (Task 8).
- Produces: `router.SkipRemoved`, and `router.Candidate.Inferred`. Task 22 turns that flag into a warning.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: `filterTarget` already has the ordered check list this extends, and `Satisfies` already implements the admit-unknown rule.

Two carried-forward items close here. Neither is a new rule — both are existing rules that had no data to act on.

- **Capability filtering admitted everything**, because every model's capabilities were `inferred`. With real data, a model that models.dev says has no tools stops being a candidate for a tool request. `Capabilities.Satisfies` already encodes this; the change is that `Known` is now sometimes true.
- **A retired model is still a candidate.** `removed_upstream` means a successful listing omitted it three times running. Routing to it is a guaranteed 404 that classifies as `RetryableModel` and never penalizes the provider, so nothing stops the wasted attempt on every request — which is precisely why spec §5.1 introduced the state.

The **admit-inferred-with-a-warning** rule from master design §6.4 is unchanged and load-bearing. Hard-filtering on guessed metadata would make every discovered Ollama model refuse the tool requests Claude Code always sends, so "your local models appear automatically" would mean "appear and never serve anything".

- [ ] **Step 1: Write the failing test**

Add to `internal/router/filter_test.go`:

```go
func TestRetiredModelsAreNotCandidates(t *testing.T) {
	// A removed_upstream model is a guaranteed 404 that classifies as
	// RetryableModel and never penalizes the provider, so nothing would ever
	// stop the wasted attempt.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "gone", State: catalog.StateRemovedUpstream,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}})

	// Addressed as provider/model, which is the path that reaches filterTarget.
	// A bare name never gets there — Offering already excludes retired models,
	// so resolveModel returns no targets at all.
	cands, skips, err := Resolve(Query{Model: "p/gone", Surface: ir.SurfaceLLM}, snap)
	if len(cands) != 0 {
		t.Fatalf("got %d candidates for a retired model", len(cands))
	}
	if err == nil {
		t.Error("Resolve returned no error with no candidates")
	}
	var found bool
	for _, sk := range skips {
		if sk.Reason == SkipRemoved {
			found = true
		}
	}
	if !found {
		t.Errorf("no SkipRemoved recorded; skips = %+v", skips)
	}
}

func TestRetiredModelsAreNotOfferedByBareName(t *testing.T) {
	// The other half: Offering excludes them, so a bare name is simply not
	// found rather than being found and then rejected.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "gone", State: catalog.StateRemovedUpstream,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}})
	if _, _, err := Resolve(Query{Model: "gone", Surface: ir.SurfaceLLM}, snap); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("err = %v, want ErrModelNotFound", err)
	}
}

func TestStaleModelsStillRoute(t *testing.T) {
	// The breaker, not the catalog, is what avoids a provider that is actually
	// broken. Emptying the catalog on a flaky probe would break every alias.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateStale,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}})
	cands, _, err := Resolve(Query{Model: "m", Surface: ir.SurfaceLLM}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Errorf("got %d candidates for a stale model, want 1", len(cands))
	}
}

func TestKnownCapabilitiesNowFilter(t *testing.T) {
	// The carried-forward item: this used to admit everything because nothing
	// was ever Known.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces:     []ir.Surface{ir.SurfaceLLM},
		Capabilities: catalog.Capabilities{Tools: false, Known: true},
		Source:       catalog.SourceModelsDev,
	}})
	cands, skips, _ := Resolve(Query{Model: "m", Surface: ir.SurfaceLLM, NeedsTools: true}, snap)
	if len(cands) != 0 {
		t.Errorf("a model known to have no tools was a candidate for a tool request")
	}
	var found bool
	for _, s := range skips {
		if s.Reason == SkipCapability {
			found = true
		}
	}
	if !found {
		t.Errorf("no SkipCapability recorded; skips = %+v", skips)
	}
}

func TestInferredCapabilitiesStillRouteAndAreFlagged(t *testing.T) {
	// Master design §6.4. Claude Code always sends tools; hard-filtering on a
	// guess would mean a local model appears in the catalog and never serves.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "llama3.3:70b", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
		Source:   catalog.SourceInferred,
	}})
	cands, _, err := Resolve(Query{Model: "llama3.3:70b", Surface: ir.SurfaceLLM, NeedsTools: true}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1 — an inferred model must still route", len(cands))
	}
	if !cands[0].Inferred {
		t.Error("the candidate is not flagged inferred; nothing downstream can warn")
	}
}

func TestKnownCapableModelsAreNotFlagged(t *testing.T) {
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces:     []ir.Surface{ir.SurfaceLLM},
		Capabilities: catalog.Capabilities{Tools: true, Known: true},
		Source:       catalog.SourceModelsDev,
	}})
	cands, _, _ := Resolve(Query{Model: "m", Surface: ir.SurfaceLLM, NeedsTools: true}, snap)
	if len(cands) != 1 {
		t.Fatalf("got %d candidates", len(cands))
	}
	if cands[0].Inferred {
		t.Error("a models.dev-sourced candidate was flagged inferred")
	}
}
```

`internal/router/filter_test.go` already has `fleetWith(cs ...provider.Credential)` and `snapOf(t, ps, avail)`, but `snapOf` builds its catalog with `catalog.FromProviders`, which cannot express a state or a capability. Add a sibling beside it — the provider still needs a credential, or every case falls out at `SkipNoCredential` before reaching the check under test:

```go
// snapWithModels builds a snapshot over a fixed catalog. snapOf cannot: it
// derives its catalog from the provider set, where every model is live and
// every capability inferred.
func snapWithModels(t *testing.T, ms []catalog.Model) Snapshot {
	t.Helper()
	ps := []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: "https://p.example/v1",
		Credentials: []provider.Credential{{ID: "k1", Secret: "a", Enabled: true}},
	}}
	return Snapshot{
		At:        time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC),
		Providers: ps,
		Catalog:   catalog.NewSnapshot(ms, []string{"p"}),
		Health:    health.Availability{},
	}
}
```

The tests also need `errors` in the file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/router/ -run 'TestRetired|TestStale|TestKnownCapabilities|TestInferred|TestKnownCapable' -v
```

Expected: FAIL to build — `undefined: SkipRemoved`, `cands[0].Inferred undefined`.

- [ ] **Step 3: Extend the types**

In `internal/router/types.go`:

```go
// Candidate is one attemptable target.
type Candidate struct {
	ProviderID string
	KeyID      string
	Model      string
	Kind       string
	Publisher  string // vertex only; empty in phase 3

	// Inferred marks a candidate admitted on guessed capability metadata.
	// Master design §6.4 admits these rather than excluding them, and the
	// executor records a warning so the trace explains why a provider's own
	// error came back instead of a routing decision.
	Inferred bool
}
```

and add the skip reason:

```go
const (
	SkipDisabled     SkipReason = "disabled"
	SkipCooling      SkipReason = "cooling"
	SkipSurface      SkipReason = "surface"
	SkipCapability   SkipReason = "capability"
	SkipNoCredential SkipReason = "no_credential"
	// SkipRemoved is a model a successful listing omitted three times running.
	// It is a durable fact about the upstream rather than a health signal,
	// which is why it is reported ahead of cooling.
	SkipRemoved SkipReason = "removed_upstream"
)
```

- [ ] **Step 4: Extend the filter**

In `internal/router/filter.go`, add the routability check directly after the surface check — durable configuration facts before transient ones, which is the ordering the existing comment already fixes — and carry the flag onto each candidate:

```go
	m, known := snap.Catalog.Lookup(t.ProviderID, t.ModelID)
	if !known || !m.DeclaresSurface(q.Surface) {
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipSurface}}, true
	}

	// A model a successful listing omitted three times running is gone. A 404
	// classifies as RetryableModel and never penalizes the provider, so without
	// this the wasted attempt happens on every request, forever.
	if !m.Routable() {
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipRemoved}}, true
	}

	if !m.Capabilities.Satisfies(catalog.Capabilities{
		Tools: q.NeedsTools, Vision: q.NeedsVision, Reasoning: q.NeedsReasoning,
	}) {
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipCapability}}, true
	}
```

and in the credential loop:

```go
		cands = append(cands, Candidate{
			ProviderID: p.ID, KeyID: c.ID, Model: t.ModelID, Kind: p.Kind,
			// Recorded per candidate rather than per request: a chain can mix
			// a models.dev-backed model with a locally discovered one, and the
			// warning belongs to whichever actually served.
			Inferred: !m.Capabilities.Known,
		})
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/router/ -race -count=1 -v
```

Expected: PASS, every test in the package. Existing tests build catalogs through `catalog.FromProviders`, whose models are `StateLive` after Task 8, so they route unchanged.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/router/types.go internal/router/filter.go internal/router/filter_test.go
git commit -m "feat(router): exclude retired models and flag guesses"
```

---

### Task 22: The inferred-capability warning reaches the request log

**Files:**
- Modify: `internal/exec/exec.go`
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `router.Candidate.Inferred` (Task 21).
- Produces: nothing new. An `ir.Warning` with field `capabilities` appears on `rec.Warnings`, and therefore in `requests.warnings_json`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4
**Approach:** inline - skip 2: `runAttempt` already accumulates `[]ir.Warning` from `BuildRequest` and hands them to `warningStrings`.

Without this the admit-inferred rule is invisible. A request needing tools routes to a model nobody has checked, the provider rejects it, and the trace shows a provider error with no hint that Darkrouter knowingly took the chance. Spec §6 requires the trace to explain it.

**Verify this end to end, not only in a unit test.** Phase 4 shipped roughly 250 passing unit tests over a warning mechanism that had a silent-loss bug, and querying `requests.warnings_json` after a live request is what found it. Task 26 does exactly that.

- [ ] **Step 1: Write the failing test**

Add to `internal/exec/exec_test.go`:

```go
func TestInferredCandidateProducesAWarning(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "local", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
		Source:   catalog.SourceInferred,
	}}, []string{"p"}))

	rec := &recordingLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [local]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Catalog: cat, Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{
	  "model":"local",
	  "messages":[{"role":"user","content":"hi"}],
	  "tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}]
	}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	got := rec.last()
	if got == nil {
		t.Fatal("nothing was logged")
	}
	var found bool
	for _, s := range got.Warnings {
		if strings.Contains(s, "capabilities") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v; the inferred-capability warning did not reach the record", got.Warnings)
	}
}

func TestNoInferredWarningWhenNothingWasNeeded(t *testing.T) {
	// A plain chat request against an inferred model needs no capability, so
	// warning about it would be noise that trains people to ignore warnings.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "local", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM}, Source: catalog.SourceInferred,
	}}, []string{"p"}))

	rec := &recordingLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [local]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Catalog: cat, Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"local","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	for _, s := range rec.last().Warnings {
		if strings.Contains(s, "capabilities") {
			t.Errorf("warned about capabilities nothing asked for: %v", rec.last().Warnings)
		}
	}
}
```

These use `executorFor` from Task 16. `recordingLogger` is a `Deps.Log` double that keeps the last record; if `internal/exec/exec_test.go` does not already have an equivalent, add:

```go
type recordingLogger struct {
	mu   sync.Mutex
	recs []*store.RequestRecord
}

func (l *recordingLogger) Log(r *store.RequestRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recs = append(l.recs, r)
}

func (l *recordingLogger) last() *store.RequestRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.recs) == 0 {
		return nil
	}
	return l.recs[len(l.recs)-1]
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestInferredCandidate|TestNoInferredWarning' -v
```

Expected: `TestInferredCandidateProducesAWarning` fails — no warning containing `capabilities` is on the record.

- [ ] **Step 3: Emit the warning**

In `internal/exec/exec.go`, in the per-attempt function, right after the target is built and before `BuildRequest`'s warnings are collected:

```go
	if w, ok := inferredWarning(c, req); ok {
		warns = append(warns, w)
	}
```

Placement matters: it must be inside the attempt, so the warning describes the candidate that actually served rather than the first one considered. Append it to the same `warns` slice `BuildRequest` contributes to, so it travels the existing path onto `rec.Warnings` and into `requests.warnings_json` — adding a second channel is how the mechanism grows a hole.

Add the helper beside `modelInfo`:

```go
// inferredWarning records that a candidate was admitted on guessed capability
// metadata for a request that actually needed a capability.
//
// Master design §6.4 admits these rather than excluding them, because
// hard-filtering on a guess would make every discovered local model refuse the
// tool requests Claude Code always sends. The cost is that a provider's own
// rejection looks like a Darkrouter failure, and this is what makes the trace
// say otherwise.
func inferredWarning(c router.Candidate, req *ir.Request) (ir.Warning, bool) {
	if !c.Inferred {
		return ir.Warning{}, false
	}
	needs := req.Needs()
	var missing []string
	if needs.Tools {
		missing = append(missing, "tools")
	}
	if needs.Vision {
		missing = append(missing, "vision")
	}
	if needs.Reasoning {
		missing = append(missing, "reasoning")
	}
	if len(missing) == 0 {
		// Warning about a plain chat request would be noise, and noise is what
		// trains people to ignore warnings.
		return ir.Warning{}, false
	}
	return ir.Warning{
		Field:  "capabilities",
		Target: c.ProviderID + "/" + c.Model,
		Reason: "the request needs " + strings.Join(missing, ", ") +
			" and this model's capabilities are unverified; routed anyway",
	}, true
}
```

Add `"strings"` to the file's imports if it is not already there.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -race -count=1 -v
```

Expected: PASS, every test in the package.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/exec/exec.go internal/exec/exec_test.go
git commit -m "feat(exec): warn when routing on guessed capabilities"
```

---

### Task 23: The client-facing listings read the catalog

**Files:**
- Modify: `internal/server/server.go` (`handleModels`, `handleGeminiModels`)
- Modify: `internal/edge/gemini/list.go`
- Test: `internal/edge/gemini/list_test.go`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `Server.Catalog()` (Task 17), `catalog.Snapshot.Search` (Task 9), `config.Config.Aliases`.
- Produces: `gemini.ListModels([]gemini.ModelEntry) map[string]any` — a **changed signature**, from `[]string`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5
**Approach:** inline - skip 2: both handlers exist and spec §9 states exactly what changes about them.

Spec §9: rewire both routes from Phase 1's provider-declared union to the catalog, **preserving Phase 1's behavior of listing configured aliases first**. `removed_upstream` models are excluded; `stale` ones are included.

Aliases first is not cosmetic. A client with a model picker shows the list in order, and the aliases are the names the operator chose for this gateway — burying them under two hundred discovered ids makes the gateway look like somebody else's catalog.

The Gemini listing also stops omitting token limits. `list.go`'s comment says they are omitted "because the catalog does not know them until Phase 6", and a client reading a zero limit refuses to send anything at all — so the fields stay absent when the catalog is still silent, and appear when it is not.

- [ ] **Step 1: Write the failing test**

Add to `internal/edge/gemini/list_test.go`:

```go
func TestListModelsCarriesLimitsWhenKnown(t *testing.T) {
	out := ListModels([]ModelEntry{
		{ID: "gemini-2.5-pro", ContextWindow: 1048576, MaxOutputTokens: 65536},
		{ID: "mystery"},
	})
	models, ok := out["models"].([]any)
	if !ok || len(models) != 2 {
		t.Fatalf("models = %v", out["models"])
	}

	first := models[0].(map[string]any)
	if first["name"] != "models/gemini-2.5-pro" || first["baseModelId"] != "gemini-2.5-pro" {
		t.Errorf("first = %v", first)
	}
	if first["inputTokenLimit"] != 1048576 || first["outputTokenLimit"] != 65536 {
		t.Errorf("limits = (%v, %v)", first["inputTokenLimit"], first["outputTokenLimit"])
	}

	// Absent rather than zero: a client reading a zero limit refuses to send
	// anything at all.
	second := models[1].(map[string]any)
	if _, present := second["inputTokenLimit"]; present {
		t.Errorf("an unknown limit was emitted as %v", second["inputTokenLimit"])
	}
	if _, present := second["outputTokenLimit"]; present {
		t.Errorf("an unknown output limit was emitted as %v", second["outputTokenLimit"])
	}
	if second["supportedGenerationMethods"] == nil {
		t.Error("supportedGenerationMethods missing; clients filter on it and would show nothing")
	}
}
```

Add to `internal/server/server_test.go`:

```go
func TestModelsListsAliasesFirstThenTheCatalog(t *testing.T) {
	srv := serverWithCatalog(t, `
server:
  proxy_listen: "127.0.0.1:0"
  admin_listen: "127.0.0.1:0"
aliases:
  fast: [p/quick]
  smart: [p/deep]
`, []catalog.Model{
		{ProviderID: "p", ModelID: "deep", State: catalog.StateLive, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "p", ModelID: "quick", State: catalog.StateLive, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "p", ModelID: "retired", State: catalog.StateRemovedUpstream, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "p", ModelID: "flaky", State: catalog.StateStale, Surfaces: []ir.Surface{ir.SurfaceLLM}},
	})

	w := httptest.NewRecorder()
	srv.ProxyHandler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var out struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "list" {
		t.Errorf("object = %q", out.Object)
	}

	ids := make([]string, len(out.Data))
	for i, d := range out.Data {
		ids[i] = d.ID
	}
	// Aliases first, in configuration order after sorting for determinism, and
	// before anything the catalog discovered.
	if len(ids) < 2 || ids[0] != "fast" || ids[1] != "smart" {
		t.Errorf("ids = %v; aliases are not listed first", ids)
	}
	has := func(want string) bool {
		for _, id := range ids {
			if id == want {
				return true
			}
		}
		return false
	}
	if !has("quick") || !has("deep") {
		t.Errorf("ids = %v; a live model is missing", ids)
	}
	// Stale stays listed, retired does not.
	if !has("flaky") {
		t.Errorf("ids = %v; a stale model was excluded", ids)
	}
	if has("retired") {
		t.Errorf("ids = %v; a removed_upstream model was listed", ids)
	}
}

func TestGeminiModelsReadsTheCatalog(t *testing.T) {
	srv := serverWithCatalog(t, `
server:
  proxy_listen: "127.0.0.1:0"
  admin_listen: "127.0.0.1:0"
`, []catalog.Model{
		{ProviderID: "p", ModelID: "gemini-2.5-pro", State: catalog.StateLive,
			Surfaces: []ir.Surface{ir.SurfaceLLM}, ContextWindow: 1048576, MaxOutputTokens: 65536},
		{ProviderID: "p", ModelID: "retired", State: catalog.StateRemovedUpstream,
			Surfaces: []ir.Surface{ir.SurfaceLLM}},
	})

	w := httptest.NewRecorder()
	srv.ProxyHandler().ServeHTTP(w, httptest.NewRequest("GET", "/v1beta/models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "models/gemini-2.5-pro") {
		t.Errorf("body = %s", body)
	}
	if !strings.Contains(body, "1048576") {
		t.Errorf("token limits missing from %s", body)
	}
	if strings.Contains(body, "retired") {
		t.Errorf("a removed_upstream model was listed: %s", body)
	}
}
```

`serverWithCatalog` builds a server from a configuration body and installs a fixed snapshot, on top of Task 17's `serverFixtureWith`:

```go
// serverWithCatalog builds a server and pins its catalog, so the listing tests
// do not depend on discovery having run.
func serverWithCatalog(t *testing.T, body string, models []catalog.Model) *Server {
	t.Helper()
	db, key, cfgStore := serverFixtureWith(t, body)
	srv, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.Catalog().Set(catalog.NewSnapshot(models, []string{"p"}))
	return srv
}
```

The configuration bodies above declare no providers, so nothing is imported and the snapshot is the only source of models — which is what these tests want. `internal/server/server_test.go` needs `catalog` and `ir` in its imports.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/gemini/ ./internal/server/ -run 'TestListModels|TestModelsListsAliases|TestGeminiModelsReads' -v
```

Expected: FAIL to build — `undefined: ModelEntry` and `cannot use []ModelEntry as []string`.

- [ ] **Step 3: Change the Gemini listing**

Rewrite `internal/edge/gemini/list.go`:

```go
package gemini

// generationMethods is what Darkrouter serves for every chat model. Clients
// filter on this list, and one that omits it shows no models at all.
var generationMethods = []string{"generateContent", "streamGenerateContent", "countTokens"}

// ModelEntry is one model to list. Zero limits mean the catalog does not know
// them.
type ModelEntry struct {
	ID              string
	ContextWindow   int
	MaxOutputTokens int
}

// ListModels renders Gemini's listing shape.
//
// A limit the catalog does not know is omitted rather than zeroed: a client
// reading inputTokenLimit: 0 refuses to send anything at all.
func ListModels(models []ModelEntry) map[string]any {
	out := []any{}
	for _, m := range models {
		entry := map[string]any{
			"name":                       "models/" + m.ID,
			"baseModelId":                m.ID,
			"displayName":                m.ID,
			"supportedGenerationMethods": generationMethods,
		}
		if m.ContextWindow > 0 {
			entry["inputTokenLimit"] = m.ContextWindow
		}
		if m.MaxOutputTokens > 0 {
			entry["outputTokenLimit"] = m.MaxOutputTokens
		}
		out = append(out, entry)
	}
	return map[string]any{"models": out}
}
```

- [ ] **Step 4: Rewire both handlers**

In `internal/server/server.go`, replace `handleModels` and the Gemini one, and add the shared listing helper:

```go
// listedModels returns the models a client should see: the configured aliases
// first, then everything routable in the catalog.
//
// Aliases first is phase 1's behavior and spec §9 preserves it deliberately.
// They are the names the operator chose for this gateway; burying them under
// two hundred discovered ids makes it look like somebody else's catalog.
//
// Search excludes removed_upstream by default and keeps stale, which is the
// asymmetry spec §5.1 exists for.
func (s *Server) listedModels() []listedModel {
	seen := map[string]bool{}
	out := []listedModel{}

	aliases := make([]string, 0, len(s.store.Current().Aliases))
	for name := range s.store.Current().Aliases {
		aliases = append(aliases, name)
	}
	// Map iteration is random; a listing that reorders itself between two
	// requests looks broken in a client's model picker.
	sort.Strings(aliases)
	for _, name := range aliases {
		seen[name] = true
		out = append(out, listedModel{ID: name, OwnedBy: "darkrouter"})
	}

	for _, m := range s.cat.Snapshot().Search(catalog.Filter{Surface: ir.SurfaceLLM}) {
		if seen[m.ModelID] {
			continue
		}
		seen[m.ModelID] = true
		out = append(out, listedModel{
			ID: m.ModelID, OwnedBy: m.ProviderID,
			ContextWindow: m.ContextWindow, MaxOutputTokens: m.MaxOutputTokens,
		})
	}
	return out
}

type listedModel struct {
	ID              string
	OwnedBy         string
	ContextWindow   int
	MaxOutputTokens int
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	data := []any{}
	for _, m := range s.listedModels() {
		data = append(data, map[string]any{
			"id": m.ID, "object": "model", "owned_by": m.OwnedBy,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func (s *Server) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	entries := make([]geminiedge.ModelEntry, 0)
	for _, m := range s.listedModels() {
		entries = append(entries, geminiedge.ModelEntry{
			ID: m.ID, ContextWindow: m.ContextWindow, MaxOutputTokens: m.MaxOutputTokens,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(geminiedge.ListModels(entries))
}
```

Add `"sort"` and `"github.com/darkraise/darkrouter/internal/catalog"` to the imports if Task 17 did not already.

The old handlers read `s.src.Providers` and could fail; the catalog snapshot cannot, so the error branch and its `WriteError` call go away with them. Read the existing `handleGeminiModels` before replacing it — if it does anything else, such as applying the Gemini proxy token check, keep that.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/gemini/ ./internal/server/ -race -count=1 -v
```

Expected: PASS. A pre-existing test asserting the old provider-union behavior will fail; read it and move the expectation to the catalog, since that is the change spec §9 asks for.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go \
  internal/edge/gemini/list.go internal/edge/gemini/list_test.go
git commit -m "feat(server): list models from the catalog"
```

---

### Task 24: A removed preset degrades instead of breaking

**Files:**
- Create: `internal/catalog/orphan.go`
- Modify: `internal/server/server.go` (collect the warnings at startup)
- Test: `internal/catalog/orphan_test.go`

**Interfaces:**
- Consumes: `catalog.Presets` (Task 1), `provider.Provider.Preset` (Task 7).
- Produces: `catalog.OrphanedPresets(providers []provider.Provider, presets Presets) []string`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: spec §3.3 states the rule and the startup-warning channel already exists from Phase 1.

Spec §3.3: a binary upgrade can rename or remove a preset that `providers.preset` rows reference. On startup, such a provider **degrades to its stored `kind` and `base_url` with a warning naming the orphaned reference**, rather than failing to load.

Silent removal of a working provider on upgrade is the failure mode to avoid — and it is already almost impossible here, because `SQLSource` reads `kind` and `base_url` from the row rather than from the preset. The degradation is therefore free; what is missing is the **warning**, without which an operator loses the preset's quirks and surfaces on upgrade and has nothing to tell them.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/orphan_test.go`:

```go
package catalog

import (
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/provider"
)

func TestOrphanedPresetsNamesTheReference(t *testing.T) {
	got := OrphanedPresets([]provider.Provider{
		{ID: "p1", Preset: "groq", Kind: "openaicompat", BaseURL: "https://a"},
		{ID: "p2", Preset: "retired-vendor", Kind: "openaicompat", BaseURL: "https://b"},
	}, Presets{"groq": {Name: "Groq"}})

	if len(got) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(got), got)
	}
	// The operator has to be able to find both ends of the break.
	if !strings.Contains(got[0], "p2") || !strings.Contains(got[0], "retired-vendor") {
		t.Errorf("warning names neither the provider nor the preset: %q", got[0])
	}
	// And to know it is still serving.
	if !strings.Contains(got[0], "openaicompat") || !strings.Contains(got[0], "https://b") {
		t.Errorf("warning does not say what it degraded to: %q", got[0])
	}
}

func TestUncataloguedProvidersAreNotOrphans(t *testing.T) {
	// An uncatalogued provider is a base URL and a key with no preset at all.
	// Warning about it every startup would be noise on a supported setup.
	got := OrphanedPresets([]provider.Provider{
		{ID: "p", Preset: "", Kind: "openaicompat", BaseURL: "https://x"},
	}, Presets{})
	if len(got) != 0 {
		t.Errorf("warned about a provider with no preset: %v", got)
	}
}

func TestOrphanWarningsAreDeterministic(t *testing.T) {
	// Two startups on the same database must produce the same /healthz text,
	// or the warnings look like they are changing when nothing is.
	ps := []provider.Provider{
		{ID: "b", Preset: "gone", Kind: "openaicompat", BaseURL: "https://b"},
		{ID: "a", Preset: "gone", Kind: "openaicompat", BaseURL: "https://a"},
	}
	first := OrphanedPresets(ps, Presets{})
	second := OrphanedPresets(ps, Presets{})
	if len(first) != 2 {
		t.Fatalf("got %d warnings, want 2", len(first))
	}
	if !strings.Contains(first[0], "\"a\"") {
		t.Errorf("warnings are not sorted by provider id: %v", first)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("warning %d differs between runs: %q and %q", i, first[i], second[i])
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestOrphan|TestUncatalogued' -v
```

Expected: FAIL to build — `undefined: OrphanedPresets`.

- [ ] **Step 3: Write the check**

Create `internal/catalog/orphan.go`:

```go
package catalog

import (
	"fmt"
	"sort"

	"github.com/darkraise/darkrouter/internal/provider"
)

// OrphanedPresets reports providers whose preset this binary no longer ships.
//
// Spec §3.3: a binary upgrade can rename or remove a preset that provider rows
// reference. Such a provider degrades to its stored kind and base URL rather
// than failing to load — silently removing a working provider on upgrade is the
// failure mode to avoid.
//
// The degradation itself is free, because provider rows carry their own kind
// and base URL. What is not free is noticing: without this warning an operator
// loses the preset's quirks, surfaces and model traits on upgrade and has
// nothing to tell them why a request shape changed.
func OrphanedPresets(providers []provider.Provider, presets Presets) []string {
	var orphans []provider.Provider
	for _, p := range providers {
		if p.Preset == "" {
			continue // an uncatalogued provider references nothing
		}
		if _, ok := presets[p.Preset]; ok {
			continue
		}
		orphans = append(orphans, p)
	}
	// Sorted so two startups on the same database produce the same /healthz
	// text; unsorted, the warnings look like they are changing when nothing is.
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].ID < orphans[j].ID })

	out := make([]string, 0, len(orphans))
	for _, p := range orphans {
		out = append(out, fmt.Sprintf(
			"provider %q references preset %q, which this build no longer ships; "+
				"serving with its stored kind %q and base url %q, without the preset's "+
				"quirks, surfaces, or model traits",
			p.ID, p.Preset, p.Kind, p.BaseURL))
	}
	return out
}
```

- [ ] **Step 4: Collect the warnings at startup**

In `internal/server/server.go`, inside `New`, after `src.Reload` succeeds and before the catalog is built:

```go
	if ps, err := src.Providers(context.Background()); err == nil {
		startupWarnings = append(startupWarnings, catalog.OrphanedPresets(ps, catalog.Embedded())...)
	}
```

They reach `/healthz` through `s.warnings`, the same channel a restart-only config change uses, and `runServer` in `cmd/darkrouter/main.go` already logs everything on that slice at startup.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ ./internal/server/ -race -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog/orphan.go internal/catalog/orphan_test.go internal/server/server.go
git commit -m "feat(catalog): warn on orphaned preset references"
```

---

### Task 25: Correct the stale Phase 4 spec and record the phase

**Files:**
- Modify: `docs/superpowers/specs/2026-08-22-darkrouter-phase4-dialects.md` (§4.6 and §4.7)
- Modify: `docs/PROGRESS.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing. Documentation only.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 0 - risk 0 = 2
**Approach:** inline - skip 2: the replacement text is given verbatim below.

Phase 4's spec §4.6 is stale in two places. Both were confirmed against the live Anthropic documentation on 2026-08-22 while executing Phase 4, and both were corrected in the Phase 4 *plan* — but the spec itself still says the old thing, so the next reader of the spec is misled. Amending it is part of finishing the work Phase 4 started.

- [ ] **Step 1: Correct §4.6's mapping table**

In `docs/superpowers/specs/2026-08-22-darkrouter-phase4-dialects.md`, replace the three table rows:

```markdown
| `ResponseFormat.JSONSchema` | `response_format: {type:"json_schema", json_schema}` | `output_config: {format: {type:"json_schema", schema}}` | `responseMimeType: "application/json"` plus `responseSchema` |
| `Reasoning.Effort` | `reasoning_effort` | `thinking: {type:"adaptive"}` plus `output_config.effort`, or `thinking: {type:"enabled", budget_tokens}` — see below | `thinkingConfig.thinkingBudget` with `includeThoughts` |
| `Reasoning.Budget` | converted to the nearest `reasoning_effort` | `budget_tokens` directly where the generation accepts it, else banded to an effort | `thinkingBudget` directly |
```

- [ ] **Step 2: Replace the beta caveat with what is actually true**

Replace:

```markdown
Whether Anthropic's structured-output beta is GA must be checked at implementation time; the spec
assumes beta and drops with a warning where unavailable.
```

with:

```markdown
**Anthropic's structured output is generally available.** Verified against the live documentation on
2026-08-22: no beta header, and the schema lives under `output_config.format` rather than at the top
level. The earlier text here assumed a beta and told the implementer to re-check; it was wrong.

**Anthropic's extended thinking has split into two mutually exclusive per-generation shapes.**
`thinking: {type:"enabled", budget_tokens}` returns a 400 on Claude 4.7 and later;
`thinking: {type:"adaptive"}` with `output_config.effort` returns a 400 on Claude 4.5 and earlier.
Choosing the wrong one makes every reasoning request against that generation fail, so the shape is a
property of the model rather than of the request. Phase 4 read it off the model name because there
was no catalog; **phase 6 moves it onto the catalog entry** and deletes the name table.

The sampling rule follows the same split, and is narrower than "no sampling with thinking":

- On the generations that accept `budget_tokens`, `temperature` and `top_k` are rejected alongside
  thinking, but `top_p` survives **between 0.95 and 1**.
- On the newest generation — `opus-4-7`, `opus-4-8`, `opus-5`, `sonnet-5`, `fable-5` — any non-default
  sampling value is a 400 on **every** request, thinking or not.

models.dev cannot express the first distinction: it lists `effort` among `claude-opus-4-5`'s
`reasoning_options`, but that names `output_config.effort`, not adaptive thinking. The two are
different controls sharing a word, which is why phase 6 declares the thinking shape in preset data
rather than deriving it.
```

- [ ] **Step 3: Correct §4.7**

Replace:

```markdown
Its current requiredness should be re-confirmed during implementation.
```

with:

```markdown
Confirmed still required on 2026-08-22. From phase 6 the substitution is the model's real
`max_output_tokens` from the catalog rather than a constant, and a request asking for more than the
model can produce is clamped with a warning rather than forwarded to a 400.
```

- [ ] **Step 4: Update the progress document**

In `docs/PROGRESS.md`:

Set the phase table row for 6 to `| 6 — Catalog | ✅ | ✅ | **Merged to master.** 26 tasks, all done criteria met. |` and update "Last updated" to the merge date.

Remove these entries from "Carried forward into phase 5 and beyond", since they are now closed — and say where each went:

- **Capability filtering admits everything** — closed. Real capability data arrives from models.dev and from local runtimes; `removed_upstream` models are excluded outright, and inferred ones still route with a warning that now reaches `requests.warnings_json`.
- **The Anthropic model-generation table is a name heuristic** — closed. `traitsFor` and its `generations` table are deleted; traits are preset-declared data reaching the adapter on `adapter.Target.Info`.
- **The Anthropic `max_tokens` substitution is a constant** — closed. It is the catalog's `max_output_tokens`, and an over-large client ask is clamped with a warning.
- **`xlate.EffortBudget`'s clamp is inert** — closed. Callers pass `t.Info.MaxOutputTokens`.

Move the two "Two spec assumptions that were stale" entries into a closed note saying the Phase 4 spec was amended in this phase.

Add a "Carried forward from phase 6" section:

```markdown
## Carried forward from phase 6

- **`presets.yaml` is generated, and regenerating it needs the OmniRoute checkout.**
  `tools/presetgen` reads `/root/repositories-community/OmniRoute` and a models.dev
  snapshot. Corrections belong in `internal/catalog/presets.overrides.yaml`, which
  is re-applied on every run; editing `presets.yaml` directly is discarded by the
  next run.
- **The embedded metadata snapshot ages.** `internal/catalog/models_snapshot.json`
  was taken on 2026-08-22. It is only the cold-start fallback — the sync
  overwrites it within twelve hours of a networked start — but a long-lived
  offline install runs on those numbers indefinitely. Regenerate it when the
  binary ships.
- **Vertex and Bedrock have no discovery.** `ProbeFor` returns
  `ErrKindNotDiscoverable` for both, so their models come from presets and
  models.dev alone. Bedrock's two control-plane calls arrive with its adapter in
  phase 8; Vertex has no practical listing API and never will.
- **The models.dev join is best-effort.** 49 of the 203 shipped presets join a
  models.dev provider key by exact id; the rest carry `no_models_dev: true` and
  their models fall back to inferred capabilities. Adding a `models_dev_id` to
  `presets.overrides.yaml` is the fix, one provider at a time.
- **Discovery probes every enabled provider on every tick regardless of how
  static it is.** An `anthropic` provider's model list changes a few times a
  year and is probed ninety-six times a day. Harmless, but phase 7's settings
  screen is where a per-provider interval would belong.
- **Failed attempts still burn tokens invisibly.** `request_attempts` carries no
  usage columns, so tokens spent by failed pre-commit attempts never reach
  `usage_daily`. Untouched by phase 6.
```

- [ ] **Step 5: Document the catalog in the README**

Add a section to `README.md` after the configuration section:

```markdown
## The model catalog

Darkrouter ships a catalog of provider presets, so adding a known provider is a
name and a key rather than a base URL, an auth style, and a list of quirks. Three
sources merge into one index:

- **Presets** — shipped data: kind, base URL, auth style, surfaces, and known
  quirks per named upstream.
- **models.dev** — pricing, context windows, and capability flags, refreshed
  every twelve hours. A snapshot is embedded in the binary, so Darkrouter starts
  and serves with no outbound access to it.
- **Discovery** — each enabled provider's own model listing, probed every fifteen
  minutes and whenever a provider or credential changes.

A provider that times out does not lose its models: after three consecutive
failed probes they are marked stale and stay routable, because the circuit
breaker rather than the catalog is what avoids a broken provider. A model a
*successful* listing omits three times running is marked removed upstream and
stops being routable.

Both workers are optional. See the `catalog:` block in `darkrouter.example.yaml`.
```

- [ ] **Step 6: Verify nothing else references the corrected text**

```bash
rg -n 'structured-output beta|re-confirmed during implementation|assumes beta' docs/
```

Expected: no matches. A hit in another spec means the same stale claim was copied and needs the same correction.

- [ ] **Step 7: Commit**

```bash
git add docs/superpowers/specs/2026-08-22-darkrouter-phase4-dialects.md docs/PROGRESS.md README.md
git commit -m "docs: correct phase 4 spec and record phase 6"
```

---

### Task 26: End-to-end verification against a live provider

**Files:**
- Modify: `docs/PROGRESS.md` (record the results)

**Interfaces:**
- Consumes: everything.
- Produces: nothing.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: the commands and their expected outputs are given below; the judgment is in reading live output, which is why this is not scored 0 on spec completeness.

Two lessons from Phase 4, both of which caught real silent-loss bugs that roughly 250 passing unit tests had missed:

- **Read generated files rather than only running them.** A golden test that regenerates and compares passes trivially when both sides are wrong.
- **Query `requests.warnings_json` after a live request** to confirm the warning mechanism fired end to end, rather than only in a unit test.

Ports 8080 and 8081 belong to an unrelated application. Use 18080 and 18081, and never kill a process this task did not start.

- [ ] **Step 1: Read the generated preset file**

```bash
grep -c '^[a-z0-9-]*:$' internal/catalog/presets.yaml
python3 - <<'EOF'
import yaml
d = yaml.safe_load(open('internal/catalog/presets.yaml'))
print(len(d), 'presets')
kinds = {}
styles = {}
joined = 0
for i, p in d.items():
    kinds[p.get('kind')] = kinds.get(p.get('kind'), 0) + 1
    styles[p['auth']['style']] = styles.get(p['auth']['style'], 0) + 1
    if p.get('models_dev_id'):
        joined += 1
print('kinds:', kinds)
print('auth styles:', styles)
print('joined to models.dev:', joined)
print('anthropic traits:', len(d['anthropic'].get('model_traits', [])))
print('groq:', d['groq'])
EOF
```

Expected: a little over 200 presets; `kinds` dominated by `openaicompat` with a handful of `anthropic`, `gemini`, `bedrock` and `vertex`; sixteen Anthropic trait rules; Groq's `base_url` ending `/openai/v1` with no request path. A `kind` of `None` for any entry means the generator failed to map a `format` and the validator did not catch it.

- [ ] **Step 2: Build the static binary**

```bash
export PATH=$PATH:/usr/local/go/bin
CGO_ENABLED=0 go build -o /tmp/darkrouter-p6 ./cmd/darkrouter
ls -lh /tmp/darkrouter-p6
file /tmp/darkrouter-p6
```

Expected: a statically linked ELF. It should be roughly 30 MB — the embedded snapshot adds under a megabyte to Phase 4's ~29 MB. A jump of several megabytes means the untrimmed models.dev document was embedded by mistake.

- [ ] **Step 3: Start the gateway against Groq**

```bash
mkdir -p /tmp/p6 && cd /tmp/p6
set -a && . /root/repositories/darkrouter/.env && set +a
cat > darkrouter.yaml <<YAML
server:
  proxy_listen: "127.0.0.1:18080"
  admin_listen: "127.0.0.1:18081"
providers:
  - id: groq
    kind: openaicompat
    base_url: https://api.groq.com/openai/v1
    api_key: ${GROQ_KEY}
    models: [openai/gpt-oss-120b]
aliases:
  fast: [groq/openai/gpt-oss-120b]
YAML
DARKROUTER_MASTER_KEY=phase6-smoke /tmp/darkrouter-p6 -config darkrouter.yaml > server.log 2>&1 &
echo $! > server.pid
sleep 5
curl -sS http://127.0.0.1:18081/healthz | head -c 600; echo
```

`DARKROUTER_MASTER_KEY` is mandatory — the binary refuses to start without it from Phase 2 on. The provider row is seeded from this configuration by the first-run import, and its `preset` column will be empty, which is correct for a hand-written provider: the orphan check must stay silent for it.

- [ ] **Step 4: Confirm discovery actually ran**

Wait for the first sweep, which happens immediately on startup.

```bash
cd /tmp/p6
sleep 10
export PATH=$PATH:/usr/local/go/bin
sqlite3 darkrouter.db "SELECT model_id, state, missing_streak, capabilities_source FROM models ORDER BY model_id LIMIT 20;" 2>/dev/null \
  || python3 -c "
import sqlite3
c = sqlite3.connect('darkrouter.db')
for r in c.execute('SELECT model_id, state, missing_streak, capabilities_source FROM models ORDER BY model_id LIMIT 20'):
    print(r)
print('---')
for r in c.execute('SELECT provider_id, consecutive_failures, last_error FROM provider_discovery'):
    print(r)
"
```

Expected: many more models than the one the configuration named, every one `live` with `missing_streak` 0, and `provider_discovery` reporting zero consecutive failures. Groq serves dozens of models; if only `openai/gpt-oss-120b` is present, discovery never ran or its probe failed — read `server.log`.

- [ ] **Step 5: Confirm the sync populated real prices**

```bash
cd /tmp/p6
python3 -c "
import sqlite3
c = sqlite3.connect('darkrouter.db')
rows = list(c.execute('''
  SELECT model_id, context_window, max_output_tokens,
         input_price_micros_per_mtok, output_price_micros_per_mtok, capabilities_source
    FROM models
   WHERE input_price_micros_per_mtok IS NOT NULL
   ORDER BY model_id'''))
print(len(rows), 'priced models')
for r in rows[:10]: print(r)
"
```

Expected: a double-digit count, with prices in the hundreds of thousands of micro-dollars, non-null context windows, and `capabilities_source` of `models_dev`. A price of exactly `0` on a commercial model means the per-million conversion is wrong; an empty result means the join failed — check that the `groq` provider row's `preset` is set, or that the `groq` preset carries `models_dev_id: groq`.

Note the provider row created from YAML has an empty `preset`, so the join will **not** fire for it. Set it and restart to exercise the join:

```bash
cd /tmp/p6
python3 -c "
import sqlite3
c = sqlite3.connect('darkrouter.db')
c.execute(\"UPDATE providers SET preset = 'groq' WHERE id = 'groq'\")
c.commit()
"
kill "$(cat server.pid)"; sleep 2
set -a && . /root/repositories/darkrouter/.env && set +a
DARKROUTER_MASTER_KEY=phase6-smoke /tmp/darkrouter-p6 -config darkrouter.yaml > server2.log 2>&1 &
echo $! > server.pid
sleep 20
```

Then re-run the price query. It must now return rows. This is the whole metadata path — preset, join, sync, write — proven end to end rather than in a fixture.

- [ ] **Step 6: Confirm the listing lists aliases first and carries limits**

```bash
curl -sS http://127.0.0.1:18080/v1/models | python3 -m json.tool | head -30
curl -sS http://127.0.0.1:18080/v1beta/models | python3 -m json.tool | head -30
```

Expected: `/v1/models` opens with `"id": "fast"` and `"owned_by": "darkrouter"`, then the discovered models. `/v1beta/models` shows `models/…` names with `inputTokenLimit` present on models the sync priced and **absent** on the rest — not zero.

- [ ] **Step 7: Make a live request and read `warnings_json`**

This is the step that catches a warning mechanism that works in tests and not in production.

```bash
cd /tmp/p6
curl -sS http://127.0.0.1:18080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"fast","messages":[{"role":"user","content":"Say OK."}],
       "tools":[{"type":"function","function":{"name":"noop","parameters":{"type":"object"}}}]}' \
  | head -c 400; echo
sleep 2
python3 -c "
import sqlite3, json
c = sqlite3.connect('darkrouter.db')
for rid, model, status, w in c.execute(
    'SELECT id, requested_model, status, warnings_json FROM requests ORDER BY ts DESC LIMIT 3'):
    print(rid, model, status, json.loads(w) if w else None)
"
```

Expected: the request succeeds, and its row's `warnings_json` is either `[]` or a list. **Which** is the interesting part:

- If the sync priced `openai/gpt-oss-120b` and models.dev reports it as tool-capable, the candidate is not inferred and there is **no** capability warning. Correct.
- If that model did not join models.dev, the candidate **is** inferred, the request needed tools, and a warning naming `capabilities` must be present.

Determine which case applies by querying that model's `capabilities_source` first, then assert the matching outcome. A missing warning in the inferred case is a real silent-loss bug of exactly the kind Phase 4 shipped and this step exists to find. Record which case you observed.

- [ ] **Step 8: Confirm it starts with no access to models.dev**

The done criterion, tested rather than assumed.

```bash
cd /tmp/p6
kill "$(cat server.pid)"; sleep 2
rm -f darkrouter.db
sed -i 's|^aliases:|catalog:\n  models_dev_url: http://127.0.0.1:1/api.json\n  discovery:\n    enabled: false\naliases:|' darkrouter.yaml
set -a && . /root/repositories/darkrouter/.env && set +a
DARKROUTER_MASTER_KEY=phase6-smoke /tmp/darkrouter-p6 -config darkrouter.yaml > server3.log 2>&1 &
echo $! > server.pid
sleep 8
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18081/readyz
curl -sS http://127.0.0.1:18080/v1/models | head -c 200; echo
grep -i 'models.dev' server3.log | head -3
curl -sS http://127.0.0.1:18080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"fast","messages":[{"role":"user","content":"Say OK."}]}' | head -c 200; echo
```

Expected: `readyz` returns 200, the listing serves, the log carries one warning naming models.dev and saying embedded metadata is being served, and a real completion comes back. A startup failure here fails the phase's done criterion outright.

- [ ] **Step 9: Stop everything this task started**

```bash
cd /tmp/p6
pkill -P "$(cat server.pid)" 2>/dev/null
kill "$(cat server.pid)" 2>/dev/null
sleep 2
ps -o pid,cmd -p "$(cat server.pid)" 2>/dev/null || echo "stopped"
rm -f server.pid
```

Confirm nothing this task started is still running. Do not kill anything on 8080 or 8081.

- [ ] **Step 10: Full suite one more time**

```bash
cd /root/repositories/darkrouter
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l . && CGO_ENABLED=0 go build ./...
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing, the static build succeeds.

- [ ] **Step 11: Record the results**

Add a numbered section to `docs/PROGRESS.md`'s "Open items" recording, with real numbers rather than "verified": how many models discovery found, how many the sync priced, which `warnings_json` case Step 7 produced and why, and that Step 8's offline start served a live completion.

- [ ] **Step 12: Commit**

```bash
git add docs/PROGRESS.md
git commit -m "docs: record phase 6 verification results"
```

---
