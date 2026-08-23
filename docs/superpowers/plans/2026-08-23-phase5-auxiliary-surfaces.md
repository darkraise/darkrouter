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
| `internal/ir/ir.go` | The seven-surface vocabulary; `ParseSurface` |
| `internal/ir/aux.go` | The narrow per-surface IR types: embedding, image, speech, transcription, rerank, moderation |
| `internal/adapter/adapter.go` | `SurfaceSet` and `Adapter.Surfaces()`; the per-surface build and parse interfaces |
| `internal/exec/surface.go` | `SurfaceOp`, `CommitWriter`, and `runAttempts` re-parameterized over it |
| `internal/exec/resolve.go` | The request prologue — record, providers, snapshot, resolve, candidate trace — shared by every route |
| `internal/exec/chat.go` | Chat as the first `SurfaceOp`; behavior identical to phase 4 |
| `internal/exec/aux.go` | The generic auxiliary `SurfaceOp`: build, send, respond |
| `internal/exec/multipart.go` | Buffering and in-form model rewriting for transcriptions |
| `internal/adapter/openaicompat/embed.go` | Embeddings, images, speech, transcriptions, moderations against an OpenAI-compatible upstream |
| `internal/adapter/openaicompat/rerank.go` | Cohere v2 rerank, at the preset-declared path |
| `internal/adapter/gemini/embed.go` | `embedContent` |
| `internal/edge/openai/aux.go` | Parsing and writing the six auxiliary shapes |
| `internal/edge/openai/responses.go` | The Responses API: request parsing, item-based body, semantic stream writer |
| `internal/catalog/presets.overrides.yaml` | Widened surface declarations |
| `internal/server/server.go` | The seven new routes |
| `internal/store/log.go` | Surface-specific log fields |
| `internal/store/migrations/0003_surfaces.sql` | The columns those fields need |

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
// The interface is deliberately four methods split at two joints: rendering the
// outbound request, and turning a 2xx into client bytes.
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

Inside it, `e.attempt(w, r, d, cfg, req, c, ...)` becomes `e.attempt(w, r, op, cfg, c, ...)`, and both `_ = d.WriteError(w, lastErr)` calls become `_ = op.WriteError(w, lastErr)`. Nothing else in the body changes — the budget gate, the health re-check, `adapterFor`, `MarkUsed`, `nextIndex` and the status assignments are all surface-invariant already.

`attempt` keeps its whole preamble and changes only where it reaches for the request or the dialect:

