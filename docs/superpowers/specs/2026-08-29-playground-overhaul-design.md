# Playground overhaul — Chat and Lab

**Status:** design, 2026-08-29. Reviewed and restructured into four stages; see §3 and §15.
**Delivery:** four separately shippable stages. Stages 3 and 4 can be cut without stranding
anything already built.
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
persistence; sampling controls covering every sampling parameter the IR actually carries, gated per
dialect (§7.1 names the two IR fields deliberately excluded); the adapter warnings that say when a
parameter was accepted and then dropped upstream; a structured-output schema editor; reasoning
controls; Compare grown from two fixed panels to N columns; new mockup fragments; the admin
endpoints, migration and settings key the two persisted features need.

All of it is **in** as a destination; §3 splits it into four separately shippable stages, of which
the last two can be cut without stranding anything already built.

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

**Out:** presence penalty, frequency penalty and seed. See §7.3 — they are absent from `ir.Request`
and from every edge's inbound wire struct, so reaching them means widening the narrow waist every
request passes through. Core routing surgery to serve three sliders.

## 3. Sequence

This document describes one destination and **four separately shippable stages** that reach it. Each
ends deployed, verified in the running console, and useful on its own. None of them leaves the
screen in a half-built state, and the last two can be cut without stranding anything already built.

The decomposition works because of an asymmetry worth stating plainly: **Lab mode is the current
screen done properly, and Chat mode is the genuinely new thing.** The four tabs that exist today
already *are* the instrument — they are simply in a frame that does not fit them, missing most of
their controls, and unable to remember anything. So stages 1 to 3 finish the screen that exists,
without introducing a mode switch at all, and stage 4 adds the second mode beside it.

### Stage 1 — The screen fills its frame

The full-height layout (§5) and the scroll-container defect (§13). The four tabs, the config pane and
the metrics strip keep their current contents and move into regions that fit the viewport: the
composer pinned to the foot, the transcript scrolling in its own container, empty states occupying
the space they are given.

Nothing is added. This is the stage that fixes what is wrong every time the page is opened, and it
is deliberately first for that reason — it is a day's work standing in front of several weeks of it.

**Gate:** live verification at 1600×1000 and 1280×800, both themes, sidebar collapsed and expanded.
No mockup: there is no new design here, only the existing one finally occupying its frame.

### Stage 2 — The instrument gets its controls

The full sampling set (§7), the per-dialect gating that stops a control lying about the wire, the
structured-output schema editor, the reasoning controls, and the adapter warnings (§7.4) that say
when a parameter was accepted and then dropped upstream.

This is the highest ratio of capability to risk in the document. It touches no storage, adds no
endpoint, and surfaces two things the gateway has carried since phase 4 and never shown: reasoning
effort and budget, and the warnings recording what each adapter dropped.

**Gate:** a mockup fragment for the rebuilt config pane and the warnings region, then live
verification.

### Stage 3 — The instrument remembers, and compares

Named presets (§8.1) with their table and endpoints, and Compare grown from two fixed panels to N
columns (§9).

The first stage that touches the database, and deliberately the smaller of the two that do: one
table, four endpoints, no new data category, no settings key, nothing to reverse.

**Gate:** the preset picker folded into stage 2's config-pane fragment; a fragment for Compare.

### Stage 4 — The second mode

The Chat/Lab switch, the history rail, saved conversations (§8.2) with their tables, endpoints and
settings key, and the quiet route line.

Everything built in stages 1 to 3 becomes Lab mode unchanged — the config pane, the metrics strip
and the four tabs move inside it without being rewritten. Chat mode is built alongside.

This stage is last because it carries the most risk for the least certain return: it introduces
prompt text at rest, reverses a boundary phase 10 drew deliberately, and its payoff — returning to a
conversation days later — is the one this document is least able to predict the value of. If it is
cut, stages 1 to 3 stand as a complete and coherent playground rather than as three quarters of one.

**Gate:** mockup fragments for both modes, then live verification.

