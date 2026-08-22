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

**Implementer:** dcc-superpower-companions:impl-opus-low
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
