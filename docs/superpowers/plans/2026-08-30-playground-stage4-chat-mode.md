# Playground Stage 4 — The Second Mode

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Add Chat mode beside the instrument — a conversation history rail whose conversations are stored server-side and survive a reload, a browser change and a week — while everything stages 1 to 3 built becomes Lab mode without being rewritten.

**Architecture:** Two tables, `playground_conversations` and `playground_messages`, joined by a cascading foreign key; the conversation carries the same opaque JSON `config` blob a preset does, so a reopened conversation restores the system prompt that shaped it. Chat mode auto-saves through a new optional `onTurn` callback on `useChatRun` rather than through logic inside the hook, because Lab's Single tab shares that hook and must persist nothing. A new `playground.save_conversations` config key gates the writes; it is read-only on the settings screen, and the purge that empties the tables is a separate confirmed action.

**Tech Stack:** Go 1.26 stdlib, `modernc.org/sqlite`; React 19, TanStack Router + Query, darkraise-ui 6.5.0 (`ToggleGroup`, `Sheet`, `Popover`, `DropdownMenu`, `Dialog`), Tailwind 4, vitest 4 + @testing-library/react.

**Spec:** `docs/superpowers/specs/2026-08-29-playground-overhaul-design.md` — §3 (stage 4), §4 (the two modes), §5 (layout mechanics), §6 (what survives), §8.2 (conversations, and prompt text at rest), §8.3 (schema), §8.4 (endpoints), §8.5 (behaviour the endpoints do not settle), §10 (files), §11 (mockups), §12 (testing).

**Stages 1, 2 and 3 are merged to `master` at `0aee480`.** The playground fills its frame, `PlaygroundConfig` carries thirteen fields, `dialect-support.ts` is a total table, `useChatRun` is the shared send loop, presets persist in `playground_presets`, and Compare runs up to four columns. This plan builds on all of it.

## Global Constraints

These apply to every task without restating.

- **TDD.** A failing test precedes the implementation it tests. Run it and see it fail before writing the code.
- **Gates before any commit.** Frontend tasks: `cd web && npm test && npm run typecheck` clean. Go tasks: `go build ./... && go vet ./...` clean and `go test -count=1 ./internal/...` clean. `go` is not on the default PATH; run `export PATH=$PATH:/usr/local/go/bin` first.
- **Never `text-xs`, never a custom size.** 14px (`text-sm`) is the floor; only `text-sm`, `text-base`, `text-lg`, `text-xl`, `text-2xl`, `text-3xl`. In a stylesheet use `var(--text-sm)`, never a pixel value. Hierarchy below body text comes from colour (`--legend`, `--muted-foreground`) and weight. The standalone mockup set under `docs/ux/mockups/` is the one exception, and Task 14 writes that exception down.
- **A conversation is never lossy.** The server stores the `config` blob as received and never decodes-and-remarshals it, exactly as it already does for a preset. The client reconstitutes it with `mergeStoredConfig` from `web/src/features/playground/preset-config.ts` — the same function, not a copy.
- **`playgroundConversations`, never `conversations` alone, in client code.** `keys.presets` and `usePresets` already mean the provider catalogue; `keys.playgroundPresets` is stage 3's. Every client symbol this plan adds says `playgroundConversation(s)`.
- **Never make a migration idempotent.** SQLite's `ADD COLUMN` has no `IF NOT EXISTS` form, and `TestMigrationRealignsTheProvidersShippedAsKeylessThatAreNot` builds a database at version 12 and replays forward, so every migration runs exactly once and none has to survive a replay. That path was tried and rejected on this project.
- **Existing test assertions do not change.** Two additive extensions are expected and are not changes: `TestMigrateCreatesEveryTable`'s `want` list gains two table names, and `configStoreFor` in `internal/admin/fixtures_test.go` gains a parameter. No assertion anywhere is rewritten.
- **Comments explain WHY, never WHAT.** No comment may reference this plan, a task number, or that something was recently added.
- **Commit subjects** are `<type>(<scope>): <subject>`, imperative, 50 characters or fewer measured across the whole line including type and scope, no trailing period. Stage explicit paths — never `git add -A`. English only.
- **Branch.** `playground-stage4`, already created from `master`.
- **No new dependencies.**
- **Never stage `providers.png`** (untracked at the repo root) or anything under `.playwright-mcp/` or `.superpowers/`.

## Baselines before Task 1

Measured on 2026-08-30 at `0aee480`. `web`: 59 files, 583 tests, all passing; `npm run typecheck` clean. Go: `go build ./...` and `go vet ./...` clean; `go test ./internal/...` passes 27 packages, `internal/edge` has no test files. Migrations run to `0014`; `//go:embed migrations/*.sql` picks up a new file with no registration step.

## The decision this stage carries

§8.2 was re-reviewed on 2026-08-30, before any of stage 4 was built, and upheld. Prompt text is retained at rest in SQLite, in plaintext, beside credentials that are encrypted. The ground on which it was upheld is worth carrying into the code review: the `localStorage` fallback also stores prompt text at rest, on the browser's disk, where no settings key governs it, no purge reaches it and `sqlite3` cannot inspect it. The fallback does not keep the question closed. Two consequences bind every task below — the writes are gated by a config key, and there is a purge that actually empties both tables.

§8.2 also gained decision 19 during that review: the key is **read-only** on the settings screen. `EDITABLE` in `web/src/features/settings/settings-catalog.ts` carries only `policy.*`, because every other key the gateway reads comes from `darkrouter.yaml` and has no write endpoint — which is why `capture.bodies` is a read-only row today. The purge is its own confirmed action in the settings `PageHeader`.

## File structure

```
internal/store/
  migrations/0015_playground_conversations.sql  T1  the two tables
  playground.go                                 T1  conversation + message store methods
  playground_test.go                            T1  store CRUD, cascade, purge, reap

internal/config/
  config.go                                     T2  PlaygroundConfig{SaveConversations *bool}
  load.go                                       T2  the default-on

internal/admin/
  configapi.go                                  T2  configFields entry + blocks entry
  playgroundstore.go                            T3  conversation CRUD handlers
                                                T4  purge handler + the save gate
  admin.go                                      T3  route registration
                                                T4  the purge route
                                                T5  the JSON 404 fallback, every verb
  routes_test.go                                T5  new file
  fixtures_test.go                              T4  a config-body parameter for the gate test

web/src/lib/
  api-types.ts                                  T6  PlaygroundConversation, PlaygroundConversationDetail
                                                T12 ConfigBlocks.playground
  queries.ts                                    T6  keys + the two read hooks

web/src/features/playground/
  lib/conversations.ts                          T6  mutations, title rule, config round trip
  lib/use-chat-run.ts                           T7  onTurn, and seeding a reopened conversation
  mode.ts                                       T8  PlaygroundMode, URL + localStorage
  playground-screen.tsx                         T8  becomes the mode router
  lab-mode.tsx                                  T8  the stage 1-3 tabs, unrewritten
  chat/history-rail.tsx                         T9
  chat/conversation-header.tsx                  T9
  chat/chat-mode.tsx                            T10 the three regions, and auto-save
  message.tsx                                   T11 the quiet RouteLine
  compare.tsx                                   T13 an AbortController per column

web/src/features/settings/
  settings-catalog.ts                           T12 the row, and the regrouped heading
  settings-screen.tsx                           T12 the purge action

docs/ux/mockups/
  fragments/13..17 -> 14..18                    T14 renumber
  css/12-playground-compare.css                 T14 prune, and 13px -> 14px
  fragments/13-playground-chat.html             T15 the new fragment
  css/13-playground-chat.css                    T15

CLAUDE.md                                       T14 the mockup font-size exception
```

**Where this diverges from §10's file list.** The spec drew stage 1 putting `composer.tsx`, `transcript.tsx` and `single.tsx` under `chat/` and `lab/`. Stage 1 as merged left them flat, and `chat.tsx` became `chat-tab.tsx`, `lib/request.ts`, `lib/stream.ts` and `lib/use-chat-run.ts`. The paths above are the ones on disk. Only Chat mode's three new components get a `chat/` directory, because they are genuinely new and belong together; nothing that already works is moved to match a diagram.

## Definition of Done

| # | Criterion | Verification |
|---|---|---|
| D1 | A conversation round-trips with its blob untouched, and deleting it takes its messages | `go test ./internal/store/ -run TestPlaygroundConversation` |
| D2 | `playground.save_conversations` defaults to on and a file `false` is not mistaken for unset | `go test ./internal/config/ -run TestSaveConversations` |
| D3 | With the key off, a write is refused and the read endpoints still answer | `go test ./internal/admin/ -run TestPlaygroundConversationsGate` |
| D4 | The purge empties both tables and answers 204 | `go test ./internal/admin/ -run TestPlaygroundConversationsPurge` |
| D5 | A mistyped `PATCH /api/…` answers JSON 404, not `index.html` | `go test ./internal/admin/ -run TestUnknownAPIPathAnswersJSON` |
| D6 | A reopened conversation restores its system prompt | `cd web && npm test -- conversations` |
| D7 | Auto-save writes exactly one user message and one assistant message per turn | `cd web && npm test -- chat-mode` |
| D8 | The mode survives a reload, and `?mode=` wins over the stored value | `cd web && npm test -- mode` |
| D9 | Removing a streaming Compare column aborts its request | `cd web && npm test -- compare` |
| D10 | The mockup set still builds and passes its gate at eighteen screens | `cd docs/ux/mockups && python3 qa.py && python3 build.py` |
| D11 | Verified live and deployed | UAT at 1600×1000 and 1280×800, both themes; container healthy, bundle byte-match |

---

### Task 1: The conversation tables and their store methods

**Files:**
- Create: `internal/store/migrations/0015_playground_conversations.sql`
- Modify: `internal/store/playground.go` (append; rename `newPresetID` to `newPlaygroundID` and update its one call site)
- Modify: `internal/store/playground_test.go` (append)
- Modify: `internal/store/migrate_test.go:21-27` (extend the `want` list)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `store.PlaygroundConversation{ID, Title, Dialect, Model string; Config json.RawMessage; CreatedAt, UpdatedAt time.Time; Preview string}`
  - `store.PlaygroundTurn{ID string; Seq int; Role, Content, RequestID string; CreatedAt time.Time}`
  - `(*DB).CreatePlaygroundConversation(ctx context.Context, title, dialect, model string, config json.RawMessage) (PlaygroundConversation, error)`
  - `(*DB).PlaygroundConversations(ctx context.Context) ([]PlaygroundConversation, error)`
  - `(*DB).PlaygroundConversationByID(ctx context.Context, id string) (PlaygroundConversation, []PlaygroundTurn, bool, error)`
  - `(*DB).UpdatePlaygroundConversation(ctx context.Context, id, title, dialect, model string, config json.RawMessage) (bool, error)`
  - `(*DB).DeletePlaygroundConversation(ctx context.Context, id string) (bool, error)`
  - `(*DB).AppendPlaygroundTurn(ctx context.Context, conversationID, role, content, requestID string) (PlaygroundTurn, error)`
  - `(*DB).PurgePlaygroundConversations(ctx context.Context) (int64, error)`
  - `(*DB).ReapEmptyPlaygroundConversations(ctx context.Context, olderThan time.Time) (int64, error)`

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 3: §8.3 gives the schema verbatim and stage 3's `playground.go` gives the store idiom

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/playground_test.go`:

```go
func TestPlaygroundConversationRoundTripsItsBlobUntouched(t *testing.T) {
	// A conversation reopened next week has to restore the system prompt that
	// produced its transcript, so the blob is stored exactly as it arrived --
	// unknown fields included.
	ctx := context.Background()
	db := migrated(t)

	blob := json.RawMessage(`{"system":"be brief","topK":"40","unknownFutureField":7}`)
	made, err := db.CreatePlaygroundConversation(ctx, "New chat", "anthropic", "claude", blob)
	if err != nil {
		t.Fatal(err)
	}
	if made.ID == "" {
		t.Fatal("no id assigned")
	}

	got, turns, found, err := db.PlaygroundConversationByID(ctx, made.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("conversation not found after create")
	}
	if len(turns) != 0 {
		t.Errorf("new conversation has %d turns, want 0", len(turns))
	}
	if string(got.Config) != string(blob) {
		t.Errorf("config = %s, want %s", got.Config, blob)
	}
	if got.Title != "New chat" || got.Dialect != "anthropic" || got.Model != "claude" {
		t.Errorf("columns did not round-trip: %+v", got)
	}
}

func TestPlaygroundTurnsTakeTheNextSeqAndBumpTheConversation(t *testing.T) {
	// seq is what orders a transcript, and the unique index means the store
	// has to hand out the next one rather than the caller guessing it.
	ctx := context.Background()
	db := migrated(t)

	c, err := db.CreatePlaygroundConversation(ctx, "New chat", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendPlaygroundTurn(ctx, c.ID, "user", "hello", ""); err != nil {
		t.Fatal(err)
	}
	second, err := db.AppendPlaygroundTurn(ctx, c.ID, "assistant", "hi", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if second.Seq != 1 {
		t.Errorf("second turn seq = %d, want 1", second.Seq)
	}

	_, turns, _, err := db.PlaygroundConversationByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("read %d turns, want 2", len(turns))
	}
	if turns[0].Role != "user" || turns[0].Content != "hello" {
		t.Errorf("first turn = %+v", turns[0])
	}
	// A turn stored before the log writer's batch lands has no trace, and the
	// UI has to treat that as ordinary rather than as a missing row.
	if turns[0].RequestID != "" {
		t.Errorf("first turn request id = %q, want empty", turns[0].RequestID)
	}
	if turns[1].RequestID != "req-1" {
		t.Errorf("second turn request id = %q, want req-1", turns[1].RequestID)
	}

	list, err := db.PlaygroundConversations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d conversations, want 1", len(list))
	}
	// The rail shows the most recent user turn under the title.
	if list[0].Preview != "hello" {
		t.Errorf("preview = %q, want hello", list[0].Preview)
	}
	// Backdated first, because the bump is what orders the history rail and
	// both writes otherwise land in the same whole second -- an assertion
	// that compares them as they stand can never see the touch happen. Both
	// columns move, because the touch overwrites updated_at with the current
	// second and only an older created_at leaves the bump visible.
	backdated := time.Now().UTC().Add(-time.Hour).Unix()
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE playground_conversations SET created_at = ?, updated_at = ? WHERE id = ?`,
		backdated, backdated, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendPlaygroundTurn(ctx, c.ID, "user", "again", ""); err != nil {
		t.Fatal(err)
	}
	list, err = db.PlaygroundConversations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !list[0].UpdatedAt.After(list[0].CreatedAt) {
		t.Errorf("appending did not move updated_at %v past created_at %v",
			list[0].UpdatedAt, list[0].CreatedAt)
	}
	if list[0].Preview != "again" {
		t.Errorf("preview = %q, want the most recent user turn", list[0].Preview)
	}
}

func TestPlaygroundConversationDeleteTakesItsTurnsWithIt(t *testing.T) {
	// ON DELETE CASCADE only fires because PRAGMA foreign_keys is in the DSN.
	// A test that only checked the parent row would pass with the pragma off
	// and leave orphaned prompt text in the database.
	ctx := context.Background()
	db := migrated(t)

	c, err := db.CreatePlaygroundConversation(ctx, "New chat", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendPlaygroundTurn(ctx, c.ID, "user", "hello", ""); err != nil {
		t.Fatal(err)
	}
	removed, err := db.DeletePlaygroundConversation(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("delete reported no row")
	}

	var orphans int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM playground_messages`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("%d messages survived their conversation", orphans)
	}
}

func TestPlaygroundConversationPurgeEmptiesBothTables(t *testing.T) {
	// The purge is what the settings screen offers when an operator decides
	// the playground should not have kept their prompts. Leaving messages
	// behind would make it a lie.
	ctx := context.Background()
	db := migrated(t)

	for _, title := range []string{"one", "two"} {
		c, err := db.CreatePlaygroundConversation(ctx, title, "openai", "gpt", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.AppendPlaygroundTurn(ctx, c.ID, "user", "hello", ""); err != nil {
			t.Fatal(err)
		}
	}

	n, err := db.PurgePlaygroundConversations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("purged %d conversations, want 2", n)
	}
	for _, table := range []string{"playground_conversations", "playground_messages"} {
		var count int
		if err := db.Read.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s still holds %d rows", table, count)
		}
	}
}

