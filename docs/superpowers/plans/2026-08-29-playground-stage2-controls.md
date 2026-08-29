# Playground Stage 2 — The Instrument Gets Its Controls

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose every sampling parameter darkrouter's IR actually carries, gate each control on the dialect that can send it, and show what the provider dropped after accepting it.

**Architecture:** A single data table declares which of the eight controls each of the three inbound dialects can carry, and why not where it cannot. The config pane reads that table and renders an unsupported control disabled with its reason rather than hiding it. `playgroundBody` gains the new fields and each dialect's body builder writes only what its own wire accepts. Warnings the adapters recorded when they dropped a parameter upstream are read off the trace the run already fetches, and rendered under the answer they belong to.

**Tech Stack:** Go 1.26 stdlib; React 19, TanStack Router + Query, darkraise-ui 6.5.0 (`Slider`, `Tooltip`, `Accordion`), Tailwind 4, vitest 4 + @testing-library/react.

**Spec:** `docs/superpowers/specs/2026-08-29-playground-overhaul-design.md` — §3 (stage 2), §7 (sampling parameters), §7.1 (the support matrix), §7.2 (dialect-aware controls), §7.4 (adapter warnings), §7.5 (Go changes), §10 (files), §11 (mockups), §12 (testing).

**Stage 1 is merged to `master`.** The playground now fills its frame; `chat.tsx` is dissolved into `lib/stream.ts`, `lib/request.ts`, `lib/use-chat-run.ts`, `transcript.tsx`, `composer.tsx` and `chat-tab.tsx`. This plan builds on that layout.

## Global Constraints

These apply to every task without restating.

- **TDD.** A failing test precedes the implementation it tests. Run it and see it fail before writing the code.
- **Gates before any commit.** Frontend tasks: `cd web && npm test && npm run typecheck` clean. Go tasks: `go build ./... && go vet ./...` clean and `go test -count=1 ./internal/admin/...` clean.
- **Never `text-xs`, never a custom size.** 14px (`text-sm`) is the floor; only `text-sm`, `text-base`, `text-lg`, `text-xl`, `text-2xl`, `text-3xl`. In a stylesheet use `var(--text-sm)`, never a pixel value. Hierarchy below body text comes from colour (`--legend`, `--muted-foreground`) and weight.
- **No control may lie.** A control the current dialect cannot carry renders disabled with the reason, never hidden and never silently dropped. This is the point of the stage.
- **Existing test assertions do not change.** Every suite under `web/src/features/playground/` and `internal/admin/` stays green untouched.
- **Comments explain WHY, never WHAT.** No comment may reference this plan, a task number, or that something was recently added.
- **Commit subjects** are `<type>(<scope>): <subject>`, imperative, 50 characters or fewer, no trailing period. Stage explicit paths — never `git add -A`. English only.
- **Branch.** Create `playground-stage2` from `master` before Task 1.
- **No new dependencies.**
- **Never stage `providers.png`** (untracked at the repo root) or anything under `.playwright-mcp/` or `.superpowers/`.

## The support matrix

Verified against `internal/ir/ir.go:204`, `internal/edge/openai/parse.go`, `internal/edge/anthropic/parse.go` and `internal/edge/gemini/parse.go`. This is the authority for Tasks 1, 3 and 4.

| Control | openai | anthropic | gemini |
|---|---|---|---|
| temperature | `temperature` | `temperature` | `generationConfig.temperature` |
| max tokens | `max_tokens` | `max_tokens` | `generationConfig.maxOutputTokens` |
| top P | `top_p` | `top_p` | `generationConfig.topP` |
| top K | **not read** | `top_k` | `generationConfig.topK` |
| stop sequences | `stop` (string or array) | `stop_sequences` | `generationConfig.stopSequences` |
| structured output | `response_format` — only `type:"json_schema"` **with** `json_schema.schema` | **not read** | `generationConfig.responseSchema` |
| reasoning effort | `reasoning_effort` | — | — |
| reasoning budget | — | `thinking.budget_tokens` | `generationConfig.thinkingConfig.thinkingBudget` |

Presence penalty, frequency penalty and seed are absent from `ir.Request` entirely and are **out of scope** — see spec §7.3.

## Definition of Done

| # | Criterion | Verification |
|---|---|---|
| D1 | The matrix is data, and tested as data | `cd web && npm test -- dialect-support` |
| D2 | Each dialect's body carries exactly what its wire accepts | `go test ./internal/admin/ -run TestPlaygroundSamplingPerDialect` |
| D3 | A control the dialect cannot carry is disabled and says why | `cd web && npm test -- config-pane` |
| D4 | An unsupported control's value never reaches the request body | `go test ./internal/admin/ -run TestPlaygroundDropsUnsupported`; `cd web && npm test -- request` |
| D5 | Warnings the adapters recorded appear under the answer | `cd web && npm test -- message` |
| D6 | The mockup set still builds and passes its gate | `cd docs/ux/mockups && python3 qa.py && python3 build.py` |
| D7 | Verified live and deployed | UAT at 1600×1000 and 1280×800, both themes; container healthy, bundle byte-match |

---

### Task 1: Declare what each dialect can carry

The matrix becomes data one module owns. Everything downstream reads it rather than re-deriving it.

**Files:**
- Create: `web/src/features/playground/dialect-support.ts`
- Create: `web/src/features/playground/dialect-support.test.ts`

**Interfaces:**
- Consumes: `PlaygroundDialect` from `web/src/lib/api-types.ts`.
- Produces:
  - `type Control = "temperature" | "maxTokens" | "topP" | "topK" | "stop" | "schema" | "reasoningEffort" | "reasoningBudget"`
  - `CONTROLS: Control[]` — the eight, in display order
  - `reasonFor(dialect: PlaygroundDialect, control: Control): string | null` — `null` when the dialect carries it, otherwise the sentence explaining why not
  - `supports(dialect: PlaygroundDialect, control: Control): boolean`

- [ ] **Step 1: Write the failing test**

Create `web/src/features/playground/dialect-support.test.ts`:

