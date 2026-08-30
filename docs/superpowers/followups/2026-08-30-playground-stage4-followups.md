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

## 1. A removed Compare column still bills

**The only open item that costs money.** Removing a column mid-stream aborts
the client's fetch — which correctly stops the orphan writing into the UI and
fixed a related Run-re-enables bug — but the provider call runs to completion
server-side.

Measured at the live gate: three `POST /api/playground` requests, all `200 OK`
at full duration (2436/2489/2513 ms), with the removed column's trace never
shown anywhere in the UI.

The cancellation chain reads as intact on both sides: `stream()` passes the
signal to `fetch` (`web/src/lib/api.ts`), `handlePlayground` derives from
`r.Context()` (`internal/admin/playground.go:272`), and `attempt` builds the
outbound request from a context descended from it (`internal/exec/exec.go`).
So the remaining suspicion is the browser/HTTP layer — a fetch aborted after
headers have arrived does not necessarily close a keep-alive HTTP/1.1
connection, and a network panel shows the request as completed either way.

**This needs a server-side log line, not another code review.** Log where the
executor's context is cancelled, remove a column mid-stream, and see whether
the server ever hears about it. It either closes, or it is a fetch-layer
limitation to document and accept.

## 2. `npm run lint` is broken

ESLint 9 with no `eslint.config.js` anywhere under `web/`. The command fails
loudly with the migration-guide error rather than silently passing, so nobody
is relying on a false green — but the project has no working lint gate.

Adding a config may surface a large backlog at once. Worth its own scoped
decision rather than being swept into something else.

## 3. Pre-existing, unrelated to stage 4

- `internal/admin/cursor.go` is not gofmt-clean (unformatted on `master`
  before this branch).
- `ConfigBlocks` in `web/src/lib/api-types.ts` omits the `media` block that
  `internal/admin/configapi.go` emits on `GET /api/config`. Harmless —
  TypeScript simply does not know the field exists. Fold into whichever change
  adds `media:` to `darkrouter.example.yaml`, which is also absent.
- `qa.py`'s colour check only catches hex literals, so `rgb()` and named
  colours would pass a gate that claims "no colour literal in a fragment".
- Settings' "Signed-in browsers" rows all read "since Invalid Date".
- Several mockup stylesheet headers cite plan task numbers ("Task 16 —
  Connect"; both `16-login.css` and `17-first-run.css` claim "Task 18"), which
  `CLAUDE.md`'s comment rule forbids. They predate this branch and refer to a
  different plan's numbering.

## 4. Deferred minors from the per-task reviews

Each was rated Minor by its reviewer and parked. Grouped by kind.

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