func TestPlaygroundReapSparesAConversationStillBeingWritten(t *testing.T) {
	// The reap exists for a client that created a conversation and died before
	// its first turn. A conversation created a moment ago is the ordinary case
	// and reaping it would delete the one the operator is typing into.
	ctx := context.Background()
	db := migrated(t)

	fresh, err := db.CreatePlaygroundConversation(ctx, "New chat", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	kept, err := db.CreatePlaygroundConversation(ctx, "has a turn", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendPlaygroundTurn(ctx, kept.ID, "user", "hello", ""); err != nil {
		t.Fatal(err)
	}

	n, err := db.ReapEmptyPlaygroundConversations(ctx, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("reaped %d conversations, want 0", n)
	}

	n, err = db.ReapEmptyPlaygroundConversations(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("reaped %d conversations, want 1", n)
	}
	if _, _, found, err := db.PlaygroundConversationByID(ctx, fresh.ID); err != nil || found {
		t.Errorf("empty conversation survived the reap (found=%v, err=%v)", found, err)
	}
	if _, _, found, err := db.PlaygroundConversationByID(ctx, kept.ID); err != nil || !found {
		t.Errorf("conversation with a turn was reaped (found=%v, err=%v)", found, err)
	}
}
```

Add `"time"` to that file's import block if it is not already there.

- [ ] **Step 2: Extend the migration test's table list**

In `internal/store/migrate_test.go`, change the last line of the `want` slice from

```go
		"proxy_tokens", "playground_presets",
```

to

```go
		"proxy_tokens", "playground_presets",
		"playground_conversations", "playground_messages",
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/store/ -run 'TestPlaygroundConversation|TestPlaygroundTurns|TestPlaygroundReap|TestMigrateCreatesEveryTable' 2>&1 | tail -20`
Expected: compile failure — `db.CreatePlaygroundConversation undefined`.

- [ ] **Step 4: Write the migration**

Create `internal/store/migrations/0015_playground_conversations.sql`:

```sql
-- Saved Chat-mode conversations, and the turns in them.
--
-- This is the first place darkrouter retains prompt text at rest. Spec section
-- 8.2 argues the case at length and the short form is: this table holds the
-- operator's own typing rather than traffic passing through the gateway, it
-- grows only when a person types into it, and it is emptied by a purge the
-- settings screen offers rather than by a retention sweep.
--
-- config is the same opaque JSON object playground_presets carries. Without it
-- a conversation reopened tomorrow loses the system prompt that produced its
-- transcript, and "open this configuration in Lab" has nothing to open.
CREATE TABLE playground_conversations (
  id         TEXT PRIMARY KEY,
  title      TEXT    NOT NULL,
  model      TEXT    NOT NULL,
  dialect    TEXT    NOT NULL,
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

- [ ] **Step 5: Rename the id helper**

In `internal/store/playground.go`, rename `newPresetID` to `newPlaygroundID`, update its doc comment and its one call site inside `CreatePlaygroundPreset`:

```go
func newPlaygroundID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate playground id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
```

- [ ] **Step 6: Write the store methods**

Append to `internal/store/playground.go`:

```go
// PlaygroundConversation is a saved Chat-mode session.
//
// Config carries the same opaque blob a preset does and for the same reason:
// the store is not the authority on what a request setting is, and a struct
// here would drop whatever field the console learned after this binary was
// built.
type PlaygroundConversation struct {
	ID        string
	Title     string
	Dialect   string
	Model     string
	Config    json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
	// Preview is the most recent user turn, which is what the history rail
	// shows beneath each title. It belongs to the listing rather than to the
	// row: a single-conversation read carries the turns themselves and leaves
	// this empty.
	Preview string
}

// PlaygroundTurn is one message in a saved conversation.
//
// RequestID is empty rather than absent when the turn has no trace. Two
// ordinary situations produce that: the log writer batches on a 250ms timer so
// a turn can be stored before its trace exists, and the request log's
// retention sweep outlives plenty of conversations.
type PlaygroundTurn struct {
	ID        string
	Seq       int
	Role      string
	Content   string
	RequestID string
	CreatedAt time.Time
}

// conversationListLimit caps the history rail. Past this the rail is not the
// right retrieval tool and search would be a different feature; the cap is
// stated so it is a decision rather than the point where the query gets slow.
const conversationListLimit = 200

// previewChars bounds what the listing carries per row. The rail draws one
// line, so sending a whole prompt would be payload nobody renders.
const previewChars = 200

func (d *DB) CreatePlaygroundConversation(
	ctx context.Context, title, dialect, model string, config json.RawMessage,
) (PlaygroundConversation, error) {
	id, err := newPlaygroundID()
	if err != nil {
		return PlaygroundConversation{}, err
	}
	now := time.Now().UTC()
	c := PlaygroundConversation{
		ID: id, Title: title, Dialect: dialect, Model: model,
		Config: config, CreatedAt: now, UpdatedAt: now,
	}
	_, err = d.Write.ExecContext(ctx,
		`INSERT INTO playground_conversations
		        (id, title, dialect, model, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Title, c.Dialect, c.Model, string(c.Config), now.Unix(), now.Unix())
	if err != nil {
		return PlaygroundConversation{}, fmt.Errorf("store playground conversation: %w", err)
	}
	return c, nil
}

func (d *DB) PlaygroundConversations(ctx context.Context) ([]PlaygroundConversation, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT c.id, c.title, c.dialect, c.model, c.config, c.created_at, c.updated_at,
		        COALESCE((SELECT substr(m.content, 1, ?) FROM playground_messages m
		                   WHERE m.conversation_id = c.id AND m.role = 'user'
		                   ORDER BY m.seq DESC LIMIT 1), '')
		   FROM playground_conversations c
		  ORDER BY c.updated_at DESC
		  LIMIT ?`, previewChars, conversationListLimit)
	if err != nil {
		return nil, fmt.Errorf("list playground conversations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []PlaygroundConversation{}
	for rows.Next() {
		var (
			c                = PlaygroundConversation{}
			cfg              string
			created, updated int64
		)
		if err := rows.Scan(&c.ID, &c.Title, &c.Dialect, &c.Model, &cfg,
			&created, &updated, &c.Preview); err != nil {
			return nil, fmt.Errorf("scan playground conversation: %w", err)
		}
		c.Config = json.RawMessage(cfg)
		c.CreatedAt = time.Unix(created, 0).UTC()
		c.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) PlaygroundConversationByID(
	ctx context.Context, id string,
) (PlaygroundConversation, []PlaygroundTurn, bool, error) {
	var (
		c                = PlaygroundConversation{}
		cfg              string
		created, updated int64
	)
	err := d.Read.QueryRowContext(ctx,
		`SELECT id, title, dialect, model, config, created_at, updated_at
		   FROM playground_conversations WHERE id = ?`, id).
		Scan(&c.ID, &c.Title, &c.Dialect, &c.Model, &cfg, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return PlaygroundConversation{}, nil, false, nil
	}
	if err != nil {
		return PlaygroundConversation{}, nil, false, fmt.Errorf("read playground conversation: %w", err)
	}
	c.Config = json.RawMessage(cfg)
	c.CreatedAt = time.Unix(created, 0).UTC()
	c.UpdatedAt = time.Unix(updated, 0).UTC()

	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, seq, role, content, request_id, created_at
		   FROM playground_messages WHERE conversation_id = ? ORDER BY seq`, id)
	if err != nil {
		return PlaygroundConversation{}, nil, false, fmt.Errorf("read playground turns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	turns := []PlaygroundTurn{}
	for rows.Next() {
		var (
			t       = PlaygroundTurn{}
			request sql.NullString
			at      int64
		)
		if err := rows.Scan(&t.ID, &t.Seq, &t.Role, &t.Content, &request, &at); err != nil {
			return PlaygroundConversation{}, nil, false, fmt.Errorf("scan playground turn: %w", err)
		}
		t.RequestID = request.String
		t.CreatedAt = time.Unix(at, 0).UTC()
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return PlaygroundConversation{}, nil, false, err
	}
	return c, turns, true, nil
}

func (d *DB) UpdatePlaygroundConversation(
	ctx context.Context, id, title, dialect, model string, config json.RawMessage,
) (bool, error) {
	res, err := d.Write.ExecContext(ctx,
		`UPDATE playground_conversations
		    SET title = ?, dialect = ?, model = ?, config = ?, updated_at = ?
		  WHERE id = ?`,
		title, dialect, model, string(config), time.Now().UTC().Unix(), id)
	if err != nil {
		return false, fmt.Errorf("update playground conversation: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (d *DB) DeletePlaygroundConversation(ctx context.Context, id string) (bool, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM playground_conversations WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete playground conversation: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// AppendPlaygroundTurn stores one message and moves its conversation to the
// top of the rail.
//
// The next seq is read and written inside one transaction because
// idx_playground_messages_seq is unique: two turns racing on the same
// conversation would otherwise both read the same maximum, and the second
// insert would fail on the index instead of taking the next number.
func (d *DB) AppendPlaygroundTurn(
	ctx context.Context, conversationID, role, content, requestID string,
) (PlaygroundTurn, error) {
	id, err := newPlaygroundID()
	if err != nil {
		return PlaygroundTurn{}, err
	}
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return PlaygroundTurn{}, fmt.Errorf("append playground turn: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var seq int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), -1) + 1 FROM playground_messages WHERE conversation_id = ?`,
		conversationID).Scan(&seq); err != nil {
		return PlaygroundTurn{}, fmt.Errorf("next playground turn seq: %w", err)
	}

	now := time.Now().UTC()
	// A NULL rather than an empty string, because "no trace" is genuinely
	// absent rather than a request whose id happens to be blank.
	var request any
	if requestID != "" {
		request = requestID
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO playground_messages
		        (id, conversation_id, seq, role, content, request_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, conversationID, seq, role, content, request, now.Unix()); err != nil {
		return PlaygroundTurn{}, fmt.Errorf("store playground turn: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE playground_conversations SET updated_at = ? WHERE id = ?`,
		now.Unix(), conversationID); err != nil {
		return PlaygroundTurn{}, fmt.Errorf("touch playground conversation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PlaygroundTurn{}, fmt.Errorf("append playground turn: %w", err)
	}
	return PlaygroundTurn{
		ID: id, Seq: seq, Role: role, Content: content,
		RequestID: requestID, CreatedAt: now,
	}, nil
}

// PurgePlaygroundConversations empties both tables and reports how many
// conversations it removed. The cascade takes the turns.
func (d *DB) PurgePlaygroundConversations(ctx context.Context) (int64, error) {
	res, err := d.Write.ExecContext(ctx, `DELETE FROM playground_conversations`)
	if err != nil {
		return 0, fmt.Errorf("purge playground conversations: %w", err)
	}
	return res.RowsAffected()
}

// ReapEmptyPlaygroundConversations removes conversations that never received a
// turn and are older than olderThan.
//
// The age floor is the whole of the safety here. The console creates a
// conversation with its first turn rather than before it, so an empty row means
// a client that died between two calls -- but a second console tab could have
// created one moments ago, and reaping that would delete the conversation
// somebody is typing into.
func (d *DB) ReapEmptyPlaygroundConversations(
	ctx context.Context, olderThan time.Time,
) (int64, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM playground_conversations
		  WHERE created_at < ?
		    AND NOT EXISTS (SELECT 1 FROM playground_messages m
		                     WHERE m.conversation_id = playground_conversations.id)`,
		olderThan.Unix())
	if err != nil {
		return 0, fmt.Errorf("reap empty playground conversations: %w", err)
	}
	return res.RowsAffected()
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test -count=1 ./internal/store/ 2>&1 | tail -10`
Expected: `ok github.com/darkraise/darkrouter/internal/store`

- [ ] **Step 8: Run the full Go gate**

Run: `export PATH=$PATH:/usr/local/go/bin && go build ./... && go vet ./... && go test -count=1 ./internal/... 2>&1 | tail -30`
Expected: build and vet silent; 27 packages `ok`, `internal/edge` reporting no test files.

- [ ] **Step 9: Commit**

```bash
git add internal/store/migrations/0015_playground_conversations.sql internal/store/playground.go internal/store/playground_test.go internal/store/migrate_test.go
git commit -m "feat(store): add saved playground conversations"
```

---

### Task 2: The `playground.save_conversations` key

**Files:**
- Modify: `internal/config/config.go` (a `Playground` block, and a reader method beside `MediaInline`)
- Modify: `internal/config/load.go` (the default-on, in `applyDefaults`)
- Modify: `internal/config/load_test.go` (append)
- Modify: `internal/admin/configapi.go:33-57` (`configFields`) and `:94-129` (`blocks`)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `config.PlaygroundConfig{SaveConversations *bool}` on `config.Config` as field `Playground`
  - `(*config.Config).SaveConversations() bool` — the effective value; absent means on
  - the wire key `playground.save_conversations` in `GET /api/config`'s `fields`, and `blocks.playground.save_conversations` carrying the boolean

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 2 - spec 0 - coupling 0 - risk 1 = 3
**Approach:** inline - skip 2: `catalog.discovery.enabled` is the same `*bool`-defaulting-to-true shape, down to the `== nil ||` read in `configapi.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/load_test.go`:

```go
func TestSaveConversationsDefaultsOnAndParsesOff(t *testing.T) {
	// The default is on, so an unset key and an explicit false must be
	// distinguishable. A plain bool would read every silent config as off and
	// quietly stop the playground saving anything.
	c, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if !c.SaveConversations() {
		t.Error("SaveConversations() = false with the key absent, want true")
	}

	off := minimal + "\nplayground:\n  save_conversations: false\n"
	c, err = Parse([]byte(off), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.SaveConversations() {
		t.Error("SaveConversations() = true with the key set to false")
	}
	found := false
	for _, k := range c.FileKeys {
		if k == "playground.save_conversations" {
			found = true
		}
	}
	if !found {
		// The settings screen labels a value "file" or "default" from this
		// list, and a key missing from it is reported as a built-in default
		// the operator never chose.
		t.Errorf("playground.save_conversations not recorded in FileKeys: %v", c.FileKeys)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/config/ -run TestSaveConversations 2>&1 | tail -10`
Expected: compile failure — `c.SaveConversations undefined`.

- [ ] **Step 3: Add the config block**

In `internal/config/config.go`, add the field to `Config` after `Media`:

```go
	Media     MediaConfig      `yaml:"media"`
	Playground PlaygroundConfig `yaml:"playground"`
```

and the type plus its reader beside `MediaInline`:

```go
// PlaygroundConfig governs what the console's playground is allowed to keep.
//
// It is the operator's own typing rather than traffic passing through the
// gateway, which is why it is a separate key from capture.bodies rather than
// covered by it.
type PlaygroundConfig struct {
	// SaveConversations is a pointer for the same reason Discovery.Enabled is:
	// the default is on, so an explicit false in the file has to be
	// distinguishable from a key the file never mentioned.
	SaveConversations *bool `yaml:"save_conversations"`
}

// SaveConversations reports the effective setting: absent means on.
func (c *Config) SaveConversations() bool {
	return c.Playground.SaveConversations == nil || *c.Playground.SaveConversations
}
```

- [ ] **Step 4: Add the default**

In `internal/config/load.go`, inside `applyDefaults`, beside the `Catalog.Discovery.Enabled` block:

```go
	if c.Playground.SaveConversations == nil {
		on := true
		c.Playground.SaveConversations = &on
	}
```

- [ ] **Step 5: Expose it on the config API**

In `internal/admin/configapi.go`, add to `configFields` immediately after `"capture.retention"`:

```go
	"playground.save_conversations",
```

and to the `blocks` map, after the `"media"` entry:

```go
			"playground": map[string]any{"save_conversations": cfg.SaveConversations()},
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test -count=1 ./internal/config/ ./internal/admin/ 2>&1 | tail -10`
Expected: both `ok`.

- [ ] **Step 7: Run the full Go gate**

Run: `export PATH=$PATH:/usr/local/go/bin && go build ./... && go vet ./... && go test -count=1 ./internal/... 2>&1 | tail -30`
Expected: build and vet silent; 27 packages `ok`.

- [ ] **Step 8: Commit**

```bash
git add internal/config/config.go internal/config/load.go internal/config/load_test.go internal/admin/configapi.go
git commit -m "feat(config): add playground.save_conversations"
```

---

### Task 3: The conversation CRUD endpoints

**Files:**
- Modify: `internal/admin/playgroundstore.go` (append)
- Modify: `internal/admin/playgroundstore_test.go` (append)
- Modify: `internal/admin/admin.go:154-157` (register six routes beside the preset routes)

**Interfaces:**
- Consumes: every `(*store.DB)` conversation method from Task 1.
- Produces the wire contract Task 6 types against:
  - `GET /api/playground/conversations` → `200` `[{id,title,dialect,model,config,preview,created_at,updated_at}]`, newest first
  - `POST /api/playground/conversations` → `201` with the same object
  - `GET /api/playground/conversations/{id}` → `200` with the same object plus `messages: [{seq,role,content,request_id,created_at}]`
  - `PATCH /api/playground/conversations/{id}` → `200` `{"id":"…"}`, `404` when absent
  - `DELETE /api/playground/conversations/{id}` → `204`, `404` when absent
  - `POST /api/playground/conversations/{id}/messages` → `201` `{"seq":N}`, `404` when the conversation is absent

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: `playgroundstore.go`'s preset handlers are the pattern, down to `readPresetBody`'s validation order

- [ ] **Step 1: Write the failing tests**

Append to `internal/admin/playgroundstore_test.go`:

```go
func TestPlaygroundConversationRoundTripsThroughTheAPI(t *testing.T) {
	// The whole point of storing the config blob is that a conversation
	// reopened next week still knows the system prompt that shaped it.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	create := `{"title":"be brief","dialect":"anthropic","model":"claude",
	            "config":{"system":"answer in one line","fieldFromTheFuture":{"nested":true}}}`
	w := do(t, s, cookie, token, "POST", "/api/playground/conversations", create)
	if w.Code != 201 {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}

	turn := `{"role":"user","content":"hello","request_id":""}`
	if w := do(t, s, cookie, token,
		"POST", "/api/playground/conversations/"+made.ID+"/messages", turn); w.Code != 201 {
		t.Fatalf("append user turn = %d: %s", w.Code, w.Body.String())
	}
	answer := `{"role":"assistant","content":"hi","request_id":"req-1"}`
	if w := do(t, s, cookie, token,
		"POST", "/api/playground/conversations/"+made.ID+"/messages", answer); w.Code != 201 {
		t.Fatalf("append assistant turn = %d: %s", w.Code, w.Body.String())
	}

	w = do(t, s, cookie, token, "GET", "/api/playground/conversations/"+made.ID, "")
	if w.Code != 200 {
		t.Fatalf("read = %d: %s", w.Code, w.Body.String())
	}
	var read struct {
		Title    string         `json:"title"`
		Dialect  string         `json:"dialect"`
		Config   map[string]any `json:"config"`
		Messages []struct {
			Seq       int    `json:"seq"`
			Role      string `json:"role"`
			Content   string `json:"content"`
			RequestID string `json:"request_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.Title != "be brief" || read.Dialect != "anthropic" {
		t.Errorf("columns did not round-trip: %+v", read)
	}
	if read.Config["system"] != "answer in one line" {
		t.Errorf("system prompt lost: %v", read.Config)
	}
	future, ok := read.Config["fieldFromTheFuture"].(map[string]any)
	if !ok || future["nested"] != true {
		t.Errorf("unknown field did not survive: %v", read.Config)
	}
	if len(read.Messages) != 2 {
		t.Fatalf("read %d messages, want 2", len(read.Messages))
	}
	if read.Messages[0].Seq != 0 || read.Messages[0].Role != "user" {
		t.Errorf("first message = %+v", read.Messages[0])
	}
	if read.Messages[1].RequestID != "req-1" {
		t.Errorf("assistant turn lost its request id: %+v", read.Messages[1])
	}
	// A turn stored before the log writer's batch lands has no trace, and the
	// client must be able to tell that apart from a malformed row.
	if read.Messages[0].RequestID != "" {
		t.Errorf("user turn invented a request id: %q", read.Messages[0].RequestID)
	}

	if w := do(t, s, cookie, token, "PATCH", "/api/playground/conversations/"+made.ID,
		`{"title":"renamed","dialect":"openai","model":"gpt","config":{"system":"x"}}`); w.Code != 200 {
		t.Fatalf("patch = %d: %s", w.Code, w.Body.String())
	}
	w = do(t, s, cookie, token, "GET", "/api/playground/conversations", "")
	if w.Code != 200 {
		t.Fatalf("list = %d", w.Code)
	}
	var list []struct {
		Title   string `json:"title"`
		Model   string `json:"model"`
		Preview string `json:"preview"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d, want 1", len(list))
	}
	if list[0].Title != "renamed" || list[0].Model != "gpt" {
		t.Errorf("patch did not land: %+v", list[0])
	}
	// The rail draws the most recent user turn under each title.
	if list[0].Preview != "hello" {
		t.Errorf("preview = %q, want hello", list[0].Preview)
	}

	if w := do(t, s, cookie, token, "DELETE", "/api/playground/conversations/"+made.ID, ""); w.Code != 204 {
		t.Fatalf("delete = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "GET", "/api/playground/conversations/"+made.ID, ""); w.Code != 404 {
		t.Errorf("read after delete = %d, want 404", w.Code)
	}
}

func TestPlaygroundConversationRejectsWhatTheClientCannotRender(t *testing.T) {
	// dialect-support.ts has no fallback case for a dialect outside the three
	// it knows, so a row naming a fourth would crash the pane that loads it.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	for _, body := range []string{
		`{"title":"x","dialect":"mistral","model":"m","config":{}}`,
		`{"title":"","dialect":"openai","model":"m","config":{}}`,
		`{"title":"x","dialect":"openai","model":"m","config":[1,2]}`,
	} {
		if w := do(t, s, cookie, token, "POST", "/api/playground/conversations", body); w.Code != 400 {
			t.Errorf("create %s = %d, want 400", body, w.Code)
		}
	}

	w := do(t, s, cookie, token, "POST", "/api/playground/conversations",
		`{"title":"x","dialect":"openai","model":"m","config":{}}`)
	if w.Code != 201 {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}
	if w := do(t, s, cookie, token, "POST",
		"/api/playground/conversations/"+made.ID+"/messages",
		`{"role":"system","content":"x"}`); w.Code != 400 {
		t.Errorf("append with an unknown role = %d, want 400", w.Code)
	}
	if w := do(t, s, cookie, token, "POST",
		"/api/playground/conversations/nosuch/messages",
		`{"role":"user","content":"x"}`); w.Code != 404 {
		t.Errorf("append to a missing conversation = %d, want 404", w.Code)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/admin/ -run TestPlaygroundConversation 2>&1 | tail -20`
Expected: the create returns 404 with `{"error":"no such endpoint"}` — the route is not registered.

- [ ] **Step 3: Write the wire shapes and the body readers**

Append to `internal/admin/playgroundstore.go`:

```go
// playgroundConversationView is the rail's row. Config is json.RawMessage in
// both directions for the same reason a preset's is: the server is a courier
// for the console's own settings, and a struct here would strip any field this
// binary has not learned yet.
type playgroundConversationView struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Dialect   string          `json:"dialect"`
	Model     string          `json:"model"`
	Config    json.RawMessage `json:"config"`
	Preview   string          `json:"preview"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// playgroundTurnView is one stored message. RequestID is a plain string rather
// than a pointer: the client treats a missing trace as ordinary, so "" and
// absent mean the same thing and a null would be a third state to handle.
type playgroundTurnView struct {
	Seq       int    `json:"seq"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	RequestID string `json:"request_id"`
	CreatedAt string `json:"created_at"`
}

type playgroundConversationDetail struct {
	playgroundConversationView
	Messages []playgroundTurnView `json:"messages"`
}

func viewOfConversation(c store.PlaygroundConversation) playgroundConversationView {
	return playgroundConversationView{
		ID: c.ID, Title: c.Title, Dialect: c.Dialect, Model: c.Model,
		Config: c.Config, Preview: c.Preview,
		CreatedAt: c.CreatedAt.Format(time.RFC3339),
		UpdatedAt: c.UpdatedAt.Format(time.RFC3339),
	}
}

type playgroundConversationBody struct {
	Title   string          `json:"title"`
	Dialect string          `json:"dialect"`
	Model   string          `json:"model"`
	Config  json.RawMessage `json:"config"`
}

// readConversationBody decodes and validates everything the store will not.
//
// The blob's interior is never inspected beyond confirming it is an object:
// that is the one shape the client can merge, and anything inside it belongs
// to the console.
func readConversationBody(w http.ResponseWriter, r *http.Request) (playgroundConversationBody, bool) {
	var body playgroundConversationBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return playgroundConversationBody{}, false
	}
	if body.Title == "" {
		writeError(w, http.StatusBadRequest, "a conversation needs a title")
		return playgroundConversationBody{}, false
	}
	if !validPlaygroundDialects[body.Dialect] {
		writeError(w, http.StatusBadRequest, "dialect must be one of openai, anthropic, gemini")
		return playgroundConversationBody{}, false
	}
	var probe map[string]any
	if err := json.Unmarshal(body.Config, &probe); err != nil || probe == nil {
		writeError(w, http.StatusBadRequest, "config must be a JSON object")
		return playgroundConversationBody{}, false
	}
	return body, true
}

type playgroundTurnBody struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// The request whose trace explains this turn, when there is one. Empty is
	// ordinary rather than an error: the log writer batches on a timer.
	RequestID string `json:"request_id"`
}

func readTurnBody(w http.ResponseWriter, r *http.Request) (playgroundTurnBody, bool) {
	var body playgroundTurnBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return playgroundTurnBody{}, false
	}
	if body.Role != "user" && body.Role != "assistant" {
		writeError(w, http.StatusBadRequest, "role must be user or assistant")
		return playgroundTurnBody{}, false
	}
	return body, true
}
```

- [ ] **Step 4: Write the handlers**

Append to `internal/admin/playgroundstore.go`:

```go
// reapAge is how long an empty conversation is left alone before the listing
// removes it. A conversation with no turns means a client that died between
// creating one and sending its first message; a conversation created seconds
// ago in another tab is the ordinary case and must survive.
const reapAge = time.Hour

func (s *Server) handleListPlaygroundConversations(w http.ResponseWriter, r *http.Request) {
	// Housekeeping, not the caller's business: a rail that fails to load
	// because a stale empty row could not be removed would be a worse
	// outcome than the row staying one more minute.
	_, _ = s.deps.DB.ReapEmptyPlaygroundConversations(r.Context(), time.Now().UTC().Add(-reapAge))

	conversations, err := s.deps.DB.PlaygroundConversations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []playgroundConversationView{}
	for _, c := range conversations {
		out = append(out, viewOfConversation(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreatePlaygroundConversation(w http.ResponseWriter, r *http.Request) {
	body, ok := readConversationBody(w, r)
	if !ok {
		return
	}
	made, err := s.deps.DB.CreatePlaygroundConversation(
		r.Context(), body.Title, body.Dialect, body.Model, body.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, viewOfConversation(made))
}

func (s *Server) handleGetPlaygroundConversation(w http.ResponseWriter, r *http.Request) {
	c, turns, found, err := s.deps.DB.PlaygroundConversationByID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no such conversation")
		return
	}
	messages := []playgroundTurnView{}
	for _, t := range turns {
		messages = append(messages, playgroundTurnView{
			Seq: t.Seq, Role: t.Role, Content: t.Content, RequestID: t.RequestID,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, playgroundConversationDetail{
		playgroundConversationView: viewOfConversation(c),
		Messages:                   messages,
	})
}

func (s *Server) handleUpdatePlaygroundConversation(w http.ResponseWriter, r *http.Request) {
	body, ok := readConversationBody(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	moved, err := s.deps.DB.UpdatePlaygroundConversation(
		r.Context(), id, body.Title, body.Dialect, body.Model, body.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !moved {
		writeError(w, http.StatusNotFound, "no such conversation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) handleDeletePlaygroundConversation(w http.ResponseWriter, r *http.Request) {
	removed, err := s.deps.DB.DeletePlaygroundConversation(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "no such conversation")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAppendPlaygroundTurn(w http.ResponseWriter, r *http.Request) {
	body, ok := readTurnBody(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	// Checked before the insert rather than caught after it: the foreign key
	// would answer with a constraint failure, which reads as a server fault
	// rather than as the missing conversation it is.
	_, _, found, err := s.deps.DB.PlaygroundConversationByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no such conversation")
		return
	}
	turn, err := s.deps.DB.AppendPlaygroundTurn(
		r.Context(), id, body.Role, body.Content, body.RequestID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"seq": turn.Seq})
}
```

- [ ] **Step 5: Register the routes**

In `internal/admin/admin.go`, directly after the four preset routes at `:154-157`:

```go
	s.mux.HandleFunc("GET /api/playground/conversations", s.requireSession(s.handleListPlaygroundConversations))
	s.mux.HandleFunc("POST /api/playground/conversations", s.requireCSRF(s.handleCreatePlaygroundConversation))
	s.mux.HandleFunc("GET /api/playground/conversations/{id}", s.requireSession(s.handleGetPlaygroundConversation))
	s.mux.HandleFunc("PATCH /api/playground/conversations/{id}", s.requireCSRF(s.handleUpdatePlaygroundConversation))
	s.mux.HandleFunc("DELETE /api/playground/conversations/{id}", s.requireCSRF(s.handleDeletePlaygroundConversation))
	s.mux.HandleFunc("POST /api/playground/conversations/{id}/messages", s.requireCSRF(s.handleAppendPlaygroundTurn))
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test -count=1 ./internal/admin/ -run TestPlaygroundConversation 2>&1 | tail -10`
Expected: `ok`.

- [ ] **Step 7: Run the full Go gate**

Run: `export PATH=$PATH:/usr/local/go/bin && go build ./... && go vet ./... && go test -count=1 ./internal/... 2>&1 | tail -30`
Expected: build and vet silent; 27 packages `ok`.

- [ ] **Step 8: Commit**

```bash
git add internal/admin/playgroundstore.go internal/admin/playgroundstore_test.go internal/admin/admin.go
git commit -m "feat(admin): add conversation endpoints"
```

---

### Task 4: The purge, and the save gate

**Files:**
- Modify: `internal/admin/playgroundstore.go` (append the gate middleware and the purge handler)
- Modify: `internal/admin/admin.go` (wrap three routes; register the purge)
- Modify: `internal/admin/fixtures_test.go:25-42` and `:52-90` (a config-body parameter)
- Modify: `internal/admin/playgroundstore_test.go` (append)

**Interfaces:**
- Consumes: Task 2's `(*config.Config).SaveConversations()`, Task 1's `PurgePlaygroundConversations`, Task 3's routes.
- Produces:
  - `DELETE /api/playground/conversations` → `200` `{"deleted":N}` — the purge, always allowed
  - `403` `{"error":"playground.save_conversations is off, so conversations are not saved"}` from `POST /conversations`, `PATCH /conversations/{id}` and `POST /conversations/{id}/messages` when the key is off
  - test helper `testServerFullWithConfig(t *testing.T, extraYAML string) (*Server, *store.DB)`

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 3 = 6
**Approach:** inline - skip 2: `requireCSRF` in `admin.go` is the middleware shape, and §8.2 fixes which verbs the gate covers

- [ ] **Step 1: Generalize the test fixture**

In `internal/admin/fixtures_test.go`, replace `configStoreFor` with a two-argument core and keep the existing name as a wrapper, so no existing caller changes:

```go
// configStoreFor writes a minimal config and opens a store over it. aliases is
// appended verbatim under an `aliases:` key when it is not empty.
func configStoreFor(t *testing.T, aliases string) *config.Store {
	t.Helper()
	return configStoreWith(t, aliases, "")
}

// configStoreWith adds arbitrary top-level YAML after the aliases, for a test
// that needs a key the minimal document does not carry.
func configStoreWith(t *testing.T, aliases, extra string) *config.Store {
	t.Helper()
	body := "server:\n  proxy_listen: \":0\"\n  admin_listen: \":0\"\n"
	if aliases != "" {
		body += "aliases:\n" + aliases
	}
	body += extra
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return s
}
```

Then change `testServerFullWithAliases` into a three-argument core with two wrappers. Replace its signature line and its `cfg :=` line, and add the wrappers above it:

```go
func testServerFullWithAliases(t *testing.T, aliases string) (*Server, *store.DB) {
	t.Helper()
	return testServerFullWith(t, aliases, "")
}

// testServerFullWithConfig is testServerFull with extra top-level YAML in the
// config document, for a test that turns a key off.
func testServerFullWithConfig(t *testing.T, extra string) (*Server, *store.DB) {
	t.Helper()
	return testServerFullWith(t, "", extra)
}

func testServerFullWith(t *testing.T, aliases, extra string) (*Server, *store.DB) {
	t.Helper()
	db := store.MigratedForTest(t)
```

and inside that body change

```go
	cfg := configStoreFor(t, aliases)
```

to

```go
	cfg := configStoreWith(t, aliases, extra)
```

leaving every other line of the function exactly as it stands.

- [ ] **Step 2: Write the failing tests**

Append to `internal/admin/playgroundstore_test.go`:

```go
func TestPlaygroundConversationsGateStopsWritesAndNotReads(t *testing.T) {
	// Section 8.2: flipping the key stops the playground keeping anything new.
	// It does not delete what is already there, and an operator who has just
	// turned it off still needs to see and remove that.
	s, db := testServerFullWithConfig(t, "playground:\n  save_conversations: false\n")
	cookie, token := login(t, s)

	existing, err := db.CreatePlaygroundConversation(
		t.Context(), "from before", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	create := `{"title":"x","dialect":"openai","model":"gpt","config":{}}`
	if w := do(t, s, cookie, token, "POST", "/api/playground/conversations", create); w.Code != 403 {
		t.Errorf("create with saving off = %d, want 403", w.Code)
	}
	if w := do(t, s, cookie, token, "PATCH",
		"/api/playground/conversations/"+existing.ID, create); w.Code != 403 {
		t.Errorf("patch with saving off = %d, want 403", w.Code)
	}
	if w := do(t, s, cookie, token, "POST",
		"/api/playground/conversations/"+existing.ID+"/messages",
		`{"role":"user","content":"x"}`); w.Code != 403 {
		t.Errorf("append with saving off = %d, want 403", w.Code)
	}

	if w := do(t, s, cookie, token, "GET", "/api/playground/conversations", ""); w.Code != 200 {
		t.Errorf("list with saving off = %d, want 200", w.Code)
	}
	if w := do(t, s, cookie, token, "GET",
		"/api/playground/conversations/"+existing.ID, ""); w.Code != 200 {
		t.Errorf("read with saving off = %d, want 200", w.Code)
	}
	if w := do(t, s, cookie, token, "DELETE",
		"/api/playground/conversations/"+existing.ID, ""); w.Code != 204 {
		t.Errorf("delete with saving off = %d, want 204", w.Code)
	}
}

func TestPlaygroundConversationsPurgeEmptiesEverything(t *testing.T) {
	// The purge is what the settings screen offers when an operator decides
	// the playground should not have kept their prompts. It must reach the
	// messages too, or it is a lie told with a confirmation dialog.
	s, db := testServerFull(t)
	cookie, token := login(t, s)

	for _, title := range []string{"one", "two"} {
		c, err := db.CreatePlaygroundConversation(
			t.Context(), title, "openai", "gpt", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.AppendPlaygroundTurn(t.Context(), c.ID, "user", "hello", ""); err != nil {
			t.Fatal(err)
		}
	}

	w := do(t, s, cookie, token, "DELETE", "/api/playground/conversations", "")
	if w.Code != 200 {
		t.Fatalf("purge = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Deleted != 2 {
		t.Errorf("purge reported %d, want 2", out.Deleted)
	}

	for _, table := range []string{"playground_conversations", "playground_messages"} {
		var count int
		if err := db.Read.QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s still holds %d rows after the purge", table, count)
		}
	}

	// The purge and the single delete share a path prefix. ServeMux prefers
	// the exact literal, and a regression that routed the purge through the
	// wildcard would answer 404 for an id of "" rather than emptying anything.
	if w := do(t, s, cookie, token, "DELETE", "/api/playground/conversations", ""); w.Code != 200 {
		t.Errorf("purge on an empty table = %d, want 200", w.Code)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/admin/ -run 'TestPlaygroundConversationsGate|TestPlaygroundConversationsPurge' 2>&1 | tail -20`
Expected: the gate test reports `create with saving off = 201`, and the purge test reports `purge = 404`.

- [ ] **Step 4: Write the gate and the purge handler**

Append to `internal/admin/playgroundstore.go`:

```go
// requireConversationSaving refuses a write when the operator has turned
// playground.save_conversations off.
//
// Reads and deletes stay open deliberately. The key governs what the playground
// may keep from here on; an operator who has just turned it off still needs to
// see what was kept before and to remove it, and a switch that also hid the
// history would leave prompt text on disk with no way to reach it.
func (s *Server) requireConversationSaving(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Config != nil && !s.deps.Config.Current().SaveConversations() {
			writeError(w, http.StatusForbidden,
				"playground.save_conversations is off, so conversations are not saved")
			return
		}
		next(w, r)
	}
}

// handlePurgePlaygroundConversations empties both tables.
//
// It is the settings screen's action rather than a side effect of the config
// value changing: config is file-backed and reloadable, and a setting whose
// reload deleted data would mean an edit to a file on disk silently destroying
// the operator's history.
func (s *Server) handlePurgePlaygroundConversations(w http.ResponseWriter, r *http.Request) {
	n, err := s.deps.DB.PurgePlaygroundConversations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": n})
}
```

- [ ] **Step 5: Wrap the three write routes and register the purge**

In `internal/admin/admin.go`, replace the three conversation write routes Task 3 added with their gated forms, and add the purge:

```go
	s.mux.HandleFunc("GET /api/playground/conversations", s.requireSession(s.handleListPlaygroundConversations))
	s.mux.HandleFunc("POST /api/playground/conversations", s.requireCSRF(s.requireConversationSaving(s.handleCreatePlaygroundConversation)))
	// The exact literal beside the wildcard below it: ServeMux prefers the
	// literal, so the purge and the single delete coexist.
	s.mux.HandleFunc("DELETE /api/playground/conversations", s.requireCSRF(s.handlePurgePlaygroundConversations))
	s.mux.HandleFunc("GET /api/playground/conversations/{id}", s.requireSession(s.handleGetPlaygroundConversation))
	s.mux.HandleFunc("PATCH /api/playground/conversations/{id}", s.requireCSRF(s.requireConversationSaving(s.handleUpdatePlaygroundConversation)))
	s.mux.HandleFunc("DELETE /api/playground/conversations/{id}", s.requireCSRF(s.handleDeletePlaygroundConversation))
	s.mux.HandleFunc("POST /api/playground/conversations/{id}/messages", s.requireCSRF(s.requireConversationSaving(s.handleAppendPlaygroundTurn)))
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test -count=1 ./internal/admin/ 2>&1 | tail -10`
Expected: `ok`.

- [ ] **Step 7: Run the full Go gate**

Run: `export PATH=$PATH:/usr/local/go/bin && go build ./... && go vet ./... && go test -count=1 ./internal/... 2>&1 | tail -30`
Expected: build and vet silent; 27 packages `ok`.

- [ ] **Step 8: Commit**

```bash
git add internal/admin/playgroundstore.go internal/admin/admin.go internal/admin/fixtures_test.go internal/admin/playgroundstore_test.go
git commit -m "feat(admin): gate and purge saved conversations"
```

---

### Task 5: A mistyped API path answers JSON on every verb

**Files:**
- Modify: `internal/admin/admin.go:194-201`
- Create: `internal/admin/routes_test.go` (the package has no `admin_test.go`; `package admin`, using the existing fixtures)

**Interfaces:**
- Consumes: nothing.
- Produces: nothing other tasks depend on.

**Implementer:** dcc-superpower-companions:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 0 = 1
**Approach:** inline - skip 1: the cause is located — the fallback is registered for two verbs and the other four fall through to the SPA

- [ ] **Step 1: Write the failing test**

Create `internal/admin/routes_test.go`:

```go
func TestUnknownAPIPathAnswersJSONOnEveryVerb(t *testing.T) {
	// The fallback exists so a mistyped API path reports the missing route
	// rather than returning the SPA's index.html, which the client reports as
	// a JSON parse error. Registered for GET and POST only, it did exactly
	// what it exists to prevent for the other four verbs.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	for _, method := range []string{"GET", "POST", "PATCH", "PUT", "DELETE"} {
		w := do(t, s, cookie, token, method, "/api/nosuchthing", "{}")
		if w.Code != http.StatusNotFound {
			t.Errorf("%s /api/nosuchthing = %d, want 404", method, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s /api/nosuchthing content-type = %q, want JSON", method, ct)
		}
	}
}
```

Its import block is `net/http`, `strings` and `testing`.

- [ ] **Step 2: Run it to verify it fails**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/admin/ -run TestUnknownAPIPathAnswersJSON 2>&1 | tail -10`
Expected: `PATCH /api/nosuchthing = 200`, and a `text/html` content type.

- [ ] **Step 3: Register the fallback once per verb**

In `internal/admin/admin.go`, replace the two `HandleFunc` calls at `:196-201` with a loop, keeping the comment above them and extending it:

```go
	// A mistyped API path must answer as an API path. Without these an
	// unknown /api/… would fall through to the SPA and return HTML, and the
	// client would report a JSON parse error instead of the missing route.
	// Every verb, not only the two that are common: a mistyped DELETE
	// answering 200 with index.html is exactly the failure this prevents.
	for _, method := range []string{"GET", "POST", "PATCH", "PUT", "DELETE"} {
		s.mux.HandleFunc(method+" /api/", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "no such endpoint")
		})
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `export PATH=$PATH:/usr/local/go/bin && go test -count=1 ./internal/admin/ 2>&1 | tail -10`
Expected: `ok`.

- [ ] **Step 5: Run the full Go gate**

Run: `export PATH=$PATH:/usr/local/go/bin && go build ./... && go vet ./... && go test -count=1 ./internal/... 2>&1 | tail -30`
Expected: build and vet silent; 27 packages `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/admin.go internal/admin/routes_test.go
git commit -m "fix(admin): answer JSON 404 on every API verb"
```

---

### Task 6: The client's conversation data layer

**Files:**
- Modify: `web/src/lib/api-types.ts` (append beside `PlaygroundPreset` at `:352-363`)
- Modify: `web/src/lib/queries.ts` (`keys` at `:29-45`, and two hooks beside `usePlaygroundPresets` at `:134`)
- Create: `web/src/features/playground/lib/conversations.ts`
- Create: `web/src/features/playground/lib/conversations.test.ts`

**Interfaces:**
- Consumes: Task 3's wire contract; `mergeStoredConfig` and `toStoredConfig` from `web/src/features/playground/preset-config.ts`; `TurnRoute` from `../message`; `DIALECTS` and `PlaygroundConfig` from `../config`.
- Produces:
  - `PlaygroundConversation`, `PlaygroundStoredTurn`, `PlaygroundConversationDetail` in `api-types.ts`
  - `keys.playgroundConversations`, `keys.playgroundConversation(id)`
  - `usePlaygroundConversations()`, `usePlaygroundConversation(id, extra?)`
  - `titleFromPrompt(prompt: string): string`
  - `configOfConversation(c: PlaygroundConversation): PlaygroundConfig`
  - `messagesOfTurns(turns: PlaygroundStoredTurn[]): PlaygroundMessage[]`
  - `routesOfTurns(turns: PlaygroundStoredTurn[]): Record<number, TurnRoute>`
  - `conversationBody(title: string, config: PlaygroundConfig): {title, dialect, model, config}`
  - hooks `useCreateConversation()`, `useAppendTurn()`, `useUpdateConversation()`, `useDeleteConversation()`, `usePurgeConversations()`

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: `preset-picker.tsx` and `queries.ts` give the exact query, mutation and config-merge idioms

- [ ] **Step 1: Write the failing tests**

Create `web/src/features/playground/lib/conversations.test.ts`:

```ts
import { describe, expect, it } from "vitest"
import {
  configOfConversation,
  messagesOfTurns,
  routesOfTurns,
  titleFromPrompt,
} from "./conversations"
import type { PlaygroundConversation, PlaygroundStoredTurn } from "../../../lib/api-types"

function conversation(over: Partial<PlaygroundConversation> = {}): PlaygroundConversation {
  return {
    id: "c1",
    title: "New chat",
    dialect: "anthropic",
    model: "claude",
    config: {},
    preview: "",
    created_at: "2026-08-30T10:00:00Z",
    updated_at: "2026-08-30T10:00:00Z",
    ...over,
  }
}

describe("titleFromPrompt", () => {
  it("keeps a short prompt whole", () => {
    expect(titleFromPrompt("summarise this")).toBe("summarise this")
  })

  it("truncates on a word boundary rather than mid-word", () => {
    const long =
      "explain the difference between speculative decoding and ordinary autoregressive sampling"
    const title = titleFromPrompt(long)
    expect(title.length).toBeLessThanOrEqual(52)
    expect(long.startsWith(title.replace(/…$/, ""))).toBe(true)
    expect(title).not.toMatch(/\s…$/)
    expect(title.endsWith("…")).toBe(true)
  })

  it("falls back on a single word longer than the limit", () => {
    const title = titleFromPrompt("x".repeat(80))
    expect(title.length).toBeLessThanOrEqual(52)
  })

  it("never returns an empty title, because the rail would draw a blank row", () => {
    expect(titleFromPrompt("   ")).toBe("New chat")
    expect(titleFromPrompt("")).toBe("New chat")
  })
})

describe("configOfConversation", () => {
  it("restores the system prompt that shaped the transcript", () => {
    const config = configOfConversation(
      conversation({ config: { system: "answer in one line", topK: "40" } }),
    )
    expect(config.system).toBe("answer in one line")
    expect(config.topK).toBe("40")
    expect(config.model).toBe("claude")
    expect(config.dialect).toBe("anthropic")
  })

  it("drops a wrong-typed stored field rather than crashing on it", () => {
    // chatBody calls .split and .trim on these without checking, so a value of
    // the wrong type is a crash rather than a degraded setting.
    const config = configOfConversation(conversation({ config: { stopRaw: 42, system: "ok" } }))
    expect(config.stopRaw).toBe("")
    expect(config.system).toBe("ok")
  })

  it("falls back to openai for a dialect the pane cannot render", () => {
    // A row can be written by hand with curl, and dialect-support.ts has no
    // fallback case for a fourth wire.
    const config = configOfConversation(
      conversation({ dialect: "mistral" as PlaygroundConversation["dialect"] }),
    )
    expect(config.dialect).toBe("openai")
  })
})

describe("messagesOfTurns and routesOfTurns", () => {
  const turns: PlaygroundStoredTurn[] = [
    { seq: 0, role: "user", content: "hello", request_id: "", created_at: "2026-08-30T10:00:00Z" },
    { seq: 1, role: "assistant", content: "hi", request_id: "req-1", created_at: "2026-08-30T10:00:01Z" },
    { seq: 2, role: "user", content: "again", request_id: "", created_at: "2026-08-30T10:00:02Z" },
    { seq: 3, role: "assistant", content: "sure", request_id: "", created_at: "2026-08-30T10:00:03Z" },
  ]

  it("reopens the transcript in order", () => {
    expect(messagesOfTurns(turns)).toEqual([
      { role: "user", content: "hello" },
      { role: "assistant", content: "hi" },
      { role: "user", content: "again" },
      { role: "assistant", content: "sure" },
    ])
  })

  it("keeps the trace link on the turn it explains, and only there", () => {
    const routes = routesOfTurns(turns)
    expect(Object.keys(routes)).toEqual(["1"])
    expect(routes[1]?.requestId).toBe("req-1")
    // Nothing else survives a reopen: the readings came from the trace, and
    // fabricating them here would print numbers nobody measured.
    expect(routes[1]?.totalMs).toBeNull()
    expect(routes[1]?.provider).toBe("")
  })

  it("treats a missing trace as ordinary", () => {
    // The request log's retention sweep outlives plenty of conversations.
    expect(routesOfTurns(turns)[3]).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run conversations 2>&1 | tail -15`
Expected: FAIL — `Failed to resolve import "./conversations"`.

- [ ] **Step 3: Add the wire types**

In `web/src/lib/api-types.ts`, after the `PlaygroundPreset` block:

```ts
/** A saved Chat-mode conversation, as the history rail lists it. */
export type PlaygroundConversation = {
  id: string
  title: string
  dialect: PlaygroundDialect
  model: string
  /** The console's own settings, stored and returned untouched. */
  config: unknown
  /** The most recent user turn, truncated by the server. The rail draws one
   *  line of it beneath the title. */
  preview: string
  created_at: string
  updated_at: string
}

export type PlaygroundStoredTurn = {
  seq: number
  role: string
  content: string
  /** Empty when the turn has no trace, which is ordinary: a turn can be stored
   *  before the log writer's batch lands, and the log's retention sweep
   *  outlives plenty of conversations. */
  request_id: string
  created_at: string
}

export type PlaygroundConversationDetail = PlaygroundConversation & {
  messages: PlaygroundStoredTurn[]
}
```

- [ ] **Step 4: Add the keys and the read hooks**

In `web/src/lib/queries.ts`, add to `keys` after `playgroundPresets`:

```ts
  playgroundConversations: ["playground-conversations"] as const,
  playgroundConversation: (id: string) => ["playground-conversations", id] as const,
```

and after `usePlaygroundPresets`:

```ts
export function usePlaygroundConversations(extra?: Extra<PlaygroundConversation[]>) {
  return useQuery({
    queryKey: keys.playgroundConversations,
    queryFn: () => api.get<PlaygroundConversation[]>("/api/playground/conversations"),
    ...extra,
  })
}

export function usePlaygroundConversation(
  id: string,
  extra?: Extra<PlaygroundConversationDetail>,
) {
  return useQuery({
    queryKey: keys.playgroundConversation(id),
    queryFn: () =>
      api.get<PlaygroundConversationDetail>(`/api/playground/conversations/${id}`),
    ...extra,
  })
}
```

Extend that file's `api-types` import with `PlaygroundConversation` and `PlaygroundConversationDetail`.

- [ ] **Step 5: Write the data layer**

Create `web/src/features/playground/lib/conversations.ts`:

```ts
import { api } from "../../../lib/api"
import { useApiMutation } from "../../../lib/mutations"
import { keys } from "../../../lib/queries"
import { mergeStoredConfig, toStoredConfig } from "../preset-config"
import { DIALECTS, type PlaygroundConfig } from "../config"
import type { TurnRoute } from "../message"
import type {
  PlaygroundConversation,
  PlaygroundMessage,
  PlaygroundStoredTurn,
} from "../../../lib/api-types"

/** 9router's rule, and it is a good one: long enough to tell two conversations
 *  apart, short enough for a 260px rail. */
const TITLE_MAX = 52

/**
 * A conversation's name, taken from the turn that started it.
 *
 * On a word boundary rather than at the character: a title cut mid-word reads
 * as a rendering fault, and the rail is a list of them.
 */
export function titleFromPrompt(prompt: string): string {
  const clean = prompt.trim().replace(/\s+/g, " ")
  if (clean === "") return "New chat"
  if (clean.length <= TITLE_MAX) return clean
  const cut = clean.slice(0, TITLE_MAX - 1)
  const boundary = cut.lastIndexOf(" ")
  // A single word longer than the limit has no boundary to cut on, so the
  // hard cut is the only honest answer.
  return (boundary > 0 ? cut.slice(0, boundary) : cut) + "…"
}

/**
 * A stored conversation, reconstituted into pane state.
 *
 * The same merge a preset gets, through the same function: section 8.3 says
 * the blob is the same shape, and a second implementation of the merge would
 * be a second set of rules about which stored fields are trustworthy.
 */
export function configOfConversation(c: PlaygroundConversation): PlaygroundConfig {
  // A row can be written by hand with curl, so the dialect column is a wire
  // value like any other: dialect-support.ts has no fallback case for one
  // outside the three it knows, so an unrecognized value crashes the pane's
  // render rather than degrading like the rest of the blob does.
  const dialect = DIALECTS.includes(c.dialect) ? c.dialect : "openai"
  return mergeStoredConfig(c.config, c.model, dialect)
}

/** The transcript, as the send loop holds it. */
export function messagesOfTurns(turns: PlaygroundStoredTurn[]): PlaygroundMessage[] {
  return turns.map((t) => ({ role: t.role, content: t.content }))
}

/**
 * What a reopened turn can still say about its routing.
 *
 * Only the trace link. The provider, the timings and the cost were read off a
 * trace at the time and are not stored, so filling them in from anywhere else
 * would print numbers nobody measured. A turn whose trace has been swept gets
 * no route at all, which is the state the transcript already renders for a
 * turn whose trace has not landed yet.
 */
export function routesOfTurns(turns: PlaygroundStoredTurn[]): Record<number, TurnRoute> {
  const out: Record<number, TurnRoute> = {}
  turns.forEach((turn, index) => {
    if (turn.request_id === "") return
    out[index] = {
      requestId: turn.request_id,
      provider: "",
      model: "",
      totalMs: null,
      tokensIn: null,
      tokensOut: null,
      costMicros: null,
      failedOver: [],
      warnings: [],
    }
  })
  return out
}

/** What a write sends. model and dialect are columns; the rest is the blob. */
export function conversationBody(title: string, config: PlaygroundConfig) {
  return {
    title,
    dialect: config.dialect,
    model: config.model,
    config: toStoredConfig(config),
  }
}

/** No success toast on any of these: a conversation that saved itself is
 *  reported by the rail updating, and a toast per turn would be noise on every
 *  message the operator sends. A failure still toasts, through useApiMutation's
 *  single error path. */
export function useCreateConversation() {
  return useApiMutation<PlaygroundConversation, { title: string; config: PlaygroundConfig }>({
    mutationFn: (vars) =>
      api.post<PlaygroundConversation>(
        "/api/playground/conversations",
        conversationBody(vars.title, vars.config),
      ),
    invalidates: [keys.playgroundConversations],
  })
}

export function useAppendTurn() {
  return useApiMutation<
    { seq: number },
    { id: string; role: "user" | "assistant"; content: string; requestId: string }
  >({
    mutationFn: (vars) =>
      api.post<{ seq: number }>(`/api/playground/conversations/${vars.id}/messages`, {
        role: vars.role,
        content: vars.content,
        request_id: vars.requestId,
      }),
    invalidates: [keys.playgroundConversations],
  })
}

export function useUpdateConversation() {
  return useApiMutation<
    { id: string },
    { id: string; title: string; config: PlaygroundConfig }
  >({
    mutationFn: (vars) =>
      api.patch<{ id: string }>(
        `/api/playground/conversations/${vars.id}`,
        conversationBody(vars.title, vars.config),
      ),
    invalidates: [keys.playgroundConversations],
  })
}

export function useDeleteConversation() {
  return useApiMutation<void, { id: string; title: string }>({
    mutationFn: (vars) => api.del<void>(`/api/playground/conversations/${vars.id}`),
    success: (_data, vars) => `Deleted ${vars.title}`,
    invalidates: [keys.playgroundConversations],
  })
}

export function usePurgeConversations() {
  return useApiMutation<{ deleted: number }, void>({
    mutationFn: () => api.del<{ deleted: number }>("/api/playground/conversations"),
    success: (data) =>
      data.deleted === 1 ? "Deleted 1 conversation" : `Deleted ${data.deleted} conversations`,
    invalidates: [keys.playgroundConversations],
  })
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && npx vitest run conversations 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 7: Run the frontend gate**

Run: `cd web && npm test 2>&1 | tail -8 && npm run typecheck 2>&1 | tail -5`
Expected: 60 files, 596 tests, all passing; typecheck silent.

- [ ] **Step 8: Commit**

```bash
git add web/src/lib/api-types.ts web/src/lib/queries.ts web/src/features/playground/lib/conversations.ts web/src/features/playground/lib/conversations.test.ts
git commit -m "feat(playground): add the conversation data layer"
```

---

### Task 7: `useChatRun` learns to reopen a conversation and to report a turn

**Files:**
- Modify: `web/src/features/playground/lib/use-chat-run.ts`
- Modify: `web/src/features/playground/lib/use-chat-run.test.tsx` (append two tests inside the existing `describe`)

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `export type CompletedTurn = { prompt: string; answer: string; requestId: string }`
  - a third, optional parameter: `useChatRun(config, onMetrics, onTurn?: (turn: CompletedTurn) => void)`. The existing two-argument call in `chat-tab.tsx` is unchanged and must stay unchanged.
  - `ChatRun` gains `load: (messages: PlaygroundMessage[], routes: Record<number, TurnRoute>) => void`

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: the hook exists and the change is two additive parameters; §8.2 fixes that persistence lives at the call site rather than in the hook

- [ ] **Step 1: Write the failing tests**

Append inside the existing `describe("running one chat turn", …)` block in `web/src/features/playground/lib/use-chat-run.test.tsx`:

```ts
  it("reports one completed turn, with the id its trace will carry", async () => {
    // Chat mode persists from here. Lab's Single tab passes no callback and
    // persists nothing, which is what keeps the two surfaces sharing one loop
    // without sharing a storage decision.
    yields(frame("Hel"), frame("lo"))
    traceMock.mockResolvedValue(null)
    const turns: unknown[] = []

    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}, (t) => turns.push(t)),
    )
    await act(() => result.current.send("hi"))
    await waitFor(() => expect(result.current.busy).toBe(false))

    expect(turns).toEqual([{ prompt: "hi", answer: "Hello", requestId: "01TRACE" }])
  })

  it("reports nothing for a turn that failed", async () => {
    // A failed run leaves an empty assistant bubble on screen and nothing
    // worth keeping. Persisting it would put a blank turn in the transcript
    // the operator reopens tomorrow.
    streamMock.mockImplementation(async function* () {
      throw new Error("upstream refused")
      // eslint-disable-next-line no-unreachable
      yield ""
    })
    const turns: unknown[] = []

    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}, (t) => turns.push(t)),
    )
    await act(() => result.current.send("hi"))
    await waitFor(() => expect(result.current.busy).toBe(false))

    expect(result.current.error).toBe("upstream refused")
    expect(turns).toEqual([])
  })

  it("loads a stored transcript over whatever is on screen", async () => {
    yields(frame("x"))
    traceMock.mockResolvedValue(null)
    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}),
    )
    await act(() => result.current.send("hi"))
    await waitFor(() => expect(result.current.busy).toBe(false))

    act(() =>
      result.current.load(
        [
          { role: "user", content: "from last week" },
          { role: "assistant", content: "an answer" },
        ],
        {
          1: {
            requestId: "01OLD", provider: "", model: "",
            totalMs: null, tokensIn: null, tokensOut: null, costMicros: null,
            failedOver: [], warnings: [],
          },
        },
      ),
    )

    expect(result.current.messages).toEqual([
      { role: "user", content: "from last week" },
      { role: "assistant", content: "an answer" },
    ])
    expect(result.current.routes[1]?.requestId).toBe("01OLD")
    expect(result.current.error).toBe("")
  })
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run use-chat-run 2>&1 | tail -20`
Expected: the first two fail on `turns` staying empty or the callback not being accepted; the third fails on `result.current.load is not a function`.

- [ ] **Step 3: Add the turn type and the third parameter**

In `web/src/features/playground/lib/use-chat-run.ts`, add above `ChatRun`:

```ts
/**
 * One turn that finished, reported to whoever wants to keep it.
 *
 * The hook does not store anything itself. Lab's Single tab and Chat mode
 * share this loop and disagree about persistence -- section 8.2 says Lab's
 * tabs persist nothing -- so the decision belongs at the call site, and a
 * callback is the smallest thing that can carry it.
 */
export type CompletedTurn = {
  prompt: string
  answer: string
  /** Empty when the response carried no id. */
  requestId: string
}
```

and extend `ChatRun`:

```ts
  /** Replaces the transcript wholesale, for a conversation reopened from the
   *  history rail. */
  load: (messages: PlaygroundMessage[], routes: Record<number, TurnRoute>) => void
```

Change the signature to:

```ts
export function useChatRun(
  config: PlaygroundConfig,
  onMetrics: (m: StreamMetrics) => void,
  onTurn?: (turn: CompletedTurn) => void,
): ChatRun {
```

- [ ] **Step 4: Accumulate the answer and report the turn**

Inside `send`, add a local accumulator beside `buffer.current` and route every append through it. Replace the three `appendToLastMessage(...)` call sites in `send` with `emit(...)`, and add after `let liveRequestId = ""`:

```ts
    // The rendered text lives in a functional setState, so it is never in a
    // variable this scope can read. A completed turn has to be reported with
    // its whole answer, so it is accumulated here as well as appended.
    let answer = ""
    let failed = false
    const emit = (text: string) => {
      if (!text) return
      answer += text
      appendToLastMessage(text)
    }
```

In the `catch`, set the flag beside the existing error:

```ts
    } catch (err) {
      // Stopping is the operator's decision, not a failure to report at them.
      if ((err as Error).name !== "AbortError") {
        setError((err as Error).message)
        failed = true
      }
    } finally {
      abort.current = null
      setBusy(false)
    }
    // After the finally, so a stopped run still reports: the turns already
    // written stay, and a half answer is what the tokens were spent on.
    if (!failed) onTurn?.({ prompt, answer, requestId: liveRequestId })
```

- [ ] **Step 5: Add `load`**

Beside `clear`:

```ts
  /** Aborts anything in flight first: a stream left running would append its
   *  next chunk into the transcript that has just replaced it. */
  function load(next: PlaygroundMessage[], nextRoutes: Record<number, TurnRoute>) {
    abort.current?.abort()
    setMessages(next)
    setRoutes(nextRoutes)
    setError("")
    onMetrics(NO_METRICS)
  }
```

and return it: `return { messages, routes, busy, error, send, stop, clear, load }`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && npx vitest run use-chat-run 2>&1 | tail -10`
Expected: PASS, all eight tests in the file.

- [ ] **Step 7: Run the frontend gate**

Run: `cd web && npm test 2>&1 | tail -8 && npm run typecheck 2>&1 | tail -5`
Expected: 60 files, 599 tests, all passing; typecheck silent.

- [ ] **Step 8: Commit**

```bash
git add web/src/features/playground/lib/use-chat-run.ts web/src/features/playground/lib/use-chat-run.test.tsx
git commit -m "feat(playground): report a finished turn and reopen one"
```

---

### Task 8: The mode switch, and Lab mode

**Files:**
- Create: `web/src/features/playground/mode.ts`
- Create: `web/src/features/playground/mode.test.ts`
- Create: `web/src/features/playground/lab-mode.tsx` (the whole of today's `PlaygroundScreen` body, moved)
- Modify: `web/src/features/playground/playground-screen.tsx` (becomes the mode router)
- Modify: `web/src/features/playground/metrics.tsx:150-155` (delete `hasReadings`, which loses its only caller)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `export type PlaygroundMode = "chat" | "lab"`
  - `isMode(value: unknown): value is PlaygroundMode`
  - `initialMode(search: { mode?: string; seed?: string }): PlaygroundMode`
  - `storedMode(): PlaygroundMode | undefined`, `rememberMode(mode: PlaygroundMode): void`
  - `<LabMode config={PlaygroundConfig} onConfigChange={(next: PlaygroundConfig) => void} />` — Lab's request settings are owned by `PlaygroundScreen`, not by `LabMode`, so that Chat mode's *open this configuration in Lab* is a `setState` and a mode switch rather than a value smuggled through storage or the URL. `LabMode` still owns its own `StreamMetrics` and its tab.
  - `playground-screen.tsx` renders `<ChatMode />` when the mode is `chat`; Task 10 creates that component, so until then this task renders a `null` placeholder and Task 10 replaces it.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: §4 fixes the switch's placement and behaviour, and the Lab body is today's screen moved rather than designed

- [ ] **Step 1: Write the failing tests**

Create `web/src/features/playground/mode.test.ts`:

```ts
import { beforeEach, describe, expect, it } from "vitest"
import { initialMode, isMode, rememberMode, storedMode } from "./mode"

describe("the playground mode", () => {
  beforeEach(() => localStorage.clear())

  it("recognises only the two modes", () => {
    expect(isMode("chat")).toBe(true)
    expect(isMode("lab")).toBe(true)
    expect(isMode("compare")).toBe(false)
    expect(isMode(undefined)).toBe(false)
  })

  it("survives a reload", () => {
    rememberMode("chat")
    expect(storedMode()).toBe("chat")
    expect(initialMode({})).toBe("chat")
  })

  it("lets the URL win over the stored preference", () => {
    // A link says where its sender meant. Silently redirecting it to the
    // reader's own last choice would make a shared link mean two things.
    rememberMode("chat")
    expect(initialMode({ mode: "lab" })).toBe("lab")
  })

  it("ignores a mode the URL made up", () => {
    rememberMode("chat")
    expect(initialMode({ mode: "compare" })).toBe("chat")
  })

  it("opens a seeded link in Lab", () => {
    // A seed is a routing investigation, not a conversation.
    rememberMode("chat")
    expect(initialMode({ seed: "01ABC" })).toBe("lab")
    // Unless the sender said otherwise, which they can only do on purpose.
    expect(initialMode({ seed: "01ABC", mode: "chat" })).toBe("chat")
  })

  it("opens in Lab when nothing has been chosen", () => {
    expect(initialMode({})).toBe("lab")
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run mode 2>&1 | tail -10`
Expected: FAIL — `Failed to resolve import "./mode"`.

- [ ] **Step 3: Write `mode.ts`**

Create `web/src/features/playground/mode.ts`:

```ts
export type PlaygroundMode = "chat" | "lab"

const STORAGE_KEY = "darkrouter.playground.mode"

export function isMode(value: unknown): value is PlaygroundMode {
  return value === "chat" || value === "lab"
}

export function storedMode(): PlaygroundMode | undefined {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    return isMode(raw) ? raw : undefined
  } catch {
    // A private window and a browser set to block site data both throw on
    // read. The mode is a convenience, so losing it is not an error state.
    return undefined
  }
}

export function rememberMode(mode: PlaygroundMode) {
  try {
    localStorage.setItem(STORAGE_KEY, mode)
  } catch {
    // Nothing to do. The choice still applies to this page.
  }
}

/**
 * The mode a fresh load opens in.
 *
 * The URL beats the stored preference because a link says where its sender
 * meant, and a seed beats both because a seed is a routing investigation --
 * the trace drawer's "Open in playground" wants the instrument, not a
 * conversation. An explicit ?mode= still wins over the seed, since nothing
 * puts one there by accident.
 */
export function initialMode(search: { mode?: string; seed?: string }): PlaygroundMode {
  if (isMode(search.mode)) return search.mode
  if (search.seed !== undefined) return "lab"
  return storedMode() ?? "lab"
}
```

- [ ] **Step 4: Move today's screen body into `lab-mode.tsx`**

Create `web/src/features/playground/lab-mode.tsx` with the current `PlaygroundScreen` body, minus the outer `div` and the `PageHeader`, and with three changes: the first tab is `single` rather than `chat`, the metrics strip no longer waits for a reading, and `hasReadings` is gone from the import.

```tsx
import { useState } from "react"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { ChatTab } from "./chat-tab"
import { Compare } from "./compare"
import { AuxPanels, Count } from "./aux-panels"
import { ConfigPane } from "./config-pane/config-pane"
import type { PlaygroundConfig } from "./config"
import { MetricsStrip, NO_METRICS, type StreamMetrics } from "./metrics"

/**
 * The instrument: four surfaces that send a real request, one config pane
 * describing that request, and the readings the last run produced.
 *
 * The request settings are one object shared by all four surfaces, because
 * they belong to the request rather than to whichever surface sent it: a
 * system prompt typed on Single used to be invisible to Compare and lost on a
 * tab switch. They are held by the screen above rather than here, so that
 * Chat mode can hand a conversation's configuration across without routing it
 * through storage or the URL.
 *
 * Auxiliary and Count keep their own controls. They send embeddings, images
 * and token counts, which take different inputs from a chat turn, and pointing
 * a chat model picker at them would be a control that lies.
 */
export function LabMode({
  config,
  onConfigChange,
}: {
  config: PlaygroundConfig
  onConfigChange: (next: PlaygroundConfig) => void
}) {
  const [metrics, setMetrics] = useState<StreamMetrics>(NO_METRICS)
  const [tab, setTab] = useState("single")
  const sends = tab === "single" || tab === "compare"

  return (
    <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
      {/* Sized to its tabs: stretched across the page it reads as an empty
          band with four words adrift in it. */}
      <TabsList className="mx-6 w-fit">
        <TabsTrigger value="single">Single</TabsTrigger>
        <TabsTrigger value="compare">Compare</TabsTrigger>
        <TabsTrigger value="auxiliary">Auxiliary</TabsTrigger>
        <TabsTrigger value="count">Count</TabsTrigger>
      </TabsList>

      {/* The height is reserved from the start, em dashes and all. This is a
          mode whose whole purpose is measurement, and a strip that appeared
          after the first run would shift the transcript down at exactly the
          moment the operator started reading it. */}
      {sends && <MetricsStrip metrics={metrics} />}

      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <TabsContent value="single" className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
            <ChatTab config={config} onConfigChange={onConfigChange} onMetrics={setMetrics} />
          </TabsContent>
          <TabsContent value="compare" className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
            <Compare config={config} />
          </TabsContent>
          <TabsContent value="auxiliary" className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
            <AuxPanels />
          </TabsContent>
          <TabsContent value="count" className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden">
            <Count />
          </TabsContent>
        </div>
        {sends && <ConfigPane config={config} onChange={onConfigChange} />}
      </div>
    </Tabs>
  )
}
```

- [ ] **Step 5: Delete `hasReadings`**

Remove the function and its doc comment at `web/src/features/playground/metrics.tsx:150-155`. It has no test and, once the strip reserves its height in Lab and Chat mode shows no strip at all, no caller.

- [ ] **Step 6: Turn `playground-screen.tsx` into the mode router**

Replace the whole file:

```tsx
import { useState } from "react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { PageHeader } from "darkraise-ui/layout"
import { ToggleGroup, ToggleGroupItem } from "darkraise-ui"
import { LabMode } from "./lab-mode"
import { emptyConfig, type PlaygroundConfig } from "./config"
import { initialMode, isMode, rememberMode, type PlaygroundMode } from "./mode"

/**
 * The playground, as two modes rather than one crowded instrument.
 *
 * Lab is the instrument: four surfaces, a config pane, and the readings from
 * the last run. Chat is a conversation that is still there tomorrow. They are
 * separate modes because one screen serving both meant a settings column
 * taking a fifth of the width from a reader, and a transcript that vanished on
 * reload for everyone else.
 *
 * The chosen mode is remembered per operator and carried in the URL, so a
 * shared link opens where its sender meant rather than where its reader last
 * was.
 */
export function PlaygroundScreen() {
  const search = useSearch({ strict: false })
  const navigate = useNavigate()
  const [mode, setMode] = useState<PlaygroundMode>(() => initialMode(search))
  // Lab's request settings live here rather than inside LabMode so that Chat
  // mode's "open this configuration in Lab" is a state change and a mode
  // switch, rather than a value smuggled across through storage or the URL.
  const [labConfig, setLabConfig] = useState<PlaygroundConfig>(emptyConfig)

  function choose(next: string) {
    if (!isMode(next) || next === mode) return
    setMode(next)
    rememberMode(next)
    void navigate({ to: "/playground", search: (prev) => ({ ...prev, mode: next }) })
  }

  return (
    <div className="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col">
      <PageHeader
        title="Playground"
        description="Send a real request, and see what it cost"
        actions={
          <ToggleGroup type="single" value={mode} onValueChange={choose} aria-label="Playground mode">
            <ToggleGroupItem value="chat">Chat</ToggleGroupItem>
            <ToggleGroupItem value="lab">Lab</ToggleGroupItem>
          </ToggleGroup>
        }
      />
      {mode === "chat" ? null : <LabMode config={labConfig} onConfigChange={setLabConfig} />}
    </div>
  )
}
```

The `null` branch is replaced by `<ChatMode />` in Task 10; nothing else in this file changes then.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd web && npx vitest run mode 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 8: Run the frontend gate**

Run: `cd web && npm test 2>&1 | tail -8 && npm run typecheck 2>&1 | tail -5`
Expected: 61 files, 605 tests, all passing; typecheck silent.

- [ ] **Step 9: Commit**

```bash
git add web/src/features/playground/mode.ts web/src/features/playground/mode.test.ts web/src/features/playground/lab-mode.tsx web/src/features/playground/playground-screen.tsx web/src/features/playground/metrics.tsx
git commit -m "feat(playground): split the screen into Chat and Lab"
```

---

### Task 9: Chat mode's chrome — the history rail and the conversation header

**Files:**
- Create: `web/src/features/playground/chat/history-rail.tsx`
- Create: `web/src/features/playground/chat/history-rail.test.tsx`
- Create: `web/src/features/playground/chat/conversation-header.tsx`
- Create: `web/src/features/playground/chat/conversation-header.test.tsx`

**Interfaces:**
- Consumes: `PlaygroundConversation` from `../../../lib/api-types`; `relativeTime` from `../../providers/test-log-tab`; `ModelCombobox` and `useModelCandidates` from `../../shell/model-combobox`; `DIALECTS` and `PlaygroundConfig` from `../config`.
- Produces:
  - `<HistoryRail conversations activeId onSelect onNew onDelete collapsed onToggleCollapsed />`
  - `<ConversationHeader config onConfigChange title onTitleChange onOpenInLab onDelete canDelete />`

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4
**Approach:** inline - skip 3: §4.1 names every element in both components, and `preset-picker.tsx` and `compare-column.tsx` give the darkraise-ui idioms

- [ ] **Step 1: Write the failing tests**

Create `web/src/features/playground/chat/history-rail.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { HistoryRail } from "./history-rail"
import type { PlaygroundConversation } from "../../../lib/api-types"

function conversation(over: Partial<PlaygroundConversation> = {}): PlaygroundConversation {
  return {
    id: "c1",
    title: "speculative decoding",
    dialect: "openai",
    model: "gpt",
    config: {},
    preview: "explain the difference",
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...over,
  }
}

const noop = () => {}

describe("the history rail", () => {
  it("lists a conversation with what it was about", () => {
    render(
      <HistoryRail
        conversations={[conversation()]}
        activeId=""
        onSelect={noop}
        onNew={noop}
        onDelete={noop}
        collapsed={false}
        onToggleCollapsed={noop}
      />,
    )
    expect(screen.getByText("speculative decoding")).toBeInTheDocument()
    expect(screen.getByText("explain the difference")).toBeInTheDocument()
    expect(screen.getByText("just now")).toBeInTheDocument()
  })

  it("says so when there is nothing to retrieve yet", () => {
    render(
      <HistoryRail
        conversations={[]}
        activeId=""
        onSelect={noop}
        onNew={noop}
        onDelete={noop}
        collapsed={false}
        onToggleCollapsed={noop}
      />,
    )
    expect(screen.getByText(/no saved conversations/i)).toBeInTheDocument()
  })

  it("selects a conversation and starts a new one", async () => {
    const onSelect = vi.fn()
    const onNew = vi.fn()
    render(
      <HistoryRail
        conversations={[conversation()]}
        activeId=""
        onSelect={onSelect}
        onNew={onNew}
        onDelete={noop}
        collapsed={false}
        onToggleCollapsed={noop}
      />,
    )
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    expect(onSelect).toHaveBeenCalledWith("c1")
    await userEvent.click(screen.getByRole("button", { name: "New" }))
    expect(onNew).toHaveBeenCalled()
  })

  it("collapses to nothing", () => {
    // 260px of rail beside a 320px pane is three columns on a laptop. The
    // operator has to be able to give the transcript the width back.
    const { container } = render(
      <HistoryRail
        conversations={[conversation()]}
        activeId=""
        onSelect={noop}
        onNew={noop}
        onDelete={noop}
        collapsed
        onToggleCollapsed={noop}
      />,
    )
    expect(screen.queryByText("speculative decoding")).not.toBeInTheDocument()
    expect(container.querySelector("aside")).toBeNull()
  })
})
```

Create `web/src/features/playground/chat/conversation-header.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { ConversationHeader } from "./conversation-header"
import { emptyConfig } from "../config"

vi.mock("../../shell/model-combobox", () => ({
  useModelCandidates: () => ({ candidates: ["gpt", "claude"], loading: false }),
  ModelCombobox: ({ value, onChange, label }: {
    value: string
    onChange: (next: string) => void
    label: string
  }) => (
    <input aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}))

const noop = () => {}

function header(over: Record<string, unknown> = {}) {
  const props = {
    config: { ...emptyConfig(), model: "gpt" },
    onConfigChange: noop,
    title: "speculative decoding",
    onTitleChange: noop,
    onOpenInLab: noop,
    onDelete: noop,
    canDelete: true,
    ...over,
  }
  return render(<ConversationHeader {...(props as never)} />)
}

describe("the conversation header", () => {
  it("shows the model on the pill rather than hiding it in a pane", () => {
    header()
    expect(screen.getByRole("button", { name: /gpt/ })).toBeInTheDocument()
  })

  it("commits a retitle on Enter and abandons it on Escape", async () => {
    const onTitleChange = vi.fn()
    header({ onTitleChange })
    const field = screen.getByLabelText("Conversation title")
    await userEvent.clear(field)
    await userEvent.type(field, "renamed{Enter}")
    expect(onTitleChange).toHaveBeenCalledWith("renamed")

    onTitleChange.mockClear()
    await userEvent.clear(field)
    await userEvent.type(field, "discarded{Escape}")
    expect(onTitleChange).not.toHaveBeenCalled()
    expect(screen.getByLabelText("Conversation title")).toHaveValue("speculative decoding")
  })

  it("never commits an empty title, because the rail would draw a blank row", async () => {
    const onTitleChange = vi.fn()
    header({ onTitleChange })
    const field = screen.getByLabelText("Conversation title")
    await userEvent.clear(field)
    await userEvent.type(field, "{Enter}")
    expect(onTitleChange).not.toHaveBeenCalled()
  })

  it("carries the configuration into Lab", async () => {
    const onOpenInLab = vi.fn()
    header({ onOpenInLab })
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    await userEvent.click(screen.getByRole("menuitem", { name: /open in lab/i }))
    expect(onOpenInLab).toHaveBeenCalled()
  })

  it("edits the system prompt, which is the one Lab setting a conversation needs", async () => {
    const onConfigChange = vi.fn()
    header({ onConfigChange })
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    await userEvent.click(screen.getByRole("menuitem", { name: /system prompt/i }))
    await userEvent.type(screen.getByLabelText("System prompt"), "be brief")
    await userEvent.click(screen.getByRole("button", { name: "Save" }))
    expect(onConfigChange).toHaveBeenCalledWith(
      expect.objectContaining({ system: "be brief" }),
    )
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run history-rail conversation-header 2>&1 | tail -15`
Expected: FAIL — both imports unresolved.

- [ ] **Step 3: Write the history rail**

Create `web/src/features/playground/chat/history-rail.tsx`:

```tsx
import { Button } from "darkraise-ui"
import { MessageSquarePlus, PanelLeftClose, PanelLeftOpen, Trash2 } from "lucide-react"
import { relativeTime } from "../../providers/test-log-tab"
import type { PlaygroundConversation } from "../../../lib/api-types"

/**
 * Every conversation, newest first.
 *
 * This is the whole reason Chat mode is a mode rather than a tab: a chat
 * surface without retrievable history is a scratchpad, and nobody keeps a
 * scratchpad. Each row carries the title, what the last turn was about, and
 * how long ago — enough to recognise a conversation without opening it.
 *
 * Collapsible to nothing because a 260px rail and a transcript are two columns
 * an operator sometimes wants to be one.
 */
export function HistoryRail({
  conversations,
  activeId,
  onSelect,
  onNew,
  onDelete,
  collapsed,
  onToggleCollapsed,
}: {
  conversations: PlaygroundConversation[]
  activeId: string
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (conversation: PlaygroundConversation) => void
  collapsed: boolean
  onToggleCollapsed: () => void
}) {
  if (collapsed) {
    return (
      <div className="shrink-0 border-r p-2">
        <Button variant="ghost" size="icon" aria-label="Show conversations" onClick={onToggleCollapsed}>
          <PanelLeftOpen className="size-[var(--icon-size)]" aria-hidden="true" />
        </Button>
      </div>
    )
  }

  return (
    <aside className="flex w-[260px] shrink-0 flex-col border-r">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <span className="text-sm font-medium">Conversations</span>
        <Button variant="ghost" size="icon" aria-label="Hide conversations" onClick={onToggleCollapsed}>
          <PanelLeftClose className="size-[var(--icon-size)]" aria-hidden="true" />
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {conversations.length === 0 ? (
          <p className="p-3 text-sm text-[hsl(var(--legend))]">
            No saved conversations yet. Send a message and this is where it stays.
          </p>
        ) : (
          <ul className="flex flex-col">
            {conversations.map((c) => (
              <li key={c.id} className="group relative border-b last:border-b-0">
                <button
                  type="button"
                  onClick={() => onSelect(c.id)}
                  aria-current={c.id === activeId ? "true" : undefined}
                  className={`flex w-full flex-col gap-0.5 px-3 py-2 pr-9 text-left text-sm hover:bg-[hsl(var(--muted))] ${
                    c.id === activeId ? "bg-[hsl(var(--muted))]" : ""
                  }`}
                >
                  <span className="truncate font-medium">{c.title}</span>
                  {/* One line, and only one: the rail is for recognising a
                      conversation, not for reading it. */}
                  <span className="truncate text-[hsl(var(--muted-foreground))]">
                    {c.preview === "" ? "No messages yet" : c.preview}
                  </span>
                  <span className="text-[hsl(var(--legend))]">
                    {relativeTime(new Date(c.updated_at).getTime())}
                  </span>
                </button>
                <button
                  type="button"
                  aria-label={`Delete ${c.title}`}
                  onClick={() => onDelete(c)}
                  className="absolute top-2 right-2 p-1 text-[hsl(var(--legend))] opacity-0 group-hover:opacity-100 focus-visible:opacity-100 hover:text-[hsl(var(--destructive))]"
                >
                  <Trash2 className="size-[var(--icon-size)]" aria-hidden="true" />
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="shrink-0 border-t p-2">
        <Button variant="ghost" className="w-full justify-start gap-2" onClick={onNew}>
          <MessageSquarePlus className="size-[var(--icon-size)]" aria-hidden="true" />
          New
        </Button>
      </div>
    </aside>
  )
}
```

- [ ] **Step 4: Write the conversation header**

Create `web/src/features/playground/chat/conversation-header.tsx`:

```tsx
import { useEffect, useState } from "react"
import {
  Button, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
  Input, Label, Popover, PopoverContent, PopoverTrigger,
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Textarea,
} from "darkraise-ui"
import { MoreHorizontal } from "lucide-react"
import { ModelCombobox, useModelCandidates } from "../../shell/model-combobox"
import { DIALECTS, type PlaygroundConfig } from "../config"
import type { PlaygroundDialect } from "../../../lib/api-types"

/**
 * What a conversation is, above the conversation.
 *
 * Chat mode shows no config pane, so the two settings a conversation genuinely
 * needs are reachable from here instead: the model and the dialect from the
 * pill's popover, the system prompt from the overflow menu. Everything else a
 * request can carry belongs to Lab, and the menu's *open in Lab* is how a
 * conversation gets there without being retyped.
 */
export function ConversationHeader({
  config,
  onConfigChange,
  title,
  onTitleChange,
  onOpenInLab,
  onDelete,
  canDelete,
}: {
  config: PlaygroundConfig
  onConfigChange: (next: PlaygroundConfig) => void
  title: string
  onTitleChange: (next: string) => void
  onOpenInLab: () => void
  onDelete: () => void
  canDelete: boolean
}) {
  const { candidates, loading } = useModelCandidates()
  const [draftTitle, setDraftTitle] = useState(title)
  const [systemOpen, setSystemOpen] = useState(false)
  const [draftSystem, setDraftSystem] = useState(config.system)

  // The field follows the conversation, not the keystroke: selecting another
  // conversation in the rail must not leave the previous one's name in it.
  useEffect(() => setDraftTitle(title), [title])

  function commitTitle() {
    const next = draftTitle.trim()
    // An empty title would draw a blank row in the rail, so it is refused
    // rather than accepted and papered over with a placeholder.
    if (next === "" || next === title) {
      setDraftTitle(title)
      return
    }
    onTitleChange(next)
  }

  return (
    <div className="flex shrink-0 items-center gap-2 border-b px-6 py-2">
      <Popover>
        <PopoverTrigger asChild>
          <Button variant="outline" size="sm" className="max-w-[18rem] truncate font-mono">
            {config.model === "" ? "Choose a model" : config.model}
          </Button>
        </PopoverTrigger>
        <PopoverContent className="flex w-80 flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pgc-model">Model</Label>
            <ModelCombobox
              label="Model or alias"
              value={config.model}
              candidates={candidates}
              loading={loading}
              onChange={(model) => onConfigChange({ ...config, model })}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pgc-dialect">Dialect</Label>
            <Select
              value={config.dialect}
              onValueChange={(dialect) =>
                onConfigChange({ ...config, dialect: dialect as PlaygroundDialect })
              }
            >
              <SelectTrigger id="pgc-dialect">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {DIALECTS.map((d) => (
                  <SelectItem key={d} value={d}>
                    {d}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </PopoverContent>
      </Popover>

      <Input
        aria-label="Conversation title"
        value={draftTitle}
        onChange={(e) => setDraftTitle(e.target.value)}
        onBlur={commitTitle}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault()
            commitTitle()
            e.currentTarget.blur()
          }
          if (e.key === "Escape") {
            setDraftTitle(title)
            e.currentTarget.blur()
          }
        }}
        className="flex-1 border-transparent bg-transparent px-2 hover:border-[hsl(var(--border))] focus:border-[hsl(var(--border))]"
      />

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" aria-label="Conversation actions">
            <MoreHorizontal className="size-[var(--icon-size)]" aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem
            onSelect={() => {
              setDraftSystem(config.system)
              setSystemOpen(true)
            }}
          >
            Edit system prompt
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={onOpenInLab}>Open in Lab</DropdownMenuItem>
          <DropdownMenuItem disabled={!canDelete} onSelect={onDelete}>
            Delete conversation
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={systemOpen} onOpenChange={setSystemOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>System prompt</DialogTitle>
            <DialogDescription>
              Sent ahead of every turn in this conversation, and stored with it.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pgc-system">System prompt</Label>
            <Textarea
              id="pgc-system"
              aria-label="System prompt"
              rows={6}
              value={draftSystem}
              onChange={(e) => setDraftSystem(e.target.value)}
            />
          </div>
          <div className="mt-2 flex items-center justify-end gap-2 border-t pt-3">
            <Button variant="ghost" onClick={() => setSystemOpen(false)}>
              Cancel
            </Button>
            <Button
              onClick={() => {
                onConfigChange({ ...config, system: draftSystem })
                setSystemOpen(false)
              }}
            >
              Save
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web && npx vitest run history-rail conversation-header 2>&1 | tail -12`
Expected: PASS.

- [ ] **Step 6: Run the frontend gate**

Run: `cd web && npm test 2>&1 | tail -8 && npm run typecheck 2>&1 | tail -5`
Expected: 63 files, 614 tests, all passing; typecheck silent.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/playground/chat/
git commit -m "feat(playground): add Chat mode's rail and header"
```

---

### Task 10: Chat mode, and auto-save

**Files:**
- Create: `web/src/features/playground/chat/chat-mode.tsx`
- Create: `web/src/features/playground/chat/chat-mode.test.tsx`
- Modify: `web/src/features/playground/playground-screen.tsx` (replace the `null` branch)

**Interfaces:**
- Consumes: Task 6's hooks and helpers, Task 7's `useChatRun(config, onMetrics, onTurn)` and `load`, Task 9's `<HistoryRail />` and `<ConversationHeader />`, the existing `<Transcript />` and `<Composer />`.
- Produces: `<ChatMode onOpenInLab={(config: PlaygroundConfig) => void} />`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: §4.1 fixes the three regions and §8.5 fixes the title, the mid-conversation PATCH and the create-on-first-turn rule

- [ ] **Step 1: Write the failing test**

Create `web/src/features/playground/chat/chat-mode.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ChatMode } from "./chat-mode"
import type { PlaygroundConversation, PlaygroundConversationDetail } from "../../../lib/api-types"

const stored: PlaygroundConversation = {
  id: "c1",
  title: "speculative decoding",
  dialect: "anthropic",
  model: "claude",
  config: { system: "answer in one line" },
  preview: "explain it",
  created_at: "2026-08-30T10:00:00Z",
  updated_at: "2026-08-30T10:00:00Z",
}

const detail: PlaygroundConversationDetail = {
  ...stored,
  messages: [
    { seq: 0, role: "user", content: "explain it", request_id: "", created_at: "2026-08-30T10:00:00Z" },
    { seq: 1, role: "assistant", content: "in one line", request_id: "01OLD", created_at: "2026-08-30T10:00:01Z" },
  ],
}

const { postMock, patchMock, delMock, streamMock, traceMock } = vi.hoisted(() => ({
  postMock: vi.fn(),
  patchMock: vi.fn(),
  delMock: vi.fn(),
  streamMock: vi.fn(),
  traceMock: vi.fn(),
}))

vi.mock("../../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/api")>()),
  api: { get: vi.fn(), post: postMock, patch: patchMock, del: delMock },
  stream: streamMock,
}))

