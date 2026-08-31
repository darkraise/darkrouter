# Playground stage 4 — open follow-ups

Everything left behind after stage 4 merged (`ef46f43`). Reconstructed from
the per-task reviews and the final whole-branch review at the end of the
session that built it.

**Nothing here blocks anything.** The stage is merged, deployed and verified.
These are the items that were deliberately deferred, plus findings that were
adjacent to the work rather than part of it.

Two decisions already taken, recorded so they are not re-litigated:

- **Presets are not brought under `playground.save_conversations` or the
  purge.** The key governs retention that happens *without the operator
  asking*, which is what spec §8.2's argument is about. A preset is
  deliberately named, saved one at a time, listed in the picker and
  individually deletable — a different category. The three places that
  claimed conversations were "the first place prompt text is retained at
  rest" were corrected instead, because that claim was false: `toStoredConfig`
  strips only `model` and `dialect`, so a preset's `system` prompt has been in
  `playground_presets.config` since stage 3.
- **The mockup set is exempt from the console's 14px font floor** and this is
  written into `CLAUDE.md`. It never loads darkraise-ui and never
  participates in the font-size axis, so a pixel size there cannot opt out of
  a setting an operator changed. `qa.py`'s 30px ceiling is the only limit.

---

## 1. A removed Compare column still bills — RESOLVED, not a defect

**Diagnosed 2026-08-31 with server-side logging. The premise was wrong: the
provider call *is* cancelled.** This item carried forward the live gate's
pre-fix measurement. `158c69c fix(playground): abort a compare column's
request` landed after that gate ran, and nobody re-measured.

Method: a temporary log line in `handlePlayground` recording when
`r.Context()` is cancelled and when the handler returns, against a real
three-column Compare run through Groq, with column 3 removed 1202 ms in by a
synchronous `browser_evaluate` click sequence.

```
DIAG playground …703404: start
DIAG playground …703404: ctx DONE after 1.200626054s err=context canceled
DIAG playground …703404: handler RETURNED after 1.200666981s
DIAG playground …655824: handler RETURNED after 2.261813516s   (kept column)
DIAG playground …614537: handler RETURNED after 2.386607127s   (kept column)
```

The removed column's context is cancelled at the instant of removal and the
handler returns immediately; the two kept columns run their full ~2.3 s. Two
controls confirm the reading: a `curl` killed mid-stream cancels at 1.996 s,
and a bare `fetch`/`AbortController` in the console page cancels at 1.521 s,
matching the client's own abort timestamp to the millisecond. The chain is
intact end to end, and the browser's fetch abort does close the connection.

The request row for the removed column corroborates it: `total_ms` 1200 rather
than the ~2.4 s of its siblings, and the warning
`passthrough -> groq/…: upstream connection failed after commit: context
canceled` — which is `forwardStream`'s post-commit read error, i.e. the cancel
reaching the executor's own read loop.

**Adjacent finding, raised and settled 2026-08-31 — a post-commit cancel is a
success. Decided; do not re-litigate.**

A client-cancelled stream is recorded as `status: "success"` with
`tokens_in`/`tokens_out` 0 and `cost_micros` 0. `internal/exec/forward.go`
treats a post-commit read failure as `OutcomeSuccess` — failover is impossible
once bytes are on the wire — so `exec.go`'s `actionFinish` writes "success",
and the usage chunk, which arrives last, never arrived.

That classification stands. Bytes reached the client and the answer on screen
is real, which is what "success" says; a cancel is the client's own choice,
not a fault of the route. `rec.Status = "cancelled"` and
`OutcomeClientCancelled` stay pre-commit, where they describe a request that
never delivered anything.

Nothing follows from the decision, because nothing else was recoverable.
`TokensIn` and `TokensOut` are non-nullable `int64`, so 0 is the only value
the row can carry, and `CostMicros` is priced from them. The provider does
bill for what it generated before the cut, so spend under-reports by that
much — a known and accepted gap, not a defect. No code change.