### What this changes about the rest of the document

Nothing in the design. §4 onward describes the destination, and the stage that delivers each part is
marked in §10's file list. Where a section describes something that only exists once Chat mode does —
the mode switch, the history rail — it is describing stage 4.

## 4. The two modes

The mode switch lives in the `PageHeader` row, right-aligned, as a two-item `ToggleGroup`. It
persists per operator in `localStorage`; the URL carries it as `?mode=` so a link opens where the
sender meant. `PageHeader` itself stays, unchanged in kind, because every other screen has one and
a playground that drops it reads as a different application.

### 4.1 Chat mode

Three regions inside the mode's full height.

**The history rail**, left, 260px, collapsible to nothing. It lists conversations newest first, each
showing its title, the first line of the most recent user turn, and a relative timestamp. A `New`
action sits at its foot. This is 9router's pattern and it is the right one: a chat surface without
retrievable history is a scratchpad, and nobody keeps a scratchpad.

**The transcript**, centre, `max-w-3xl` and centred within its column, scrolling in its own
container. Above it sits a slim strip carrying the model pill — a button that opens the existing
`ModelCombobox` in a popover, with the dialect select beside it — the conversation title,
inline-editable, and an overflow menu holding *edit the system prompt*, *open this configuration in
Lab*, rename and delete.

**The composer**, pinned to the foot of the region rather than sticky within a scrolling page. The
existing auto-growing `Textarea`, Enter-to-send and Shift+Enter-for-newline behaviour is kept
as-is; it is the one part of the current screen that is already right.

Chat mode shows no config pane and no metrics strip. The two settings a conversation genuinely needs
are reachable without one: model and dialect from the model pill's popover, the system prompt from
the overflow menu. Everything else belongs to Lab.

### 4.2 Lab mode

Sub-tabs Single, Compare, Auxiliary and Count, in a `TabsList` sized to its content, with the config
pane pinned right and the metrics strip pinned beneath the tabs.

The metrics strip **reserves its height from the start** in Lab, rendering em dashes until a run
produces readings. This reverses the current behaviour, and deliberately: `hasReadings` exists
because a row of em dashes above an empty *chat* is furniture, which was true when chat and
instrument shared one surface. In a mode whose entire purpose is measurement, a strip that appears
after the first run shifts the transcript down at exactly the moment the operator is reading it.

`Single` is the current Chat tab without the history rail — one conversation, disposable, with the
full route line under every answer.

## 5. Layout mechanics

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

One detail that will otherwise be discovered as a mystery gap: `.dr-sidebar-layout-content` carries
its own `p-6`. A composer "pinned to the foot of the region" sits 24px above the visual bottom
unless the screen negates that padding. The current `chat.tsx` composer already does this with
`-mx-6 -mb-6` and a comment explaining it; the new layout should negate the padding once, at the
screen root, rather than at each pinned edge.

Below `lg`, the config pane moves from a right column to a `Sheet`, and the Chat history rail from
a left column to a `Sheet`. A 320px pane and a 260px rail either side of a transcript is three
columns on a laptop and an unusable squeeze on anything narrower.

## 6. What survives from the current screen

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

## 7. Sampling parameters

### 7.1 The constraint

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
| Structured output | `ResponseFormat` | `response_format` (json_schema only) | — not read | `responseSchema` only |
| Reasoning effort | `Reasoning.Effort` | `reasoning_effort` | — | — |
| Reasoning budget | `Reasoning.Budget` | — | `thinking.budget_tokens` | `thinkingConfig.thinkingBudget` |
| Presence/frequency penalty | **absent** | — | — | — |
| Seed | **absent** | — | — | — |

Two IR fields are edge-parsed and adapter-written and are deliberately **not** controls on this
screen. `ParallelToolCalls` belongs with the tools editor rather than with sampling, and is
meaningless until tools are defined. `Safety` is Gemini-only, is a policy decision rather than a
sampling one, and a four-category threshold matrix in a sampling pane would dominate it. Both are
named here because §2 promises "every parameter the IR carries" and silence would read as an
oversight rather than a choice.

