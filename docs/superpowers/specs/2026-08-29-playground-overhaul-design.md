# Playground overhaul — Chat and Lab

**Status:** design, 2026-08-29. Awaiting review.
**Master design:** `2026-08-22-darkrouter-design.md`
**Builds on:** `2026-08-24-darkrouter-phase10-operator-console.md`, which established the Warm
Console design language, the mockup pipeline, and the playground this document replaces.
**Supersedes:** phase 10's playground screens — mockup fragments `11-playground.html` and
`12-playground-compare.html`, and the implementation in `web/src/features/playground/`.
**References surveyed:** OmniRoute (`src/app/(dashboard)/dashboard/playground/`) and 9router
(`src/app/(dashboard)/dashboard/basic-chat/`), both at `/root/repositories-community`.

---

## 1. Goal

The playground is the one screen where an operator makes the gateway do the thing it exists to do,
and it is the screen that reads least like it was designed. Three problems, in the order they hurt.

**It does not occupy the page.** Measured against the running console at 1600×1000, the entire
instrument stops 570px down and leaves roughly 450px of empty background beneath it. The config
pane's left border ends in mid-air, the composer floats in the middle of the page rather than
sitting at its foot, and the empty state is centred in a short band instead of in the space it has.
The screen was built as a document that flows, when what it wants to be is an application layout
that fills its frame.

**It asks one surface to do two jobs.** A tab strip of Chat, Compare, Auxiliary and Count, with a
config pane pinned beside all four, serves the operator who is testing a routing decision. It does
not serve the operator who wants to talk to a model for twenty minutes and come back to the
conversation tomorrow. Today the second operator gets a transcript that vanishes on reload, no way
to name or retrieve a session, and a settings column taking a fifth of the width they wanted for
reading.

**It exposes a fraction of what the gateway already carries.** `ir.Request` holds `TopP`, `TopK`,
`StopSequences`, `ResponseFormat` and `Reasoning`, every one of them parsed by at least two of the
three inbound edges — and the playground offers temperature, max tokens, and nothing else. The
reasoning controls in particular are a capability the router has had since phase 4 and has never
been able to demonstrate.

## 2. Scope boundary

**In:** a two-mode playground — Chat and Lab — replacing the four-tab screen; a full-height layout;
a conversation history rail with server-side persistence; named request presets with server-side
persistence; sampling controls covering every parameter the IR actually carries, gated per dialect;
a structured-output schema editor; reasoning controls; Compare grown from two fixed panels to N
columns; new mockup fragments; the admin endpoints and migration the two persisted features need.

**Out:** code export (curl/Python/TypeScript snippets). Considered and deliberately dropped — the
Connect screen already generates client snippets against the router's base URL, and a second
generator on a second screen is two things to keep in step.

**Out:** a prompt-improver. It would make the console originate LLM calls of its own, against a
model chosen by the console rather than the operator, and spend the operator's money on a request
they did not compose.

**Out:** image and file attachments in Chat. `playgroundMessage.Content` is a `string` end to end,
and multimodal content would mean changing that type, the three dialect body builders, and the
message rendering — a feature in its own right, not part of a layout overhaul. `ir.Media` and
`BlockImage` already exist to build it on when it is wanted.

**Out:** presence penalty, frequency penalty and seed. See §6.3 — these are not reachable without
changing `ir.Request` and every edge and adapter that reads it, and two of the three dialects have
no equivalent field. Adding them would be core routing surgery to serve three sliders.

## 3. The two modes

The mode switch lives in the `PageHeader` row, right-aligned, as a two-item `ToggleGroup`. It
persists per operator in `localStorage`; the URL carries it as `?mode=` so a link opens where the
sender meant. `PageHeader` itself stays, unchanged in kind, because every other screen has one and
a playground that drops it reads as a different application.

### 3.1 Chat mode

Three regions inside the mode's full height.

**The history rail**, left, 260px, collapsible to nothing. It lists conversations newest first, each
showing its title, the first line of the most recent user turn, and a relative timestamp. A `New`
action sits at its foot. This is 9router's pattern and it is the right one: a chat surface without
retrievable history is a scratchpad, and nobody keeps a scratchpad.

**The transcript**, centre, `max-w-3xl` and centred within its column, scrolling in its own
container. Above it sits a slim strip carrying the model pill — a button that opens the existing
`ModelCombobox` in a popover — the conversation title, inline-editable, and an overflow menu holding
rename, delete, and *open this configuration in Lab*.