## 2. `npm run lint` is broken — RESOLVED

`web/eslint.config.js` now exists: `@eslint/js` recommended,
`typescript-eslint` recommended, and `eslint-plugin-react-hooks` recommended,
with `no-unused-vars` configured to allow the codebase's `^_` discard
convention (`toStoredConfig`'s two stripped columns, `drain`'s thrown-away
chunk). Both plugins are saved to `devDependencies`.

The backlog was small: **7 problems across 6 of 163 files**, and 0 errors once
the `^_` convention is configured. `npm run lint` exits 0. What remains is 4
warnings, none of them mechanical:

- three `react-hooks/exhaustive-deps` (`chat-tab.tsx:34`, `chat-mode.tsx:107`,
  `playground-screen.tsx:46`) — all deliberate omissions in effects whose
  re-firing behaviour is the point. Adding the deps would change behaviour,
  so they are left visible rather than silenced or "fixed".
- one stale `eslint-disable no-unreachable` in
  `use-chat-run.test.tsx:147`, which typescript-eslint disables in favour of
  the compiler's own check.

## 3. Pre-existing, unrelated to stage 4

- ~~`internal/admin/cursor.go` is not gofmt-clean~~ — fixed 2026-08-31
  (`UntilMs` alignment); `gofmt -l internal/` is now empty.
- ~~`ConfigBlocks` omits the `media` block~~ — fixed 2026-08-31, together with
  the missing `media:` block in `darkrouter.example.yaml`, as the note asked.
- ~~`qa.py`'s colour check only catches hex literals~~ — fixed 2026-08-31.
  Adds the opaque colour functions and the CSS named colours; `rgba()` and
  `hsla()` stay legal, since the hex message already points at them for
  translucency a token cannot carry. Named colours are matched only where a
  value is expected, because a fragment's prose says "Orange" and a class can
  be named `.plum`. Comments are blanked first, offsets preserved, so the
  preset scrim's header can go on naming the near-black it was picked
  against. Probed both ways: four escape forms fail, five legal ones pass.
- ~~Settings' "Signed-in browsers" rows all read "since Invalid Date"~~ —
  fixed 2026-08-31. Root cause was a unit mismatch, not a formatting one:
  `CreateSession` writes `UnixMilli`, but `SessionRows` decoded with
  `time.Unix(sec, 0)` and filtered with `now.Unix()`. Every row landed in the
  year 58633, which `new Date()` cannot parse (a five-digit year needs the
  `+058633` form) — hence "Invalid Date". The seconds filter also meant
  `expires_at > ?` was true for every row, so **expired sessions were listed
  as revocable browsers**. `CreateSession`, `TouchSession` and the sweep were
  all already correct; `SessionRows` was the only seconds-based reader, so
  authentication was never affected. Pinned by two store tests.
- ~~Several mockup stylesheet headers cite plan task numbers~~ — fixed
  2026-08-31. All 13 `Task N — ` header prefixes stripped; each header already
  named its screen, so nothing was lost. `qa.py` still passes (19 fragments).
  The in-body cross-references followed on 2026-08-31, written as the screens
  they mean rather than stripped: Task 9 is Providers, Task 7 is Request
  trace, Task 12 is the model catalog, Tasks 3 and 4 are the design language
  board and the ladder specimen, and Task 2 is the shared kit, which owns no
  screen at all. `index.html` and `artifact.html` rebuilt.

## 4. Deferred minors from the per-task reviews

Each was rated Minor by its reviewer and parked. Grouped by kind.

**Triaged 2026-08-31.** Seven were done and the rest were left deliberately,
because several are defensible as they stand and doing them all would have
churned working code for no reader's benefit. Done:

- A reopened conversation now fetches each turn's trace and shows the same
  provider, duration, tokens and cost a fresh answer does. This was filed as
  a placeholder question; the placeholder was the symptom. A turn whose trace
  the log has swept now draws a settled mark rather than the dashed one, and
  a turn with no route at all keeps the dashed mark, because that one really
  is still arriving.