```ts
import { describe, expect, it } from "vitest"
import { CONTROLS, reasonFor, supports, type Control } from "./dialect-support"
import { DIALECTS } from "./config"

describe("what each dialect can carry", () => {
  it("lets every dialect send the three every wire shares", () => {
    for (const d of DIALECTS) {
      expect(supports(d, "temperature")).toBe(true)
      expect(supports(d, "maxTokens")).toBe(true)
      expect(supports(d, "topP")).toBe(true)
    }
  })

  it("refuses top K on the OpenAI chat wire, which has no such field", () => {
    expect(supports("openai", "topK")).toBe(false)
    expect(supports("anthropic", "topK")).toBe(true)
    expect(supports("gemini", "topK")).toBe(true)
  })

  it("refuses structured output on Anthropic, whose edge never reads it", () => {
    expect(supports("anthropic", "schema")).toBe(false)
    expect(supports("openai", "schema")).toBe(true)
    expect(supports("gemini", "schema")).toBe(true)
  })

  it("splits reasoning: an effort tier on OpenAI, a token budget elsewhere", () => {
    // Same idea, two spellings. ir.Reasoning holds both, and no dialect
    // carries both, so exactly one of the pair is live per dialect.
    expect(supports("openai", "reasoningEffort")).toBe(true)
    expect(supports("openai", "reasoningBudget")).toBe(false)
    for (const d of ["anthropic", "gemini"] as const) {
      expect(supports(d, "reasoningEffort")).toBe(false)
      expect(supports(d, "reasoningBudget")).toBe(true)
    }
  })

  it("gives a reason for every control it refuses, and none for one it allows", () => {
    // A disabled control with no reason is worse than a hidden one: it looks
    // broken rather than inapplicable.
    for (const d of DIALECTS) {
      for (const c of CONTROLS) {
        const reason = reasonFor(d, c)
        if (supports(d, c)) expect(reason).toBeNull()
        else expect(reason).toMatch(/\S/)
      }
    }
  })

  it("names the dialect that can send it, so the reason is actionable", () => {
    expect(reasonFor("openai", "topK")).toMatch(/anthropic|gemini/i)
    expect(reasonFor("anthropic", "schema")).toMatch(/openai|gemini/i)
  })

  it("covers every control for every dialect, with no gaps", () => {
    expect(CONTROLS).toHaveLength(8)
    for (const d of DIALECTS) {
      for (const c of CONTROLS) {
        expect(() => reasonFor(d, c)).not.toThrow()
      }
    }
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- dialect-support
```

Expected: FAIL — cannot resolve `./dialect-support`.

- [ ] **Step 3: Write the module**

Create `web/src/features/playground/dialect-support.ts`:

```ts
import type { PlaygroundDialect } from "../../lib/api-types"

/**
 * Which sampling controls each inbound dialect can actually carry.
 *
 * The binding constraint is not what the provider's API accepts — it is what
 * darkrouter's own edge parses into ir.Request. A field the edge does not read
 * is dropped before routing begins, so a control that sends it would accept a
 * value, report nothing, and change nothing.
 *
 * Verified against internal/edge/{openai,anthropic,gemini}/parse.go rather
 * than against provider documentation, which disagrees with the edges in
 * several places.
 */
export type Control =
  | "temperature"
  | "maxTokens"
  | "topP"
  | "topK"
  | "stop"
  | "schema"
  | "reasoningEffort"
  | "reasoningBudget"

/** In the order the pane shows them. */
export const CONTROLS: Control[] = [
  "temperature",
  "maxTokens",
  "topP",
  "topK",
  "stop",
  "schema",
  "reasoningEffort",
  "reasoningBudget",
]

/** null means the dialect carries it. A string is the reason it does not. */
const UNSUPPORTED: Record<PlaygroundDialect, Partial<Record<Control, string>>> = {
  openai: {
    topK: "The OpenAI chat wire has no top_k field. Switch the dialect to anthropic or gemini to send it.",
    reasoningBudget:
      "OpenAI takes a reasoning effort tier rather than a token budget. Use Effort, or switch to anthropic or gemini to set a budget.",
  },
  anthropic: {
    schema:
      "Darkrouter's Anthropic edge does not read response_format, so a schema sent here would be dropped before routing. Switch to openai or gemini.",
    reasoningEffort:
      "Anthropic takes a thinking budget in tokens rather than an effort tier. Use Budget, or switch to openai to set an effort.",
  },
  gemini: {
    reasoningEffort:
      "Gemini takes a thinking budget in tokens rather than an effort tier. Use Budget, or switch to openai to set an effort.",
  },
}

export function reasonFor(dialect: PlaygroundDialect, control: Control): string | null {
  return UNSUPPORTED[dialect]?.[control] ?? null
}

export function supports(dialect: PlaygroundDialect, control: Control): boolean {
  return reasonFor(dialect, control) === null
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd web && npm test -- dialect-support && npm run typecheck
```

Expected: PASS, seven tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/playground/dialect-support.ts \
        web/src/features/playground/dialect-support.test.ts
git commit -m "feat(web): declare what each dialect can carry"
```

---

### Task 2: Carry the new settings, and drop what the dialect cannot send

`PlaygroundConfig` grows five fields and `chatBody` learns to omit what the current dialect cannot carry — so an unsupported value is dropped at the boundary rather than sent and ignored.

**Files:**
- Modify: `web/src/features/playground/config.ts`
- Modify: `web/src/features/playground/lib/request.ts`
- Create: `web/src/features/playground/lib/request.test.ts`

**Interfaces:**
- Consumes: `reasonFor`, `supports` from `../dialect-support`.
- Produces: `PlaygroundConfig` gains `topP: string`, `topK: string`, `stopRaw: string`, `schemaRaw: string`, `reasoningEffort: string`, `reasoningBudget: string`. New exports from `lib/request.ts`: `parseSchema(raw: string): { schema?: unknown; error?: string }`, `parseStopLines(raw: string): string[]`. `PlaygroundChatBody` gains the matching optional wire fields.

- [ ] **Step 1: Write the failing test**

Create `web/src/features/playground/lib/request.test.ts`:

```ts
import { describe, expect, it } from "vitest"
import { chatBody, parseSchema, parseStopLines } from "./request"
import { emptyConfig } from "../config"

const base = { ...emptyConfig(), model: "m", messages: [{ role: "user", content: "hi" }] }

describe("stop sequences", () => {
  it("takes one per line, ignoring blank lines and surrounding space", () => {
    expect(parseStopLines("END\n  STOP  \n\nDONE\n")).toEqual(["END", "STOP", "DONE"])
  })

  it("treats an empty box as no stop sequences", () => {
    expect(parseStopLines("   \n\n")).toEqual([])
  })
})