**The composer**, pinned to the foot of the region rather than sticky within a scrolling page. The
existing auto-growing `Textarea`, Enter-to-send and Shift+Enter-for-newline behaviour is kept
as-is; it is the one part of the current screen that is already right.

Chat mode shows no config pane and no metrics strip. The two settings a conversation genuinely needs
are reachable without one: model and dialect from the model pill's popover, the system prompt from
the overflow menu. Everything else belongs to Lab.

### 3.2 Lab mode

Sub-tabs Single, Compare, Auxiliary and Count, in a `TabsList` sized to its content, with the config
pane pinned right and the metrics strip pinned beneath the tabs.

The metrics strip **reserves its height from the start** in Lab, rendering em dashes until a run
produces readings. This reverses the current behaviour, and deliberately: `hasReadings` exists
because a row of em dashes above an empty *chat* is furniture, which was true when chat and
instrument shared one surface. In a mode whose entire purpose is measurement, a strip that appears
after the first run shifts the transcript down at exactly the moment the operator is reading it.

`Single` is the current Chat tab without the history rail — one conversation, disposable, with the
full route line under every answer.

## 4. Layout mechanics

The fix is small and the measurement is what makes it safe to make.

`main.dr-sidebar-layout-content` — darkraise-ui's `SidebarLayout` content element — computes to
`display: block`, `flex: 1 1 0%`, `overflow: auto`, and a definite height of 944px inside a
`dr-sidebar-layout-main` column that is `overflow: hidden` at viewport height. The height is
therefore already definite, and a child asking for `h-full` resolves against it. Nothing in the
component library blocks a full-height screen; the playground simply never asked.

`PlaygroundScreen` becomes `flex h-full min-h-0 flex-col`. Every region that scrolls does so in its
own `min-h-0 overflow-y-auto` container. The consequence worth stating: `main` stops scrolling and
the transcript starts, which is what makes a pinned composer and a pinned metrics strip possible at
all.

Below `lg`, the config pane moves from a right column to a `Sheet`, and the Chat history rail from
a left column to a `Sheet`. A 320px pane and a 260px rail either side of a transcript is three
columns on a laptop and an unusable squeeze on anything narrower.

## 5. What survives from the current screen

Named because an overhaul that discards them would be a worse screen, not a better one.

`message.tsx`'s `RouteLine` is the reason this playground is not a generic chat window: the provider
mark in the gutter, the served `provider/model`, and the failover badge naming what was tried first.
It stays. In Chat mode it quiets to the mark plus the duration, expanding on click to the full
line — cost, tokens, and the trace link. In Lab it is always expanded.

`metrics.tsx`'s `LatencySplit` — one bar split where the first token arrived — stays unchanged. It
is the reading that separates a slow provider from a slow generation and no reference product has
anything like it.

`traceWhenWritten`'s retry loop stays. The log writer batches on a 250ms timer, the race is real,
and the comment explaining it should outlive this overhaul.

`drainSSE` and the per-dialect extractors stay untouched. They are verified against the edge writers
and there is no reason to reopen them.

## 6. Sampling parameters

### 6.1 The constraint

A parameter reaches a provider only if the playground sends it, the **inbound edge parses it into
`ir.Request`**, and the outbound adapter writes it. The middle step is the binding one and it is
where the reference products' parameter sets stop being a useful guide — OmniRoute talks to
providers directly, darkrouter routes through an IR.

Verified by reading `internal/ir/ir.go:204`, `internal/edge/openai/parse.go`,
`internal/edge/anthropic/parse.go` and `internal/edge/gemini/parse.go`:

| Control | `ir.Request` | openai edge | anthropic edge | gemini edge |
|---|---|---|---|---|
| Temperature | `Temperature` | `temperature` | `temperature` | `temperature` |
| Max tokens | `MaxTokens` | `max_tokens` | `max_tokens` | `maxOutputTokens` |
| Top P | `TopP` | `top_p` | `top_p` | `topP` |
| Top K | `TopK` | — not read | `top_k` | `topK` |
| Stop sequences | `StopSequences` | `stop` | `stop_sequences` | `stopSequences` |
| Structured output | `ResponseFormat` | `response_format` (json_schema only) | — not read | `responseMimeType` + `responseSchema` |
| Reasoning effort | `Reasoning.Effort` | `reasoning_effort` | — | — |
| Reasoning budget | `Reasoning.Budget` | — | `thinking.budget_tokens` | `thinkingConfig.thinkingBudget` |
| Presence/frequency penalty | **absent** | — | — | — |
| Seed | **absent** | — | — | — |

### 6.2 Dialect-aware controls