- A title typed before the first send is kept rather than overwritten by
  `titleFromPrompt`, with the create-promise memo folded in alongside it.
- The conversation list key is `["playground-conversations", "list"]`, so
  appending a turn no longer refetches the open conversation.
- A pending model commit is flushed on unmount rather than cancelled.
- The destructive GET says so on the handler.
- `relativeTime` moved to `lib/`, and `rows.Err()` is wrapped.
- Two tests: the 404s on an unknown conversation id, and
  `settings-catalog.test.ts`, which passed with its catalogue entry deleted.

**Second pass, 2026-08-31 — the rest resolved.** A stop before the first
token no longer stores a turn: an abort is now told apart from a completion,
so a half answer is still kept and a provider's genuinely empty answer is
still kept, but a run that streamed nothing is not. The quiet route line
gained a collapse, verified live. The mode sync now seeds from `initialMode`,
so Back to a bare `/playground` lands where a fresh load of that URL would —
and it sets the mode directly rather than routing through `choose`, which is
what stops Back writing the stored preference and stops it pushing `?mode=`
back onto the URL it just left. `load` clears `busy` itself rather than
waiting a microtask for the aborted run. The `save_conversations` default
moved out of the discovery cluster. The unmount abort's comment now names the
behaviour it carries.

The system-prompt dialog's focus return turned out not to be a defect: focus
does come back to the menu trigger. Held by a test now, so a Radix upgrade
that breaks it is caught.

Test coverage closed: the store-level update and its miss, the 200-row cap
and the 200-char preview, the save gate's error body, `mode.ts`'s two
`localStorage` catch branches, the `"routed"` fallback, the client's
saving-off path, and the popover's commit flush. `compare-abort`'s
near-tautological length check became one that discriminates — that removing
a never-run column aborts neither live column — and its helper clears each
box before naming it. `chat-mode`'s per-keystroke case types in one tick, so
a loaded runner or a smaller `COMMIT_QUIET_MS` can no longer fail it for a
reason unrelated to the behaviour.

Still left, deliberately: the `bodyFor` duplication, by its own rule that a
third caller has to appear first; the `emit` superseded-guard, which would
need a test written to fit it rather than to describe it; and the purge
button's position, defended twice and checked live.

### Behaviour worth a second look

- **A pending model commit is dropped on unmount rather than flushed.**
  `conversation-header.tsx` cancels the quiet-period timer on unmount, so
  typing a model name and switching to Lab within ~400 ms loses the change
  silently.
- **Navigating away mid-answer now persists the partial turn.** The unmount
  abort added in the fix wave means `onTurn` fires with whatever streamed.
  Consistent with Stop's documented semantics ("a half answer is still an
  answer") but a behaviour change the comment does not mention.
- **Back now writes the mode preference to `localStorage`**, because the sync
  effect routes through `choose`. Arguably right — Back is a deliberate
  navigation — but previously untrue. Back to a bare `/playground` with no
  `mode` param is not synced at all, since `isMode(undefined)` is false.
- **Stopping a run before the first token stores an empty assistant message.**
  Consistent with what is on screen, but a stored row with no content that is
  re-rendered forever.
- **Two narrow timing windows in auto-save.** `conversationRef` is written
  only after `create.mutateAsync` resolves, so a second exchange completing
  while the first create is still in flight would create two conversations;
  and turn appends from consecutive exchanges can interleave and scramble
  `seq`. Both need a local SQLite write to outlast a full model round trip. A
  create-promise memo closes the first.
- **Pre-send title edits are clobbered** — a title typed before the first send
  is not persisted, then overwritten by `titleFromPrompt`.
- **Every appended turn refetches the whole open conversation.**
  `keys.playgroundConversation(id)` is a prefix of `keys.playgroundConversations`,
  so each of the two `POST .../messages` per exchange invalidates the detail
  query too. Cost, not corruption — the load guard holds — but it grows with
  the conversation.