vi.mock("../../../lib/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/queries")>()),
  usePlaygroundConversations: () => ({ data: [stored], isLoading: false }),
  usePlaygroundConversation: (id: string) => ({
    data: id === "c1" ? detail : undefined,
    isLoading: false,
  }),
}))

// traceWhenWritten waits 300ms and retries six times by design; left real,
// every test here would spend 1.8s inside it.
vi.mock("../metrics", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../metrics")>()),
  traceWhenWritten: traceMock,
}))

vi.mock("../../shell/model-combobox", () => ({
  useModelCandidates: () => ({ candidates: ["gpt"], loading: false }),
  ModelCombobox: ({ value, onChange, label }: {
    value: string
    onChange: (next: string) => void
    label: string
  }) => <input aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />,
}))

function mounted(onOpenInLab = () => {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <ChatMode onOpenInLab={onOpenInLab} />
    </QueryClientProvider>,
  )
}

async function send(text: string) {
  await userEvent.type(screen.getByLabelText("Message"), text)
  await userEvent.click(screen.getByRole("button", { name: "Send" }))
}

describe("Chat mode", () => {
  beforeEach(() => {
    postMock.mockReset()
    patchMock.mockReset()
    delMock.mockReset()
    streamMock.mockReset()
    traceMock.mockReset()
    traceMock.mockResolvedValue(null)
    streamMock.mockImplementation(async function* (
      _path: string,
      _body: unknown,
      onStart?: (s: { requestId: string }) => void,
    ) {
      onStart?.({ requestId: "01NEW" })
      yield `data: ${JSON.stringify({ choices: [{ delta: { content: "an answer" } }] })}\n\n`
    })
  })

  it("saves exactly one user turn and one assistant turn per exchange", async () => {
    // The count is the assertion. A second create, or a duplicated message,
    // is the failure this feature makes easy and expensive.
    postMock.mockImplementation((path: string) =>
      path === "/api/playground/conversations"
        ? Promise.resolve({ ...stored, id: "new1", title: "hello there" })
        : Promise.resolve({ seq: 0 }),
    )
    mounted()
    await userEvent.click(screen.getByRole("button", { name: /choose a model|gpt/i }))
    await userEvent.type(screen.getByLabelText("Model or alias"), "gpt")
    await userEvent.keyboard("{Escape}")
    await send("hello there")

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(3))
    const paths = postMock.mock.calls.map((c) => c[0] as string)
    expect(paths.filter((p) => p === "/api/playground/conversations")).toHaveLength(1)
    expect(paths.filter((p) => p === "/api/playground/conversations/new1/messages")).toHaveLength(2)

    const [, createBody] = postMock.mock.calls.find(
      (c) => c[0] === "/api/playground/conversations",
    ) as [string, { title: string }]
    // Titled from the first turn rather than left as "New chat": a rail of
    // identical placeholders retrieves nothing.
    expect(createBody.title).toBe("hello there")

    const turns = postMock.mock.calls.filter((c) =>
      (c[0] as string).endsWith("/messages"),
    ) as [string, { role: string; content: string; request_id: string }][]
    expect(turns[0]![1]).toEqual({ role: "user", content: "hello there", request_id: "" })
    expect(turns[1]![1]).toEqual({ role: "assistant", content: "an answer", request_id: "01NEW" })
  })

  it("creates one conversation across two exchanges, not two", async () => {
    postMock.mockImplementation((path: string) =>
      path === "/api/playground/conversations"
        ? Promise.resolve({ ...stored, id: "new1", title: "first" })
        : Promise.resolve({ seq: 0 }),
    )
    mounted()
    await userEvent.click(screen.getByRole("button", { name: /choose a model|gpt/i }))
    await userEvent.type(screen.getByLabelText("Model or alias"), "gpt")
    await userEvent.keyboard("{Escape}")
    await send("first")
    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(3))
    await send("second")
    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(5))

    const creates = postMock.mock.calls.filter((c) => c[0] === "/api/playground/conversations")
    expect(creates).toHaveLength(1)
  })

  it("reopens a conversation with its system prompt intact", async () => {
    mounted()
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))

    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())
    expect(screen.getByText("explain it")).toBeInTheDocument()
    expect(screen.getByLabelText("Conversation title")).toHaveValue("speculative decoding")

    // The setting that shaped every answer above, restored rather than lost.
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    await userEvent.click(screen.getByRole("menuitem", { name: /system prompt/i }))
    expect(screen.getByLabelText("System prompt")).toHaveValue("answer in one line")
  })

  it("hands the whole configuration to Lab", async () => {
    const onOpenInLab = vi.fn()
    mounted(onOpenInLab)
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())

    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    await userEvent.click(screen.getByRole("menuitem", { name: /open in lab/i }))
    expect(onOpenInLab).toHaveBeenCalledWith(
      expect.objectContaining({ system: "answer in one line", model: "claude", dialect: "anthropic" }),
    )
  })

  it("patches the row when the model changes part-way through", async () => {
    // Section 8.5: the transcript keeps the turns that came before, and each
    // answer's route line already records what actually served it.
    mounted()
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())

    await userEvent.click(screen.getByRole("button", { name: /claude/ }))
    await userEvent.clear(screen.getByLabelText("Model or alias"))
    await userEvent.type(screen.getByLabelText("Model or alias"), "gpt")

    await waitFor(() => expect(patchMock).toHaveBeenCalled())
    const [path, body] = patchMock.mock.calls.at(-1) as [string, { model: string }]
    expect(path).toBe("/api/playground/conversations/c1")
    expect(body.model).toBe("gpt")
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run chat-mode 2>&1 | tail -15`
Expected: FAIL — `Failed to resolve import "./chat-mode"`.

- [ ] **Step 3: Write Chat mode**

Create `web/src/features/playground/chat/chat-mode.tsx`:

```tsx
import { useEffect, useRef, useState } from "react"
import { Button, Sheet, SheetContent, SheetTitle, SheetTrigger } from "darkraise-ui"
import { PanelLeftOpen } from "lucide-react"
import { usePlaygroundConversation, usePlaygroundConversations } from "../../../lib/queries"
import {
  configOfConversation,
  messagesOfTurns,
  routesOfTurns,
  titleFromPrompt,
  useAppendTurn,
  useCreateConversation,
  useDeleteConversation,
  useUpdateConversation,
} from "../lib/conversations"
import { useChatRun, type CompletedTurn } from "../lib/use-chat-run"
import { emptyConfig, type PlaygroundConfig } from "../config"
import { parseTools } from "../lib/request"
import { Transcript } from "../transcript"
import { Composer } from "../composer"
import { HistoryRail } from "./history-rail"
import { ConversationHeader } from "./conversation-header"
import type { PlaygroundConversation } from "../../../lib/api-types"

/**
 * A conversation that is still there tomorrow.
 *
 * Three regions: the history rail, the transcript, and the composer pinned to
 * the foot. No config pane and no metrics strip — the two settings a
 * conversation genuinely needs are on the header, and everything else belongs
 * to Lab.
 *
 * The saving is deliberately invisible. A history rail behind an explicit Save
 * button does not get used, and a conversation the operator has to remember to
 * keep is one they will lose. What that costs is stated in spec section 8.2:
 * this is the first place darkrouter retains prompt text at rest.
 */
export function ChatMode({
  onOpenInLab,
}: {
  onOpenInLab: (config: PlaygroundConfig) => void
}) {
  const [config, setConfig] = useState<PlaygroundConfig>(emptyConfig)
  const [activeId, setActiveId] = useState("")
  const [loadedId, setLoadedId] = useState("")
  const [title, setTitle] = useState("New chat")
  const [collapsed, setCollapsed] = useState(false)
  const [railOpen, setRailOpen] = useState(false)

  const { data: conversations } = usePlaygroundConversations()
  const detail = usePlaygroundConversation(activeId, { enabled: activeId !== "" })
  const create = useCreateConversation()
  const append = useAppendTurn()
  const update = useUpdateConversation()
  const remove = useDeleteConversation()

  // The turn callback runs inside an async send, so it reads the conversation
  // and the settings through refs rather than through the closure the send
  // captured. Two messages sent in quick succession would otherwise both see
  // an empty id and create two conversations for one exchange.
  const conversationRef = useRef("")
  const configRef = useRef(config)
  configRef.current = config

  async function persistTurn(turn: CompletedTurn) {
    try {
      let id = conversationRef.current
      if (id === "") {
        const made = await create.mutateAsync({
          title: titleFromPrompt(turn.prompt),
          config: configRef.current,
        })
        id = made.id
        conversationRef.current = id
        setActiveId(id)
        // Marked loaded at creation, so the read below does not fetch the row
        // that was just written and replace the live transcript with it.
        setLoadedId(id)
        setTitle(made.title)
      }
      await append.mutateAsync({ id, role: "user", content: turn.prompt, requestId: "" })
      await append.mutateAsync({
        id, role: "assistant", content: turn.answer, requestId: turn.requestId,
      })
    } catch {
      // useApiMutation has already reported it through the toaster. Losing a
      // saved turn must not take the transcript on screen down with it.
    }
  }

  const run = useChatRun(config, () => {}, (turn) => void persistTurn(turn))

  // Applied once per conversation: re-firing would stomp on turns the operator
  // has typed since it was opened.
  useEffect(() => {
    if (!detail.data || detail.data.id === loadedId) return
    run.load(messagesOfTurns(detail.data.messages), routesOfTurns(detail.data.messages))
    setConfig(configOfConversation(detail.data))
    setTitle(detail.data.title)
    setLoadedId(detail.data.id)
  }, [detail.data, loadedId])

  function startNew() {
    conversationRef.current = ""
    setActiveId("")
    setLoadedId("")
    setTitle("New chat")
    run.load([], {})
    setRailOpen(false)
  }

  function select(id: string) {
    if (id === activeId) return
    conversationRef.current = id
    setActiveId(id)
    setRailOpen(false)
  }

  /** Model, dialect and the system prompt are stored with the conversation, so
   *  changing one part-way through moves the row rather than only the screen.
   *  The turns that came before stay: each answer's route line already records
   *  what actually served it. */
  function changeConfig(next: PlaygroundConfig) {
    setConfig(next)
    if (conversationRef.current !== "") {
      update.mutate({ id: conversationRef.current, title, config: next })
    }
  }

  function retitle(next: string) {
    setTitle(next)
    if (conversationRef.current !== "") {
      update.mutate({ id: conversationRef.current, title: next, config })
    }
  }

  function removeConversation(c: PlaygroundConversation) {
    remove.mutate({ id: c.id, title: c.title })
    if (c.id === conversationRef.current) startNew()
  }

  const rail = (
    <HistoryRail
      conversations={conversations ?? []}
      activeId={activeId}
      onSelect={select}
      onNew={startNew}
      onDelete={removeConversation}
      collapsed={collapsed}
      onToggleCollapsed={() => setCollapsed((c) => !c)}
    />
  )

  return (
    <div className="flex min-h-0 flex-1">
      {/* A 260px rail beside a transcript is two columns on a laptop and a
          squeeze on anything narrower, so below lg it becomes a sheet. */}
      <div className="hidden lg:flex">{rail}</div>

      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <div className="flex items-center gap-2 lg:hidden">
          <Sheet open={railOpen} onOpenChange={setRailOpen}>
            <SheetTrigger asChild>
              <Button variant="ghost" size="icon" aria-label="Show conversations" className="ml-4 mt-2">
                <PanelLeftOpen className="size-[var(--icon-size)]" aria-hidden="true" />
              </Button>
            </SheetTrigger>
            <SheetContent side="left" className="w-[280px] p-0">
              <SheetTitle className="sr-only">Conversations</SheetTitle>
              <HistoryRail
                conversations={conversations ?? []}
                activeId={activeId}
                onSelect={select}
                onNew={startNew}
                onDelete={removeConversation}
                collapsed={false}
                onToggleCollapsed={() => setRailOpen(false)}
              />
            </SheetContent>
          </Sheet>
        </div>

        <ConversationHeader
          config={config}
          onConfigChange={changeConfig}
          title={title}
          onTitleChange={retitle}
          onOpenInLab={() => onOpenInLab(config)}
          onDelete={() => {
            const current = (conversations ?? []).find((c) => c.id === activeId)
            if (current) removeConversation(current)
          }}
          canDelete={activeId !== ""}
        />

        {/* Centred and capped: a transcript run to the full width of a wide
            monitor is a line length nobody reads twice. */}
        <div className="mx-auto flex min-h-0 w-full max-w-3xl flex-1 flex-col">
          <Transcript
            messages={run.messages}
            routes={run.routes}
            busy={run.busy}
            model={config.model}
          />
          <Composer
            model={config.model}
            busy={run.busy}
            error={run.error}
            toolsError={parseTools(config.toolsRaw).error}
            canClear={run.messages.length > 0}
            onSend={(p) => void run.send(p)}
            onStop={run.stop}
            onClear={startNew}
          />
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Wire it into the mode router**

In `web/src/features/playground/playground-screen.tsx`, add the import and replace the `null` branch:

```tsx
import { ChatMode } from "./chat/chat-mode"
```

```tsx
      {mode === "chat" ? (
        <ChatMode
          onOpenInLab={(next) => {
            setLabConfig(next)
            choose("lab")
          }}
        />
      ) : (
        <LabMode config={labConfig} onConfigChange={setLabConfig} />
      )}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd web && npx vitest run chat-mode 2>&1 | tail -15`
Expected: PASS, five tests.

The composer's textarea carries `aria-label="Message"` and its send control is a button labelled `Send`, which is what the helper above queries. The composer is unchanged by this stage; if a query misses, fix the **test**, never the component.

- [ ] **Step 6: Run the frontend gate**

Run: `cd web && npm test 2>&1 | tail -8 && npm run typecheck 2>&1 | tail -5`
Expected: 64 files, 619 tests, all passing; typecheck silent.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/playground/chat/chat-mode.tsx web/src/features/playground/chat/chat-mode.test.tsx web/src/features/playground/playground-screen.tsx
git commit -m "feat(playground): add Chat mode with auto-save"
```

---

### Task 11: The quiet route line

**Files:**
- Modify: `web/src/features/playground/message.tsx:151-186` (`RouteLine`) and `:72-84` (`AssistantTurn`)
- Modify: `web/src/features/playground/message.test.tsx` (append)
- Modify: `web/src/features/playground/transcript.tsx` (a `quiet` prop, passed through)
- Modify: `web/src/features/playground/chat/chat-mode.tsx` (pass `quiet` to `<Transcript />`)

**Interfaces:**
- Consumes: Task 10's `<ChatMode />`.
- Produces: an optional `quiet?: boolean` on `AssistantTurn` and on `Transcript`. Default `false`, so Lab is unchanged without saying so.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: §6 states the two states exactly — the mark plus the duration, expanding on click to the full line

- [ ] **Step 1: Write the failing test**

Append to `web/src/features/playground/message.test.tsx`:

```tsx
describe("the route line in Chat mode", () => {
  const route = {
    requestId: "01TRACE",
    provider: "groq",
    model: "llama",
    totalMs: 1240,
    tokensIn: 12,
    tokensOut: 40,
    costMicros: 1500,
    failedOver: [],
    warnings: [],
  }

  it("quiets to the duration, and expands to the whole line on click", async () => {
    // A twenty-minute conversation does not want cost and token counts under
    // every turn. It does want them under the one turn being questioned,
    // which is what makes this a disclosure rather than a removal.
    render(<AssistantTurn text="an answer" route={route} quiet />)
    expect(screen.getByText("1.2s")).toBeInTheDocument()
    expect(screen.queryByText(/12 in/)).not.toBeInTheDocument()
    expect(screen.queryByRole("link", { name: "trace" })).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole("button", { name: /routing detail/i }))
    expect(screen.getByText(/12 in · 40 out/)).toBeInTheDocument()
    expect(screen.getByText("groq")).toBeInTheDocument()
  })

  it("stays expanded in Lab, where measurement is the point", () => {
    render(<AssistantTurn text="an answer" route={route} />)
    expect(screen.getByText(/12 in · 40 out/)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /routing detail/i })).not.toBeInTheDocument()
  })
})
```

If `message.test.tsx` does not already import `userEvent`, add `import userEvent from "@testing-library/user-event"`. Wrap the render in whatever router provider the existing tests in that file use for the trace `<Link>`; copy it verbatim from the neighbouring describe rather than inventing one.

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run message 2>&1 | tail -15`
Expected: FAIL — `quiet` is not a prop, so the full line renders and `queryByText(/12 in/)` finds it.