Every cell marked "—" is a control that would render, accept a value, and change nothing. The
codebase already refuses that class of lie — `aux-panels.tsx` keeps `catalogSurface` beside
`surface` precisely so a model filter does not silently match nothing — and this screen should
refuse it too.

A control the current dialect cannot carry renders **disabled, with the reason in a tooltip**:
"Top K is not part of the OpenAI chat wire. Switch the dialect to Anthropic or Gemini to send it."
It is not hidden. Hiding it makes the control appear and disappear as the dialect changes, which
reads as a bug; disabling it teaches the operator something true about the three wires, which is
most of what this screen is for.

Two cases need naming beyond the table:

**Structured output is a schema, not a switch.** `internal/edge/openai/parse.go:131` honours
`response_format` only when its type is `json_schema` *and* a schema is present — a bare
`{"type":"json_object"}` is parsed and dropped. A "JSON mode" toggle, which is what OmniRoute ships,
would therefore do nothing at all on the dialect most operators use. The control is a schema
editor: a `Textarea` validated as JSON on every keystroke, with the same inline error treatment
`parseTools` already uses for the tools field.

**Reasoning is one control with two renderings.** OpenAI takes an effort tier, Anthropic and Gemini
take a token budget, and `ir.Reasoning` holds both. The pane shows a segmented low/medium/high on
the OpenAI dialect and a budget input on the other two, under one "Reasoning" heading, so the
operator learns that these are the same idea rather than three unrelated fields.

### 6.3 Why penalties and seed are out

They are absent from `ir.Request`. Adding them means a new field on the IR, a parse in the OpenAI
edge, a write in every outbound adapter, and a `Warning` from every adapter that cannot express
them — which is all of them except OpenAI-compatible. That is a change to the core routing path,
reviewed on the core routing path's terms, to serve three controls that two of three dialects
cannot carry. If they are wanted they should be their own change with its own justification, not a
rider on a UI overhaul.

### 6.4 Go changes

`playgroundBody` in `internal/admin/playground.go` gains `TopP *float64`, `TopK *int`,
`Stop []string`, `ResponseSchema json.RawMessage` and `Reasoning *playgroundReasoning`. Each of the
three body builders writes what its wire carries and drops the rest — the same shape the existing
builders already have for tools, where `geminiPlaygroundBody` refuses rather than silently dropping.

The refusal precedent matters: `playgroundRequest` already errors on Gemini plus tools rather than
sending a request the operator would misread. Parameters follow the softer rule instead — dropped,
because the UI has already disabled them, so a value can only arrive here from a hand-made request
or a preset saved under another dialect. §7.2 covers the preset case.

## 7. Persistence

### 7.1 Presets

A preset is a named request configuration: model, dialect, system prompt, tools, and every sampling
parameter. Loading one replaces the config pane's state wholesale.

A preset saved under one dialect can be loaded under another, and its parameters may then include
values the new dialect cannot carry. The preset is **not** rewritten on load. It keeps what it
stored and the pane disables what the current dialect cannot send, with the disabled control still
showing the stored value. Silently dropping the value would make a preset quietly lossy every time
it round-tripped through a dialect it was not written for.

### 7.2 Conversations, and prompt text at rest

**This is the decision in this document with consequences outside this screen.**

Phase 10 §2 put request/response body capture explicitly out of scope, on the grounds that
"wiring the writer is its own decision about storing prompt content on disk". `capture.bodies` and
its retention sweep exist and nothing writes `request_bodies`; the trace drawer's Bodies panel is
permanently empty and says so. Saved conversations would make the playground the first place in
darkrouter where prompt text is retained at rest.

The decision taken here, and it should be reviewed as a decision rather than absorbed as a detail:

- Chat mode **auto-saves**. A history rail behind an explicit Save button does not get used, and a
  conversation the operator has to remember to keep is one they will lose.
- Lab mode's Single, Compare, Auxiliary and Count tabs persist **nothing**. They are for one-off
  runs and their transcripts are disposable by design.
- Conversations persist until deleted. There is no retention sweep, because unlike the request log
  this table grows only when a person types into it.
- Settings gains a switch, `playground.save_conversations`, default on. Turning it off stops new
  writes and purges every stored conversation, in one action with one confirmation.
- The stored text is the operator's own prompts and the models' replies. It is not client traffic,
  and nothing in this design starts capturing that.

This is separable from the rest of the overhaul. If the reviewer wants prompt-at-rest to stay a
closed question, the history rail becomes session-scoped `localStorage` and everything else in this
document is unchanged.

### 7.3 Schema