```go
func (e *Executor) attempt(w http.ResponseWriter, r *http.Request, op SurfaceOp,
	cfg *config.Config, c router.Candidate, p provider.Provider,
	bud budget, rec *store.RequestRecord, seq int, ad adapter.Adapter,
	cat catalog.Reader) (adapter.Outcome, int, *ir.Error) {
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
		Warns: warns, Adapter: ad,
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

Six surfaces differ in exactly two ways — what they render and how they write — and are identical in every other respect. Six near-duplicate types implementing four methods each would be thirty-odd methods of ceremony around twelve lines of actual difference.

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
		return nil, fmt.Errorf("request body exceeds %d bytes", maxBody)
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
- Produces: `(*Executor).RunAux(w, r, dialect string, surface ir.Surface, ew errorWriter, build func() (SurfaceOp, error))`. Tasks 15, 16, 18, 19, 22 and 23 each call it exactly once.

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
		func() (SurfaceOp, error) { return nil, errors.New("input is required") })

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
		func() (SurfaceOp, error) { return op, nil })

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
	build func() (SurfaceOp, error)) {

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

	op, err := build()
	if err != nil {
		rec.ErrorCode = string(ir.ErrInvalidRequest)
		_ = ew.WriteError(w, &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()})
		return
	}
	e.runOp(w, r, op, rec, start)
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
	rec *store.RequestRecord, start time.Time) {

	cfg := e.store.Current() // one snapshot for this request's whole lifetime
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

`embedOp` implements `SurfaceOp` directly rather than wrapping `AuxOp`. It carries one piece of per-request state — the first candidate's model — and a closure over that state buys nothing over a struct field.

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
- `catalogAlias(aProvider, aModel, bProvider, bModel string, surfaces ...ir.Surface)` — two *different* models, reached through an alias named `embed`. The alias is declared in the config `executorForTwo` builds.
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
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
)

// embedOp is the embedding surface. It implements SurfaceOp directly rather
// than wrapping AuxOp because it carries one piece of per-request state, and a
// closure over that state buys nothing over a field.
type embedOp struct {
	d   edge.EmbeddingDialect
	req *ir.EmbeddingRequest

	// firstModel is the model the first attempt rendered for. Spec §8: an
	// embedding request that fails over to a different model returns vectors
	// from a different vector space, a client filling an index across that
	// failover corrupts it, and nothing in the response body says so. The
	// comparison is against the first candidate rather than the name the client
	// sent, which is usually an alias and often not a model name at all.
	//
	// Written only from Build, which the loop calls once per attempt on one
	// goroutine, so no synchronization is needed.
	firstModel string
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
	if o.firstModel == "" {
		o.firstModel = tgt.Model
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

	warns := ac.Warns
	if ac.Cand.Model != o.firstModel {
		warns = append(warns, ir.Warning{
			Field:  "model",
			Target: ac.Cand.ProviderID + "/" + ac.Cand.Model,
			Reason: "embeddings served by " + ac.Cand.Model + " after " + o.firstModel +
				" failed; vectors from two models are not in the same vector space " +
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
	maxBody := e.store.Current().Server.MaxBodyBytes
	e.RunAux(w, r, d.Name(), ir.SurfaceEmbedding, d, func() (SurfaceOp, error) {
		req, err := d.ParseEmbedding(r, maxBody)
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
	if got.CostMicros != nil {
		t.Errorf("cost = %v; moderations report no usage and cost must stay NULL", *got.CostMicros)
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
		results = append(results, map[string]any{
			"flagged":         r.Flagged,
			"categories":      cats,
			"category_scores": scores,
		})
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
		return nil, fmt.Errorf("request body exceeds %d bytes", maxBody)
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
		Flagged    bool               `json:"flagged"`
		Categories map[string]bool    `json:"categories"`
		Scores     map[string]float64 `json:"category_scores"`
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
	maxBody := e.store.Current().Server.MaxBodyBytes
	e.RunAux(w, r, d.Name(), ir.SurfaceModeration, d, func() (SurfaceOp, error) {
		req, err := d.ParseModeration(r, maxBody)
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

### Task 17: The preset-declared rerank path reaches the adapter

**Files:**
- Modify: `internal/adapter/adapter.go`, `internal/exec/exec.go`
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `catalog.Preset.QuirkValue` (phase 6), `provider.Provider.Preset`.
- Produces: `adapter.Target.RerankPath`. Task 18's adapter reads it.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: the quirk mechanism and its accessor both exist from phase 6, and the only new code is one field and one lookup.

Spec §3.1: "Each preset declares its own rerank path, since providers expose it at differing URLs." Task 2 encodes that as the valued quirk `rerank-path=/v2/rerank` and its test refuses a rerank preset without one. Nothing carries the value to the adapter yet, and the adapter is where the URL is built.

**Why a field on `Target` rather than a lookup inside the adapter.** The adapter is given a resolved target and knows nothing about presets; reaching into `catalog.Embedded()` from inside it would make the renderer depend on the shipped data file and become untestable without it. Every other per-provider fact on `Target` — base URL, key, model, model info — is resolved by the executor for the same reason, and this is one more.

An empty `RerankPath` is not a fallback to a guessed URL. The router only produces a rerank candidate for a model whose preset declares the surface, and Task 2's test refuses such a preset without the quirk, so empty means a misconfiguration and Task 18 fails the build loudly rather than posting a rerank body at `/chat/completions`.

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

### Task 18: Rerank end to end, at the preset-declared path

**Files:**
- Modify: `internal/ir/aux.go`, `internal/adapter/adapter.go`, `internal/edge/edge.go`, `internal/edge/openai/aux.go`, `internal/server/server.go`
- Create: `internal/adapter/openaicompat/rerank.go`, `internal/exec/rerank.go`
- Test: `internal/adapter/openaicompat/rerank_test.go`, `internal/exec/rerank_test.go`, `internal/edge/openai/aux_test.go`

**Interfaces:**
- Consumes: `adapter.Target.RerankPath` (Task 17), `RunAux` (Task 14), `failedParse` (Task 15).
- Produces: `ir.RerankRequest`, `ir.RerankResponse`, `ir.RerankResult`, `adapter.Reranker`, `edge.RerankDialect`, and `(*Executor).HandleRerank`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: the shape is Cohere v2, settled in this plan's foundation, and the file layout is Task 16's with the rerank types substituted.

OpenAI defines no rerank endpoint, so Darkrouter's inbound contract **is** Cohere v2 — the same shape in and out. That makes this the one surface where the edge and the adapter agree by construction rather than by translation.

**The path is absolute from the host root, and that is load-bearing.** `cohere`'s preset base URL is `https://api.cohere.com/compatibility/v1` — an OpenAI-compatibility shim — while its rerank endpoint is the native `/v2/rerank`. Joining them the way every other endpoint is joined produces `https://api.cohere.com/compatibility/v1/v2/rerank`, which does not exist. A `rerank-path` beginning with `/` therefore replaces the base URL's path entirely.

Cohere v2 accepts `documents` as strings or as objects. Both are read; an object contributes its `text` field and **any other field it carries is dropped with a warning**, because a document object with structured fields is being reranked on its text alone and the client cannot otherwise tell.

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
			Index    int      `json:"index"`
			Score    float64  `json:"relevance_score"`
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

func TestParseRerankReadsResultsAndDocuments(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Body: io.NopCloser(strings.NewReader(`{"id":"r-1","results":[
		  {"index":2,"relevance_score":0.98,"document":{"text":"kept"}},
		  {"index":0,"relevance_score":0.11}]}`)),
	}
	out, err := New().ParseRerank(resp)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "r-1" || len(out.Results) != 2 {
		t.Fatalf("response = %+v", out)
	}
	if out.Results[0].Index != 2 || out.Results[0].RelevanceScore != 0.98 ||
		out.Results[0].Document != "kept" {
		t.Errorf("first result = %+v", out.Results[0])
	}
	if out.Results[1].Document != "" {
		t.Errorf("second document = %q, want empty", out.Results[1].Document)
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
	body := map[string]any{
		"model": t.Model, "query": req.Query, "documents": req.Documents,
	}
	// Zero is not a legal top_n, so absence needs no separate presence flag.
	if req.TopN > 0 {
		body["top_n"] = req.TopN
	}
	if req.ReturnDocuments {
		body["return_documents"] = true
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

type rerankEnvelope struct {
	ID      string `json:"id"`
	Results []struct {
		Index    int     `json:"index"`
		Score    float64 `json:"relevance_score"`
		Document *struct {
			Text string `json:"text"`
		} `json:"document"`
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
		res := ir.RerankResult{Index: r.Index, RelevanceScore: r.Score}
		if r.Document != nil {
			res.Document = r.Document.Text
		}
		out.Results = append(out.Results, res)
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
	maxBody := e.store.Current().Server.MaxBodyBytes
	e.RunAux(w, r, d.Name(), ir.SurfaceRerank, d, func() (SurfaceOp, error) {
		req, err := d.ParseRerank(r, maxBody)
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