describe("the structured output schema", () => {
  it("accepts a JSON object", () => {
    expect(parseSchema('{"type":"object"}')).toEqual({ schema: { type: "object" } })
  })

  it("treats an empty box as no schema rather than as an error", () => {
    expect(parseSchema("  ")).toEqual({})
  })

  it("names a parse failure instead of sending nothing", () => {
    const out = parseSchema("{not json")
    expect(out.schema).toBeUndefined()
    expect(out.error).toMatch(/json/i)
  })

  it("refuses a bare array, which is not a schema", () => {
    expect(parseSchema("[1,2]").error).toMatch(/object/i)
  })
})

describe("building the request body", () => {
  it("sends top_p on every dialect", () => {
    for (const dialect of ["openai", "anthropic", "gemini"] as const) {
      expect(chatBody({ ...base, dialect, topP: "0.9" }).top_p).toBe(0.9)
    }
  })

  it("drops top_k on openai, whose edge never reads it", () => {
    // Sending it would look like a setting that works. It is dropped here, at
    // the boundary, so the request carries only what the wire can express.
    expect(chatBody({ ...base, dialect: "openai", topK: "40" }).top_k).toBeUndefined()
    expect(chatBody({ ...base, dialect: "anthropic", topK: "40" }).top_k).toBe(40)
  })

  it("drops the schema on anthropic, whose edge never reads it", () => {
    const raw = '{"type":"object"}'
    expect(chatBody({ ...base, dialect: "anthropic", schemaRaw: raw }).response_schema).toBeUndefined()
    expect(chatBody({ ...base, dialect: "gemini", schemaRaw: raw }).response_schema).toEqual({
      type: "object",
    })
  })

  it("sends effort only on openai and budget only on the other two", () => {
    const openai = chatBody({ ...base, dialect: "openai", reasoningEffort: "high", reasoningBudget: "2048" })
    expect(openai.reasoning_effort).toBe("high")
    expect(openai.reasoning_budget).toBeUndefined()

    const anthropic = chatBody({ ...base, dialect: "anthropic", reasoningEffort: "high", reasoningBudget: "2048" })
    expect(anthropic.reasoning_effort).toBeUndefined()
    expect(anthropic.reasoning_budget).toBe(2048)
  })

  it("omits every new field when its box is empty", () => {
    const body = chatBody(base)
    for (const k of ["top_p", "top_k", "stop", "response_schema", "reasoning_effort", "reasoning_budget"]) {
      expect(body).not.toHaveProperty(k)
    }
  })

  it("still sends the transcript and the settings that predate this", () => {
    const body = chatBody({ ...base, temperature: "0.2", maxTokens: "100", system: "be brief" })
    expect(body.messages).toHaveLength(1)
    expect(body.temperature).toBe(0.2)
    expect(body.max_tokens).toBe(100)
    expect(body.system).toBe("be brief")
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- request.test
```

Expected: FAIL — `parseSchema` and `parseStopLines` are not exported.

- [ ] **Step 3: Extend the config type**

In `web/src/features/playground/config.ts`, add to `PlaygroundConfig`, after `maxTokens`:

```ts
  topP: string
  topK: string
  /** One sequence per line. A comma would be a character a sequence may contain. */
  stopRaw: string
  /** A JSON Schema object. Structured output is a schema, never a boolean:
   *  the OpenAI edge honours response_format only when a schema is present,
   *  so a bare "JSON mode" switch would be a control that does nothing. */
  schemaRaw: string
  /** "" | "low" | "medium" | "high" — OpenAI's spelling of reasoning. */
  reasoningEffort: string
  /** A token budget — Anthropic's and Gemini's spelling of the same idea. */
  reasoningBudget: string
```

and to `emptyConfig()`'s returned object: `topP: "", topK: "", stopRaw: "", schemaRaw: "", reasoningEffort: "", reasoningBudget: ""`.

- [ ] **Step 4: Extend the wire type**

In `web/src/lib/api-types.ts`, add to `PlaygroundChatBody`:

```ts
  top_p?: number
  top_k?: number
  stop?: string[]
  response_schema?: unknown
  reasoning_effort?: string
  reasoning_budget?: number
```

- [ ] **Step 5: Teach `chatBody` the boundary**

In `web/src/features/playground/lib/request.ts`, add the two parsers and extend `chatBody`:

```ts
import { supports } from "../dialect-support"

/** One stop sequence per line. Blank lines are not sequences. */
export function parseStopLines(raw: string): string[] {
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "")
}

/** The structured-output schema, or the reason it could not be read. */
export function parseSchema(raw: string): { schema?: unknown; error?: string } {
  const trimmed = raw.trim()
  if (trimmed === "") return {}
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch (err) {
    return { error: `schema must be JSON: ${(err as Error).message}` }
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return { error: "schema must be a JSON object" }
  }
  return { schema: parsed }
}
```

and inside `chatBody`, after the existing `max_tokens` handling and before the tools block:

```ts
  // Dropped here rather than sent and ignored: a value the dialect's edge does
  // not parse never reaches the router, so putting it on the wire would make
  // the request body disagree with what actually happened.
  const d = state.dialect
  if (state.topP !== "" && supports(d, "topP")) body.top_p = Number(state.topP)
  if (state.topK !== "" && supports(d, "topK")) body.top_k = Number(state.topK)
  const stop = parseStopLines(state.stopRaw)
  if (stop.length > 0 && supports(d, "stop")) body.stop = stop
  const { schema } = parseSchema(state.schemaRaw)
  if (schema !== undefined && supports(d, "schema")) body.response_schema = schema
  if (state.reasoningEffort !== "" && supports(d, "reasoningEffort")) {
    body.reasoning_effort = state.reasoningEffort
  }
  if (state.reasoningBudget !== "" && supports(d, "reasoningBudget")) {
    body.reasoning_budget = Number(state.reasoningBudget)
  }
```

- [ ] **Step 6: Run the tests**

```bash
cd web && npm test && npm run typecheck
```

Expected: PASS. `chat.test.ts`'s existing `chatBody` assertions must still pass untouched — they build a config literal, so add the six new fields to those literals **only** if typecheck demands it, and change no assertion.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/playground/config.ts \
        web/src/features/playground/lib/request.ts \
        web/src/features/playground/lib/request.test.ts \
        web/src/lib/api-types.ts \
        web/src/features/playground/chat.test.ts
git commit -m "feat(web): carry the full sampling set"
```

---

### Task 3: Send the full sampling set, per dialect

The Go side gains the fields and each builder writes only what its own wire accepts.

**Files:**
- Modify: `internal/admin/playground.go`
- Modify: `internal/admin/playground_test.go` (or create if absent — check first with `ls internal/admin/playground*_test.go`)

**Interfaces:**
- Produces: `playgroundBody` gains `TopP *float64`, `TopK *int`, `Stop []string`, `ResponseSchema json.RawMessage`, `ReasoningEffort string`, `ReasoningBudget *int`, with json tags `top_p`, `top_k`, `stop`, `response_schema`, `reasoning_effort`, `reasoning_budget`.

- [ ] **Step 1: Write the failing test**

Append to `internal/admin/playground_test.go`:

```go
func TestPlaygroundSamplingPerDialect(t *testing.T) {
	topP, topK, budget := 0.9, 40, 2048
	body := playgroundBody{
		Model: "m", Prompt: "hi",
		TopP: &topP, TopK: &topK,
		Stop:            []string{"END", "STOP"},
		ResponseSchema:  json.RawMessage(`{"type":"object"}`),
		ReasoningEffort: "high",
		ReasoningBudget: &budget,
	}

	t.Run("openai takes top_p, stop, a json_schema and an effort", func(t *testing.T) {
		got := openaiPlaygroundBody(body, false)
		if got["top_p"] != 0.9 {
			t.Errorf("top_p = %v, want 0.9", got["top_p"])
		}
		if _, ok := got["top_k"]; ok {
			// The OpenAI chat wire has no such field; sending it would be a
			// setting the operator believes took effect.
			t.Error("top_k must not appear on the openai wire")
		}
		stop, _ := got["stop"].([]string)
		if len(stop) != 2 || stop[0] != "END" {
			t.Errorf("stop = %v, want [END STOP]", got["stop"])
		}
		if got["reasoning_effort"] != "high" {
			t.Errorf("reasoning_effort = %v, want high", got["reasoning_effort"])
		}
		rf, ok := got["response_format"].(map[string]any)
		if !ok || rf["type"] != "json_schema" {
			t.Fatalf("response_format = %v, want a json_schema wrapper", got["response_format"])
		}
		// The edge honours response_format only when json_schema.schema is
		// present, so the wrapper is not optional decoration.
		js, ok := rf["json_schema"].(map[string]any)
		if !ok || js["schema"] == nil {
			t.Errorf("json_schema.schema missing: %v", rf)
		}
	})

	t.Run("anthropic takes top_k and a thinking budget, not a schema", func(t *testing.T) {
		got := anthropicPlaygroundBody(body, false)
		if got["top_k"] != 40 {
			t.Errorf("top_k = %v, want 40", got["top_k"])
		}
		seq, _ := got["stop_sequences"].([]string)
		if len(seq) != 2 {
			t.Errorf("stop_sequences = %v, want two", got["stop_sequences"])
		}
		if _, ok := got["response_format"]; ok {
			t.Error("the anthropic edge never reads response_format")
		}
		th, ok := got["thinking"].(map[string]any)
		if !ok || th["budget_tokens"] != 2048 {
			t.Fatalf("thinking = %v, want budget_tokens 2048", got["thinking"])
		}
		if th["type"] != "enabled" {
			t.Errorf("thinking.type = %v, want enabled", th["type"])
		}
		if _, ok := got["reasoning_effort"]; ok {
			t.Error("anthropic spells reasoning as a budget, not an effort")
		}
	})

	t.Run("gemini nests everything under generationConfig", func(t *testing.T) {
		got := geminiPlaygroundBody(body)
		gen, ok := got["generationConfig"].(map[string]any)
		if !ok {
			t.Fatalf("no generationConfig in %v", got)
		}
		if gen["topP"] != 0.9 || gen["topK"] != 40 {
			t.Errorf("topP/topK = %v/%v, want 0.9/40", gen["topP"], gen["topK"])
		}
		seq, _ := gen["stopSequences"].([]string)
		if len(seq) != 2 {
			t.Errorf("stopSequences = %v, want two", gen["stopSequences"])
		}
		if gen["responseSchema"] == nil {
			t.Error("responseSchema missing")
		}
		tc, ok := gen["thinkingConfig"].(map[string]any)
		if !ok || tc["thinkingBudget"] != 2048 {
			t.Errorf("thinkingConfig = %v, want thinkingBudget 2048", gen["thinkingConfig"])
		}
	})
}

func TestPlaygroundDropsUnsupported(t *testing.T) {
	// Nothing set: no dialect may invent a field. A body that carries an empty
	// stop array or a zero budget is a body that changed the request.
	body := playgroundBody{Model: "m", Prompt: "hi"}
	for name, got := range map[string]map[string]any{
		"openai":    openaiPlaygroundBody(body, false),
		"anthropic": anthropicPlaygroundBody(body, false),
	} {
		for _, k := range []string{"top_p", "top_k", "stop", "stop_sequences", "response_format", "thinking", "reasoning_effort"} {
			if _, ok := got[k]; ok {
				t.Errorf("%s: %s must be absent when unset", name, k)
			}
		}
	}
	gem := geminiPlaygroundBody(body)
	if _, ok := gem["generationConfig"]; ok {
		t.Error("gemini: generationConfig must be absent when nothing is set")
	}
}
```

Add `"encoding/json"` to the test file's imports if it is not already there.

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/admin/ -run 'TestPlaygroundSamplingPerDialect|TestPlaygroundDropsUnsupported'
```

Expected: FAIL to compile — `playgroundBody` has no field `TopP`.

- [ ] **Step 3: Extend the request struct**

In `internal/admin/playground.go`, add to `playgroundBody` after `MaxTokens`:

```go
	TopP            *float64        `json:"top_p,omitempty"`
	TopK            *int            `json:"top_k,omitempty"`
	Stop            []string        `json:"stop,omitempty"`
	ResponseSchema  json.RawMessage `json:"response_schema,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ReasoningBudget *int            `json:"reasoning_budget,omitempty"`
```

- [ ] **Step 4: Write what each wire accepts**

In `openaiPlaygroundBody`, before the tools block:

```go
	if b.TopP != nil {
		out["top_p"] = *b.TopP
	}
	if len(b.Stop) > 0 {
		out["stop"] = b.Stop
	}
	if b.ReasoningEffort != "" {
		out["reasoning_effort"] = b.ReasoningEffort
	}
	// The edge reads response_format only when the type is json_schema and a
	// schema is present, so a bare {"type":"json_object"} would parse and then
	// be dropped. The name is required by the wire and is not otherwise used.
	if len(b.ResponseSchema) > 0 {
		out["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "response",
				"schema": b.ResponseSchema,
			},
		}
	}
```

In `anthropicPlaygroundBody`, before the tools block:

```go
	if b.TopP != nil {
		out["top_p"] = *b.TopP
	}
	if b.TopK != nil {
		out["top_k"] = *b.TopK
	}
	if len(b.Stop) > 0 {
		out["stop_sequences"] = b.Stop
	}
	// The type travels with the budget: the edge keeps it as transport state
	// the Anthropic adapter needs to choose its outbound shape.
	if b.ReasoningBudget != nil && *b.ReasoningBudget > 0 {
		out["thinking"] = map[string]any{
			"type":          "enabled",
			"budget_tokens": *b.ReasoningBudget,
		}
	}
```

In `geminiPlaygroundBody`, inside the existing `gen` map building, after `maxOutputTokens`:

```go
	if b.TopP != nil {
		gen["topP"] = *b.TopP
	}
	if b.TopK != nil {
		gen["topK"] = *b.TopK
	}
	if len(b.Stop) > 0 {
		gen["stopSequences"] = b.Stop
	}
	// The edge maps responseSchema alone; responseMimeType is declared on its
	// wire struct but never read, so sending it would be noise.
	if len(b.ResponseSchema) > 0 {
		gen["responseSchema"] = b.ResponseSchema
	}
	if b.ReasoningBudget != nil && *b.ReasoningBudget > 0 {
		gen["thinkingConfig"] = map[string]any{"thinkingBudget": *b.ReasoningBudget}
	}
```

- [ ] **Step 5: Run the tests**

```bash
go build ./... && go vet ./... && go test -count=1 ./internal/admin/...
```

Expected: PASS, existing playground tests included.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/playground.go internal/admin/playground_test.go
git commit -m "feat(admin): send the full sampling set"
```

---

### Task 4: Gate the controls the dialect cannot send

The pane splits into focused files and grows the new controls, each disabled with its reason where the dialect cannot carry it.

**Files:**
- Create: `web/src/features/playground/config-pane/gated-field.tsx`
- Create: `web/src/features/playground/config-pane/sampling.tsx`
- Create: `web/src/features/playground/config-pane/config-pane.test.tsx`
- Move: `web/src/features/playground/config-pane.tsx` → `web/src/features/playground/config-pane/config-pane.tsx`
- Modify: `web/src/features/playground/playground-screen.tsx` (import path only)

**Interfaces:**
- Consumes: `reasonFor`, `type Control` from `../dialect-support`; `PlaygroundConfig` from `../config`.
- Produces:
  - `GatedField({ reason, children }: { reason: string | null; children: ReactNode })` — wraps a control, showing the reason when present
  - `Sampling({ config, onChange }: { config: PlaygroundConfig; onChange: (c: PlaygroundConfig) => void })`

- [ ] **Step 1: Write the failing test**

Create `web/src/features/playground/config-pane/config-pane.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { Sampling } from "./sampling"
import { emptyConfig } from "../config"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

const noop = () => {}

describe("the sampling controls", () => {
  it("enables top K on anthropic", () => {
    render(<Sampling config={{ ...emptyConfig(), dialect: "anthropic" }} onChange={noop} />)
    expect(screen.getByLabelText(/top k/i)).toBeEnabled()
  })

  it("disables top K on openai and says why", () => {
    // Disabled rather than hidden: a control that appears and disappears as
    // the dialect changes reads as a bug, while a disabled one with a reason
    // teaches something true about the three wires.
    render(<Sampling config={{ ...emptyConfig(), dialect: "openai" }} onChange={noop} />)
    const topK = screen.getByLabelText(/top k/i)
    expect(topK).toBeDisabled()
    expect(screen.getByText(/no top_k field/i)).toBeInTheDocument()
  })

  it("keeps a disabled control's stored value visible", () => {
    // A preset written under another dialect keeps what it stored. Blanking
    // the box would make the setting quietly lossy on every round trip.
    render(
      <Sampling config={{ ...emptyConfig(), dialect: "openai", topK: "40" }} onChange={noop} />,
    )
    expect(screen.getByLabelText(/top k/i)).toHaveValue(40)
  })

  it("enables top P on every dialect", () => {
    for (const dialect of ["openai", "anthropic", "gemini"] as const) {
      const { unmount } = render(<Sampling config={{ ...emptyConfig(), dialect }} onChange={noop} />)
      expect(screen.getByLabelText(/top p/i)).toBeEnabled()
      unmount()
    }
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- config-pane
```

Expected: FAIL — cannot resolve `./sampling`.

- [ ] **Step 3: Write `gated-field.tsx`**

```tsx
import type { ReactNode } from "react"

/**
 * A control the current dialect may not be able to carry.
 *
 * The reason renders as text beneath rather than only in a tooltip: a disabled
 * element fires no pointer events, so a tooltip bound to the control itself
 * would never open, and a wrapper that exists only to catch a hover is a
 * mechanism a reader has to know about to trust the control.
 */
export function GatedField({ reason, children }: { reason: string | null; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      {children}
      {reason ? <p className="text-sm text-[hsl(var(--legend))]">{reason}</p> : null}
    </div>
  )
}
```

- [ ] **Step 4: Write `sampling.tsx`**

```tsx
import { Input, Label, Switch, Textarea } from "darkraise-ui"
import { GatedField } from "./gated-field"
import { reasonFor } from "../dialect-support"
import type { PlaygroundConfig } from "../config"

export function Sampling({
  config,
  onChange,
}: {
  config: PlaygroundConfig
  onChange: (next: PlaygroundConfig) => void
}) {
  const set = <K extends keyof PlaygroundConfig>(key: K, value: PlaygroundConfig[K]) =>
    onChange({ ...config, [key]: value })
  const why = (control: Parameters<typeof reasonFor>[1]) => reasonFor(config.dialect, control)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex gap-2">
        <GatedField reason={why("temperature")}>
          <Label htmlFor="pg-temp">Temperature</Label>
          <Input
            id="pg-temp" type="number" placeholder="default"
            value={config.temperature}
            onChange={(e) => set("temperature", e.target.value)}
          />
        </GatedField>
        <GatedField reason={why("maxTokens")}>
          <Label htmlFor="pg-max">Max tokens</Label>
          <Input
            id="pg-max" type="number" placeholder="default"
            value={config.maxTokens}
            onChange={(e) => set("maxTokens", e.target.value)}
          />
        </GatedField>
      </div>

      <div className="flex gap-2">
        <GatedField reason={why("topP")}>
          <Label htmlFor="pg-topp">Top P</Label>
          <Input
            id="pg-topp" type="number" step="0.01" placeholder="default"
            disabled={why("topP") !== null}
            value={config.topP}
            onChange={(e) => set("topP", e.target.value)}
          />
        </GatedField>
        <GatedField reason={why("topK")}>
          <Label htmlFor="pg-topk">Top K</Label>
          <Input
            id="pg-topk" type="number" placeholder="default"
            disabled={why("topK") !== null}
            value={config.topK}
            onChange={(e) => set("topK", e.target.value)}
          />
        </GatedField>
      </div>

      <GatedField reason={why("stop")}>
        <Label htmlFor="pg-stop">Stop sequences</Label>
        <Textarea
          id="pg-stop" rows={2} placeholder="one per line"
          disabled={why("stop") !== null}
          value={config.stopRaw}
          onChange={(e) => set("stopRaw", e.target.value)}
          className="font-mono text-sm"
        />
      </GatedField>

      <label className="flex items-center gap-2 text-sm">
        <Switch checked={config.stream} onCheckedChange={(next) => set("stream", next)} />
        Stream the reply
      </label>
    </div>
  )
}
```

- [ ] **Step 5: Move the pane and use `Sampling`**

`git mv web/src/features/playground/config-pane.tsx web/src/features/playground/config-pane/config-pane.tsx`. Fix its imports for the new depth (`./config` becomes `../config`, `./lib/request` becomes `../lib/request`, `../shell/model-combobox` becomes `../../shell/model-combobox`, `../../lib/api-types` becomes `../../../lib/api-types`). Replace the sampling accordion's inner markup with `<Sampling config={config} onChange={onChange} />`, keeping the accordion itself.

Update `playground-screen.tsx`: `import { ConfigPane } from "./config-pane/config-pane"`.

- [ ] **Step 6: Run the tests**

```bash
cd web && npm test && npm run typecheck
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/playground/config-pane/ \
        web/src/features/playground/playground-screen.tsx
git commit -m "feat(web): gate controls the dialect can't send"
```

---

### Task 5: Reasoning and structured output

The two controls that are one idea in two spellings, and the schema editor that replaces the JSON-mode switch that would not have worked.

**Files:**
- Create: `web/src/features/playground/config-pane/reasoning.tsx`
- Create: `web/src/features/playground/config-pane/structured-output.tsx`
- Modify: `web/src/features/playground/config-pane/config-pane.tsx`
- Modify: `web/src/features/playground/config-pane/config-pane.test.tsx`

**Interfaces:**
- Consumes: `GatedField`, `reasonFor`, `parseSchema` from `../lib/request`.
- Produces: `Reasoning({ config, onChange })`, `StructuredOutput({ config, onChange })` with the same prop shape as `Sampling`.

- [ ] **Step 1: Write the failing test**

Append to `config-pane.test.tsx`:

```tsx
import { Reasoning } from "./reasoning"
import { StructuredOutput } from "./structured-output"

describe("reasoning, in whichever spelling the dialect uses", () => {
  it("offers an effort tier on openai", () => {
    render(<Reasoning config={{ ...emptyConfig(), dialect: "openai" }} onChange={noop} />)
    expect(screen.getByLabelText(/effort/i)).toBeEnabled()
    expect(screen.getByLabelText(/budget/i)).toBeDisabled()
  })

  it("offers a token budget on anthropic and gemini", () => {
    for (const dialect of ["anthropic", "gemini"] as const) {
      const { unmount } = render(<Reasoning config={{ ...emptyConfig(), dialect }} onChange={noop} />)
      expect(screen.getByLabelText(/budget/i)).toBeEnabled()
      expect(screen.getByLabelText(/effort/i)).toBeDisabled()
      unmount()
    }
  })
})

describe("structured output", () => {
  it("is a schema editor, not a switch", () => {
    // The OpenAI edge honours response_format only with a schema present, so
    // a JSON-mode toggle would be a control that did nothing on two of the
    // three dialects.
    render(<StructuredOutput config={{ ...emptyConfig(), dialect: "openai" }} onChange={noop} />)
    expect(screen.getByLabelText(/schema/i)).toBeEnabled()
  })

  it("reports malformed JSON rather than sending nothing", () => {
    render(
      <StructuredOutput
        config={{ ...emptyConfig(), dialect: "openai", schemaRaw: "{not json" }}
        onChange={noop}
      />,
    )
    expect(screen.getByText(/schema must be JSON/i)).toBeInTheDocument()
  })

  it("is disabled on anthropic, whose edge never reads it", () => {
    render(<StructuredOutput config={{ ...emptyConfig(), dialect: "anthropic" }} onChange={noop} />)
    expect(screen.getByLabelText(/schema/i)).toBeDisabled()
    expect(screen.getByText(/does not read response_format/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- config-pane
```

Expected: FAIL — cannot resolve `./reasoning`.

- [ ] **Step 3: Write `reasoning.tsx`**

```tsx
import {
  Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "darkraise-ui"
import { GatedField } from "./gated-field"
import { reasonFor } from "../dialect-support"
import type { PlaygroundConfig } from "../config"

const EFFORTS = ["low", "medium", "high"]

/**
 * One capability, two spellings.
 *
 * ir.Reasoning holds an effort tier and a token budget because the wires
 * disagree about which to take, not because they are different settings. Shown
 * under one heading so the operator learns they are the same idea rather than
 * hunting for the one their dialect happens to use.
 */
export function Reasoning({
  config,
  onChange,
}: {
  config: PlaygroundConfig
  onChange: (next: PlaygroundConfig) => void
}) {
  const set = <K extends keyof PlaygroundConfig>(key: K, value: PlaygroundConfig[K]) =>
    onChange({ ...config, [key]: value })
  const effortWhy = reasonFor(config.dialect, "reasoningEffort")
  const budgetWhy = reasonFor(config.dialect, "reasoningBudget")

  return (
    <div className="flex flex-col gap-4">
      <GatedField reason={effortWhy}>
        <Label htmlFor="pg-effort">Effort</Label>
        <Select
          value={config.reasoningEffort}
          onValueChange={(v) => set("reasoningEffort", v)}
          disabled={effortWhy !== null}
        >
          {/* Label association only, no aria-label: the existing dialect
              select in this pane pairs Label htmlFor with SelectTrigger id,
              and two labelling mechanisms on one control is one too many. */}
          <SelectTrigger id="pg-effort">
            <SelectValue placeholder="default" />
          </SelectTrigger>
          <SelectContent>
            {EFFORTS.map((e) => (
              <SelectItem key={e} value={e}>{e}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </GatedField>

      <GatedField reason={budgetWhy}>
        <Label htmlFor="pg-budget">Budget</Label>
        <Input
          id="pg-budget" type="number" placeholder="tokens"
          disabled={budgetWhy !== null}
          value={config.reasoningBudget}
          onChange={(e) => set("reasoningBudget", e.target.value)}
        />
      </GatedField>
    </div>
  )
}
```

- [ ] **Step 4: Write `structured-output.tsx`**

```tsx
import { Label, Textarea } from "darkraise-ui"
import { GatedField } from "./gated-field"
import { reasonFor } from "../dialect-support"
import { parseSchema } from "../lib/request"
import type { PlaygroundConfig } from "../config"

/**
 * A schema, never a switch.
 *
 * Two of the three edges honour structured output only when a schema is
 * present -- OpenAI drops a bare {"type":"json_object"}, and Gemini reads
 * responseSchema while ignoring responseMimeType. A JSON-mode toggle would
 * therefore be a control that did nothing on the dialect most operators use.
 */
export function StructuredOutput({
  config,
  onChange,
}: {
  config: PlaygroundConfig
  onChange: (next: PlaygroundConfig) => void
}) {
  const why = reasonFor(config.dialect, "schema")
  const { error } = parseSchema(config.schemaRaw)

  return (
    <GatedField reason={why}>
      <Label htmlFor="pg-schema">Response schema</Label>
      <Textarea
        id="pg-schema" rows={4}
        placeholder='JSON Schema, e.g. {"type":"object","properties":{…}}'
        disabled={why !== null}
        value={config.schemaRaw}
        onChange={(e) => onChange({ ...config, schemaRaw: e.target.value })}
        className="font-mono text-sm"
      />
      {error ? <p className="text-sm text-[hsl(var(--destructive))]">{error}</p> : null}
    </GatedField>
  )
}
```

- [ ] **Step 5: Add both to the pane**

In `config-pane/config-pane.tsx`, add two `AccordionItem`s after the existing "Sampling" one, matching its structure:

```tsx
      <AccordionItem value="reasoning">
      <AccordionTrigger className="text-sm">Reasoning</AccordionTrigger>
      <AccordionContent className="pt-1">
        <Reasoning config={config} onChange={onChange} />
      </AccordionContent>
      </AccordionItem>

      <AccordionItem value="schema">
      <AccordionTrigger className="text-sm">Structured output</AccordionTrigger>
      <AccordionContent className="pt-1">
        <StructuredOutput config={config} onChange={onChange} />
      </AccordionContent>
      </AccordionItem>
```

- [ ] **Step 6: Run the tests**

```bash
cd web && npm test && npm run typecheck
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/playground/config-pane/
git commit -m "feat(web): add reasoning and schema controls"
```

---

### Task 6: Show what the provider dropped

Gating stops a control lying about the wire. It does not stop one lying about the provider — and the gateway already records when that happens.

**Background the implementer needs.** `internal/adapter/anthropic/build.go:111-145` drops temperature, top_p and top_k, each with an `ir.Warning`, when thinking is on or the model rejects non-default sampling. `internal/adapter/openaicompat/build.go:39-43` drops top_k. Those warnings are flattened by `ir.Warning.String()` into `"field -> target: reason"` (for example `"top_k -> openai: not expressible"`), stored, and served on the trace as `warnings?: string[]`. **Render the strings as they arrive — do not parse them back into a triple.** A reason containing `" -> "` would mis-split, and the format belongs to the Go side.

**Files:**
- Modify: `web/src/features/playground/message.tsx`
- Modify: `web/src/features/playground/message.test.tsx`

**Interfaces:**
- Produces: `TurnRoute` gains `warnings: string[]`. `routeFromTrace` populates it from `trace.warnings ?? []`.

- [ ] **Step 1: Write the failing test**

Append to `message.test.tsx`:

```tsx
describe("what the provider dropped", () => {
  it("carries the trace's warnings onto the turn", () => {
    const r = routeFromTrace(trace({ warnings: ["top_k -> openai: not expressible"] }))
    expect(r.warnings).toEqual(["top_k -> openai: not expressible"])
  })

  it("treats a trace without warnings as none, not as unknown", () => {
    // The field is optional on the wire; a run that dropped nothing simply
    // omits it.
    expect(routeFromTrace(trace()).warnings).toEqual([])
  })

  it("shows each warning under the answer it belongs to", () => {
    // A control the dialect accepted can still be dropped by the provider --
    // temperature alongside thinking, say. Silence there is the same lie the
    // dialect gating exists to prevent.
    render(
      <AssistantTurn
        text="an answer"
        route={route({ warnings: ["temperature -> anthropic: rejected alongside thinking"] })}
      />,
    )
    expect(screen.getByText(/rejected alongside thinking/i)).toBeInTheDocument()
  })

  it("renders the warning as sent, without re-splitting it", () => {
    // The string is the Go side's format. Parsing it back into field, target
    // and reason would mis-split any reason containing the separator.
    const odd = "stop -> gemini: not expressible -> see the adapter notes"
    render(<AssistantTurn text="a" route={route({ warnings: [odd] })} />)
    expect(screen.getByText(odd)).toBeInTheDocument()
  })

  it("says nothing when there are no warnings", () => {
    const { container } = render(<AssistantTurn text="a" route={route({ warnings: [] })} />)
    expect(container.textContent).not.toMatch(/dropped/i)
  })
})
```

Extend the existing `route()` helper in that file with `warnings: []` in its defaults, and the `trace()` helper is already spread-based so `warnings` passes through.

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- message
```

Expected: FAIL — `warnings` is not a property of `TurnRoute`.

- [ ] **Step 3: Carry the warnings onto the turn**

In `message.tsx`, add to `TurnRoute`:

```ts
  /** What an adapter accepted and then dropped, in the gateway's own wording.
   *  Flat strings rather than a triple: ir.Warning.String() owns the format,
   *  and re-splitting it here would break on any reason containing " -> ". */
  warnings: string[]
```

and in `routeFromTrace`'s returned object: `warnings: trace.warnings ?? [],`.

- [ ] **Step 4: Render them**

In `message.tsx`, add beneath `RouteLine`'s call site in `AssistantTurn`:

```tsx
        {route && route.warnings.length > 0 ? <TurnWarnings warnings={route.warnings} /> : null}
```

and the component:

```tsx
/**
 * Parameters the provider would not take.
 *
 * Distinct from an error: the request succeeded, and this is the part of it
 * that did not survive the trip. Drawn quietly, because a run with warnings is
 * still a run that worked.
 */
function TurnWarnings({ warnings }: { warnings: string[] }) {
  return (
    <ul className="flex flex-col gap-1">
      {warnings.map((w, i) => (
        <li key={i} className="flex items-start gap-1.5 text-sm text-[hsl(var(--legend))]">
          <TriangleAlert
            className="mt-0.5 size-[var(--icon-size,1rem)] shrink-0"
            aria-hidden="true"
          />
          <span className="font-mono">{w}</span>
        </li>
      ))}
    </ul>
  )
}
```

Add `TriangleAlert` to the existing `lucide-react` import.

- [ ] **Step 5: Run the tests**

```bash
cd web && npm test && npm run typecheck
```

Expected: PASS. The existing `message.test.tsx` assertions must still pass; only the `route()` helper's defaults changed.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/playground/message.tsx \
        web/src/features/playground/message.test.tsx
git commit -m "feat(web): show what the provider dropped"
```

---

### Task 7: Redraw the playground mockup

Spec §11 makes stage 2's gate a mockup fragment rewritten in place.

**Files:**
- Modify: `docs/ux/mockups/fragments/11-playground.html`

- [ ] **Step 1: Read the pipeline's rules**

```bash
cd docs/ux/mockups && sed -n '1,60p' qa.py
```

The two hard rules: a fragment declares no colour of its own (every hex is a gate failure — colour lives in `darkrouter-ui.css`), and nothing is fetched from the network.

- [ ] **Step 2: Redraw the fragment**

Rewrite `fragments/11-playground.html` to show the screen as it now is: the full-height frame, the config pane carrying Model, Dialect, and the Sampling / Reasoning / Structured output sections, with **two controls visibly disabled and their reasons shown** (Top K and Structured output under the anthropic dialect are the clearest pair), and one assistant turn whose route line is followed by a dropped-parameter warning.

Keep the existing fragment's chrome (rail, header) — copy it from the current file rather than inventing it. Update the `<p class="legend">` at the top to describe what the screen now shows.

**Typography:** the fragment must not introduce a font size below the scale's floor. Use the same classes the surrounding fragments use.

- [ ] **Step 3: Run the gate**

```bash
cd docs/ux/mockups && python3 qa.py && python3 build.py
```

Expected: both clean. `qa.py` fails loudly on a colour literal or a network fetch.

- [ ] **Step 4: Commit**

```bash
git add docs/ux/mockups/fragments/11-playground.html \
        docs/ux/mockups/index.html docs/ux/mockups/artifact.html
git commit -m "docs(ux): redraw the playground mockup"
```

---

### Task 8: Verify live, then deploy

**Files:** none, until the record commit.

- [ ] **Step 1: Full gate**

```bash
cd web && npm test && npm run typecheck && npm run build
go build ./... && go vet ./... && go test -count=1 ./internal/...
```

Expected: all clean.

- [ ] **Step 2: Build and deploy with the UAT overlay**

The overlay is required — `compose.prod.yml` alone sets `pull_policy: always` and would discard the local build.

```bash
docker build -t darkraise/darkrouter:latest .
docker compose -f compose.prod.yml -f compose.uat.yml up -d darkrouter
docker ps --filter name=darkrouter --format '{{.Names}}\t{{.Status}}'
curl -s http://localhost:8091/healthz
```

Use port **8091**; 8081 belongs to another container on this machine.

- [ ] **Step 3: Confirm the served bundle is this build**

```bash
asset=$(curl -s http://localhost:8091/ | grep -o 'assets/index-[A-Za-z0-9_-]*\.js')
curl -s "http://localhost:8091/$asset" > /tmp/served.js
cmp /tmp/served.js internal/admin/dist/assets/index-*.js && echo "deploy matches source"
```

- [ ] **Step 4: Look at it**

Password from `.uat-credentials`; never copy it into a tracked file. At **1600×1000** and **1280×800**, **light and dark**:

| Check | What passing looks like |
|---|---|
| D3 | Switch the dialect to anthropic: Top K enables, Structured output disables with its reason beneath it. Switch to openai: the reverse |
| Value retention | Type 40 into Top K on anthropic, switch to openai — the box stays disabled but still reads 40 |
| Reasoning | Effort is live on openai and Budget on anthropic, never both |
| Pane height | The pane still scrolls its own contents; the accordion sections do not push the pane past the frame |
| D5 | A run whose provider dropped a parameter shows the warning under the route line |

Any failure is a defect in Tasks 4–6, not a new task: fix it there, re-run the suite, redeploy.

- [ ] **Step 5: Record the gate**

Append a "Stage 2 result" section to this plan recording what each criterion showed, then:

```bash
git add docs/superpowers/plans/2026-08-29-playground-stage2-controls.md
git commit -m "docs(playground): record the stage 2 gate"
```

---

## Notes for whoever picks this up

**The matrix is the spine.** Task 1's table is read by the request builder (Task 2), mirrored by the Go builders (Task 3), and rendered by the pane (Tasks 4 and 5). If those three disagree, a control lies in one direction or another. The Go tests in Task 3 and the web tests in Task 2 assert the same facts from opposite sides deliberately.

**Two lies, not one.** Dialect gating stops a control lying about the *wire*. Task 6 stops it lying about the *provider* — a temperature the pane happily enabled still goes into the void when Anthropic's adapter drops it alongside thinking. Both halves are needed for the stage's claim to hold.

**Structured output resists the obvious implementation.** Every reference product ships a JSON-mode boolean. Here it would do nothing on two of three dialects, because both edges require a schema to be present. If a task starts to feel like it wants a switch, re-read spec §7.2.

**A disabled control keeps its value.** That is tested in Task 4 and checked live in Task 8, because it is what makes a preset non-lossy in stage 3 — the stage that adds presets is the one that will discover if this was got wrong.