New migration `internal/store/migrations/0014_playground.sql`, following the `STRICT` table
convention every migration since `0001` uses.

```sql
CREATE TABLE playground_presets (
  id         TEXT PRIMARY KEY,
  name       TEXT    NOT NULL,
  dialect    TEXT    NOT NULL,
  model      TEXT    NOT NULL,
  config     TEXT    NOT NULL,  -- JSON: system, tools, sampling, reasoning
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX idx_playground_presets_name ON playground_presets(name);

CREATE TABLE playground_conversations (
  id         TEXT PRIMARY KEY,
  title      TEXT    NOT NULL,
  model      TEXT    NOT NULL,
  dialect    TEXT    NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_playground_conversations_updated
  ON playground_conversations(updated_at DESC);

CREATE TABLE playground_messages (
  id              TEXT PRIMARY KEY,
  conversation_id TEXT    NOT NULL
                    REFERENCES playground_conversations(id) ON DELETE CASCADE,
  seq             INTEGER NOT NULL,
  role            TEXT    NOT NULL,
  content         TEXT    NOT NULL,
  -- The request whose trace explains this turn. Nullable: a turn can be
  -- stored before its trace is written, and a trace is swept on the log's
  -- retention schedule long before the conversation is deleted.
  request_id      TEXT,
  created_at      INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX idx_playground_messages_seq
  ON playground_messages(conversation_id, seq);
```

`request_id` is the join that keeps darkrouter's identity in a saved conversation: reopening one
from last week and clicking through to the trace is the feature no chat client has. It is nullable
and the UI must treat a missing trace as ordinary, because the request log's retention sweep will
outlive plenty of conversations.

### 7.4 Endpoints

Registered in `internal/admin/admin.go` beside the existing three playground routes, all behind
`requireCSRF` for the mutating verbs.

```
GET    /api/playground/presets
POST   /api/playground/presets
PATCH  /api/playground/presets/{id}
DELETE /api/playground/presets/{id}

GET    /api/playground/conversations           list, newest first, no messages
POST   /api/playground/conversations
GET    /api/playground/conversations/{id}      with messages
PATCH  /api/playground/conversations/{id}      title only
DELETE /api/playground/conversations/{id}
POST   /api/playground/conversations/{id}/messages
DELETE /api/playground/conversations           purge, for the settings switch
```

Store methods land in `internal/store/playground.go`; the HTTP layer in
`internal/admin/playgroundstore.go`, separate from `playground.go` so the file that synthesizes
proxy requests does not also become the file that does CRUD.

## 8. Compare, as N columns

Compare today is two fixed `Side` values and two `SidePanel`s. It becomes a list, with add and
remove, a per-column status dot — idle, streaming, done, error — and per-column metrics, following
OmniRoute's `CompareColumn` closely because the shape is right.

Two things stay from the current implementation and are worth stating so a rewrite does not lose
them. `chatBody` is **shared, not rebuilt**: every column sends the identical request but for its
model, so a difference in the transcripts is the models and not a second slightly different request
shape. And the columns run concurrently, which is the only way the latency numbers beside them mean
anything.

Columns are capped at four. Beyond that each is too narrow to read a wrapped answer in, and the
comparison the screen exists for stops being possible.

## 9. Files

```
web/src/features/playground/
  playground-screen.tsx      rewritten — mode router, ~60 lines
  mode.ts                    new — mode type, URL and localStorage persistence
  config.ts                  extended — the full parameter set
  dialect-support.ts         new — the §6.1 matrix as data, plus per-control reasons
  chat/
    chat-mode.tsx            new — the three-region layout
    history-rail.tsx         new
    conversation-header.tsx  new — model pill, title, overflow menu
    composer.tsx             new — extracted from chat.tsx unchanged in behaviour
    transcript.tsx           new — extracted from chat.tsx
  lab/
    lab-mode.tsx             new — sub-tabs, config pane, metrics strip
    single.tsx               from chat.tsx, minus the composer and transcript extractions
    compare.tsx              rewritten — N columns
    compare-column.tsx       new
  config-pane/
    config-pane.tsx          rewritten
    preset-picker.tsx        new
    sampling.tsx             new — sliders and inputs
    reasoning.tsx            new
    structured-output.tsx    new
  aux-panels.tsx             unchanged
  metrics.tsx                unchanged but for the reserved-height flag
  message.tsx                extended — quiet and expanded RouteLine
  markdown*.tsx              unchanged
  lib/
    stream.ts                new — drainSSE and the dialect extractors, moved verbatim
    presets.ts               new — queries and mutations
    conversations.ts         new — queries and mutations

internal/store/migrations/0014_playground.sql   new
internal/store/playground.go                    new
internal/admin/playgroundstore.go               new
internal/admin/playground.go                    extended — §6.4
internal/admin/admin.go                         extended — route registration
```