### 7.2 Dialect-aware controls

Every cell marked "—" is a control that would render, accept a value, and change nothing. The
codebase already refuses that class of lie — `aux-panels.tsx` keeps `catalogSurface` beside
`surface` precisely so a model filter does not silently match nothing — and this screen should
refuse it too.

A control the current dialect cannot carry renders **disabled, with the reason in a tooltip**:
"Top K is not part of the OpenAI chat wire. Switch the dialect to Anthropic or Gemini to send it."
It is not hidden. Hiding it makes the control appear and disappear as the dialect changes, which
reads as a bug; disabling it teaches the operator something true about the three wires, which is
most of what this screen is for.

A disabled element fires no pointer events, so the tooltip attaches to a wrapper around the control
rather than to the control itself. Stated because it is a trap that reliably costs an hour.

Two cases need naming beyond the table:

**Structured output is a schema, not a switch.** `internal/edge/openai/parse.go:131` honours
`response_format` only when its type is `json_schema` *and* a schema is present — a bare
`{"type":"json_object"}` is parsed and dropped. Gemini behaves the same way from the other side:
`internal/edge/gemini/parse.go:88` declares `responseMimeType` in the wire struct, but the IR
mapping at `parse.go:171` reads only `responseSchema`, so a mime type without a schema is likewise
parsed and dropped. A "JSON mode" toggle, which is what OmniRoute ships, would therefore do nothing
at all on two of the three dialects. The control is a schema editor: a `Textarea` validated as JSON
on every keystroke, with the same inline error treatment `parseTools` already uses for the tools
field.

**Reasoning is one control with two renderings.** OpenAI takes an effort tier, Anthropic and Gemini
take a token budget, and `ir.Reasoning` holds both. The pane shows a segmented low/medium/high on
the OpenAI dialect and a budget input on the other two, under one "Reasoning" heading, so the
operator learns that these are the same idea rather than three unrelated fields.

### 7.3 Why penalties and seed are out

They are absent from `ir.Request`, and absent from every edge's inbound wire struct. Adding them
means a new field on the IR, a parse in the OpenAI and Gemini edges, a write in the adapters that
can express them, and a `Warning` from the Anthropic adapter, which cannot.

Be accurate about the reason, because the obvious version of this argument is wrong: **Gemini's API
does carry `presencePenalty`, `frequencyPenalty` and `seed`** in `generationConfig`, and OpenAI
carries all three. Only Anthropic genuinely lacks them. The case for dropping them is not that the
providers cannot take them — it is that darkrouter's IR is the narrow waist every request passes
through, and widening it is a change to the core routing path reviewed on the core routing path's
terms. Doing that as a rider on a UI overhaul is how a narrow waist stops being narrow.

One apparent shortcut is not one. `ir.Request.Extra` exists and looks like a passthrough channel,
but nothing in any edge or adapter reads or writes it — it is a vestigial field, not an escape
hatch, and routing a parameter through it would mean building the passthrough as well.

If these are wanted they should be their own change with its own justification.

### 7.4 The other half of the lie: adapter warnings

Gating controls per dialect stops a control lying about the *wire*. It does not stop one lying about
the *provider*, and the gateway already knows when that happens.

`internal/adapter/anthropic/build.go:111-145` drops temperature, top_p and top_k — each with an
`ir.Warning` naming the field, the target and the reason — when thinking is on, or when the model is
one that rejects any non-default sampling parameter. `internal/adapter/openaicompat/build.go:39-43`
drops top_k as having no equivalent, and `build.go:53-60` downgrades a reasoning budget to the
nearest effort band. So on the Anthropic dialect, a temperature the pane happily enabled goes into
the void the moment reasoning is switched on, and today nothing on this screen says so.

