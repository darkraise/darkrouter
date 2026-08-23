# Darkrouter Phase 5 — Auxiliary Surfaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Serve the six non-chat surfaces — embeddings, images, speech, transcriptions, rerank, moderations — plus the chat-shaped Responses API through the same router, executor, health model, and request log as chat, so a failure on any of them fails over exactly as a chat request does.

**Architecture:** The attempt loop is the asset worth reusing and the only place Phase 3's commit rule may live, so it is extracted rather than duplicated. `runAttempts` loses its two chat-typed parameters and takes a `SurfaceOp` — four methods, split at the two joints that are genuinely surface-specific: `Build` renders the outbound request, and `Respond` turns a 2xx into client bytes. Everything between them stays in the loop, because it is surface-invariant and is exactly where Phase 3's subtle bugs were fixed: `client.Do`, cancel-cause classification, the 400-body reclassification through `BodyClassifier`, `Retry-After` capture before the body closes, attempt records, and health signals. Chat becomes the first implementation and its behavior is unchanged; each auxiliary surface becomes another. The narrow per-surface IR types stay narrow: forcing a six-field embedding call through the content-block message model would obscure both shapes and buy nothing.

**Tech Stack:** Go 1.26.1, the Phase 1–6 dependencies. No new modules. `CGO_ENABLED=0` for the shipped binary; `CGO_ENABLED=1` locally so `-race` works.

**Spec:** `docs/superpowers/specs/2026-08-22-darkrouter-phase5-auxiliary-surfaces.md` (master design: `docs/superpowers/specs/2026-08-22-darkrouter-design.md`; the design wins wherever they disagree — and in this phase it does, see below).

## Global Constraints

- Go 1.26. Module path `github.com/darkraise/darkrouter`. The shipped binary builds with `CGO_ENABLED=0`.
- English only in code, comments, commits, and errors.
- Commits are `<type>(<scope>): <subject>`, subject at most 50 characters, imperative, no trailing period.
- **Every task ends green.** `export PATH=$PATH:/usr/local/go/bin` first; the toolchain is not on `PATH`. Run `go test ./... -race -count=1`, `go vet ./...`, and `gofmt -l .` before committing. `gofmt -l .` must print nothing.
- **Ports 8080 and 8081 are occupied by an unrelated application.** Every smoke run binds 18080 (proxy) and 18081 (admin). Never kill a process this plan did not start, and kill by binary name — `ps -C darkrouter -o pid=` — because `nohup … &` inside a compound command returns the subshell's pid, not the binary's.
- **`DARKROUTER_MASTER_KEY` must be set for any run of the binary**, including smoke tests. A throwaway value is fine.
- **Chat behavior must not change.** The regression net for the `runAttempts` extraction is **`internal/exec`'s own suite**, not the golden suite — `internal/golden` never imports `exec`; it tests translators. Task 3 closes the two gaps in that suite which sit directly on the refactor's seam, and it runs *before* any code moves.
- **Every surface routes through the same loop.** Budget gate, live health re-check, credential rotation, commit semantics, attempt records, and the request log are inherited, never reimplemented per surface. A surface that needs different behavior states why in a comment.
- **Once the first byte reaches the client, no re-route.** Phase 3's rule is unchanged and applies to binary surfaces too, where its consequence is sharper: a truncated audio body cannot be failed over and there is no in-stream error vocabulary to signal it. Byte counts are logged so the trace shows the truncation the client could not be warned about.
- **The op detects commit; the loop enforces it.** Only the op knows what "content-bearing" means for its wire format — a delta event for chat SSE, the first body byte for binary, a completed parse for unary. But the authoritative fact is the loop's: it wraps the `http.ResponseWriter` and, after `Respond` returns, consults the wrapper rather than the op's word. A surface that reports success after bytes have gone out cannot restart the chain.
- **A surface no configured provider offers returns the distinguishable no-provider error** without attempting anything, exactly as an unknown model does.
- Every new package gets a package comment. Comments explain why, never what.

## The surface vocabulary is wrong in the code, and this phase is where it matters

Master design §6 fixes **seven** surfaces: `llm`, `embedding`, `image`, `tts`, `stt`, `rerank`, `moderation`. The Phase 5 spec's §4 adapter matrix uses the same seven, with separate `tts` and `stt` rows.

`internal/ir/ir.go` has shipped **six** since Phase 1: `llm`, `embeddings`, `images`, `audio`, `rerank`, `moderations` — plural, with speech and transcription collapsed into one `audio`.

The master design wins, per the specs' own precedence rule, and the split is substantive rather than cosmetic: a provider can serve `/v1/audio/speech` without serving `/v1/audio/transcriptions`, and one `audio` surface cannot express that. The §4 matrix would be unimplementable as written.

Correcting it is cheap **now** and expensive later:

- The plural constants exist only as declarations in `ir.go`. Nothing in the request path consumes any value but `SurfaceLLM`.
- `llm` is spelled identically in both vocabularies, and every `models.surfaces` row a running gateway holds is `["llm"]`, so no stored data changes.
- The only other occurrences are seven lines of `internal/catalog/presets.overrides.yaml`, which Task 2 rewrites and regenerates.

Task 1 does this first, before anything depends on the wrong names.

## What "surface metadata from phase 6" actually turned out to be

The spec's dependency line says Phase 5 has a soft dependency on Phase 6 "for surface metadata", and that before Phase 6 "every model's surfaces come from its preset declaration". Verified on 2026-08-23: **they come from the preset declaration afterwards too.** models.dev cannot supply them.

- `text-embedding-3-small` reports `modalities.output: ["text"]` — byte-identical to a chat model. An embedding model is not distinguishable.
- `whisper-1`, `dall-e-3` and `tts-1` are absent from models.dev's `openai` entry entirely.

So surfaces are preset-declared, permanently. Today that means 189 of the 196 shipped presets declare `llm` only:

| Surface | Presets declaring it before this phase |
|---|---|
| llm | 196 |
| embeddings | 7 — cohere, gemini, lmstudio, mistral, ollama, openai, vertex |
| rerank | 1 — cohere |
| images, audio, moderations | 1 each — openai |

That is not a defect: `tools/presetgen` cannot know, and the hand-written overrides carry the rest. But it means **Phase 5 includes a data task**, not only route wiring — Task 2 widens the declarations for the providers whose auxiliary surfaces are known, and the router honestly reports no provider for the rest.

## A phase 6 defect this phase would otherwise have tripped over — already fixed

`merge.surfaces` resolved override → row → preset. But discovery hardcodes `'["llm"]'` into every row
it inserts (`internal/store/catalog_lifecycle.go:97`) and never updates the column, and the models.dev
sync echoes that value straight back (`internal/catalog/sync.go:163`). The row therefore **always**
shadowed the preset: widening a preset had no effect on any discovered model, an embedding request
against a provider whose discovery works was skipped as `SkipSurface`, and the client got "no provider
offers this" — on precisely the providers this phase targets.

Task 2's widening would have gone green regardless, because its tests only exercise `LoadPresets`, and
failed at runtime on the phase's own done criterion.

Fixed before this plan continues: the preset now outranks the row, on the grounds that the row's
surfaces carry no information — discovery writes a constant and the sync echoes it — while an operator
override still wins and remains the only per-model source that will ever carry intent. A future writer
that genuinely learns a model's surfaces should write to `model_overrides`, which has no writer at all
until phase 7.

## The rerank shape is settled

Findings ledger O1 applied Cohere v2 provisionally and said to revisit "if the actual provider mix argues for Jina's or Voyage's shape". The shipped mix answers it: exactly one preset declares a `rerank` surface — `cohere` — and neither Jina nor Voyage is a preset at all. Cohere v2 is not merely the recommendation; it is the only shape any shipped provider serves. **No revisit is needed and none is planned.**

## File Structure

| Path | Responsibility |
|---|---|
| `internal/ir/ir.go` | The seven-surface vocabulary; `ParseSurface`; `ErrPayloadTooLarge`; `Request.Warnings` |
| `internal/ir/aux.go` | The narrow per-surface IR types: embedding, image, speech, rerank, moderation |
| `internal/adapter/adapter.go` | `SurfaceSet`, `Adapter.Surfaces()`, `Target.RerankPath`, and the six optional per-surface interfaces |
| `internal/exec/surface.go` | `SurfaceOp`, `chatOp`, `RunSurface`, `RunAux` |
| `internal/exec/commitwriter.go` | `CommitWriter`: the loop's own answer to "did bytes go out" |
| `internal/exec/resolve.go` | The request prologue shared by every route |
| `internal/exec/aux.go` | The generic auxiliary scaffold, `DecodeJSON`, `ReadCapped` |
| `internal/exec/multipart.go` | Buffering and in-form model rewriting for transcriptions |
| `internal/exec/embed.go` | The embedding op, its route, and spec §8's cross-model warning |
| `internal/exec/moderation.go` | The moderation op and route |
| `internal/exec/rerank.go` | The rerank op and route |
| `internal/exec/image.go` | The image op and route |
| `internal/exec/transcription.go` | The stt op, its route, and `copyFlushing` |
| `internal/exec/speech.go` | The tts op and route: binary through, never held |
| `internal/adapter/openaicompat/embed.go` | Embeddings against an OpenAI-compatible upstream |
| `internal/adapter/openaicompat/moderation.go` | Moderations |
| `internal/adapter/openaicompat/rerank.go` | Cohere v2 rerank, at the preset-declared path |
| `internal/adapter/openaicompat/image.go` | Image generation |
| `internal/adapter/openaicompat/audio.go` | Transcriptions: the rendered form onto the wire |
| `internal/adapter/openaicompat/speech.go` | Speech |
| `internal/edge/edge.go` | The five auxiliary inbound dialect interfaces |
| `internal/edge/openai/aux.go` | Parsing and writing the six auxiliary shapes |
| `internal/edge/openai/responses.go` | The Responses request, its refusals, and the item body |
| `internal/edge/openai/responses_stream.go` | The Responses semantic stream writer |
| `internal/edge/openai/dialect.go` | `ResponsesDialect`, the fourth `edge.Dialect` |
| `internal/catalog/presets.overrides.yaml` | Widened surface declarations |
| `internal/server/server.go` | The seven new routes |
| `internal/store/log.go` | `SurfaceMeta`, `ResponseBytes`, `ResponseContentType` |
| `internal/store/migrations/0003_surfaces.sql` | The three columns those fields need |

## What this phase deliberately does not do

- **No batch, files, video, music, OCR, web search, or `/v1/audio/translations`.** Permanently out per master design §2. Whisper clients do call translations; its absence is deliberate.
- **No Imagen.** Gemini's image surface is out of scope for v1 per the §4 matrix, so `gemini` declares no `image`.
- **No resolvable Responses IDs.** Darkrouter cannot honor an echoed `previous_response_id`, so returned ids are marked non-resumable and any request carrying one is rejected.
- **No new config knob for embedding failover.** Spec §8 makes it a documented hazard with a warning, not a setting.

---

### Task 1: The seven-surface vocabulary

**Files:**
- Modify: `internal/ir/ir.go`
- Test: `internal/ir/ir_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ir.SurfaceEmbedding`, `ir.SurfaceImage`, `ir.SurfaceTTS`, `ir.SurfaceSTT`, `ir.SurfaceModeration` replacing the plural four; `ir.SurfaceLLM` and `ir.SurfaceRerank` unchanged; `ir.AllSurfaces() []Surface`. Every later task routes on these.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: master design §6 states the seven values verbatim and the constants already exist in this exact form.

Risk is 2 because this is a shared vocabulary every later task and the whole catalog read on. It is cheap **today** — nothing consumes any value but `SurfaceLLM`, and `llm` is spelled identically in both vocabularies, so no stored `models.surfaces` row changes — and it becomes expensive the moment a second surface is in use.

The substantive part is the split. `audio` cannot express a provider that serves `/v1/audio/speech` but not `/v1/audio/transcriptions`, and the spec's §4 matrix has separate `tts` and `stt` rows precisely because that provider exists. Keeping one `audio` surface would make the matrix unimplementable.

- [ ] **Step 1: Write the failing test**

Add to `internal/ir/ir_test.go`:

```go
func TestSurfaceVocabularyMatchesTheMasterDesign(t *testing.T) {
	// Master design §6 fixes these seven. Phase 1 shipped six, with speech and
	// transcription collapsed into one "audio" — which cannot express a
	// provider that serves one and not the other, and phase 5's adapter matrix
	// has a row for each.
	want := []Surface{
		SurfaceLLM, SurfaceEmbedding, SurfaceImage,
		SurfaceTTS, SurfaceSTT, SurfaceRerank, SurfaceModeration,
	}
	got := AllSurfaces()
	if len(got) != len(want) {
		t.Fatalf("AllSurfaces has %d entries, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllSurfaces()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSurfaceSpellings(t *testing.T) {
	// The exact strings are what presets declare and the models.surfaces
	// column stores, so they are data, not just identifiers.
	cases := map[Surface]string{
		SurfaceLLM:        "llm",
		SurfaceEmbedding:  "embedding",
		SurfaceImage:      "image",
		SurfaceTTS:        "tts",
		SurfaceSTT:        "stt",
		SurfaceRerank:     "rerank",
		SurfaceModeration: "moderation",
	}
	for s, want := range cases {
		if string(s) != want {
			t.Errorf("surface = %q, want %q", s, want)
		}
	}
}

func TestParseSurfaceAcceptsEveryValueAndNothingElse(t *testing.T) {
	for _, s := range AllSurfaces() {
		if got, ok := ParseSurface(string(s)); !ok || got != s {
			t.Errorf("ParseSurface(%q) = (%q, %v)", s, got, ok)
		}
	}
	// The retired plural spellings must not parse. A preset still carrying one
	// would otherwise be silently dropped by the merge and the model would
	// serve chat only, with nothing saying why.
	for _, bad := range []string{"embeddings", "images", "audio", "moderations", "chat", ""} {
		if _, ok := ParseSurface(bad); ok {
			t.Errorf("ParseSurface(%q) accepted a value that is not in the vocabulary", bad)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/ir/ -run TestSurface -v
```

Expected: FAIL to build — `undefined: SurfaceEmbedding`, `undefined: AllSurfaces`.

- [ ] **Step 3: Correct the vocabulary**

In `internal/ir/ir.go`, replace the constant block and `ParseSurface`:

```go
// Surface is the kind of work a request asks for. It lives here rather than in
// the router or the catalog because both need it and either import would
// create a cycle.
//
// The seven values are master design §6's, and the strings are data: presets
// declare them and the models.surfaces column stores them. tts and stt are
// separate because a provider can serve /v1/audio/speech without serving
// /v1/audio/transcriptions, and one "audio" surface cannot express that.
type Surface string

const (
	SurfaceLLM        Surface = "llm"
	SurfaceEmbedding  Surface = "embedding"
	SurfaceImage      Surface = "image"
	SurfaceTTS        Surface = "tts"
	SurfaceSTT        Surface = "stt"
	SurfaceRerank     Surface = "rerank"
	SurfaceModeration Surface = "moderation"
)

// AllSurfaces returns the vocabulary in master design order. Callers that
// enumerate surfaces — the adapter matrix test, the admin API — use this rather
// than repeating the list.
func AllSurfaces() []Surface {
	return []Surface{
		SurfaceLLM, SurfaceEmbedding, SurfaceImage,
		SurfaceTTS, SurfaceSTT, SurfaceRerank, SurfaceModeration,
	}
}

// ParseSurface converts a stored or inbound string. It reports failure rather
// than defaulting, because a request routed to the wrong surface fails in a
// much more confusing way than one refused up front.
func ParseSurface(s string) (Surface, bool) {
	for _, known := range AllSurfaces() {
		if Surface(s) == known {
			return known, true
		}
	}
	return "", false
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/ir/ -race -count=1 -v
```

Expected: PASS, three new tests plus the package's existing ones.

- [ ] **Step 5: Fix the compile breaks**

```bash
export PATH=$PATH:/usr/local/go/bin
go build ./... 2>&1 | head -20
```

Expected: breaks only where the retired names are referenced — `internal/catalog/snapshot_test.go`, `internal/catalog/merge_test.go`, `internal/server/server_test.go` and any other test using `ir.SurfaceEmbeddings`. Rename each to `ir.SurfaceEmbedding`. These are tests choosing an arbitrary second surface; none asserts the old spelling deliberately.

Do **not** touch `internal/store/migrations/0002_catalog.sql`. Its `'["llm"]'` default is correct in both vocabularies, and rewriting a shipped migration would change a schema other databases have already applied.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing. `internal/catalog`'s preset test still passes: it checks that a preset declares *some* surface, not that each one parses. Task 2 adds that check, and it is what turns the now-stale preset data into a visible failure.

- [ ] **Step 7: Commit**

```bash
git add internal/ir/ir.go internal/ir/ir_test.go internal/catalog internal/server
git commit -m "fix(ir): use the master design surface vocabulary"
```

---

### Task 2: Preset surface declarations, corrected and widened

**Files:**
- Modify: `internal/catalog/preset_test.go`
- Modify: `internal/catalog/presets.overrides.yaml`
- Modify: `internal/catalog/presets.yaml` (regenerated)

**Interfaces:**
- Consumes: `ir.ParseSurface` (Task 1).
- Produces: shipped presets declaring the corrected surfaces, and a preset test that rejects an unparseable one. Tasks 5 onward route against these.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: the generator and its overrides file both exist from phase 6, and the surface names are fixed by Task 1.

Two things happen here, and the order matters. The **guard** comes first: a test asserting every declared surface parses. Without it, Task 1's rename leaves seven preset lines spelling surfaces that `merge.parseSurfaces` silently drops — the model would serve chat only and nothing would say why. That is the exact failure mode phase 6's migration was written to remove, reintroduced in the data layer.

Then the **widening**. Phase 6 shipped 189 of 196 presets declaring `llm` only, because `tools/presetgen` cannot infer surfaces and models.dev cannot supply them. The providers below are the ones whose auxiliary surfaces are documented and verifiable; everything else keeps `llm` and the router honestly reports no provider.

- [ ] **Step 1: Write the failing test**

Add to `internal/catalog/preset_test.go`:

```go
func TestEveryDeclaredSurfaceParses(t *testing.T) {
	// merge.parseSurfaces drops what ir.ParseSurface rejects, so a preset
	// spelling a surface wrong loses it silently: the model serves chat only
	// and nothing reports the loss. This is the guard for that.
	ps, err := LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	for id, p := range ps {
		for _, s := range p.Surfaces {
			if _, ok := ir.ParseSurface(s); !ok {
				t.Errorf("%s: surface %q is not in the vocabulary", id, s)
			}
		}
	}
}

func TestAuxiliarySurfacesAreDeclaredSomewhere(t *testing.T) {
	// Phase 5's done criterion is that each of the seven routes reaches a real
	// provider. A surface no preset declares cannot, and the failure would be
	// a confusing "no provider offers this" at request time rather than here.
	ps, err := LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]int{}
	for _, p := range ps {
		for _, s := range p.Surfaces {
			declared[s]++
		}
	}
	for _, s := range ir.AllSurfaces() {
		if declared[string(s)] == 0 {
			t.Errorf("no shipped preset declares the %q surface", s)
		}
	}
}

func TestRerankPresetsDeclareAPath(t *testing.T) {
	// Spec §3.1: each preset declares its own rerank path, because providers
	// expose it at differing URLs. A rerank surface without one would build a
	// request against the chat path.
	ps, err := LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	for id, p := range ps {
		serves := false
		for _, s := range p.Surfaces {
			if s == string(ir.SurfaceRerank) {
				serves = true
			}
		}
		if !serves {
			continue
		}
		if _, ok := p.QuirkValue("rerank-path"); !ok {
			t.Errorf("%s declares the rerank surface with no rerank-path quirk", id)
		}
	}
}
```

Add `"github.com/darkraise/darkrouter/internal/ir"` to that file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -run 'TestEveryDeclaredSurface|TestAuxiliarySurfaces|TestRerankPresets' -v
```

Expected: `TestEveryDeclaredSurfaceParses` names seven offending entries spelling `embeddings`, `images`, `audio` or `moderations`; `TestAuxiliarySurfacesAreDeclaredSomewhere` reports `embedding`, `image`, `tts`, `stt` and `moderation` undeclared. `TestRerankPresetsDeclareAPath` passes already — `cohere` carries its quirk.

- [ ] **Step 3: Correct and widen the overrides**

In `internal/catalog/presets.overrides.yaml`, replace each `surfaces:` line below. The values are the corrected vocabulary, and the additions are providers whose auxiliary surfaces are documented.

```yaml
openai:
  models_dev_id: openai
  surfaces: [llm, embedding, image, tts, stt, moderation]
  quirks: [max-completion-tokens-name]
```

```yaml
gemini:
  models_dev_id: google
  surfaces: [llm, embedding]
```

```yaml
cohere:
  models_dev_id: cohere
  surfaces: [llm, embedding, rerank]
  quirks: [rerank-path=/v2/rerank]
```

```yaml
mistral:
  models_dev_id: mistral
  surfaces: [llm, embedding]
```

```yaml
groq:
  models_dev_id: groq
  free_tier: true
  # Groq serves /v1/audio/transcriptions (whisper-large-v3) and
  # /v1/audio/speech (the playai-tts voices) on the same OpenAI-compatible base
  # URL. It serves no embeddings, images, rerank or moderations endpoint, and
  # the router reports that honestly rather than being told otherwise here.
  surfaces: [llm, stt, tts]
```

```yaml
ollama:
  name: Ollama
  kind: openaicompat
  base_url: http://localhost:11434/v1
  auth:
    style: none
  surfaces: [llm, embedding]
  no_models_dev: true
  website: https://ollama.com
  capability_probe: ollama
  quirks: [usage-final-chunk-only]
```

```yaml
lmstudio:
  name: LM Studio
  kind: openaicompat
  base_url: http://localhost:1234/v1
  auth:
    style: none
  surfaces: [llm, embedding]
  no_models_dev: true
  website: https://lmstudio.ai
  quirks: []
```

```yaml
vertex:
  name: Google Vertex AI
  kind: vertex
  base_url: ""
  auth:
    style: gcp-sa
  surfaces: [llm, embedding]
  models_dev_id: google-vertex
  website: https://cloud.google.com/vertex-ai
  quirks: []
```

And add these entries, whose auxiliary surfaces are their whole purpose:

```yaml
voyage:
  name: Voyage AI
  kind: openaicompat
  base_url: https://api.voyageai.com/v1
  auth:
    style: bearer
  # Embeddings only. Voyage serves a rerank endpoint too, but its shape is not
  # Cohere v2, and spec §3.1 excludes a deviating provider from the surface
  # rather than special-casing it with quirks.
  surfaces: [embedding]
  no_models_dev: true
  website: https://docs.voyageai.com

deepinfra:
  models_dev_id: deepinfra
  surfaces: [llm, embedding]

fireworks:
  models_dev_id: fireworks-ai
  surfaces: [llm, embedding, image]
  model_aliases:
    accounts/fireworks/models/llama-v3p3-70b-instruct: llama-3.3-70b

together:
  models_dev_id: togetherai
  surfaces: [llm, embedding, image]
```

A preset may declare a surface with **no** `llm`, as `voyage` does. Nothing requires every provider to serve chat, and the router filters on the surface the request asked for.

- [ ] **Step 4: Regenerate**

```bash
export PATH=$PATH:/usr/local/go/bin
mkdir -p /tmp/presetgen
curl -fsS https://models.dev/api.json -o /tmp/presetgen/modelsdev.json
go run ./tools/presetgen \
  -omniroute /root/repositories-community/OmniRoute \
  -modelsdev /tmp/presetgen/modelsdev.json \
  -out-presets internal/catalog/presets.yaml \
  -out-snapshot internal/catalog/models_snapshot.json \
  -overrides internal/catalog/presets.overrides.yaml
```

Expected: a preset count near 196 and no error. If it reports overrides that "target presets the generator did not produce", the id is wrong — `deepinfra`, `fireworks` and `together` exist in the OmniRoute registry, `voyage` does not and is therefore written as a complete standalone entry above. Fix the id rather than padding the override.

The regenerated `models_snapshot.json` will differ from the committed one wherever models.dev has changed since 2026-08-22. That is expected and desirable; read the diff stat, and if it is enormous rather than incremental, stop and check the fetch succeeded.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package including the three new ones.

- [ ] **Step 6: Read the result rather than trusting the count**

```bash
python3 -c "
import yaml
from collections import Counter
d = yaml.safe_load(open('internal/catalog/presets.yaml'))
c = Counter(s for p in d.values() for s in p.get('surfaces', []))
print(len(d), 'presets;', dict(c))
for k in ('openai','cohere','voyage','gemini'):
    print(k, '->', d[k]['surfaces'])
"
```

Expected: every one of the seven surfaces has a non-zero count, `openai` carries six, `voyage` carries `['embedding']` alone, and the total preset count has grown by one — the standalone `voyage` entry.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/catalog/preset_test.go internal/catalog/presets.overrides.yaml \
  internal/catalog/presets.yaml internal/catalog/models_snapshot.json
git commit -m "feat(catalog): declare auxiliary surfaces on presets"
```

---

### Task 3: Pin the refactor's seam before anything moves

**Files:**
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing. Two regression tests, and no production change at all.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 0 - spec 0 - coupling 1 - risk 1 = 2
**Approach:** inline - skip 2: both behaviors already exist in `internal/exec/exec.go` and the task only asserts them.

This task moves no code. It exists because the two behaviors below are load-bearing, are currently untested, and sit exactly where the `runAttempts` extraction will cut. A refactor that breaks either would ship green.

`internal/golden` is **not** the net here — it never imports `exec`. `internal/exec`'s own suite is, and it is strong on idle handoff, deadline-cause classification and pre-commit replay. These are its two holes.

- [ ] **Step 1: Write both tests**

Add to `internal/exec/exec_test.go`:

```go
func TestEmptyStreamSucceedsWithoutFailover(t *testing.T) {
	// A 200 SSE that ends with no content-bearing event is a legitimate empty
	// completion, not a failure: exec.go flushes the buffer, succeeds, and does
	// not fail over. Nothing pinned that, and a refactor moving the
	// stream-ended-cleanly break across the op boundary would silently turn
	// every instantly-stopping model into a full-chain retry.
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// A well-formed stream carrying no delta.
		_, _ = w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	rec := &captureLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: a
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [m]
  - id: b
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [m]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("upstream called %d times; an empty stream must not fail over", got)
	}
	if got := rec.only(t); got.Status != "success" {
		t.Errorf("status = %q, want success", got.Status)
	}
	// The buffered events still reach the client.
	if !strings.Contains(w.Body.String(), "finish_reason") {
		t.Errorf("the buffered stream was not flushed: %q", w.Body.String())
	}
}

func TestAnAbandonedAttemptsWarningsDoNotReachTheRecord(t *testing.T) {
	// exec.go assigns warnings per served attempt rather than appending across
	// the chain. A loop-level accumulator is the natural refactor mistake, and
	// it would leak a dropped-field warning from an attempt nobody was served
	// into the record for the one they were.
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The first provider fails pre-commit; the second serves.
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	rec := &captureLogger{}
	// The anthropic adapter warns about a missing max_tokens; the openaicompat
	// one does not. Attempt 1 therefore produces a warning and is abandoned.
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: first
    kind: anthropic
    base_url: `+upstream.URL+`
    api_key: sk
    priority: 10
    models: [m]
  - id: second
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    priority: 1
    models: [m]
`, map[string]adapter.Adapter{
		"anthropic":    anthropicadapter.New(),
		"openaicompat": openaicompat.New(),
	}, Deps{Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	got := rec.only(t)
	if got.FinalProviderID != "second" {
		t.Fatalf("served by %q, want second", got.FinalProviderID)
	}
	for _, warn := range got.Warnings {
		if strings.Contains(warn, "max_tokens") {
			t.Errorf("warnings = %v; an abandoned attempt's warning reached the served record", got.Warnings)
		}
	}
}
```

The second test needs `anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"` and `"sync/atomic"` in the file's imports if they are not already there. Read the import block first — `executorFor` and `captureLogger` are from phase 6 and already present.

- [ ] **Step 2: Run both tests and confirm they PASS**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestEmptyStreamSucceeds|TestAnAbandonedAttempts' -race -count=1 -v
```

Expected: **PASS**, both. This is the one task in the plan whose tests are green on the first run — they describe behavior that already exists. A failure here means the behavior is not what this plan assumes, and the refactor's design must be revisited before Task 5 rather than after.

If `TestAnAbandonedAttemptsWarningsDoNotReachTheRecord` fails because the anthropic provider is not tried first, check the `priority` ordering took effect: `router.Resolve` orders by priority descending. If it fails because the anthropic adapter produced no `max_tokens` warning, the request already carried a cap — it must not.

- [ ] **Step 3: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 4: Run the two new tests repeatedly**

Both involve a two-provider chain and a real HTTP server.

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestEmptyStreamSucceeds|TestAnAbandonedAttempts' -race -count=5
```

Expected: `ok`, no flakes. A flake here means the provider ordering is not deterministic, which would make these useless as a regression net.

- [ ] **Step 5: Commit**

```bash
git add internal/exec/exec_test.go
git commit -m "test(exec): pin empty streams and warning scope"
```

---

### Task 4: Extract the request prologue

**Files:**
- Create: `internal/exec/resolve.go`
- Modify: `internal/exec/exec.go` (`Handle`)
- Modify: `internal/exec/count.go` (`HandleCount`)

**Interfaces:**
- Consumes: nothing new.
- Produces: `exec.errorWriter`, `exec.resolved`, and `(*Executor).resolve(...) (resolved, bool)`. Tasks 6 onward and all seven new routes go through it.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: both copies exist and the extraction is the intersection of two functions already in the tree.

The prologue — fetch providers, freeze the snapshot, resolve candidates, record the trace — is **already** duplicated between `Handle` (`internal/exec/exec.go`) and `HandleCount` (`internal/exec/count.go`), and seven new routes would make nine copies. Extracting it now is what keeps each new route a thin constructor rather than a third transcription of the same twenty lines.

The two copies differ in exactly three ways, and each becomes a parameter rather than a fork:

- `Handle` takes its surface from the passthrough; `HandleCount` hardcodes `ir.SurfaceLLM`. Both become the caller's `router.Query`.
- `Handle` records `Candidates` and `Skips` on the record; `HandleCount` discards the skips. Both record — dropping the trace was an omission, not a decision, and a count that routes to nothing should be as diagnosable as a chat request that does.
- `Handle` sets `X-Darkrouter-Attempts`; `HandleCount` does not. That stays in the callers, because a count that never attempts should not claim it did.

- [ ] **Step 1: Write the failing test**

Add to `internal/exec/exec_test.go`:

```go
func TestResolveRecordsTheTraceForEveryRoute(t *testing.T) {
	// HandleCount discarded its skips, so a count request that routed to
	// nothing was undiagnosable. Sharing the prologue fixes that as a side
	// effect, and this is what pins it.
	upstream := unaryUpstream()
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "chat-only", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}}, []string{"p"}))

	rec := &captureLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: anthropic
    base_url: `+upstream.URL+`
    api_key: sk
    models: [chat-only]
`, map[string]adapter.Adapter{"anthropic": anthropicadapter.New()},
		Deps{Catalog: cat, Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/messages/count_tokens",
		strings.NewReader(`{"model":"nonexistent","messages":[{"role":"user","content":"hi"}]}`))
	e.HandleCount(w, r, anthropicedge.New(), "anthropic")

	got := rec.only(t)
	if got.RequestedModel != "nonexistent" {
		t.Errorf("requested model = %q", got.RequestedModel)
	}
	if got.ErrorCode == "" {
		t.Error("a count that resolved to nothing recorded no error code")
	}
}
```

Add `anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"` to the imports if it is not present.

- [ ] **Step 2: Run the test to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run TestResolveRecordsTheTrace -race -count=1 -v
```

Expected: FAIL. The count path resolves nothing and returns an error to the client, but the record's `ErrorCode` is set — read the actual failure. If it already passes, the behavior is better than assumed; keep the test and note it in the commit rather than deleting it.

- [ ] **Step 3: Write the extraction**

Create `internal/exec/resolve.go`:

```go
package exec

import (
	"context"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/router"
	"github.com/darkraise/darkrouter/internal/store"
)

// errorWriter is the slice of a dialect the prologue needs. Both edge.Dialect
// and edge.CountWriter satisfy it, and so will every auxiliary surface's
// writer — the prologue must be able to report a failure in whatever shape the
// client speaks, per master design §14.
type errorWriter interface {
	WriteError(w http.ResponseWriter, e *ir.Error) error
}

// resolved is everything the prologue produced, frozen for the attempt loop.
type resolved struct {
	Candidates []router.Candidate
	ByID       map[string]provider.Provider
	Catalog    catalog.Reader
	Cfg        *config.Config
}

// resolve runs the prologue every route shares: fetch the provider set, freeze
// the router snapshot, resolve candidates, and record the trace.
//
// It reports false when it has already written an error to w — the caller must
// return immediately rather than inspecting the zero resolved. Returning a
// bool rather than an error keeps the "who writes the response" question
// answered in exactly one place.
//
// The snapshot freezes every input the router is allowed to read, and health is
// resolved to booleans here rather than inside Resolve, which is what keeps the
// router a pure function of its arguments.
func (e *Executor) resolve(ctx context.Context, w http.ResponseWriter, ew errorWriter,
	q router.Query, rec *store.RequestRecord, cfg *config.Config, start time.Time) (resolved, bool) {

	rec.Surface = string(q.Surface)
	rec.RequestedModel = q.Model

	providers, err := e.src.Providers(ctx)
	if err != nil {
		rec.ErrorCode = string(ir.ErrDarkrouter)
		_ = ew.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return resolved{}, false
	}

	cat := e.catalogFor(providers)
	snap := router.Snapshot{At: start, Providers: providers, Catalog: cat, Config: cfg}
	if e.deps.Fleet != nil {
		snap.Health = e.deps.Fleet.SnapshotAvailability(start)
		snap.LastUsed = e.deps.Fleet.LastUsedSnapshot()
	}

	cands, skips, rerr := router.Resolve(q, snap)
	// Recorded before the error check: the skips are what explain an empty
	// candidate list, so discarding them on the failure path throws away the
	// only evidence of why nothing routed.
	rec.Candidates = traceCandidates(cands)
	rec.Skips = traceSkips(skips)

	if rerr != nil {
		e2 := routerError(rerr)
		rec.ErrorCode = string(e2.Type)
		_ = ew.WriteError(w, e2)
		return resolved{}, false
	}

	byID := make(map[string]provider.Provider, len(providers))
	for _, p := range providers {
		byID[p.ID] = p
	}
	return resolved{Candidates: cands, ByID: byID, Catalog: cat, Cfg: cfg}, true
}
```

- [ ] **Step 4: Rewire both callers**

In `internal/exec/exec.go`, replace everything in `Handle` from the `providers, err := e.src.Providers(...)` line through the `byID` loop with:

```go
	needs := req.Needs()
	res, ok := e.resolve(r.Context(), w, d, router.Query{
		Model: req.Model, Surface: surface,
		NeedsTools: needs.Tools, NeedsVision: needs.Vision, NeedsReasoning: needs.Reasoning,
	}, rec, cfg, start)
	if !ok {
		return
	}
	e.runAttempts(w, r, d, cfg, req, res.Candidates, rec, start, res.ByID, res.Catalog)
```

The two lines above it that set `rec.Surface` and `rec.RequestedModel` come out — `resolve` sets both.

In `internal/exec/count.go`, replace the same span in `HandleCount` with:

```go
	needs := req.Needs()
	res, ok := e.resolve(r.Context(), w, d, router.Query{
		Model: req.Model, Surface: ir.SurfaceLLM,
		NeedsTools: needs.Tools, NeedsVision: needs.Vision, NeedsReasoning: needs.Reasoning,
	}, rec, cfg, start)
	if !ok {
		return
	}
	cands, byID := res.Candidates, res.ByID
```

and delete its now-duplicated `rec.RequestedModel` assignment. `HandleCount` keeps everything after that unchanged.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package. The prologue is behavior-preserving for `Handle`; the only change anywhere is that `HandleCount` now records its skips.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/exec/resolve.go internal/exec/exec.go internal/exec/count.go internal/exec/exec_test.go
git commit -m "refactor(exec): extract the shared request prologue"
```

---

### Task 5: The commit-aware response writer

**Files:**
- Create: `internal/exec/commitwriter.go`
- Test: `internal/exec/commitwriter_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `exec.CommitWriter` with `Committed() bool`, `Bytes() int64`, `OnCommit(func())`, and the `http.ResponseWriter` and `http.Flusher` methods. Task 6's loop wraps every response in one.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4
**Approach:** inline - skip 2: the wrapper is a standard `http.ResponseWriter` decorator and its contract is stated in full below.

Phase 3's rule is that once the first byte reaches the client there is no re-route. Today that fact is inferred from an outcome value, and `attemptStream` returns `OutcomeSuccess` for a *post-commit failure* — conflating "committed" with "succeeded". That encoding is survivable with one surface. Inheriting it into six more is not: a binary surface that reports success after writing half a truncated audio body would otherwise be indistinguishable from one that finished.

So the fact becomes observable rather than reported. The loop wraps the writer, and after a surface hands back control the loop asks the **wrapper**, not the surface, whether bytes went out.

`Bytes()` is not bookkeeping for its own sake — spec §7 requires the byte count to be logged precisely because a truncated binary response cannot be warned about in-band, so the trace is the only place the truncation can show up.

- [ ] **Step 1: Write the failing test**

Create `internal/exec/commitwriter_test.go`:

```go
package exec

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCommitWriterStartsUncommitted(t *testing.T) {
	cw := NewCommitWriter(httptest.NewRecorder())
	if cw.Committed() {
		t.Error("a writer nobody has written to reports committed")
	}
	if cw.Bytes() != 0 {
		t.Errorf("bytes = %d", cw.Bytes())
	}
}

func TestWriteHeaderCommits(t *testing.T) {
	// A status line is as irrevocable as a body byte: the client has been told
	// this attempt is the answer.
	rec := httptest.NewRecorder()
	cw := NewCommitWriter(rec)
	cw.WriteHeader(http.StatusOK)
	if !cw.Committed() {
		t.Error("WriteHeader did not commit")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestWriteCommitsAndCounts(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := NewCommitWriter(rec)
	n, err := cw.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	if _, err := cw.Write([]byte(" world")); err != nil {
		t.Fatal(err)
	}
	if !cw.Committed() {
		t.Error("Write did not commit")
	}
	if cw.Bytes() != 11 {
		t.Errorf("bytes = %d, want 11", cw.Bytes())
	}
	if rec.Body.String() != "hello world" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestAnEmptyWriteDoesNotCommit(t *testing.T) {
	// A zero-length write reaches no client and must not end the chain. A
	// surface that probes with one would otherwise lose its failover.
	cw := NewCommitWriter(httptest.NewRecorder())
	if _, err := cw.Write(nil); err != nil {
		t.Fatal(err)
	}
	if cw.Committed() {
		t.Error("an empty write committed")
	}
}

func TestOnCommitFiresExactlyOnce(t *testing.T) {
	// The loop hangs the total-to-idle timeout switch and the diagnostics
	// headers off this hook. Firing it twice would restart the idle clock
	// mid-stream.
	var fired int
	cw := NewCommitWriter(httptest.NewRecorder())
	cw.OnCommit(func() { fired++ })

	cw.WriteHeader(http.StatusOK)
	_, _ = cw.Write([]byte("a"))
	_, _ = cw.Write([]byte("b"))

	if fired != 1 {
		t.Errorf("OnCommit fired %d times, want 1", fired)
	}
}

func TestOnCommitRegisteredAfterCommitFiresImmediately(t *testing.T) {
	// Registration order must not decide whether the hook runs, or a surface
	// that writes before the loop finishes wiring would skip the timer switch.
	var fired int
	cw := NewCommitWriter(httptest.NewRecorder())
	_, _ = cw.Write([]byte("a"))
	cw.OnCommit(func() { fired++ })
	if fired != 1 {
		t.Errorf("OnCommit fired %d times, want 1", fired)
	}
}

func TestFlushPassesThroughAndCommits(t *testing.T) {
	// SSE surfaces flush per event. A recorder implements http.Flusher, and a
	// wrapper that swallowed it would buffer every stream to completion.
	rec := httptest.NewRecorder()
	cw := NewCommitWriter(rec)
	f, ok := any(cw).(http.Flusher)
	if !ok {
		t.Fatal("CommitWriter does not implement http.Flusher")
	}
	f.Flush()
	if !cw.Committed() {
		t.Error("a flush did not commit; the client has seen the headers")
	}
	if !rec.Flushed {
		t.Error("the flush did not reach the underlying writer")
	}
}

func TestHeaderIsTheUnderlyingHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := NewCommitWriter(rec)
	cw.Header().Set("X-Test", "1")
	if rec.Header().Get("X-Test") != "1" {
		t.Error("Header() did not reach the underlying writer")
	}
	if cw.Committed() {
		t.Error("setting a header committed; nothing has been sent yet")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestCommitWriter|TestWriteHeaderCommits|TestWriteCommits|TestAnEmptyWrite|TestOnCommit|TestFlushPasses|TestHeaderIsThe' -v
```

Expected: FAIL to build — `undefined: NewCommitWriter`.

- [ ] **Step 3: Write the wrapper**

Create `internal/exec/commitwriter.go`:

```go
package exec

import "net/http"

// CommitWriter makes Phase 3's commit rule observable rather than reported.
//
// The rule is that once the first byte reaches the client there is no
// re-route. Today the loop infers that from an outcome value, and the stream
// path returns OutcomeSuccess for a post-commit failure — conflating
// "committed" with "succeeded". That is survivable with one surface and not
// with seven: a binary surface reporting success after writing half a
// truncated body would be indistinguishable from one that finished.
//
// So the loop wraps the response writer and, after a surface returns, asks the
// wrapper rather than the surface. Detecting what counts as content-bearing
// stays with the surface — only it knows its wire format — but the record of
// whether anything actually went out belongs here.
//
// It is not safe for concurrent use. One request writes to one of these from
// one goroutine, which is what the handler contract already requires.
type CommitWriter struct {
	w         http.ResponseWriter
	committed bool
	bytes     int64
	onCommit  []func()
}

func NewCommitWriter(w http.ResponseWriter) *CommitWriter {
	return &CommitWriter{w: w}
}

// Committed reports whether anything has reached the client. Once true it
// never returns false again.
func (c *CommitWriter) Committed() bool { return c.committed }

// Bytes is how many body bytes went out. Spec §7 requires this on the record:
// a truncated binary response cannot be signalled in-band, so the trace is the
// only place the truncation can appear.
func (c *CommitWriter) Bytes() int64 { return c.bytes }

// OnCommit registers a hook to run when the first byte goes out — the loop
// hangs the total-to-idle timeout switch and the diagnostics headers off it.
//
// A hook registered after the commit runs immediately, so registration order
// cannot decide whether it runs at all.
func (c *CommitWriter) OnCommit(fn func()) {
	if c.committed {
		fn()
		return
	}
	c.onCommit = append(c.onCommit, fn)
}

func (c *CommitWriter) commit() {
	if c.committed {
		return
	}
	c.committed = true
	for _, fn := range c.onCommit {
		fn()
	}
	c.onCommit = nil
}

func (c *CommitWriter) Header() http.Header { return c.w.Header() }

func (c *CommitWriter) WriteHeader(status int) {
	// A status line is as irrevocable as a body byte: the client has been told
	// this attempt is the answer.
	c.commit()
	c.w.WriteHeader(status)
}

func (c *CommitWriter) Write(b []byte) (int, error) {
	if len(b) == 0 {
		// A zero-length write reaches no client, so it must not end the chain.
		// net/http would send headers here, but a surface probing with an empty
		// write has not answered anything and keeps its failover.
		return 0, nil
	}
	c.commit()
	n, err := c.w.Write(b)
	c.bytes += int64(n)
	return n, err
}

// Flush forwards to the underlying writer. SSE surfaces flush per event, and a
// wrapper that swallowed it would buffer every stream to completion.
func (c *CommitWriter) Flush() {
	c.commit()
	if f, ok := c.w.(http.Flusher); ok {
		f.Flush()
	}
}

var (
	_ http.ResponseWriter = (*CommitWriter)(nil)
	_ http.Flusher        = (*CommitWriter)(nil)
)
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestCommitWriter|TestWriteHeaderCommits|TestWriteCommits|TestAnEmptyWrite|TestOnCommit|TestFlushPasses|TestHeaderIsThe' -race -count=1 -v
```

Expected: PASS, eight tests.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing. Nothing consumes `CommitWriter` yet; Task 6 wires it in.

- [ ] **Step 6: Commit**

```bash
git add internal/exec/commitwriter.go internal/exec/commitwriter_test.go
git commit -m "feat(exec): add a commit-aware response writer"
```

---

### Task 6: SurfaceOp, and the loop re-parameterized over it

**Files:**
- Create: `internal/exec/surface.go`
- Modify: `internal/exec/exec.go` (`runAttempts`, `attempt`, `attemptStream`, `Handle`)
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `CommitWriter` (Task 5), `resolved` (Task 4).
- Produces: `exec.SurfaceOp`, `exec.AttemptCtx`, `exec.chatOp`, and `runAttempts(w, r, op SurfaceOp, cfg, cands, rec, start, byID, cat)`. Every auxiliary surface from Task 8 onward implements `SurfaceOp`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 2: the interface and the cut points are stated in full below, and the largest moving part is a verbatim relocation rather than new code.

Risk is 3 because this rewrites the request path every existing route runs on. **Chat behavior must not change.** Task 3's two tests plus `internal/exec`'s existing suite — idle handoff, deadline-cause classification, pre-commit replay, the `postcommit_test.go` pair — are the net. `internal/golden` is not: it never imports `exec`.

**The cut.** `attempt` currently does seven things. Five are surface-invariant and stay in the loop, because they are exactly where Phase 3's subtle bugs were fixed:

| Stays in the loop | Moves to the op |
|---|---|
| The cancel-cause timer and its post-commit reset | — |
| `adapter.Target` construction, including `modelInfo` | — |
| `makeReplayable`, `client.Do`, `classify` | — |
| The `BodyClassifier` 400-body reclassification | — |
| `recordAttempt`, `recordHealthFor`, the non-success early return | — |
| — | Rendering the outbound request (`Build`) |
| — | Turning a 2xx into client bytes (`Respond`) |

A single opaque `Attempt` method would have made six new surfaces each re-implement the left column.

The interface is five methods: `Dialect`, `Query`, `Build`, `Respond` and `WriteError`.

- [ ] **Step 1: Write the failing test**

Add to `internal/exec/exec_test.go`. This asserts the seam itself rather than chat behavior, which the existing suite already covers:

```go
// probeOp is a SurfaceOp that records what the loop handed it. It exists to
// pin the contract between the loop and an op, which no chat test can: chat is
// the one implementation whose behavior the rest of the suite already fixes.
type probeOp struct {
	q         router.Query
	builds    int
	responds  int
	lastInfo  adapter.ModelInfo
	buildWarn string
	onRespond func(cw *CommitWriter) (adapter.Outcome, *ir.Error)
}

func (p *probeOp) Query() router.Query { return p.q }

func (p *probeOp) Dialect() string { return "probe" }

func (p *probeOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	p.builds++
	p.lastInfo = tgt.Info
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(tgt.BaseURL, "/")+"/probe",
		strings.NewReader(`{}`))
	if err != nil {
		return nil, nil, err
	}
	var warns []ir.Warning
	if p.buildWarn != "" {
		warns = append(warns, ir.Warning{Field: p.buildWarn, Target: "probe", Reason: "test"})
	}
	return req, warns, nil
}

func (p *probeOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	p.responds++
	defer resp.Body.Close()
	if p.onRespond != nil {
		return p.onRespond(cw)
	}
	_, _ = cw.Write([]byte("ok"))
	return adapter.OutcomeSuccess, nil
}

func (p *probeOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte(e.Message))
	return nil
}

func TestTheLoopGivesAnOpTheCatalogFacts(t *testing.T) {
	// The loop owns Target construction, so an op must receive the catalog's
	// view without doing its own lookup.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM}, MaxOutputTokens: 4242,
	}}, []string{"p"}))

	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceLLM}}
	e, rec := executorForOp(t, upstream.URL, cat)
	e.RunSurface(httptest.NewRecorder(), httptest.NewRequest("POST", "/probe", nil), op)

	if op.builds != 1 || op.responds != 1 {
		t.Fatalf("builds = %d, responds = %d, want 1 and 1", op.builds, op.responds)
	}
	if op.lastInfo.MaxOutputTokens != 4242 {
		t.Errorf("Info = %+v; the loop did not supply the catalog facts", op.lastInfo)
	}
	if got := rec.only(t); got.Status != "success" {
		t.Errorf("status = %q", got.Status)
	}
}

func TestAnOpThatCommittedCannotRestartTheChain(t *testing.T) {
	// The op detects commit; the loop enforces it. An op reporting a retryable
	// outcome after bytes went out must not produce a second attempt, or a
	// client would receive two half-responses concatenated.
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	op := &probeOp{
		q: router.Query{Model: "m", Surface: ir.SurfaceLLM},
		onRespond: func(cw *CommitWriter) (adapter.Outcome, *ir.Error) {
			_, _ = cw.Write([]byte("partial"))
			// A lie the loop must not believe.
			return adapter.OutcomeRetryableProvider, &ir.Error{Type: ir.ErrAPI, Message: "boom"}
		},
	}
	e, _ := executorForOpWithTwoProviders(t, upstream.URL)
	w := httptest.NewRecorder()
	e.RunSurface(w, httptest.NewRequest("POST", "/probe", nil), op)

	if got := hits.Load(); got != 1 {
		t.Errorf("upstream called %d times; a committed attempt restarted the chain", got)
	}
	if !strings.Contains(w.Body.String(), "partial") {
		t.Errorf("body = %q; the committed bytes were lost", w.Body.String())
	}
}
```

Add two helpers beside them. `executorForOp` returns an executor whose single provider points at `url` with a `probe` kind, plus the `captureLogger` it logs to; `executorForOpWithTwoProviders` is the same with a second identical provider so a retry has somewhere to go. Both reuse `executorFor` from phase 6 and register `openaicompat.New()` under the kind name `probe` — the op builds its own request, so the adapter is only there to satisfy `adapterFor`.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestTheLoopGivesAnOp|TestAnOpThatCommitted' -v
```

Expected: FAIL to build — `undefined: SurfaceOp`, `undefined: AttemptCtx`, `e.RunSurface undefined`.

- [ ] **Step 3: Declare the interface**

Create `internal/exec/surface.go`:

```go
package exec

import (
	"context"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
	"github.com/darkraise/darkrouter/internal/store"
)

// SurfaceOp is what varies between surfaces. Everything else — the budget gate,
// the live health re-check, credential rotation, adapter resolution, the send,
// outcome classification, attempt records, health signals and the request log —
// is surface-invariant and stays in the loop, because that is where phase 3's
// subtle bugs were fixed and it must not be reimplemented six more times.
//
// Beyond naming its dialect it is deliberately narrow, split at two joints:
// rendering the outbound request, and turning a 2xx into client bytes.
type SurfaceOp interface {
	// Dialect names the inbound wire form, for the request row's dialect
	// column. An op knows it; the loop cannot infer it, and the six auxiliary
	// routes are not all the same dialect as the chat route they share a
	// package with.
	Dialect() string

	// Query is what the router filters on. Auxiliary surfaces set no capability
	// needs — an embedding request does not ask for tools.
	Query() router.Query

	// Build renders the outbound request for one resolved target. It is called
	// once per attempt, not once per request: the target's model name differs
	// per candidate, and a multipart body must be re-rendered with the new name
	// inside the form.
	Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error)

	// Respond turns a successful upstream response into client bytes. It is
	// called only when the loop classified the response as OutcomeSuccess, and
	// it owns closing resp.Body.
	//
	// Writing to cw is what commits the response. The op decides what counts as
	// content-bearing for its wire format; the loop decides what that means for
	// failover, by consulting the writer rather than the returned outcome.
	Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error)

	// WriteError renders a Darkrouter error in the shape the client speaks.
	// Master design §14: an error is normalized into the inbound dialect.
	WriteError(w http.ResponseWriter, e *ir.Error) error
}

// AttemptCtx is what Respond needs from the attempt around it. It is a struct
// rather than six parameters because auxiliary surfaces use different subsets
// and the list would otherwise grow with every one of them.
type AttemptCtx struct {
	Exec *Executor
	Cfg  *config.Config
	Cand router.Candidate
	Rec  *store.RequestRecord
	Seq  int
	// Timer bounds the attempt. Respond resets it at commit, when the total
	// timeout stops applying and idle takes over.
	Timer *time.Timer
	// Warns are the warnings Build produced, plus the inferred-capability
	// warning when the loop admitted a guess. Respond appends whatever the
	// response itself raised and assigns the union to the record — assigned,
	// never appended across attempts, so an abandoned attempt's warnings do not
	// describe the translation the client received.
	Warns   []ir.Warning
	Adapter adapter.Adapter

	// FirstModel is the model of the first candidate the router produced, not
	// of the first attempt that ran. Spec §8's embedding warning fires when the
	// serving model differs from it, and the difference matters: a first
	// candidate skipped by the live cooling re-check never reaches Build, so an
	// op inferring "first" from its own calls would stay silent in exactly the
	// case the warning exists for.
	FirstModel string
}
```

- [ ] **Step 4: Re-parameterize the loop**

In `internal/exec/exec.go`, change `runAttempts` and `attempt` to take the op in place of `d edge.Dialect` and `req *ir.Request`.

`runAttempts`'s signature and its three uses of the old parameters:

```go
func (e *Executor) runAttempts(w http.ResponseWriter, r *http.Request, op SurfaceOp,
	cfg *config.Config, cands []router.Candidate,
	rec *store.RequestRecord, start time.Time, byID map[string]provider.Provider,
	cat catalog.Reader) {
```

Inside it, `e.attempt(w, r, d, cfg, req, c, ...)` becomes `e.attempt(w, r, op, cfg, c, ..., firstModel)`, where `firstModel` is computed once before the loop:

```go
	// The first candidate the router produced, captured before the loop so a
	// candidate skipped by the live health re-check is still "first". Spec §8's
	// embedding warning depends on this distinction.
	var firstModel string
	if len(cands) > 0 {
		firstModel = cands[0].Model
	}
```

Both `_ = d.WriteError(w, lastErr)` calls become `_ = op.WriteError(w, lastErr)`. Nothing else in the body changes — the budget gate, the health re-check, `adapterFor`, `MarkUsed`, `nextIndex` and the status assignments are all surface-invariant already.

`attempt` keeps its whole preamble and changes only where it reaches for the request or the dialect:

```go
func (e *Executor) attempt(w http.ResponseWriter, r *http.Request, op SurfaceOp,
	cfg *config.Config, c router.Candidate, p provider.Provider,
	bud budget, rec *store.RequestRecord, seq int, ad adapter.Adapter,
	cat catalog.Reader, firstModel string) (adapter.Outcome, int, *ir.Error) {
```

Replace the build block — everything from `var warns []ir.Warning` through the `makeReplayable` check — with:

```go
	var warns []ir.Warning
	if iw, ok := inferredWarningFor(c, op.Query()); ok {
		warns = append(warns, iw)
	}
	hr, buildWarns, err := op.Build(ctx, tgt, ad)
	warns = append(warns, buildWarns...)
	if err != nil {
		return adapter.OutcomeFatal, 0, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}
	if err := makeReplayable(hr); err != nil {
		return adapter.OutcomeFatal, 0, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}
```

Replace everything from `if req.Stream {` to the end of the function with:

```go
	cw := NewCommitWriter(w)
	outcome, aerr := op.Respond(cw, resp, &AttemptCtx{
		Exec: e, Cfg: cfg, Cand: c, Rec: rec, Seq: seq, Timer: timer,
		Warns: warns, Adapter: ad, FirstModel: firstModel,
	})
	// The loop asks the writer, not the op. An op that reports a retryable
	// outcome after bytes have gone out is describing a post-commit failure,
	// and phase 3's rule says the chain ends there regardless — a second
	// attempt would concatenate two half-responses on one connection.
	if cw.Committed() && outcome != adapter.OutcomeSuccess {
		rec.ErrorCode = string(ir.ErrAPI)
		return adapter.OutcomeSuccess, statusCode, nil
	}
	return outcome, statusCode, aerr
```

`inferredWarningFor` is `inferredWarning` with its `*ir.Request` parameter replaced by the query, since an auxiliary surface has no `ir.Request` to ask. Replace the existing function with:

```go
// inferredWarningFor records that a candidate was admitted on guessed
// capability metadata for a request that actually needed a capability.
//
// Master design §6.4 admits these rather than excluding them, because
// hard-filtering on a guess would make every discovered local model refuse the
// tool requests Claude Code always sends. The cost is that a provider's own
// rejection looks like a Darkrouter failure, and this is what makes the trace
// say otherwise.
//
// It takes the query rather than the request because an auxiliary surface has
// no ir.Request — and needs no capability, so it never warns.
func inferredWarningFor(c router.Candidate, q router.Query) (ir.Warning, bool) {
	if !c.Inferred {
		return ir.Warning{}, false
	}
	var missing []string
	if q.NeedsTools {
		missing = append(missing, "tools")
	}
	if q.NeedsVision {
		missing = append(missing, "vision")
	}
	if q.NeedsReasoning {
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

- [ ] **Step 5: Move chat behind the interface**

Add `chatOp` to `internal/exec/surface.go`. Its `Respond` is the old tail of `attempt`, moved rather than rewritten:

```go
// chatOp is the llm surface. It is the first SurfaceOp and its behavior is
// identical to phase 4's: the whole point of the extraction is that this file
// contains a move, not a rewrite.
type chatOp struct {
	d   edge.Dialect
	req *ir.Request
}

func (o *chatOp) Dialect() string { return o.d.Name() }

func (o *chatOp) Query() router.Query {
	needs := o.req.Needs()
	return router.Query{
		Model: o.req.Model, Surface: ir.SurfaceLLM,
		NeedsTools: needs.Tools, NeedsVision: needs.Vision, NeedsReasoning: needs.Reasoning,
	}
}

func (o *chatOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	return ad.BuildRequest(ctx, tgt, o.req)
}

func (o *chatOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}
```

`chatOp.Respond` is the code currently in `attempt` from `if req.Stream {` onward, relocated with three substitutions and no other change:

- `req.Stream` becomes `o.req.Stream`, `d` becomes `o.d`, and `ad` becomes `ac.Adapter`.
- `e.attemptStream(...)` becomes `ac.Exec.attemptStream(o.d, ac.Cfg, ac.Cand, resp, rec, ac.Seq, ac.Timer, ac.Warns, ac.Adapter, cw)` — give `attemptStream` a `*CommitWriter` in place of its `http.ResponseWriter` and drop its now-unused `statusCode` parameter, which it only passed through.
- The unary tail keeps `rec.Warnings = warningStrings(append(ac.Warns, out.Warnings...))` **assigned, not appended**. Task 3's second test is what catches an accumulator here.

Move `attemptStream` and its helpers unchanged otherwise. Its internal writes already go through the writer it is handed, so passing a `*CommitWriter` is the only edit its body needs.

- [ ] **Step 6: Give the loop a public entry point**

Add to `internal/exec/surface.go`:

```go
// RunSurface is the entry point every route shares: prologue, then the loop.
// Handle and the six auxiliary routes are each a few lines of parsing followed
// by one call to this.
func (e *Executor) RunSurface(w http.ResponseWriter, r *http.Request, op SurfaceOp) {
	start := time.Now()
	cfg := e.store.Current()
	rec, done := e.newRecord(start, op)
	defer done()

	res, ok := e.resolve(r.Context(), w, op, op.Query(), rec, cfg, start)
	if !ok {
		return
	}
	e.runAttempts(w, r, op, cfg, res.Candidates, rec, start, res.ByID, res.Catalog)
}
```

`newRecord` is the record construction and deferred log that `Handle` and `HandleCount` both open with — extract it alongside, taking the op for its dialect and its surface:

```go
// newRecord opens the request row and returns the closer that emits it. The
// record is built as the request proceeds and emitted exactly once on every
// exit path, and Status starts as "error" so a path that forgets to set it is
// recorded as a failure rather than a silent success.
func (e *Executor) newRecord(start time.Time, op SurfaceOp) (*store.RequestRecord, func()) {
	rec := &store.RequestRecord{
		ID:      ulid.MustNew(ulid.Timestamp(start), rand.Reader).String(),
		TS:      start,
		Dialect: op.Dialect(),
		Surface: string(op.Query().Surface),
		Status:  "error",
	}
	return rec, func() {
		total := time.Since(start).Milliseconds()
		rec.TotalMs = &total
		e.log(rec)
	}
}
```

`RunSurface` sets `X-Darkrouter-Request` and the initial `X-Darkrouter-Attempts: 0` from `rec.ID` immediately after this, exactly as `Handle` does today. `Handle` keeps its own body-parsing preamble and then constructs a `chatOp` and calls `RunSurface`; `HandleCount` is unchanged, since it does not run the attempt loop.

- [ ] **Step 7: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package — the two new seam tests, Task 3's two, and every phase 3 and 4 test unchanged. A failure in `postcommit_test.go` means the timer handoff moved; a failure in the idle or deadline tests means the cancel-cause plumbing did.

- [ ] **Step 8: Verify chat end to end**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
go test ./internal/exec/ ./internal/server/ -race -count=5
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing, and no flakes across five runs. `internal/golden` passing proves nothing about this task — say so in the commit rather than citing it.

- [ ] **Step 9: Commit**

```bash
git add internal/exec/surface.go internal/exec/exec.go internal/exec/exec_test.go
git commit -m "refactor(exec): drive the loop through a SurfaceOp"
```

---

### Task 7: Adapters declare the surfaces they implement

**Files:**
- Modify: `internal/adapter/adapter.go`
- Modify: `internal/adapter/openaicompat/classify.go`
- Test: `internal/adapter/adapter_test.go`

**Interfaces:**
- Consumes: `ir.Surface` (Task 1).
- Produces: `adapter.SurfaceSet`, `adapter.SurfaceProvider`, `adapter.SurfacesOf(Adapter) SurfaceSet`, and `openaicompat.Adapter.Surfaces()`. Task 8's filter reads them.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: `BodyClassifier` and `TokenCounter` in the same file are the established optional-interface idiom, and this follows it exactly.

Master design §5.1's `Adapter.Surfaces()` is the item PROGRESS has carried since phase 3 as "still does not exist… phase 5 is where it becomes load-bearing". This is that.

It is an **optional** interface, not a method on `Adapter`. That matches `BodyClassifier` and `TokenCounter` beside it, and it means an adapter that says nothing serves `llm` only — the honest default, and the one that keeps phase 8's bedrock and vertex adapters compiling untouched.

The §4 matrix is the specification. `openaicompat` is the only kind that serves more than chat and embeddings, which is why it declares six here and the other two get their single addition alongside the surface that needs it.

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/adapter_test.go`:

```go
package adapter

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// silentAdapter implements Adapter without SurfaceProvider. Only Kind is
// exercised; the rest satisfy the interface.
type silentAdapter struct{ Adapter }

func (silentAdapter) Kind() string { return "silent" }

type talkativeAdapter struct{ Adapter }

func (talkativeAdapter) Kind() string { return "talkative" }
func (talkativeAdapter) Surfaces() SurfaceSet {
	return SurfaceSet{ir.SurfaceLLM: true, ir.SurfaceEmbedding: true}
}

func TestSurfacesOfDefaultsToChatOnly(t *testing.T) {
	// An adapter that declares nothing serves llm. Defaulting to everything
	// would make an unimplemented surface a runtime 404 from the provider
	// instead of a routing decision Darkrouter can explain.
	got := SurfacesOf(silentAdapter{})
	if !got.Has(ir.SurfaceLLM) {
		t.Error("the default does not include llm")
	}
	for _, s := range ir.AllSurfaces() {
		if s == ir.SurfaceLLM {
			continue
		}
		if got.Has(s) {
			t.Errorf("the default claims %q", s)
		}
	}
}

func TestSurfacesOfReadsTheDeclaration(t *testing.T) {
	got := SurfacesOf(talkativeAdapter{})
	if !got.Has(ir.SurfaceLLM) || !got.Has(ir.SurfaceEmbedding) {
		t.Errorf("surfaces = %v", got)
	}
	if got.Has(ir.SurfaceTTS) {
		t.Error("an undeclared surface reported present")
	}
}

func TestSurfaceSetHasIsNilSafe(t *testing.T) {
	// A nil set is "nothing declared", not a panic: the zero value has to be
	// usable because a map field is easy to leave unset.
	var s SurfaceSet
	if s.Has(ir.SurfaceLLM) {
		t.Error("a nil set claimed a surface")
	}
}
```

Add to `internal/adapter/openaicompat/classify_test.go`:

```go
func TestOpenAICompatDeclaresTheMatrixSurfaces(t *testing.T) {
	// Phase 5 spec §4: openaicompat is the only kind serving more than chat
	// and embeddings. Getting this wrong makes a route unreachable with a
	// confusing "no provider offers this" rather than a clear gap.
	got := New().Surfaces()
	for _, want := range []ir.Surface{
		ir.SurfaceLLM, ir.SurfaceEmbedding, ir.SurfaceImage,
		ir.SurfaceTTS, ir.SurfaceSTT, ir.SurfaceRerank, ir.SurfaceModeration,
	} {
		if !got.Has(want) {
			t.Errorf("openaicompat does not declare %q", want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/... -run 'TestSurfacesOf|TestSurfaceSetHas|TestOpenAICompatDeclares' -v
```

Expected: FAIL to build — `undefined: SurfaceSet`, `undefined: SurfacesOf`, and `New().Surfaces undefined`.

- [ ] **Step 3: Add the vocabulary**

In `internal/adapter/adapter.go`, beside `BodyClassifier` and `TokenCounter`:

```go
// SurfaceSet is the set of surfaces an adapter implements.
type SurfaceSet map[ir.Surface]bool

// Has is nil-safe, because the zero value has to be usable — a map field is
// easy to leave unset and a panic on the routing path is not an acceptable way
// to find out.
func (s SurfaceSet) Has(x ir.Surface) bool { return s[x] }

// SurfaceProvider is implemented by an adapter serving more than chat.
//
// Optional rather than a method on Adapter, matching BodyClassifier and
// TokenCounter above: an adapter that says nothing serves llm only, which is
// the honest default and keeps a kind whose auxiliary support arrives in a
// later phase compiling untouched.
type SurfaceProvider interface {
	Surfaces() SurfaceSet
}

// SurfacesOf reports what an adapter implements.
//
// The default is llm alone rather than everything. Master design §5.1 makes an
// unimplemented surface a routing filter, not a runtime error — an operator
// reading "no provider offers this model on this surface" learns more than one
// reading a 404 the provider produced.
func SurfacesOf(a Adapter) SurfaceSet {
	if sp, ok := a.(SurfaceProvider); ok {
		return sp.Surfaces()
	}
	return SurfaceSet{ir.SurfaceLLM: true}
}
```

- [ ] **Step 4: Declare openaicompat's surfaces**

In `internal/adapter/openaicompat/classify.go`, below `Kind`:

```go
// Surfaces is phase 5 spec §4's openaicompat column. It is the only kind that
// serves more than chat and embeddings, because OpenAI's own API defines all
// seven and every OpenAI-compatible upstream inherits the shapes.
//
// A provider that serves only some of them is filtered by its *preset*
// declaration, not here: this says what the adapter can render, and the catalog
// says what the upstream actually offers.
func (a *Adapter) Surfaces() adapter.SurfaceSet {
	return adapter.SurfaceSet{
		ir.SurfaceLLM:        true,
		ir.SurfaceEmbedding:  true,
		ir.SurfaceImage:      true,
		ir.SurfaceTTS:        true,
		ir.SurfaceSTT:        true,
		ir.SurfaceRerank:     true,
		ir.SurfaceModeration: true,
	}
}
```

Add `"github.com/darkraise/darkrouter/internal/ir"` to that file's imports if it is not already there, and assert the interface beside the file's other assertions:

```go
var _ adapter.SurfaceProvider = (*Adapter)(nil)
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/... -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS. `anthropic` and `gemini` declare nothing yet and default to `llm`, which is correct for both today — gemini's `embedding` arrives with Task 12, and anthropic serves chat only in the §4 matrix and always will.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing. Nothing reads `SurfacesOf` yet; Task 8 wires it into the filter.

- [ ] **Step 7: Commit**

```bash
git add internal/adapter/adapter.go internal/adapter/adapter_test.go \
  internal/adapter/openaicompat/classify.go internal/adapter/openaicompat/classify_test.go
git commit -m "feat(adapter): declare implemented surfaces"
```

---

### Task 8: The router filters on adapter support

**Files:**
- Modify: `internal/router/types.go`
- Modify: `internal/router/filter.go`
- Test: `internal/router/filter_test.go`

**Interfaces:**
- Consumes: `adapter.SurfaceSet` (Task 7).
- Produces: `router.Snapshot.AdapterSurfaces map[string]adapter.SurfaceSet` and `router.SkipAdapterSurface`. Task 9 fills the map.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: `filterTarget` already has the ordered check list this extends, and spec §4 states the rule.

Spec §4: "A surface an adapter does not implement makes that provider ineligible at routing time — a filter, never a runtime error." Two facts have to agree before a candidate survives, and they are different facts:

- The **catalog** says what the upstream offers — preset-declared, per model.
- The **adapter** says what Darkrouter can render — per kind.

A `bedrock` provider whose preset declares `embedding` is a real upstream capability Darkrouter cannot yet speak. Routing to it would produce a confusing runtime failure instead of a clear "no provider offers this model on this surface".

**A nil `AdapterSurfaces` map means no adapter constraint.** That is what lets this task land before Task 9 fills it, and it keeps the zero `Snapshot` — which several router tests build — routing exactly as it does today. Failing open on an unset map is also the right production default: a missing map is a wiring bug, and silently routing nothing would be a far worse symptom than routing as before.

- [ ] **Step 1: Write the failing test**

Add to `internal/router/filter_test.go`:

```go
func TestAnAdapterThatCannotServeTheSurfaceIsFiltered(t *testing.T) {
	// The catalog says the upstream offers embeddings; the adapter cannot
	// render them. Routing anyway would turn a knowable gap into a runtime
	// failure from the provider.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
	}})
	snap.AdapterSurfaces = map[string]adapter.SurfaceSet{
		"openaicompat": {ir.SurfaceLLM: true},
	}

	cands, skips, err := Resolve(Query{Model: "m", Surface: ir.SurfaceEmbedding}, snap)
	if len(cands) != 0 {
		t.Fatalf("got %d candidates for a surface the adapter cannot render", len(cands))
	}
	if err == nil {
		t.Error("Resolve returned no error with no candidates")
	}
	var found bool
	for _, s := range skips {
		if s.Reason == SkipAdapterSurface {
			found = true
		}
	}
	if !found {
		t.Errorf("no SkipAdapterSurface recorded; skips = %+v", skips)
	}
}

func TestAnAdapterThatServesTheSurfaceIsKept(t *testing.T) {
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
	}})
	snap.AdapterSurfaces = map[string]adapter.SurfaceSet{
		"openaicompat": {ir.SurfaceLLM: true, ir.SurfaceEmbedding: true},
	}
	cands, _, err := Resolve(Query{Model: "m", Surface: ir.SurfaceEmbedding}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Errorf("got %d candidates, want 1", len(cands))
	}
}

func TestANilAdapterSurfacesMapImposesNoConstraint(t *testing.T) {
	// A missing map is a wiring bug. Routing nothing would be a far worse
	// symptom than routing as before, and every phase 3 test builds a snapshot
	// without one.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
	}})
	if snap.AdapterSurfaces != nil {
		t.Fatal("the fixture set a map; this test needs it nil")
	}
	cands, _, err := Resolve(Query{Model: "m", Surface: ir.SurfaceEmbedding}, snap)
	if err != nil || len(cands) != 1 {
		t.Errorf("got %d candidates, err = %v; a nil map must not filter", len(cands), err)
	}
}
```

Add `"github.com/darkraise/darkrouter/internal/adapter"` to the file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/router/ -run 'TestAnAdapterThat|TestANilAdapterSurfaces' -v
```

Expected: FAIL to build — `unknown field AdapterSurfaces`, `undefined: SkipAdapterSurface`.

- [ ] **Step 3: Extend the snapshot and the skip vocabulary**

In `internal/router/types.go`, add to `Snapshot`:

```go
	// AdapterSurfaces is what each provider kind's adapter can render, keyed by
	// kind. It is a different fact from the catalog's surfaces: the catalog
	// says what the upstream offers, this says what Darkrouter can speak.
	//
	// A nil map imposes no constraint. That is deliberate — a missing map is a
	// wiring bug, and routing nothing would be a worse symptom than routing as
	// before.
	AdapterSurfaces map[string]adapter.SurfaceSet
```

and to the skip reasons:

```go
	// SkipAdapterSurface is a provider whose upstream offers the surface but
	// whose kind Darkrouter cannot speak it to. It is reported separately from
	// SkipSurface because the fix is different: one is a catalog gap the
	// operator can close, the other is a Darkrouter gap they cannot.
	SkipAdapterSurface SkipReason = "adapter_surface"
```

- [ ] **Step 4: Add the check**

In `internal/router/filter.go`, immediately after the `Routable` check and before the capability check — durable configuration facts before transient ones, which is the ordering the file's existing comment fixes:

```go
	// The catalog said the upstream offers this surface; this asks whether
	// Darkrouter can render it. Spec §4 makes an unimplemented surface a
	// routing filter rather than a runtime error, because an operator reading
	// "no provider offers this model on this surface" learns more than one
	// reading a 404 the provider produced.
	if q.Surface != "" && snap.AdapterSurfaces != nil {
		if !snap.AdapterSurfaces[p.Kind].Has(q.Surface) {
			return nil, []Skip{{
				ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipAdapterSurface,
			}}, true
		}
	}
```

- [ ] **Step 5: Extend `emptyReason`**

`emptyReason` picks the error that best explains an empty candidate list. A chain emptied by this check must not report "every provider is cooling". In `internal/router/router.go`, add `SkipAdapterSurface` to whichever branch already maps `SkipSurface` to `ErrSurfaceUnsupported` — read the function and follow its existing shape rather than adding a parallel one. Both mean the same thing to a client: nothing offers this model on this surface.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/router/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package. The phase 3 tests build snapshots with no `AdapterSurfaces`, so the nil-map path keeps them unchanged — that is what this task's third test asserts.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/router/types.go internal/router/filter.go internal/router/router.go internal/router/filter_test.go
git commit -m "feat(router): filter on adapter surface support"
```

---

### Task 9: The executor supplies the adapter-surface map

**Files:**
- Modify: `internal/exec/exec.go` (`New`)
- Modify: `internal/exec/resolve.go`
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `adapter.SurfacesOf` (Task 7), `router.Snapshot.AdapterSurfaces` (Task 8).
- Produces: nothing new. The filter Task 8 added starts constraining.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: the registry already exists on the `Executor` and this reads it once.

The map is computed **once in `New`**, not per request. `e.adapters` is fixed at construction and never mutated, so rebuilding the map on every request would allocate for a value that cannot change — and the router snapshot is meant to be frozen inputs, not derived work.

- [ ] **Step 1: Write the failing test**

Add to `internal/exec/exec_test.go`:

```go
func TestAnEmbeddingRequestSkipsAChatOnlyAdapter(t *testing.T) {
	// anthropic declares no surfaces, so it defaults to llm. Its preset could
	// still claim embeddings — the catalog describes the upstream, not what
	// Darkrouter can speak — and routing there would fail at the provider.
	upstream := unaryUpstream()
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
	}}, []string{"p"}))

	rec := &captureLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: anthropic
    base_url: `+upstream.URL+`
    api_key: sk
    models: [m]
`, map[string]adapter.Adapter{"anthropic": anthropicadapter.New()},
		Deps{Catalog: cat, Log: rec})

	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceEmbedding}}
	w := httptest.NewRecorder()
	e.RunSurface(w, httptest.NewRequest("POST", "/v1/embeddings", nil), op)

	if op.builds != 0 {
		t.Errorf("the op built %d requests; a chat-only adapter must be filtered before any attempt", op.builds)
	}
	got := rec.only(t)
	var found bool
	for _, s := range got.Skips {
		if strings.Contains(s, "adapter_surface") {
			found = true
		}
	}
	if !found {
		t.Errorf("skips = %v; the trace does not explain why nothing routed", got.Skips)
	}
}

func TestAChatRequestStillRoutesToAChatOnlyAdapter(t *testing.T) {
	// The obvious regression: constraining the map must not exclude the kind
	// that only ever served llm.
	upstream := unaryUpstream()
	defer upstream.Close()

	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [m]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestAnEmbeddingRequestSkips|TestAChatRequestStill' -race -count=1 -v
```

Expected: `TestAnEmbeddingRequestSkipsAChatOnlyAdapter` fails — the map is nil, so nothing filters and the op builds one request. The second test passes already and is there to stay passing.

- [ ] **Step 3: Compute the map once**

In `internal/exec/exec.go`, add the field to `Executor`:

```go
type Executor struct {
	store    *config.Store
	src      provider.Source
	adapters map[string]adapter.Adapter
	// adapterSurfaces is what each kind can render, derived from adapters at
	// construction. It cannot change afterwards, so recomputing it per request
	// would allocate for a constant — and the router snapshot is meant to hold
	// frozen inputs rather than derived work.
	adapterSurfaces map[string]adapter.SurfaceSet
	client          *http.Client
	deps            Deps
}
```

and fill it in `New`, immediately before the returned literal:

```go
	surfaces := make(map[string]adapter.SurfaceSet, len(adapters))
	for kind, ad := range adapters {
		surfaces[kind] = adapter.SurfacesOf(ad)
	}
```

then set `adapterSurfaces: surfaces` in the `&Executor{...}` literal alongside `adapters`.

- [ ] **Step 4: Put it on the snapshot**

In `internal/exec/resolve.go`, add the field where the snapshot is built:

```go
	snap := router.Snapshot{
		At: start, Providers: providers, Catalog: cat, Config: cfg,
		AdapterSurfaces: e.adapterSurfaces,
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package. Every existing chat test routes through `openaicompat`, `anthropic` or `gemini` on the `llm` surface, which all three declare or default to.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/exec/exec.go internal/exec/resolve.go internal/exec/exec_test.go
git commit -m "feat(exec): supply adapter surfaces to the router"
```

---

### Task 10: The auxiliary op scaffold

**Files:**
- Create: `internal/exec/aux.go`
- Test: `internal/exec/aux_test.go`

**Interfaces:**
- Consumes: `SurfaceOp`, `AttemptCtx`, `CommitWriter` (Tasks 5, 6).
- Produces: `exec.AuxOp` with `exec.NewAuxOp(dialect string, q router.Query, build AuxBuild, respond AuxRespond, writeErr AuxWriteError) *AuxOp`, plus `exec.DecodeJSON` and `exec.ReadCapped`. Tasks 11 onward each construct one.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: the three closure types fall directly out of `SurfaceOp`'s method set, which Task 6 fixed.

Six surfaces differ in exactly two ways — what they render and how they write — and are identical in every other respect. Six near-duplicate types implementing five methods each would be thirty methods of ceremony around twelve lines of actual difference.

So the scaffold takes the difference as three closures and supplies the rest. This is not a new abstraction layer: it is `SurfaceOp` with the boilerplate written once.

`ReadCapped` exists because every JSON auxiliary response must be bounded. An embedding response is large but finite; an unbounded read from a misbehaving provider on a background-free request path is the hazard `max_body_bytes` exists to prevent inbound, and nothing was enforcing it outbound.

- [ ] **Step 1: Write the failing test**

Create `internal/exec/aux_test.go`:

```go
package exec

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

func TestAuxOpDelegatesToItsClosures(t *testing.T) {
	q := router.Query{Model: "m", Surface: ir.SurfaceEmbedding}
	var built, responded, errored bool

	op := NewAuxOp("openai", q,
		func(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
			built = true
			return http.NewRequestWithContext(ctx, "POST", "http://x/v1/embeddings", strings.NewReader("{}"))
		},
		func(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
			responded = true
			return adapter.OutcomeSuccess, nil
		},
		func(w http.ResponseWriter, e *ir.Error) error {
			errored = true
			return nil
		})

	if op.Query() != q {
		t.Errorf("Query() = %+v", op.Query())
	}
	if op.Dialect() != "openai" {
		t.Errorf("Dialect() = %q; the request row would record the wrong wire form", op.Dialect())
	}
	if _, _, err := op.Build(context.Background(), &adapter.Target{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, aerr := op.Respond(NewCommitWriter(httptest.NewRecorder()),
		&http.Response{Body: io.NopCloser(strings.NewReader(""))}, &AttemptCtx{}); aerr != nil {
		t.Fatal(aerr)
	}
	_ = op.WriteError(httptest.NewRecorder(), &ir.Error{})

	if !built || !responded || !errored {
		t.Errorf("delegation = (%v, %v, %v), want all true", built, responded, errored)
	}
}

func TestAuxOpSatisfiesSurfaceOp(t *testing.T) {
	var _ SurfaceOp = NewAuxOp("openai", router.Query{}, nil, nil, nil)
}

func TestReadCappedStopsAtTheLimit(t *testing.T) {
	// An unbounded read from a misbehaving provider is exactly the hazard
	// max_body_bytes prevents inbound and nothing was preventing outbound.
	body := io.NopCloser(strings.NewReader(strings.Repeat("a", 100)))
	got, err := ReadCapped(body, 10)
	if err == nil {
		t.Fatal("an oversized body read cleanly")
	}
	if len(got) > 10 {
		t.Errorf("read %d bytes past a 10-byte cap", len(got))
	}
}

func TestReadCappedAcceptsAnExactFit(t *testing.T) {
	// The boundary must not be an off-by-one: a response exactly at the cap is
	// legitimate, and rejecting it would fail on a perfectly good payload.
	got, err := ReadCapped(io.NopCloser(strings.NewReader("0123456789")), 10)
	if err != nil {
		t.Fatalf("a body exactly at the cap was rejected: %v", err)
	}
	if string(got) != "0123456789" {
		t.Errorf("body = %q", got)
	}
}

func TestDecodeJSONRejectsGarbage(t *testing.T) {
	// An HTML error page behind a 200 is a provider fault, not a decode
	// curiosity: it must surface as an error the loop can classify.
	var into struct {
		A int `json:"a"`
	}
	err := DecodeJSON(io.NopCloser(strings.NewReader("<html>502</html>")), 1<<20, &into)
	if err == nil {
		t.Fatal("an HTML body decoded cleanly")
	}
}

func TestDecodeJSONReadsTheDocument(t *testing.T) {
	var into struct {
		A int `json:"a"`
	}
	if err := DecodeJSON(io.NopCloser(strings.NewReader(`{"a":7}`)), 1<<20, &into); err != nil {
		t.Fatal(err)
	}
	if into.A != 7 {
		t.Errorf("a = %d, want 7", into.A)
	}
}

func TestDecodeJSONPropagatesTheCap(t *testing.T) {
	var into map[string]any
	err := DecodeJSON(io.NopCloser(strings.NewReader(`{"a":"`+strings.Repeat("x", 100)+`"}`)), 16, &into)
	if err == nil {
		t.Fatal("an oversized document decoded cleanly")
	}
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("err = %v, want ErrBodyTooLarge so the caller can tell it from a syntax error", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestAuxOp|TestReadCapped|TestDecodeJSON' -v
```

Expected: FAIL to build — `undefined: NewAuxOp`, `undefined: ReadCapped`, `undefined: DecodeJSON`, `undefined: ErrBodyTooLarge`.

- [ ] **Step 3: Write the scaffold**

Create `internal/exec/aux.go`:

```go
package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

// The three ways an auxiliary surface differs from every other. Everything else
// — the budget gate, health, credential rotation, classification, attempt
// records, commit semantics — belongs to the loop.
type (
	AuxBuild      func(context.Context, *adapter.Target, adapter.Adapter) (*http.Request, []ir.Warning, error)
	AuxRespond    func(*CommitWriter, *http.Response, *AttemptCtx) (adapter.Outcome, *ir.Error)
	AuxWriteError func(http.ResponseWriter, *ir.Error) error
)

// AuxOp is SurfaceOp with the boilerplate written once.
//
// Six surfaces differ in what they render and how they write, and are identical
// in everything else. Six near-duplicate types implementing four methods each
// would be thirty methods of ceremony around a dozen lines of real difference,
// and every one of them an opportunity to diverge on the parts that must not.
type AuxOp struct {
	dialect  string
	query    router.Query
	build    AuxBuild
	respond  AuxRespond
	writeErr AuxWriteError
}

func NewAuxOp(dialect string, q router.Query, build AuxBuild, respond AuxRespond, writeErr AuxWriteError) *AuxOp {
	return &AuxOp{dialect: dialect, query: q, build: build, respond: respond, writeErr: writeErr}
}

func (o *AuxOp) Dialect() string { return o.dialect }

func (o *AuxOp) Query() router.Query { return o.query }

func (o *AuxOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	return o.build(ctx, tgt, ad)
}

func (o *AuxOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	return o.respond(cw, resp, ac)
}

func (o *AuxOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.writeErr(w, e)
}

var _ SurfaceOp = (*AuxOp)(nil)

// ErrBodyTooLarge distinguishes an oversized response from a malformed one.
// They classify differently: an oversized body is a provider fault worth
// failing over, a syntax error may be a refusal shaped like one.
var ErrBodyTooLarge = errors.New("response body exceeds the cap")

// ReadCapped reads at most max bytes and reports ErrBodyTooLarge if there were
// more. It reads max+1 to tell "exactly at the cap" from "over it" — a response
// landing exactly on the boundary is legitimate and must not be rejected.
func ReadCapped(r io.Reader, max int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return buf, err
	}
	if int64(len(buf)) > max {
		return buf[:max], fmt.Errorf("%w: %d bytes", ErrBodyTooLarge, max)
	}
	return buf, nil
}

// DecodeJSON reads a bounded JSON document into v.
//
// Bounded because an unbounded read from a misbehaving provider is the hazard
// max_body_bytes prevents on the way in and nothing was preventing on the way
// out. The body is read whole rather than streamed into a decoder so the cap is
// enforced before any parsing work happens.
func DecodeJSON(r io.Reader, max int64, v any) error {
	buf, err := ReadCapped(r, max)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(buf, v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestAuxOp|TestReadCapped|TestDecodeJSON' -race -count=1 -v
```

Expected: PASS, seven tests.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing. Nothing constructs an `AuxOp` yet; Task 11 is its first user and is what proves the scaffold.

- [ ] **Step 6: Commit**

```bash
git add internal/exec/aux.go internal/exec/aux_test.go
git commit -m "feat(exec): add the auxiliary surface scaffold"
```

---

### Task 11: Embedding IR types and the adapter interface

**Files:**
- Create: `internal/ir/aux.go`
- Modify: `internal/adapter/adapter.go`
- Test: `internal/ir/aux_test.go`

**Interfaces:**
- Consumes: `ir.Usage`.
- Produces: `ir.EmbeddingRequest` (with both its `Input` and `Tokens` forms), `ir.EmbeddingResponse`, `ir.Embedding`, and `adapter.Embedder`. Task 12 implements the interface; Task 13 constructs the request.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: spec §3 fixes the field list and `TokenCounter` in the same file is the optional-interface precedent.

Spec §3: "Each type stays deliberately narrow. Forcing a six-field embedding call through the content-block message model would obscure both shapes and buy nothing."

The one field carrying real design is `Embedding`. OpenAI returns `embedding` as an **array of floats** when `encoding_format` is `float` and as a **base64 string** when it is `base64`, and a client that asked for base64 did so to avoid the decode. Holding the base64 verbatim rather than decoding into floats and re-encoding on the way out preserves the bytes exactly and skips two conversions on the largest payload any of these surfaces carries.

- [ ] **Step 1: Write the failing test**

Create `internal/ir/aux_test.go`:

```go
package ir

import "testing"

func TestEmbeddingCarriesEitherEncoding(t *testing.T) {
	// A client asking for base64 did so to avoid the decode. Holding the
	// string verbatim preserves the bytes and skips two conversions on the
	// largest payload these surfaces carry.
	f := Embedding{Index: 0, Float: []float32{0.1, 0.2}}
	if !f.IsFloat() || f.IsBase64() {
		t.Errorf("float embedding misreported: %+v", f)
	}
	b := Embedding{Index: 1, Base64: "AACAPwAAAEA="}
	if b.IsFloat() || !b.IsBase64() {
		t.Errorf("base64 embedding misreported: %+v", b)
	}
	var empty Embedding
	if empty.IsFloat() || empty.IsBase64() {
		t.Error("an empty embedding claimed an encoding")
	}
}

func TestEncodingOrDefault(t *testing.T) {
	// OpenAI's default is float when the field is absent, and a request that
	// omitted it must not be forwarded with an empty encoding_format.
	if got := (&EmbeddingRequest{}).EncodingOrDefault(); got != "float" {
		t.Errorf("default = %q, want float", got)
	}
	if got := (&EmbeddingRequest{Encoding: "base64"}).EncodingOrDefault(); got != "base64" {
		t.Errorf("explicit = %q", got)
	}
}

func TestEmbeddingRequestInputCount(t *testing.T) {
	// Logged per spec §9 as the input item count, and it is what makes a
	// batched call distinguishable from a single one in the trace.
	if got := (&EmbeddingRequest{Input: []string{"a", "b", "c"}}).InputCount(); got != 3 {
		t.Errorf("count = %d, want 3", got)
	}
	if got := (&EmbeddingRequest{}).InputCount(); got != 0 {
		t.Errorf("count = %d, want 0", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/ir/ -run 'TestEmbedding|TestEncodingOrDefault' -v
```

Expected: FAIL to build — `undefined: Embedding`, `undefined: EmbeddingRequest`.

- [ ] **Step 3: Write the types**

Create `internal/ir/aux.go`:

```go
package ir

// The auxiliary surfaces' request and response types.
//
// Each is deliberately narrow. Forcing a six-field embedding call through the
// content-block message model would obscure both shapes and buy nothing, so
// these do not reuse Request or Response — only Usage, which every surface that
// reports tokens reports in the same units.

// EmbeddingRequest is one batched embedding call.
type EmbeddingRequest struct {
	Model string
	// Input is the text form, always a slice. OpenAI accepts a bare string or
	// an array of strings and the edge flattens both here, so nothing
	// downstream branches on the inbound shape.
	Input []string
	// Tokens is the pre-tokenized form. OpenAI also accepts an array of
	// integers or an array of integer arrays, and those cannot be folded into
	// Input: Darkrouter has no detokenizer, and rendering token ids as text
	// would send a different request from the one the client made. Exactly one
	// of Input and Tokens is ever populated.
	Tokens [][]int
	// Encoding is "float" or "base64". Empty means the client did not say.
	Encoding string
	// Dimensions is 0 when unset. Zero is not a legal value, so it needs no
	// separate presence flag.
	Dimensions int
	User       string
}

// EncodingOrDefault is the encoding to send upstream. OpenAI's default when the
// field is absent is float, and forwarding an empty encoding_format would be a
// different request from the one the client made.
func (r *EmbeddingRequest) EncodingOrDefault() string {
	if r.Encoding == "" {
		return "float"
	}
	return r.Encoding
}

// InputCount is the batched item count, recorded on the request row per spec §9.
// It counts whichever form the client sent, because the row records how many
// items were embedded rather than which encoding carried them.
func (r *EmbeddingRequest) InputCount() int {
	if len(r.Tokens) > 0 {
		return len(r.Tokens)
	}
	return len(r.Input)
}

// Embedding is one vector. Exactly one of Float and Base64 is populated.
//
// A client that asked for base64 did so to avoid the decode, so the string is
// carried verbatim rather than decoded to floats and re-encoded on the way out:
// that preserves the bytes exactly and skips two conversions on the largest
// payload any auxiliary surface carries.
type Embedding struct {
	Index  int
	Float  []float32
	Base64 string
}

func (e Embedding) IsFloat() bool  { return len(e.Float) > 0 }
func (e Embedding) IsBase64() bool { return e.Base64 != "" }

type EmbeddingResponse struct {
	// Model is what the provider actually served, which may differ from what
	// was asked for. Spec §8: a failover to a different model returns vectors
	// from a different vector space, so the served name is not decoration.
	Model      string
	Embeddings []Embedding
	Usage      Usage
}
```

- [ ] **Step 4: Add the adapter interface**

In `internal/adapter/adapter.go`, beside `TokenCounter`:

```go
// Embedder is implemented by an adapter serving the embedding surface. Optional
// for the same reason TokenCounter is: two of the five kinds have no embedding
// endpoint, and a method on Adapter would make them stub it out.
type Embedder interface {
	BuildEmbedding(ctx context.Context, t *Target, req *ir.EmbeddingRequest) (*http.Request, []ir.Warning, error)
	// ParseEmbedding takes ownership of resp.Body and always closes it.
	ParseEmbedding(resp *http.Response) (*ir.EmbeddingResponse, error)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/ir/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing. Nothing implements `Embedder` yet.

- [ ] **Step 7: Commit**

```bash
git add internal/ir/aux.go internal/ir/aux_test.go internal/adapter/adapter.go
git commit -m "feat(ir): add embedding request and response types"
```

---

### Task 12: openaicompat serves embeddings

**Files:**
- Create: `internal/adapter/openaicompat/embed.go`
- Test: `internal/adapter/openaicompat/embed_test.go`

**Interfaces:**
- Consumes: `ir.EmbeddingRequest`, `ir.EmbeddingResponse`, `adapter.Embedder` (Task 11).
- Produces: `openaicompat.Adapter` implementing `adapter.Embedder`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: the wire shape is OpenAI's `/v1/embeddings`, and `build.go` beside it is the pattern for rendering one.

The response's `embedding` field is `[]float64` under `float` encoding and a `string` under `base64`. Decoding into `any` and type-switching is what handles both without asking the caller which it requested — and asking would be wrong anyway, because a provider is free to answer in the other one.

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/openaicompat/embed_test.go`:

```go
package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildEmbeddingRendersTheOpenAIShape(t *testing.T) {
	hr, warns, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://api.example.com/v1/", APIKey: "sk", Model: "text-embedding-3-small"},
		&ir.EmbeddingRequest{
			Input: []string{"a", "b"}, Encoding: "base64", Dimensions: 256, User: "u",
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v; nothing in this request is lossy", warns)
	}
	if hr.URL.String() != "https://api.example.com/v1/embeddings" {
		t.Errorf("url = %s", hr.URL)
	}
	if hr.Header.Get("Authorization") != "Bearer sk" {
		t.Errorf("auth = %q", hr.Header.Get("Authorization"))
	}

	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "text-embedding-3-small" {
		t.Errorf("model = %v; the target's name must be sent, not the client's", body["model"])
	}
	if body["encoding_format"] != "base64" {
		t.Errorf("encoding_format = %v", body["encoding_format"])
	}
	if body["dimensions"].(float64) != 256 {
		t.Errorf("dimensions = %v", body["dimensions"])
	}
	in, ok := body["input"].([]any)
	if !ok || len(in) != 2 {
		t.Errorf("input = %v", body["input"])
	}
}

func TestBuildEmbeddingForwardsTokenInput(t *testing.T) {
	// A client that sent token ids gets token ids sent upstream. There is no
	// detokenizer here, so the alternative is not "render as text" — it is
	// silently embedding something else.
	hr, _, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "m"},
		&ir.EmbeddingRequest{Tokens: [][]int{{1, 2, 3}, {4}}})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	outer, ok := body["input"].([]any)
	if !ok || len(outer) != 2 {
		t.Fatalf("input = %v, want two token arrays", body["input"])
	}
	inner, ok := outer[0].([]any)
	if !ok || len(inner) != 3 || inner[0].(float64) != 1 {
		t.Errorf("input[0] = %v, want [1,2,3]", outer[0])
	}
}

func TestBuildEmbeddingOmitsUnsetOptionals(t *testing.T) {
	// dimensions is not a legal zero and an explicit 0 is a 400 on OpenAI.
	hr, _, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "m"},
		&ir.EmbeddingRequest{Input: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	_ = json.Unmarshal(raw, &body)
	if _, present := body["dimensions"]; present {
		t.Error("an unset dimensions was sent")
	}
	if _, present := body["user"]; present {
		t.Error("an unset user was sent")
	}
	// The encoding is always sent: omitting it would be a different request
	// from the one the client made once the default is applied downstream.
	if body["encoding_format"] != "float" {
		t.Errorf("encoding_format = %v, want the applied default", body["encoding_format"])
	}
}

func jsonResp(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestParseEmbeddingReadsFloatVectors(t *testing.T) {
	out, err := New().ParseEmbedding(jsonResp(`{
	  "object":"list","model":"text-embedding-3-small",
	  "data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]},
	          {"object":"embedding","index":1,"embedding":[0.3]}],
	  "usage":{"prompt_tokens":7,"total_tokens":7}}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Model != "text-embedding-3-small" {
		t.Errorf("model = %q; the served name is what spec §8 needs", out.Model)
	}
	if len(out.Embeddings) != 2 {
		t.Fatalf("got %d vectors", len(out.Embeddings))
	}
	if !out.Embeddings[0].IsFloat() || len(out.Embeddings[0].Float) != 2 {
		t.Errorf("vector 0 = %+v", out.Embeddings[0])
	}
	if out.Embeddings[1].Index != 1 {
		t.Errorf("index = %d; the order is the client's batch order", out.Embeddings[1].Index)
	}
	// ir.Usage is Darkrouter's vocabulary, not OpenAI's: prompt_tokens maps to
	// InputTokens, which is what the rollup and the cost calculation read.
	if out.Usage.InputTokens != 7 {
		t.Errorf("usage = %+v", out.Usage)
	}
}

func TestParseEmbeddingReadsBase64Vectors(t *testing.T) {
	// A provider may answer in base64 whatever was asked, so the parser reads
	// the shape it received rather than the shape it requested.
	out, err := New().ParseEmbedding(jsonResp(`{
	  "object":"list","model":"m",
	  "data":[{"object":"embedding","index":0,"embedding":"AACAPwAAAEA="}],
	  "usage":{"prompt_tokens":2,"total_tokens":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Embeddings[0].IsBase64() {
		t.Fatalf("vector = %+v, want base64", out.Embeddings[0])
	}
	if out.Embeddings[0].Base64 != "AACAPwAAAEA=" {
		t.Errorf("base64 = %q; it must survive verbatim", out.Embeddings[0].Base64)
	}
}

func TestParseEmbeddingRejectsAnEmptyList(t *testing.T) {
	// A 200 carrying no vectors is a provider fault, not an empty answer: the
	// client asked for N and got none, and failing over is the right response.
	if _, err := New().ParseEmbedding(jsonResp(`{"object":"list","data":[]}`)); err == nil {
		t.Error("an empty data array parsed cleanly")
	}
}

func TestParseEmbeddingClosesTheBody(t *testing.T) {
	// The interface says ParseEmbedding takes ownership. A leaked body holds a
	// connection out of the pool for the process's lifetime.
	rc := &closeTracker{Reader: strings.NewReader(`{"data":[{"index":0,"embedding":[1]}]}`)}
	resp := &http.Response{StatusCode: 200, Body: rc, Header: http.Header{}}
	if _, err := New().ParseEmbedding(resp); err != nil {
		t.Fatal(err)
	}
	if !rc.closed {
		t.Error("ParseEmbedding did not close the body")
	}
}

type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error { c.closed = true; return nil }
```

If `closeTracker` already exists in the package's tests, use it rather than redeclaring.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/openaicompat/ -run 'TestBuildEmbedding|TestParseEmbedding' -v
```

Expected: FAIL to build — `New().BuildEmbedding undefined`.

- [ ] **Step 3: Write the adapter**

Create `internal/adapter/openaicompat/embed.go`:

```go
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// maxEmbeddingBytes bounds the response. A batched float embedding is the
// largest payload any auxiliary surface carries — 2048 vectors of 3072 float64s
// is tens of megabytes — so the cap is generous, but an unbounded read from a
// misbehaving provider is the hazard it exists to stop.
const maxEmbeddingBytes = 128 << 20

func (a *Adapter) BuildEmbedding(ctx context.Context, t *adapter.Target,
	req *ir.EmbeddingRequest) (*http.Request, []ir.Warning, error) {

	body := map[string]any{
		"model": t.Model,
		// Always sent: applying the default downstream and omitting it here
		// would make the upstream request differ from the client's.
		"encoding_format": req.EncodingOrDefault(),
	}
	// A pre-tokenized input is forwarded as token ids. Rendering it as text is
	// not possible — there is no detokenizer here — and sending the text form
	// of something the client sent as tokens would be a different request.
	if len(req.Tokens) > 0 {
		body["input"] = req.Tokens
	} else {
		body["input"] = req.Input
	}
	// Neither is a legal zero, so absence needs no separate presence flag — and
	// an explicit dimensions of 0 is a 400 on OpenAI.
	if req.Dimensions > 0 {
		body["dimensions"] = req.Dimensions
	}
	if req.User != "" {
		body["user"] = req.User
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/embeddings"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build embedding request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil, nil
}

// embeddingEnvelope decodes the vector into any, because OpenAI returns an
// array of numbers under float encoding and a string under base64 — and a
// provider is free to answer in the other one whatever was asked.
type embeddingEnvelope struct {
	Model string `json:"model"`
	Data  []struct {
		Index     int `json:"index"`
		Embedding any `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func (a *Adapter) ParseEmbedding(resp *http.Response) (*ir.EmbeddingResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbeddingBytes))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	var env embeddingEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}
	if len(env.Data) == 0 {
		// A 200 carrying no vectors is a provider fault: the client asked for
		// N and got none, so failing over is the right answer rather than
		// handing back an empty list that looks like a valid response.
		return nil, errors.New("embedding response carried no vectors")
	}

	out := &ir.EmbeddingResponse{
		Model:      env.Model,
		Embeddings: make([]ir.Embedding, 0, len(env.Data)),
		// Embeddings report input tokens only, per spec §9. OutputTokens stays
		// zero rather than borrowing total_tokens, which would double-count
		// the input in the daily rollup.
		Usage: ir.Usage{InputTokens: env.Usage.PromptTokens},
	}
	for _, d := range env.Data {
		e := ir.Embedding{Index: d.Index}
		switch v := d.Embedding.(type) {
		case string:
			// Carried verbatim: the client asked for base64 to avoid the
			// decode, and round-tripping through floats would undo that.
			e.Base64 = v
		case []any:
			e.Float = make([]float32, 0, len(v))
			for _, n := range v {
				f, ok := n.(float64)
				if !ok {
					return nil, fmt.Errorf("embedding %d contains a non-numeric component", d.Index)
				}
				e.Float = append(e.Float, float32(f))
			}
		default:
			return nil, fmt.Errorf("embedding %d has an unrecognized encoding", d.Index)
		}
		out.Embeddings = append(out.Embeddings, e)
	}
	return out, nil
}

var _ adapter.Embedder = (*Adapter)(nil)
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/openaicompat/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
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
git add internal/adapter/openaicompat/embed.go internal/adapter/openaicompat/embed_test.go
git commit -m "feat(openaicompat): render and parse embeddings"
```

---

### Task 13: The OpenAI embedding edge

**Files:**
- Modify: `internal/edge/edge.go`
- Create: `internal/edge/openai/aux.go`
- Test: `internal/edge/openai/aux_test.go`

**Interfaces:**
- Consumes: `ir.EmbeddingRequest`, `ir.EmbeddingResponse`, `ir.Embedding` (Task 11).
- Produces: `edge.EmbeddingDialect`, `openai.ParseEmbedding`, `openai.WriteEmbedding`, and both as methods on `openai.Dialect`. Task 15's route parses and writes through them.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: the wire shape is OpenAI's own `/v1/embeddings` and `parse.go` beside it is the pattern for reading one.

`input` is a **union of four shapes** and there is no Go type that decodes all of them: a bare string, an array of strings, a bare token array, and an array of token arrays. The shape is decided from the first byte and decoded once it is known.

Guessing wrong here is not a formatting nuisance. A token array read as text embeds the literal digits, the call succeeds, and the vector is silently wrong — the client has no way to detect it. That is the same failure class as spec §8's cross-model failover, arriving one layer earlier.

`EmbeddingDialect` is a separate interface rather than four more methods on `Dialect`. The two shapes share nothing: an embedding request has no messages and its response has no content blocks, and every dialect that does not serve embeddings — Anthropic and Gemini both — would have to stub four methods to say so.

- [ ] **Step 1: Write the failing test**

Create `internal/edge/openai/aux_test.go`:

```go
package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func embedReq(t *testing.T, body string) *ir.EmbeddingRequest {
	t.Helper()
	req, err := ParseEmbedding(httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(body)), 1<<20)
	if err != nil {
		t.Fatalf("ParseEmbedding(%s): %v", body, err)
	}
	return req
}

func TestParseEmbeddingNormalizesABareString(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":"hello"}`)
	if len(req.Input) != 1 || req.Input[0] != "hello" {
		t.Errorf("Input = %v, want one element", req.Input)
	}
	if len(req.Tokens) != 0 {
		t.Errorf("Tokens = %v; a string input is not tokens", req.Tokens)
	}
}

func TestParseEmbeddingNormalizesAStringArray(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":["a","b","c"]}`)
	if len(req.Input) != 3 || req.Input[2] != "c" {
		t.Errorf("Input = %v", req.Input)
	}
}

func TestParseEmbeddingCarriesAFlatTokenArray(t *testing.T) {
	// A flat array of integers is ONE token array, not many. Reading it as
	// many would send each token id as a separate embedding input.
	req := embedReq(t, `{"model":"m","input":[15496,11,995]}`)
	if len(req.Input) != 0 {
		t.Errorf("Input = %v; token ids must not become text", req.Input)
	}
	if len(req.Tokens) != 1 || len(req.Tokens[0]) != 3 || req.Tokens[0][0] != 15496 {
		t.Errorf("Tokens = %v, want one array of three", req.Tokens)
	}
}

func TestParseEmbeddingCarriesNestedTokenArrays(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":[[1,2],[3]]}`)
	if len(req.Tokens) != 2 || len(req.Tokens[1]) != 1 || req.Tokens[1][0] != 3 {
		t.Errorf("Tokens = %v", req.Tokens)
	}
}

func TestParseEmbeddingReadsTheOptionals(t *testing.T) {
	req := embedReq(t, `{"model":"m","input":"x","encoding_format":"base64","dimensions":256,"user":"u"}`)
	if req.Model != "m" || req.Encoding != "base64" || req.Dimensions != 256 || req.User != "u" {
		t.Errorf("request = %+v", req)
	}
}

func TestParseEmbeddingRejectsAMissingOrEmptyInput(t *testing.T) {
	// An absent input is a client bug and must be a 400 rather than an
	// upstream call that fails less legibly.
	for _, body := range []string{
		`{"model":"m"}`,
		`{"model":"m","input":null}`,
		`{"model":"m","input":[]}`,
		`{"model":"m","input":7}`,
	} {
		if _, err := ParseEmbedding(httptest.NewRequest("POST", "/v1/embeddings",
			strings.NewReader(body)), 1<<20); err == nil {
			t.Errorf("ParseEmbedding(%s) accepted an unusable input", body)
		}
	}
}

func TestParseEmbeddingEnforcesTheBodyCap(t *testing.T) {
	big := `{"model":"m","input":"` + strings.Repeat("x", 200) + `"}`
	if _, err := ParseEmbedding(httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(big)), 64); err == nil {
		t.Error("an oversized body was accepted")
	}
}

func TestWriteEmbeddingEmitsFloatVectors(t *testing.T) {
	w := httptest.NewRecorder()
	err := WriteEmbedding(w, &ir.EmbeddingResponse{
		Model: "text-embedding-3-small",
		Embeddings: []ir.Embedding{
			{Index: 0, Float: []float32{0.5, -0.25}},
			{Index: 1, Float: []float32{1}},
		},
		Usage: ir.Usage{InputTokens: 9},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Object string `json:"object"`
		Model  string `json:"model"`
		Data   []struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" || body.Model != "text-embedding-3-small" {
		t.Errorf("envelope = %+v", body)
	}
	if len(body.Data) != 2 || body.Data[0].Object != "embedding" || body.Data[1].Index != 1 {
		t.Fatalf("data = %+v", body.Data)
	}
	if body.Data[0].Embedding[1] != -0.25 {
		t.Errorf("vector = %v", body.Data[0].Embedding)
	}
	if body.Usage.PromptTokens != 9 || body.Usage.TotalTokens != 9 {
		t.Errorf("usage = %+v; embeddings report input tokens only", body.Usage)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestWriteEmbeddingCarriesBase64Verbatim(t *testing.T) {
	// The client asked for base64 to avoid the decode. Re-encoding through
	// floats would change the bytes it receives.
	w := httptest.NewRecorder()
	if err := WriteEmbedding(w, &ir.EmbeddingResponse{
		Model:      "m",
		Embeddings: []ir.Embedding{{Index: 0, Base64: "AACAPwAAAEA="}},
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), `"embedding":"AACAPwAAAEA="`) {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestWriteEmbeddingNeverEmitsANullVector(t *testing.T) {
	// An OpenAI client indexes into this array. null there is a crash, not an
	// empty vector.
	w := httptest.NewRecorder()
	if err := WriteEmbedding(w, &ir.EmbeddingResponse{
		Model:      "m",
		Embeddings: []ir.Embedding{{Index: 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "null") {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestDialectServesTheEmbeddingSurface(t *testing.T) {
	var _ interface {
		ParseEmbedding(*http.Request, int64) (*ir.EmbeddingRequest, error)
		WriteEmbedding(http.ResponseWriter, *ir.EmbeddingResponse) error
	} = New()
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ -run 'TestParseEmbedding|TestWriteEmbedding|TestDialectServes' -v
```

Expected: FAIL to build — `undefined: ParseEmbedding`, `undefined: WriteEmbedding`.

- [ ] **Step 3: Declare the interface**

Add to `internal/edge/edge.go`:

```go
// EmbeddingDialect is the inbound wire form of the embedding surface.
//
// It is a separate interface rather than more methods on Dialect because the
// two shapes share nothing — an embedding request has no messages and its
// response has no content blocks — and Anthropic and Gemini would each stub
// four methods to say they do not serve it.
type EmbeddingDialect interface {
	Name() string
	ParseEmbedding(r *http.Request, maxBody int64) (*ir.EmbeddingRequest, error)
	WriteEmbedding(w http.ResponseWriter, resp *ir.EmbeddingResponse) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}
```

- [ ] **Step 4: Write the parser and the writer**

Create `internal/edge/openai/aux.go`:

```go
package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

// wireEmbeddingRequest holds input as raw JSON deliberately: the field is a
// union of four shapes and decoding it into any concrete Go type rejects three
// of them.
type wireEmbeddingRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	EncodingFormat string          `json:"encoding_format"`
	Dimensions     *int            `json:"dimensions"`
	User           string          `json:"user"`
}

func ParseEmbedding(r *http.Request, maxBody int64) (*ir.EmbeddingRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBody {
		return nil, &ir.Error{
			Type:    ir.ErrPayloadTooLarge,
			Message: fmt.Sprintf("request body exceeds %d bytes", maxBody),
		}
	}
	var w wireEmbeddingRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	texts, tokens, err := parseEmbeddingInput(w.Input)
	if err != nil {
		return nil, err
	}
	req := &ir.EmbeddingRequest{
		Model:    w.Model,
		Input:    texts,
		Tokens:   tokens,
		Encoding: w.EncodingFormat,
		User:     w.User,
	}
	if w.Dimensions != nil {
		req.Dimensions = *w.Dimensions
	}
	return req, nil
}

// parseEmbeddingInput normalizes OpenAI's four accepted input shapes: a bare
// string, an array of strings, a bare token array, and an array of token
// arrays.
//
// The shape is decided from the first byte because no Go type decodes all four.
// Guessing wrong is not a formatting nuisance — a token array read as text
// embeds the literal digits, the call succeeds, and the client has no way to
// detect that the vector is wrong.
func parseEmbeddingInput(raw json.RawMessage) ([]string, [][]int, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil, errors.New("input is required")
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, nil, fmt.Errorf("input: %w", err)
		}
		return []string{s}, nil, nil
	case '[':
		return parseEmbeddingArray(trimmed)
	default:
		return nil, nil, errors.New("input must be a string or an array")
	}
}

func parseEmbeddingArray(trimmed []byte) ([]string, [][]int, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, nil, fmt.Errorf("input: %w", err)
	}
	if len(items) == 0 {
		return nil, nil, errors.New("input is empty")
	}
	first := bytes.TrimSpace(items[0])
	if len(first) == 0 {
		return nil, nil, errors.New("input contains an empty element")
	}
	switch first[0] {
	case '"':
		out := make([]string, 0, len(items))
		for i, it := range items {
			var s string
			if err := json.Unmarshal(it, &s); err != nil {
				return nil, nil, fmt.Errorf("input[%d]: %w", i, err)
			}
			out = append(out, s)
		}
		return out, nil, nil
	case '[':
		out := make([][]int, 0, len(items))
		for i, it := range items {
			var toks []int
			if err := json.Unmarshal(it, &toks); err != nil {
				return nil, nil, fmt.Errorf("input[%d]: %w", i, err)
			}
			out = append(out, toks)
		}
		return nil, out, nil
	default:
		// A flat array of integers is one token array, not many: reading it as
		// many would ask for one embedding per token id.
		var toks []int
		if err := json.Unmarshal(trimmed, &toks); err != nil {
			return nil, nil, fmt.Errorf("input: %w", err)
		}
		return nil, [][]int{toks}, nil
	}
}

func WriteEmbedding(w http.ResponseWriter, resp *ir.EmbeddingResponse) error {
	data := make([]any, 0, len(resp.Embeddings))
	for _, e := range resp.Embeddings {
		row := map[string]any{"object": "embedding", "index": e.Index}
		if e.IsBase64() {
			row["embedding"] = e.Base64
		} else {
			// Never nil: an OpenAI client indexes into this array, and null
			// there is a crash rather than an empty vector.
			v := e.Float
			if v == nil {
				v = []float32{}
			}
			row["embedding"] = v
		}
		data = append(data, row)
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
		"model":  resp.Model,
		// Embeddings report input tokens only, so total equals prompt unless a
		// provider volunteered an output count. Adding them rather than
		// hardcoding equality keeps an honest total when one does.
		"usage": map[string]any{
			"prompt_tokens": resp.Usage.InputTokens,
			"total_tokens":  resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	})
}

func (d *Dialect) ParseEmbedding(r *http.Request, maxBody int64) (*ir.EmbeddingRequest, error) {
	return ParseEmbedding(r, maxBody)
}

func (d *Dialect) WriteEmbedding(w http.ResponseWriter, resp *ir.EmbeddingResponse) error {
	return WriteEmbedding(w, resp)
}

var _ edge.EmbeddingDialect = (*Dialect)(nil)
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package including phase 1's chat tests.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/edge/edge.go internal/edge/openai/aux.go internal/edge/openai/aux_test.go
git commit -m "feat(edge): parse and write the embedding shape"
```

---

### Task 14: RunAux, the auxiliary entry point

**Files:**
- Modify: `internal/exec/surface.go`
- Test: `internal/exec/surface_test.go`

**Interfaces:**
- Consumes: `SurfaceOp`, `resolve` (Tasks 4, 6).
- Produces: `(*Executor).RunAux(w, r, dialect string, surface ir.Surface, ew errorWriter, build func(*config.Config) (SurfaceOp, error))`. Tasks 15, 16, 19, 20, 23 and 24 each call it exactly once.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: it is `RunSurface` with the parse step moved inside the record's lifetime, and both halves already exist.

`RunSurface` takes an op, so a route must parse its body **before** calling it — and a parse failure would then produce no request row at all. Chat does not behave that way: `Handle` opens the record first and parses second, precisely so that a malformed body is a request the gateway received and refused rather than a request that never happened. Six routes silently dropping their 400s from the log would be a real regression in the only place an operator can see them.

So the parse becomes a closure that runs inside the record's lifetime. `RunSurface` keeps its signature — Task 6's tests call it directly — and both share the tail.

- [ ] **Step 1: Write the failing test**

Create `internal/exec/surface_test.go`:

```go
package exec

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

func TestRunAuxLogsAMalformedBody(t *testing.T) {
	// Handle opens its record before parsing, so a 400 is a logged request.
	// Six auxiliary routes parsing first would drop theirs from the only place
	// an operator can see them.
	e, rec := executorForOp(t, "http://127.0.0.1:1", nil)
	w := httptest.NewRecorder()

	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceEmbedding}}
	e.RunAux(w, httptest.NewRequest("POST", "/v1/embeddings", nil),
		"openai", ir.SurfaceEmbedding, op,
		func(*config.Config) (SurfaceOp, error) { return nil, errors.New("input is required") })

	got := rec.only(t)
	if got.Surface != string(ir.SurfaceEmbedding) {
		t.Errorf("surface = %q", got.Surface)
	}
	if got.Dialect != "openai" {
		t.Errorf("dialect = %q", got.Dialect)
	}
	if got.ErrorCode != string(ir.ErrInvalidRequest) {
		t.Errorf("error code = %q, want %q", got.ErrorCode, ir.ErrInvalidRequest)
	}
	if got.Status != "error" {
		t.Errorf("status = %q", got.Status)
	}
	if got.ID == "" || w.Header().Get("X-Darkrouter-Request") != got.ID {
		t.Errorf("request id header = %q, record id = %q",
			w.Header().Get("X-Darkrouter-Request"), got.ID)
	}
	if len(got.Attempts) != 0 {
		t.Errorf("attempts = %d; a body that never parsed reached an upstream", len(got.Attempts))
	}
}

func TestRunAuxRunsTheOpWhenTheBodyParses(t *testing.T) {
	upstream := httptest.NewServer(jsonOK())
	defer upstream.Close()

	cat := catalogWith("p", "m", ir.SurfaceEmbedding)
	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceEmbedding}}
	e, rec := executorForOp(t, upstream.URL, cat)

	e.RunAux(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/embeddings", nil),
		"openai", ir.SurfaceEmbedding, op,
		func(*config.Config) (SurfaceOp, error) { return op, nil })

	if op.builds != 1 || op.responds != 1 {
		t.Fatalf("builds = %d, responds = %d", op.builds, op.responds)
	}
	if got := rec.only(t); got.Status != "success" {
		t.Errorf("status = %q", got.Status)
	}
}
```

Add two helpers beside them, used by this task and every surface task after it:

- `jsonOK()` returns an `http.HandlerFunc` writing `{}` with `Content-Type: application/json`.
- `catalogWith(providerID, modelID string, surfaces ...ir.Surface) *catalog.Store` builds a one-model live snapshot declaring those surfaces. Task 6's `executorForOp` already accepts a `*catalog.Store`, and its `probe` kind is registered as `openaicompat.New()`.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run TestRunAux -v
```

Expected: FAIL to build — `e.RunAux undefined`.

- [ ] **Step 3: Split RunSurface and add RunAux**

In `internal/exec/surface.go`, replace `RunSurface`'s body with a call to a shared tail and add `RunAux` beside it:

```go
// RunSurface is the entry point for a route whose request is already parsed.
// Handle uses it; Task 6's seam tests drive an op through it directly.
func (e *Executor) RunSurface(w http.ResponseWriter, r *http.Request, op SurfaceOp) {
	start := time.Now()
	rec, done := e.newRecord(start, op)
	defer done()
	e.beginResponse(w, rec)
	e.runOp(w, r, op, rec, start)
}

// RunAux is RunSurface with the parse step moved inside the record's lifetime.
//
// A route that parsed first would produce no request row for a malformed body,
// and chat does not behave that way: Handle opens its record before parsing so
// that a 400 is a request the gateway received and refused rather than one that
// never happened. Six routes dropping their 400s from the log would be a real
// regression in the only place an operator can see them.
//
// ew rather than the op writes the error, because on a parse failure there is
// no op yet — the dialect is what knows the client's error shape.
func (e *Executor) RunAux(w http.ResponseWriter, r *http.Request,
	dialect string, surface ir.Surface, ew errorWriter,
	build func(cfg *config.Config) (SurfaceOp, error)) {

	start := time.Now()
	rec := &store.RequestRecord{
		ID:      ulid.MustNew(ulid.Timestamp(start), rand.Reader).String(),
		TS:      start,
		Dialect: dialect,
		Surface: string(surface),
		Status:  "error",
	}
	defer func() {
		total := time.Since(start).Milliseconds()
		rec.TotalMs = &total
		e.log(rec)
	}()
	e.beginResponse(w, rec)

	cfg := e.store.Current() // one snapshot for this request's whole lifetime
	op, err := build(cfg)
	if err != nil {
		rec.ErrorCode = string(ir.ErrInvalidRequest)
		_ = ew.WriteError(w, &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()})
		return
	}
	e.runOp(w, r, op, rec, start, cfg)
}

// beginResponse sets the two headers every route emits before it knows whether
// it will succeed. Attempts is overwritten by the diagnostics on both the
// success and the error path; the zero here is what a response that never
// attempted anything carries.
func (e *Executor) beginResponse(w http.ResponseWriter, rec *store.RequestRecord) {
	w.Header().Set("X-Darkrouter-Request", rec.ID)
	w.Header().Set("X-Darkrouter-Attempts", "0")
}

func (e *Executor) runOp(w http.ResponseWriter, r *http.Request, op SurfaceOp,
	rec *store.RequestRecord, start time.Time, cfg *config.Config) {

	res, ok := e.resolve(r.Context(), w, op, op.Query(), rec, cfg, start)
	if !ok {
		return
	}
	e.runAttempts(w, r, op, cfg, res.Candidates, rec, start, res.ByID, res.Catalog)
}
```

`newRecord` from Task 6 keeps its two callers by delegating its own record construction to the same fields listed above; the duplication is four lines and folding it into a shared constructor that takes both an op and a loose dialect string reads worse than either.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package. `RunSurface`'s behavior is unchanged — the config snapshot simply moved one call deeper.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/exec/surface.go internal/exec/surface_test.go
git commit -m "feat(exec): log a malformed auxiliary request body"
```

---

### Task 15: The embeddings op, route, and the cross-model warning

**Files:**
- Create: `internal/exec/embed.go`
- Modify: `internal/server/server.go`
- Test: `internal/exec/embed_test.go`

**Interfaces:**
- Consumes: `RunAux` (Task 14), `adapter.Embedder` (Task 11), `edge.EmbeddingDialect` (Task 13).
- Produces: `(*Executor).HandleEmbeddings(w, r, d edge.EmbeddingDialect)` and the `POST /v1/embeddings` route. Tasks 16 onward copy this file's shape.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4
**Approach:** inline - skip 2: the op's four methods are fixed by `SurfaceOp` and its `Respond` is chat's unary tail with the embedding types substituted.

This is the first surface to run end to end, so it is the one that proves the pipeline. Everything after it is the same file with different types.

Risk is 2 because of spec §8. An embedding request that fails over to a **different model** returns vectors from a different vector space; a client filling an index across that failover corrupts it, and **nothing in the response body signals it**. Darkrouter permits the failover — refusing it would make an embedding alias useless the moment its first provider rate-limits — and records a warning naming both models. The comparison is against the **first candidate's model**, not the name the client sent, because that name is usually an alias and often not a model name at all.

The first candidate's model comes from `AttemptCtx.FirstModel`, which Task 6's loop fills from `cands[0]`. The op deliberately does **not** infer it from its own first `Build` call: a first candidate skipped by the live cooling re-check never reaches `Build`, and that — primary cooling, secondary with a different model serving — is precisely the case the warning exists for.

- [ ] **Step 1: Write the failing test**

Create `internal/exec/embed_test.go`:

```go
package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

// embedUpstream answers /v1/embeddings with one vector, reporting the model it
// was asked for so a test can tell which candidate served.
func embedUpstream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","model":"` + in.Model +
			`","data":[{"object":"embedding","index":0,"embedding":[0.5,0.25]}],` +
			`"usage":{"prompt_tokens":4,"total_tokens":4}}`))
	}
}

func TestEmbeddingsServeEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(embedUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "e5", ir.SurfaceEmbedding))
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"e5","input":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Object string `json:"object"`
		Data   []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" || len(body.Data) != 1 || body.Data[0].Embedding[0] != 0.5 {
		t.Fatalf("body = %s", w.Body.String())
	}
	got := rec.only(t)
	if got.Surface != "embedding" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
	if got.TokensIn != 4 || got.TokensOut != 0 {
		t.Errorf("tokens = %d in, %d out; embeddings report input only", got.TokensIn, got.TokensOut)
	}
	if w.Header().Get("X-Darkrouter-Model") != "e5" {
		t.Errorf("X-Darkrouter-Model = %q; spec §8 requires it always",
			w.Header().Get("X-Darkrouter-Model"))
	}
}

func TestEmbeddingsFailOverToASecondProvider(t *testing.T) {
	var hits atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(embedUpstream())
	defer good.Close()

	e, rec := executorForTwo(t, bad.URL, good.URL,
		catalogPair("bad", "good", "e5", ir.SurfaceEmbedding))
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"e5","input":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("the failing provider was called %d times", hits.Load())
	}
	got := rec.only(t)
	if len(got.Attempts) != 2 || got.FinalProviderID != "good" {
		t.Errorf("attempts = %d, final = %q", len(got.Attempts), got.FinalProviderID)
	}
	for _, warn := range got.Warnings {
		if strings.Contains(warn, "vector space") {
			t.Errorf("a same-model failover raised the cross-model warning: %q", warn)
		}
	}
}

func TestACrossModelEmbeddingFailoverWarns(t *testing.T) {
	// Spec §8: the vectors come from a different vector space and nothing in
	// the body says so. The warning on the request row is the only record.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(embedUpstream())
	defer good.Close()

	e, rec := executorForTwo(t, bad.URL, good.URL,
		catalogAlias("bad", "e5-small", "good", "e5-large", ir.SurfaceEmbedding))
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"embed","input":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	got := rec.only(t)
	var found string
	for _, warn := range got.Warnings {
		if strings.Contains(warn, "vector space") {
			found = warn
		}
	}
	if found == "" {
		t.Fatalf("no cross-model warning in %v", got.Warnings)
	}
	if !strings.Contains(found, "e5-small") || !strings.Contains(found, "e5-large") {
		t.Errorf("warning = %q; it must name both models or it cannot be acted on", found)
	}
	if w.Header().Get("X-Darkrouter-Model") != "e5-large" {
		t.Errorf("X-Darkrouter-Model = %q, want the model that actually served",
			w.Header().Get("X-Darkrouter-Model"))
	}
}

func TestAnEmbeddingRequestWithNoEmbeddingProviderIsRefused(t *testing.T) {
	// Spec §10's third per-surface case: nothing is attempted and the error
	// names the fact.
	upstream := httptest.NewServer(embedUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleEmbeddings(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"chat-only","input":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "surface") {
		t.Errorf("body = %s; the error must name the surface as the reason", w.Body.String())
	}
	got := rec.only(t)
	if len(got.Attempts) != 0 {
		t.Errorf("attempts = %d; a surface no provider offers must attempt nothing", len(got.Attempts))
	}
}
```

Add three catalog helpers beside `catalogWith`, each returning a `*catalog.Store` holding one live snapshot:

- `catalogPair(a, b, model string, surfaces ...ir.Surface)` — the same model on two providers, `a` first.
- `catalogAlias(aProvider, aModel, bProvider, bModel string, surfaces ...ir.Surface)` — two *different* models behind one alias. Aliases live in configuration as `map[string][]string` of `provider/model` strings, not in the catalog, so this helper cannot declare one on its own: have it return the store **and** the two `provider/model` strings, and have `executorForTwo` write them into the config it builds under the alias name **`embed`**. Every test that wants a cross-model failover requests the model `embed`, on whichever surface — the name is the alias, not the surface.
- `executorForTwo(t, urlA, urlB string, cat *catalog.Store)` — `executorForOp` with two providers, both of kind `probe`, in the order given.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestEmbeddings|TestACrossModel|TestAnEmbeddingRequestWithNo' -v
```

Expected: FAIL to build — `e.HandleEmbeddings undefined`.

- [ ] **Step 3: Write the op and the route**

Create `internal/exec/embed.go`:

```go
package exec

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

// embedOp is the embedding surface. It implements SurfaceOp directly rather
// than wrapping AuxOp because its Respond does one thing no other surface does.
type embedOp struct {
	d   edge.EmbeddingDialect
	req *ir.EmbeddingRequest
}

func (o *embedOp) Dialect() string { return o.d.Name() }

// Query sets no capability needs: an embedding request does not ask for tools,
// vision or reasoning, and requiring them would filter out every real embedding
// model.
func (o *embedOp) Query() router.Query {
	return router.Query{Model: o.req.Model, Surface: ir.SurfaceEmbedding}
}

func (o *embedOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	em, ok := ad.(adapter.Embedder)
	if !ok {
		// Unreachable through the router, which filters on adapter surfaces.
		// It is checked anyway because the alternative to failing here is
		// sending a chat body to an embedding endpoint.
		return nil, nil, fmt.Errorf("adapter %s does not serve embeddings", ad.Kind())
	}
	return em.BuildEmbedding(ctx, tgt, o.req)
}

func (o *embedOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	em, ok := ac.Adapter.(adapter.Embedder)
	if !ok {
		resp.Body.Close()
		return adapter.OutcomeFatal, &ir.Error{
			Type: ir.ErrDarkrouter, Message: "adapter does not serve embeddings",
		}
	}
	out, err := em.ParseEmbedding(resp)
	if err != nil {
		return failedParse(ac, resp, err)
	}

	// Spec §8. The comparison is against the first candidate the router
	// produced — supplied by the loop, not inferred from this op's own Build
	// calls, because a first candidate skipped by the live cooling re-check
	// never reaches Build and is exactly when this warning must still fire.
	warns := ac.Warns
	if ac.FirstModel != "" && ac.Cand.Model != ac.FirstModel {
		warns = append(warns, ir.Warning{
			Field:  "model",
			Target: ac.Cand.ProviderID + "/" + ac.Cand.Model,
			Reason: "embeddings served by " + ac.Cand.Model + " after " + ac.FirstModel +
				" was not used; vectors from two models are not in the same vector space " +
				"and an index filled across this failover is corrupt",
		})
	}

	ttft := time.Since(ac.Rec.TS).Milliseconds()
	ac.Rec.TTFTMs = &ttft
	applyUsage(ac.Rec, &out.Usage)
	ac.Rec.FinalProviderID = ac.Cand.ProviderID
	ac.Rec.FinalModel = ac.Cand.Model
	// Assigned, not appended: the record must describe the translation the
	// client received, not every attempt abandoned on the way there.
	ac.Rec.Warnings = warningStrings(warns)

	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	_ = o.d.WriteEmbedding(cw, out)
	return adapter.OutcomeSuccess, nil
}

func (o *embedOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*embedOp)(nil)

// failedParse is chat's parse-failure tail, shared by every auxiliary surface.
//
// A 2xx that cannot be read is a provider fault, so it rejoins the outcome path
// and signals health. A refusal is not: recording it would trip the breaker on
// a healthy provider, and failing over would re-ask a question every model in
// the chain will refuse.
func failedParse(ac *AttemptCtx, resp *http.Response, err error) (adapter.Outcome, *ir.Error) {
	outcome := outcomeForParseError(err)
	if last := len(ac.Rec.Attempts) - 1; last >= 0 {
		ac.Rec.Attempts[last].Outcome = string(outcome)
		ac.Rec.Attempts[last].Error = err.Error()
	}
	if outcome != adapter.OutcomeFatal {
		ac.Exec.recordHealthFor(ac.Cand, outcome, resp)
	}
	var ie *ir.Error
	if errors.As(err, &ie) {
		return outcome, ie
	}
	return outcome, errorFor(outcome, err)
}

// HandleEmbeddings serves POST /v1/embeddings.
func (e *Executor) HandleEmbeddings(w http.ResponseWriter, r *http.Request, d edge.EmbeddingDialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceEmbedding, d, func(cfg *config.Config) (SurfaceOp, error) {
		req, err := d.ParseEmbedding(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &embedOp{d: d, req: req}, nil
	})
}
```

- [ ] **Step 4: Wire the route**

In `internal/server/server.go`, beside the chat route:

```go
	mux.HandleFunc("POST /v1/embeddings", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleEmbeddings(w, r, oa)
	}))
```

`oa` is the same `*openaiedge.Dialect` the chat route uses: it satisfies `edge.Dialect` for `authed` and `edge.EmbeddingDialect` for the handler, so no second value is constructed.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ ./internal/server/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in both packages.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/exec/embed.go internal/exec/embed_test.go internal/server/server.go
git commit -m "feat(exec): serve the embeddings surface"
```

---

### Task 16: Moderations end to end

**Files:**
- Modify: `internal/ir/aux.go`, `internal/adapter/adapter.go`, `internal/edge/edge.go`, `internal/edge/openai/aux.go`, `internal/server/server.go`
- Create: `internal/adapter/openaicompat/moderation.go`, `internal/exec/moderation.go`
- Test: `internal/adapter/openaicompat/moderation_test.go`, `internal/exec/moderation_test.go`, `internal/edge/openai/aux_test.go`

**Interfaces:**
- Consumes: `RunAux` (Task 14), `failedParse` (Task 15).
- Produces: `ir.ModerationRequest`, `ir.ModerationResponse`, `ir.ModerationResult`, `adapter.Moderator`, `edge.ModerationDialect`, and `(*Executor).HandleModerations`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: this is Task 15's four files with the moderation types substituted, and every one of them is given below.

The one real design decision is **categories are maps, not structs**. OpenAI's category list has grown five times since the endpoint shipped and is different again on `omni-moderation-latest`. A struct would silently drop every category added after this was written, and a dropped category on a moderation endpoint is a safety signal the client never sees. A map forwards whatever arrives.

Moderations report **no usage** — the endpoint is free and returns no `usage` object. `TokensIn` and `TokensOut` stay zero and cost stays NULL, which is the honest record rather than a fabricated one.

`omni-moderation-latest` also accepts an array of content-part objects for image moderation. That shape is **rejected with a clear error** rather than half-supported: forwarding it would need an image-part IR this phase does not build, and accepting it while dropping the image parts would moderate the text and report the whole input clean.

- [ ] **Step 1: Write the failing tests**

Add to `internal/edge/openai/aux_test.go`:

```go
func TestParseModerationNormalizesBothInputShapes(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []string
	}{
		{`{"model":"m","input":"hello"}`, []string{"hello"}},
		{`{"model":"m","input":["a","b"]}`, []string{"a", "b"}},
	} {
		req, err := ParseModeration(httptest.NewRequest("POST", "/v1/moderations",
			strings.NewReader(tc.body)), 1<<20)
		if err != nil {
			t.Fatalf("ParseModeration(%s): %v", tc.body, err)
		}
		if len(req.Input) != len(tc.want) || req.Input[0] != tc.want[0] {
			t.Errorf("ParseModeration(%s).Input = %v, want %v", tc.body, req.Input, tc.want)
		}
	}
}

func TestParseModerationRejectsContentParts(t *testing.T) {
	// Accepting this while dropping the image parts would moderate the text
	// and report the whole input clean, which is worse than refusing it.
	_, err := ParseModeration(httptest.NewRequest("POST", "/v1/moderations",
		strings.NewReader(`{"model":"m","input":[{"type":"image_url","image_url":{"url":"x"}}]}`)), 1<<20)
	if err == nil {
		t.Fatal("a content-part input was accepted")
	}
	if !strings.Contains(err.Error(), "text") {
		t.Errorf("err = %v; it must say what is supported", err)
	}
}

func TestWriteModerationCarriesEveryCategory(t *testing.T) {
	// The category list is provider-defined and grows. A dropped category on a
	// moderation endpoint is a safety signal the client never sees.
	w := httptest.NewRecorder()
	if err := WriteModeration(w, &ir.ModerationResponse{
		ID: "modr-1", Model: "omni-moderation-latest",
		Results: []ir.ModerationResult{{
			Flagged:    true,
			Categories: map[string]bool{"harassment": true, "a-category-invented-later": false},
			Scores:     map[string]float64{"harassment": 0.91, "a-category-invented-later": 0.01},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var body struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Results []struct {
			Flagged    bool               `json:"flagged"`
			Categories map[string]bool    `json:"categories"`
			Scores     map[string]float64 `json:"category_scores"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "modr-1" || len(body.Results) != 1 || !body.Results[0].Flagged {
		t.Fatalf("body = %s", w.Body.String())
	}
	if _, ok := body.Results[0].Categories["a-category-invented-later"]; !ok {
		t.Errorf("categories = %v; an unknown category was dropped", body.Results[0].Categories)
	}
	if body.Results[0].Scores["harassment"] != 0.91 {
		t.Errorf("scores = %v", body.Results[0].Scores)
	}
}
```

Create `internal/adapter/openaicompat/moderation_test.go`:

```go
package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildModerationRendersTheOpenAIShape(t *testing.T) {
	hr, warns, err := New().BuildModeration(context.Background(),
		&adapter.Target{BaseURL: "https://api.example.com/v1/", APIKey: "sk", Model: "omni-moderation-latest"},
		&ir.ModerationRequest{Input: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	if hr.URL.String() != "https://api.example.com/v1/moderations" {
		t.Errorf("url = %s", hr.URL)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "omni-moderation-latest" {
		t.Errorf("model = %v; the target's name must be sent", body["model"])
	}
	if in, ok := body["input"].([]any); !ok || len(in) != 2 {
		t.Errorf("input = %v", body["input"])
	}
}

func TestParseModerationKeepsUnknownCategories(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Body: io.NopCloser(strings.NewReader(`{"id":"modr-1","model":"m","results":[
		  {"flagged":true,
		   "categories":{"hate":false,"invented-later":true},
		   "category_scores":{"hate":0.01,"invented-later":0.99}}]}`)),
	}
	out, err := New().ParseModeration(resp)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "modr-1" || len(out.Results) != 1 || !out.Results[0].Flagged {
		t.Fatalf("response = %+v", out)
	}
	if !out.Results[0].Categories["invented-later"] {
		t.Errorf("categories = %v", out.Results[0].Categories)
	}
	if out.Results[0].Scores["invented-later"] != 0.99 {
		t.Errorf("scores = %v", out.Results[0].Scores)
	}
}

func TestParseModerationKeepsAppliedInputTypes(t *testing.T) {
	// Without it a client cannot tell a flagged image from flagged text.
	resp := &http.Response{
		StatusCode: 200,
		Body: io.NopCloser(strings.NewReader(`{"id":"m","model":"m","results":[
		  {"flagged":true,"categories":{"violence":true},"category_scores":{"violence":0.9},
		   "category_applied_input_types":{"violence":["image"]}}]}`)),
	}
	out, err := New().ParseModeration(resp)
	if err != nil {
		t.Fatal(err)
	}
	got := out.Results[0].AppliedInputTypes["violence"]
	if len(got) != 1 || got[0] != "image" {
		t.Errorf("applied input types = %v", out.Results[0].AppliedInputTypes)
	}
}

func TestParseModerationRejectsAnEmptyResultSet(t *testing.T) {
	// A 200 with no results is a provider fault: the client asked about input
	// it got no verdict on, and returning it as success hides that.
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`{"id":"m","model":"m","results":[]}`)),
	}
	if _, err := New().ParseModeration(resp); err == nil {
		t.Fatal("a verdict-free 200 parsed cleanly")
	}
}
```

Create `internal/exec/moderation_test.go`:

```go
package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

func moderationUpstream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"modr-1","model":"omni","results":[
		  {"flagged":false,"categories":{"hate":false},"category_scores":{"hate":0.001}}]}`))
	}
}

func TestModerationsServeEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(moderationUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "omni", ir.SurfaceModeration))
	w := httptest.NewRecorder()
	e.HandleModerations(w, httptest.NewRequest("POST", "/v1/moderations",
		strings.NewReader(`{"model":"omni","input":"hello"}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Results []struct {
			Flagged bool `json:"flagged"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 1 {
		t.Fatalf("body = %s", w.Body.String())
	}
	got := rec.only(t)
	if got.Surface != "moderation" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
	if got.TokensIn != 0 || got.TokensOut != 0 {
		t.Errorf("tokens = %d/%d; the moderation endpoint reports none", got.TokensIn, got.TokensOut)
	}
}

func TestModerationsFailOverToASecondProvider(t *testing.T) {
	var hits atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(moderationUpstream())
	defer good.Close()

	e, rec := executorForTwo(t, bad.URL, good.URL,
		catalogPair("bad", "good", "omni", ir.SurfaceModeration))
	w := httptest.NewRecorder()
	e.HandleModerations(w, httptest.NewRequest("POST", "/v1/moderations",
		strings.NewReader(`{"model":"omni","input":"hello"}`)), openaiedge.New())

	if w.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("status = %d, failing provider hits = %d", w.Code, hits.Load())
	}
	if got := rec.only(t); got.FinalProviderID != "good" {
		t.Errorf("final = %q", got.FinalProviderID)
	}
}

func TestAModerationRequestWithNoModerationProviderIsRefused(t *testing.T) {
	upstream := httptest.NewServer(moderationUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleModerations(w, httptest.NewRequest("POST", "/v1/moderations",
		strings.NewReader(`{"model":"chat-only","input":"hello"}`)), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(rec.only(t).Attempts) != 0 {
		t.Error("a surface no provider offers attempted an upstream call")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ ./internal/adapter/openaicompat/ ./internal/exec/ -run Moderation -v
```

Expected: FAIL to build in all three — `undefined: ParseModeration`, `undefined: BuildModeration`, `e.HandleModerations undefined`.

- [ ] **Step 3: Add the IR types and the adapter interface**

Append to `internal/ir/aux.go`:

```go
// ModerationRequest is one moderation call. Input is always a slice: OpenAI
// accepts a bare string or an array of strings and the edge flattens both.
type ModerationRequest struct {
	Model string
	Input []string
}

// InputCount is the batched item count, recorded on the request row per spec §9.
func (r *ModerationRequest) InputCount() int { return len(r.Input) }

// ModerationResult is one verdict.
//
// Categories and Scores are maps rather than a fixed field set because the
// category list is provider-defined and has grown repeatedly. A struct would
// silently drop every category added after it was written, and a dropped
// category on a moderation endpoint is a safety signal the client never sees.
type ModerationResult struct {
	Flagged    bool
	Categories map[string]bool
	Scores     map[string]float64
	// AppliedInputTypes is omni-moderation's per-category record of which
	// input modality triggered it. Dropping it would leave a client unable to
	// tell a flagged image from flagged text.
	AppliedInputTypes map[string][]string
}

type ModerationResponse struct {
	ID    string
	Model string
	// Results is parallel to the request's Input, one verdict per item.
	Results []ModerationResult
	// Usage is zero for every known provider: the endpoint reports none. It is
	// carried anyway so a provider that starts reporting is recorded rather
	// than discarded.
	Usage Usage
}
```

In `internal/adapter/adapter.go`, beside `Embedder`:

```go
// Moderator is implemented by an adapter serving the moderation surface.
type Moderator interface {
	BuildModeration(ctx context.Context, t *Target, req *ir.ModerationRequest) (*http.Request, []ir.Warning, error)
	// ParseModeration takes ownership of resp.Body and always closes it.
	ParseModeration(resp *http.Response) (*ir.ModerationResponse, error)
}
```

- [ ] **Step 4: Add the edge interface, parser and writer**

In `internal/edge/edge.go`, beside `EmbeddingDialect`:

```go
// ModerationDialect is the inbound wire form of the moderation surface.
type ModerationDialect interface {
	Name() string
	ParseModeration(r *http.Request, maxBody int64) (*ir.ModerationRequest, error)
	WriteModeration(w http.ResponseWriter, resp *ir.ModerationResponse) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}
```

Append to `internal/edge/openai/aux.go`:

```go
type wireModerationRequest struct {
	Model string          `json:"model"`
	Input json.RawMessage `json:"input"`
}

func ParseModeration(r *http.Request, maxBody int64) (*ir.ModerationRequest, error) {
	body, err := readCappedBody(r, maxBody)
	if err != nil {
		return nil, err
	}
	var w wireModerationRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	texts, err := parseTextInput(w.Input)
	if err != nil {
		return nil, err
	}
	return &ir.ModerationRequest{Model: w.Model, Input: texts}, nil
}

// parseTextInput reads a bare string or an array of strings.
//
// omni-moderation-latest also accepts an array of content-part objects for
// image moderation. That is refused rather than half-supported: accepting it
// while dropping the image parts would moderate the text and report the whole
// input clean.
func parseTextInput(raw json.RawMessage) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, errors.New("input is required")
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("input: %w", err)
		}
		return []string{s}, nil
	case '[':
		var out []string
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return nil, fmt.Errorf("input must be text or an array of text: %w", err)
		}
		if len(out) == 0 {
			return nil, errors.New("input is empty")
		}
		return out, nil
	default:
		return nil, errors.New("input must be text or an array of text")
	}
}

func WriteModeration(w http.ResponseWriter, resp *ir.ModerationResponse) error {
	results := make([]any, 0, len(resp.Results))
	for _, r := range resp.Results {
		cats := r.Categories
		if cats == nil {
			cats = map[string]bool{}
		}
		scores := r.Scores
		if scores == nil {
			scores = map[string]float64{}
		}
		row := map[string]any{
			"flagged":         r.Flagged,
			"categories":      cats,
			"category_scores": scores,
		}
		// Omitted when the provider sent none: the older moderation models do
		// not report it and an empty object would claim they did.
		if len(r.AppliedInputTypes) > 0 {
			row["category_applied_input_types"] = r.AppliedInputTypes
		}
		results = append(results, row)
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{
		"id": resp.ID, "model": resp.Model, "results": results,
	})
}

func (d *Dialect) ParseModeration(r *http.Request, maxBody int64) (*ir.ModerationRequest, error) {
	return ParseModeration(r, maxBody)
}

func (d *Dialect) WriteModeration(w http.ResponseWriter, resp *ir.ModerationResponse) error {
	return WriteModeration(w, resp)
}

var _ edge.ModerationDialect = (*Dialect)(nil)
```

Extract the body read `ParseEmbedding` already performs into `readCappedBody` in the same file, and have `ParseEmbedding` call it too — every auxiliary parser needs the identical eight lines:

```go
// readCappedBody reads the inbound body under the configured cap, reading one
// byte past it so "exactly at the cap" is not rejected.
func readCappedBody(r *http.Request, maxBody int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBody {
		// Typed, so RunAux answers 413 rather than 400. An oversized JSON body
		// asks the client for the same thing an oversized upload does.
		return nil, &ir.Error{
			Type:    ir.ErrPayloadTooLarge,
			Message: fmt.Sprintf("request body exceeds %d bytes", maxBody),
		}
	}
	return body, nil
}
```

- [ ] **Step 5: Implement the adapter**

Create `internal/adapter/openaicompat/moderation.go`:

```go
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// maxModerationBytes bounds the response read. A verdict set is small; an
// unbounded read from a misbehaving provider is the hazard max_body_bytes
// prevents inbound and nothing was preventing outbound.
const maxModerationBytes = 4 << 20

func (a *Adapter) BuildModeration(ctx context.Context, t *adapter.Target,
	req *ir.ModerationRequest) (*http.Request, []ir.Warning, error) {

	buf, err := json.Marshal(map[string]any{"model": t.Model, "input": req.Input})
	if err != nil {
		return nil, nil, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/moderations"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build moderation request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil, nil
}

type moderationEnvelope struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Results []struct {
		Flagged    bool                `json:"flagged"`
		Categories map[string]bool     `json:"categories"`
		Scores     map[string]float64  `json:"category_scores"`
		Applied    map[string][]string `json:"category_applied_input_types"`
	} `json:"results"`
}

func (a *Adapter) ParseModeration(resp *http.Response) (*ir.ModerationResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxModerationBytes))
	if err != nil {
		return nil, fmt.Errorf("read moderation response: %w", err)
	}
	var env moderationEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse moderation response: %w", err)
	}
	if len(env.Results) == 0 {
		// A 200 with no verdict is a provider fault: the client asked about
		// input it got no answer on, and reporting success hides that.
		return nil, errors.New("moderation response carried no results")
	}
	out := &ir.ModerationResponse{
		ID: env.ID, Model: env.Model,
		Results: make([]ir.ModerationResult, 0, len(env.Results)),
	}
	for _, r := range env.Results {
		out.Results = append(out.Results, ir.ModerationResult{
			Flagged: r.Flagged, Categories: r.Categories, Scores: r.Scores,
			AppliedInputTypes: r.Applied,
		})
	}
	return out, nil
}

var _ adapter.Moderator = (*Adapter)(nil)
```

- [ ] **Step 6: Write the op and the route**

Create `internal/exec/moderation.go`:

```go
package exec

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

type moderationOp struct {
	d   edge.ModerationDialect
	req *ir.ModerationRequest
}

func (o *moderationOp) Dialect() string { return o.d.Name() }

func (o *moderationOp) Query() router.Query {
	return router.Query{Model: o.req.Model, Surface: ir.SurfaceModeration}
}

func (o *moderationOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	m, ok := ad.(adapter.Moderator)
	if !ok {
		return nil, nil, fmt.Errorf("adapter %s does not serve moderations", ad.Kind())
	}
	return m.BuildModeration(ctx, tgt, o.req)
}

func (o *moderationOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	m, ok := ac.Adapter.(adapter.Moderator)
	if !ok {
		resp.Body.Close()
		return adapter.OutcomeFatal, &ir.Error{
			Type: ir.ErrDarkrouter, Message: "adapter does not serve moderations",
		}
	}
	out, err := m.ParseModeration(resp)
	if err != nil {
		return failedParse(ac, resp, err)
	}

	ttft := time.Since(ac.Rec.TS).Milliseconds()
	ac.Rec.TTFTMs = &ttft
	applyUsage(ac.Rec, &out.Usage)
	ac.Rec.FinalProviderID = ac.Cand.ProviderID
	ac.Rec.FinalModel = ac.Cand.Model
	ac.Rec.Warnings = warningStrings(ac.Warns)

	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	_ = o.d.WriteModeration(cw, out)
	return adapter.OutcomeSuccess, nil
}

func (o *moderationOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*moderationOp)(nil)

// HandleModerations serves POST /v1/moderations.
func (e *Executor) HandleModerations(w http.ResponseWriter, r *http.Request, d edge.ModerationDialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceModeration, d, func(cfg *config.Config) (SurfaceOp, error) {
		req, err := d.ParseModeration(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &moderationOp{d: d, req: req}, nil
	})
}
```

In `internal/server/server.go`, beside the embeddings route:

```go
	mux.HandleFunc("POST /v1/moderations", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleModerations(w, r, oa)
	}))
```

- [ ] **Step 7: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ ./internal/adapter/openaicompat/ ./internal/exec/ ./internal/server/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in all four packages.

- [ ] **Step 8: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 9: Commit**

```bash
git add internal/ir/aux.go internal/adapter/adapter.go internal/adapter/openaicompat/moderation.go \
        internal/adapter/openaicompat/moderation_test.go internal/edge/edge.go \
        internal/edge/openai/aux.go internal/edge/openai/aux_test.go \
        internal/exec/moderation.go internal/exec/moderation_test.go internal/server/server.go
git commit -m "feat(exec): serve the moderation surface"
```

---

### Task 17: Providers declare their preset in configuration

**Files:**
- Modify: `internal/config/config.go`, `internal/provider/provider.go`, `internal/store/import.go`
- Test: `internal/config/load_test.go`, `internal/provider/provider_test.go`, `internal/store/import_test.go`

**Interfaces:**
- Consumes: `catalog.Embedded` (phase 6).
- Produces: `config.ProviderConfig.Preset` and a populated `provider.Provider.Preset` from both sources. Task 18's `rerankPath` reads it; Tasks 19, 31 and 34 depend on it.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5
**Approach:** inline - skip 2: the column already exists and the three writers are three lines each.

**A YAML-configured provider can never reach its preset today, and that is a production gap, not a test inconvenience.** `provider.Provider.Preset` is documented as "how quirks, surfaces, model traits and the models.dev join key are reached at request time" — but `config.ProviderConfig` has no `preset` field, the loader is strict (`dec.KnownFields(true)`, so writing one is a validation *error*), `YAMLSource.Providers` never sets it, and `ImportFromConfig` omits it from its `INSERT` even though `providers.preset` has existed since migration 0001. The only writer of that column anywhere in the tree is a test doing raw SQL.

Phase 6's verification already recorded the symptom — "a provider imported from the YAML `providers:` block has an empty [preset], so nothing joins until it is set… by hand" — and filed it as a phase 7 UI concern. It is not: it is a missing three-line plumbing, and phase 5 makes it blocking. Task 18 resolves the rerank path from the preset, so **without this, rerank cannot be served by any YAML-configured provider at all**, and Task 34's live verification cannot attach Groq's audio surfaces.

An unknown preset name is a **warning, not an error**. `Config.Warnings` exists for exactly this and surfaces on `/healthz`; refusing to start because a preset was renamed upstream would take a working gateway down over metadata.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/load_test.go`:

```go
func TestAProviderCanNameItsPreset(t *testing.T) {
	// Without this the loader rejects the key outright: dec.KnownFields(true)
	// makes an unknown field a validation failure.
	cfg := mustLoad(t, `
server:
  proxy_listen: ":0"
providers:
  - id: cohere
    kind: openaicompat
    preset: cohere
    base_url: https://api.cohere.com/compatibility/v1
    api_key: sk
    models: [rerank-v3.5]
`)
	if len(cfg.Providers) != 1 || cfg.Providers[0].Preset != "cohere" {
		t.Fatalf("providers = %+v", cfg.Providers)
	}
}

func TestAnUnknownPresetWarnsRatherThanFailing(t *testing.T) {
	// A preset renamed upstream must not take a working gateway down. The
	// warning reaches /healthz, which is where an operator looks.
	cfg := mustLoad(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    preset: not-a-real-preset
    base_url: https://x/v1
    api_key: sk
    models: [m]
`)
	if len(cfg.Warnings) == 0 {
		t.Fatal("an unknown preset produced no warning")
	}
	if !strings.Contains(strings.Join(cfg.Warnings, " "), "not-a-real-preset") {
		t.Errorf("warnings = %v; the warning must name the preset", cfg.Warnings)
	}
}
```

Use whatever helper `load_test.go` already has for loading a config from a string; if it has none, write the document to `t.TempDir()` and call the same loader the other tests call. **Read the file first** rather than assuming `mustLoad` exists.

Add to `internal/provider/provider_test.go`:

```go
func TestYAMLSourceCarriesThePreset(t *testing.T) {
	// Provider.Preset is documented as how quirks, surfaces and traits are
	// reached at request time. A YAML provider reached none of them.
	ps := providersFrom(t, `
server:
  proxy_listen: ":0"
providers:
  - id: cohere
    kind: openaicompat
    preset: cohere
    base_url: https://api.cohere.com/compatibility/v1
    api_key: sk
    models: [rerank-v3.5]
`)
	if len(ps) != 1 || ps[0].Preset != "cohere" {
		t.Fatalf("providers = %+v", ps)
	}
}
```

`providersFrom` builds a `config.Store` over a temp file and calls `NewYAMLSource(...).Providers(ctx)`; follow whatever the existing tests in that file already do.

Add to `internal/store/import_test.go`:

```go
func TestImportWritesThePresetColumn(t *testing.T) {
	// providers.preset has existed since migration 0001 and nothing has ever
	// written it, so the catalog join and the rerank path both saw "".
	db := migrated(t)
	cfg := &config.Config{Providers: []config.ProviderConfig{{
		ID: "cohere", Kind: "openaicompat", Preset: "cohere",
		BaseURL: "https://api.cohere.com/compatibility/v1", APIKey: "sk",
		Models: []string{"rerank-v3.5"},
	}}}
	if _, err := db.ImportFromConfig(context.Background(), cfg, testKey(t)); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.Read.QueryRowContext(context.Background(),
		`SELECT preset FROM providers WHERE id = 'cohere'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "cohere" {
		t.Errorf("preset = %q", got)
	}
}
```

Match `ImportFromConfig`'s real receiver, signature and key argument, and the master-key helper the other import tests use — **read `internal/store/import_test.go` first**.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/config/ ./internal/provider/ ./internal/store/ -run 'Preset' -v
```

Expected: the config tests fail with a validation error naming the unknown field `preset`; the others fail to build on `ProviderConfig.Preset`.

- [ ] **Step 3: Add the field**

In `internal/config/config.go`:

```go
type ProviderConfig struct {
	ID   string `yaml:"id"`
	Kind string `yaml:"kind"`
	// Preset names the shipped catalog entry this provider is an instance of.
	// It is how quirks, surfaces, model traits and the models.dev join key are
	// reached at request time; without it a provider is a base URL and a key.
	Preset   string   `yaml:"preset"`
	BaseURL  string   `yaml:"base_url"`
	APIKey   string   `yaml:"api_key"`
	Priority int      `yaml:"priority"`
	Models   []string `yaml:"models"`
}
```

- [ ] **Step 4: Warn on an unknown preset**

In `internal/config/load.go`, in the same pass that produces the other warnings:

```go
	for _, p := range c.Providers {
		if p.Preset == "" {
			continue
		}
		if _, ok := catalog.Embedded()[p.Preset]; !ok {
			// A warning, not an error. A preset renamed upstream must not take
			// a working gateway down over metadata; /healthz is where this
			// belongs and where an operator looks.
			c.Warnings = append(c.Warnings,
				fmt.Sprintf("provider %q names preset %q, which is not a shipped preset; "+
					"its quirks, surfaces and pricing will not be applied", p.ID, p.Preset))
		}
	}
```

**Check for an import cycle before writing this.** If `internal/catalog` imports `internal/config`, this validation cannot live here — move it to wherever the other startup warnings are assembled (`internal/server`), and say so in the commit. Run `go list -deps` or simply build; do not leave a cycle in place.

- [ ] **Step 5: Carry it through both sources**

In `internal/provider/provider.go`, in `YAMLSource.Providers`:

```go
		out = append(out, Provider{
			ID: p.ID, Kind: p.Kind, BaseURL: p.BaseURL, Preset: p.Preset,
			// A config credential has no database row, so its id is empty. The
			// breaker keys on that empty id, which is what phase 2 already did.
			Credentials: []Credential{{ID: "", Secret: p.APIKey, Enabled: true}},
			Priority:    p.Priority, Models: p.Models,
		})
```

In `internal/store/import.go`, extend the provider insert:

```go
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO providers (id, name, preset, kind, base_url, priority, enabled, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, 1, ?)`,
			p.ID, p.ID, p.Preset, p.Kind, p.BaseURL, p.Priority, now.UnixMilli()); err != nil {
			return ImportResult{}, fmt.Errorf("import provider %q: %w", p.ID, err)
		}
```

No migration is needed: `providers.preset` has existed since `0001_init.sql` with a `''` default and has simply never been written.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/config/ ./internal/provider/ ./internal/store/ ./internal/catalog/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS in all four packages.

- [ ] **Step 7: Document the field**

Add `preset:` to `darkrouter.example.yaml`'s provider block with a one-line comment saying it is what connects a provider to its shipped metadata, and that omitting it means no pricing, capabilities or quirks. This is the field phase 6's verification found people would otherwise have to set by hand in SQL.

- [ ] **Step 8: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 9: Commit**

```bash
git add internal/config/config.go internal/config/load.go internal/config/load_test.go \
        internal/provider/provider.go internal/provider/provider_test.go \
        internal/store/import.go internal/store/import_test.go darkrouter.example.yaml
git commit -m "feat(config): let a provider name its preset"
```

---

### Task 18: The preset-declared rerank path reaches the adapter

**Files:**
- Modify: `internal/adapter/adapter.go`, `internal/exec/exec.go`
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `catalog.Preset.QuirkValue` (phase 6), `provider.Provider.Preset`.
- Produces: `adapter.Target.RerankPath`. Task 19's adapter reads it.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: the quirk mechanism and its accessor both exist from phase 6, and the only new code is one field and one lookup.

Spec §3.1: "Each preset declares its own rerank path, since providers expose it at differing URLs." Task 2 encodes that as the valued quirk `rerank-path=/v2/rerank` and its test refuses a rerank preset without one. Nothing carries the value to the adapter yet, and the adapter is where the URL is built.

**Why a field on `Target` rather than a lookup inside the adapter.** The adapter is given a resolved target and knows nothing about presets; reaching into `catalog.Embedded()` from inside it would make the renderer depend on the shipped data file and become untestable without it. Every other per-provider fact on `Target` — base URL, key, model, model info — is resolved by the executor for the same reason, and this is one more.

An empty `RerankPath` is not a fallback to a guessed URL. The router only produces a rerank candidate for a model whose preset declares the surface, and Task 2's test refuses such a preset without the quirk, so empty means a misconfiguration and Task 19 fails the build loudly rather than posting a rerank body at `/chat/completions`.

- [ ] **Step 1: Write the failing test**

Add to `internal/exec/exec_test.go`:

```go
func TestTheTargetCarriesThePresetRerankPath(t *testing.T) {
	// Spec §3.1: providers expose rerank at differing URLs, so the path is
	// data. The adapter is handed a resolved target and must not have to reach
	// into the shipped preset file to build a URL.
	upstream := httptest.NewServer(jsonOK())
	defer upstream.Close()

	op := &probeOp{q: router.Query{Model: "rerank-v3.5", Surface: ir.SurfaceRerank}}
	e, _ := executorForPreset(t, upstream.URL, "cohere",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	e.RunSurface(httptest.NewRecorder(), httptest.NewRequest("POST", "/probe", nil), op)

	if op.lastTarget.RerankPath != "/v2/rerank" {
		t.Errorf("RerankPath = %q, want the cohere preset's quirk value", op.lastTarget.RerankPath)
	}
}

func TestAProviderWithNoPresetCarriesNoRerankPath(t *testing.T) {
	// An uncatalogued provider is a base URL and a key. Guessing a rerank path
	// for it would post a rerank body at whatever URL the guess produced.
	upstream := httptest.NewServer(jsonOK())
	defer upstream.Close()

	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceRerank}}
	e, _ := executorForPreset(t, upstream.URL, "", catalogWith("p", "m", ir.SurfaceRerank))
	e.RunSurface(httptest.NewRecorder(), httptest.NewRequest("POST", "/probe", nil), op)

	if op.lastTarget.RerankPath != "" {
		t.Errorf("RerankPath = %q, want empty", op.lastTarget.RerankPath)
	}
}
```

Two supporting edits:

- Give `probeOp` a `lastTarget adapter.Target` field and set it in `Build` beside the existing `lastInfo`. `lastInfo` stays, so Task 6's assertions are untouched.
- Add `executorForPreset(t, url, preset string, cat *catalog.Store)`, which is `executorForOp` with `preset:` set on the provider row.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestTheTargetCarriesThePreset|TestAProviderWithNoPreset' -v
```

Expected: FAIL to build — `op.lastTarget undefined` and `tgt.RerankPath undefined`.

- [ ] **Step 3: Add the field**

In `internal/adapter/adapter.go`:

```go
type Target struct {
	BaseURL string
	APIKey  string
	Model   string
	Info    ModelInfo

	// RerankPath is the preset-declared Cohere-v2 path, spec §3.1, resolved by
	// the executor because the adapter is handed a target and knows nothing
	// about presets. Empty for a provider with no preset, which is a
	// misconfiguration for this surface rather than a licence to guess a URL.
	RerankPath string
}
```

- [ ] **Step 4: Resolve it in the executor**

In `internal/exec/exec.go`, extend the `Target` construction inside `attempt`:

```go
	tgt := &adapter.Target{
		BaseURL: p.BaseURL, APIKey: secretOf(p, c.KeyID), Model: c.Model,
		Info:       modelInfo(cat, c.ProviderID, c.Model),
		RerankPath: rerankPath(p.Preset),
	}
```

and add beside `modelInfo`:

```go
// rerankPath returns the provider's preset-declared rerank path, or "" when it
// has no preset or the preset declares none. Spec §3.1: providers expose rerank
// at differing URLs, so the path is preset data rather than an adapter
// constant.
func rerankPath(preset string) string {
	if preset == "" {
		return ""
	}
	p, ok := catalog.Embedded()[preset]
	if !ok {
		return ""
	}
	v, _ := p.QuirkValue("rerank-path")
	return v
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/adapter/adapter.go internal/exec/exec.go internal/exec/exec_test.go
git commit -m "feat(exec): resolve the preset rerank path onto the target"
```

---

### Task 19: Rerank end to end, at the preset-declared path

**Files:**
- Modify: `internal/ir/aux.go`, `internal/adapter/adapter.go`, `internal/edge/edge.go`, `internal/edge/openai/aux.go`, `internal/server/server.go`
- Create: `internal/adapter/openaicompat/rerank.go`, `internal/exec/rerank.go`
- Test: `internal/adapter/openaicompat/rerank_test.go`, `internal/exec/rerank_test.go`, `internal/edge/openai/aux_test.go`

**Interfaces:**
- Consumes: `adapter.Target.RerankPath` (Task 18), `RunAux` (Task 14), `failedParse` (Task 15).
- Produces: `ir.RerankRequest`, `ir.RerankResponse`, `ir.RerankResult`, `adapter.Reranker`, `edge.RerankDialect`, and `(*Executor).HandleRerank`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: the shape is Cohere v2, settled in this plan's foundation, and the file layout is Task 16's with the rerank types substituted.

OpenAI defines no rerank endpoint, so Darkrouter's inbound contract **is** Cohere v2 — the same shape in and out. That makes this the one surface where the edge and the adapter agree by construction rather than by translation.

**The path is absolute from the host root, and that is load-bearing.** `cohere`'s preset base URL is `https://api.cohere.com/compatibility/v1` — an OpenAI-compatibility shim — while its rerank endpoint is the native `/v2/rerank`. Joining them the way every other endpoint is joined produces `https://api.cohere.com/compatibility/v1/v2/rerank`, which does not exist. A `rerank-path` beginning with `/` therefore replaces the base URL's path entirely.

**Spec §3.1 is factually wrong about Cohere v2, and the master design wins.** It describes "document objects, `top_n`, `return_documents`". Verified against the live API reference on 2026-08-23: v2's `documents` is a **list of strings**; `return_documents` and `rank_fields` are **v1 parameters that v2 does not define**; and each element of `results[]` carries **`index` and `relevance_score` only, never a `document`**. The master design says only "the Cohere v2 request and response schema", which is what is implemented.

That leaves `return_documents` — which the inbound contract keeps, because a client asking for its documents back is reasonable and every rerank client expects it. **Darkrouter honors it at the edge rather than forwarding it.** The buffered request already holds every document, and `results[]` carries the index into it, so the op fills each result's document from the request it sent. That is strictly better than forwarding a parameter the endpoint ignores: forwarding it would return results with no documents while the client believed it had asked for them — the same silent lie this plan refuses for embeddings, one surface over.

Cohere v2 accepts `documents` as strings. Darkrouter's **inbound** contract also accepts objects, as a deliberate superset: an object contributes its `text` field and **any other field it carries is dropped with a warning**, because a document object with structured fields is being reranked on its text alone and the client cannot otherwise tell.

Rerank reports no token usage — Cohere bills it in `search_units` — so `TokensIn` and `TokensOut` stay zero and cost stays NULL.

- [ ] **Step 1: Write the failing tests**

Add to `internal/edge/openai/aux_test.go`:

```go
func TestParseRerankReadsBothDocumentForms(t *testing.T) {
	req, err := ParseRerank(httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"rerank-v3.5","query":"q","documents":["plain",{"text":"boxed"}],
		  "top_n":1,"return_documents":true}`)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "rerank-v3.5" || req.Query != "q" {
		t.Errorf("request = %+v", req)
	}
	if len(req.Documents) != 2 || req.Documents[0] != "plain" || req.Documents[1] != "boxed" {
		t.Errorf("documents = %v", req.Documents)
	}
	if req.TopN != 1 || !req.ReturnDocuments {
		t.Errorf("top_n = %d, return_documents = %v", req.TopN, req.ReturnDocuments)
	}
	if len(req.Warnings) != 0 {
		t.Errorf("warnings = %v; neither document lost a field", req.Warnings)
	}
}

func TestParseRerankWarnsOnDroppedDocumentFields(t *testing.T) {
	// A document object with structured fields is reranked on its text alone.
	// The client cannot tell that from the response, so the trace must.
	req, err := ParseRerank(httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"m","query":"q","documents":[{"text":"t","title":"T","url":"u"}]}`)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one", req.Warnings)
	}
	if !strings.Contains(req.Warnings[0].Reason, "title") ||
		!strings.Contains(req.Warnings[0].Reason, "url") {
		t.Errorf("warning = %q; it must name the dropped fields", req.Warnings[0].Reason)
	}
}

func TestParseRerankRejectsAnUnusableRequest(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","documents":["a"]}`,
		`{"model":"m","query":"q"}`,
		`{"model":"m","query":"q","documents":[]}`,
		`{"model":"m","query":"q","documents":[{"title":"no text"}]}`,
	} {
		if _, err := ParseRerank(httptest.NewRequest("POST", "/v1/rerank",
			strings.NewReader(body)), 1<<20); err == nil {
			t.Errorf("ParseRerank(%s) accepted an unusable request", body)
		}
	}
}

func TestWriteRerankEmitsCohereV2(t *testing.T) {
	w := httptest.NewRecorder()
	if err := WriteRerank(w, &ir.RerankResponse{
		ID: "r-1", Model: "rerank-v3.5",
		Results: []ir.RerankResult{
			{Index: 2, RelevanceScore: 0.98, Document: "kept"},
			{Index: 0, RelevanceScore: 0.11},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var body struct {
		ID      string `json:"id"`
		Results []struct {
			Index    int     `json:"index"`
			Score    float64 `json:"relevance_score"`
			Document *struct {
				Text string `json:"text"`
			} `json:"document"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "r-1" || len(body.Results) != 2 {
		t.Fatalf("body = %s", w.Body.String())
	}
	if body.Results[0].Index != 2 || body.Results[0].Score != 0.98 {
		t.Errorf("first result = %+v", body.Results[0])
	}
	if body.Results[0].Document == nil || body.Results[0].Document.Text != "kept" {
		t.Errorf("document = %v; a returned document was lost", body.Results[0].Document)
	}
	if body.Results[1].Document != nil {
		t.Errorf("document = %v; a result with no document must omit the key entirely",
			body.Results[1].Document)
	}
}
```

Create `internal/adapter/openaicompat/rerank_test.go`:

```go
package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildRerankUsesTheAbsolutePresetPath(t *testing.T) {
	// cohere's base URL is an OpenAI-compatibility shim while its rerank
	// endpoint is native. Joining them produces a URL that does not exist.
	hr, _, err := New().BuildRerank(context.Background(),
		&adapter.Target{
			BaseURL:    "https://api.cohere.com/compatibility/v1",
			APIKey:     "sk",
			Model:      "rerank-v3.5",
			RerankPath: "/v2/rerank",
		},
		&ir.RerankRequest{Query: "q", Documents: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := hr.URL.String(); got != "https://api.cohere.com/v2/rerank" {
		t.Errorf("url = %s, want the path replaced rather than appended", got)
	}
	if hr.Header.Get("Authorization") != "Bearer sk" {
		t.Errorf("auth = %q", hr.Header.Get("Authorization"))
	}
}

func TestBuildRerankJoinsARelativePresetPath(t *testing.T) {
	hr, _, err := New().BuildRerank(context.Background(),
		&adapter.Target{BaseURL: "https://x/api/", Model: "m", RerankPath: "rerank"},
		&ir.RerankRequest{Query: "q", Documents: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := hr.URL.String(); got != "https://x/api/rerank" {
		t.Errorf("url = %s", got)
	}
}

func TestBuildRerankRefusesToGuessAPath(t *testing.T) {
	// The router only produces a rerank candidate for a preset declaring the
	// surface, and that preset must declare a path. Empty is a misconfiguration
	// and posting a rerank body at a guessed URL is the worse failure.
	_, _, err := New().BuildRerank(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "m"},
		&ir.RerankRequest{Query: "q", Documents: []string{"a"}})
	if err == nil {
		t.Fatal("a target with no rerank path built a request")
	}
	if !strings.Contains(err.Error(), "rerank-path") {
		t.Errorf("err = %v; it must name the quirk an operator has to set", err)
	}
}

func TestBuildRerankOmitsUnsetTopN(t *testing.T) {
	hr, _, err := New().BuildRerank(context.Background(),
		&adapter.Target{BaseURL: "https://x", Model: "m", RerankPath: "/v2/rerank"},
		&ir.RerankRequest{Query: "q", Documents: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	_ = json.Unmarshal(raw, &body)
	if _, present := body["top_n"]; present {
		t.Error("an unset top_n was sent; zero is not a legal value")
	}
	if body["model"] != "m" {
		t.Errorf("model = %v", body["model"])
	}
}

func TestParseRerankReadsTheV2Results(t *testing.T) {
	// Cohere v2 returns index and relevance_score and nothing else.
	resp := &http.Response{
		StatusCode: 200,
		Body: io.NopCloser(strings.NewReader(`{"id":"r-1","results":[
		  {"index":2,"relevance_score":0.98},
		  {"index":0,"relevance_score":0.11}]}`)),
	}
	out, err := New().ParseRerank(resp)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "r-1" || len(out.Results) != 2 {
		t.Fatalf("response = %+v", out)
	}
	if out.Results[0].Index != 2 || out.Results[0].RelevanceScore != 0.98 {
		t.Errorf("first result = %+v", out.Results[0])
	}
	if out.Results[0].Document != "" {
		t.Errorf("document = %q; the adapter must not invent one", out.Results[0].Document)
	}
}

func TestBuildRerankNeverSendsReturnDocuments(t *testing.T) {
	// It is a v1 parameter. Sending it to v2 asks for something the endpoint
	// does not define, and the client would get results with no documents.
	hr, _, err := New().BuildRerank(context.Background(),
		&adapter.Target{BaseURL: "https://x", Model: "m", RerankPath: "/v2/rerank"},
		&ir.RerankRequest{Query: "q", Documents: []string{"a"}, ReturnDocuments: true})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	_ = json.Unmarshal(raw, &body)
	if _, present := body["return_documents"]; present {
		t.Error("return_documents was forwarded to a v2 endpoint")
	}
	for _, k := range []string{"model", "query", "documents"} {
		if _, present := body[k]; !present {
			t.Errorf("body is missing %q: %v", k, body)
		}
	}
}

func TestParseRerankRejectsAResultlessBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`{"id":"r","results":[]}`)),
	}
	if _, err := New().ParseRerank(resp); err == nil {
		t.Fatal("a rerank 200 with no ranking parsed cleanly")
	}
}
```

Create `internal/exec/rerank_test.go`:

```go
package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

func rerankUpstream(seen *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"r-1","results":[{"index":0,"relevance_score":0.9}]}`))
	}
}

func TestRerankServesEndToEndAtThePresetPath(t *testing.T) {
	var path string
	upstream := httptest.NewServer(rerankUpstream(&path))
	defer upstream.Close()

	e, rec := executorForPreset(t, upstream.URL, "cohere",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	w := httptest.NewRecorder()
	e.HandleRerank(w, httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"rerank-v3.5","query":"q","documents":["a","b"]}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if path != "/v2/rerank" {
		t.Errorf("upstream path = %q, want the preset's declared path", path)
	}
	var body struct {
		Results []struct {
			Index int `json:"index"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 1 {
		t.Fatalf("body = %s", w.Body.String())
	}
	got := rec.only(t)
	if got.Surface != "rerank" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
}

func TestRerankEchoesDocumentsTheProviderDoesNotReturn(t *testing.T) {
	// Cohere v2 returns no documents. Darkrouter holds them and results carry
	// the index, so return_documents is honored here rather than forwarded.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"r-1","results":[
		  {"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.2}]}`))
	}))
	defer upstream.Close()

	e, _ := executorForPreset(t, upstream.URL, "cohere",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	w := httptest.NewRecorder()
	e.HandleRerank(w, httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"rerank-v3.5","query":"q","documents":["alpha","beta"],"return_documents":true}`)),
		openaiedge.New())

	var body struct {
		Results []struct {
			Index    int `json:"index"`
			Document *struct {
				Text string `json:"text"`
			} `json:"document"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 2 {
		t.Fatalf("body = %s", w.Body.String())
	}
	if body.Results[0].Document == nil || body.Results[0].Document.Text != "beta" {
		t.Errorf("first result document = %v, want the document at index 1", body.Results[0].Document)
	}
	if body.Results[1].Document == nil || body.Results[1].Document.Text != "alpha" {
		t.Errorf("second result document = %v", body.Results[1].Document)
	}
}

func TestRerankOmitsDocumentsWhenTheClientDidNotAsk(t *testing.T) {
	upstream := httptest.NewServer(rerankUpstream(nil))
	defer upstream.Close()

	e, _ := executorForPreset(t, upstream.URL, "cohere",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	w := httptest.NewRecorder()
	e.HandleRerank(w, httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"rerank-v3.5","query":"q","documents":["alpha"]}`)), openaiedge.New())

	if strings.Contains(w.Body.String(), "document") {
		t.Errorf("body = %s; a document was returned unasked", w.Body.String())
	}
}

func TestRerankFailsOverToASecondProvider(t *testing.T) {
	// Spec §10 requires a failover case per surface.
	var hits atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(rerankUpstream(nil))
	defer good.Close()

	e, rec := executorForTwoPresets(t, bad.URL, good.URL, "cohere",
		catalogPair("bad", "good", "rerank-v3.5", ir.SurfaceRerank))
	w := httptest.NewRecorder()
	e.HandleRerank(w, httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"rerank-v3.5","query":"q","documents":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("status = %d, failing provider hits = %d, body = %s",
			w.Code, hits.Load(), w.Body.String())
	}
	if got := rec.only(t); len(got.Attempts) != 2 || got.FinalProviderID != "good" {
		t.Errorf("attempts = %d, final = %q", len(got.Attempts), got.FinalProviderID)
	}
}

func TestARerankRequestWithNoRerankProviderIsRefused(t *testing.T) {
	upstream := httptest.NewServer(rerankUpstream(nil))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleRerank(w, httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
		`{"model":"chat-only","query":"q","documents":["a"]}`)), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(rec.only(t).Attempts) != 0 {
		t.Error("a surface no provider offers attempted an upstream call")
	}
}

func TestARerankRequestRecordsItsDroppedDocumentFields(t *testing.T) {
	upstream := httptest.NewServer(rerankUpstream(nil))
	defer upstream.Close()

	e, rec := executorForPreset(t, upstream.URL, "cohere",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	e.HandleRerank(httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(
			`{"model":"rerank-v3.5","query":"q","documents":[{"text":"t","title":"T"}]}`)),
		openaiedge.New())

	got := rec.only(t)
	if len(got.Warnings) == 0 || !strings.Contains(strings.Join(got.Warnings, " "), "title") {
		t.Errorf("warnings = %v; the dropped field never reached the request row", got.Warnings)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ ./internal/adapter/openaicompat/ ./internal/exec/ -run Rerank -v
```

Expected: FAIL to build in all three — `undefined: ParseRerank`, `undefined: BuildRerank`, `e.HandleRerank undefined`.

- [ ] **Step 3: Add the IR types and the adapter interface**

Append to `internal/ir/aux.go`:

```go
// RerankRequest is one Cohere-v2 rerank call. OpenAI defines no rerank
// endpoint, so this shape is both the inbound contract and the outbound one.
type RerankRequest struct {
	Model string
	Query string
	// Documents is always text. Cohere v2 accepts document objects too; the
	// edge takes their text field and warns about the rest, because a document
	// reranked on its text alone is not something the response reveals.
	Documents       []string
	TopN            int // 0 when unset; zero is not a legal value
	ReturnDocuments bool
	// Warnings are what the inbound parse could not express. They ride on the
	// request because the edge, not the adapter, is where the loss happens.
	Warnings []Warning
}

// DocumentCount is recorded on the request row per spec §9.
func (r *RerankRequest) DocumentCount() int { return len(r.Documents) }

type RerankResult struct {
	Index          int
	RelevanceScore float64
	// Document is populated only when the client set return_documents.
	Document string
}

type RerankResponse struct {
	ID      string
	Model   string
	Results []RerankResult
	// Usage is zero: Cohere bills rerank in search units, not tokens.
	Usage Usage
}
```

In `internal/adapter/adapter.go`, beside `Moderator`:

```go
// Reranker is implemented by an adapter serving the rerank surface.
type Reranker interface {
	BuildRerank(ctx context.Context, t *Target, req *ir.RerankRequest) (*http.Request, []ir.Warning, error)
	// ParseRerank takes ownership of resp.Body and always closes it.
	ParseRerank(resp *http.Response) (*ir.RerankResponse, error)
}
```

- [ ] **Step 4: Add the edge interface, parser and writer**

In `internal/edge/edge.go`, beside `ModerationDialect`:

```go
// RerankDialect is the inbound wire form of the rerank surface. Its shape is
// Cohere v2, which Darkrouter adopts because OpenAI defines no rerank endpoint.
type RerankDialect interface {
	Name() string
	ParseRerank(r *http.Request, maxBody int64) (*ir.RerankRequest, error)
	WriteRerank(w http.ResponseWriter, resp *ir.RerankResponse) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}
```

Append to `internal/edge/openai/aux.go`:

```go
type wireRerankRequest struct {
	Model           string            `json:"model"`
	Query           string            `json:"query"`
	Documents       []json.RawMessage `json:"documents"`
	TopN            *int              `json:"top_n"`
	ReturnDocuments bool              `json:"return_documents"`
}

func ParseRerank(r *http.Request, maxBody int64) (*ir.RerankRequest, error) {
	body, err := readCappedBody(r, maxBody)
	if err != nil {
		return nil, err
	}
	var w wireRerankRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	if w.Query == "" {
		return nil, errors.New("query is required")
	}
	if len(w.Documents) == 0 {
		return nil, errors.New("documents is required and must not be empty")
	}
	req := &ir.RerankRequest{
		Model: w.Model, Query: w.Query, ReturnDocuments: w.ReturnDocuments,
		Documents: make([]string, 0, len(w.Documents)),
	}
	if w.TopN != nil {
		req.TopN = *w.TopN
	}
	for i, raw := range w.Documents {
		text, dropped, err := rerankDocument(raw)
		if err != nil {
			return nil, fmt.Errorf("documents[%d]: %w", i, err)
		}
		req.Documents = append(req.Documents, text)
		if len(dropped) > 0 {
			req.Warnings = append(req.Warnings, ir.Warning{
				Field:  fmt.Sprintf("documents[%d]", i),
				Target: "rerank",
				Reason: "reranked on text alone; dropped " + strings.Join(dropped, ", "),
			})
		}
	}
	return req, nil
}

// rerankDocument reads one document, which Cohere v2 allows as a string or an
// object. An object contributes its text field; every other field is reported
// so the trace can say the document was ranked on less than it carried.
func rerankDocument(raw json.RawMessage) (string, []string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", nil, errors.New("document is empty")
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", nil, err
		}
		return s, nil, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return "", nil, errors.New("document must be text or an object with a text field")
	}
	var text string
	if t, ok := obj["text"]; ok {
		if err := json.Unmarshal(t, &text); err != nil {
			return "", nil, fmt.Errorf("text: %w", err)
		}
	}
	if text == "" {
		return "", nil, errors.New("document object has no text field")
	}
	dropped := make([]string, 0, len(obj))
	for k := range obj {
		if k != "text" {
			dropped = append(dropped, k)
		}
	}
	// Sorted so the warning text is stable: map iteration order would make an
	// otherwise identical request produce a different trace line each time.
	sort.Strings(dropped)
	return text, dropped, nil
}

func WriteRerank(w http.ResponseWriter, resp *ir.RerankResponse) error {
	results := make([]any, 0, len(resp.Results))
	for _, r := range resp.Results {
		row := map[string]any{"index": r.Index, "relevance_score": r.RelevanceScore}
		// The key is omitted rather than null when the client did not ask for
		// documents: a Cohere client tests for its presence.
		if r.Document != "" {
			row["document"] = map[string]any{"text": r.Document}
		}
		results = append(results, row)
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{
		"id": resp.ID, "results": results,
	})
}

func (d *Dialect) ParseRerank(r *http.Request, maxBody int64) (*ir.RerankRequest, error) {
	return ParseRerank(r, maxBody)
}

func (d *Dialect) WriteRerank(w http.ResponseWriter, resp *ir.RerankResponse) error {
	return WriteRerank(w, resp)
}

var _ edge.RerankDialect = (*Dialect)(nil)
```

Add `"sort"` and `"strings"` to that file's imports.

- [ ] **Step 5: Implement the adapter**

Create `internal/adapter/openaicompat/rerank.go`:

```go
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

const maxRerankBytes = 8 << 20

func (a *Adapter) BuildRerank(ctx context.Context, t *adapter.Target,
	req *ir.RerankRequest) (*http.Request, []ir.Warning, error) {

	if t.RerankPath == "" {
		return nil, nil, errors.New(
			"this provider declares no rerank-path quirk; rerank cannot be served without one")
	}
	// model, query, documents and top_n are the whole of Cohere v2's request.
	// return_documents is a v1 parameter v2 does not define, so it is honored
	// at the edge from the buffered request rather than forwarded here.
	body := map[string]any{
		"model": t.Model, "query": req.Query, "documents": req.Documents,
	}
	// Zero is not a legal top_n, so absence needs no separate presence flag.
	if req.TopN > 0 {
		body["top_n"] = req.TopN
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	endpoint, err := rerankURL(t.BaseURL, t.RerankPath)
	if err != nil {
		return nil, nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build rerank request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	// The inbound parse's losses ride out with the request so the loop records
	// them on the row that describes the attempt they applied to.
	return hr, req.Warnings, nil
}

// rerankURL resolves the preset-declared path against the provider's base URL.
//
// A path beginning with "/" replaces the base URL's path entirely. That is not
// a stylistic choice: cohere's base URL is
// https://api.cohere.com/compatibility/v1 — an OpenAI-compatibility shim — and
// its rerank endpoint is the native /v2/rerank. Appending would produce
// /compatibility/v1/v2/rerank, which does not exist.
func rerankURL(base, path string) (string, error) {
	if strings.HasPrefix(path, "/") {
		u, err := url.Parse(base)
		if err != nil {
			return "", fmt.Errorf("provider base URL: %w", err)
		}
		u.Path = path
		u.RawQuery = ""
		u.Fragment = ""
		return u.String(), nil
	}
	return strings.TrimRight(base, "/") + "/" + path, nil
}

// rerankEnvelope is Cohere v2's response: results carry an index and a score
// and nothing else. There is no document object to read, which is why the op
// fills documents from the request it sent.
type rerankEnvelope struct {
	ID      string `json:"id"`
	Results []struct {
		Index int     `json:"index"`
		Score float64 `json:"relevance_score"`
	} `json:"results"`
}

func (a *Adapter) ParseRerank(resp *http.Response) (*ir.RerankResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRerankBytes))
	if err != nil {
		return nil, fmt.Errorf("read rerank response: %w", err)
	}
	var env rerankEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse rerank response: %w", err)
	}
	if len(env.Results) == 0 {
		// A 200 with no ranking is a provider fault: the documents went up and
		// no ordering came back.
		return nil, errors.New("rerank response carried no results")
	}
	out := &ir.RerankResponse{ID: env.ID, Results: make([]ir.RerankResult, 0, len(env.Results))}
	for _, r := range env.Results {
		out.Results = append(out.Results, ir.RerankResult{Index: r.Index, RelevanceScore: r.Score})
	}
	return out, nil
}

var _ adapter.Reranker = (*Adapter)(nil)
```

- [ ] **Step 6: Write the op and the route**

Create `internal/exec/rerank.go`:

```go
package exec

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

type rerankOp struct {
	d   edge.RerankDialect
	req *ir.RerankRequest
}

func (o *rerankOp) Dialect() string { return o.d.Name() }

func (o *rerankOp) Query() router.Query {
	return router.Query{Model: o.req.Model, Surface: ir.SurfaceRerank}
}

func (o *rerankOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	rr, ok := ad.(adapter.Reranker)
	if !ok {
		return nil, nil, fmt.Errorf("adapter %s does not serve rerank", ad.Kind())
	}
	return rr.BuildRerank(ctx, tgt, o.req)
}

func (o *rerankOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	rr, ok := ac.Adapter.(adapter.Reranker)
	if !ok {
		resp.Body.Close()
		return adapter.OutcomeFatal, &ir.Error{
			Type: ir.ErrDarkrouter, Message: "adapter does not serve rerank",
		}
	}
	out, err := rr.ParseRerank(resp)
	if err != nil {
		return failedParse(ac, resp, err)
	}
	// The provider's own id is echoed, but the model it ranked with is the
	// candidate's: a rerank response carries no model field.
	out.Model = ac.Cand.Model

	// Cohere v2 returns no documents, so return_documents is honored here from
	// the request Darkrouter sent. Forwarding the parameter instead would
	// return results with no documents while the client believed it had asked
	// for them. An out-of-range index is a provider fault and is left empty
	// rather than panicking on a slice the response does not agree with.
	if o.req.ReturnDocuments {
		for i, r := range out.Results {
			if r.Index >= 0 && r.Index < len(o.req.Documents) {
				out.Results[i].Document = o.req.Documents[r.Index]
			}
		}
	}

	ttft := time.Since(ac.Rec.TS).Milliseconds()
	ac.Rec.TTFTMs = &ttft
	applyUsage(ac.Rec, &out.Usage)
	ac.Rec.FinalProviderID = ac.Cand.ProviderID
	ac.Rec.FinalModel = ac.Cand.Model
	ac.Rec.Warnings = warningStrings(ac.Warns)

	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	_ = o.d.WriteRerank(cw, out)
	return adapter.OutcomeSuccess, nil
}

func (o *rerankOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*rerankOp)(nil)

// HandleRerank serves POST /v1/rerank.
func (e *Executor) HandleRerank(w http.ResponseWriter, r *http.Request, d edge.RerankDialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceRerank, d, func(cfg *config.Config) (SurfaceOp, error) {
		req, err := d.ParseRerank(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &rerankOp{d: d, req: req}, nil
	})
}
```

In `internal/server/server.go`, beside the moderations route:

```go
	mux.HandleFunc("POST /v1/rerank", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleRerank(w, r, oa)
	}))
```

- [ ] **Step 7: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ ./internal/adapter/openaicompat/ ./internal/exec/ ./internal/server/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in all four packages.

- [ ] **Step 8: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 9: Commit**

```bash
git add internal/ir/aux.go internal/adapter/adapter.go internal/adapter/openaicompat/rerank.go \
        internal/adapter/openaicompat/rerank_test.go internal/edge/edge.go \
        internal/edge/openai/aux.go internal/edge/openai/aux_test.go \
        internal/exec/rerank.go internal/exec/rerank_test.go internal/server/server.go
git commit -m "feat(exec): serve the rerank surface"
```

---

### Task 20: Images end to end

**Files:**
- Modify: `internal/ir/aux.go`, `internal/adapter/adapter.go`, `internal/edge/edge.go`, `internal/edge/openai/aux.go`, `internal/server/server.go`
- Create: `internal/adapter/openaicompat/image.go`, `internal/exec/image.go`
- Test: `internal/adapter/openaicompat/image_test.go`, `internal/exec/image_test.go`, `internal/edge/openai/aux_test.go`

**Interfaces:**
- Consumes: `RunAux` (Task 14), `failedParse` (Task 15).
- Produces: `ir.ImageRequest`, `ir.ImageResponse`, `ir.Image`, `adapter.ImageGenerator`, `edge.ImageDialect`, and `(*Executor).HandleImages`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: this is Task 16's layout with the image types substituted, and every file is given below.

**Two models, two usage stories, and the difference is not a detail.** `gpt-image-1` returns a `usage` object with `input_tokens` and `output_tokens`, including image input tokens. The `dall-e` models return no `usage` key at all. Zero tokens therefore means *not reported*, never *free*, and the cost column must stay NULL for a dall-e call rather than record a confident zero. Task 21 is where that rule is enforced; this task's job is to carry the distinction faithfully rather than to normalize it away.

`response_format` is forwarded exactly as the client sent it. It is a `dall-e` parameter that `gpt-image-1` rejects with a 400 — but a client sending it to `gpt-image-1` gets the same 400 talking to OpenAI directly, and inventing a translation here would make Darkrouter's behavior differ from the provider's for no gain.

Image bytes are large. The response cap is 64 MiB rather than the JSON default because a `b64_json` response carrying four 1024×1024 PNGs is several megabytes of base64, and a cap that rejects a legitimate response is worse than no cap at all.

- [ ] **Step 1: Write the failing tests**

Add to `internal/edge/openai/aux_test.go`:

```go
func TestParseImageReadsTheOptionals(t *testing.T) {
	req, err := ParseImage(httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(
		`{"model":"gpt-image-1","prompt":"a cat","n":2,"size":"1024x1024",
		  "quality":"high","style":"vivid","response_format":"b64_json",
		  "background":"transparent","output_format":"png","user":"u"}`)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-image-1" || req.Prompt != "a cat" || req.N != 2 {
		t.Errorf("request = %+v", req)
	}
	if req.Size != "1024x1024" || req.Quality != "high" || req.Style != "vivid" {
		t.Errorf("request = %+v", req)
	}
	if req.ResponseFormat != "b64_json" || req.Background != "transparent" ||
		req.OutputFormat != "png" || req.User != "u" {
		t.Errorf("request = %+v", req)
	}
}

func TestParseImageRejectsStreaming(t *testing.T) {
	// The op parses a JSON body. Accepting stream:true would fail on the first
	// SSE event with an error that says nothing useful.
	_, err := ParseImage(httptest.NewRequest("POST", "/v1/images/generations",
		strings.NewReader(`{"model":"gpt-image-1","prompt":"a cat","stream":true}`)), 1<<20)
	if err == nil {
		t.Fatal("a streamed image request was accepted")
	}
	if !strings.Contains(err.Error(), "stream") {
		t.Errorf("err = %v; it must name what is unsupported", err)
	}
}

func TestParseImageRejectsAnEmptyPrompt(t *testing.T) {
	if _, err := ParseImage(httptest.NewRequest("POST", "/v1/images/generations",
		strings.NewReader(`{"model":"m"}`)), 1<<20); err == nil {
		t.Error("a promptless generation request was accepted")
	}
}

func TestWriteImageEmitsBothPayloadForms(t *testing.T) {
	w := httptest.NewRecorder()
	if err := WriteImage(w, &ir.ImageResponse{
		Created: 1700000000,
		Images: []ir.Image{
			{URL: "https://example.invalid/a.png", RevisedPrompt: "a revised cat"},
			{Base64: "aGVsbG8="},
		},
		Usage: ir.Usage{InputTokens: 11, OutputTokens: 22}, UsageReported: true,
	}); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Created int64 `json:"created"`
		Data    []struct {
			URL           string `json:"url"`
			B64           string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Created != 1700000000 || len(body.Data) != 2 {
		t.Fatalf("body = %s", w.Body.String())
	}
	if body.Data[0].URL == "" || body.Data[0].RevisedPrompt != "a revised cat" {
		t.Errorf("first image = %+v", body.Data[0])
	}
	if body.Data[0].B64 != "" {
		t.Errorf("a URL image carried a b64_json key: %+v", body.Data[0])
	}
	if body.Data[1].B64 != "aGVsbG8=" || body.Data[1].URL != "" {
		t.Errorf("second image = %+v", body.Data[1])
	}
	if body.Usage == nil || body.Usage.TotalTokens != 33 {
		t.Errorf("usage = %+v", body.Usage)
	}
}

func TestWriteImageOmitsUsageWhenTheProviderReportedNone(t *testing.T) {
	// The dall-e models return no usage object. Emitting a zeroed one would
	// tell the client the call was free.
	w := httptest.NewRecorder()
	if err := WriteImage(w, &ir.ImageResponse{
		Created: 1, Images: []ir.Image{{URL: "https://example.invalid/a.png"}},
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "usage") {
		t.Errorf("body = %s; a usage object was invented", w.Body.String())
	}
}
```

Create `internal/adapter/openaicompat/image_test.go`:

```go
package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildImageRendersTheOpenAIShape(t *testing.T) {
	hr, warns, err := New().BuildImage(context.Background(),
		&adapter.Target{BaseURL: "https://api.example.com/v1/", APIKey: "sk", Model: "gpt-image-1"},
		&ir.ImageRequest{
			Prompt: "a cat", N: 2, Size: "1024x1024", Quality: "high",
			ResponseFormat: "b64_json", User: "u",
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	if hr.URL.String() != "https://api.example.com/v1/images/generations" {
		t.Errorf("url = %s", hr.URL)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-image-1" || body["prompt"] != "a cat" {
		t.Errorf("body = %v", body)
	}
	if body["n"].(float64) != 2 || body["response_format"] != "b64_json" {
		t.Errorf("body = %v", body)
	}
}

func TestBuildImageOmitsEveryUnsetOptional(t *testing.T) {
	// An explicit null or empty string is a 400 on several of these, and a
	// zero n asks for no images at all.
	hr, _, err := New().BuildImage(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "dall-e-3"},
		&ir.ImageRequest{Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	_ = json.Unmarshal(raw, &body)
	for _, k := range []string{"n", "size", "quality", "style", "response_format",
		"background", "output_format", "moderation", "output_compression", "user"} {
		if _, present := body[k]; present {
			t.Errorf("an unset %q was sent as %v", k, body[k])
		}
	}
}

func TestParseImageReportsUsageWhenTheProviderDoes(t *testing.T) {
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(
		`{"created":17,"data":[{"b64_json":"aGk="}],
		  "usage":{"input_tokens":11,"output_tokens":22,"total_tokens":33}}`))}
	out, err := New().ParseImage(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !out.UsageReported {
		t.Error("UsageReported = false on a response carrying usage")
	}
	if out.Usage.InputTokens != 11 || out.Usage.OutputTokens != 22 {
		t.Errorf("usage = %+v", out.Usage)
	}
	if len(out.Images) != 1 || out.Images[0].Base64 != "aGk=" {
		t.Errorf("images = %+v", out.Images)
	}
}

func TestParseImageDistinguishesAbsentUsageFromZero(t *testing.T) {
	// The dall-e models report none. Zero tokens must mean "not reported", not
	// "free", or the cost column records a confident zero.
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(
		`{"created":17,"data":[{"url":"https://example.invalid/a.png","revised_prompt":"r"}]}`))}
	out, err := New().ParseImage(resp)
	if err != nil {
		t.Fatal(err)
	}
	if out.UsageReported {
		t.Error("UsageReported = true on a response with no usage object")
	}
	if out.Images[0].URL == "" || out.Images[0].RevisedPrompt != "r" {
		t.Errorf("images = %+v", out.Images)
	}
}

func TestParseImageRejectsAnImagelessBody(t *testing.T) {
	resp := &http.Response{StatusCode: 200,
		Body: io.NopCloser(strings.NewReader(`{"created":1,"data":[]}`))}
	if _, err := New().ParseImage(resp); err == nil {
		t.Fatal("a 200 with no images parsed cleanly")
	}
}
```

Create `internal/exec/image_test.go`:

```go
package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

func imageUpstream(usage bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"created":17,"data":[{"b64_json":"aGk="}]`
		if usage {
			body += `,"usage":{"input_tokens":11,"output_tokens":22,"total_tokens":33}`
		}
		_, _ = w.Write([]byte(body + `}`))
	}
}

func TestImagesServeEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(imageUpstream(true))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "gpt-image-1", ir.SurfaceImage))
	w := httptest.NewRecorder()
	e.HandleImages(w, httptest.NewRequest("POST", "/v1/images/generations",
		strings.NewReader(`{"model":"gpt-image-1","prompt":"a cat","n":1}`)), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Data []struct {
			B64 string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].B64 != "aGk=" {
		t.Fatalf("body = %s", w.Body.String())
	}
	got := rec.only(t)
	if got.Surface != "image" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
	if got.TokensIn != 11 || got.TokensOut != 22 {
		t.Errorf("tokens = %d in, %d out; gpt-image-1 reports both", got.TokensIn, got.TokensOut)
	}
}

func TestAnImageCallReportingNoUsageRecordsNone(t *testing.T) {
	upstream := httptest.NewServer(imageUpstream(false))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "dall-e-3", ir.SurfaceImage))
	e.HandleImages(httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/images/generations",
			strings.NewReader(`{"model":"dall-e-3","prompt":"a cat"}`)), openaiedge.New())

	got := rec.only(t)
	if got.Status != "success" {
		t.Fatalf("status = %q", got.Status)
	}
	if got.TokensIn != 0 || got.TokensOut != 0 {
		t.Errorf("tokens = %d/%d; the provider reported none", got.TokensIn, got.TokensOut)
	}
	// CostMicros is nil for every surface today — nothing computes cost, and
	// the reason is recorded as a carried-forward item in Task 33. This is a
	// guard for when pricing lands: a dall-e call must stay NULL rather than
	// record a confident zero.
	if got.CostMicros != nil {
		t.Errorf("cost = %d; a call with no reported usage must leave cost NULL", *got.CostMicros)
	}
}

func TestImagesFailOverToASecondProvider(t *testing.T) {
	// Spec §10 requires a failover case per surface.
	var hits atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(imageUpstream(true))
	defer good.Close()

	e, rec := executorForTwo(t, bad.URL, good.URL,
		catalogPair("bad", "good", "gpt-image-1", ir.SurfaceImage))
	w := httptest.NewRecorder()
	e.HandleImages(w, httptest.NewRequest("POST", "/v1/images/generations",
		strings.NewReader(`{"model":"gpt-image-1","prompt":"a cat"}`)), openaiedge.New())

	if w.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("status = %d, failing provider hits = %d", w.Code, hits.Load())
	}
	if got := rec.only(t); len(got.Attempts) != 2 || got.FinalProviderID != "good" {
		t.Errorf("attempts = %d, final = %q", len(got.Attempts), got.FinalProviderID)
	}
}

func TestAnImageRequestWithNoImageProviderIsRefused(t *testing.T) {
	upstream := httptest.NewServer(imageUpstream(true))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleImages(w, httptest.NewRequest("POST", "/v1/images/generations",
		strings.NewReader(`{"model":"chat-only","prompt":"a cat"}`)), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(rec.only(t).Attempts) != 0 {
		t.Error("a surface no provider offers attempted an upstream call")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ ./internal/adapter/openaicompat/ ./internal/exec/ -run Image -v
```

Expected: FAIL to build in all three — `undefined: ParseImage`, `undefined: BuildImage`, `e.HandleImages undefined`.

- [ ] **Step 3: Add the IR types and the adapter interface**

Append to `internal/ir/aux.go`:

```go
// ImageRequest is one generation call.
//
// ResponseFormat is carried and forwarded verbatim although gpt-image-1 rejects
// it and the dall-e models require it. A client sending it to gpt-image-1 gets
// the same 400 talking to the provider directly, and translating it here would
// make Darkrouter behave differently from the upstream for no gain.
type ImageRequest struct {
	Model  string
	Prompt string
	// N is 0 when unset. Zero images is not a request anyone makes, so it needs
	// no separate presence flag.
	N              int
	Size           string
	Quality        string
	Style          string
	ResponseFormat string
	Background     string
	OutputFormat   string
	// Moderation and OutputCompression are gpt-image-1 parameters. They are
	// carried rather than dropped because a client that set them gets
	// different images without them, and nothing in the response would say so.
	Moderation        string
	OutputCompression int
	User              string
}

// ImageCount is what was asked for, recorded on the request row per spec §9.
// An unset n means one image, which is OpenAI's own default.
func (r *ImageRequest) ImageCount() int {
	if r.N <= 0 {
		return 1
	}
	return r.N
}

// Image is one generated image. Exactly one of URL and Base64 is populated,
// chosen by the provider rather than by the request: gpt-image-1 always returns
// base64 whatever response_format said.
type Image struct {
	URL    string
	Base64 string
	// RevisedPrompt is what the provider actually generated from, when it says.
	RevisedPrompt string
}

type ImageResponse struct {
	Created int64
	Model   string
	Images  []Image

	Usage Usage
	// UsageReported distinguishes "the provider reported zero" from "the
	// provider reported nothing". gpt-image-1 returns a usage object; the
	// dall-e models return none, and recording their calls as zero-cost would
	// be a confident lie rather than a missing value.
	UsageReported bool
}
```

In `internal/adapter/adapter.go`, beside `Reranker`:

```go
// ImageGenerator is implemented by an adapter serving the image surface.
type ImageGenerator interface {
	BuildImage(ctx context.Context, t *Target, req *ir.ImageRequest) (*http.Request, []ir.Warning, error)
	// ParseImage takes ownership of resp.Body and always closes it.
	ParseImage(resp *http.Response) (*ir.ImageResponse, error)
}
```

- [ ] **Step 4: Add the edge interface, parser and writer**

In `internal/edge/edge.go`, beside `RerankDialect`:

```go
// ImageDialect is the inbound wire form of the image surface.
type ImageDialect interface {
	Name() string
	ParseImage(r *http.Request, maxBody int64) (*ir.ImageRequest, error)
	WriteImage(w http.ResponseWriter, resp *ir.ImageResponse) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}
```

Append to `internal/edge/openai/aux.go`:

```go
type wireImageRequest struct {
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	N                 *int   `json:"n"`
	Size              string `json:"size"`
	Quality           string `json:"quality"`
	Style             string `json:"style"`
	ResponseFormat    string `json:"response_format"`
	Background        string `json:"background"`
	OutputFormat      string `json:"output_format"`
	Moderation        string `json:"moderation"`
	OutputCompression *int   `json:"output_compression"`
	Stream            bool   `json:"stream"`
	User              string `json:"user"`
}

func ParseImage(r *http.Request, maxBody int64) (*ir.ImageRequest, error) {
	body, err := readCappedBody(r, maxBody)
	if err != nil {
		return nil, err
	}
	var w wireImageRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	if w.Prompt == "" {
		return nil, errors.New("prompt is required")
	}
	if w.Stream {
		// gpt-image-1 streams partial images as SSE. The image op parses a JSON
		// body, so accepting this would fail on the first event with an
		// unhelpful error; refusing it says what is actually supported.
		return nil, errors.New(
			"streamed image generation is not supported; omit stream to receive the finished images")
	}
	req := &ir.ImageRequest{
		Model: w.Model, Prompt: w.Prompt, Size: w.Size, Quality: w.Quality,
		Style: w.Style, ResponseFormat: w.ResponseFormat,
		Background: w.Background, OutputFormat: w.OutputFormat,
		Moderation: w.Moderation, User: w.User,
	}
	if w.N != nil {
		req.N = *w.N
	}
	if w.OutputCompression != nil {
		req.OutputCompression = *w.OutputCompression
	}
	return req, nil
}

func WriteImage(w http.ResponseWriter, resp *ir.ImageResponse) error {
	data := make([]any, 0, len(resp.Images))
	for _, img := range resp.Images {
		row := map[string]any{}
		// Exactly one payload key, and never both: a client tests for the one
		// it asked for and an empty string in the other reads as a real answer.
		if img.Base64 != "" {
			row["b64_json"] = img.Base64
		} else {
			row["url"] = img.URL
		}
		if img.RevisedPrompt != "" {
			row["revised_prompt"] = img.RevisedPrompt
		}
		data = append(data, row)
	}
	out := map[string]any{"created": resp.Created, "data": data}
	// Omitted, not zeroed, when the provider reported nothing: the dall-e
	// models return no usage object and a zeroed one says the call was free.
	if resp.UsageReported {
		out["usage"] = map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"total_tokens":  resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

func (d *Dialect) ParseImage(r *http.Request, maxBody int64) (*ir.ImageRequest, error) {
	return ParseImage(r, maxBody)
}

func (d *Dialect) WriteImage(w http.ResponseWriter, resp *ir.ImageResponse) error {
	return WriteImage(w, resp)
}

var _ edge.ImageDialect = (*Dialect)(nil)
```

- [ ] **Step 5: Implement the adapter**

Create `internal/adapter/openaicompat/image.go`:

```go
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// maxImageBytes is far above the JSON default because a b64_json response
// carrying four 1024x1024 PNGs is several megabytes of base64, and a cap that
// rejects a legitimate response is worse than no cap at all.
const maxImageBytes = 64 << 20

func (a *Adapter) BuildImage(ctx context.Context, t *adapter.Target,
	req *ir.ImageRequest) (*http.Request, []ir.Warning, error) {

	body := map[string]any{"model": t.Model, "prompt": req.Prompt}
	// Each is omitted rather than sent empty: an explicit null or "" is a 400
	// on several of them, and n=0 asks for no images at all.
	if req.N > 0 {
		body["n"] = req.N
	}
	for k, v := range map[string]string{
		"size":            req.Size,
		"quality":         req.Quality,
		"style":           req.Style,
		"response_format": req.ResponseFormat,
		"background":      req.Background,
		"output_format":   req.OutputFormat,
		"moderation":      req.Moderation,
		"user":            req.User,
	} {
		if v != "" {
			body[k] = v
		}
	}
	if req.OutputCompression > 0 {
		body["output_compression"] = req.OutputCompression
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/images/generations"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build image request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil, nil
}

// imageEnvelope keeps usage as a pointer so an absent object stays
// distinguishable from a reported zero.
type imageEnvelope struct {
	Created int64 `json:"created"`
	Data    []struct {
		URL           string `json:"url"`
		B64           string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (a *Adapter) ParseImage(resp *http.Response) (*ir.ImageResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return nil, fmt.Errorf("read image response: %w", err)
	}
	var env imageEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse image response: %w", err)
	}
	if len(env.Data) == 0 {
		// A 200 with no images is a provider fault: the prompt was accepted and
		// nothing came back.
		return nil, errors.New("image response carried no images")
	}
	out := &ir.ImageResponse{
		Created: env.Created,
		Images:  make([]ir.Image, 0, len(env.Data)),
	}
	for _, d := range env.Data {
		out.Images = append(out.Images, ir.Image{
			URL: d.URL, Base64: d.B64, RevisedPrompt: d.RevisedPrompt,
		})
	}
	if env.Usage != nil {
		out.UsageReported = true
		out.Usage = ir.Usage{
			InputTokens:  env.Usage.InputTokens,
			OutputTokens: env.Usage.OutputTokens,
		}
	}
	return out, nil
}

var _ adapter.ImageGenerator = (*Adapter)(nil)
```

- [ ] **Step 6: Write the op and the route**

Create `internal/exec/image.go`:

```go
package exec

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

type imageOp struct {
	d   edge.ImageDialect
	req *ir.ImageRequest
}

func (o *imageOp) Dialect() string { return o.d.Name() }

func (o *imageOp) Query() router.Query {
	return router.Query{Model: o.req.Model, Surface: ir.SurfaceImage}
}

func (o *imageOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	g, ok := ad.(adapter.ImageGenerator)
	if !ok {
		return nil, nil, fmt.Errorf("adapter %s does not serve images", ad.Kind())
	}
	return g.BuildImage(ctx, tgt, o.req)
}

func (o *imageOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	g, ok := ac.Adapter.(adapter.ImageGenerator)
	if !ok {
		resp.Body.Close()
		return adapter.OutcomeFatal, &ir.Error{
			Type: ir.ErrDarkrouter, Message: "adapter does not serve images",
		}
	}
	out, err := g.ParseImage(resp)
	if err != nil {
		return failedParse(ac, resp, err)
	}
	out.Model = ac.Cand.Model

	ttft := time.Since(ac.Rec.TS).Milliseconds()
	ac.Rec.TTFTMs = &ttft
	// Only when the provider reported it. A dall-e call recorded as zero tokens
	// is indistinguishable in the log from a call that genuinely cost nothing.
	if out.UsageReported {
		applyUsage(ac.Rec, &out.Usage)
	}
	ac.Rec.FinalProviderID = ac.Cand.ProviderID
	ac.Rec.FinalModel = ac.Cand.Model
	ac.Rec.Warnings = warningStrings(ac.Warns)

	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	_ = o.d.WriteImage(cw, out)
	return adapter.OutcomeSuccess, nil
}

func (o *imageOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*imageOp)(nil)

// HandleImages serves POST /v1/images/generations.
func (e *Executor) HandleImages(w http.ResponseWriter, r *http.Request, d edge.ImageDialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceImage, d, func(cfg *config.Config) (SurfaceOp, error) {
		req, err := d.ParseImage(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &imageOp{d: d, req: req}, nil
	})
}
```

In `internal/server/server.go`, beside the rerank route:

```go
	mux.HandleFunc("POST /v1/images/generations", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleImages(w, r, oa)
	}))
```

- [ ] **Step 7: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ ./internal/adapter/openaicompat/ ./internal/exec/ ./internal/server/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in all four packages.

- [ ] **Step 8: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 9: Commit**

```bash
git add internal/ir/aux.go internal/adapter/adapter.go internal/adapter/openaicompat/image.go \
        internal/adapter/openaicompat/image_test.go internal/edge/edge.go \
        internal/edge/openai/aux.go internal/edge/openai/aux_test.go \
        internal/exec/image.go internal/exec/image_test.go internal/server/server.go
git commit -m "feat(exec): serve the image surface"
```

---

### Task 21: An oversized body is a 413, not a 400

**Files:**
- Modify: `internal/ir/ir.go`, `internal/edge/openai/write.go`, `internal/edge/anthropic/write.go`, `internal/edge/gemini/write.go`, `internal/exec/surface.go`
- Test: `internal/edge/openai/write_test.go`, `internal/edge/anthropic/write_test.go`, `internal/edge/gemini/write_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ir.ErrPayloadTooLarge` and its status mapping in all three dialects; `RunAux` honors it. Task 22's multipart parser raises it.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: all three status tables exist and this adds one case to each.

Spec §6 requires an oversized upload to be "rejected with 413 before any upstream connection". `RunAux` turns every parse failure into `ir.ErrInvalidRequest`, which every dialect maps to 400. To a client uploading a 90-minute recording those two answers mean completely different things — 400 says the request is malformed and retrying is pointless, 413 says send a smaller file — and the client has no other signal to tell them apart.

The type is added to all three dialects rather than just OpenAI's. Both the JSON parsers and the multipart one raise it, so an oversized embedding batch and an oversized upload answer the same way. Only the auxiliary routes raise it today, and they are all OpenAI-dialect. But `statusFor` has no fall-through that would be right: an unmapped type becomes a 502 in OpenAI's table and Anthropic's, which would report a client's oversized upload as a gateway failure the moment any later phase raised it from a chat path.

- [ ] **Step 1: Write the failing tests**

Add to `internal/edge/openai/write_test.go`:

```go
func TestAnOversizedPayloadIs413(t *testing.T) {
	// 400 tells a client its request is malformed and retrying is pointless.
	// 413 tells it to send a smaller file. An audio client has no other signal
	// to tell those apart.
	w := httptest.NewRecorder()
	if err := WriteError(w, &ir.Error{
		Type: ir.ErrPayloadTooLarge, Message: "upload exceeds the configured maximum",
	}); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}
```

Add the equivalent to `internal/edge/anthropic/write_test.go` and `internal/edge/gemini/write_test.go`, asserting 413 in each. Anthropic's error `type` string is `"invalid_request_error"` — it has no size-specific member and inventing one would break clients matching on the documented set — and Gemini's status string is `"INVALID_ARGUMENT"` for the same reason. **The HTTP status is what carries the distinction; the body stays inside each provider's documented vocabulary.** Assert both in each test.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/... -run Oversized -v
```

Expected: FAIL to build — `undefined: ir.ErrPayloadTooLarge`.

- [ ] **Step 3: Add the type**

In `internal/ir/ir.go`, in the `ErrorType` block:

```go
	// ErrPayloadTooLarge is an inbound body over the configured maximum. It is
	// separate from ErrInvalidRequest because the two ask the client for
	// different things: one says the request is malformed, the other says send
	// less. A transcription client uploading a long recording can act on the
	// second and not on the first.
	ErrPayloadTooLarge ErrorType = "payload_too_large"
```

- [ ] **Step 4: Map it in all three dialects**

In `internal/edge/openai/write.go`, in `statusFor`:

```go
	case ir.ErrPayloadTooLarge:
		return http.StatusRequestEntityTooLarge
```

In `internal/edge/anthropic/write.go`, add to its type-and-status switch:

```go
	case ir.ErrPayloadTooLarge:
		// Anthropic documents no size-specific error type, and inventing one
		// would break clients matching on the documented set. The status is
		// what carries the distinction.
		return "invalid_request_error", http.StatusRequestEntityTooLarge
```

In `internal/edge/gemini/write.go`, likewise:

```go
	case ir.ErrPayloadTooLarge:
		// Same reasoning as Anthropic: the google.rpc.Code vocabulary is fixed,
		// so the status carries what the code cannot.
		return "INVALID_ARGUMENT", http.StatusRequestEntityTooLarge
```

- [ ] **Step 5: Let `RunAux` raise it**

Two edits in `internal/exec/surface.go`. First, `RunAux`'s build closure takes the config snapshot, so a route reads it once rather than twice — today each `HandleX` calls `e.store.Current()` for `maxBody` and `runOp` calls it again, and a reload between the two would serve one request under two configurations:

```go
	build func(cfg *config.Config) (SurfaceOp, error)
```

with `runOp` receiving the same `cfg` rather than re-reading it. Then replace the fixed error type in the parse-failure branch:

```go
	op, err := build(cfg)
	if err != nil {
		// A parser reporting an oversized body says so in the error it
		// returns, because only it knows the cap it was given. The message is
		// taken from the typed error rather than from err.Error(), which
		// prepends the type and would reach the client as
		// "payload_too_large: upload exceeds…".
		e := &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()}
		var ie *ir.Error
		if errors.As(err, &ie) && ie.Type != "" {
			e = ie
		}
		rec.ErrorCode = string(e.Type)
		_ = ew.WriteError(w, e)
		return
	}
```

Add `"errors"` to that file's imports.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/... ./internal/ir/ ./internal/exec/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS in every package.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/ir/ir.go internal/edge/openai/write.go internal/edge/openai/write_test.go \
        internal/edge/anthropic/write.go internal/edge/anthropic/write_test.go \
        internal/edge/gemini/write.go internal/edge/gemini/write_test.go \
        internal/exec/surface.go
git commit -m "feat(ir): distinguish an oversized payload from a bad one"
```

---

### Task 22: The multipart buffer and the in-form model rewrite

**Files:**
- Create: `internal/exec/multipart.go`
- Test: `internal/exec/multipart_test.go`

**Interfaces:**
- Consumes: `ir.ErrPayloadTooLarge` (Task 21).
- Produces: `exec.Form` with `ParseForm(r, maxBody) (*Form, error)`, `(*Form).Field(name) string`, and `(*Form).Render(model string) (body []byte, contentType string, err error)`. Task 23's op calls all three.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4
**Approach:** inline - skip 2: `mime/multipart` supplies both halves and the buffering decision is settled by spec §6.

Spec §6 is unusually explicit here, and the reason is worth restating: an earlier draft specified **streaming** the multipart body through, which is **incompatible with failover**. A streamed body cannot be replayed for a second attempt, so transcriptions would have been the one surface with no failover in a gateway whose entire purpose is failover.

It also makes the required rewrite impossible. The `model` field lives *inside* the multipart form and must be changed to the target's name — and clients are free to place it **after** the file part, so a streaming router would have to consume most of the body before it knew where to route anyway.

Buffering restores failover, makes the rewrite trivial, and lets an oversized upload be refused before any upstream connection exists.

Risk is 2 because this holds whole audio uploads in memory. The cap is `server.max_body_bytes` and it is enforced while reading rather than after, so a client cannot make the gateway allocate more than the operator allowed by lying about `Content-Length`.

- [ ] **Step 1: Write the failing test**

Create `internal/exec/multipart_test.go`:

```go
package exec

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// buildForm writes a multipart body with the parts in the order given, so a
// test can put the model field after the file exactly as a client may.
func buildForm(t *testing.T, parts [][2]string, file [2]string, fileFirst bool) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	writeFile := func() {
		fw, err := w.CreateFormFile("file", file[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(fw, file[1]); err != nil {
			t.Fatal(err)
		}
	}
	if fileFirst {
		writeFile()
	}
	for _, p := range parts {
		if err := w.WriteField(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	if !fileFirst {
		writeFile()
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), w.FormDataContentType()
}

func parseForm(t *testing.T, body, ct string, max int64) (*Form, error) {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", ct)
	return ParseForm(r, max)
}

func TestParseFormFindsAFieldPlacedAfterTheFile(t *testing.T) {
	// Clients do this. A streaming router would have had to consume the whole
	// upload before it knew where to route.
	body, ct := buildForm(t, [][2]string{{"model", "whisper-1"}, {"language", "en"}},
		[2]string{"a.mp3", "AUDIO"}, true)
	f, err := parseForm(t, body, ct, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Field("model"); got != "whisper-1" {
		t.Errorf("model = %q", got)
	}
	if got := f.Field("language"); got != "en" {
		t.Errorf("language = %q", got)
	}
	if got := f.Field("absent"); got != "" {
		t.Errorf("absent field = %q", got)
	}
}

func TestRenderRewritesTheModelInsideTheForm(t *testing.T) {
	body, ct := buildForm(t, [][2]string{{"model", "whisper-1"}},
		[2]string{"a.mp3", "AUDIO"}, false)
	f, err := parseForm(t, body, ct, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	out, outCT, err := f.Render("distil-whisper-large-v3")
	if err != nil {
		t.Fatal(err)
	}
	got := reparse(t, out, outCT)
	if got.Field("model") != "distil-whisper-large-v3" {
		t.Errorf("model = %q; the target's name must replace the client's", got.Field("model"))
	}
	if got.File("file") != "AUDIO" {
		t.Errorf("file = %q; the upload did not survive the rewrite", got.File("file"))
	}
}

func TestRenderAddsAModelFieldWhenTheClientSentNone(t *testing.T) {
	// The router resolved a target from an alias in the URL or from a default,
	// so the upstream still needs a model name.
	body, ct := buildForm(t, nil, [2]string{"a.mp3", "AUDIO"}, true)
	f, err := parseForm(t, body, ct, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	out, outCT, err := f.Render("whisper-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := reparse(t, out, outCT).Field("model"); got != "whisper-1" {
		t.Errorf("model = %q", got)
	}
}

func TestRenderIsReplayable(t *testing.T) {
	// Two attempts, two different targets, both from one buffered body. This
	// is the whole reason the body is buffered.
	body, ct := buildForm(t, [][2]string{{"model", "a"}}, [2]string{"a.mp3", "AUDIO"}, false)
	f, err := parseForm(t, body, ct, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	first, ct1, err := f.Render("first-model")
	if err != nil {
		t.Fatal(err)
	}
	second, ct2, err := f.Render("second-model")
	if err != nil {
		t.Fatal(err)
	}
	if reparse(t, first, ct1).Field("model") != "first-model" {
		t.Error("first render lost its model")
	}
	if reparse(t, second, ct2).Field("model") != "second-model" {
		t.Error("second render lost its model")
	}
	if reparse(t, second, ct2).File("file") != "AUDIO" {
		t.Error("the second render lost the upload")
	}
}

func TestRenderPreservesTheFilePartMetadata(t *testing.T) {
	// Whisper providers select a decoder from the filename extension. Dropping
	// it turns a working upload into an unsupported-format error.
	body, ct := buildForm(t, [][2]string{{"model", "m"}},
		[2]string{"recording.m4a", "AUDIO"}, false)
	f, err := parseForm(t, body, ct, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	out, outCT, err := f.Render("m")
	if err != nil {
		t.Fatal(err)
	}
	if name := reparse(t, out, outCT).FileName("file"); name != "recording.m4a" {
		t.Errorf("filename = %q", name)
	}
}

func TestParseFormRefusesAnOversizedUpload(t *testing.T) {
	body, ct := buildForm(t, [][2]string{{"model", "m"}},
		[2]string{"a.mp3", strings.Repeat("A", 4096)}, false)
	_, err := parseForm(t, body, ct, 512)
	if err == nil {
		t.Fatal("an oversized upload was accepted")
	}
	var ie *ir.Error
	if !errors.As(err, &ie) || ie.Type != ir.ErrPayloadTooLarge {
		t.Errorf("err = %v; it must be distinguishable so the route answers 413", err)
	}
}

func TestParseFormRejectsANonMultipartBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(`{"a":1}`))
	r.Header.Set("Content-Type", "application/json")
	if _, err := ParseForm(r, 1<<20); err == nil {
		t.Fatal("a JSON body parsed as multipart")
	}
}
```

`File` and `FileName` are exported methods on `Form`, written in Step 3, because Task 23's tests use them too. Add `reparse` to the test file:

```go
// reparse reads a rendered form back, which is how a test asserts on what the
// upstream would actually receive rather than on the writer's intentions.
func reparse(t *testing.T, body []byte, contentType string) *Form {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", bytes.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	f, err := ParseForm(r, 1<<20)
	if err != nil {
		t.Fatalf("rendered form did not parse: %v", err)
	}
	return f
}
```

The test file's import block therefore needs `bytes`, `errors`, `io`, `mime/multipart`, `net/http/httptest`, `strings`, `testing` and the `ir` package. It does **not** need `mime` — that import belongs to `multipart.go`, for the boundary check.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestParseForm|TestRender' -v
```

Expected: FAIL to build — `undefined: ParseForm`, `undefined: Form`.

- [ ] **Step 3: Write the buffer**

Create `internal/exec/multipart.go`:

```go
package exec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"

	"github.com/darkraise/darkrouter/internal/ir"
)

// Form is a multipart request body held whole.
//
// Buffered, not streamed, per spec §6. A streamed body cannot be replayed for a
// second attempt, which would make transcriptions the one surface with no
// failover — and the rewrite the router requires is impossible while streaming
// anyway, because the model field lives inside the form and clients are free to
// place it after the file part. Buffering restores failover, makes the rewrite
// trivial, and lets an oversized upload be refused before any upstream
// connection exists.
type Form struct {
	parts []formPart
}

type formPart struct {
	name     string
	filename string
	header   textproto.MIMEHeader
	value    []byte
}

// ParseForm reads the whole body, enforcing max while reading rather than
// after, so a client cannot make the gateway allocate more than the operator
// allowed by lying about Content-Length.
func ParseForm(r *http.Request, max int64) (*Form, error) {
	ct := r.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return nil, fmt.Errorf("invalid Content-Type: %w", err)
	}
	if mediaType != "multipart/form-data" {
		return nil, fmt.Errorf("expected multipart/form-data, got %s", mediaType)
	}
	boundary, ok := params["boundary"]
	if !ok {
		return nil, errors.New("multipart body has no boundary")
	}

	// One budget across every part: capping each part separately would let a
	// client send a thousand parts each just under the limit.
	remaining := max
	mr := multipart.NewReader(r.Body, boundary)
	f := &Form{}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read multipart body: %w", err)
		}
		// One byte past the budget is enough to know it was exceeded, and stops
		// the read there rather than draining the rest of the upload.
		buf, err := io.ReadAll(io.LimitReader(p, remaining+1))
		p.Close()
		if err != nil {
			return nil, fmt.Errorf("read multipart part %q: %w", p.FormName(), err)
		}
		if int64(len(buf)) > remaining {
			return nil, &ir.Error{
				Type:    ir.ErrPayloadTooLarge,
				Message: fmt.Sprintf("upload exceeds the configured maximum of %d bytes", max),
			}
		}
		remaining -= int64(len(buf))
		f.parts = append(f.parts, formPart{
			name: p.FormName(), filename: p.FileName(),
			// textproto.MIMEHeader has no Clone; only http.Header does. A
			// shallow copy is right here because the values are never mutated,
			// and the header must be copied because the reader reuses the part.
			header: maps.Clone(p.Header), value: buf,
		})
	}
	if len(f.parts) == 0 {
		return nil, errors.New("multipart body has no parts")
	}
	return f, nil
}

// Field returns a non-file field's value, or "" when it is absent.
func (f *Form) Field(name string) string {
	for _, p := range f.parts {
		if p.name == name && p.filename == "" {
			return string(p.value)
		}
	}
	return ""
}

// File returns a file part's contents, or "" when it is absent.
func (f *Form) File(name string) string {
	for _, p := range f.parts {
		if p.name == name && p.filename != "" {
			return string(p.value)
		}
	}
	return ""
}

// FileName returns a file part's declared filename. Whisper providers select a
// decoder from its extension, so dropping it turns a working upload into an
// unsupported-format error.
func (f *Form) FileName(name string) string {
	for _, p := range f.parts {
		if p.name == name && p.filename != "" {
			return p.filename
		}
	}
	return ""
}

// Render writes the form out with model rewritten to the target's name, adding
// the field when the client sent none. It is called once per attempt, which is
// what makes failover across two differently-named models possible.
func (f *Form) Render(model string) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	wrote := false
	for _, p := range f.parts {
		if p.name == "model" && p.filename == "" {
			if err := w.WriteField("model", model); err != nil {
				return nil, "", err
			}
			wrote = true
			continue
		}
		// CreatePart rather than CreateFormFile: the original part's header
		// carries the Content-Type a provider may use to pick a decoder, and
		// CreateFormFile would replace it with application/octet-stream.
		pw, err := w.CreatePart(p.header)
		if err != nil {
			return nil, "", err
		}
		if _, err := pw.Write(p.value); err != nil {
			return nil, "", err
		}
	}
	if !wrote {
		if err := w.WriteField("model", model); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'TestParseForm|TestRender' -race -count=1 -v
```

Expected: PASS, all seven.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing. Nothing calls `ParseForm` yet; Task 23 is its only user.

- [ ] **Step 6: Commit**

```bash
git add internal/exec/multipart.go internal/exec/multipart_test.go
git commit -m "feat(exec): buffer and re-render a multipart upload"
```

---

### Task 23: Transcriptions end to end

**Files:**
- Modify: `internal/adapter/adapter.go`, `internal/adapter/openaicompat/classify.go`, `internal/server/server.go`
- Create: `internal/adapter/openaicompat/audio.go`, `internal/exec/transcription.go`
- Test: `internal/adapter/openaicompat/audio_test.go`, `internal/exec/transcription_test.go`

**Interfaces:**
- Consumes: `Form` and `ParseForm` (Task 22), `RunAux` (Task 14).
- Produces: `adapter.Transcriber`, `exec.copyFlushing`, and `(*Executor).HandleTranscriptions`. Task 24 reuses `copyFlushing`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5
**Approach:** inline - skip 2: the buffering decision is settled by spec §6, Task 22 supplies the machinery, and the three response shapes are enumerated below.

**There is no transcription IR type, and that is deliberate.** The request is a multipart form that `Form` already holds and re-renders; the response is OpenAI's own shape going straight back to an OpenAI client. Parsing it into an IR and re-emitting would *lose* fields — `verbose_json` carries per-segment timings, log-probabilities and a language guess that no narrow type would model — so the body is **forwarded verbatim** and read only to extract what the request row needs.

**The response shape is chosen by `Content-Type`, never by the route.** Spec §6 is explicit, and it has to be: the same route returns JSON for `response_format: json`, `text/plain` for `text`, `srt` and `vtt`, and `text/event-stream` for `stream: true`. Three of those four format values are indistinguishable from the route and two of them arrive as fields buried in a multipart form. Reading the header the provider actually sent is the only thing that is right in every case.

- `application/json` — read bounded, extract `text`, `duration` and `usage` for the record, then write the bytes through unchanged.
- `text/event-stream` — copy through with a flush per chunk. Buffering an SSE transcript turns incremental output into a single blob at the end.
- anything else — copy through with a flush per chunk, as opaque bytes.

`adapter.Transcriber` takes rendered bytes rather than a `*Form`, because `Form` lives in `internal/exec` and an adapter importing it would be an import cycle. The split is right anyway: the op owns the rewrite, the adapter owns the URL and the auth.

The upload is already whole in memory by the time `Build` runs, so re-rendering per attempt costs one buffer copy and buys failover across two providers whose model names differ.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/openaicompat/audio_test.go`:

```go
package openaicompat

import (
	"context"
	"io"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestBuildTranscriptionPostsTheFormVerbatim(t *testing.T) {
	body := []byte("--b\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nwhisper-1\r\n--b--\r\n")
	hr, warns, err := New().BuildTranscription(context.Background(),
		&adapter.Target{BaseURL: "https://api.example.com/v1/", APIKey: "sk", Model: "whisper-1"},
		body, "multipart/form-data; boundary=b")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	if hr.URL.String() != "https://api.example.com/v1/audio/transcriptions" {
		t.Errorf("url = %s", hr.URL)
	}
	if got := hr.Header.Get("Content-Type"); got != "multipart/form-data; boundary=b" {
		t.Errorf("content-type = %q; the boundary must be the rendered form's", got)
	}
	if hr.Header.Get("Authorization") != "Bearer sk" {
		t.Errorf("auth = %q", hr.Header.Get("Authorization"))
	}
	sent, _ := io.ReadAll(hr.Body)
	if string(sent) != string(body) {
		t.Errorf("body was altered: %q", sent)
	}
	if hr.ContentLength != int64(len(body)) {
		t.Errorf("content-length = %d, want %d", hr.ContentLength, len(body))
	}
}

func TestBuildTranscriptionIsReplayable(t *testing.T) {
	// The transport retries by calling GetBody. Without it a retried upload is
	// sent empty and the provider reports an unreadable file.
	hr, _, err := New().BuildTranscription(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "m"},
		[]byte("AUDIO"), "multipart/form-data; boundary=b")
	if err != nil {
		t.Fatal(err)
	}
	if hr.GetBody == nil {
		t.Fatal("GetBody is nil")
	}
	again, err := hr.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(again)
	if string(got) != "AUDIO" {
		t.Errorf("replayed body = %q", got)
	}
}
```

Create `internal/exec/transcription_test.go`:

```go
package exec

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

// transcriptionRequest builds a real multipart upload with the model field
// after the file part, which is where several clients put it.
func transcriptionRequest(t *testing.T, model string) *http.Request {
	t.Helper()
	body, ct := buildForm(t, [][2]string{{"model", model}, {"response_format", "json"}},
		[2]string{"a.mp3", "AUDIO"}, true)
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", ct)
	return r
}

func TestTranscriptionsServeJSON(t *testing.T) {
	var sawModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("upstream could not parse the form: %v", err)
		}
		sawModel = r.FormValue("model")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello there","duration":2.5,
		  "usage":{"type":"tokens","input_tokens":7,"output_tokens":3}}`))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "whisper-1", ir.SurfaceSTT))
	w := httptest.NewRecorder()
	e.HandleTranscriptions(w, transcriptionRequest(t, "whisper-1"), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if sawModel != "whisper-1" {
		t.Errorf("upstream saw model %q", sawModel)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["text"] != "hello there" {
		t.Errorf("body = %s", w.Body.String())
	}
	if _, ok := body["duration"]; !ok {
		t.Error("duration was dropped; the body must be forwarded verbatim")
	}
	got := rec.only(t)
	if got.Surface != "stt" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
	if got.TokensIn != 7 || got.TokensOut != 3 {
		t.Errorf("tokens = %d/%d", got.TokensIn, got.TokensOut)
	}
}

func TestTranscriptionsForwardPlainTextByContentType(t *testing.T) {
	// The route cannot tell srt from json; the response header can.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "1\n00:00:00,000 --> 00:00:02,500\nhello there\n")
	}))
	defer upstream.Close()

	e, _ := executorForOp(t, upstream.URL, catalogWith("p", "whisper-1", ir.SurfaceSTT))
	w := httptest.NewRecorder()
	e.HandleTranscriptions(w, transcriptionRequest(t, "whisper-1"), openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("content-type = %q; the upstream's was not preserved", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "00:00:02,500") {
		t.Errorf("body = %q; a subtitle body was not forwarded intact", w.Body.String())
	}
}

func TestTranscriptionsForwardSSEByContentType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.delta\",\"delta\":\"hel\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.done\",\"text\":\"hello\"}\n\n")
	}))
	defer upstream.Close()

	e, _ := executorForOp(t, upstream.URL, catalogWith("p", "whisper-1", ir.SurfaceSTT))
	w := httptest.NewRecorder()
	e.HandleTranscriptions(w, transcriptionRequest(t, "whisper-1"), openaiedge.New())

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "transcript.text.delta") ||
		!strings.Contains(w.Body.String(), "transcript.text.done") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestATranscriptionSurvivesAFirstProviderFailure(t *testing.T) {
	// The done criterion: the buffered body is replayed against a second
	// provider whose model name differs, and the form carries the new name.
	var hits atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	var sawModel, sawFile string
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("replayed form did not parse: %v", err)
		}
		sawModel = r.FormValue("model")
		f, _, err := r.FormFile("file")
		if err == nil {
			b, _ := io.ReadAll(f)
			sawFile = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello"}`))
	}))
	defer good.Close()

	e, rec := executorForTwo(t, bad.URL, good.URL,
		catalogAlias("bad", "whisper-1", "good", "distil-whisper", ir.SurfaceSTT))
	w := httptest.NewRecorder()
	body, ct := buildForm(t, [][2]string{{"model", "embed"}}, [2]string{"a.mp3", "AUDIO"}, true)
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", ct)
	e.HandleTranscriptions(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("the failing provider was called %d times", hits.Load())
	}
	if sawModel != "distil-whisper" {
		t.Errorf("replayed model = %q; the in-form name was not rewritten for the second target", sawModel)
	}
	if sawFile != "AUDIO" {
		t.Errorf("replayed file = %q; the upload did not survive the replay", sawFile)
	}
	if got := rec.only(t); len(got.Attempts) != 2 {
		t.Errorf("attempts = %d", len(got.Attempts))
	}
}

func TestAnOversizedUploadIsRefusedBeforeAnyUpstreamCall(t *testing.T) {
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer upstream.Close()

	e, rec := executorForCapped(t, upstream.URL, catalogWith("p", "whisper-1", ir.SurfaceSTT), 512)
	w := httptest.NewRecorder()
	body, ct := buildForm(t, [][2]string{{"model", "whisper-1"}},
		[2]string{"a.mp3", strings.Repeat("A", 4096)}, false)
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", ct)
	e.HandleTranscriptions(w, r, openaiedge.New())

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", w.Code, w.Body.String())
	}
	if hits.Load() != 0 {
		t.Errorf("an oversized upload reached an upstream %d times", hits.Load())
	}
	if got := rec.only(t); got.ErrorCode != string(ir.ErrPayloadTooLarge) {
		t.Errorf("error code = %q", got.ErrorCode)
	}
}

func TestATranscriptionWithNoSTTProviderIsRefused(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleTranscriptions(w, transcriptionRequest(t, "chat-only"), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(rec.only(t).Attempts) != 0 {
		t.Error("a surface no provider offers attempted an upstream call")
	}
}
```

Add `executorForCapped(t, url string, cat *catalog.Store, maxBody int64)` beside the other executor helpers — `executorForOp` with `server.max_body_bytes` set to the given value.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/openaicompat/ ./internal/exec/ -run 'Transcription|OversizedUpload' -v
```

Expected: FAIL to build — `undefined: BuildTranscription`, `e.HandleTranscriptions undefined`.

- [ ] **Step 3: Add the adapter interface and implementation**

In `internal/adapter/adapter.go`, beside `ImageGenerator`:

```go
// Transcriber is implemented by an adapter serving the stt surface.
//
// It takes rendered bytes rather than a parsed form because the form type lives
// in the executor and importing it here would be a cycle. The split is right
// regardless: the executor owns the in-form model rewrite, the adapter owns the
// URL and the credential. There is no Parse counterpart — a transcription
// response is forwarded to the client verbatim, since parsing it into an IR
// would drop the per-segment timings and log-probabilities verbose_json carries.
type Transcriber interface {
	BuildTranscription(ctx context.Context, t *Target, body []byte, contentType string) (*http.Request, []ir.Warning, error)
}
```

Create `internal/adapter/openaicompat/audio.go`:

```go
package openaicompat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func (a *Adapter) BuildTranscription(ctx context.Context, t *adapter.Target,
	body []byte, contentType string) (*http.Request, []ir.Warning, error) {

	url := strings.TrimRight(t.BaseURL, "/") + "/audio/transcriptions"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build transcription request: %w", err)
	}
	// The rendered form's own boundary, not a fresh one: the body and the
	// header have to agree or the provider sees no parts at all.
	hr.Header.Set("Content-Type", contentType)
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	hr.ContentLength = int64(len(body))
	// Set here rather than left to makeReplayable so a transport-level retry
	// resends the upload instead of an empty body.
	hr.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return hr, nil, nil
}

var _ adapter.Transcriber = (*Adapter)(nil)
```

- [ ] **Step 4: Write the op and the route**

Create `internal/exec/transcription.go`:

```go
package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

// maxTranscriptBytes bounds a JSON transcription response. verbose_json for a
// long recording carries a segment object per phrase, so the cap is generous;
// a cap that rejects a legitimate transcript is worse than no cap.
const maxTranscriptBytes = 64 << 20

type transcriptionOp struct {
	d    edge.Dialect
	form *Form
	// model is the name the client put in the form, kept for routing. The name
	// sent upstream is the candidate's, written into the form by Render.
	model string
}

func (o *transcriptionOp) Dialect() string { return o.d.Name() }

func (o *transcriptionOp) Query() router.Query {
	return router.Query{Model: o.model, Surface: ir.SurfaceSTT}
}

func (o *transcriptionOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	tr, ok := ad.(adapter.Transcriber)
	if !ok {
		return nil, nil, fmt.Errorf("adapter %s does not serve transcriptions", ad.Kind())
	}
	// Re-rendered per attempt, not once per request: the model field lives
	// inside the form and the second candidate's name is usually different.
	body, ct, err := o.form.Render(tgt.Model)
	if err != nil {
		return nil, nil, err
	}
	return tr.BuildTranscription(ctx, tgt, body, ct)
}

// Respond dispatches on the response Content-Type rather than on the route.
// Spec §6: one route returns JSON, plain text and SSE depending on a
// response_format field buried in the multipart form, and the header the
// provider actually sent is the only thing that is right in every case.
func (o *transcriptionOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	ac.Rec.FinalProviderID = ac.Cand.ProviderID
	ac.Rec.FinalModel = ac.Cand.Model
	ac.Rec.Warnings = warningStrings(ac.Warns)

	if strings.HasPrefix(ct, "application/json") {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxTranscriptBytes+1))
		if err != nil {
			return failedParse(ac, resp, fmt.Errorf("read transcription response: %w", err))
		}
		if int64(len(raw)) > maxTranscriptBytes {
			// Forwarding the truncated prefix would send the client invalid
			// JSON under a 200. An oversized body is a provider fault.
			return failedParse(ac, resp,
				fmt.Errorf("transcription response exceeds %d bytes", int64(maxTranscriptBytes)))
		}
		// Read for the record only. The bytes go out unchanged, because
		// verbose_json carries per-segment timings and log-probabilities that
		// re-emitting from a narrow IR would drop.
		applyTranscriptUsage(ac, raw)
		ttft := time.Since(ac.Rec.TS).Milliseconds()
		ac.Rec.TTFTMs = &ttft
		ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
		cw.Header().Set("Content-Type", ct)
		_, _ = cw.Write(raw)
		return adapter.OutcomeSuccess, nil
	}

	// Text and SSE alike are opaque and are forwarded with a flush per chunk.
	// Buffering an SSE transcript would turn incremental output into one blob
	// at the end, which is the whole thing the client asked to avoid.
	ttft := time.Since(ac.Rec.TS).Milliseconds()
	ac.Rec.TTFTMs = &ttft
	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	if ct != "" {
		cw.Header().Set("Content-Type", ct)
	}
	if _, err := copyFlushing(cw, resp.Body); err != nil && !cw.Committed() {
		return failedParse(ac, resp, err)
	}
	// Once bytes have gone out the chain ends whatever happened next. The loop
	// enforces this by consulting the writer, and the byte count is what the
	// trace has instead of an in-stream error the format cannot carry.
	return adapter.OutcomeSuccess, nil
}

func (o *transcriptionOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*transcriptionOp)(nil)

// applyTranscriptUsage reads the token counts a transcription model may report.
// whisper-1 reports none; the gpt-4o transcription models report a usage object
// whose type is "tokens". A "duration" type carries seconds rather than tokens
// and is deliberately not recorded as tokens.
func applyTranscriptUsage(ac *AttemptCtx, raw []byte) {
	var env struct {
		Usage *struct {
			Type         string `json:"type"`
			InputTokens  int    `json:"input_tokens"`
			OutputTokens int    `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Usage == nil {
		return
	}
	if env.Usage.Type != "" && env.Usage.Type != "tokens" {
		return
	}
	applyUsage(ac.Rec, &ir.Usage{
		InputTokens: env.Usage.InputTokens, OutputTokens: env.Usage.OutputTokens,
	})
}

// copyFlushing copies src to dst, flushing after every chunk.
//
// io.Copy alone would let the ResponseWriter buffer, which turns an SSE
// transcript or a streamed audio body into a single delivery at the end. It is
// shared with the speech surface, which has the same requirement for the same
// reason.
func copyFlushing(dst *CommitWriter, src io.Reader) (int64, error) {
	buf := make([]byte, 32<<10)
	var total int64
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
			dst.Flush()
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return total, nil
			}
			return total, rerr
		}
	}
}

// HandleTranscriptions serves POST /v1/audio/transcriptions.
func (e *Executor) HandleTranscriptions(w http.ResponseWriter, r *http.Request, d edge.Dialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceSTT, d, func(cfg *config.Config) (SurfaceOp, error) {
		form, err := ParseForm(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &transcriptionOp{d: d, form: form, model: form.Field("model")}, nil
	})
}
```

In `internal/server/server.go`, beside the images route:

```go
	mux.HandleFunc("POST /v1/audio/transcriptions", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleTranscriptions(w, r, oa)
	}))
```

- [ ] **Step 5: Confirm the surface is already declared**

Task 7 gives `openaicompat` all seven surfaces from the §4 matrix, so `ir.SurfaceSTT` is already in its `Surfaces()` set and **no edit is expected here**. Read `internal/adapter/openaicompat/classify.go` and confirm it; if the value is missing, Task 7 was implemented against the wrong matrix row and that is the bug to fix, not this line.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/openaicompat/ ./internal/exec/ ./internal/server/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in all three packages.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/adapter/adapter.go internal/adapter/openaicompat/audio.go \
        internal/adapter/openaicompat/audio_test.go internal/adapter/openaicompat/classify.go \
        internal/exec/transcription.go internal/exec/transcription_test.go internal/server/server.go
git commit -m "feat(exec): serve the transcription surface"
```

---

### Task 24: Speech end to end, streamed and never captured

**Files:**
- Modify: `internal/ir/aux.go`, `internal/adapter/adapter.go`, `internal/edge/edge.go`, `internal/edge/openai/aux.go`, `internal/server/server.go`
- Create: `internal/adapter/openaicompat/speech.go`, `internal/exec/speech.go`
- Test: `internal/adapter/openaicompat/speech_test.go`, `internal/exec/speech_test.go`

**Interfaces:**
- Consumes: `copyFlushing` (Task 23), `RunAux` (Task 14).
- Produces: `ir.SpeechRequest`, `adapter.Speaker`, `edge.SpeechDialect`, and `(*Executor).HandleSpeech`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5
**Approach:** inline - skip 2: the request shape is OpenAI's and the response handling is Task 23's non-JSON branch with nothing read at all.

Spec §6: the response "streams through without parsing, bypasses body capture entirely — audio in SQLite is never right — and the log records content type, byte count, and duration."

**There is no speech response type.** Nothing about an audio body is representable in an IR and nothing needs to be: the bytes go from the upstream to the client and the only facts worth keeping are the content type and the count. `stream_format: "sse"` changes nothing here either — the op already forwards whatever arrives with a flush per chunk, so an SSE speech response is handled by the same three lines as an MP3.

**What "not captured" means today, stated plainly.** `capture.bodies` is a config field with a retention sweep and **no writer anywhere in the tree** — nothing has ever inserted into `request_bodies`. So "a speech response is not captured" is true by construction rather than by enforcement, and a test asserting an empty table would prove nothing about this surface. The property that *is* enforceable and is what the criterion is really protecting is that the body is **never held whole**: the test below makes the upstream refuse to send its second chunk until the first has reached the client, so a buffering implementation deadlocks instead of passing. The capture gap is recorded as a carried-forward item in Task 31.

Risk is 2 because of spec §7. Once the first audio byte is out there is no re-route, and unlike chat there is no in-stream error vocabulary to tell the client the rest is missing. A provider that returns a fast 200 and then truncates delivers truncated audio, and the byte count on the request row is the only place that shows up.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/openaicompat/speech_test.go`:

```go
package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildSpeechRendersTheOpenAIShape(t *testing.T) {
	hr, warns, err := New().BuildSpeech(context.Background(),
		&adapter.Target{BaseURL: "https://api.example.com/v1/", APIKey: "sk", Model: "tts-1"},
		&ir.SpeechRequest{
			Input: "hello", Voice: "alloy", ResponseFormat: "mp3",
			Speed: 1.25, Instructions: "cheerful", StreamFormat: "sse",
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	if hr.URL.String() != "https://api.example.com/v1/audio/speech" {
		t.Errorf("url = %s", hr.URL)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "tts-1" || body["input"] != "hello" || body["voice"] != "alloy" {
		t.Errorf("body = %v", body)
	}
	if body["response_format"] != "mp3" || body["speed"].(float64) != 1.25 {
		t.Errorf("body = %v", body)
	}
	if body["instructions"] != "cheerful" || body["stream_format"] != "sse" {
		t.Errorf("body = %v", body)
	}
}

func TestBuildSpeechOmitsUnsetOptionals(t *testing.T) {
	// speed 0 is not "default", it is a 400.
	hr, _, err := New().BuildSpeech(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1", Model: "tts-1"},
		&ir.SpeechRequest{Input: "hi", Voice: "alloy"})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	raw, _ := io.ReadAll(hr.Body)
	_ = json.Unmarshal(raw, &body)
	for _, k := range []string{"speed", "response_format", "instructions", "stream_format"} {
		if _, present := body[k]; present {
			t.Errorf("an unset %q was sent as %v", k, body[k])
		}
	}
}
```

Create `internal/exec/speech_test.go`:

```go
package exec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

// watchWriter reports each Write as it happens, so a test can prove bytes
// reached the client before the upstream finished sending.
type watchWriter struct {
	http.ResponseWriter
	writes chan []byte
}

func (w *watchWriter) Write(b []byte) (int, error) {
	cp := append([]byte(nil), b...)
	select {
	case w.writes <- cp:
	default:
	}
	return w.ResponseWriter.Write(b)
}

func (w *watchWriter) Flush() {}

func speechRequest() *http.Request {
	return httptest.NewRequest("POST", "/v1/audio/speech",
		strings.NewReader(`{"model":"tts-1","input":"hello","voice":"alloy"}`))
}

func TestSpeechIsNeverHeldWhole(t *testing.T) {
	// The upstream refuses to send its second chunk until the first has reached
	// the client. An implementation that buffers the audio deadlocks here
	// instead of passing, which is the enforceable form of "never captured".
	gotFirst := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("FIRST"))
		w.(http.Flusher).Flush()
		select {
		case <-gotFirst:
		case <-time.After(5 * time.Second):
			t.Error("the first chunk never reached the client; the body was buffered")
		}
		_, _ = w.Write([]byte("SECOND"))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "tts-1", ir.SurfaceTTS))
	inner := httptest.NewRecorder()
	ww := &watchWriter{ResponseWriter: inner, writes: make(chan []byte, 8)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.HandleSpeech(ww, speechRequest(), openaiedge.New())
	}()

	select {
	case b := <-ww.writes:
		if string(b) != "FIRST" {
			t.Errorf("first chunk = %q", b)
		}
		close(gotFirst)
	case <-time.After(5 * time.Second):
		t.Fatal("no bytes reached the client before the upstream finished")
	}
	<-done

	if got := inner.Body.String(); got != "FIRSTSECOND" {
		t.Errorf("body = %q", got)
	}
	if ct := inner.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("content-type = %q; the upstream's was not preserved", ct)
	}
	got := rec.only(t)
	if got.Surface != "tts" || got.Status != "success" {
		t.Errorf("record = surface %q status %q", got.Surface, got.Status)
	}
}

func TestSpeechForwardsAnSSEBodyUnchanged(t *testing.T) {
	// stream_format: "sse" changes nothing in the op: whatever arrives is
	// forwarded with a flush per chunk.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"speech.audio.delta\",\"audio\":\"AA==\"}\n\n"))
	}))
	defer upstream.Close()

	e, _ := executorForOp(t, upstream.URL, catalogWith("p", "tts-1", ir.SurfaceTTS))
	w := httptest.NewRecorder()
	e.HandleSpeech(w, httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(
		`{"model":"tts-1","input":"hi","voice":"alloy","stream_format":"sse"}`)), openaiedge.New())

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "speech.audio.delta") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestSpeechFailsOverBeforeTheFirstByte(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("AUDIO"))
	}))
	defer good.Close()

	e, rec := executorForTwo(t, bad.URL, good.URL, catalogPair("bad", "good", "tts-1", ir.SurfaceTTS))
	w := httptest.NewRecorder()
	e.HandleSpeech(w, speechRequest(), openaiedge.New())

	if w.Code != http.StatusOK || w.Body.String() != "AUDIO" {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
	if got := rec.only(t); len(got.Attempts) != 2 || got.FinalProviderID != "good" {
		t.Errorf("attempts = %d, final = %q", len(got.Attempts), got.FinalProviderID)
	}
}

func TestASpeechRequestWithNoTTSProviderIsRefused(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "chat-only", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.HandleSpeech(w, httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(
		`{"model":"chat-only","input":"hi","voice":"alloy"}`)), openaiedge.New())

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(rec.only(t).Attempts) != 0 {
		t.Error("a surface no provider offers attempted an upstream call")
	}
}

func TestAMalformedSpeechBodyIsRefused(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	e, _ := executorForOp(t, upstream.URL, catalogWith("p", "tts-1", ir.SurfaceTTS))
	for _, body := range []string{`{"model":"tts-1","voice":"alloy"}`, `{"model":"tts-1","input":"hi"}`} {
		w := httptest.NewRecorder()
		e.HandleSpeech(w, httptest.NewRequest("POST", "/v1/audio/speech",
			strings.NewReader(body)), openaiedge.New())
		if w.Code != http.StatusBadRequest {
			t.Errorf("HandleSpeech(%s) status = %d, want 400", body, w.Code)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/openaicompat/ ./internal/exec/ -run Speech -v
```

Expected: FAIL to build — `undefined: BuildSpeech`, `e.HandleSpeech undefined`.

- [ ] **Step 3: Add the IR type, the adapter interface and the edge**

Append to `internal/ir/aux.go`:

```go
// SpeechRequest is one text-to-speech call.
//
// There is no SpeechResponse. An audio body is not representable in an IR and
// nothing needs it to be: the bytes go from the upstream to the client
// untouched, and the only facts worth keeping are the content type and the byte
// count, which the executor records.
type SpeechRequest struct {
	Model          string
	Input          string
	Voice          string
	ResponseFormat string
	// Speed is 0 when unset. Zero is not a legal speed, so it needs no separate
	// presence flag.
	Speed        float64
	Instructions string
	// StreamFormat is "sse" when the client asked for events rather than a
	// binary body. It changes nothing in the executor — whatever arrives is
	// forwarded with a flush per chunk — and is carried only so the upstream
	// request matches the client's.
	StreamFormat string
}
```

In `internal/adapter/adapter.go`, beside `Transcriber`:

```go
// Speaker is implemented by an adapter serving the tts surface. Like
// Transcriber it has no Parse counterpart: the response is audio, forwarded
// without being read.
type Speaker interface {
	BuildSpeech(ctx context.Context, t *Target, req *ir.SpeechRequest) (*http.Request, []ir.Warning, error)
}
```

In `internal/edge/edge.go`, beside `ImageDialect`:

```go
// SpeechDialect is the inbound wire form of the tts surface. It has no writer:
// the response is forwarded byte for byte.
type SpeechDialect interface {
	Name() string
	ParseSpeech(r *http.Request, maxBody int64) (*ir.SpeechRequest, error)
	WriteError(w http.ResponseWriter, e *ir.Error) error
}
```

Append to `internal/edge/openai/aux.go`:

```go
type wireSpeechRequest struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          string   `json:"voice"`
	ResponseFormat string   `json:"response_format"`
	Speed          *float64 `json:"speed"`
	Instructions   string   `json:"instructions"`
	StreamFormat   string   `json:"stream_format"`
}

func ParseSpeech(r *http.Request, maxBody int64) (*ir.SpeechRequest, error) {
	body, err := readCappedBody(r, maxBody)
	if err != nil {
		return nil, err
	}
	var w wireSpeechRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	if w.Input == "" {
		return nil, errors.New("input is required")
	}
	if w.Voice == "" {
		return nil, errors.New("voice is required")
	}
	req := &ir.SpeechRequest{
		Model: w.Model, Input: w.Input, Voice: w.Voice,
		ResponseFormat: w.ResponseFormat, Instructions: w.Instructions,
		StreamFormat: w.StreamFormat,
	}
	if w.Speed != nil {
		req.Speed = *w.Speed
	}
	return req, nil
}

func (d *Dialect) ParseSpeech(r *http.Request, maxBody int64) (*ir.SpeechRequest, error) {
	return ParseSpeech(r, maxBody)
}

var _ edge.SpeechDialect = (*Dialect)(nil)
```

- [ ] **Step 4: Implement the adapter**

Create `internal/adapter/openaicompat/speech.go`:

```go
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func (a *Adapter) BuildSpeech(ctx context.Context, t *adapter.Target,
	req *ir.SpeechRequest) (*http.Request, []ir.Warning, error) {

	body := map[string]any{"model": t.Model, "input": req.Input, "voice": req.Voice}
	// Omitted rather than sent empty: speed 0 is a 400 rather than a default,
	// and an empty response_format or stream_format is rejected outright.
	if req.Speed > 0 {
		body["speed"] = req.Speed
	}
	for k, v := range map[string]string{
		"response_format": req.ResponseFormat,
		"instructions":    req.Instructions,
		"stream_format":   req.StreamFormat,
	} {
		if v != "" {
			body[k] = v
		}
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/audio/speech"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build speech request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil, nil
}

var _ adapter.Speaker = (*Adapter)(nil)
```

- [ ] **Step 5: Write the op and the route**

Create `internal/exec/speech.go`:

```go
package exec

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

type speechOp struct {
	d   edge.SpeechDialect
	req *ir.SpeechRequest
}

func (o *speechOp) Dialect() string { return o.d.Name() }

func (o *speechOp) Query() router.Query {
	return router.Query{Model: o.req.Model, Surface: ir.SurfaceTTS}
}

func (o *speechOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	sp, ok := ad.(adapter.Speaker)
	if !ok {
		return nil, nil, fmt.Errorf("adapter %s does not serve speech", ad.Kind())
	}
	return sp.BuildSpeech(ctx, tgt, o.req)
}

// Respond forwards the body without reading it.
//
// Spec §6: audio in SQLite is never right, so the bytes are never held. Spec §7:
// once the first byte is out there is no re-route, and unlike chat there is no
// in-stream error vocabulary to tell the client the rest is missing — so a
// truncated body reaches the client as truncated audio and the byte count on
// the request row is the only place that shows up.
func (o *speechOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	defer resp.Body.Close()

	ac.Rec.FinalProviderID = ac.Cand.ProviderID
	ac.Rec.FinalModel = ac.Cand.Model
	ac.Rec.Warnings = warningStrings(ac.Warns)
	ttft := time.Since(ac.Rec.TS).Milliseconds()
	ac.Rec.TTFTMs = &ttft

	ac.Exec.writeDiagnostics(cw, ac.Rec.ID, ac.Cand, ac.Seq)
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		cw.Header().Set("Content-Type", ct)
	}
	if _, err := copyFlushing(cw, resp.Body); err != nil && !cw.Committed() {
		// Nothing reached the client, so the chain may still continue.
		return adapter.OutcomeRetryableProvider, errorFor(adapter.OutcomeRetryableProvider, err)
	}
	return adapter.OutcomeSuccess, nil
}

func (o *speechOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return o.d.WriteError(w, e)
}

var _ SurfaceOp = (*speechOp)(nil)

// HandleSpeech serves POST /v1/audio/speech.
func (e *Executor) HandleSpeech(w http.ResponseWriter, r *http.Request, d edge.SpeechDialect) {
	e.RunAux(w, r, d.Name(), ir.SurfaceTTS, d, func(cfg *config.Config) (SurfaceOp, error) {
		req, err := d.ParseSpeech(r, cfg.Server.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		return &speechOp{d: d, req: req}, nil
	})
}
```

In `internal/server/server.go`, beside the transcriptions route:

```go
	mux.HandleFunc("POST /v1/audio/speech", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleSpeech(w, r, oa)
	}))
```

- [ ] **Step 6: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/openaicompat/ ./internal/exec/ ./internal/server/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in all three packages. Run `internal/exec` at `-count=5` as well — `TestSpeechIsNeverHeldWhole` synchronizes two goroutines and a flaky pass would be worse than a failure.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
go test ./internal/exec/ -race -count=5
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing, no flakes across five runs.

- [ ] **Step 8: Commit**

```bash
git add internal/ir/aux.go internal/adapter/adapter.go internal/adapter/openaicompat/speech.go \
        internal/adapter/openaicompat/speech_test.go internal/edge/edge.go \
        internal/edge/openai/aux.go internal/exec/speech.go internal/exec/speech_test.go \
        internal/server/server.go
git commit -m "feat(exec): serve the speech surface"
```

---

### Task 25: The Responses request, and what it refuses

**Files:**
- Create: `internal/edge/openai/responses.go`
- Test: `internal/edge/openai/responses_test.go`

**Interfaces:**
- Consumes: `ir.Request` and its content-block model.
- Produces: `openai.ParseResponses(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, *responsesEcho, error)`. Tasks 26 to 29 build the writers and the dialect around it.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: the item vocabulary is enumerated below and `parse.go` beside it is the pattern for building `ir.Request` from a wire body.

`/v1/responses` is chat-shaped, so it maps onto the chat IR rather than getting its own type. What makes it its own task is not the mapping but **what it refuses**.

**Stateful requests are rejected, not degraded.** With a server-stored conversation the body carries only the newest turn. Degrading that to a chat completion would return a fluent, confident, amnesic answer that looks entirely successful, and no client can detect it. The same applies to a declared built-in tool: silently answering without the web search the client asked for is the same class of lie. An error the client can handle beats a wrong answer it cannot.

So three things are refused:

- `previous_response_id` — Darkrouter mints no resolvable ids and cannot resolve anyone else's.
- `conversation` — same reason, one level up.
- `background: true` — it asks for a queued response the client polls for by id. Darkrouter has no queue and no resolvable ids, and answering with a finished response would leave the client polling an id that will never exist.
- any tool whose `type` is not `function` — `web_search`, `file_search`, `code_interpreter`, `image_generation`, `computer_use_preview`, `mcp`, `local_shell`. None is something Darkrouter can execute or forward.

**`store` is not refused.** Its default is `true`, so refusing it would fail every request written by an SDK's defaults. It is accepted, ignored, and answered with `store: false` in the response body — which is precisely how a Responses client is told the id is not resumable. Task 26 emits that.

Reasoning items in the input are **dropped rather than replayed**. An encrypted reasoning item is meaningful only to the provider that minted it, and Darkrouter may be sending this turn somewhere else entirely. The drop is warned about, so the trace shows it.

- [ ] **Step 1: Write the failing test**

Create `internal/edge/openai/responses_test.go`:

```go
package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func parseResponses(t *testing.T, body string) (*ir.Request, error) {
	t.Helper()
	req, _, _, err := ParseResponses(httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(body)), 1<<20)
	return req, err
}

func parseResponsesEcho(t *testing.T, body string) *responsesEcho {
	t.Helper()
	_, _, echo, err := ParseResponses(httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(body)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return echo
}

func TestParseResponsesTurnsABareInputIntoAUserTurn(t *testing.T) {
	req, err := parseResponses(t, `{"model":"gpt-4o","input":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-4o" || len(req.Messages) != 1 {
		t.Fatalf("request = %+v", req)
	}
	if req.Messages[0].Role != ir.RoleUser || req.Messages[0].Content[0].Text != "hello" {
		t.Errorf("message = %+v", req.Messages[0])
	}
}

func TestParseResponsesMapsInstructionsToSystem(t *testing.T) {
	req, err := parseResponses(t, `{"model":"m","instructions":"be terse","input":"hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.System) != 1 || req.System[0].Text != "be terse" {
		t.Errorf("system = %+v", req.System)
	}
}

func TestParseResponsesReadsMessageItems(t *testing.T) {
	req, err := parseResponses(t, `{"model":"m","input":[
	  {"role":"user","content":"first"},
	  {"type":"message","role":"assistant","content":[{"type":"output_text","text":"second"}]},
	  {"type":"message","role":"user","content":[
	     {"type":"input_text","text":"third"},
	     {"type":"input_image","image_url":"data:image/png;base64,AAA"}]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d: %+v", len(req.Messages), req.Messages)
	}
	if req.Messages[1].Role != ir.RoleAssistant || req.Messages[1].Content[0].Text != "second" {
		t.Errorf("assistant turn = %+v", req.Messages[1])
	}
	last := req.Messages[2].Content
	if len(last) != 2 || last[0].Text != "third" || last[1].Type != ir.BlockImage {
		t.Errorf("multimodal turn = %+v", last)
	}
	if last[1].Media == nil || last[1].Media.MIME != "image/png" || last[1].Media.Data != "AAA" {
		t.Errorf("image = %+v", last[1].Media)
	}
}

func TestParseResponsesReadsAToolCallRoundTrip(t *testing.T) {
	// This is what an agent loop replays on its second turn. Losing either
	// half strands the model waiting for a result it already produced.
	req, err := parseResponses(t, `{"model":"m","input":[
	  {"role":"user","content":"weather?"},
	  {"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Oslo\"}"},
	  {"type":"function_call_output","call_id":"call_1","output":"12C"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d: %+v", len(req.Messages), req.Messages)
	}
	call := req.Messages[1]
	if call.Role != ir.RoleAssistant || call.Content[0].Type != ir.BlockToolUse {
		t.Fatalf("call turn = %+v", call)
	}
	if call.Content[0].ToolUse.ID != "call_1" || call.Content[0].ToolUse.Name != "get_weather" {
		t.Errorf("tool use = %+v", call.Content[0].ToolUse)
	}
	out := req.Messages[2]
	if out.Content[0].Type != ir.BlockToolResult ||
		out.Content[0].ToolResult.ToolUseID != "call_1" ||
		out.Content[0].ToolResult.Text() != "12C" {
		t.Errorf("output turn = %+v", out.Content[0])
	}
}

func TestParseResponsesReadsFunctionTools(t *testing.T) {
	req, err := parseResponses(t, `{"model":"m","input":"hi","tools":[
	  {"type":"function","name":"f","description":"d","parameters":{"type":"object"}}],
	  "tool_choice":"required","parallel_tool_calls":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "f" || req.Tools[0].Description != "d" {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "any" {
		t.Errorf("tool choice = %+v", req.ToolChoice)
	}
	if req.ParallelToolCalls == nil || *req.ParallelToolCalls {
		t.Errorf("parallel_tool_calls = %v", req.ParallelToolCalls)
	}
}

func TestParseResponsesReadsSamplingAndFormat(t *testing.T) {
	req, err := parseResponses(t, `{"model":"m","input":"hi","max_output_tokens":128,
	  "temperature":0.2,"top_p":0.9,"stream":true,"reasoning":{"effort":"high"},
	  "text":{"format":{"type":"json_schema","name":"s","schema":{"type":"object"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 128 {
		t.Errorf("max tokens = %v", req.MaxTokens)
	}
	if !req.Stream || req.Temperature == nil || *req.Temperature != 0.2 {
		t.Errorf("request = %+v", req)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "high" {
		t.Errorf("reasoning = %+v", req.Reasoning)
	}
	if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_schema" {
		t.Fatalf("response format = %+v", req.ResponseFormat)
	}
	// The schema, not only its type. Responses flattens text.format while chat
	// nests it under json_schema, so decoding with chat's struct leaves this
	// nil and the request reaches the provider with no schema at all.
	if !strings.Contains(string(req.ResponseFormat.Schema), `"object"`) {
		t.Errorf("schema = %q; the flattened text.format was not read",
			req.ResponseFormat.Schema)
	}
}

func TestParseResponsesCarriesAReplayedRefusal(t *testing.T) {
	// An assistant turn that refused is replayed on the next turn. Rejecting
	// it would 400 a legitimate agent loop.
	req, err := parseResponses(t, `{"model":"m","input":[
	  {"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot"}]},
	  {"role":"user","content":"why?"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 2 || req.Messages[0].Content[0].Text != "I cannot" {
		t.Errorf("messages = %+v", req.Messages)
	}
}

func TestParseResponsesWarnsOnStrictTools(t *testing.T) {
	req, err := parseResponses(t, `{"model":"m","input":"hi","tools":[
	  {"type":"function","name":"f","parameters":{"type":"object"},"strict":true}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Warnings) != 1 || !strings.Contains(req.Warnings[0].Reason, "strict") {
		t.Errorf("warnings = %+v; a guarantee the client asked for is not forwarded", req.Warnings)
	}
}

func TestParseResponsesRejectsAStatefulRequest(t *testing.T) {
	// Degrading either of these returns a fluent, confident, amnesic answer
	// that looks entirely successful and no client can detect.
	for _, body := range []string{
		`{"model":"m","input":"hi","previous_response_id":"resp_abc"}`,
		`{"model":"m","input":"hi","conversation":"conv_abc"}`,
		`{"model":"m","input":"hi","conversation":{"id":"conv_abc"}}`,
		`{"model":"m","input":"hi","background":true}`,
	} {
		_, err := parseResponses(t, body)
		if err == nil {
			t.Errorf("ParseResponses(%s) served a stateful request", body)
			continue
		}
		if !strings.Contains(err.Error(), "stateless") {
			t.Errorf("err = %v; it must tell the client what will work", err)
		}
	}
}

func TestParseResponsesRejectsBuiltInTools(t *testing.T) {
	for _, kind := range []string{"web_search", "web_search_preview", "file_search",
		"code_interpreter", "image_generation", "computer_use_preview", "mcp", "local_shell"} {
		_, err := parseResponses(t, `{"model":"m","input":"hi","tools":[{"type":"`+kind+`"}]}`)
		if err == nil {
			t.Errorf("a %s tool was accepted; answering without it is the same lie as answering "+
				"without the conversation", kind)
			continue
		}
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("err = %v; it must name the tool that cannot be served", err)
		}
	}
}

func TestParseResponsesAcceptsStore(t *testing.T) {
	// store defaults to true, so refusing it would fail every request an SDK
	// writes with its defaults. The response body is what says the id is not
	// resumable.
	if _, err := parseResponses(t, `{"model":"m","input":"hi","store":true}`); err != nil {
		t.Fatalf("store:true was refused: %v", err)
	}
}

func TestParseResponsesDropsReasoningItemsWithAWarning(t *testing.T) {
	// An encrypted reasoning item means something only to the provider that
	// minted it, and this turn may be going somewhere else entirely.
	req, err := parseResponses(t, `{"model":"m","input":[
	  {"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"xxx"},
	  {"role":"user","content":"hi"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 {
		t.Errorf("messages = %+v; the reasoning item was replayed", req.Messages)
	}
	if len(req.Warnings) != 1 || !strings.Contains(req.Warnings[0].Reason, "reasoning") {
		t.Errorf("warnings = %+v", req.Warnings)
	}
}

func TestParseResponsesReportsTheSurface(t *testing.T) {
	// The passthrough carries the surface so the executor's record says llm
	// rather than guessing from the route.
	_, pt, _, err := ParseResponses(httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"m","input":"hi"}`)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt == nil || pt.Surface != ir.SurfaceLLM || pt.ModelField != "model" {
		t.Errorf("passthrough = %+v", pt)
	}
}
```

`req.Warnings` is a new field on `ir.Request`: `Warnings []Warning`, sitting beside `Extra`. It carries losses the **inbound parse** discovered, which until now could only be produced by an adapter on the way out. Add it in this task with the comment:

```go
	// Warnings are losses the inbound parse discovered. Until phase 5 every
	// warning came from an adapter rendering outbound; a dialect that drops
	// something on the way in had nowhere to say so.
	Warnings []Warning
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ -run TestParseResponses -v
```

Expected: FAIL to build — `undefined: ParseResponses`.

- [ ] **Step 3: Write the parser**

Create `internal/edge/openai/responses.go`:

```go
package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wireResponsesRequest struct {
	Model        string          `json:"model"`
	Input        json.RawMessage `json:"input"`
	Instructions string          `json:"instructions"`
	Tools        []wireRespTool  `json:"tools"`
	// RawTools is the same array undecoded, echoed verbatim into the response
	// object. Re-rendering from the decoded form would drop whatever the IR
	// does not model and quietly change what the client is told it sent.
	RawTools          json.RawMessage `json:"-"`
	ToolChoice        json.RawMessage `json:"tool_choice"`
	MaxOutputTokens   *int            `json:"max_output_tokens"`
	Temperature       *float64        `json:"temperature"`
	TopP              *float64        `json:"top_p"`
	Stream            bool            `json:"stream"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls"`
	Reasoning         *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
	Text *struct {
		Format *wireRespFormat `json:"format"`
	} `json:"text"`
	Metadata map[string]string `json:"metadata"`

	// The two stateful fields. Their presence is the rejection, so both are
	// raw: a conversation may be an id string or an object.
	PreviousResponseID string          `json:"previous_response_id"`
	Conversation       json.RawMessage `json:"conversation"`
	Background         bool            `json:"background"`
}

type wireRespTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict"`
}

// wireRespFormat is flat, and that is the whole reason it exists rather than
// reusing chat's wireResponseFormat. Chat nests the schema under a json_schema
// key; Responses puts name, schema and strict at the top level of text.format.
// Decoding the flat shape into the nested struct leaves the schema nil and
// ships a structured-output request with no schema at all — a silent drop the
// client cannot see.
type wireRespFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict *bool           `json:"strict"`
}

// wireRespItem is one element of the input array. The vocabulary is a union
// discriminated by type, except that a plain message may omit type entirely.
type wireRespItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`

	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Output    string `json:"output"`
}

type wireRespPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Refusal  string `json:"refusal"`
	ImageURL string `json:"image_url"`
	FileURL  string `json:"file_url"`
	FileData string `json:"file_data"`
	Filename string `json:"filename"`
	FileID   string `json:"file_id"`
}

// ParseResponses returns the echo alongside the request. The echo is what the
// response object must repeat back, and the dialect is constructed per request
// so it can hold it between ParseRequest and the writer.
func ParseResponses(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, *responsesEcho, error) {
	body, err := readCappedBody(r, maxBody)
	if err != nil {
		return nil, nil, nil, err
	}
	var w wireResponsesRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	// Decoded twice on purpose: once into the typed shape the IR needs, once
	// raw so the response can echo the tools array exactly as it arrived.
	var rawTop struct {
		Tools json.RawMessage `json:"tools"`
	}
	_ = json.Unmarshal(body, &rawTop)
	w.RawTools = rawTop.Tools

	// Refused before anything else is read: an answer built from a body that
	// carries only the newest turn is fluent, confident and amnesic, and the
	// client cannot tell it apart from a correct one.
	if w.PreviousResponseID != "" {
		return nil, nil, nil, errors.New(
			"previous_response_id is not supported: Darkrouter stores no conversations, " +
				"so send the full input each turn and use stateless requests")
	}
	if len(w.Conversation) > 0 && string(w.Conversation) != "null" {
		return nil, nil, nil, errors.New(
			"conversation is not supported: Darkrouter stores no conversations, " +
				"so send the full input each turn and use stateless requests")
	}
	if w.Background {
		// Answering with a finished response would leave the client polling an
		// id that will never exist.
		return nil, nil, nil, errors.New(
			"background is not supported: Darkrouter has no queue and mints no " +
				"resolvable ids, so use a stateless foreground request")
	}

	req := &ir.Request{
		Model:             w.Model,
		MaxTokens:         w.MaxOutputTokens,
		Temperature:       w.Temperature,
		TopP:              w.TopP,
		Stream:            w.Stream,
		ParallelToolCalls: w.ParallelToolCalls,
		Metadata:          w.Metadata,
	}
	if w.Instructions != "" {
		req.System = []ir.ContentBlock{{Type: ir.BlockText, Text: w.Instructions}}
	}
	if w.Reasoning != nil && w.Reasoning.Effort != "" {
		req.Reasoning = &ir.Reasoning{Effort: w.Reasoning.Effort}
	}
	if f := w.Text; f != nil && f.Format != nil && f.Format.Type == "json_schema" {
		req.ResponseFormat = &ir.ResponseFormat{Type: "json_schema", Schema: f.Format.Schema}
	}
	if err := applyResponsesTools(req, w.Tools, w.ToolChoice); err != nil {
		return nil, nil, nil, err
	}
	if err := applyResponsesInput(req, w.Input); err != nil {
		return nil, nil, nil, err
	}
	echo := &responsesEcho{
		Instructions: w.Instructions, Tools: rawArray(w.RawTools),
		ToolChoice: w.ToolChoice, ParallelToolCalls: w.ParallelToolCalls,
		Temperature: w.Temperature, TopP: w.TopP,
		MaxOutputTokens: w.MaxOutputTokens, Metadata: w.Metadata,
	}
	return req, &edge.Passthrough{
		Body: body, ModelField: "model", Surface: ir.SurfaceLLM,
	}, echo, nil
}

// rawArray keeps a nil tools array out of the response body, where the field is
// required and null would fail the SDK's model.
func rawArray(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// applyResponsesTools reads the function tools and refuses every built-in.
//
// Silently answering without a requested web search is the same class of lie as
// silently answering without the stored conversation: the response looks
// entirely successful and nothing in it says the tool never ran.
func applyResponsesTools(req *ir.Request, tools []wireRespTool, choice json.RawMessage) error {
	var warns []ir.Warning
	for _, t := range tools {
		if t.Type != "" && t.Type != "function" {
			return fmt.Errorf(
				"tool type %q is not supported: Darkrouter cannot execute built-in tools, "+
					"and answering without one would look like success", t.Type)
		}
		if t.Name == "" {
			return errors.New("a function tool has no name")
		}
		if t.Strict != nil && *t.Strict {
			// The IR has no strict flag and no adapter renders one, so the
			// model may return arguments that do not validate against the
			// schema. The client asked for a guarantee it will not get.
			warns = append(warns, ir.Warning{
				Field: "tools." + t.Name + ".strict", Target: "responses",
				Reason: "strict schema adherence is not forwarded; arguments may not validate",
			})
		}
		req.Tools = append(req.Tools, ir.Tool{
			Name: t.Name, Description: t.Description, Schema: t.Parameters,
		})
	}
	req.Warnings = append(req.Warnings, warns...)
	req.ToolChoice = parseResponsesToolChoice(choice)
	return nil
}

// parseResponsesToolChoice maps the Responses spellings onto the IR's. The
// Responses "required" is the IR's "any", which is Anthropic's spelling and the
// one the IR settled on in phase 1.
func parseResponsesToolChoice(raw json.RawMessage) *ir.ToolChoice {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "auto":
			return &ir.ToolChoice{Mode: "auto"}
		case "none":
			return &ir.ToolChoice{Mode: "none"}
		case "required":
			return &ir.ToolChoice{Mode: "any"}
		default:
			return nil
		}
	}
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	if obj.Type == "function" && obj.Name != "" {
		return &ir.ToolChoice{Mode: "tool", Name: obj.Name}
	}
	return nil
}

func applyResponsesInput(req *ir.Request, raw json.RawMessage) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return errors.New("input is required")
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("input: %w", err)
		}
		req.Messages = append(req.Messages, ir.Message{
			Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: s}},
		})
		return nil
	}

	var items []wireRespItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("input must be text or an array of items: %w", err)
	}
	if len(items) == 0 {
		return errors.New("input is empty")
	}
	for i, it := range items {
		switch {
		case it.Type == "function_call":
			args := it.Arguments
			if args == "" {
				args = "{}"
			}
			req.Messages = append(req.Messages, ir.Message{
				Role: ir.RoleAssistant,
				Content: []ir.ContentBlock{{
					Type:    ir.BlockToolUse,
					ToolUse: &ir.ToolUse{ID: it.CallID, Name: it.Name, Input: json.RawMessage(args)},
				}},
			})
		case it.Type == "function_call_output":
			req.Messages = append(req.Messages, ir.Message{
				Role: ir.RoleTool,
				Content: []ir.ContentBlock{{
					Type: ir.BlockToolResult,
					ToolResult: &ir.ToolResult{
						ToolUseID: it.CallID,
						Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: it.Output}},
					},
				}},
			})
		case it.Type == "reasoning":
			// Dropped, not replayed: an encrypted reasoning item is meaningful
			// only to the provider that minted it, and this turn may be routed
			// somewhere else entirely.
			req.Warnings = append(req.Warnings, ir.Warning{
				Field:  fmt.Sprintf("input[%d]", i),
				Target: "responses",
				Reason: "reasoning item dropped; it is only meaningful to the provider that produced it",
			})
		case it.Type == "" || it.Type == "message":
			blocks, err := responsesContent(it.Content)
			if err != nil {
				return fmt.Errorf("input[%d]: %w", i, err)
			}
			req.Messages = append(req.Messages, ir.Message{
				Role: roleOf(it.Role), Content: blocks,
			})
		default:
			return fmt.Errorf("input[%d]: item type %q is not supported", i, it.Type)
		}
	}
	if len(req.Messages) == 0 {
		return errors.New("input carried no messages")
	}
	return nil
}

func roleOf(s string) ir.Role {
	switch s {
	case "assistant":
		return ir.RoleAssistant
	case "system", "developer":
		return ir.RoleSystem
	default:
		return ir.RoleUser
	}
}

// responsesContent reads a message's content, which is a string or an array of
// typed parts. input_text and output_text differ only in which side wrote them,
// so both become text blocks.
func responsesContent(raw json.RawMessage) ([]ir.ContentBlock, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, errors.New("content is required")
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return []ir.ContentBlock{{Type: ir.BlockText, Text: s}}, nil
	}
	var parts []wireRespPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("content must be text or an array of parts: %w", err)
	}
	out := make([]ir.ContentBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text", "summary_text":
			out = append(out, ir.ContentBlock{Type: ir.BlockText, Text: p.Text})
		case "refusal":
			// A replayed assistant turn that refused carries one of these. It
			// is history, not a new refusal, so it is carried as text rather
			// than rejected — 400ing a legitimate agent-loop replay would be
			// the worse failure.
			out = append(out, ir.ContentBlock{Type: ir.BlockText, Text: p.Refusal})
		case "input_image", "image":
			blk, err := imageBlockFrom(p.ImageURL, p.FileID)
			if err != nil {
				return nil, err
			}
			out = append(out, blk)
		case "input_file", "file":
			// A file part carries file_url or inline file_data, not image_url.
			// Reading only the latter would drop the document silently.
			m := &ir.Media{FileID: p.FileID, URL: p.FileURL, Data: p.FileData}
			out = append(out, ir.ContentBlock{Type: ir.BlockDocument, Media: m})
		default:
			return nil, fmt.Errorf("content part type %q is not supported", p.Type)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("content carried no parts")
	}
	return out, nil
}

// imageBlockFrom splits a data URL into its MIME type and payload, and passes a
// plain URL or a provider file handle through. FileID is not interchangeable
// with URL: a target accepting its own handle will reject a public address.
func imageBlockFrom(imageURL, fileID string) (ir.ContentBlock, error) {
	if fileID != "" {
		return ir.ContentBlock{Type: ir.BlockImage, Media: &ir.Media{FileID: fileID}}, nil
	}
	if imageURL == "" {
		return ir.ContentBlock{}, errors.New("image part has neither image_url nor file_id")
	}
	if rest, ok := strings.CutPrefix(imageURL, "data:"); ok {
		meta, payload, found := strings.Cut(rest, ",")
		if !found {
			return ir.ContentBlock{}, errors.New("malformed data URL in an image part")
		}
		mime, _, _ := strings.Cut(meta, ";")
		return ir.ContentBlock{
			Type: ir.BlockImage, Media: &ir.Media{MIME: mime, Data: payload},
		}, nil
	}
	return ir.ContentBlock{Type: ir.BlockImage, Media: &ir.Media{URL: imageURL}}, nil
}
```

**Do not reuse chat's `wireResponseFormat` here.** It is `{Type string; JSONSchema *struct{Name, Schema}}` and expects the schema nested under a `json_schema` key, because that is chat's shape. Responses flattens it: `text.format` is `{"type":"json_schema","name":…,"schema":…,"strict":…}`. Decoding the flat shape into the nested struct leaves `JSONSchema` nil, the conversion produces nothing, and every structured-output Responses request reaches the provider **with no schema** — succeeding, returning free-form text, and giving the client nothing to notice. That is why `wireRespFormat` above is its own type and why the test below asserts the schema survives rather than only its type.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
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
git add internal/ir/ir.go internal/edge/openai/responses.go internal/edge/openai/responses_test.go internal/edge/openai/parse.go
git commit -m "feat(edge): parse the responses request shape"
```

---

### Task 26: The Responses item-based body

**Files:**
- Modify: `internal/edge/openai/responses.go`
- Test: `internal/edge/openai/responses_test.go`

**Interfaces:**
- Consumes: `ir.Response` (phase 1).
- Produces: `openai.WriteResponses(w, *ir.Response) error`, plus `responsesBody(id string, resp *ir.Response, status string) map[string]any` and `responsesItemID(kind string, index int) string`. Task 28's stream writer reuses both so the streamed and unary final objects cannot drift.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: the item vocabulary is enumerated below and `write.go` beside it is the pattern for assembling a body from `ir.Response`.

Responses returns an **item-based** body: an `output` array of `reasoning`, `message` and `function_call` items rather than a single `choices[0].message`. Usage is named `input_tokens` and `output_tokens`, not `prompt_tokens` and `completion_tokens`.

**`responsesBody` is factored out on purpose.** The streamed `response.completed` event carries the same object the unary path returns, and two independently-written assemblers would drift on exactly the fields a client reads last. Task 28 accumulates its deltas into an `ir.Response` and calls this.

**The response object must echo the request, and three of those fields are required.** In `openai-python`'s `Response` model, `tools`, `tool_choice` and `parallel_tool_calls` are declared with **no default**, and `error`, `incomplete_details`, `instructions`, `metadata`, `temperature` and `top_p` are always present though nullable. A body omitting them survives the SDK's lenient `construct()` path and misbehaves on any strict one — and the Agents SDK reads `response.tools` directly. So `ParseResponses` keeps what it needs to echo, verbatim as raw JSON rather than re-rendered, and hands it to the writer. Echoing the client's own bytes cannot drift from what the client sent.

**Ids are marked non-resumable, and `store: false` is how.** Darkrouter mints no resolvable ids, and a Responses client reads `store: false` as "this response was not persisted and cannot be referenced". The `resp_dr_` prefix makes the same fact legible to a human reading a log line. Task 25 already refuses any request that carries one back.

- [ ] **Step 1: Write the failing test**

Add to `internal/edge/openai/responses_test.go`:

```go
func TestWriteResponsesEmitsItems(t *testing.T) {
	w := httptest.NewRecorder()
	err := WriteResponses(w, &ir.Response{
		ID: "chatcmpl-1", Model: "gpt-4o",
		Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "pondering"}},
			{Type: ir.BlockText, Text: "the answer"},
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: "call_1", Name: "f", Input: []byte(`{"a":1}`)}},
		},
		StopReason: ir.StopToolUse,
		Usage:      ir.Usage{InputTokens: 5, OutputTokens: 7, ReasoningTokens: 3},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		ID     string `json:"id"`
		Object string `json:"object"`
		Status string `json:"status"`
		Model  string `json:"model"`
		Store  bool   `json:"store"`
		Output []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Summary []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"summary"`
		} `json:"output"`
		OutputText string `json:"output_text"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
			OutDetails   struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "response" || body.Status != "completed" || body.Model != "gpt-4o" {
		t.Fatalf("body = %s", w.Body.String())
	}
	if !strings.HasPrefix(body.ID, "resp_dr_") {
		t.Errorf("id = %q; it must be legible as non-resumable", body.ID)
	}
	if body.Store {
		t.Error("store = true; Darkrouter persists nothing and the client must be told")
	}
	if len(body.Output) != 3 {
		t.Fatalf("output = %+v", body.Output)
	}
	if body.Output[0].Type != "reasoning" || body.Output[0].Summary[0].Text != "pondering" {
		t.Errorf("reasoning item = %+v", body.Output[0])
	}
	if body.Output[1].Type != "message" || body.Output[1].Role != "assistant" ||
		body.Output[1].Content[0].Type != "output_text" ||
		body.Output[1].Content[0].Text != "the answer" {
		t.Errorf("message item = %+v", body.Output[1])
	}
	if body.Output[2].Type != "function_call" || body.Output[2].CallID != "call_1" ||
		body.Output[2].Name != "f" || body.Output[2].Arguments != `{"a":1}` {
		t.Errorf("function call item = %+v", body.Output[2])
	}
	if body.OutputText != "the answer" {
		t.Errorf("output_text = %q; the SDK convenience field is wrong", body.OutputText)
	}
	if body.Usage.InputTokens != 5 || body.Usage.OutputTokens != 7 ||
		body.Usage.TotalTokens != 12 || body.Usage.OutDetails.ReasoningTokens != 3 {
		t.Errorf("usage = %+v; Responses names these differently from chat", body.Usage)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestWriteResponsesReportsATruncatedAnswerAsIncomplete(t *testing.T) {
	// A client that reads status "completed" stops asking. A length-truncated
	// answer is not completed, and Responses has a field that says so.
	w := httptest.NewRecorder()
	if err := WriteResponses(w, &ir.Response{
		ID: "x", Model: "m",
		Content:    []ir.ContentBlock{{Type: ir.BlockText, Text: "half an ans"}},
		StopReason: ir.StopMaxTokens,
	}, nil); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Status     string `json:"status"`
		Incomplete *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "incomplete" {
		t.Errorf("status = %q", body.Status)
	}
	if body.Incomplete == nil || body.Incomplete.Reason != "max_output_tokens" {
		t.Errorf("incomplete_details = %+v", body.Incomplete)
	}
}

func TestWriteResponsesReportsAFilteredAnswerAsIncomplete(t *testing.T) {
	w := httptest.NewRecorder()
	if err := WriteResponses(w, &ir.Response{
		ID: "x", Model: "m", StopReason: ir.StopContentFilter,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: ""}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Status     string `json:"status"`
		Incomplete *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Status != "incomplete" || body.Incomplete == nil ||
		body.Incomplete.Reason != "content_filter" {
		t.Errorf("status = %q, incomplete = %+v", body.Status, body.Incomplete)
	}
}

func TestWriteResponsesEchoesTheRequiredRequestFields(t *testing.T) {
	// tools, tool_choice and parallel_tool_calls have no default in the SDK's
	// Response model; a body omitting them fails strict validation, and the
	// Agents SDK reads response.tools directly.
	echo := parseResponsesEcho(t, `{"model":"m","input":"hi","instructions":"be terse",
	  "temperature":0.25,"top_p":0.9,"parallel_tool_calls":false,
	  "tools":[{"type":"function","name":"f","parameters":{"type":"object"}}],
	  "tool_choice":"required"}`)

	w := httptest.NewRecorder()
	if err := WriteResponses(w, &ir.Response{ID: "x", Model: "m"}, echo); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"tools", "tool_choice", "parallel_tool_calls",
		"error", "incomplete_details", "instructions", "metadata", "temperature", "top_p"} {
		if _, present := body[k]; !present {
			t.Errorf("response omits %q, which the SDK model requires", k)
		}
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Errorf("tools = %v; the client's own array must be echoed", body["tools"])
	}
	if body["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v; it must be echoed verbatim, not translated", body["tool_choice"])
	}
	if body["parallel_tool_calls"] != false || body["instructions"] != "be terse" {
		t.Errorf("body = %s", w.Body.String())
	}
	if body["temperature"].(float64) != 0.25 || body["top_p"].(float64) != 0.9 {
		t.Errorf("sampling echo = %v / %v", body["temperature"], body["top_p"])
	}
}

func TestWriteResponsesWithNoEchoStillCarriesTheRequiredFields(t *testing.T) {
	w := httptest.NewRecorder()
	if err := WriteResponses(w, &ir.Response{ID: "x", Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["parallel_tool_calls"] != true || body["tool_choice"] != "auto" {
		t.Errorf("defaults = %v / %v", body["parallel_tool_calls"], body["tool_choice"])
	}
	if tools, ok := body["tools"].([]any); !ok || len(tools) != 0 {
		t.Errorf("tools = %v, want an empty array rather than null", body["tools"])
	}
}

func TestWriteResponsesEmitsAnEmptyOutputArrayNotNull(t *testing.T) {
	// An SDK ranges over output. null there is a crash rather than no items.
	w := httptest.NewRecorder()
	if err := WriteResponses(w, &ir.Response{ID: "x", Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), `"output":[]`) {
		t.Errorf("body = %s", w.Body.String())
	}
}
```

Add `"encoding/json"` to that file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ -run TestWriteResponses -v
```

Expected: FAIL to build — `undefined: WriteResponses`.

- [ ] **Step 3: Write the assembler**

Append to `internal/edge/openai/responses.go`:

```go
// responsesEcho is what the response object repeats back to the client.
//
// tools, tool_choice and parallel_tool_calls are required fields with no
// default in the OpenAI SDK's Response model, so omitting them breaks strict
// validation; the rest are always present though nullable. The tool fields are
// held as raw JSON and echoed verbatim, which cannot drift from what the client
// sent the way a re-rendering would.
type responsesEcho struct {
	Instructions      string
	Tools             json.RawMessage
	ToolChoice        json.RawMessage
	ParallelToolCalls *bool
	Temperature       *float64
	TopP              *float64
	MaxOutputTokens   *int
	Metadata          map[string]string
}

// responsesID marks the id as one Darkrouter cannot resolve. Master design and
// spec §5: no resolvable ids are minted, so the prefix makes that legible in a
// log line and store:false makes it legible to the client.
func responsesID(upstream string) string {
	if upstream == "" {
		return "resp_dr_darkrouter"
	}
	return "resp_dr_" + upstream
}

// responsesItemID names an output item. It is derived from the response id and
// the item's position so that the streamed events and the final object agree —
// a client correlates deltas to items by this value.
func responsesItemID(respID, kind string, index int) string {
	return fmt.Sprintf("%s_%s_%d", strings.TrimPrefix(respID, "resp_dr_"), kind, index)
}

// responsesStatus maps a stop reason onto the Responses status pair. A client
// reading "completed" stops asking, so a truncated or filtered answer must not
// claim it.
func responsesStatus(s ir.StopReason) (string, string) {
	switch s {
	case ir.StopMaxTokens:
		return "incomplete", "max_output_tokens"
	case ir.StopContentFilter:
		return "incomplete", "content_filter"
	default:
		return "completed", ""
	}
}

// responsesBody assembles the object both the unary path and the streamed
// response.completed event return. It is one function so the two cannot drift
// on the fields a client reads last.
func responsesBody(id string, resp *ir.Response, status, incomplete string, echo *responsesEcho) map[string]any {
	output := make([]any, 0, len(resp.Content))
	var text strings.Builder
	for _, b := range resp.Content {
		idx := len(output)
		switch b.Type {
		case ir.BlockThinking:
			if b.Thinking == nil || b.Thinking.Text == "" {
				continue
			}
			output = append(output, map[string]any{
				"type": "reasoning",
				"id":   responsesItemID(id, "rs", idx),
				"summary": []any{map[string]any{
					"type": "summary_text", "text": b.Thinking.Text,
				}},
			})
		case ir.BlockText:
			if b.Text == "" {
				continue
			}
			text.WriteString(b.Text)
			output = append(output, map[string]any{
				"type": "message", "id": responsesItemID(id, "msg", idx),
				"status": "completed", "role": "assistant",
				"content": []any{map[string]any{
					"type": "output_text", "text": b.Text, "annotations": []any{},
				}},
			})
		case ir.BlockToolUse:
			if b.ToolUse == nil {
				continue
			}
			args := string(b.ToolUse.Input)
			if args == "" {
				args = "{}"
			}
			output = append(output, map[string]any{
				"type": "function_call", "id": responsesItemID(id, "fc", idx),
				"call_id": b.ToolUse.ID, "name": b.ToolUse.Name,
				"arguments": args, "status": "completed",
			})
		}
	}

	body := map[string]any{
		"id":         id,
		"object":     "response",
		"created_at": now().Unix(),
		"status":     status,
		"model":      resp.Model,
		// Darkrouter persists nothing, and this is the field a Responses client
		// reads to learn the id cannot be referenced later.
		"store":       false,
		"output":      output,
		"output_text": text.String(),
		"usage":       responsesUsage(resp.Usage),
	}
	// Always present, null when there is nothing to report: an SDK reads
	// through both without a presence check.
	body["error"] = nil
	body["incomplete_details"] = nil
	if incomplete != "" {
		body["incomplete_details"] = map[string]any{"reason": incomplete}
	}
	applyResponsesEcho(body, echo)
	return body
}

// applyResponsesEcho fills the request-derived fields. tools, tool_choice and
// parallel_tool_calls have no default in the SDK's model, so they are written
// even when the request omitted them.
func applyResponsesEcho(body map[string]any, echo *responsesEcho) {
	body["tools"] = json.RawMessage("[]")
	body["tool_choice"] = json.RawMessage(`"auto"`)
	body["parallel_tool_calls"] = true
	body["instructions"] = nil
	body["metadata"] = map[string]string{}
	body["temperature"] = nil
	body["top_p"] = nil
	body["max_output_tokens"] = nil
	if echo == nil {
		return
	}
	if len(echo.Tools) > 0 {
		body["tools"] = echo.Tools
	}
	if len(echo.ToolChoice) > 0 {
		body["tool_choice"] = echo.ToolChoice
	}
	if echo.ParallelToolCalls != nil {
		body["parallel_tool_calls"] = *echo.ParallelToolCalls
	}
	if echo.Instructions != "" {
		body["instructions"] = echo.Instructions
	}
	if echo.Metadata != nil {
		body["metadata"] = echo.Metadata
	}
	if echo.Temperature != nil {
		body["temperature"] = *echo.Temperature
	}
	if echo.TopP != nil {
		body["top_p"] = *echo.TopP
	}
	if echo.MaxOutputTokens != nil {
		body["max_output_tokens"] = *echo.MaxOutputTokens
	}
}

func responsesUsage(u ir.Usage) map[string]any {
	body := map[string]any{
		"input_tokens":  u.InputTokens,
		"output_tokens": u.OutputTokens,
		"total_tokens":  u.InputTokens + u.OutputTokens,
	}
	// The details objects are always present: an SDK reads through them
	// without a nil check, unlike chat's, where OpenAI itself omits them.
	body["input_tokens_details"] = map[string]any{"cached_tokens": u.CacheReadTokens}
	body["output_tokens_details"] = map[string]any{"reasoning_tokens": u.ReasoningTokens}
	return body
}

func WriteResponses(w http.ResponseWriter, resp *ir.Response, echo *responsesEcho) error {
	id := responsesID(resp.ID)
	status, incomplete := responsesStatus(resp.StopReason)
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(responsesBody(id, resp, status, incomplete, echo))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
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
git add internal/edge/openai/responses.go internal/edge/openai/responses_test.go
git commit -m "feat(edge): assemble the responses item body"
```

---

### Task 27: Reasoning deltas need an index of their own

**Files:**
- Modify: `internal/adapter/openaicompat/parse.go`
- Test: `internal/adapter/openaicompat/parse_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: a distinct block index space for reasoning in the openaicompat stream. Task 28's writer is the first consumer that keys on index and cannot work without it.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: `toolBlockBase` in the same file is the established fix for exactly this collision, and this applies it to the third block kind.

**This is a real defect in shipped code, found while reviewing the Responses stream writer.** In `internal/adapter/openaicompat/parse.go`'s `ParseStream`, text deltas open a block at `textIdx = 0` with a proper `EventBlockStart`, and tool deltas get `toolBlockBase + tc.Index` precisely so they "cannot collide" — the constant's own comment says so. Reasoning deltas get neither:

```go
				if d.Reasoning != "" {
					if !yield(ir.StreamEvent{Type: ir.EventContentDelta,
						Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: d.Reasoning}}, nil) {
						return
					}
				}
```

No `Index`, so the zero value — **which is the text block's index** — and no `EventBlockStart` or `EventBlockStop` at all.

It has been invisible because the only consumers so far switch on `ev.Delta.Type` and ignore the index. The Responses stream writer is the first that keys items *by* index, and on a model that emits both reasoning and text it either misattributes every text delta to the reasoning item or dereferences a nil `Thinking` and panics, depending on which arrives first. Anthropic and Gemini both assign distinct indices and are unaffected.

Fixing it at the adapter rather than working around it in the writer is the right call for three reasons: the IR contract says a block index identifies a block, one consumer's workaround would not protect the next one, and a `BlockThinking` delta and a `BlockText` delta sharing an index is wrong however it is consumed.

- [ ] **Step 1: Write the failing test**

Add to `internal/adapter/openaicompat/parse_test.go`:

```go
func TestReasoningAndTextDoNotShareABlockIndex(t *testing.T) {
	// toolBlockBase exists because a colliding index is a bug. Reasoning was
	// left at the zero value, which is the text block's index.
	body := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`,
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"answer"}}]}`,
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"data: [DONE]",
		"",
	}, "\n\n")

	var thinkIdx, textIdx = -1, -1
	for ev, err := range New().ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Type != ir.EventContentDelta || ev.Delta == nil {
			continue
		}
		switch ev.Delta.Type {
		case ir.BlockThinking:
			thinkIdx = ev.Index
		case ir.BlockText:
			textIdx = ev.Index
		}
	}
	if thinkIdx < 0 || textIdx < 0 {
		t.Fatalf("missing deltas: thinking at %d, text at %d", thinkIdx, textIdx)
	}
	if thinkIdx == textIdx {
		t.Errorf("reasoning and text both at index %d; a consumer keying on the index "+
			"cannot tell the two blocks apart", thinkIdx)
	}
}

func TestAReasoningBlockIsOpenedAndClosed(t *testing.T) {
	// A consumer that opens an item on block_start and finalizes it on
	// block_stop gets neither for reasoning, so the item is never closed.
	body := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`,
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"data: [DONE]",
		"",
	}, "\n\n")

	var start, stop bool
	var startIdx, stopIdx int
	for ev, err := range New().ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case ir.EventBlockStart:
			if ev.Delta != nil && ev.Delta.Type == ir.BlockThinking {
				start, startIdx = true, ev.Index
			}
		case ir.EventBlockStop:
			stop, stopIdx = true, ev.Index
		}
	}
	if !start {
		t.Error("no content_block_start for the reasoning block")
	}
	if !stop || stopIdx != startIdx {
		t.Errorf("reasoning block opened at %d was not closed (stop=%v at %d)", startIdx, stop, stopIdx)
	}
}
```

Check the wire field name the existing tests use for reasoning — `reasoning_content` is OpenAI-compatible providers' spelling and is what `wireDelta` should already carry. **Read the struct before writing the fixture** rather than assuming.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/openaicompat/ -run 'ReasoningAndText|AReasoningBlock' -v
```

Expected: the first reports both at index 0; the second reports no `content_block_start`.

- [ ] **Step 3: Give reasoning its own index space**

In `internal/adapter/openaicompat/parse.go`, beside `toolBlockBase`:

```go
// reasoningBlockBase does for reasoning what toolBlockBase does for tool calls.
// A block index identifies a block, and a consumer that keys items by index —
// the responses stream writer is the first — cannot tell a thinking delta from
// a text delta when both arrive at zero.
const reasoningBlockBase = 2000
```

and replace the reasoning branch inside the choices loop:

```go
				if d.Reasoning != "" {
					if reasoningIdx < 0 {
						reasoningIdx = reasoningBlockBase
						open[reasoningIdx] = true
						if !yield(ir.StreamEvent{Type: ir.EventBlockStart, Index: reasoningIdx,
							Delta: &ir.Delta{Type: ir.BlockThinking}}, nil) {
							return
						}
					}
					if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: reasoningIdx,
						Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: d.Reasoning}}, nil) {
						return
					}
				}
```

Declare `reasoningIdx := -1` beside `textIdx := -1`, and reset it to `-1` wherever `textIdx` is reset. `closeAll` already emits an `EventBlockStop` for every key in `open`, so registering the block there is what closes it — read `closeAll` and confirm that before relying on it.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/openaicompat/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package. Existing stream tests switch on `Delta.Type` and ignore the index, so none should move.

- [ ] **Step 5: Check the golden suite, which is where a stream shape change shows**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/golden/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

If a golden file changes, **read the diff before regenerating**. A new `content_block_start`/`content_block_stop` pair around reasoning is the expected change and is correct; anything else — reordered text, a changed tool index, a dropped event — means the edit went wrong. Regenerate only after reading, and say in the commit which files moved and why.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/adapter/openaicompat/parse.go internal/adapter/openaicompat/parse_test.go
git commit -m "fix(openaicompat): index reasoning blocks distinctly"
```

---

### Task 28: The Responses semantic stream writer

**Files:**
- Create: `internal/edge/openai/responses_stream.go`
- Test: `internal/edge/openai/responses_stream_test.go`

**Interfaces:**
- Consumes: `responsesBody`, `responsesItemID`, `responsesID`, `responsesStatus` (Task 26).
- Produces: `openai.WriteResponsesStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error`. Task 29's dialect calls it.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4
**Approach:** inline - skip 2: the event sequence is enumerated below in full and `stream.go` beside it is the pattern for driving one from `iter.Seq2`.

Spec §5 calls this "effectively a fourth edge stream writer, not a thin adaptation, and the largest single piece of work in this phase". Three things make it different from the chat writer beside it:

- **Every event carries a `sequence_number`**, monotonic across the whole stream, and clients use it to detect drops.
- **Items are opened and closed explicitly.** A client does not treat an item as final until it sees `response.output_item.done`, so a stream that ends mid-item leaves the client waiting forever. Whatever the provider left open is closed before `response.completed`.
- **The terminal event carries the whole response object**, the same one the unary path returns. That is why the writer accumulates into an `ir.Response` and calls Task 26's `responsesBody` rather than assembling a second time.

**The terminal event waits for the end of the sequence, not for `message_stop`.** OpenAI-compatible upstreams send the usage chunk *after* the chunk carrying `finish_reason`, and `internal/adapter/openaicompat/build.go` always injects `stream_options.include_usage`, so the IR yields `EventMessageStop` and only then `EventMessageDelta` with the usage. Completing on `message_stop` would report **zero usage on every streamed Responses call**. `message_stop` therefore closes the open items and records the stop reason; the terminal event goes out when the iterator is exhausted.

**The terminal event's name follows the status.** `response.completed` always carries status `completed`; a truncated or filtered answer ends with `response.incomplete`. A client switching on the event type — which is the normal way to consume this stream — would otherwise treat a half answer as a whole one.

**There is no `[DONE]` sentinel.** Chat's SSE ends with one; the Responses stream ends at `response.completed`. Sending it would put an unparseable line in front of a client that reads every `data:` as JSON.

**Output index is not the IR block index.** `openaicompat` offsets tool blocks by 1000 to keep them clear of the text block, so the two are mapped exactly as chat's writer maps `toolIndex` — and the mapping has a second job here, because the item ids the deltas carry must match the ids `responsesBody` derives from position in the final object.

The full sequence for a turn with reasoning, text and one tool call:

| Order | Event |
|---|---|
| 1 | `response.created` |
| 2 | `response.in_progress` |
| 3 | `response.output_item.added` (reasoning) |
| 4 | `response.reasoning_summary_part.added` |
| 5 | `response.reasoning_summary_text.delta` × n |
| 6 | `response.reasoning_summary_text.done`, `.part.done`, `response.output_item.done` |
| 7 | `response.output_item.added` (message), `response.content_part.added` |
| 8 | `response.output_text.delta` × n |
| 9 | `response.output_text.done`, `response.content_part.done`, `response.output_item.done` |
| 10 | `response.output_item.added` (function_call) |
| 11 | `response.function_call_arguments.delta` × n |
| 12 | `response.function_call_arguments.done`, `response.output_item.done` |
| 13 | `response.completed` |

- [ ] **Step 1: Write the failing test**

Create `internal/edge/openai/responses_stream_test.go`:

```go
package openai

import (
	"encoding/json"
	"iter"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// respEvents runs the writer over a fixed sequence and returns the parsed
// event objects in order.
func respEvents(t *testing.T, evs []ir.StreamEvent, final error) []map[string]any {
	t.Helper()
	seq := func(yield func(ir.StreamEvent, error) bool) {
		for _, e := range evs {
			if !yield(e, nil) {
				return
			}
		}
		if final != nil {
			yield(ir.StreamEvent{}, final)
		}
	}
	w := httptest.NewRecorder()
	// A nil echo is the no-request case and must produce a valid object: the
	// required fields fall back to their SDK defaults.
	if err := WriteResponsesStream(w, iter.Seq2[ir.StreamEvent, error](seq), nil); err != nil {
		t.Fatalf("WriteResponsesStream: %v", err)
	}
	var out []map[string]any
	for _, block := range strings.Split(w.Body.String(), "\n\n") {
		var data string
		for _, line := range strings.Split(block, "\n") {
			if rest, ok := strings.CutPrefix(line, "data: "); ok {
				data += rest
			}
		}
		if data == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			t.Fatalf("event data is not JSON: %q", data)
		}
		out = append(out, obj)
	}
	return out
}

func types(evs []map[string]any) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		s, _ := e["type"].(string)
		out = append(out, s)
	}
	return out
}

// textTurn puts the usage event AFTER message_stop, which is the real order:
// OpenAI-compatible upstreams send the usage chunk after the finish chunk and
// Darkrouter always asks for it.
func textTurn() []ir.StreamEvent {
	return []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "chatcmpl-1", Model: "gpt-4o"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "he"}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "llo"}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
		{Type: ir.EventMessageDelta, Usage: &ir.Usage{InputTokens: 3, OutputTokens: 2}},
	}
}

func TestResponsesStreamEmitsTheTextLifecycle(t *testing.T) {
	got := types(respEvents(t, textTurn(), nil))
	want := []string{
		"response.created", "response.in_progress",
		"response.output_item.added", "response.content_part.added",
		"response.output_text.delta", "response.output_text.delta",
		"response.output_text.done", "response.content_part.done",
		"response.output_item.done", "response.completed",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResponsesStreamNumbersEveryEvent(t *testing.T) {
	// Clients detect drops with this. A repeated or missing number is
	// indistinguishable from a lost event.
	evs := respEvents(t, textTurn(), nil)
	for i, e := range evs {
		n, ok := e["sequence_number"].(float64)
		if !ok {
			t.Fatalf("event %d (%v) has no sequence_number", i, e["type"])
		}
		if int(n) != i {
			t.Errorf("event %d has sequence_number %d", i, int(n))
		}
	}
}

func TestResponsesStreamNeverSendsTheChatSentinel(t *testing.T) {
	// The Responses stream ends at response.completed. [DONE] would put an
	// unparseable line in front of a client that reads every data: as JSON.
	w := httptest.NewRecorder()
	seq := func(yield func(ir.StreamEvent, error) bool) {
		for _, e := range textTurn() {
			if !yield(e, nil) {
				return
			}
		}
	}
	if err := WriteResponsesStream(w, iter.Seq2[ir.StreamEvent, error](seq), nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("body carried the chat sentinel:\n%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "event: response.completed") {
		t.Errorf("no named completed event:\n%s", w.Body.String())
	}
}

func TestResponsesStreamCompletedCarriesTheWholeResponse(t *testing.T) {
	evs := respEvents(t, textTurn(), nil)
	last := evs[len(evs)-1]
	resp, ok := last["response"].(map[string]any)
	if !ok {
		t.Fatalf("completed event = %v", last)
	}
	if resp["status"] != "completed" || resp["output_text"] != "hello" {
		t.Errorf("response = %v", resp)
	}
	if resp["store"] != false {
		t.Errorf("store = %v; the streamed object must say the id is not resumable", resp["store"])
	}
	usage, _ := resp["usage"].(map[string]any)
	if usage == nil || usage["input_tokens"].(float64) != 3 || usage["output_tokens"].(float64) != 2 {
		t.Errorf("usage = %v", usage)
	}
	out, _ := resp["output"].([]any)
	if len(out) != 1 {
		t.Fatalf("output = %v", out)
	}
	item, _ := out[0].(map[string]any)
	if item["type"] != "message" || item["status"] != "completed" {
		t.Errorf("item = %v", item)
	}
}

func TestResponsesStreamItemIDsMatchTheFinalObject(t *testing.T) {
	// A client correlates deltas to items by item_id. If the streamed id and
	// the one in the final object differ, the assembled answer is dropped.
	evs := respEvents(t, textTurn(), nil)
	var deltaID string
	for _, e := range evs {
		if e["type"] == "response.output_text.delta" {
			deltaID, _ = e["item_id"].(string)
		}
	}
	resp := evs[len(evs)-1]["response"].(map[string]any)
	item := resp["output"].([]any)[0].(map[string]any)
	if deltaID == "" || deltaID != item["id"] {
		t.Errorf("delta item_id = %q, final item id = %v", deltaID, item["id"])
	}
}

func TestResponsesStreamEmitsAToolCallLifecycle(t *testing.T) {
	got := types(respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventBlockStart, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_1", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `{"a":`}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `1}`}},
		{Type: ir.EventBlockStop, Index: 1000},
		{Type: ir.EventMessageStop, StopReason: ir.StopToolUse},
	}, nil))
	want := []string{
		"response.created", "response.in_progress", "response.output_item.added",
		"response.function_call_arguments.delta", "response.function_call_arguments.delta",
		"response.function_call_arguments.done", "response.output_item.done",
		"response.completed",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResponsesStreamCarriesTheAssembledArguments(t *testing.T) {
	evs := respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventBlockStart, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_1", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `{"a":1}`}},
		{Type: ir.EventBlockStop, Index: 1000},
		{Type: ir.EventMessageStop, StopReason: ir.StopToolUse},
	}, nil)
	resp := evs[len(evs)-1]["response"].(map[string]any)
	item := resp["output"].([]any)[0].(map[string]any)
	if item["type"] != "function_call" || item["call_id"] != "call_1" ||
		item["name"] != "f" || item["arguments"] != `{"a":1}` {
		t.Errorf("item = %v", item)
	}
}

func TestResponsesStreamEmitsReasoningSummaries(t *testing.T) {
	got := types(respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{
			Type: ir.BlockThinking, Thinking: "pondering"}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil))
	want := []string{
		"response.created", "response.in_progress", "response.output_item.added",
		"response.reasoning_summary_part.added", "response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done", "response.reasoning_summary_part.done",
		"response.output_item.done", "response.completed",
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResponsesStreamClosesAnItemTheProviderLeftOpen(t *testing.T) {
	// A client does not treat an item as final until output_item.done. A
	// stream that ends mid-item would leave it waiting forever.
	got := types(respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil))
	var sawDone, sawCompleted bool
	for i, ty := range got {
		if ty == "response.output_item.done" {
			sawDone = true
			for _, later := range got[i+1:] {
				if later == "response.completed" {
					sawCompleted = true
				}
			}
		}
	}
	if !sawDone || !sawCompleted {
		t.Errorf("events = %v; the open item was not closed before completion", got)
	}
}

func TestResponsesStreamReportsTruncationAsIncomplete(t *testing.T) {
	evs := respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "half"}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventMessageStop, StopReason: ir.StopMaxTokens},
	}, nil)
	last := evs[len(evs)-1]
	// The event NAME, not only the status inside it. A client switching on the
	// terminal event type would treat response.completed as a whole answer.
	if last["type"] != "response.incomplete" {
		t.Errorf("terminal event = %v, want response.incomplete", last["type"])
	}
	resp := last["response"].(map[string]any)
	if resp["status"] != "incomplete" {
		t.Errorf("status = %v", resp["status"])
	}
	det, _ := resp["incomplete_details"].(map[string]any)
	if det == nil || det["reason"] != "max_output_tokens" {
		t.Errorf("incomplete_details = %v", det)
	}
}

func TestResponsesStreamReportsUsageThatArrivesAfterTheStop(t *testing.T) {
	// This is the real order on every OpenAI-compatible upstream. Completing on
	// message_stop would report zero usage on every streamed response.
	evs := respEvents(t, textTurn(), nil)
	resp := evs[len(evs)-1]["response"].(map[string]any)
	usage, _ := resp["usage"].(map[string]any)
	if usage == nil || usage["input_tokens"].(float64) != 3 || usage["output_tokens"].(float64) != 2 {
		t.Errorf("usage = %v; the post-stop usage event was not waited for", usage)
	}
}

func TestResponsesStreamEmitsExactlyOneTerminalEvent(t *testing.T) {
	// A reader error after the finish chunk must not append a second one.
	evs := respEvents(t, textTurn(), &ir.Error{Type: ir.ErrAPI, Message: "trailing garbage"})
	terminal := 0
	for _, e := range evs {
		switch e["type"] {
		case "response.completed", "response.incomplete", "response.failed":
			terminal++
		}
	}
	if terminal != 1 {
		t.Errorf("%d terminal events in %v", terminal, types(evs))
	}
}

func TestResponsesStreamEndsAFailedStreamWithResponseFailed(t *testing.T) {
	// The client has already received content, so it cannot be given a
	// different response. It must at least be told this one did not finish.
	evs := respEvents(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "c1", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}},
	}, &ir.Error{Type: ir.ErrOverloaded, Message: "upstream went away"})

	last := evs[len(evs)-1]
	if last["type"] != "response.failed" {
		t.Fatalf("last event = %v", last["type"])
	}
	resp, _ := last["response"].(map[string]any)
	e, _ := resp["error"].(map[string]any)
	if e == nil || e["code"] != string(ir.ErrOverloaded) ||
		!strings.Contains(e["message"].(string), "went away") {
		t.Errorf("error = %v", e)
	}
	if resp["status"] != "failed" {
		t.Errorf("status = %v", resp["status"])
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ -run TestResponsesStream -v
```

Expected: FAIL to build — `undefined: WriteResponsesStream`.

- [ ] **Step 3: Write the stream writer**

Create `internal/edge/openai/responses_stream.go`:

```go
package openai

import (
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"sort"
	"strings"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

// responsesItem is one open output item. It holds what the closing events need
// and what the accumulated response needs, which are the same text.
type responsesItem struct {
	index  int // the output index, which is also its position in acc.Content
	kind   string
	itemID string
	text   strings.Builder
}

// responsesStream is the state a semantic stream needs that a chat stream does
// not: every event carries a sequence number and an output index, every item is
// opened and closed explicitly, and the terminal event carries the whole
// response object.
type responsesStream struct {
	s   *sse.Writer
	seq int
	id  string

	// acc is the response as it accumulates. response.completed returns the
	// same object the unary path does, and building it here as an ir.Response
	// is what lets both call responsesBody rather than assembling twice.
	acc ir.Response

	// open maps an IR block index to its item. The IR block index is not the
	// output index — openaicompat offsets tool blocks by 1000 to keep them
	// clear of the text block — so the two are mapped, exactly as chat's
	// writer maps toolIndex.
	open map[int]*responsesItem
	next int

	echo *responsesEcho

	started   bool
	completed bool
	// stopped records that the provider sent its finish reason. The terminal
	// event is not emitted there: OpenAI-compatible upstreams send the usage
	// chunk AFTER the finish chunk, and Darkrouter always asks for it, so
	// completing on message_stop would report zero usage on every streamed
	// response. The terminal event goes out when the sequence ends.
	stopped bool
}

// WriteResponsesStream converts canonical stream events into Responses semantic
// events. There is no DONE sentinel: the Responses stream ends at
// response.completed, and chat's sentinel would put an unparseable line in
// front of a client that reads every data: line as JSON.
func WriteResponsesStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error],
	echo *responsesEcho) error {

	rs := &responsesStream{s: sse.NewWriter(w), open: map[int]*responsesItem{}, echo: echo}
	for ev, err := range events {
		if err != nil {
			return rs.fail(err)
		}
		if serr := rs.handle(ev); serr != nil {
			return serr
		}
	}
	// A provider that ends without a message_stop still owes the client a
	// terminal event, or it waits forever.
	return rs.complete()
}

func (rs *responsesStream) send(kind string, obj map[string]any) error {
	obj["type"] = kind
	obj["sequence_number"] = rs.seq
	rs.seq++
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	return rs.s.Send(kind, string(b))
}

func (rs *responsesStream) handle(ev ir.StreamEvent) error {
	switch ev.Type {
	case ir.EventMessageStart:
		rs.acc.ID, rs.acc.Model = ev.ID, ev.Model
		rs.id = responsesID(ev.ID)
		return rs.ensureStarted()
	case ir.EventBlockStart:
		if ev.Delta == nil || ev.Delta.Type != ir.BlockToolUse {
			return nil
		}
		_, err := rs.openTool(ev.Index, ev.Delta.ToolID, ev.Delta.ToolName)
		return err
	case ir.EventContentDelta:
		return rs.delta(ev)
	case ir.EventBlockStop:
		return rs.closeItem(ev.Index)
	case ir.EventMessageDelta:
		if ev.Usage != nil {
			rs.acc.Usage = *ev.Usage
		}
		return nil
	case ir.EventMessageStop:
		// Close the items but do not finish: the usage chunk arrives after the
		// finish chunk on every OpenAI-compatible upstream, and Darkrouter
		// always requests it. Completing here would report zero usage.
		rs.acc.StopReason = ev.StopReason
		rs.stopped = true
		return rs.closeAll()
	default:
		return nil
	}
}

func (rs *responsesStream) ensureStarted() error {
	if rs.started {
		return nil
	}
	rs.started = true
	if rs.id == "" {
		rs.id = responsesID(rs.acc.ID)
	}
	if err := rs.send("response.created", map[string]any{
		"response": responsesBody(rs.id, &rs.acc, "in_progress", "", rs.echo),
	}); err != nil {
		return err
	}
	return rs.send("response.in_progress", map[string]any{
		"response": responsesBody(rs.id, &rs.acc, "in_progress", "", rs.echo),
	})
}

func (rs *responsesStream) delta(ev ir.StreamEvent) error {
	if ev.Delta == nil {
		return nil
	}
	switch ev.Delta.Type {
	case ir.BlockText:
		if ev.Delta.Text == "" {
			return nil
		}
		it, err := rs.openMessage(ev.Index)
		if err != nil {
			return err
		}
		it.text.WriteString(ev.Delta.Text)
		rs.acc.Content[it.index].Text = it.text.String()
		return rs.send("response.output_text.delta", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "content_index": 0,
			"delta": ev.Delta.Text, "logprobs": []any{},
		})
	case ir.BlockThinking:
		if ev.Delta.Thinking == "" {
			return nil
		}
		it, err := rs.openReasoning(ev.Index)
		if err != nil {
			return err
		}
		it.text.WriteString(ev.Delta.Thinking)
		rs.acc.Content[it.index].Thinking.Text = it.text.String()
		return rs.send("response.reasoning_summary_text.delta", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "summary_index": 0,
			"delta": ev.Delta.Thinking,
		})
	case ir.BlockToolUse:
		if ev.Delta.ToolInput == "" {
			return nil
		}
		// A provider that streams arguments without ever opening the block
		// still has to reach the client, so the item is opened here rather
		// than dropping the call.
		it, err := rs.openTool(ev.Index, ev.Delta.ToolID, ev.Delta.ToolName)
		if err != nil {
			return err
		}
		it.text.WriteString(ev.Delta.ToolInput)
		rs.acc.Content[it.index].ToolUse.Input = json.RawMessage(it.text.String())
		return rs.send("response.function_call_arguments.delta", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "delta": ev.Delta.ToolInput,
		})
	default:
		return nil
	}
}

// claim allocates an output index and appends the matching accumulator block.
// The two must stay in step: responsesBody derives an item's id from its
// position in the final output array, and a delta's item_id has to match it or
// the client drops the text it assembled.
func (rs *responsesStream) claim(kind, prefix string, at int, blk ir.ContentBlock) *responsesItem {
	it := &responsesItem{index: rs.next, kind: kind}
	it.itemID = responsesItemID(rs.id, prefix, it.index)
	rs.next++
	rs.open[at] = it
	rs.acc.Content = append(rs.acc.Content, blk)
	return it
}

func (rs *responsesStream) openMessage(block int) (*responsesItem, error) {
	if it, ok := rs.open[block]; ok {
		return it, nil
	}
	if err := rs.ensureStarted(); err != nil {
		return nil, err
	}
	it := rs.claim("message", "msg", block, ir.ContentBlock{Type: ir.BlockText})
	if err := rs.send("response.output_item.added", map[string]any{
		"output_index": it.index,
		"item": map[string]any{
			"type": "message", "id": it.itemID, "status": "in_progress",
			"role": "assistant", "content": []any{},
		},
	}); err != nil {
		return nil, err
	}
	return it, rs.send("response.content_part.added", map[string]any{
		"item_id": it.itemID, "output_index": it.index, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

func (rs *responsesStream) openReasoning(block int) (*responsesItem, error) {
	if it, ok := rs.open[block]; ok {
		return it, nil
	}
	if err := rs.ensureStarted(); err != nil {
		return nil, err
	}
	it := rs.claim("reasoning", "rs", block,
		ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{}})
	if err := rs.send("response.output_item.added", map[string]any{
		"output_index": it.index,
		"item": map[string]any{
			"type": "reasoning", "id": it.itemID, "summary": []any{},
		},
	}); err != nil {
		return nil, err
	}
	return it, rs.send("response.reasoning_summary_part.added", map[string]any{
		"item_id": it.itemID, "output_index": it.index, "summary_index": 0,
		"part": map[string]any{"type": "summary_text", "text": ""},
	})
}

func (rs *responsesStream) openTool(block int, callID, name string) (*responsesItem, error) {
	if it, ok := rs.open[block]; ok {
		return it, nil
	}
	if err := rs.ensureStarted(); err != nil {
		return nil, err
	}
	it := rs.claim("function_call", "fc", block, ir.ContentBlock{
		Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: callID, Name: name},
	})
	return it, rs.send("response.output_item.added", map[string]any{
		"output_index": it.index,
		"item": map[string]any{
			"type": "function_call", "id": it.itemID, "call_id": callID,
			"name": name, "arguments": "", "status": "in_progress",
		},
	})
}

func (rs *responsesStream) closeItem(block int) error {
	it, ok := rs.open[block]
	if !ok {
		return nil
	}
	delete(rs.open, block)
	text := it.text.String()

	switch it.kind {
	case "message":
		part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
		if err := rs.send("response.output_text.done", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "content_index": 0,
			"text": text, "logprobs": []any{},
		}); err != nil {
			return err
		}
		if err := rs.send("response.content_part.done", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "content_index": 0,
			"part": part,
		}); err != nil {
			return err
		}
		return rs.send("response.output_item.done", map[string]any{
			"output_index": it.index,
			"item": map[string]any{
				"type": "message", "id": it.itemID, "status": "completed",
				"role": "assistant", "content": []any{part},
			},
		})
	case "reasoning":
		summary := map[string]any{"type": "summary_text", "text": text}
		if err := rs.send("response.reasoning_summary_text.done", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "summary_index": 0, "text": text,
		}); err != nil {
			return err
		}
		if err := rs.send("response.reasoning_summary_part.done", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "summary_index": 0, "part": summary,
		}); err != nil {
			return err
		}
		return rs.send("response.output_item.done", map[string]any{
			"output_index": it.index,
			"item": map[string]any{
				"type": "reasoning", "id": it.itemID, "summary": []any{summary},
			},
		})
	default:
		args := text
		if args == "" {
			args = "{}"
		}
		tu := rs.acc.Content[it.index].ToolUse
		if err := rs.send("response.function_call_arguments.done", map[string]any{
			"item_id": it.itemID, "output_index": it.index, "arguments": args,
		}); err != nil {
			return err
		}
		return rs.send("response.output_item.done", map[string]any{
			"output_index": it.index,
			"item": map[string]any{
				"type": "function_call", "id": it.itemID, "call_id": tu.ID,
				"name": tu.Name, "arguments": args, "status": "completed",
			},
		})
	}
}

// closeAll closes whatever the provider left open, in output order so the
// events are deterministic.
func (rs *responsesStream) closeAll() error {
	blocks := make([]int, 0, len(rs.open))
	for b := range rs.open {
		blocks = append(blocks, b)
	}
	sort.Ints(blocks)
	for _, b := range blocks {
		if err := rs.closeItem(b); err != nil {
			return err
		}
	}
	return nil
}

func (rs *responsesStream) complete() error {
	if rs.completed {
		return nil
	}
	if err := rs.ensureStarted(); err != nil {
		return err
	}
	// A client does not treat an item as final until output_item.done, so an
	// item still open here would leave it waiting forever.
	if err := rs.closeAll(); err != nil {
		return err
	}
	rs.completed = true
	status, incomplete := responsesStatus(rs.acc.StopReason)
	// The terminal event name follows the status. response.completed always
	// carries status "completed"; a truncated or filtered answer ends with
	// response.incomplete, and a client switching on the event type would
	// otherwise treat a half answer as a whole one.
	name := "response.completed"
	if status == "incomplete" {
		name = "response.incomplete"
	}
	return rs.send(name, map[string]any{
		"response": responsesBody(rs.id, &rs.acc, status, incomplete, rs.echo),
	})
}

// fail ends a stream the provider could not finish. The client has already
// received content and cannot be given a different response, so the least
// wrong thing is to tell it plainly that this one did not complete.
func (rs *responsesStream) fail(err error) error {
	// A reader error after the terminal event — trailing bytes, a reset after
	// [DONE] went out — must not produce a second terminal event.
	if rs.completed {
		return nil
	}
	var e *ir.Error
	if !errors.As(err, &e) {
		e = &ir.Error{Type: ir.ErrAPI, Message: err.Error()}
	}
	if serr := rs.ensureStarted(); serr != nil {
		return serr
	}
	if serr := rs.closeAll(); serr != nil {
		return serr
	}
	rs.completed = true
	body := responsesBody(rs.id, &rs.acc, "failed", "", rs.echo)
	body["error"] = map[string]any{"code": string(e.Type), "message": e.Message}
	return rs.send("response.failed", map[string]any{"response": body})
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package.

- [ ] **Step 5: Read the emitted stream rather than only asserting on it**

Add a temporary `t.Log("\n" + w.Body.String())` inside `respEvents`, run

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ -run TestResponsesStreamEmitsTheTextLifecycle -v
```

and **read the raw SSE frames it prints**: confirm each frame has an `event:` line whose name matches its `data.type`, that sequence numbers run 0..n with no gaps, and that the body ends with a blank line after `response.completed`. Remove the `t.Log` before committing. Asserting on parsed maps cannot catch a malformed frame boundary, which is exactly what breaks a real client.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/edge/openai/responses_stream.go internal/edge/openai/responses_stream_test.go
git commit -m "feat(edge): write the responses semantic stream"
```

---

### Task 29: The Responses dialect and route

**Files:**
- Modify: `internal/edge/openai/dialect.go`, `internal/exec/surface.go`, `internal/server/server.go`
- Test: `internal/edge/openai/dialect_test.go`, `internal/exec/responses_test.go`

**Interfaces:**
- Consumes: `ParseResponses` (Task 25), `WriteResponses` (Task 26), `WriteResponsesStream` (Task 28).
- Produces: `openai.NewResponses() *ResponsesDialect` satisfying `edge.Dialect`, and the `POST /v1/responses` route.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: `Dialect` in the same file is the pattern and the three writers are already built.

Responses is chat-shaped, so it becomes a **fourth `edge.Dialect`** rather than a seventh `SurfaceOp`. The route is then one line — `s.ex.Handle(w, r, rd)` — and Responses inherits the attempt loop, the commit rule, the budget gate and the request log without a word of new executor code. That is the whole payoff of Task 6's extraction arriving on the surface that needed it least and benefits most.

`Name()` is `"openai-responses"` rather than `"openai"`. The request row's `dialect` column is how an operator tells a Responses client from a chat client, and both speak OpenAI over the same auth.

**A fresh dialect per request, and that is not incidental.** `ParseRequest` produces the echo the writers need, so the value has to hold state between them. `Handle` calls both on the same instance within one request, so a per-request value is correct and a route-scoped one is a data race under concurrency. `authed` keeps a shared instance because `ProxyToken` and `WriteError` touch no state.

**One thing has to be fixed here or the parse warnings vanish.** `chatOp.Build` returns only the adapter's warnings. Task 25's parser is the first inbound parse that produces any, and without appending them the reasoning-item drop is recorded nowhere.

- [ ] **Step 1: Write the failing test**

Add to `internal/edge/openai/dialect_test.go`. It currently imports only `httptest` and `testing`, so add `strings` and `github.com/darkraise/darkrouter/internal/edge`:

```go
func TestResponsesDialectIsDistinguishableInTheLog(t *testing.T) {
	// The dialect column is how an operator tells a Responses client from a
	// chat client, and both speak OpenAI over the same auth.
	if got := NewResponses().Name(); got != "openai-responses" {
		t.Errorf("Name() = %q", got)
	}
	if NewResponses().Name() == New().Name() {
		t.Error("the two OpenAI dialects are indistinguishable in the request log")
	}
}

func TestResponsesDialectReadsTheSameBearer(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	r.Header.Set("Authorization", "bearer sk-x")
	if got := NewResponses().ProxyToken(r); got != "sk-x" {
		t.Errorf("ProxyToken = %q", got)
	}
}

func TestResponsesDialectSatisfiesEdgeDialect(t *testing.T) {
	var _ edge.Dialect = NewResponses()
}

func TestEachResponsesDialectHoldsItsOwnEcho(t *testing.T) {
	// A route-scoped instance would race on this field and answer one client
	// with another's tools.
	a, b := NewResponses(), NewResponses()
	if _, _, err := a.ParseRequest(httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"m","input":"hi","instructions":"first"}`)), 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.ParseRequest(httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"m","input":"hi","instructions":"second"}`)), 1<<20); err != nil {
		t.Fatal(err)
	}
	if a.echo == nil || b.echo == nil || a.echo.Instructions != "first" {
		t.Errorf("first dialect echo = %+v", a.echo)
	}
	if b.echo.Instructions != "second" {
		t.Errorf("second dialect echo = %+v", b.echo)
	}
}
```

Create `internal/exec/responses_test.go`:

```go
package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestAStatelessResponsesRequestServes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","model":"gpt-4o","choices":[
		  {"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],
		  "usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "gpt-4o", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.Handle(w, httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"gpt-4o","input":"hi"}`)), openaiedge.NewResponses())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Object     string `json:"object"`
		Status     string `json:"status"`
		OutputText string `json:"output_text"`
		Store      bool   `json:"store"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "response" || body.Status != "completed" || body.OutputText != "hello" {
		t.Errorf("body = %s", w.Body.String())
	}
	if body.Store {
		t.Error("store = true; the id is not resumable and the client must be told")
	}
	got := rec.only(t)
	if got.Dialect != "openai-responses" {
		t.Errorf("dialect = %q", got.Dialect)
	}
	if got.Surface != "llm" || got.Status != "success" || got.TokensIn != 3 {
		t.Errorf("record = %+v", got)
	}
}

func TestAStatefulResponsesRequestIsRejectedAndLogged(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a stateful request reached an upstream")
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "gpt-4o", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.Handle(w, httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"gpt-4o","input":"hi","previous_response_id":"resp_dr_x"}`)),
		openaiedge.NewResponses())

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "previous_response_id") {
		t.Errorf("body = %s; the error must name what was refused", w.Body.String())
	}
	got := rec.only(t)
	if got.ErrorCode != string(ir.ErrInvalidRequest) || len(got.Attempts) != 0 {
		t.Errorf("record = %+v", got)
	}
}

func TestAResponsesRequestStreamsSemanticEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, chunk := range []string{
			`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
			`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"he"}}]}`,
			`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"llo"}}]}`,
			`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		} {
			_, _ = w.Write([]byte("data: " + chunk + "\n\n"))
			f.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer upstream.Close()

	e, _ := executorForOp(t, upstream.URL, catalogWith("p", "gpt-4o", ir.SurfaceLLM))
	w := httptest.NewRecorder()
	e.Handle(w, httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"gpt-4o","input":"hi","stream":true}`)), openaiedge.NewResponses())

	body := w.Body.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.output_item.added",
		"event: response.output_text.delta",
		"event: response.output_item.done",
		"event: response.completed",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stream is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "[DONE]") {
		t.Errorf("the chat sentinel leaked into a responses stream:\n%s", body)
	}
	if !strings.Contains(body, `"delta":"he"`) || !strings.Contains(body, `"delta":"llo"`) {
		t.Errorf("the deltas did not reach the client:\n%s", body)
	}
}

func TestAResponsesParseWarningReachesTheRequestRow(t *testing.T) {
	// The dropped reasoning item is invisible in the response body, so the
	// request row is the only place it can be seen.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","model":"m","choices":[
		  {"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "gpt-4o", ir.SurfaceLLM))
	e.Handle(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"gpt-4o","input":[
		  {"type":"reasoning","id":"rs_1","summary":[]},{"role":"user","content":"hi"}]}`)),
		openaiedge.NewResponses())

	got := rec.only(t)
	if len(got.Warnings) == 0 ||
		!strings.Contains(strings.Join(got.Warnings, " "), "reasoning") {
		t.Errorf("warnings = %v", got.Warnings)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ ./internal/exec/ -run 'Responses' -v
```

Expected: FAIL to build — `undefined: NewResponses`.

- [ ] **Step 3: Add the dialect**

Append to `internal/edge/openai/dialect.go`:

```go
// ResponsesDialect serves /v1/responses. It is a fourth edge.Dialect rather
// than a seventh SurfaceOp because Responses is chat-shaped: making it a
// dialect is what lets it inherit the attempt loop, the commit rule, the budget
// gate and the request log without a word of new executor code.
// ResponsesDialect is constructed per request, not once per route.
//
// The response object has to echo the request — tools, tool_choice and
// parallel_tool_calls are required fields with no default in the OpenAI SDK's
// model — and only ParseRequest sees the request while only the writers build
// the response. A route-scoped instance would have to hold that echo across
// concurrent requests, which is a data race and a wrong answer. Gemini's
// NewFor(r) already establishes this shape for the same reason.
type ResponsesDialect struct {
	echo *responsesEcho
}

func NewResponses() *ResponsesDialect { return &ResponsesDialect{} }

// Name is not "openai". The request row's dialect column is how an operator
// tells a Responses client from a chat client, and both speak OpenAI over the
// same auth.
func (d *ResponsesDialect) Name() string { return "openai-responses" }

func (d *ResponsesDialect) ProxyToken(r *http.Request) string {
	return (&Dialect{}).ProxyToken(r)
}

func (d *ResponsesDialect) ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error) {
	req, pt, echo, err := ParseResponses(r, maxBody)
	// Held for the writers. Safe because this value serves exactly one request.
	d.echo = echo
	return req, pt, err
}

func (d *ResponsesDialect) WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	return WriteResponses(w, resp, d.echo)
}

func (d *ResponsesDialect) WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error {
	return WriteResponsesStream(w, events, d.echo)
}

// WriteError is chat's. The Responses error body has the same shape, and
// duplicating it would be two places to keep in step for no difference.
func (d *ResponsesDialect) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return WriteError(w, e)
}

var _ edge.Dialect = (*ResponsesDialect)(nil)
```

- [ ] **Step 4: Carry the inbound parse warnings**

In `internal/exec/surface.go`, `chatOp.Build` returns only the adapter's warnings. Append the request's:

```go
func (o *chatOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	hr, warns, err := ad.BuildRequest(ctx, tgt, o.req)
	// The inbound parse's losses travel with the outbound ones. Until phase 5
	// no dialect produced any, so nothing carried them and the responses
	// parser's dropped reasoning item would have been recorded nowhere.
	return hr, append(warns, o.req.Warnings...), err
}
```

- [ ] **Step 5: Wire the route**

In `internal/server/server.go`, beside the chat route:

```go
	// One shared instance for the auth check, which is stateless, and a fresh
	// one per request for the handler, which holds the response echo. Sharing
	// the handler's instance across requests would race on that field.
	rdAuth := openaiedge.NewResponses()
	mux.HandleFunc("POST /v1/responses", s.authed(rdAuth, func(w http.ResponseWriter, r *http.Request) {
		s.ex.Handle(w, r, openaiedge.NewResponses())
	}))
```

- [ ] **Step 6: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/openai/ ./internal/exec/ ./internal/server/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in all three packages.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/edge/openai/dialect.go internal/edge/openai/dialect_test.go \
        internal/exec/surface.go internal/exec/responses_test.go internal/server/server.go
git commit -m "feat(server): serve the responses API"
```

---

### Task 30: Migration 0003 and the surface log columns

**Files:**
- Create: `internal/store/migrations/0003_surfaces.sql`
- Modify: `internal/store/log.go`
- Test: `internal/store/log_test.go`, `internal/store/migrate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `store.RequestRecord.SurfaceMeta map[string]any`, `.ResponseBytes int64`, `.ResponseContentType string`, and the three columns behind them. Task 31 fills them.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 2: `0002_catalog.sql` is the pattern, the loader asserts contiguity already, and the three columns are stated below.

Risk is 3 because this is a schema migration against a database that already holds production rows. Two things make it safe and both are load-bearing: every column is added with a non-null default, which is what `ALTER TABLE ADD COLUMN` requires on a `STRICT` table and what keeps existing rows valid; and nothing is dropped or rebuilt, so a failed run leaves the phase 2 schema exactly as it was.

**One JSON column, not nine.** Spec §9 asks for input item count and dimensions on embeddings, image count and size on images, duration and voice on audio, document count on rerank. **No two surfaces share a single one of those fields.** Nine columns would be nine mostly-NULL columns, and the tenth surface would mean a tenth migration. `candidates_json` beside it is the precedent for exactly this shape, and SQLite's `json_extract` keeps the column queryable for phase 7's trace view.

**Two things do get real columns**, because they apply to every surface and one of them is load-bearing. Spec §7: a truncated binary body cannot be signalled in-band and there is no in-stream error vocabulary for audio, so **the byte count on the request row is the only place the truncation appears**. A field buried in a JSON blob is not where an operator finds that. Content type sits beside it because it is what tells a reader whether a row is audio, a subtitle file or JSON at all.

- [ ] **Step 1: Write the failing test**

Add to `internal/store/log_test.go`:

```go
func TestRequestRowCarriesSurfaceDetail(t *testing.T) {
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{})
	rec := &RequestRecord{
		ID: "r1", TS: time.Now(), Dialect: "openai", Surface: "embedding",
		RequestedModel: "e5", Status: "success",
		SurfaceMeta: map[string]any{"input_count": 3, "dimensions": 256},
	}
	if _, err := w.writeBatch(context.Background(), []*RequestRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.Read.QueryRow(
		`SELECT surface_meta_json FROM requests WHERE id = 'r1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("surface_meta_json is not JSON: %q", raw)
	}
	if got["input_count"].(float64) != 3 || got["dimensions"].(float64) != 256 {
		t.Errorf("surface meta = %v", got)
	}
}

func TestARecordWithNoSurfaceDetailStoresAnEmptyObject(t *testing.T) {
	// The column is NOT NULL. A nil map must encode as {} rather than null, or
	// every chat row fails the insert.
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{})
	rec := &RequestRecord{
		ID: "r2", TS: time.Now(), Dialect: "openai", Surface: "llm",
		RequestedModel: "m", Status: "success",
	}
	if _, err := w.writeBatch(context.Background(), []*RequestRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.Read.QueryRow(
		`SELECT surface_meta_json FROM requests WHERE id = 'r2'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "{}" {
		t.Errorf("surface_meta_json = %q, want {}", raw)
	}
}

func TestRequestRowCarriesResponseSizeAndType(t *testing.T) {
	// Spec §7: a truncated audio body cannot be signalled in-band, so this is
	// the only place the truncation appears.
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{})
	rec := &RequestRecord{
		ID: "r3", TS: time.Now(), Dialect: "openai", Surface: "tts",
		RequestedModel: "tts-1", Status: "success",
		ResponseBytes: 204800, ResponseContentType: "audio/mpeg",
	}
	if _, err := w.writeBatch(context.Background(), []*RequestRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var n int64
	var ct string
	if err := db.Read.QueryRow(
		`SELECT response_bytes, response_content_type FROM requests WHERE id = 'r3'`).
		Scan(&n, &ct); err != nil {
		t.Fatal(err)
	}
	if n != 204800 || ct != "audio/mpeg" {
		t.Errorf("bytes = %d, content type = %q", n, ct)
	}
}

func TestSurfaceDetailIsQueryable(t *testing.T) {
	// A JSON column is only defensible if phase 7 can filter on it. This is
	// the assertion that keeps it defensible.
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{})
	if _, err := w.writeBatch(context.Background(), []*RequestRecord{
		{ID: "a", TS: time.Now(), Dialect: "openai", Surface: "image",
			RequestedModel: "m", Status: "success",
			SurfaceMeta: map[string]any{"image_count": 4}},
		{ID: "b", TS: time.Now(), Dialect: "openai", Surface: "image",
			RequestedModel: "m", Status: "success",
			SurfaceMeta: map[string]any{"image_count": 1}},
	}); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.Read.QueryRow(
		`SELECT id FROM requests WHERE json_extract(surface_meta_json, '$.image_count') > 2`).
		Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != "a" {
		t.Errorf("id = %q", id)
	}
}
```

Add `"encoding/json"` and `"context"` to the imports if absent. The names above are the real ones and were checked against the tree: the batch helper is `migrated(t)` from `migrate_test.go` (not `testDB`); the ctx-taking, `(int, error)`-returning writer is `(*LogWriter).writeBatch` (`flush` is the timer-driven wrapper, `func(batch *[]*RequestRecord)`, with no context and no returns); and `DB` exposes `Read`, `Write` and `Sync` as fields rather than an accessor method.

Add to `internal/store/migrate_test.go`:

```go
func TestMigrationsReachVersionThree(t *testing.T) {
	// The loader asserts contiguity from 1, so a mis-numbered file fails here
	// rather than at a customer's first start.
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 3 {
		t.Fatalf("loaded %d migrations, want 3", len(ms))
	}
}

func TestMigrationThreeIsAdditive(t *testing.T) {
	// Nothing is dropped or rebuilt, so a failed run leaves the phase 2 schema
	// exactly as it was. Every added column carries a non-null default, which
	// ALTER TABLE ADD COLUMN requires on a STRICT table.
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToUpper(ms[2].sql)
	for _, forbidden := range []string{"DROP TABLE", "DROP COLUMN", "DELETE FROM"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("migration 3 contains %q", forbidden)
		}
	}
	if strings.Count(body, "ADD COLUMN") != 3 {
		t.Errorf("migration 3 adds %d columns, want 3", strings.Count(body, "ADD COLUMN"))
	}
}
```

The field is the unexported `sql`, which a same-package test can read; `loadMigrations` is the real function name.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run 'TestRequestRowCarries|TestSurfaceDetail|TestMigration|TestARecordWithNo' -v
```

Expected: FAIL to build on `rec.SurfaceMeta`, and `TestMigrationsReachVersionThree` reporting 2.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/0003_surfaces.sql`:

```sql
-- Phase 5 schema, per spec section 9.
--
-- Additive only: every column carries a non-null default, which is what
-- ALTER TABLE ADD COLUMN requires on a STRICT table and what keeps the rows
-- already in the table valid. Nothing is dropped or rebuilt, so a failed run
-- leaves the phase 2 schema exactly as it was.
--
-- surface_meta_json is one column rather than nine because no two surfaces
-- share a field: embeddings record an input count and a dimension count,
-- images a count and a size, audio a duration and a voice, rerank a document
-- count. Nine mostly-NULL columns would also mean a tenth migration for a
-- tenth surface. candidates_json is the precedent, and json_extract keeps the
-- column queryable for phase 7's trace view.
ALTER TABLE requests ADD COLUMN surface_meta_json TEXT NOT NULL DEFAULT '{}';

-- These two are real columns because they apply to every surface, and because
-- spec section 7 makes the byte count load-bearing: a provider that returns a
-- fast 200 and then truncates a binary body cannot be failed over and there is
-- no in-stream error vocabulary to warn the client, so this is the only place
-- the truncation is recorded. A field inside a JSON blob is not where an
-- operator finds that.
ALTER TABLE requests ADD COLUMN response_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE requests ADD COLUMN response_content_type TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 4: Extend the record and the insert**

In `internal/store/log.go`, add to `RequestRecord` after `Warnings`:

```go
	// SurfaceMeta is the surface-specific detail spec §9 asks for: input count
	// and dimensions for embeddings, image count and size, audio duration and
	// voice, document count for rerank. One JSON column rather than nine,
	// because no two surfaces share a field.
	SurfaceMeta map[string]any

	// ResponseBytes and ResponseContentType apply to every surface. Spec §7:
	// a truncated binary body cannot be signalled in-band, so the byte count on
	// this row is the only place the truncation appears.
	ResponseBytes       int64
	ResponseContentType string
```

Extend the `INSERT` in **`writeBatch`** — not in `flush`, which only drains the channel — with the three columns and three placeholders, and in `insertOne`:

```go
	// The column is NOT NULL and defaults to an empty object, so a nil map must
	// encode as {} rather than null — otherwise every chat row fails to insert.
	meta := r.SurfaceMeta
	if meta == nil {
		meta = map[string]any{}
	}
	surfaceMeta, err := json.Marshal(meta)
	if err != nil {
		return err
	}
```

and append `string(surfaceMeta), r.ResponseBytes, r.ResponseContentType` to the `ExecContext` argument list, in the same order as the column list.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package.

- [ ] **Step 6: Verify the migration against a phase 2 database, not only a fresh one**

A fresh database is created by running every migration in order, so a fresh-start test never exercises the upgrade path that production takes.

`sqlite3` is **not installed on this machine** — checked. Write a one-off query tool once and reuse it here and in Task 34:

```bash
export PATH=$PATH:/usr/local/go/bin
mkdir -p /tmp/dbq
cat > /tmp/dbq/main.go <<'EOF'
// Command dbq prints the rows of one query. It exists because sqlite3 is not
// installed and the verification steps have to read the database.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "file:"+os.Args[1]+"?mode=ro")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	rows, err := db.Query(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	fmt.Println(strings.Join(cols, " | "))
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			panic(err)
		}
		out := make([]string, len(vals))
		for i, v := range vals {
			out[i] = fmt.Sprintf("%v", v)
		}
		fmt.Println(strings.Join(out, " | "))
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
}
EOF
```

Run it from the repository root so it resolves `modernc.org/sqlite` from this module: `go run /tmp/dbq/main.go <db-path> "<query>"`.

Now exercise the **upgrade** path, which a fresh database never takes:

```bash
export PATH=$PATH:/usr/local/go/bin
export DARKROUTER_MASTER_KEY=throwaway-verify-key
rm -rf /tmp/dr-migrate && mkdir -p /tmp/dr-migrate
sed -e 's/:8080/:18080/' -e 's/:8081/:18081/' darkrouter.example.yaml > /tmp/dr-migrate/dr.yaml

# Build once WITHOUT the new migration, so the database lands at version 2.
mv internal/store/migrations/0003_surfaces.sql /tmp/dr-migrate/0003.sql.hold
go build -o /tmp/dr-migrate/dr-v2 ./cmd/darkrouter
mv /tmp/dr-migrate/0003.sql.hold internal/store/migrations/0003_surfaces.sql
go build -o /tmp/dr-migrate/dr-v3 ./cmd/darkrouter

/tmp/dr-migrate/dr-v2 -config /tmp/dr-migrate/dr.yaml -db /tmp/dr-migrate/dr.db >/tmp/dr-migrate/v2.log 2>&1 &
sleep 2; kill "$(ps -C dr-v2 -o pid= | head -1)" 2>/dev/null; sleep 1
go run /tmp/dbq/main.go /tmp/dr-migrate/dr.db "SELECT version FROM schema_version"

/tmp/dr-migrate/dr-v3 -config /tmp/dr-migrate/dr.yaml -db /tmp/dr-migrate/dr.db >/tmp/dr-migrate/v3.log 2>&1 &
sleep 2; kill "$(ps -C dr-v3 -o pid= | head -1)" 2>/dev/null; sleep 1
go run /tmp/dbq/main.go /tmp/dr-migrate/dr.db "SELECT version FROM schema_version"
go run /tmp/dbq/main.go /tmp/dr-migrate/dr.db "SELECT surface_meta_json, response_bytes, response_content_type FROM requests LIMIT 0"
ps -C dr-v2 -o pid= ; ps -C dr-v3 -o pid= ; echo "both stopped if nothing printed above"
```

Expected: `schema_version` holds `2` after the first run and `3` after the second — it is a **single row that is UPDATEd**, not one row per migration, and the table is `schema_version`, not `schema_migrations`. The `LIMIT 0` query prints the three new column names and no rows, which is how the columns are confirmed without needing `.schema`.

Three details that will otherwise waste time: the flags are `-config` and `-db`, and `-db` names the **database file**, not a directory; `darkrouter.example.yaml` binds 8080/8081, which belong to an unrelated application, hence the `sed`; and the migration file is untracked when this step runs, so `git stash push` would fail with "did not match any file(s) known to git" — `mv` it aside instead.

Each binary is backgrounded and killed **by its own distinct name**. `nohup … &` inside a compound command returns the subshell's pid, not the binary's, and the distinct names mean this never touches another `darkrouter` process.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/store/migrations/0003_surfaces.sql internal/store/log.go \
        internal/store/log_test.go internal/store/migrate_test.go
git commit -m "feat(store): record surface detail on the request row"
```

---

### Task 31: Each surface records its own detail

**Files:**
- Modify: `internal/exec/embed.go`, `internal/exec/image.go`, `internal/exec/rerank.go`, `internal/exec/transcription.go`, `internal/exec/speech.go`, `internal/exec/moderation.go`
- Test: `internal/exec/surfacemeta_test.go`

**Interfaces:**
- Consumes: `store.RequestRecord.SurfaceMeta`, `.ResponseBytes`, `.ResponseContentType` (Task 30); `CommitWriter.Bytes()` (Task 5).
- Produces: nothing new.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: the fields per surface are enumerated by spec §9 and listed below, and each is two lines in a `Respond` that already exists.

Spec §9 names what each surface records. The values come from the request for anything the client asked for and from the response for anything the provider reported, which is why they are assigned in `Respond` rather than at parse time — a request that never reached a provider should not claim an image count it never generated.

| Surface | `surface_meta_json` | `response_bytes` | `response_content_type` |
|---|---|---|---|
| embedding | `input_count`, `dimensions`, `encoding` | — | — |
| image | `image_count`, `size`, `quality` | — | — |
| rerank | `document_count`, `top_n` | — | — |
| moderation | `input_count`, `flagged_count` | — | — |
| stt | `file_name` | yes | yes |
| tts | `voice`, `response_format` | yes | yes |

`ResponseBytes` is taken from `cw.Bytes()` **after** the copy, which is the wrapper's count of what actually reached the client rather than what the provider claimed to send. That distinction is the point: a truncated audio body has a byte count lower than the provider's `Content-Length`, and this is where that shows up.

- [ ] **Step 1: Write the failing test**

Create `internal/exec/surfacemeta_test.go`:

```go
package exec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestEmbeddingsRecordTheirInputCount(t *testing.T) {
	upstream := httptest.NewServer(embedUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "e5", ir.SurfaceEmbedding))
	e.HandleEmbeddings(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"e5","input":["a","b","c"],"dimensions":256,"encoding_format":"base64"}`)),
		openaiedge.New())

	got := rec.only(t).SurfaceMeta
	if got["input_count"] != 3 || got["dimensions"] != 256 || got["encoding"] != "base64" {
		t.Errorf("surface meta = %v", got)
	}
}

func TestImagesRecordTheirCountAndSize(t *testing.T) {
	upstream := httptest.NewServer(imageUpstream(true))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "gpt-image-1", ir.SurfaceImage))
	e.HandleImages(httptest.NewRecorder(),
		httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(
			`{"model":"gpt-image-1","prompt":"a cat","n":2,"size":"1024x1024","quality":"high"}`)),
		openaiedge.New())

	got := rec.only(t).SurfaceMeta
	if got["image_count"] != 2 || got["size"] != "1024x1024" || got["quality"] != "high" {
		t.Errorf("surface meta = %v", got)
	}
}

func TestRerankRecordsItsDocumentCount(t *testing.T) {
	upstream := httptest.NewServer(rerankUpstream(nil))
	defer upstream.Close()

	e, rec := executorForPreset(t, upstream.URL, "cohere",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	e.HandleRerank(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/rerank",
		strings.NewReader(`{"model":"rerank-v3.5","query":"q","documents":["a","b"],"top_n":1}`)),
		openaiedge.New())

	got := rec.only(t).SurfaceMeta
	if got["document_count"] != 2 || got["top_n"] != 1 {
		t.Errorf("surface meta = %v", got)
	}
}

func TestModerationsRecordTheirFlaggedCount(t *testing.T) {
	upstream := httptest.NewServer(moderationUpstream())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "omni", ir.SurfaceModeration))
	e.HandleModerations(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/moderations",
		strings.NewReader(`{"model":"omni","input":["a","b"]}`)), openaiedge.New())

	got := rec.only(t).SurfaceMeta
	if got["input_count"] != 2 || got["flagged_count"] != 0 {
		t.Errorf("surface meta = %v", got)
	}
}

func TestSpeechRecordsWhatActuallyReachedTheClient(t *testing.T) {
	// Not the provider's Content-Length. A truncated body has a lower count,
	// and spec §7 makes this the only place that appears.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "tts-1", ir.SurfaceTTS))
	e.HandleSpeech(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/audio/speech",
		strings.NewReader(`{"model":"tts-1","input":"hi","voice":"alloy","response_format":"mp3"}`)),
		openaiedge.New())

	got := rec.only(t)
	if got.ResponseBytes != 10 {
		t.Errorf("response bytes = %d, want 10", got.ResponseBytes)
	}
	if got.ResponseContentType != "audio/mpeg" {
		t.Errorf("content type = %q", got.ResponseContentType)
	}
	if got.SurfaceMeta["voice"] != "alloy" || got.SurfaceMeta["response_format"] != "mp3" {
		t.Errorf("surface meta = %v", got.SurfaceMeta)
	}
}

func TestTranscriptionsRecordTheirContentTypeAndSize(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("hello there"))
	}))
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "whisper-1", ir.SurfaceSTT))
	e.HandleTranscriptions(httptest.NewRecorder(), transcriptionRequest(t, "whisper-1"), openaiedge.New())

	got := rec.only(t)
	if got.ResponseBytes != 11 {
		t.Errorf("response bytes = %d, want 11", got.ResponseBytes)
	}
	if !strings.HasPrefix(got.ResponseContentType, "text/plain") {
		t.Errorf("content type = %q", got.ResponseContentType)
	}
	if got.SurfaceMeta["file_name"] != "a.mp3" {
		t.Errorf("surface meta = %v", got.SurfaceMeta)
	}
}

func TestAChatRequestRecordsNoSurfaceDetail(t *testing.T) {
	// The column defaults to {}. A chat row inventing keys would make the
	// trace view show fields that mean nothing for that surface.
	upstream := httptest.NewServer(jsonOK())
	defer upstream.Close()

	e, rec := executorForOp(t, upstream.URL, catalogWith("p", "m", ir.SurfaceLLM))
	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceLLM}}
	e.RunSurface(httptest.NewRecorder(), httptest.NewRequest("POST", "/probe", nil), op)

	if got := rec.only(t).SurfaceMeta; len(got) != 0 {
		t.Errorf("surface meta = %v, want empty", got)
	}
}
```

Add `"github.com/darkraise/darkrouter/internal/router"` to the imports.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'RecordTheir|RecordsIts|RecordsWhat|RecordsNoSurface' -v
```

Expected: FAIL — every `SurfaceMeta` lookup returns nil and `ResponseBytes` is 0.

- [ ] **Step 3: Fill the fields in each op**

In `internal/exec/embed.go`, in `Respond` before `writeDiagnostics`:

```go
	ac.Rec.SurfaceMeta = map[string]any{
		"input_count": o.req.InputCount(),
		"encoding":    o.req.EncodingOrDefault(),
	}
	// Omitted rather than zero: dimensions has no legal zero, so recording one
	// would claim the client asked for a value it did not send.
	if o.req.Dimensions > 0 {
		ac.Rec.SurfaceMeta["dimensions"] = o.req.Dimensions
	}
```

In `internal/exec/image.go`:

```go
	ac.Rec.SurfaceMeta = map[string]any{"image_count": o.req.ImageCount()}
	for k, v := range map[string]string{"size": o.req.Size, "quality": o.req.Quality} {
		if v != "" {
			ac.Rec.SurfaceMeta[k] = v
		}
	}
```

In `internal/exec/rerank.go`:

```go
	ac.Rec.SurfaceMeta = map[string]any{"document_count": o.req.DocumentCount()}
	if o.req.TopN > 0 {
		ac.Rec.SurfaceMeta["top_n"] = o.req.TopN
	}
```

In `internal/exec/moderation.go`, after the parse so the flagged count is real:

```go
	flagged := 0
	for _, r := range out.Results {
		if r.Flagged {
			flagged++
		}
	}
	ac.Rec.SurfaceMeta = map[string]any{
		"input_count": o.req.InputCount(), "flagged_count": flagged,
	}
```

In `internal/exec/speech.go`, after the copy so the count is what reached the client:

```go
	n, err := copyFlushing(cw, resp.Body)
	// cw.Bytes() rather than n or the provider's Content-Length: the wrapper
	// counts what actually reached the client, and a truncated body's count is
	// lower than what the provider claimed. Spec §7 makes this the only place
	// that truncation appears.
	ac.Rec.ResponseBytes = cw.Bytes()
	ac.Rec.ResponseContentType = resp.Header.Get("Content-Type")
	ac.Rec.SurfaceMeta = map[string]any{"voice": o.req.Voice}
	if o.req.ResponseFormat != "" {
		ac.Rec.SurfaceMeta["response_format"] = o.req.ResponseFormat
	}
	_ = n
	if err != nil && !cw.Committed() {
		return adapter.OutcomeRetryableProvider, errorFor(adapter.OutcomeRetryableProvider, err)
	}
	return adapter.OutcomeSuccess, nil
```

Drop the now-unused `n` binding rather than assigning it to `_` if the implementer prefers; `copyFlushing`'s count and `cw.Bytes()` agree on the success path and only `cw.Bytes()` is right on the truncated one, so the wrapper is the one to keep.

In `internal/exec/transcription.go`, on **both** branches:

```go
	ac.Rec.SurfaceMeta = map[string]any{"file_name": o.form.FileName("file")}
	ac.Rec.ResponseContentType = ct
```

and set `ac.Rec.ResponseBytes = cw.Bytes()` after the write on each — after `cw.Write(raw)` on the JSON branch and after `copyFlushing` on the other.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
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
git add internal/exec/embed.go internal/exec/image.go internal/exec/rerank.go \
        internal/exec/moderation.go internal/exec/transcription.go internal/exec/speech.go \
        internal/exec/surfacemeta_test.go
git commit -m "feat(exec): record each surface's own detail"
```

---

### Task 32: Gemini serves embeddings

**Files:**
- Create: `internal/adapter/gemini/embed.go`
- Modify: `internal/adapter/gemini/adapter.go`
- Test: `internal/adapter/gemini/embed_test.go`

**Interfaces:**
- Consumes: `ir.EmbeddingRequest`, `ir.EmbeddingResponse`, `adapter.Embedder` (Task 11), `adapter.SurfaceSet` (Task 7).
- Produces: `gemini.Adapter` implementing `adapter.Embedder` and `adapter.SurfaceProvider`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: `count.go` beside it is the pattern for a second gemini endpoint, and the wire shape is given below.

Spec §4's matrix says gemini serves `embedding` via `embedContent`, and Task 2 widens its preset to declare it. Without this task the adapter never declares the surface, Task 8's filter excludes gemini from every embedding request, and the matrix — which the plan calls the specification — has a cell no step implements. The runtime behavior would be safe but wrong: a `gemini` provider advertising embeddings in the catalog that the router always skips.

**`batchEmbedContents`, not `embedContent`.** The IR request is batched and the single-item endpoint would mean one HTTP call per input — a 96-input batch becoming 96 round trips through the attempt loop, each one separately failable. `batchEmbedContents` is the batched form of the same operation and takes the same per-item body.

Two losses have to be reported rather than hidden:

- **Gemini returns floats only.** A client that asked for `base64` gets floats, which is a different response from the one it asked for, so it warns.
- **Gemini reports no token usage** for embeddings. `Usage` stays zero, which the request row records as unreported rather than free.

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/gemini/embed_test.go`:

```go
package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildEmbeddingRendersTheBatchShape(t *testing.T) {
	hr, warns, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{
			BaseURL: "https://generativelanguage.googleapis.com/v1beta",
			APIKey:  "k", Model: "text-embedding-004",
		},
		&ir.EmbeddingRequest{Input: []string{"a", "b"}, Dimensions: 256})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	want := "https://generativelanguage.googleapis.com/v1beta/models/text-embedding-004:batchEmbedContents"
	if hr.URL.String() != want {
		t.Errorf("url = %s, want %s", hr.URL, want)
	}
	if hr.Header.Get("x-goog-api-key") != "k" {
		t.Errorf("auth header = %q; gemini does not use bearer", hr.Header.Get("x-goog-api-key"))
	}

	var body struct {
		Requests []struct {
			Model   string `json:"model"`
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			OutputDimensionality *int `json:"outputDimensionality"`
		} `json:"requests"`
	}
	raw, _ := io.ReadAll(hr.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Requests) != 2 {
		t.Fatalf("requests = %d, want one per input", len(body.Requests))
	}
	// Every element must repeat the model, prefixed. Gemini rejects the batch
	// without it even though the name is already in the URL.
	if body.Requests[0].Model != "models/text-embedding-004" {
		t.Errorf("requests[0].model = %q", body.Requests[0].Model)
	}
	if body.Requests[1].Content.Parts[0].Text != "b" {
		t.Errorf("requests[1] text = %q", body.Requests[1].Content.Parts[0].Text)
	}
	if body.Requests[0].OutputDimensionality == nil || *body.Requests[0].OutputDimensionality != 256 {
		t.Errorf("outputDimensionality = %v", body.Requests[0].OutputDimensionality)
	}
}

func TestBuildEmbeddingOmitsUnsetDimensions(t *testing.T) {
	hr, _, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1beta", Model: "m"},
		&ir.EmbeddingRequest{Input: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(hr.Body)
	if strings.Contains(string(raw), "outputDimensionality") {
		t.Errorf("body = %s; an unset dimension count was sent", raw)
	}
}

func TestBuildEmbeddingWarnsThatBase64IsUnavailable(t *testing.T) {
	// The client asked for base64 to skip a decode and will get floats. That
	// is a different response from the one it asked for.
	_, warns, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1beta", Model: "m"},
		&ir.EmbeddingRequest{Input: []string{"a"}, Encoding: "base64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Reason, "float") {
		t.Errorf("warnings = %v", warns)
	}
}

func TestBuildEmbeddingRefusesTokenInput(t *testing.T) {
	// Gemini takes text. Sending token ids as their decimal spelling would
	// embed the digits and the client could not tell.
	_, _, err := New().BuildEmbedding(context.Background(),
		&adapter.Target{BaseURL: "https://x/v1beta", Model: "m"},
		&ir.EmbeddingRequest{Tokens: [][]int{{1, 2}}})
	if err == nil {
		t.Fatal("a pre-tokenized input was accepted")
	}
}

func TestParseEmbeddingReadsTheBatchResponse(t *testing.T) {
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(
		`{"embeddings":[{"values":[0.5,-0.25]},{"values":[1]}]}`))}
	out, err := New().ParseEmbedding(resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Embeddings) != 2 {
		t.Fatalf("embeddings = %+v", out.Embeddings)
	}
	if out.Embeddings[0].Index != 0 || out.Embeddings[1].Index != 1 {
		t.Errorf("indices = %d, %d; gemini returns order only and the index is ours to assign",
			out.Embeddings[0].Index, out.Embeddings[1].Index)
	}
	if out.Embeddings[0].Float[1] != -0.25 {
		t.Errorf("vector = %v", out.Embeddings[0].Float)
	}
	if out.Usage.InputTokens != 0 {
		t.Errorf("usage = %+v; gemini reports none for embeddings", out.Usage)
	}
}

func TestParseEmbeddingRejectsAVectorlessBody(t *testing.T) {
	resp := &http.Response{StatusCode: 200,
		Body: io.NopCloser(strings.NewReader(`{"embeddings":[]}`))}
	if _, err := New().ParseEmbedding(resp); err == nil {
		t.Fatal("a 200 with no vectors parsed cleanly")
	}
}

func TestGeminiDeclaresTheEmbeddingSurface(t *testing.T) {
	// Spec §4's matrix. Without this the router filters gemini out of every
	// embedding request while its preset advertises the surface.
	got := adapter.SurfacesOf(New())
	if !got.Has(ir.SurfaceEmbedding) || !got.Has(ir.SurfaceLLM) {
		t.Errorf("surfaces = %v", got)
	}
	if got.Has(ir.SurfaceImage) {
		t.Error("gemini declares image; Imagen is out of scope for v1 per the §4 matrix")
	}
}
```

Match `adapter.SurfaceSet`'s accessor to whatever Task 7 actually named it — read `internal/adapter/adapter.go` before writing the last test rather than assuming `Has`.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/gemini/ -run 'Embedding|GeminiDeclares' -v
```

Expected: FAIL to build — `undefined: BuildEmbedding`.

- [ ] **Step 3: Implement the adapter**

Create `internal/adapter/gemini/embed.go`:

```go
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

const maxGeminiEmbeddingBytes = 32 << 20

type geminiEmbedRequest struct {
	Requests []geminiEmbedItem `json:"requests"`
}

type geminiEmbedItem struct {
	// Model is repeated per item, prefixed with "models/". Gemini rejects the
	// batch without it even though the name is already in the URL.
	Model   string `json:"model"`
	Content struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"content"`
	OutputDimensionality *int `json:"outputDimensionality,omitempty"`
}

// BuildEmbedding renders batchEmbedContents rather than embedContent. The IR
// request is batched, and the single-item endpoint would turn a 96-input batch
// into 96 round trips through the attempt loop, each separately failable.
func (a *Adapter) BuildEmbedding(ctx context.Context, t *adapter.Target,
	req *ir.EmbeddingRequest) (*http.Request, []ir.Warning, error) {

	if len(req.Tokens) > 0 {
		// Gemini takes text. Sending token ids as their decimal spelling would
		// embed the digits, succeed, and give the client no way to notice.
		return nil, nil, errors.New(
			"this provider takes text, and the request carried pre-tokenized input")
	}
	if len(req.Input) == 0 {
		return nil, nil, errors.New("input is required")
	}

	var warns []ir.Warning
	if req.Encoding == "base64" {
		warns = append(warns, ir.Warning{
			Field: "encoding_format", Target: t.Model,
			Reason: "this provider returns float vectors only; base64 was requested",
		})
	}

	name := "models/" + t.Model
	body := geminiEmbedRequest{Requests: make([]geminiEmbedItem, 0, len(req.Input))}
	for _, text := range req.Input {
		item := geminiEmbedItem{Model: name}
		item.Content.Parts = append(item.Content.Parts, struct {
			Text string `json:"text"`
		}{Text: text})
		if req.Dimensions > 0 {
			d := req.Dimensions
			item.OutputDimensionality = &d
		}
		body.Requests = append(body.Requests, item)
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	endpoint := strings.TrimRight(t.BaseURL, "/") + "/models/" +
		url.PathEscape(t.Model) + ":batchEmbedContents"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, fmt.Errorf("build embedding request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("x-goog-api-key", t.APIKey)
	}
	return hr, warns, nil
}

func (a *Adapter) ParseEmbedding(resp *http.Response) (*ir.EmbeddingResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxGeminiEmbeddingBytes))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	var env struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}
	if len(env.Embeddings) == 0 {
		return nil, errors.New("embedding response carried no vectors")
	}
	out := &ir.EmbeddingResponse{
		Embeddings: make([]ir.Embedding, 0, len(env.Embeddings)),
		// Gemini reports no usage for embeddings. Zero here means unreported,
		// and the request row records it as such rather than as free.
	}
	for i, e := range env.Embeddings {
		// The index is ours to assign: the batch response carries order only.
		out.Embeddings = append(out.Embeddings, ir.Embedding{Index: i, Float: e.Values})
	}
	return out, nil
}

var _ adapter.Embedder = (*Adapter)(nil)
```

- [ ] **Step 4: Declare the surface**

In `internal/adapter/gemini/adapter.go`, beside the other methods:

```go
// Surfaces is spec §4's matrix row. Imagen is out of scope for v1, so no image
// surface is declared even though the provider offers one.
func (a *Adapter) Surfaces() adapter.SurfaceSet {
	return adapter.NewSurfaceSet(ir.SurfaceLLM, ir.SurfaceEmbedding)
}
```

Match `NewSurfaceSet` to the constructor Task 7 actually wrote.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/gemini/ ./internal/adapter/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS in both packages. `internal/adapter`'s matrix test from Task 7 now sees gemini's real row.

- [ ] **Step 6: Read the rendered request rather than only asserting on it**

Add a temporary `t.Log(string(raw))` to `TestBuildEmbeddingRendersTheBatchShape`, re-run it, and read the JSON: confirm every element repeats `"model":"models/…"`, that `content.parts` is an array of objects rather than a bare string, and that `outputDimensionality` sits inside each request rather than at the top level. Remove the log before committing. Gemini rejects all three mistakes with the same unhelpful 400.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/adapter/gemini/embed.go internal/adapter/gemini/embed_test.go internal/adapter/gemini/adapter.go
git commit -m "feat(gemini): serve the embedding surface"
```

---

### Task 33: Documentation

**Files:**
- Modify: `README.md`, `darkrouter.example.yaml`, `docs/PROGRESS.md`
- Modify: `docs/superpowers/specs/README.md`

**Interfaces:**
- Consumes: everything.
- Produces: nothing code depends on.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 0 - risk 0 = 2
**Approach:** inline - skip 2: the content is enumerated below and the three documents already have the sections it belongs in.

Spec §8 makes one of these a **requirement rather than a courtesy**: "The documentation states plainly that embedding aliases should list same-model targets across providers, not different models." That sentence is the entire mitigation for a hazard that silently corrupts a vector index, so it is not a nice-to-have paragraph.

- [ ] **Step 1: Document the routes**

In `README.md`, beside the existing endpoint list, add the seven routes with what each needs:

| Route | Surface | Notes |
|---|---|---|
| `POST /v1/embeddings` | `embedding` | Batched input; `float` or `base64`; optional `dimensions`. Pre-tokenized input is forwarded as token ids. |
| `POST /v1/responses` | `llm` | Stateless only. `previous_response_id`, `conversation` and `background` are refused, not degraded. |
| `POST /v1/images/generations` | `image` | URLs or base64. `gpt-image-1` reports usage; the dall-e models report none and their cost stays unrecorded. |
| `POST /v1/audio/speech` | `tts` | Binary or SSE, streamed through and never stored. |
| `POST /v1/audio/transcriptions` | `stt` | Multipart in; JSON, plain text or SSE out, chosen by the response `Content-Type`. |
| `POST /v1/rerank` | `rerank` | Cohere v2 schema, at the path the provider's preset declares. |
| `POST /v1/moderations` | `moderation` | Category flags forwarded whole, including categories added after this was written. |

Note in the same section that these routes speak the **OpenAI dialect only**. The Anthropic and Gemini inbound dialects serve chat and token counting; neither vendor defines a rerank or moderations endpoint, and a client speaking those dialects reaches the auxiliary surfaces by calling the OpenAI routes with its existing token.

Note also that `/v1/audio/translations` is **permanently absent** per master design §2. Whisper clients do call it, so its absence should be findable in the README rather than discovered as a 404.

- [ ] **Step 2: Write the embedding failover hazard where an operator will read it**

In `README.md`, as its own short subsection under the routes — not a footnote:

> **Embedding aliases must point at the same model.**
>
> An alias exists so a request can fail over. For chat that is unambiguously
> good. For embeddings it is a hazard: two models produce vectors in different
> vector spaces, so a failover to a different model returns vectors that are
> not comparable to the ones already in your index — and **nothing in the
> response says so**. The call succeeds, the vector looks fine, and similarity
> search quietly degrades.
>
> So an embedding alias should list the *same* model across *different*
> providers, never different models:
>
> ```yaml
> aliases:
>   # Good: one model, two providers. A failover is invisible and harmless.
>   embed:
>     - openai/text-embedding-3-small
>     - azure-openai/text-embedding-3-small
> ```
>
> An alias entry is a `provider/model` string, which is the shape
> `darkrouter.example.yaml` already uses for its chat aliases.
>
> Darkrouter permits the other arrangement rather than refusing it — refusing
> would make an alias useless the moment its first provider rate-limits — but
> it records a warning on the request row naming both models, and always sets
> `X-Darkrouter-Model` to the model that actually served. Check that header if
> you index across a failover.

Add the same alias to `darkrouter.example.yaml` as a commented block, with the one-line version of the warning above it.

- [ ] **Step 3: Update the progress document**

In `docs/PROGRESS.md`, set phase 5 to complete in the status table, and add a "Closed by phase 5" section recording:

- **`Adapter.Surfaces()` exists**, closing the item phase 3 carried and phase 4 and 6 both deferred. Routing now excludes a provider whose kind Darkrouter cannot speak the surface to, as a filter rather than a runtime error.
- **The surface vocabulary matches master design §6.** Seven values, `tts` and `stt` separate, and every shipped preset declares them in the corrected spelling.
- **A phase 6 merge defect was fixed on the branch** (`f6ae00c`): `merge.surfaces` resolved override → row → preset, but discovery hardcodes `'["llm"]'` into every row it inserts and the sync echoes it, so the row always shadowed the preset and widening a preset had no effect on any discovered model. The preset now outranks the row; an operator override still wins.

Add a "Carried forward from phase 5" section with these, each one or two sentences:

- **Cost is still never computed.** `applyUsage` leaves `CostMicros` nil on every surface including chat, although phase 6 shipped `catalog.Pricing` with real per-MTok numbers. Nothing in phase 5 changed that, and the item below is why it was not simply switched on.
- **`ir.Usage.InputTokens` does not mean the same thing across adapters.** Anthropic's `input_tokens` **excludes** cache read and write tokens; OpenAI's `prompt_tokens` and Gemini's `promptTokenCount` **include** them. Each adapter copies its provider's own convention into the same field. Any cost formula written today is therefore wrong for at least one family — it either double-charges cached input or under-charges it — so the IR has to normalize before pricing can be turned on. This is the blocker for the item above, and it is a real defect in the existing usage plumbing rather than a phase 5 omission.
- **`capture.bodies` has no writer.** The `request_bodies` table exists from phase 2 and the retention sweep prunes it, but **nothing has ever inserted a row**. The setting, its `max_bytes` and its `retention` are all inert. Spec §5's "a speech response is never captured even when `capture.bodies` is on" is therefore satisfied by construction rather than by enforcement — what phase 5 does enforce is that the body is never held whole, which is the property that matters.
- **Per-call image pricing has no catalog source.** Spec §9 says cost should come from per-call or per-unit pricing where no usage arrives, but `catalog.Pricing` carries only per-MTok rates and models.dev supplies nothing else. A dall-e call therefore records no cost at all, which is correct but incomplete.
- **Unmodeled Responses request fields are dropped silently.** `top_logprobs`, `truncation`, `include`, `service_tier`, `max_tool_calls` and `prompt_cache_key` are parsed away without a warning. None changes the answer's content, but `truncation` and `max_tool_calls` change its shape, and a client setting them gets behavior it did not ask for with nothing in the trace saying so.
- **Responses fields the IR does not model are dropped rather than re-emitted.** Spec §5 says they "ride in `Extra` and are re-emitted"; `truncation`, `include`, `service_tier`, `top_logprobs` and `prompt_cache_key` are instead parsed away. The response echoes the fields the OpenAI SDK's model requires — tools, tool choice, sampling, instructions, metadata — which is what keeps a client working; the rest are a documented deviation, not an oversight. `truncation` and `max_tool_calls` change the answer's shape, so a client setting them gets behavior it did not ask for with nothing in the trace saying so.
- **A reasoning-block indexing defect was found in shipped code and fixed here.** `internal/adapter/openaicompat/parse.go` emitted reasoning deltas with no block index — the zero value, which is the text block's — and no open or close events, while tool blocks were carefully offset by 1000 to avoid exactly that. It was invisible because every consumer until the Responses stream writer switched on the delta's type and ignored the index. Task 27 gives reasoning its own index space.
- **Responses ids are not resolvable, by design.** Returned ids carry a `resp_dr_` prefix and `store: false`; any request echoing one back is refused. A client built around server-side conversation state will not work against Darkrouter and is told so explicitly rather than served an amnesic answer.
- **Four of the seven surfaces have no live-verified provider.** Task 34 verifies chat, responses, transcriptions and speech against Groq. Embeddings, images, rerank and moderations are verified only as the no-provider error, because no key for a provider serving them was available. Verifying them needs an OpenAI or Cohere key.

- [ ] **Step 4: Update the spec index**

In `docs/superpowers/specs/README.md`, the "Open decisions" section still says the rerank wire shape may be revisited. Replace it with the settled statement: exactly one shipped preset declares a `rerank` surface, `cohere`, and neither Jina nor Voyage is a preset at all, so Cohere v2 is not merely the recommendation but the only shape any shipped provider serves. **No revisit is planned.**

- [ ] **Step 5: Verify the documents say what the code does**

```bash
export PATH=$PATH:/usr/local/go/bin
grep -c 'v1/embeddings\|v1/responses\|v1/images/generations\|v1/audio/speech\|v1/audio/transcriptions\|v1/rerank\|v1/moderations' README.md
grep -n 'HandleFunc("POST /v1' internal/server/server.go
```

Expected: the README names all seven, and every route in `server.go` appears in the README. **Read both lists side by side** — a route documented but not wired, or wired but not documented, is exactly the drift this step exists to catch.

- [ ] **Step 6: Commit**

```bash
git add README.md darkrouter.example.yaml docs/PROGRESS.md docs/superpowers/specs/README.md
git commit -m "docs: document the auxiliary surfaces"
```

---

### Task 34: Live verification against a real provider

**Files:**
- Modify: `docs/PROGRESS.md`

**Interfaces:**
- Consumes: everything.
- Produces: the verification record.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 0 - risk 0 = 2
**Approach:** inline - skip 2: every command is given below; only the judgement of what the output should say is prose.

The unit suite proves the shapes. It cannot prove that a real provider accepts them, and phase 6's verification is the reason to insist: reading `warnings_json` after a live request is what caught a mechanism that passed every test and did nothing in production.

**What Groq can and cannot verify, stated up front.** Groq serves chat, `/v1/audio/transcriptions` and `/v1/audio/speech` on one OpenAI-compatible base URL. It serves no embeddings, images, rerank or moderations endpoint. So four of the seven routes are verified as the **no-provider error**, which is itself a done criterion — "a request for a surface no configured provider offers returns a clear error naming that fact" — and is a real check rather than a skipped one. Say so in the record; do not imply seven live surfaces.

- [ ] **Step 1: Build and start**

```bash
export PATH=$PATH:/usr/local/go/bin
set -a; . ./.env; set +a
export DARKROUTER_MASTER_KEY=throwaway-phase5-verify
CGO_ENABLED=0 go build -o /tmp/darkrouter-p5 ./cmd/darkrouter
ls -la /tmp/darkrouter-p5
```

Write a verification config at `/tmp/dr-p5.yaml` with `proxy_listen: ":18080"`, `admin_listen: ":18081"`, one provider `id: groq`, `kind: openaicompat`, **`preset: groq`** — Task 17 is what makes that key legal, and without it nothing joins the catalog and the audio surfaces are never declared — `base_url: https://api.groq.com/openai/v1`, the key from `GROQ_KEY`, and `models: [openai/gpt-oss-120b, whisper-large-v3, playai-tts]`.

**Check how the config reads a secret before writing the file.** `config.NewStore` is constructed with `os.LookupEnv`, so some form of environment expansion exists; read `internal/config/load.go` for the exact syntax rather than assuming `${GROQ_KEY}` works, and fall back to pasting the literal key into the temp file — it lives in `/tmp`, not the repository.

Start it in the background and record the pid **by binary name**:

```bash
mkdir -p /tmp/dr-p5-data
/tmp/darkrouter-p5 -config /tmp/dr-p5.yaml -db /tmp/dr-p5-data/darkrouter.db >/tmp/dr-p5.log 2>&1 &
sleep 3
ps -C darkrouter-p5 -o pid=
curl -fsS localhost:18081/readyz && echo READY
```

The flags are `-config` and `-db`, and `-db` names the database **file**. `sqlite3` is not installed here, so every query below runs through the `/tmp/dbq/main.go` helper written in Task 30 Step 6; run it from the repository root so it resolves `modernc.org/sqlite` from this module.

`nohup … &` inside a compound command returns the subshell's pid, not the binary's. Kill by name at the end and confirm nothing is left holding the port.

- [ ] **Step 2: Verify chat still works**

The `runAttempts` refactor is the largest change in this phase and chat is what must not have moved.

```bash
curl -sS -D /tmp/h.txt localhost:18080/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{"model":"openai/gpt-oss-120b","messages":[{"role":"user","content":"say hi"}]}' | head -c 400
grep -i 'x-darkrouter' /tmp/h.txt
```

Expected: a completion, and `X-Darkrouter-Request`, `-Provider`, `-Model` and `-Attempts: 1` all present.

- [ ] **Step 3: Verify the Responses API, unary and streamed**

```bash
curl -sS localhost:18080/v1/responses -H 'content-type: application/json' \
  -d '{"model":"openai/gpt-oss-120b","input":"say hi in three words"}' | head -c 600
echo
curl -sS -N localhost:18080/v1/responses -H 'content-type: application/json' \
  -d '{"model":"openai/gpt-oss-120b","input":"count to three","stream":true}' | head -40
echo
curl -sS -o /dev/null -w '%{http_code}\n' localhost:18080/v1/responses \
  -H 'content-type: application/json' \
  -d '{"model":"openai/gpt-oss-120b","input":"hi","previous_response_id":"resp_dr_x"}'
```

Expected, and **read the streamed output rather than only checking the exit code**: the unary body has `"object":"response"`, `"status":"completed"`, `"store":false`, an `output` array with a `message` item, and an `output_text`. The stream shows `event:` lines with names matching each frame's `data.type`, sequence numbers running from 0 with no gaps, `response.output_item.done` **before** `response.completed`, and **no `[DONE]`**. The stateful request is `400`.

- [ ] **Step 4: Verify transcriptions, including the in-form rewrite**

```bash
export PATH=$PATH:/usr/local/go/bin
ffmpeg -f lavfi -i 'sine=frequency=440:duration=2' -ar 16000 /tmp/tone.mp3 -y 2>/dev/null || \
  echo "no ffmpeg: use any small mp3/wav at /tmp/tone.mp3"
curl -sS localhost:18080/v1/audio/transcriptions \
  -F file=@/tmp/tone.mp3 -F model=whisper-large-v3 -F response_format=json
echo
curl -sS localhost:18080/v1/audio/transcriptions \
  -F file=@/tmp/tone.mp3 -F response_format=text -F model=whisper-large-v3 -w '\n%{content_type}\n'
```

The second call puts `model` **after** `response_format` deliberately; a client that puts it after the file part is the case spec §6 calls out. Expected: JSON with a `text` field on the first, `text/plain` content type on the second, and both 200. A tone transcribes to little or nothing — an empty `text` is a pass, a 400 is not.

- [ ] **Step 5: Verify speech streams and is not stored**

```bash
curl -sS localhost:18080/v1/audio/speech -H 'content-type: application/json' \
  -d '{"model":"playai-tts","input":"Darkrouter phase five.","voice":"Fritz-PlayAI","response_format":"mp3"}' \
  -o /tmp/out.mp3 -w '%{content_type} %{size_download}\n'
file /tmp/out.mp3 2>/dev/null || head -c 4 /tmp/out.mp3 | xxd
go run /tmp/dbq/main.go /tmp/dr-p5-data/darkrouter.db "SELECT count(*) FROM request_bodies"
go run /tmp/dbq/main.go /tmp/dr-p5-data/darkrouter.db \
  "SELECT surface, response_bytes, response_content_type, surface_meta_json
     FROM requests WHERE surface = 'tts' ORDER BY ts DESC LIMIT 1"
```

Expected: an `audio/mpeg` body of non-trivial size, `request_bodies` empty, and the request row's `response_bytes` **equal to the downloaded size** with `response_content_type` `audio/mpeg` and a `voice` in the surface meta. If `playai-tts` requires terms acceptance on the account and returns a 400, record that and verify the surface reached the provider — the point of this step is that the route routes and streams, and a provider-side entitlement error still proves both.

- [ ] **Step 6: Verify the no-provider error on the four surfaces Groq does not serve**

```bash
for route in v1/embeddings v1/images/generations v1/rerank v1/moderations; do
  printf '%s -> ' "$route"
  curl -sS -o /tmp/body.json -w '%{http_code} ' localhost:18080/$route \
    -H 'content-type: application/json' \
    -d '{"model":"openai/gpt-oss-120b","input":"x","prompt":"x","query":"q","documents":["a"]}'
  cat /tmp/body.json; echo
done
```

Expected: `404` on all four, each body naming the **surface** as the reason rather than the model. Then confirm nothing was attempted:

```bash
go run /tmp/dbq/main.go /tmp/dr-p5-data/darkrouter.db \
  "SELECT r.surface, r.error_code, count(a.seq)
     FROM requests r LEFT JOIN request_attempts a ON a.request_id = r.id
    WHERE r.surface IN ('embedding','image','rerank','moderation')
    GROUP BY r.id ORDER BY r.ts"
```

Expected: four rows, each with `not_found` and **zero attempts**. A non-zero count means the surface filter ran after the provider was contacted, which is the failure spec §4 exists to prevent.

- [ ] **Step 7: Read the request rows rather than trusting the responses**

```bash
go run /tmp/dbq/main.go /tmp/dr-p5-data/darkrouter.db \
  "SELECT dialect, surface, status, tokens_in, tokens_out, response_bytes,
          substr(surface_meta_json,1,60) AS meta, substr(warnings_json,1,80) AS warn
     FROM requests ORDER BY ts"
```

Expected: a `openai-responses` dialect row distinct from the `openai` chat rows; `stt` and `tts` rows carrying `response_bytes` and their surface meta; `warnings_json` `[]` everywhere nothing was lost. Phase 6's verification found a mechanism that passed every test and did nothing in production by reading this column — read it, do not assume it.

- [ ] **Step 8: Stop the gateway and confirm nothing is left running**

```bash
kill "$(ps -C darkrouter-p5 -o pid= | head -1)" 2>/dev/null
sleep 1
ps -C darkrouter-p5 -o pid= || echo "stopped"
ss -ltnp 2>/dev/null | grep -E ':1808[01]' || echo "ports free"
```

Expected: no process and no listener on 18080 or 18081. Ports 8080 and 8081 belong to an unrelated application and must never be touched.

- [ ] **Step 9: Record the result**

Add a numbered section to `docs/PROGRESS.md`'s "Open items" recording what was verified, with the real numbers — the actual `response_bytes` for the speech call, the actual token counts, the streamed event names in order. State plainly that four surfaces were verified only as the no-provider error and name the key that would be needed to do better. A verification note that overstates its coverage is worse than none.

- [ ] **Step 10: Commit**

```bash
git add docs/PROGRESS.md
git commit -m "docs: record phase 5 live verification"
```

---

## Finishing

With Task 34 committed, use superpowers:finishing-a-development-branch. The merge is `--no-ff` onto `master`, so the phase stays legible as a unit in the history:

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
git checkout master
git merge --no-ff phase5-auxiliary-surfaces -m "feat: phase 5 auxiliary surfaces"
```

Do not push. Master is already far ahead of origin and pushing is the operator's call.