- [ ] **Step 3: Extract the duration format and add the quiet state**

In `web/src/features/playground/message.tsx`, add above `RouteLine`:

```tsx
/** The reading a quiet line keeps, and the first thing the full line says. */
function shortDuration(ms: number): string {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${ms}ms`
}
```

Replace the first `parts.push` in `RouteLine` with `parts.push(shortDuration(route.totalMs))`, and give the function the collapsed state:

```tsx
/**
 * The route, under the answer it explains.
 *
 * One line, in the quiet colour, holding what the operator came for: who
 * answered, how long it took, what it spent. A failover is the exception and
 * is drawn as one — it is the most interesting thing that can happen to a
 * request, and a number in a row would bury it.
 *
 * Quiet is Chat mode's reading of the same line. Twenty turns each carrying
 * cost, tokens and a trace link is an instrument panel under a conversation;
 * the duration alone still says the answer was routed, and one click gets the
 * rest back. Lab never quiets, because measurement is what Lab is for.
 */
function RouteLine({ route, quiet = false }: { route: TurnRoute; quiet?: boolean }) {
  const [expanded, setExpanded] = useState(false)

  if (quiet && !expanded) {
    return (
      <button
        type="button"
        aria-label="Show routing detail"
        onClick={() => setExpanded(true)}
        className="w-fit text-sm text-[hsl(var(--legend))] underline-offset-2 hover:underline"
      >
        {route.totalMs === null ? "routed" : shortDuration(route.totalMs)}
      </button>
    )
  }

  const parts: string[] = []
  // …the existing body, unchanged from here down.
```

`message.tsx` already imports `useState` for `CopyTurn`, so no import changes.

- [ ] **Step 4: Pass `quiet` down**

In `AssistantTurn`, add the prop and forward it:

```tsx
export function AssistantTurn({
  text,
  route,
  streaming = false,
  quiet = false,
}: {
  text: string
  route?: TurnRoute
  streaming?: boolean
  /** Chat mode's reading: the duration only, until the operator asks. */
  quiet?: boolean
}) {
```

and change the render site to `{route ? <RouteLine route={route} quiet={quiet} /> : null}`.

In `web/src/features/playground/transcript.tsx`, add `quiet` to the props type and pass it to every `<AssistantTurn />`:

```tsx
  quiet = false,
}: {
  messages: PlaygroundMessage[]
  routes: Record<number, TurnRoute>
  busy: boolean
  model: string
  seedNote?: string
  quiet?: boolean
}) {
```

In `web/src/features/playground/chat/chat-mode.tsx`, add `quiet` to the `<Transcript>` call.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web && npx vitest run message transcript chat-mode 2>&1 | tail -12`
Expected: PASS. Every assertion already in `message.test.tsx` and `transcript.test.tsx` passes untouched — `quiet` defaults to `false`.

- [ ] **Step 6: Run the frontend gate**

Run: `cd web && npm test 2>&1 | tail -8 && npm run typecheck 2>&1 | tail -5`
Expected: 64 files, 621 tests, all passing; typecheck silent.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/playground/message.tsx web/src/features/playground/message.test.tsx web/src/features/playground/transcript.tsx web/src/features/playground/chat/chat-mode.tsx
git commit -m "feat(playground): quiet the route line in Chat"
```

---

### Task 12: The settings row, and the purge

**Files:**
- Modify: `web/src/lib/api-types.ts:407-425` (`ConfigBlocks`)
- Modify: `web/src/features/settings/settings-catalog.ts` (the `logging` group blurb, the `SETTINGS` entry, `groupForPrefix`)
- Modify: `web/src/features/settings/settings-catalog.test.ts` (append)
- Modify: `web/src/features/settings/settings-screen.tsx:368-401` (a third `PageHeader` action)
- Modify: `web/src/features/settings/settings-screen.test.tsx` (append)

**Interfaces:**
- Consumes: Task 2's `blocks.playground.save_conversations` and the `playground.save_conversations` field entry; Task 6's `usePurgeConversations`.
- Produces: nothing other tasks depend on.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 3 = 6
**Approach:** inline - skip 3: decision 19 in §14 settles the shape, and the settings screen's `ConfirmButton` actions are the existing pattern

- [ ] **Step 1: Write the failing tests**

Append to `web/src/features/settings/settings-catalog.test.ts`:

```ts
describe("the two settings that govern prompt text at rest", () => {
  it("shows them under one heading, so the distinction is offered rather than inferred", () => {
    const withPlayground = cfg({
      blocks: {
        ...cfg().blocks,
        playground: { save_conversations: true },
      },
      fields: {
        ...cfg().fields,
        "playground.save_conversations": { source: "default", hot_reloadable: true },
      },
    } as Partial<ConfigResponse>)

    const logging = settingGroups(withPlayground).find((g) => g.group.id === "logging")
    const fields = logging?.rows.map((r) => r.field) ?? []
    expect(fields).toContain("capture.bodies")
    expect(fields).toContain("playground.save_conversations")
    // An operator who turned body capture off for privacy would reasonably
    // expect that stance to cover the playground. It does not, and the
    // heading has to say so rather than leave it to be discovered.
    expect(logging?.group.blurb).toMatch(/your own/i)
  })

  it("reads the switch as On and Off rather than as a raw boolean", () => {
    const withPlayground = cfg({
      blocks: { ...cfg().blocks, playground: { save_conversations: false } },
    } as Partial<ConfigResponse>)
    expect(
      settingRow(
        withPlayground,
        "playground.save_conversations",
        SETTINGS["playground.save_conversations"]!,
      ).display,
    ).toBe("Off")
  })
})
```

Append to `web/src/features/settings/settings-screen.test.tsx`, following that file's existing mounting helper and mocks:

```tsx
it("offers the purge behind a confirmation that says what it destroys", async () => {
  // The purge is a separate action from the key on purpose: config is
  // file-backed and reloadable, and a setting whose reload deleted data would
  // mean an edit to a file on disk silently destroying the operator's history.
  mounted(<SettingsScreen />)
  await userEvent.click(
    await screen.findByRole("button", { name: /delete saved conversations/i }),
  )
  expect(screen.getByText(/cannot be undone/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole("button", { name: "Delete" }))
  await waitFor(() =>
    expect(delMock).toHaveBeenCalledWith("/api/playground/conversations"),
  )
})
```

Read the top of `settings-screen.test.tsx` first and reuse its existing `api` mock and mounting helper verbatim; if it does not already mock `api.del`, extend the existing mock object rather than adding a second `vi.mock` for the same module.

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run settings 2>&1 | tail -15`
Expected: FAIL — no `playground.save_conversations` row, and no purge button.

- [ ] **Step 3: Add the block to the wire type**

In `web/src/lib/api-types.ts`, inside `ConfigBlocks`, after the `catalog` entry:

```ts
  playground: { save_conversations: boolean }
```

- [ ] **Step 4: Name the setting, and say how it differs from the one beside it**

In `web/src/features/settings/settings-catalog.ts`, replace the `logging` entry in `GROUPS`:

```ts
  {
    id: "logging",
    title: "Logging and capture",
    blurb:
      "What is recorded about each request, and how long it is kept. Two of these keep prompt text on disk and they are not the same thing: body capture records other people's traffic passing through the gateway, and saved conversations record your own typing in the playground.",
  },
```

Add to `SETTINGS`, immediately after `"capture.retention"`:

```ts
  "playground.save_conversations": {
    name: "Save playground conversations",
    description:
      "Keeps Chat mode's conversations so you can return to one tomorrow. Off stops new ones being written; it does not delete what is already there — the button above the table does that.",
    group: "logging",
  },
```

and extend `groupForPrefix`:

```ts
  if (field.startsWith("log") || field.startsWith("capture") || field.startsWith("playground")) {
    return "logging"
  }
```

- [ ] **Step 5: Add the purge action**

In `web/src/features/settings/settings-screen.tsx`, add the import:

```tsx
import { usePurgeConversations } from "../playground/lib/conversations"
```

the mutation beside `sync`:

```tsx
  const purgeConversations = usePurgeConversations()
```

and a third `ConfirmButton` in the `PageHeader` actions, before `Sync catalog now`:

```tsx
            <ConfirmButton
              size="sm"
              variant="outline"
              destructive
              disabled={purgeConversations.isPending}
              title="Delete every saved conversation?"
              description="Every conversation the playground has kept, and every message in them, is removed. This cannot be undone. Turning the setting off stops new ones being saved; this is what removes the ones already there."
              confirmLabel="Delete"
              onConfirm={() => purgeConversations.mutate()}
            >
              Delete saved conversations
            </ConfirmButton>
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd web && npx vitest run settings 2>&1 | tail -12`
Expected: PASS.

- [ ] **Step 7: Run the frontend gate**

Run: `cd web && npm test 2>&1 | tail -8 && npm run typecheck 2>&1 | tail -5`
Expected: 64 files, 624 tests, all passing; typecheck silent.

- [ ] **Step 8: Commit**

```bash
git add web/src/lib/api-types.ts web/src/features/settings/settings-catalog.ts web/src/features/settings/settings-catalog.test.ts web/src/features/settings/settings-screen.tsx web/src/features/settings/settings-screen.test.tsx
git commit -m "feat(settings): show the save key and offer the purge"
```

---

### Task 13: Compare aborts a column it no longer owns

**Files:**
- Modify: `web/src/features/playground/compare.tsx`
- Create: `web/src/features/playground/compare-abort.test.tsx`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks depend on. `runColumn` gains a `signal: AbortSignal` parameter; it is module-private.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 2 = 3
**Approach:** inline - skip 1: the cause is located — `runColumn` never passes a signal to `stream`, so nothing can cancel it

A defect stage 3's review found and did not fix: removing a streaming column leaves its request in flight, and `busy` — which is `columns.some(c => c.status === "streaming")` — drops to false while the orphan is still arriving, so **Run** re-enables mid-stream. The rerun path has the same shape and no test at all.

- [ ] **Step 1: Write the failing tests**

Create `web/src/features/playground/compare-abort.test.tsx`. It needs a model combobox that actually sets a value, so it carries its own mocks rather than sharing `compare.test.tsx`'s read-only one:

```tsx
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { Compare } from "./compare"
import { emptyConfig } from "./config"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

vi.mock("../shell/model-combobox", () => ({
  ModelCombobox: ({ label, value, onChange }: {
    label: string
    value: string
    onChange: (next: string) => void
  }) => <input aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />,
  useModelCandidates: () => ({ candidates: [], loading: false }),
}))

const { streamMock, signals } = vi.hoisted(() => ({
  streamMock: vi.fn(),
  signals: [] as AbortSignal[],
}))

vi.mock("../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/api")>()),
  stream: streamMock,
}))

/** A stream that starts and then never finishes until its signal aborts, which
 *  is the state a removed or rerun column is actually in. */
function hangs() {
  streamMock.mockImplementation(async function* (
    _path: string,
    _body: unknown,
    onStart?: (s: { requestId: string }) => void,
    signal?: AbortSignal,
  ) {
    if (signal) signals.push(signal)
    onStart?.({ requestId: "01A" })
    yield `data: ${JSON.stringify({ choices: [{ delta: { content: "…" } }] })}\n\n`
    await new Promise<void>((_resolve, reject) => {
      signal?.addEventListener("abort", () => reject(Object.assign(new Error("aborted"), { name: "AbortError" })))
    })
  })
}

async function startARun() {
  render(<Compare config={{ ...emptyConfig(), model: "gpt" }} />)
  for (const box of screen.getAllByRole("textbox", { name: /model/i })) {
    await userEvent.type(box, "gpt")
  }
  await userEvent.type(screen.getByPlaceholderText("Prompt"), "compare these")
  await userEvent.click(screen.getByRole("button", { name: "Run" }))
  await waitFor(() => expect(signals).toHaveLength(2))
}

describe("a Compare column that stops being watched", () => {
  beforeEach(() => {
    streamMock.mockReset()
    signals.length = 0
    hangs()
  })

  it("aborts its request when a third column is removed", async () => {
    // Left running, the orphan keeps costing tokens for output nothing renders,
    // and busy — which counts streaming columns — drops while it arrives, so
    // Run re-enables mid-stream.
    await startARun()
    await userEvent.click(screen.getByRole("button", { name: "Add a column" }))
    const third = screen.getAllByRole("textbox", { name: /model/i })[2]!
    await userEvent.type(third, "gpt")
    await userEvent.click(screen.getByRole("button", { name: "Run" }))
    await waitFor(() => expect(signals).toHaveLength(5))

    const removes = screen.getAllByRole("button", { name: /remove/i })
    await userEvent.click(removes[removes.length - 1]!)
    await waitFor(() => expect(signals[4]!.aborted).toBe(true))
    expect(signals[3]!.aborted).toBe(false)
  })

  it("abandons the previous run when Run is pressed again", async () => {
    await startARun()
    await userEvent.click(screen.getByRole("button", { name: "Run" }))
    await waitFor(() => expect(signals).toHaveLength(4))
    expect(signals[0]!.aborted).toBe(true)
    expect(signals[1]!.aborted).toBe(true)
    expect(signals[2]!.aborted).toBe(false)
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run compare-abort 2>&1 | tail -15`
Expected: FAIL — `signals` stays empty, because `runColumn` passes no signal to `stream`.

- [ ] **Step 3: Give `runColumn` a signal, and fold the latency into its two endings**

In `web/src/features/playground/compare.tsx`, replace `runColumn`:

```ts
async function runColumn(
  model: string,
  prompt: string,
  config: PlaygroundConfig,
  signal: AbortSignal,
  update: (fn: (c: Column) => Column) => void,
): Promise<void> {
  const started = performance.now()
  update((c) => ({ ...emptyColumn(c.id), model: c.model, status: "streaming" }))
  let buffer = ""
  try {
    const turns: PlaygroundMessage[] = [{ role: "user", content: prompt }]
    for await (const chunk of stream(
      "/api/playground",
      // The shared settings, with only the model differing between columns:
      // comparing models under two system prompts would answer a question
      // nobody asked.
      chatBody({ ...config, model, stream: true, messages: turns }),
      (s: StreamStart) => update((c) => ({ ...c, requestId: s.requestId })),
      signal,
    )) {
      buffer += chunk
      const { text, rest } = drainSSE(buffer, config.dialect)
      buffer = rest
      if (text) update((c) => ({ ...c, text: c.text + text }))
    }
    update((c) => ({ ...c, status: "done", latencyMs: performance.now() - started }))
  } catch (err) {
    // An abort is the column being removed or the run being replaced, not a
    // provider failing. It leaves nothing behind: the column is either gone
    // or about to be reset by the run that replaced this one.
    if ((err as Error).name === "AbortError") return
    update((c) => ({
      ...c,
      error: (err as Error).message,
      status: "error",
      latencyMs: performance.now() - started,
    }))
  }
}
```

- [ ] **Step 4: Own one controller per column**

In `Compare`, add beside `counter`:

```tsx
  // One per column, so removing a column can stop the request it started.
  // Without this the orphan keeps arriving into state nothing renders, and
  // `busy` — which counts streaming columns — drops while it is still coming.
  const controllers = useRef(new Map<string, AbortController>())

  function abortColumn(id: string) {
    controllers.current.get(id)?.abort()
    controllers.current.delete(id)
  }
```

replace `run`:

```tsx
  function run() {
    if (!canRun) return
    // A rerun replaces the run before it. Left alone, the previous streams
    // append into columns this run has just reset, and the latencies beside
    // them measure two runs at once.
    for (const id of [...controllers.current.keys()]) abortColumn(id)
    // Started in one pass so they overlap: run sequentially and the latency
    // readings beside them would measure the queue, not the providers.
    for (const column of columns) {
      const controller = new AbortController()
      controllers.current.set(column.id, controller)
      void runColumn(column.model, prompt, config, controller.signal, (fn) =>
        updateColumn(column.id, fn),
      ).finally(() => {
        // Only if it is still this column's controller: a rerun has already
        // replaced the entry, and deleting it would leave the new run
        // unstoppable.
        if (controllers.current.get(column.id) === controller) {
          controllers.current.delete(column.id)
        }
      })
    }
  }
```

and abort on removal:

```tsx
              onRemove={() => {
                abortColumn(column.id)
                setColumns((cs) => cs.filter((c) => c.id !== column.id))
              }}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web && npx vitest run compare 2>&1 | tail -12`
Expected: PASS — both files, six tests. The four assertions already in `compare.test.tsx` are untouched.

- [ ] **Step 6: Run the frontend gate**

Run: `cd web && npm test 2>&1 | tail -8 && npm run typecheck 2>&1 | tail -5`
Expected: 65 files, 626 tests, all passing; typecheck silent.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/playground/compare.tsx web/src/features/playground/compare-abort.test.tsx
git commit -m "fix(playground): abort a compare column's request"
```

---

### Task 14: Renumber the mockups, prune fragment 12's CSS, and write the font-size rule down

**Files:**
- Rename: `docs/ux/mockups/fragments/{13-connect,14-settings,15-login,16-first-run,17-light-proof}.html` → `{14,15,16,17,18}-…`
- Rename: `docs/ux/mockups/css/{13-connect,14-settings,15-login,16-first-run,17-light-proof}.css` → `{14,15,16,17,18}-…`
- Modify: the `id="s-NN-…"` attribute inside each renamed fragment
- Modify: `docs/ux/mockups/css/12-playground-compare.css` (prune to what fragment 12 still uses; 13px → 14px)
- Modify: `CLAUDE.md` (the mockup exception to the typography rule)
- Regenerate: `docs/ux/mockups/index.html` and `artifact.html` via `build.py`

**Interfaces:**
- Consumes: nothing.
- Produces: `13` free for Task 15's new fragment.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 2 - spec 0 - coupling 0 - risk 1 = 3
**Approach:** inline - skip 3: §11 fixes the renumber, and follow-up 5's answer was settled by two independent reviews

`build.py` numbers the table of contents from `enumerate` over the sorted fragment filenames, so the ordering follows the filenames; the `s-NN-…` ids inside the sections do not, and have to be edited to match or the anchors read one number and the contents another. `css/` files are concatenated in sorted order and their rules are class-scoped, so renaming them changes nothing but keeps the pairing legible.

- [ ] **Step 1: Rename, highest first**

Highest first, so no rename lands on a name that is still taken:

```bash
cd docs/ux/mockups
for n in 17:18 16:17 15:16 14:15 13:14; do
  from=${n%:*}; to=${n#*:}
  f=$(ls fragments/${from}-*.html); stem=$(basename "$f" .html); name=${stem#*-}
  git mv "fragments/${from}-${name}.html" "fragments/${to}-${name}.html"
  git mv "css/${from}-${name}.css" "css/${to}-${name}.css"
  sed -i "s/id=\"s-${from}-${name}\"/id=\"s-${to}-${name}\"/" "fragments/${to}-${name}.html"
done
cd -
```

- [ ] **Step 2: Check nothing still refers to the old numbers**

Run: `rg -n 's-1[3-7]-(connect|settings|login|first-run|light-proof)' docs/ CLAUDE.md`
Expected: matches only inside `docs/ux/mockups/index.html` and `artifact.html`, which are build artifacts and are regenerated in step 5. If anything else matches — a cross-reference in a spec or in `docs/ux/DONE-CRITERIA.md` — update it to the new number.

- [ ] **Step 3: Prune fragment 12's stylesheet**

`fragments/12-playground-compare.html` uses exactly two classes from this file after stage 3's redraw: `.pgc-out` and `.pgc-caret`. Everything else is orphaned. Replace the whole of `docs/ux/mockups/css/12-playground-compare.css` with:

```css
/* --- 12 use/playground — compare ---------------------------------------- */

/* Fixed height on purpose: the columns must occupy the identical box whatever
   arrives in them, or the comparison becomes a reading-order exercise. */
.pgc-out {
  height: 160px;
  padding: 10px;
  overflow: hidden;
  font-family: 'IBM Plex Mono', ui-monospace, monospace;
  font-size: 14px;
  line-height: 1.5;
  color: var(--ink);
}
.pgc-out p { margin: 0 0 10px; }
.pgc-out p:last-child { margin-bottom: 0; }

/* The stream cursor. Static: this product has exactly four movements and a
   blink would be a fifth. --accent-ink because an open socket is a UI fact,
   not a provider's health and not a routing verdict. */
.pgc-caret {
  display: inline-block;
  width: 6px;
  height: 14px;
  margin-left: 2px;
  transform: translateY(2px);
  background: var(--accent-ink);
}
```

Confirm nothing else was in use first:

```bash
cd docs/ux/mockups && rg -o 'pgc-[a-z-]+' fragments/12-playground-compare.html | sort -u
```

Expected: exactly `pgc-caret` and `pgc-out`. If anything else appears, keep that rule too rather than deleting a class the fragment still needs.

- [ ] **Step 4: Write the exception into `CLAUDE.md`**

Append to the "Typography: never hardcode a font size" section of `CLAUDE.md`, after the paragraph about stylesheets:

```markdown
**The one exception: `docs/ux/mockups/`.** The mockup set is a standalone HTML
document with its own stylesheet. It never loads darkraise-ui and never
participates in the font-size axis, so a pixel size there cannot silently opt
out of a setting an operator changed — which is the entire reason for the rule
above. Pixel sizes in `docs/ux/mockups/css/` are therefore allowed, and
`qa.py`'s 30px ceiling is the only limit that applies to them. This was
deferred twice and reviewed twice; it is written here so it stops being
re-litigated. Everything under `web/` is bound by the rule without exception.
```

- [ ] **Step 5: Rebuild and run the mockup gate**

Run: `cd docs/ux/mockups && python3 qa.py && python3 build.py`
Expected: `qa.py` reports no problems; `build.py` writes `index.html` and `artifact.html`. Confirm the table of contents now ends at `17` for `contrast proof` — the toc is zero-indexed by position, so eighteen screens number `00` to `17`, and the *filenames* run `00` to `18` only once Task 15 adds `13`.

- [ ] **Step 6: Commit**

Two commits: the renumber and prune are one concern, the rule is another.

```bash
git add docs/ux/mockups
git commit -m "refactor(mockups): renumber for a chat fragment"
git add CLAUDE.md
git commit -m "docs: exempt the mockups from the size floor"
```

---

### Task 15: Draw `13-playground-chat.html`

**Files:**
- Create: `docs/ux/mockups/fragments/13-playground-chat.html`
- Create: `docs/ux/mockups/css/13-playground-chat.css`
- Regenerate: `docs/ux/mockups/index.html` and `artifact.html`

**Interfaces:**
- Consumes: Task 14's freed number.
- Produces: nothing other tasks depend on. Task 16 verifies the built screen against the running console.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 1 = 4
**Approach:** inline - skip 2: `11-playground.html` and `12-playground-compare.html` are the two neighbours this fragment sits between, and §4.1 names every region in it

**Read first, and copy their structure rather than inventing one:** `docs/ux/mockups/fragments/11-playground.html` for the shell (`.screen`, `.rail`, `.topbar`, `.chrome`, `.chrome-main`) and the legend paragraph convention, and `12-playground-compare.html` for how a mid-stream transcript is drawn. `qa.py` enforces: a `data-screen-title` attribute, no colour literal anywhere in a fragment (every colour is a `var(--…)` token), nothing fetched from the network, and a font-size ceiling of 30px.

**What the fragment shows** — §11 states it exactly: Chat mode mid-stream, the history rail populated, one answer with a quiet route line and one expanded.

- [ ] **Step 1: Write the fragment**

Create `docs/ux/mockups/fragments/13-playground-chat.html` with `<section class="screen" id="s-13-playground-chat" data-screen-title="use/playground &mdash; chat">`, the standard rail and topbar copied from `11-playground.html`, and inside `.chrome-main` three regions left to right:

1. **The history rail**, 260px, with a header reading `Conversations`, four rows, and a `New` action at the foot. Each row carries a title, one line of the last user turn in `--legend`, and a relative timestamp. The second row is the active one and takes the muted background.
2. **The transcript column.** A slim header strip with the model pill (`gpt-oss-120b`, in mono, as a bordered button), the dialect beside it, an inline-editable title, and an overflow glyph on the right. Beneath it, a transcript capped at `max-width: 768px` and centred, holding four turns: a user turn, an assistant turn whose route line is **quiet** (the provider mark in the gutter plus `1.2s`), a second user turn, and a fourth turn **mid-stream** — partial text with the `.pgc-caret` cursor. Draw one further turn above them with its route line **expanded**, showing `groq/llama-3.3-70b-versatile · 840ms · 12 in · 240 out · $0.0015 · trace`.
3. **The composer**, pinned to the foot of the column, full width of the transcript's cap, with the Send control on its right.

No config pane and no metrics strip — that is the point of the mode, and drawing either would make the mockup describe a screen this stage does not build.

Lead the section with a `<p class="legend">` paragraph, as every other fragment does, saying what the screen is for: a conversation the operator can come back to, the rail that retrieves it, and the route line that stays available under every answer without shouting.

- [ ] **Step 2: Write the stylesheet**

Create `docs/ux/mockups/css/13-playground-chat.css`. Prefix every class `pgt-` so it cannot collide with `pg-` (fragment 11) or `pgc-` (fragment 12), and reuse `.pgc-caret` for the stream cursor rather than defining a second one. Colour comes only from tokens — `--ink`, `--legend`, `--accent-ink`, `--rule`, `--surface` — never a literal.

- [ ] **Step 3: Run the mockup gate**

Run: `cd docs/ux/mockups && python3 qa.py && python3 build.py`
Expected: `qa.py` silent, `build.py` writing both files, and the table of contents carrying nineteen entries `00` to `18` with `13 use/playground — chat` between compare and connect.

- [ ] **Step 4: Look at it**

Run: `cd docs/ux/mockups && python3 check.py 2>&1 | tail -20`
Expected: a screenshot per screen in both themes, including the new one, with no failure reported. Open the produced screenshot for screen 13 in both themes and confirm the three regions read as described and nothing overflows its column.

- [ ] **Step 5: Commit**

```bash
git add docs/ux/mockups
git commit -m "docs(ux): draw the chat mode fragment"
```

---

### Task 16: Verify live, then deploy

**Files:** none, until the record commit.

**Interfaces:** none.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 0 - spec 1 - coupling 0 - risk 2 = 3
**Approach:** inline - skip 3: the spec's stage 4 gate and this repository's CLAUDE.md fix the whole procedure

- [ ] **Step 1: Full gate**

```bash
cd web && npm test && npm run typecheck && npm run build
export PATH=$PATH:/usr/local/go/bin
go build ./... && go vet ./... && go test -count=1 ./internal/...
```

- [ ] **Step 2: Build and deploy with the UAT overlay**

The overlay is required — `compose.prod.yml` alone sets `pull_policy: always` and would discard the local build.

```bash
docker build -t darkraise/darkrouter:latest .
docker compose -f compose.prod.yml -f compose.uat.yml up -d darkrouter
docker ps --filter name=darkrouter --format '{{.Names}}\t{{.Status}}'
curl -s http://localhost:8091/healthz
```

Use port **8091**; 8080 and 8081 belong to other containers on this machine.

- [ ] **Step 3: Confirm migration 0015 applied and the served bundle is this build**

```bash
asset=$(curl -s http://localhost:8091/ | grep -o 'assets/index-[A-Za-z0-9_-]*\.js')
curl -s "http://localhost:8091/$asset" > /tmp/served.js
cmp /tmp/served.js internal/admin/dist/assets/index-*.js && echo "deploy matches source"
docker exec "$(docker ps -q --filter name=darkrouter)" \
  sh -c "ls /data" 2>/dev/null || true
```

Compare bytes, not filenames: Vite's hash differs between the image's build path and the repo path. Then confirm both tables exist in the running database — the container came up healthy is not the same claim.

- [ ] **Step 4: Look at it**

Password from `.uat-credentials`; never copy it into a tracked file. At **1600×1000** and **1280×800**, **light and dark**, sidebar collapsed and expanded:

| Check | What passing looks like |
|---|---|
| Mode switch | The toggle sits in the `PageHeader`, right-aligned. Choosing Chat, reloading, and landing back in Chat. `?mode=lab` on the URL overriding a stored Chat |
| D6 | Send a turn with a system prompt set from the overflow menu. Reload the page, pick the conversation from the rail: the transcript returns and the system prompt is still in the dialog |
| D7 | After one exchange the rail shows one conversation, and the conversation holds exactly two messages. Send a second exchange: still one conversation, now four messages. Two conversations for one exchange is the failure to watch for |
| Rail | Titles derived from the first turn, not a column of "New chat". The preview line truncating rather than wrapping. Collapse to nothing and back |
| Quiet route line | Chat's answers showing the mark and the duration only, expanding on click to provider, tokens, cost and the trace link. Lab's Single tab still always expanded |
| Open in Lab | The overflow menu's *Open in Lab* switching modes with the model, dialect and system prompt already in the config pane |
| Lab unchanged | The four tabs, the config pane and the preset picker exactly as stage 3 left them, with the first tab now named Single and the metrics strip present from the start showing em dashes |
| Purge | Settings offering *Delete saved conversations* with a confirmation naming what it destroys. After confirming, the rail is empty and stays empty across a reload |
| Save key off | With `playground:\n  save_conversations: false` in `darkrouter.yaml` and a reload, the settings row reads Off, a new turn does not appear in the rail, and the existing conversations are still readable |
| Narrow width | At 1280×800 the rail still fits beside the transcript; below `lg` it becomes a sheet and the transcript takes the width |
| D9 | Start a Compare run with three columns, remove one mid-stream: the removed column's request stops, and Run stays disabled until the other two finish |

Any failure is a defect in the task that introduced it, not a new task: fix it there, re-run the suite, redeploy.

**One judgement is yours to make with the screenshots in front of you.** §4.1 puts the transcript at `max-w-3xl` centred in its column. At 1600×1000 with the rail open, that leaves a wide empty margin on the right where Lab has its config pane. Decide whether the transcript should stay capped and centred within the whole column, or sit left-aligned against the rail. Record what you decided and why; deciding to leave it is a result, leaving it undecided is not.

- [ ] **Step 5: Record the gate**

Append a "Stage 4 result" section to this plan recording what each criterion showed, then:

```bash
git add docs/superpowers/plans/2026-08-30-playground-stage4-chat-mode.md
git commit -m "docs(playground): record the stage 4 gate"
```

---

## Notes for whoever picks this up

**§8.2 is the decision this stage carries, and it was re-reviewed rather than assumed.** The playground now retains prompt text at rest, in plaintext, in the same database that encrypts provider credentials. The asymmetry is deliberate and argued in §8.2: a credential is a live capability an attacker can turn against a third party, a test prompt is content. The `localStorage` fallback was reconsidered on 2026-08-30 and rejected on a ground the spec had not stated — it stores prompt text at rest too, on the browser's disk, where no settings key governs it, no purge reaches it and `sqlite3` cannot inspect it. A reviewer who wants to reopen this should reopen it as *whether the history rail exists*, not *where the text is kept*.

**The hook does not know about storage, and that is load-bearing.** `useChatRun` is shared by Chat mode and Lab's Single tab, and §8.2 says Lab's four tabs persist nothing. Persistence therefore lives at the call site, behind an optional `onTurn` callback. A future change that moves a write inside the hook silently gives Lab a storage decision nobody made.

**The conversation is created with its first turn, not before it.** §8.5 describes a "New chat" created up front and retitled once the first turn completes, plus a reap for conversations abandoned before they were used. Creating on the first turn is the same behaviour with one fewer write and no window in which an empty row exists: the title is derived at creation because the first user turn is already in hand. The reap is kept as a backstop for a client that died between the create and the first message, with an age floor so a conversation another tab created seconds ago is never destroyed.

**The save key is read-only in the console, and the purge is a separate button.** Not an oversight: `EDITABLE` in `settings-catalog.ts` carries only `policy.*`, because every other key comes from `darkrouter.yaml` and has no write endpoint. And §8.2 refuses to let a config *reload* delete data, which a single combined switch would amount to. This is decision 19 in §14.

**`playgroundConversations`, never a bare `conversations`, in client code.** `presets` already means the provider catalogue and `playgroundPresets` means stage 3's saved requests. A third bare name in the same query cache would be the third chance to make the same mistake.

**Two follow-ups from stage 3's reviews are deliberately not in this plan.** The two Go handlers that answer 500 where a 409-with-id would be better — PATCH renaming a preset onto a taken name, and the create TOCTOU race — stay open. Neither path is reachable from the UI: §8.5 ships no rename affordance, and the race needs two concurrent saves of the same name from one operator. They are real and they are not observable.

**The listing endpoint deletes rows, and it will bite the next test author.** `GET /api/playground/conversations` runs `ReapEmptyPlaygroundConversations` before it lists, with a one-hour floor keyed on `created_at`. §8.5 asks for exactly that — an abandoned conversation goes when the rail next loads — but it makes a read-shaped endpoint destructive, and it caught two test authors inside one stage: a test that backdates `created_at` to control ordering has its own fixture deleted by the call under test. Backdate `updated_at` alone; `created_at` is the reaper's column, not the ordering's.

**Master is well ahead of `origin/master`.** Pushing is a decision for the human, not for whoever executes this plan.