- **A reopened conversation shows a permanent "trace hasn't landed"
  placeholder.** `routesOfTurns` builds a route with `provider: ""`, and
  `AssistantTurn` draws the dashed pending-gutter whenever provider is empty,
  so every restored turn renders forever in a state that reads as still
  loading. Not inventing numbers is right; drawing a *loading-shaped* gutter
  for a turn that will never load is not. A settled-but-unknown mark would fix
  it; richer still, a restored turn whose trace has not been swept could fetch
  it, since the row stores `request_id` and `routeFromTrace` already exists.
- **The route-line disclosure is one-way** — once expanded there is no control
  to collapse it again. Matches §6 as written; a collapse affordance is a
  design decision.
- **`GET /api/playground/conversations` performs a delete** (the empty-conversation
  reap) and sits behind `requireSession` rather than `requireCSRF`. §8.5 asks
  for the behaviour and the blast radius is tiny, but a destructive GET is
  worth a comment on the handler, not only a note in the plan. It has already
  caught two test authors: a test that backdates `created_at` to control
  ordering has its own fixture deleted by the call under test.
- **The system-prompt dialog's close likely drops focus to `document.body`**
  rather than returning it to the menu trigger — the known cost of Radix's
  `preventDefault` pattern for opening a dialog from a menu item.

### Structure and consistency

- `HistoryRail` imports `relativeTime` from `web/src/features/providers/test-log-tab.tsx`
  — a shared formatting helper reached through another feature's tab
  component. Move it to `lib/`.
- The dialect-hazard comment and `conversationBody` in
  `playground/lib/conversations.ts` mirror `preset-picker.tsx`'s `bodyFor`
  verbatim. Worth a shared helper only if a third caller appears.
- `rows.Err()` in `PlaygroundConversationByID` is returned unwrapped while
  every sibling error path wraps with context.
- The `save_conversations` default in `applyDefaults` sits between
  `Discovery.Enabled` and `Discovery.Interval`, splitting the discovery
  cluster.

### Test coverage

- `UpdatePlaygroundConversation` has no store-level test (exercised through
  HTTP by the PATCH round trip).
- The 200-row conversation list cap and the 200-char preview truncation are
  untested.
- `PATCH` and `DELETE` on a missing conversation id return 404 in code but are
  unasserted; presets have a dedicated test for this, conversations do not.
- The save-gate test asserts the 403 status but not the specific error body.
- The `emit` superseded-guard in `useChatRun` is uncovered — reproducing it
  needs a stream chunk resolving in the same microtask as the `load()` call.
  Deliberately left uncovered rather than pinned by a test written to fit it.
- `mode.ts`'s two `localStorage` catch branches are untested.
- The model/dialect popover in `ConversationHeader` has no test.
- The `totalMs === null` → `"routed"` fallback in `RouteLine` is untested.
- The client's behaviour when saving is off (the 403 path) is verified live
  but pinned by nothing.
- `settings-catalog.test.ts`'s "reads the switch as On and Off" passes even
  with the catalogue entry deleted, because `settingRow` never consults
  `meta` when deriving `display`.
- `compare-abort.test.tsx`'s `expect(signals).toHaveLength(2)` is
  near-tautological; its `startARun` helper types into comboboxes that may
  already carry a model.
- `chat-mode.test.tsx`'s per-keystroke assertion depends on real timers
  staying under `COMMIT_QUIET_MS` — comfortable today, flaky on a loaded
  runner or if that constant drops.
- The model popover is uncontrolled, so the commit flush depends on Radix's
  outside-click ordering letting the close handler run before the underlying
  click. Current Radix behaviour, pinned by no test.

### Cosmetic

- `load()` leaves `busy` true until the aborted run's `finally` clears it.
- The destructive purge sits leftmost of three settings header actions. Two
  reviewers independently defended it — the rightmost slot is the habitual
  primary target, so leftmost keeps a destructive action away from muscle
  memory — and it was checked live. Left as built.