The warnings already ride the trace, and the trace types already declare them — but **as flat
strings, not as a structured triple**, which is worth stating because the obvious implementation
assumes otherwise. `ir.Warning{Field, Target, Reason}` is flattened by `Warning.String()`
(`internal/ir/ir.go:255`) into `"field -> target: reason"` — for example
`"top_k -> openai: not expressible"` — collected by `warningStrings` (`internal/exec/exec.go:1063`),
persisted as `warnings_json`, and served on the trace as `warnings?: string[]`
(`web/src/lib/api-types.ts:150`, optional). The screen therefore renders the strings as they
arrive. It must not parse them back into a triple: a reason containing `" -> "` or `": "` would
mis-split, and the client would be duplicating a format only the Go side owns.

They render **under the answer they belong to**, beneath the route line, in the same quiet register.
Per turn rather than per screen, because a transcript of six answers may have warnings on only one
of them, and because the route line above already establishes the pattern of "what happened to this
turn". This is the natural completion of §7.2's principle rather than a new feature — a control that
was accepted, sent, and then dropped upstream is exactly as misleading as one that was never on the
wire, and this is the only screen in the console positioned to show it.

The trace is already fetched for every run, so this costs no new request.

Chat mode shows a single quiet marker when a run produced warnings, expanding to the list on click.
The full treatment belongs to the instrument.

### 7.5 Go changes

`playgroundBody` in `internal/admin/playground.go` gains `TopP *float64`, `TopK *int`,
`Stop []string`, `ResponseSchema json.RawMessage` and `Reasoning *playgroundReasoning`. Each of the
three body builders writes what its wire carries and drops the rest — the same shape the existing
builders already have for tools, where `geminiPlaygroundBody` refuses rather than silently dropping.

The refusal precedent matters: `playgroundRequest` (`internal/admin/playground.go:71-75`) already
errors on Gemini plus tools rather than sending a request the operator would misread. Parameters
follow the softer rule instead — dropped, because the UI has already disabled them, so a value can
only arrive here from a hand-made request or a preset saved under another dialect. §8.1 covers the
preset case.

## 8. Persistence

### 8.1 Presets

A preset is a named request configuration: model, dialect, system prompt, tools, and every sampling
parameter. Loading one replaces the config pane's state wholesale.

A preset saved under one dialect can be loaded under another, and its parameters may then include
values the new dialect cannot carry. The preset is **not** rewritten on load. It keeps what it
stored and the pane disables what the current dialect cannot send, with the disabled control still
showing the stored value. Silently dropping the value would make a preset quietly lossy every time
it round-tripped through a dialect it was not written for.

### 8.2 Conversations, and prompt text at rest

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

**Its relationship to `capture.bodies`.** These are now two knobs governing prompt text at rest and
they must not be silently unrelated. An operator who turned body capture off for privacy reasons
would reasonably expect that stance to cover the playground. They stay separate settings, because
they govern genuinely different things — `capture.bodies` records *other people's* traffic passing
through the gateway, `playground.save_conversations` records *the operator's own* typing — but the
settings screen groups them under one heading that says exactly that, so the distinction is offered
rather than left to be inferred.

**Why the text is stored in plaintext.** The same database holds provider credentials encrypted at
rest (`provider_keys.ciphertext`/`nonce`), so plaintext here is a choice and should read as one. The
threat model differs: a credential is a live capability an attacker can use against a third party,
and it is encrypted so that a leaked database file does not become a leaked API key. A test prompt
is content, not capability; encrypting it would protect it from an attacker who already has the
database file and the ability to read every other table in it, while costing the ability to search
or inspect conversations with `sqlite3`. If the operator's prompts are sensitive enough to need
encryption at rest, the honest answer is the switch, not a cipher.

**Settings plumbing.** `configFields` in `internal/admin/configapi.go:33` is a deliberate allowlist,
not reflection — the comment says so and names the credential it exists to keep out — so the switch
needs a new key in `internal/config`, an entry in that list, and a control on the settings screen.
The purge is the UI's `DELETE /api/playground/conversations` call, not a side effect of the config
value changing. Config is file-backed and reloadable, and a setting whose *reload* deletes data
would mean an edit to a file on disk silently destroying the operator's history. Flipping the key in
the file stops new writes; it does not delete what is already there.