`chat.tsx` is dissolved. Its SSE draining and dialect extractors move to `lib/stream.ts` untouched;
its component halves become `transcript.tsx` and `composer.tsx`. The file is 451 lines doing four
jobs and it is the reason the current screen is hard to change.

## 10. Mockups gate implementation

Phase 10 established that mockups gate implementation, and this overhaul is large enough to earn
the same gate. Fragments `11-playground.html` and `12-playground-compare.html` are replaced by:

- `11-playground-chat.html` — Chat mode, mid-stream, history rail populated, one answer showing a
  quiet route line and one expanded.
- `12-playground-lab.html` — Lab mode on Single, config pane open with a preset loaded, two
  controls disabled with their dialect reasons visible, metrics strip populated.
- `12b-playground-compare.html` — Lab mode on Compare, four columns, one streaming, one errored.

`build.py`, `qa.py` and `check.py` are unchanged and their gates apply as they stand: no colour
literal in a fragment, nothing fetched from the network, and a screenshot per screen in both
themes. The `index.html` table of contents and `docs/ux/DONE-CRITERIA.md` are updated to match.

## 11. Testing

**Console.** Vitest under the **threads pool** — the default fork pool silently skips half this
suite. New coverage: the dialect-support matrix as a pure table test; preset round-trip through a
dialect that cannot carry one of its parameters, asserting the value survives; conversation
auto-save writing exactly one message per turn; the compare column cap; mode persistence across a
reload. Existing `chat.test.ts`, `metrics.test.ts`, `message.test.tsx`, `markdown.test.tsx` and
`aux-panels.test.ts` must pass unchanged — the logic they cover is being moved, not rewritten, and
any of them needing an edit is a signal that something was lost.

**Go.** Table tests over `playgroundRequest` asserting, per dialect, exactly which of the new
parameters appear in the synthesized body and which are dropped — the §6.1 matrix, executable.
Store CRUD including the `ON DELETE CASCADE` and the purge. The existing playground tests stand.

**Live.** Per `CLAUDE.md`, a layout change cannot be verified from tests. Both modes are checked in
the running console at 1600×1000 and at 1280×800, in light and dark, with the sidebar collapsed and
expanded, before the change is called done. Then redeploy, per the same file.

## 12. The adjacent bug

`web/src/features/playground/chat.tsx:238` guards the streaming auto-scroll with:

```js
window.innerHeight + window.scrollY >= document.body.offsetHeight - 160
```

`body` is `overflow: hidden` at exactly viewport height — the scroll container is `main`, not the
window. So `window.scrollY` is always 0 and `document.body.offsetHeight` always equals
`window.innerHeight`, making the test `1000 >= 840`: permanently true. The guard that is supposed to
stop yanking the operator back to the bottom while they read something earlier has never fired.

The fix falls out of §4: once the transcript is its own scroll container, the check reads that
element's `scrollTop`, `clientHeight` and `scrollHeight`. It is called out separately because it is
a real defect with a real symptom, and it should be fixed as its own commit rather than absorbed
silently into a layout rewrite.

## 13. Decisions taken

| # | Decision | Alternative rejected |
|---|---|---|
| 1 | Two modes, Chat and Lab | One density toggle across four tabs; no tabs at all |
| 2 | `PageHeader` retained, mode toggle inside it | Dropping it for a bespoke top bar — breaks shell consistency |
| 3 | Conversations auto-save server-side | Explicit save; `localStorage` only |
| 4 | Parameters limited to what `ir.Request` carries | Adding penalties and seed to the IR |
| 5 | Structured output is a schema editor | A JSON-mode boolean, which the OpenAI edge drops |
| 6 | Unsupported controls disabled with a reason | Hidden per dialect; or shown and silently dropped |
| 7 | Presets keep values a dialect cannot send | Rewriting the preset on load, making it lossy |
| 8 | Compare capped at four columns | Unbounded |
| 9 | Code export dropped | Duplicating the Connect screen's generator |

## 14. Open decisions

None. The one that was open — §7.2, whether the playground may retain prompt text at rest — was put
to the owner during design and approved on 2026-08-29, with the auto-save, the settings switch and
the purge as described. It is recorded in §7.2 at length rather than in a line here because it
reverses a boundary phase 10 drew deliberately, and a future reader finding saved prompts on disk
should be able to find out why without reconstructing it.