This is separable from the rest of the overhaul. If the reviewer wants prompt-at-rest to stay a
closed question, the history rail becomes session-scoped `localStorage` and everything else in this
document is unchanged.

### 8.3 Schema

Two migrations, split along the stage boundary in §3 so that stage 4 stays cuttable —
`0014_playground.sql` carries the presets table in stage 3, `0015_playground_conversations.sql` the
two conversation tables in stage 4. Both follow the `STRICT` convention every migration since `0001`
uses, and `PRAGMA foreign_keys` is set in the DSN (`internal/store/db.go:50`), so the cascade below
does fire.

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
  -- The rest of the request that shaped every answer below: system prompt,
  -- tools, sampling, reasoning. Same JSON shape as playground_presets.config.
  -- Without it a conversation reopened tomorrow loses the system prompt that
  -- produced its transcript, and "open this configuration in Lab" has nothing
  -- to open -- which is the quiet lossiness section 7.1 refuses for presets.
  config     TEXT    NOT NULL,
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

### 8.4 Endpoints

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
PATCH  /api/playground/conversations/{id}      title, model, dialect, config
DELETE /api/playground/conversations/{id}
POST   /api/playground/conversations/{id}/messages
DELETE /api/playground/conversations           purge, for the settings switch
```

Go's `ServeMux` registers the exact literal `DELETE /api/playground/conversations` alongside the
wildcard `.../{id}` and routes the literal correctly, so the purge and the single delete coexist.

Store methods land in `internal/store/playground.go`; the HTTP layer in
`internal/admin/playgroundstore.go`, separate from `playground.go` so the file that synthesizes
proxy requests does not also become the file that does CRUD.

### 8.5 Behaviour the endpoints do not settle

Small decisions that will otherwise each be made twice, differently.

**Title.** A new conversation is created titled "New chat" and retitled from the first user turn once
it completes — truncated at 52 characters on a word boundary, 9router's rule, which is a good one. A
title the operator has edited is never overwritten.

**Mid-conversation changes.** Switching model or dialect part-way through `PATCH`es the conversation
row. The transcript keeps the turns that came before; each answer's route line already records what
actually served it, so a conversation that changed models mid-way stays readable rather than
claiming one model answered everything.

**Duplicate preset names.** `idx_playground_presets_name` is unique, so the store returns a conflict
and the save dialog offers to overwrite the existing preset rather than reporting a database error.

**List size.** The conversations list is capped at the 200 most recent, ordered by `updated_at`. Past
that the rail is not the right retrieval tool and search would be a different feature; the cap is
stated so it is a decision rather than the point where the query gets slow.

**Empty conversations.** A conversation with no messages — created, then abandoned — is deleted when
the rail next loads. Nothing is lost and the alternative is a rail that fills with "New chat".

## 9. Compare, as N columns

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

## 10. Files

The stage that delivers each file is marked `[1]`–`[4]`, per §3. A file listed under more than one
stage is touched again there; nothing is rewritten twice.

```
web/src/features/playground/
  playground-screen.tsx      [1] full-height frame  [4] becomes the mode router
  mode.ts                    [4] mode type, URL and localStorage persistence
  config.ts                  [2] extended — the full parameter set
  dialect-support.ts         [2] the §7.1 matrix as data, plus per-control reasons
  chat/
    chat-mode.tsx            [4] the three-region layout
    history-rail.tsx         [4]
    conversation-header.tsx  [4] model pill, dialect, title, overflow menu
    composer.tsx             [1] from chat.tsx, unchanged in behaviour
    transcript.tsx           [1] from chat.tsx
  lab/
    lab-mode.tsx             [4] wraps the stage 1-3 tabs; no rewrite of them
    single.tsx               [1] composer + transcript + useChatRun
    compare.tsx              [3] rewritten — N columns
    compare-column.tsx       [3]
    warnings.tsx             [2] §7.4, the adapter warnings for a run
  config-pane/
    config-pane.tsx          [1] moved into its region  [2] rebuilt inside
    preset-picker.tsx        [3]
    sampling.tsx             [2] sliders and inputs
    reasoning.tsx            [2]
    structured-output.tsx    [2]
  aux-panels.tsx             unchanged throughout
  metrics.tsx                [1] reserved-height flag
  message.tsx                [2] warnings marker  [4] quiet RouteLine
  markdown*.tsx              unchanged throughout
  lib/
    stream.ts                [1] drainSSE, extractUnaryText, dialect extractors
    request.ts               [1] chatBody, parseTools, seedFromTrace
    use-chat-run.ts          [1] the send loop, shared by every sending surface
    presets.ts               [3] queries and mutations
    conversations.ts         [4] queries and mutations

web/src/features/settings/settings-catalog.ts   [4] the save switch

internal/store/migrations/0014_playground.sql   [3] presets  [4] conversations
internal/store/playground.go                    [3] presets  [4] conversations
internal/admin/playgroundstore.go               [3] presets  [4] conversations
internal/admin/playground.go                    [2] extended — §7.5
internal/admin/configapi.go                     [4] configFields entry
internal/config/*.go                            [4] the new key
internal/admin/admin.go                         [3] [4] route registration
```

**Two migrations, not one.** `0014_playground.sql` creates `playground_presets` in stage 3;
`0015_playground_conversations.sql` creates the conversation tables in stage 4. Splitting them is
what keeps stage 4 cuttable — a single migration would put the prompt-at-rest tables on disk in
stage 3, in service of a feature that might never ship, and migrations do not come back off.

**Stage 1 does the file surgery.** Dissolving `chat.tsx`, extracting the hook and the two component
halves, and moving the five pure functions all happen in the first stage, before anything new is
built on them. Doing it later would mean building stage 2's controls against a file that is about
to be taken apart.

**`chat.tsx` is dissolved, and every one of its exports needs a stated home** — it has five, and
three of them have importers outside the file today (`config-pane.tsx:15` takes `parseTools`,
`compare.tsx:7` takes `chatBody` and `drainSSE`). `drainSSE` and `extractUnaryText` go to
`lib/stream.ts`; `chatBody`, `parseTools` and `seedFromTrace` to `lib/request.ts`. All five move
verbatim.

**The send loop is a hook, not a duplicated block.** `send()`, `appendToLastMessage`, the abort
controller, the TTFT measurement and the `traceWhenWritten` follow-up (`chat.tsx:239-330`) are
needed identically by Chat mode and by Lab's Single tab. They become `useChatRun`. Copying them into
two files would guarantee the two surfaces drift, and this is precisely the logic §12 wants moved
rather than rewritten.

**The `?seed=` flow keeps working, in Lab.** `trace-drawer.tsx:158-162` links "Open in playground"
with `search={{ seed: trace.id }}`, and `chat.tsx:210-221` consumes it. A seed is a routing
investigation, so it opens **Lab / Single**, and the consuming effect moves there with
`seedFromTrace`. The existing note explaining that a trace carries no prompt text — only model and
dialect are recoverable — moves with it unchanged; it is the only thing standing between the
operator and a transcript that is mysteriously empty.

## 11. Mockups gate implementation

Phase 10 established that mockups gate implementation. That gate applies per stage rather than up
front, because a mockup drawn for a stage that gets cut is work thrown away, and because stage 1
introduces no new design to draw.

**Stage 1 — no fragment.** There is no new design: it is the existing one occupying its frame. The
gate is live verification, and the existing `11-playground.html` stays as it is so the mockup set
does not describe a screen that no longer exists mid-sequence.

**Stage 2 — `11-playground.html` is rewritten in place.** The rebuilt config pane with two controls
disabled and their dialect reasons visible, the metrics strip populated, and one adapter warning
beneath it.

**Stage 3 — `12-playground-compare.html` is rewritten in place**, four columns, one streaming and
one errored, with the preset picker visible in the pane.

**Stage 4 — one fragment is added**, `13-playground-chat.html`: Chat mode mid-stream, history rail
populated, one answer with a quiet route line and one expanded. The existing `13`–`17` shift up by
one, and `index.html`'s table of contents and `docs/ux/DONE-CRITERIA.md` are updated to match.

Rewriting two fragments in place and adding one, rather than replacing both and adding two, keeps
the numbering churn to the single stage that actually needs it — and if stage 4 is cut, no
renumbering happens at all.

`build.py`, `qa.py` and `check.py` are unchanged and their gates apply as they stand: no colour
literal in a fragment, nothing fetched from the network, and a screenshot per screen in both themes.

**One warning for whoever builds these.** `qa.py:45` enforces a font-size *ceiling* only, so it will
not catch a size below the floor, and the two reference products are saturated with exactly that:
OmniRoute's playground components carry over a hundred `text-xs` and `text-[11px]` occurrences —
`StudioConfigPane.tsx` alone has twelve — and 9router's chat client has seven. `CLAUDE.md` forbids
both outright: 14px (`text-sm`) is the floor and only the predefined scale is allowed. Every layout
borrowed from those files must be translated to the `text-*` scale, and hierarchy below body text
comes from colour and weight instead. This is the single easiest way for this overhaul to import a
rule violation, and neither the mockup gate nor a typecheck will stop it.

## 12. Testing

**Console.** Vitest as configured, with no `pool` pinned.

An earlier draft of this section required the **threads pool**, on the belief that the default fork
pool silently skips part of this suite. That was measured on 2026-08-29 and is **false** at the
version in the lockfile: `npx vitest run` under `--pool=forks` and under `--pool=threads` both run
51 files and 517 tests, all passing. The requirement is withdrawn rather than kept as harmless
insurance — a config line justified by a false premise, carrying a comment asserting that premise,
is worse than no line. Recorded here because the claim is the kind that gets re-derived from folklore
every time a suite looks short.

New coverage, by the stage that adds it:

| Stage | Tests |
|---|---|
| 1 | The extracted pure functions still behave (the moved suites); the scroll-follow guard actually declining to follow when the operator has scrolled up — the assertion the current code cannot make |
| 2 | The dialect-support matrix as a pure table test; a control disabled for the current dialect not appearing in the request body; adapter warnings rendering for a run that produced them |
| 3 | Preset round-trip through a dialect that cannot carry one of its parameters, asserting the value survives; the compare column cap |
| 4 | A conversation reopened with its system prompt intact; auto-save writing exactly one message per turn; mode persistence across a reload; the purge |

**The gate on existing tests, stated so it is achievable.** `chat.test.ts:2` imports `parseTools`,
`chatBody`, `seedFromTrace`, `drainSSE` and `extractUnaryText` from `./chat` — the file §10 deletes.
Those five tests therefore *must* change: their import lines move to `lib/request.ts` and
`lib/stream.ts`. The gate is that **nothing but the import lines changes**. Every assertion in
`chat.test.ts`, `metrics.test.ts`, `message.test.tsx`, `markdown.test.tsx` and `aux-panels.test.ts`
stands as written, and an assertion needing an edit is the signal that behaviour was lost in the
move. The earlier phrasing — "must pass unchanged" — was impossible by §10's own file layout and
would have fired on day one for a reason that signalled nothing.

**Go.** Stage 2: table tests over `playgroundRequest` asserting, per dialect, exactly which of the
new parameters appear in the synthesized body and which are dropped — the §7.1 matrix, executable.
Stage 3: preset store CRUD. Stage 4: conversation store CRUD including the `ON DELETE CASCADE` and
the purge. The existing playground tests stand throughout.

**Live, and it ends each stage rather than the project.** Per `CLAUDE.md`, a layout change cannot be
verified from tests. Every stage is checked in the running console at 1600×1000 and at 1280×800, in
light and dark, with the sidebar collapsed and expanded, and then redeployed with the documented
`compose.uat.yml` overlay before it is called done. A stage that has not been deployed and looked at
is not finished, and that is the point of splitting them: four chances to discover the design is
wrong instead of one.

## 13. The adjacent bug

`web/src/features/playground/chat.tsx:207` guards the streaming auto-scroll with:

```js
window.innerHeight + window.scrollY >= document.body.offsetHeight - 160
```

darkraise-ui's layout root is `flex h-screen overflow-hidden`, so the window never scrolls — the
scroll container is `main`, not the window. `window.scrollY` is therefore always 0 and
`document.body.offsetHeight` always equals `window.innerHeight`, making the test `1000 >= 840`:
permanently true. The guard that is supposed to stop yanking the operator back to the bottom while
they read something earlier has never fired.

The fix falls out of §5: once the transcript is its own scroll container, the check reads that
element's `scrollTop`, `clientHeight` and `scrollHeight`. It is called out separately because it is
a real defect with a real symptom, and it should be fixed as its own commit rather than absorbed
silently into a layout rewrite.

## 14. Decisions taken

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
| 10 | Adapter warnings surfaced beside the run | Leaving a control that was sent and then dropped upstream indistinguishable from one that worked |
| 11 | Conversations store their full config, not just model and dialect | A conversation that silently loses its system prompt on reopen |
| 12 | `playground.save_conversations` and `capture.bodies` stay separate, grouped under one heading | One combined switch; or two unrelated switches with no explanation |
| 13 | Conversation text stored in plaintext | Encrypting it as credentials are — protects only against an attacker who already has the database |
| 14 | The send loop is a shared hook | Duplicating it into Chat and Lab Single, which would drift |
| 15 | `?seed=` opens Lab / Single | Chat mode — a seed is a routing investigation, not a conversation |
| 16 | Four separately shippable stages, layout first | One plan delivered whole, which puts the daily annoyance behind several weeks of work that might be cut |
| 17 | Two migrations, split on the stage boundary | One migration, which would put the prompt-at-rest tables on disk in service of a stage that might never ship |
| 18 | Mockups drawn per stage, none for stage 1 | Drawing all of them up front, where a cut stage wastes the work and stage 1 has no new design to draw |

## 15. Review history

| Artifact | Reviewer | Outcome |
|---|---|---|
| This spec, first draft | 1 × Fable, read-only | 2 Critical, 8 Important, 8 Minor. All verified against source and all applied. The factual survey held: of roughly twenty checkable claims, seventeen verified exactly. |

The two Critical findings were both real and both would have surfaced on the first day of
implementation. The test gate in §12 was impossible as written — `chat.test.ts` imports five symbols
from the file §10 deletes, so "must pass unchanged" could not hold. And `playground_conversations`
had no column for the system prompt, so the auto-save feature §8.2 reverses a phase 10 boundary to
provide would have silently dropped the setting that shaped every answer it stored.

The most valuable finding was not a defect but a completion: §7.4 exists because the review pointed
out that gating controls per dialect stops a control lying about the wire while leaving it free to
lie about the provider — and that the gateway already records exactly that, in warnings no screen
displays.

**Restructured into stages, 2026-08-29, after review.** The first draft described one destination and
implied one delivery. The owner's objection was that the change worth having soonest — the screen
occupying its frame — sat behind several weeks of work of much less certain value, and that the two
persisted features were the least justified parts of the document. §3 is the answer: the same
design, sequenced so the daily annoyance is fixed first and the riskiest feature is last and
cuttable. Nothing in §4 onward changed to accommodate it, which is the test of whether a
decomposition is honest.

## 16. Open decisions

None. The one that was open — §8.2, whether the playground may retain prompt text at rest — was put
to the owner during design and approved on 2026-08-29, with the auto-save, the settings switch and
the purge as described. It is recorded in §8.2 at length rather than in a line here because it
reverses a boundary phase 10 drew deliberately, and a future reader finding saved prompts on disk
should be able to find out why without reconstructing it.
