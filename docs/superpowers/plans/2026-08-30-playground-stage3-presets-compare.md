# Playground Stage 3 — The Instrument Remembers, and Compares

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Give the playground named request presets that persist server-side and survive a dialect round trip without losing a value, and grow Compare from two fixed panels to up to four columns.

**Architecture:** One new table, `playground_presets`, holds a name, model, dialect and an opaque JSON `config` blob. The server stores that blob exactly as it received it; the client reconstitutes it by merging over `emptyConfig()` and keeping only keys whose `typeof` matches the default's. Compare becomes an array of columns that each carry a model and share one `chatBody`, so a difference between transcripts is the models rather than a second request shape.

**Tech Stack:** Go 1.26 stdlib, `modernc.org/sqlite`; React 19, TanStack Router + Query, darkraise-ui 6.5.0 (`Dialog`, `Select`, `Card`), Tailwind 4, vitest 4 + @testing-library/react.

**Spec:** `docs/superpowers/specs/2026-08-29-playground-overhaul-design.md` — §3 (stage 3), §8.1 (presets, and the four decisions settled at planning), §8.3 (schema), §8.4 (endpoints), §8.5 (behaviour the endpoints do not settle), §9 (Compare as N columns).

**Stages 1 and 2 are merged to `master`.** The playground fills its frame, `PlaygroundConfig` carries thirteen fields, and `dialect-support.ts` is a total table the compiler will not let a new control skip. This plan builds on both.

## Global Constraints

These apply to every task without restating.

- **TDD.** A failing test precedes the implementation it tests. Run it and see it fail before writing the code.
- **Gates before any commit.** Frontend tasks: `cd web && npm test && npm run typecheck` clean. Go tasks: `go build ./... && go vet ./...` clean and `go test -count=1 ./internal/...` clean. `go` is not on the default PATH; run `export PATH=$PATH:/usr/local/go/bin` first.
- **Never `text-xs`, never a custom size.** 14px (`text-sm`) is the floor; only `text-sm`, `text-base`, `text-lg`, `text-xl`, `text-2xl`, `text-3xl`. In a stylesheet use `var(--text-sm)`, never a pixel value. Hierarchy below body text comes from colour (`--legend`, `--muted-foreground`) and weight.
- **A preset is never lossy.** The server stores the `config` blob as received and never decodes-and-remarshals it. The client drops a stored key only when its `typeof` disagrees with the default's, and never rewrites the stored row on load.
- **`playgroundPresets`, never `presets`, in client code.** `keys.presets`, `usePresets` and `PresetsResponse` already mean the provider catalogue shipped with the binary. A second meaning in the same query cache is a defect.
- **Existing test assertions do not change.** Every suite under `web/src/features/playground/`, `internal/store/` and `internal/admin/` stays green untouched.
- **Comments explain WHY, never WHAT.** No comment may reference this plan, a task number, or that something was recently added.
- **Commit subjects** are `<type>(<scope>): <subject>`, imperative, 50 characters or fewer measured across the whole line including type and scope, no trailing period. Stage explicit paths — never `git add -A`. English only.
- **Branch.** `playground-stage3`, already created from `master`.
- **No new dependencies.**
- **Never stage `providers.png`** (untracked at the repo root) or anything under `.playwright-mcp/` or `.superpowers/`.

## Baselines before Task 1

`web`: 56 files, 566 tests. Go: `go build`, `go vet` clean, 27 packages pass. Migrations run to `0013`; `//go:embed migrations/*.sql` picks up a new file with no registration step.

## Definition of Done

| # | Criterion | Verification |
|---|---|---|
| D1 | A preset round-trips through the store, and a duplicate name is reported rather than thrown | `go test ./internal/store/ -run TestPlaygroundPreset` |
| D2 | An unknown key in the blob survives a save | `go test ./internal/admin/ -run TestPlaygroundPresetBlobIsOpaque` |
| D3 | A wrong-typed stored field is dropped, not crashed on | `cd web && npm test -- preset-config` |
| D4 | The picker loads a preset wholesale and offers overwrite on a name clash | `cd web && npm test -- preset-picker` |
| D5 | Compare runs up to four columns concurrently off one shared body | `cd web && npm test -- compare` |
| D6 | The mockup set still builds and passes its gate | `cd docs/ux/mockups && python3 qa.py && python3 build.py` |
| D7 | Verified live and deployed | UAT at 1600×1000 and 1280×800, both themes; container healthy, bundle byte-match |

---

### Task 1: The presets table and its store methods

**Files:**
- Create: `internal/store/migrations/0014_playground.sql`
- Create: `internal/store/playground.go`
- Create: `internal/store/playground_test.go`

**Interfaces:**
- Produces: `store.PlaygroundPreset{ID, Name, Dialect, Model string; Config json.RawMessage; CreatedAt, UpdatedAt time.Time}`; `(*DB).CreatePlaygroundPreset(ctx, name, dialect, model string, config json.RawMessage) (PlaygroundPreset, error)`; `(*DB).PlaygroundPresets(ctx) ([]PlaygroundPreset, error)`; `(*DB).PlaygroundPresetByName(ctx, name string) (PlaygroundPreset, bool, error)`; `(*DB).UpdatePlaygroundPreset(ctx, id, name, dialect, model string, config json.RawMessage) (bool, error)`; `(*DB).DeletePlaygroundPreset(ctx, id string) (bool, error)`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 2: `internal/store/proxytokens.go` already establishes the CRUD shape, and skip 3: §8.3 gives the DDL verbatim

- [ ] **Step 1: Write the failing test**

Create `internal/store/playground_test.go`:

```go
package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPlaygroundPresetRoundTripsItsBlobUntouched(t *testing.T) {
	// The blob is the operator's saved request. A store that reshaped it
	// would make every preset quietly lossy.
	ctx := context.Background()
	db := migrated(t)

	blob := json.RawMessage(`{"system":"be brief","topK":"40","unknownFutureField":7}`)
	got, err := db.CreatePlaygroundPreset(ctx, "terse", "anthropic", "claude", blob)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" {
		t.Fatal("no id assigned")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("timestamps not set")
	}

	list, err := db.PlaygroundPresets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d presets, want 1", len(list))
	}
	if string(list[0].Config) != string(blob) {
		t.Errorf("config = %s, want %s", list[0].Config, blob)
	}
	if list[0].Name != "terse" || list[0].Dialect != "anthropic" || list[0].Model != "claude" {
		t.Errorf("columns did not round-trip: %+v", list[0])
	}
}

func TestPlaygroundPresetReportsADuplicateNameRatherThanFailing(t *testing.T) {
	// The save dialog offers to overwrite, so it needs the clashing preset's
	// id -- a bare error would leave it nothing to PATCH.
	ctx := context.Background()
	db := migrated(t)

	first, err := db.CreatePlaygroundPreset(ctx, "terse", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	found, ok, err := db.PlaygroundPresetByName(ctx, "terse")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("existing preset not found by name")
	}
	if found.ID != first.ID {
		t.Errorf("found id %s, want %s", found.ID, first.ID)
	}

	if _, _, err := db.PlaygroundPresetByName(ctx, "nothing-saved-here"); err != nil {
		t.Errorf("absent name errored: %v", err)
	}
	if _, ok, _ := db.PlaygroundPresetByName(ctx, "nothing-saved-here"); ok {
		t.Error("absent name reported as found")
	}
}

func TestPlaygroundPresetUpdatesAndDeletesReportWhetherARowMoved(t *testing.T) {
	ctx := context.Background()
	db := migrated(t)

	made, err := db.CreatePlaygroundPreset(ctx, "terse", "openai", "gpt", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	ok, err := db.UpdatePlaygroundPreset(ctx, made.ID, "verbose", "gemini", "flash",
		json.RawMessage(`{"system":"explain"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("update reported no row")
	}
	list, _ := db.PlaygroundPresets(ctx)
	if list[0].Name != "verbose" || list[0].Dialect != "gemini" || list[0].Model != "flash" {
		t.Errorf("update did not land: %+v", list[0])
	}
	if string(list[0].Config) != `{"system":"explain"}` {
		t.Errorf("config = %s", list[0].Config)
	}

	// An unknown id is not an error, it is a 404 the caller must be able to
	// tell apart from a successful write.
	if ok, err := db.UpdatePlaygroundPreset(ctx, "nope", "x", "openai", "m", json.RawMessage(`{}`)); err != nil || ok {
		t.Errorf("update of unknown id = %v, %v; want false, nil", ok, err)
	}
	if ok, err := db.DeletePlaygroundPreset(ctx, made.ID); err != nil || !ok {
		t.Errorf("delete = %v, %v; want true, nil", ok, err)
	}
	if ok, _ := db.DeletePlaygroundPreset(ctx, made.ID); ok {
		t.Error("second delete reported a row")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run TestPlaygroundPreset
```

Expected: FAIL to compile — `db.CreatePlaygroundPreset` undefined.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/0014_playground.sql`:

```sql
-- Named request configurations for the playground.
--
-- config is an opaque JSON object: the console's own settings, stored as the
-- console sent them. It is deliberately not a set of columns. The sampling
-- parameters it holds track three providers' wire formats and have changed
-- twice already, and a column per parameter would mean a migration every time
-- the console learns a new one.
--
-- The unique index on name is what lets the save dialog offer to overwrite:
-- the clash is detected before the insert, so the operator is asked rather
-- than shown a constraint error.
CREATE TABLE playground_presets (
  id         TEXT PRIMARY KEY,
  name       TEXT    NOT NULL,
  dialect    TEXT    NOT NULL,
  model      TEXT    NOT NULL,
  config     TEXT    NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX idx_playground_presets_name ON playground_presets(name);
```

- [ ] **Step 4: Write the store methods**

Create `internal/store/playground.go`:

```go
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PlaygroundPreset is a named request configuration.
//
// Config is carried as raw JSON rather than a struct: the store is not the
// authority on what a request setting is, and decoding it here would silently
// drop any field the console had learned and this build had not.
type PlaygroundPreset struct {
	ID        string
	Name      string
	Dialect   string
	Model     string
	Config    json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

func newPresetID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate preset id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (d *DB) CreatePlaygroundPreset(
	ctx context.Context, name, dialect, model string, config json.RawMessage,
) (PlaygroundPreset, error) {
	id, err := newPresetID()
	if err != nil {
		return PlaygroundPreset{}, err
	}
	now := time.Now().UTC()
	p := PlaygroundPreset{
		ID: id, Name: name, Dialect: dialect, Model: model,
		Config: config, CreatedAt: now, UpdatedAt: now,
	}
	_, err = d.Write.ExecContext(ctx,
		`INSERT INTO playground_presets (id, name, dialect, model, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Dialect, p.Model, string(p.Config), now.Unix(), now.Unix())
	if err != nil {
		return PlaygroundPreset{}, fmt.Errorf("store playground preset: %w", err)
	}
	return p, nil
}

func (d *DB) PlaygroundPresets(ctx context.Context) ([]PlaygroundPreset, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, name, dialect, model, config, created_at, updated_at
		   FROM playground_presets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list playground presets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []PlaygroundPreset{}
	for rows.Next() {
		p, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlaygroundPresetByName finds the preset a name already belongs to, so a save
// that would clash can offer to overwrite that row rather than reporting a
// constraint failure.
func (d *DB) PlaygroundPresetByName(ctx context.Context, name string) (PlaygroundPreset, bool, error) {
	row := d.Read.QueryRowContext(ctx,
		`SELECT id, name, dialect, model, config, created_at, updated_at
		   FROM playground_presets WHERE name = ?`, name)
	p, err := scanPreset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PlaygroundPreset{}, false, nil
	}
	if err != nil {
		return PlaygroundPreset{}, false, err
	}
	return p, true, nil
}

func (d *DB) UpdatePlaygroundPreset(
	ctx context.Context, id, name, dialect, model string, config json.RawMessage,
) (bool, error) {
	res, err := d.Write.ExecContext(ctx,
		`UPDATE playground_presets
		    SET name = ?, dialect = ?, model = ?, config = ?, updated_at = ?
		  WHERE id = ?`,
		name, dialect, model, string(config), time.Now().UTC().Unix(), id)
	if err != nil {
		return false, fmt.Errorf("update playground preset: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (d *DB) DeletePlaygroundPreset(ctx context.Context, id string) (bool, error) {
	res, err := d.Write.ExecContext(ctx, `DELETE FROM playground_presets WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete playground preset: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// scanner is what *sql.Row and *sql.Rows have in common, so one scan serves
// the lookup and the listing.
type scanner interface{ Scan(dest ...any) error }

func scanPreset(s scanner) (PlaygroundPreset, error) {
	var (
		p                = PlaygroundPreset{}
		cfg              string
		created, updated int64
	)
	if err := s.Scan(&p.ID, &p.Name, &p.Dialect, &p.Model, &cfg, &created, &updated); err != nil {
		return PlaygroundPreset{}, err
	}
	p.Config = json.RawMessage(cfg)
	p.CreatedAt = time.Unix(created, 0).UTC()
	p.UpdatedAt = time.Unix(updated, 0).UTC()
	return p, nil
}
```

- [ ] **Step 5: Run the gate**

```bash
export PATH=$PATH:/usr/local/go/bin
go build ./... && go vet ./... && go test -count=1 ./internal/...
```

Expected: PASS, existing store tests included.

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/0014_playground.sql \
        internal/store/playground.go \
        internal/store/playground_test.go
git commit -m "feat(store): add the playground presets table"
```

---

### Task 2: The preset endpoints

**Files:**
- Create: `internal/admin/playgroundstore.go`
- Create: `internal/admin/playgroundstore_test.go`
- Modify: `internal/admin/admin.go` (four route registrations beside the existing three playground routes)

**Interfaces:**
- Consumes: every method Task 1 produced on `*store.DB`.
- Produces: `GET /api/playground/presets` returning a JSON array of `{id, name, dialect, model, config, created_at, updated_at}` with `config` as a raw JSON object; `POST /api/playground/presets` taking `{name, dialect, model, config}`, answering 409 with `{"error": "...", "id": "<existing>"}` on a name clash; `PATCH /api/playground/presets/{id}`; `DELETE /api/playground/presets/{id}`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** advisor - the opaque-bytes write path is load-bearing for the non-lossy property and an advisor pass amended it away from decode-and-remarshal

- [ ] **Step 1: Write the failing test**

Create `internal/admin/playgroundstore_test.go`:

```go
package admin

import (
	"encoding/json"
	"testing"
)

func TestPlaygroundPresetBlobIsOpaque(t *testing.T) {
	// A field the console learned before this binary did must survive a save.
	// Decoding into a struct of today's fields and re-marshalling would drop
	// it silently, which is the lossy preset the design forbids.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	body := `{"name":"terse","dialect":"anthropic","model":"claude",
	          "config":{"system":"be brief","fieldFromTheFuture":{"nested":true}}}`
	if w := do(t, s, cookie, token, "POST", "/api/playground/presets", body); w.Code != 201 {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}

	w := do(t, s, cookie, token, "GET", "/api/playground/presets", "")
	if w.Code != 200 {
		t.Fatalf("list = %d", w.Code)
	}
	var list []struct {
		ID     string         `json:"id"`
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d, want 1", len(list))
	}
	future, ok := list[0].Config["fieldFromTheFuture"].(map[string]any)
	if !ok || future["nested"] != true {
		t.Errorf("unknown field did not survive: %v", list[0].Config)
	}
}

func TestPlaygroundPresetNameClashOffersTheExistingRow(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	first := `{"name":"terse","dialect":"openai","model":"gpt","config":{}}`

	w := do(t, s, cookie, token, "POST", "/api/playground/presets", first)
	if w.Code != 201 {
		t.Fatalf("first create = %d", w.Code)
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}

	w = do(t, s, cookie, token, "POST", "/api/playground/presets", first)
	if w.Code != 409 {
		t.Fatalf("clash = %d, want 409", w.Code)
	}
	var clash struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &clash); err != nil {
		t.Fatal(err)
	}
	if clash.ID != made.ID {
		t.Errorf("clash id = %q, want %q", clash.ID, made.ID)
	}
	if clash.Error == "" {
		t.Error("clash carried no message")
	}
}

func TestPlaygroundPresetRejectsABlobThatIsNotAnObject(t *testing.T) {
	// The blob is stored unparsed, so this is the only place its shape is
	// checked. A bare array or string would reach the client as a config it
	// cannot merge.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	for _, cfg := range []string{`[1,2]`, `"text"`, `7`, `null`} {
		body := `{"name":"n","dialect":"openai","model":"m","config":` + cfg + `}`
		if w := do(t, s, cookie, token, "POST", "/api/playground/presets", body); w.Code != 400 {
			t.Errorf("config %s = %d, want 400", cfg, w.Code)
		}
	}
}

func TestPlaygroundPresetUpdateAndDeleteAnswer404ForAnUnknownID(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	body := `{"name":"n","dialect":"openai","model":"m","config":{}}`
	if w := do(t, s, cookie, token, "PATCH", "/api/playground/presets/nope", body); w.Code != 404 {
		t.Errorf("patch unknown = %d, want 404", w.Code)
	}
	if w := do(t, s, cookie, token, "DELETE", "/api/playground/presets/nope", ""); w.Code != 404 {
		t.Errorf("delete unknown = %d, want 404", w.Code)
	}
}
```

The harness is already in `internal/admin/fixtures_test.go`: `testServerFull(t) (*Server, *store.DB)`, `login(t, s) (*http.Cookie, string)` and `do(t, s, cookie, token, method, path, body) *httptest.ResponseRecorder`, which sets the CSRF header and content type for every non-GET. Do not add a second harness.

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run TestPlaygroundPreset
```

Expected: FAIL — the routes 404, or the package fails to compile if the harness names differ.

- [ ] **Step 3: Write the handlers**

Create `internal/admin/playgroundstore.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

// presetView is the wire shape. Config is json.RawMessage in both directions:
// the server is a courier for the console's own settings, and a struct here
// would strip any field this binary has not learned yet.
type presetView struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Dialect   string          `json:"dialect"`
	Model     string          `json:"model"`
	Config    json.RawMessage `json:"config"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

func viewOfPreset(p store.PlaygroundPreset) presetView {
	return presetView{
		ID: p.ID, Name: p.Name, Dialect: p.Dialect, Model: p.Model, Config: p.Config,
		CreatedAt: p.CreatedAt.Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.Format(time.RFC3339),
	}
}

type presetBody struct {
	Name    string          `json:"name"`
	Dialect string          `json:"dialect"`
	Model   string          `json:"model"`
	Config  json.RawMessage `json:"config"`
}

// readPresetBody decodes and validates everything the store will not.
//
// The blob's interior is never inspected beyond confirming it is an object:
// that is the one shape the client can merge, and anything inside it belongs
// to the console.
func readPresetBody(w http.ResponseWriter, r *http.Request) (presetBody, bool) {
	var body presetBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return presetBody{}, false
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "a preset needs a name")
		return presetBody{}, false
	}
	var probe map[string]any
	if err := json.Unmarshal(body.Config, &probe); err != nil || probe == nil {
		writeError(w, http.StatusBadRequest, "config must be a JSON object")
		return presetBody{}, false
	}
	return body, true
}

func (s *Server) handleListPlaygroundPresets(w http.ResponseWriter, r *http.Request) {
	presets, err := s.deps.DB.PlaygroundPresets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []presetView{}
	for _, p := range presets {
		out = append(out, viewOfPreset(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreatePlaygroundPreset(w http.ResponseWriter, r *http.Request) {
	body, ok := readPresetBody(w, r)
	if !ok {
		return
	}
	// Checked before the insert rather than caught after it: the dialog needs
	// the clashing row's id to offer an overwrite, and the unique index would
	// only tell it that something went wrong.
	if existing, found, err := s.deps.DB.PlaygroundPresetByName(r.Context(), body.Name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if found {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "a preset called " + body.Name + " already exists",
			"id":    existing.ID,
		})
		return
	}
	made, err := s.deps.DB.CreatePlaygroundPreset(
		r.Context(), body.Name, body.Dialect, body.Model, body.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 201, matching every other create in this package.
	writeJSON(w, http.StatusCreated, viewOfPreset(made))
}

func (s *Server) handleUpdatePlaygroundPreset(w http.ResponseWriter, r *http.Request) {
	body, ok := readPresetBody(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	moved, err := s.deps.DB.UpdatePlaygroundPreset(
		r.Context(), id, body.Name, body.Dialect, body.Model, body.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !moved {
		writeError(w, http.StatusNotFound, "no such preset")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) handleDeletePlaygroundPreset(w http.ResponseWriter, r *http.Request) {
	removed, err := s.deps.DB.DeletePlaygroundPreset(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "no such preset")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Register the routes**

In `internal/admin/admin.go`, directly after the three existing playground routes (`POST /api/playground`, `.../count`, `.../aux`):

```go
	s.mux.HandleFunc("GET /api/playground/presets", s.requireSession(s.handleListPlaygroundPresets))
	s.mux.HandleFunc("POST /api/playground/presets", s.requireCSRF(s.handleCreatePlaygroundPreset))
	s.mux.HandleFunc("PATCH /api/playground/presets/{id}", s.requireCSRF(s.handleUpdatePlaygroundPreset))
	s.mux.HandleFunc("DELETE /api/playground/presets/{id}", s.requireCSRF(s.handleDeletePlaygroundPreset))
```

- [ ] **Step 5: Run the gate**

```bash
export PATH=$PATH:/usr/local/go/bin
go build ./... && go vet ./... && go test -count=1 ./internal/...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/playgroundstore.go \
        internal/admin/playgroundstore_test.go \
        internal/admin/admin.go
git commit -m "feat(admin): add the preset endpoints"
```

---

### Task 3: The client's preset data layer

**Files:**
- Modify: `web/src/lib/api-types.ts`
- Modify: `web/src/lib/queries.ts`
- Create: `web/src/features/playground/preset-config.ts`
- Create: `web/src/features/playground/preset-config.test.ts`

**Interfaces:**
- Consumes: the wire shape Task 2 produced; `emptyConfig`, `PlaygroundConfig` from `./config`.
- Produces: `PlaygroundPreset` and `keys.playgroundPresets` and `usePlaygroundPresets()`; `mergeStoredConfig(stored: unknown, model: string, dialect: PlaygroundDialect): PlaygroundConfig`; `toStoredConfig(config: PlaygroundConfig): Record<string, unknown>`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** advisor - the type-checked merge is the client half of the non-lossy property and an advisor pass amended it away from a blind spread

- [ ] **Step 1: Write the failing test**

Create `web/src/features/playground/preset-config.test.ts`:

```ts
import { describe, expect, it } from "vitest"
import { mergeStoredConfig, toStoredConfig } from "./preset-config"
import { emptyConfig } from "./config"

describe("reading a stored preset", () => {
  it("takes the model and dialect from the columns, not the blob", () => {
    const out = mergeStoredConfig({ model: "ignored", dialect: "ignored" }, "claude", "anthropic")
    expect(out.model).toBe("claude")
    expect(out.dialect).toBe("anthropic")
  })

  it("keeps every stored value whose type matches", () => {
    const out = mergeStoredConfig(
      { system: "be brief", topK: "40", stream: false },
      "m",
      "anthropic",
    )
    expect(out.system).toBe("be brief")
    expect(out.topK).toBe("40")
    expect(out.stream).toBe(false)
  })

  it("defaults a field the preset was saved before", () => {
    // A blob written by an older console has no key for a control added since.
    // It must arrive at its default rather than as undefined: chatBody calls
    // .split and .trim on these without checking.
    const out = mergeStoredConfig({ system: "hi" }, "m", "openai")
    expect(out.stopRaw).toBe("")
    expect(out.schemaRaw).toBe("")
    expect(out.reasoningBudget).toBe("")
  })

  it("drops a stored value of the wrong type rather than passing it on", () => {
    // Reachable through the operator-facing API. Passing 42 through would be a
    // TypeError at parseStopLines, not a degraded setting.
    const out = mergeStoredConfig({ stopRaw: 42, schemaRaw: null, stream: "yes" }, "m", "openai")
    expect(out.stopRaw).toBe("")
    expect(out.schemaRaw).toBe("")
    expect(out.stream).toBe(true)
  })

  it("ignores a key the config does not have", () => {
    const out = mergeStoredConfig({ fieldFromTheFuture: 7 }, "m", "openai")
    expect(out).not.toHaveProperty("fieldFromTheFuture")
  })

  it("survives a blob that is not an object at all", () => {
    for (const junk of [null, "text", 7, [1, 2]]) {
      const out = mergeStoredConfig(junk, "m", "openai")
      expect(out).toEqual({ ...emptyConfig(), model: "m", dialect: "openai" })
    }
  })
})

describe("writing a preset", () => {
  it("stores everything except model and dialect, which are columns", () => {
    const stored = toStoredConfig({ ...emptyConfig(), model: "m", dialect: "gemini", topP: "0.9" })
    expect(stored).not.toHaveProperty("model")
    expect(stored).not.toHaveProperty("dialect")
    expect(stored.topP).toBe("0.9")
    expect(stored.stream).toBe(true)
  })

  it("round-trips a config unchanged", () => {
    const original = { ...emptyConfig(), model: "m", dialect: "anthropic" as const, topK: "40" }
    expect(mergeStoredConfig(toStoredConfig(original), "m", "anthropic")).toEqual(original)
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- preset-config
```

Expected: FAIL — cannot resolve `./preset-config`.

- [ ] **Step 3: Write the merge**

Create `web/src/features/playground/preset-config.ts`:

```ts
import { emptyConfig, type PlaygroundConfig } from "./config"
import type { PlaygroundDialect } from "../../lib/api-types"

/**
 * A stored preset, reconstituted into pane state.
 *
 * Merged over the defaults rather than spread onto them, and only where the
 * stored value's type matches the default's. The missing-field half of blob
 * drift is what a plain spread fixes; the wrong-typed half is the one that
 * bites, because chatBody calls .split and .trim on these without checking, so
 * a value of the wrong type is a crash rather than a degraded setting. Keys the
 * config does not have are dropped here, which is what lets the writer below
 * spread safely.
 */
export function mergeStoredConfig(
  stored: unknown,
  model: string,
  dialect: PlaygroundDialect,
): PlaygroundConfig {
  const base = emptyConfig()
  const out: PlaygroundConfig = { ...base, model, dialect }
  if (typeof stored !== "object" || stored === null || Array.isArray(stored)) return out

  const record = stored as Record<string, unknown>
  const writable = out as unknown as Record<string, unknown>
  for (const key of Object.keys(base)) {
    if (key === "model" || key === "dialect") continue
    const value = record[key]
    if (typeof value === typeof (base as unknown as Record<string, unknown>)[key]) {
      writable[key] = value
    }
  }
  return out
}

/** What goes in the blob: everything but the two columns. */
export function toStoredConfig(config: PlaygroundConfig): Record<string, unknown> {
  const { model: _model, dialect: _dialect, ...rest } = config
  return rest
}
```

- [ ] **Step 4: Add the wire type**

In `web/src/lib/api-types.ts`, beside the other playground types:

```ts
/** A saved request configuration. Distinct from `Preset`, which is a provider
 *  preset shipped with the binary and never written by an operator. */
export type PlaygroundPreset = {
  id: string
  name: string
  dialect: PlaygroundDialect
  model: string
  /** The console's own settings, stored and returned untouched. */
  config: unknown
  created_at: string
  updated_at: string
}
```

- [ ] **Step 5: Add the query**

In `web/src/lib/queries.ts`, add to the `keys` object after `presets`:

```ts
  playgroundPresets: ["playground-presets"] as const,
```

and the hook beside `usePresets`:

```ts
export function usePlaygroundPresets(extra?: Extra<PlaygroundPreset[]>) {
  return useQuery({
    queryKey: keys.playgroundPresets,
    queryFn: () => api.get<PlaygroundPreset[]>("/api/playground/presets"),
    ...extra,
  })
}
```

Add `PlaygroundPreset` to the existing type import from `./api-types`.

- [ ] **Step 6: Run the gate**

```bash
cd web && npm test && npm run typecheck
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/api-types.ts web/src/lib/queries.ts \
        web/src/features/playground/preset-config.ts \
        web/src/features/playground/preset-config.test.ts
git commit -m "feat(web): read and write preset configs"
```

---

### Task 4: The preset picker

**Files:**
- Create: `web/src/features/playground/config-pane/preset-picker.tsx`
- Create: `web/src/features/playground/config-pane/preset-picker.test.tsx`
- Modify: `web/src/features/playground/config-pane/config-pane.tsx`

**Interfaces:**
- Consumes: `usePlaygroundPresets`, `keys.playgroundPresets`, `mergeStoredConfig`, `toStoredConfig`, `useApiMutation`, `api`.
- Produces: `PresetPicker({ config, onChange }: { config: PlaygroundConfig; onChange: (next: PlaygroundConfig) => void })`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: `web/src/features/routing/add-alias-dialog.tsx` is the established save-dialog pattern in this console, and its Dialog usage is reproduced above

- [ ] **Step 1: Write the failing test**

Create `web/src/features/playground/config-pane/preset-picker.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi } from "vitest"
import { PresetPicker } from "./preset-picker"
import { emptyConfig } from "../config"
import type { PlaygroundPreset } from "../../../lib/api-types"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

const saved: PlaygroundPreset = {
  id: "p1",
  name: "terse",
  dialect: "anthropic",
  model: "claude",
  config: { system: "be brief", topK: "40" },
  created_at: "2026-08-30T00:00:00Z",
  updated_at: "2026-08-30T00:00:00Z",
}

vi.mock("../../../lib/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/queries")>()),
  usePlaygroundPresets: () => ({ data: [saved], isLoading: false }),
}))

function mounted(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

describe("loading a preset", () => {
  it("replaces the pane wholesale, model and dialect included", async () => {
    // Wholesale is the point: a preset that merged into whatever was already
    // typed would produce a request neither the operator nor the preset asked
    // for.
    const onChange = vi.fn()
    mounted(
      <PresetPicker config={{ ...emptyConfig(), model: "gpt", dialect: "openai" }} onChange={onChange} />,
    )
    await userEvent.click(screen.getByRole("button", { name: /load a preset|preset/i }))
    await userEvent.click(await screen.findByText("terse"))

    expect(onChange).toHaveBeenCalledTimes(1)
    const next = onChange.mock.calls[0][0]
    expect(next.model).toBe("claude")
    expect(next.dialect).toBe("anthropic")
    expect(next.system).toBe("be brief")
    expect(next.topK).toBe("40")
  })
})

describe("saving a preset", () => {
  it("asks for a name before writing anything", async () => {
    mounted(<PresetPicker config={{ ...emptyConfig(), model: "gpt" }} onChange={() => {}} />)
    await userEvent.click(screen.getByRole("button", { name: /save/i }))
    expect(await screen.findByLabelText(/name/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- preset-picker
```

Expected: FAIL — cannot resolve `./preset-picker`.

- [ ] **Step 3: Write the picker**

Create `web/src/features/playground/config-pane/preset-picker.tsx`:

```tsx
import { useState } from "react"
import {
  Button, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
  Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "darkraise-ui"
import { Trash2 } from "lucide-react"
import { api } from "../../../lib/api"
import { useApiMutation } from "../../../lib/mutations"
import { keys, usePlaygroundPresets } from "../../../lib/queries"
import { mergeStoredConfig, toStoredConfig } from "../preset-config"
import type { PlaygroundConfig } from "../config"
import type { PlaygroundPreset } from "../../../lib/api-types"

/** What a save sends. model and dialect are columns; the rest is the blob. */
function bodyFor(name: string, config: PlaygroundConfig) {
  return {
    name,
    dialect: config.dialect,
    model: config.model,
    config: toStoredConfig(config),
  }
}

/**
 * Saved request configurations, above the fields they fill in.
 *
 * Loading replaces the pane wholesale rather than merging into what is already
 * typed: a half-loaded preset would produce a request neither the operator nor
 * the preset asked for. A name that is already taken is not an error path —
 * the server answers 409 with the clashing row's id precisely so this dialog
 * can offer to overwrite it.
 */
export function PresetPicker({
  config,
  onChange,
}: {
  config: PlaygroundConfig
  onChange: (next: PlaygroundConfig) => void
}) {
  const { data: presets } = usePlaygroundPresets()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")

  const close = () => {
    setOpen(false)
    setName("")
  }

  // The clash is found in the list already on screen rather than by sending a
  // save and reading the rejection: ApiError carries a status and a message
  // and no body, so a 409's id could not be recovered from it anyway. The
  // server's unique index stays the integrity backstop.
  const clash = (presets ?? []).find((preset) => preset.name === name.trim())

  const save = useApiMutation<PlaygroundPreset, { name: string }>({
    mutationFn: (vars) => api.post<PlaygroundPreset>("/api/playground/presets", bodyFor(vars.name, config)),
    success: (_data, vars) => `Saved ${vars.name}`,
    invalidates: [keys.playgroundPresets],
    onSuccess: () => close(),
  })

  const overwrite = useApiMutation<{ id: string }, { id: string; name: string }>({
    mutationFn: (vars) =>
      api.patch<{ id: string }>(`/api/playground/presets/${vars.id}`, bodyFor(vars.name, config)),
    success: (_data, vars) => `Overwrote ${vars.name}`,
    invalidates: [keys.playgroundPresets],
    onSuccess: () => close(),
  })

  const remove = useApiMutation<void, { id: string; name: string }>({
    mutationFn: (vars) => api.del<void>(`/api/playground/presets/${vars.id}`),
    success: (_data, vars) => `Deleted ${vars.name}`,
    invalidates: [keys.playgroundPresets],
  })

  function load(preset: PlaygroundPreset) {
    onChange(mergeStoredConfig(preset.config, preset.model, preset.dialect))
  }

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor="pg-preset">Preset</Label>
      <div className="flex items-center gap-2">
        <Select
          value=""
          onValueChange={(id) => {
            const found = (presets ?? []).find((p) => p.id === id)
            if (found) load(found)
          }}
        >
          <SelectTrigger id="pg-preset" className="flex-1">
            <SelectValue placeholder="Load a preset" />
          </SelectTrigger>
          <SelectContent>
            {(presets ?? []).map((preset) => (
              <SelectItem key={preset.id} value={preset.id}>
                {preset.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button variant="ghost" onClick={() => setOpen(true)}>
          Save
        </Button>
      </div>

      {(presets ?? []).length > 0 ? (
        <ul className="flex flex-col">
          {(presets ?? []).map((preset) => (
            <li key={preset.id} className="flex items-center gap-2 text-sm">
              <button
                type="button"
                className="flex-1 truncate text-left hover:underline"
                onClick={() => load(preset)}
              >
                {preset.name}
              </button>
              <button
                type="button"
                aria-label={`Delete ${preset.name}`}
                className="shrink-0 p-1 text-[hsl(var(--legend))] hover:text-[hsl(var(--destructive))]"
                onClick={() => remove.mutate({ id: preset.id, name: preset.name })}
              >
                <Trash2 className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}

      <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>Save this request</DialogTitle>
            <DialogDescription>
              The model, the dialect and every setting in this pane, under a name you can
              load them back with.
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pg-preset-name">Name</Label>
            <Input
              id="pg-preset-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="terse"
              autoFocus
            />
            {clash ? (
              <p className="text-sm text-[hsl(var(--legend))]">
                A preset called {clash.name} already exists. Overwrite it?
              </p>
            ) : null}
          </div>

          <div className="mt-2 flex items-center justify-end gap-2 border-t pt-3">
            <Button variant="ghost" onClick={close}>
              Cancel
            </Button>
            {clash ? (
              <Button onClick={() => overwrite.mutate({ id: clash.id, name: clash.name })}>
                Overwrite
              </Button>
            ) : (
              <Button
                disabled={name.trim() === ""}
                onClick={() => save.mutate({ name: name.trim() })}
              >
                Save
              </Button>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
```


- [ ] **Step 4: Mount it in the pane**

In `web/src/features/playground/config-pane/config-pane.tsx`, render `<PresetPicker config={config} onChange={onChange} />` directly beneath the `<h2>Request</h2>` and above the `ModelCombobox`, so a preset is chosen before the fields it fills in.

- [ ] **Step 5: Run the gate**

```bash
cd web && npm test && npm run typecheck
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/playground/config-pane/
git commit -m "feat(web): add the preset picker"
```

---

### Task 5: Compare as N columns

**Files:**
- Create: `web/src/features/playground/compare-column.tsx`
- Create: `web/src/features/playground/compare.test.tsx`
- Modify: `web/src/features/playground/compare.tsx`

**Interfaces:**
- Consumes: `chatBody` from `./lib/request`, `drainSSE` from `./lib/stream`, `stream` from `../../lib/api`, `ModelCombobox`.
- Produces: `type ColumnStatus = "idle" | "streaming" | "done" | "error"`; `CompareColumn({ column, onModel, onRemove, candidates, loading, removable })`; `MAX_COLUMNS = 4`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 3: §9 fixes the shape, the cap, the shared body and the concurrency

- [ ] **Step 1: Write the failing test**

Create `web/src/features/playground/compare.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { Compare, MAX_COLUMNS } from "./compare"
import { emptyConfig } from "./config"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

vi.mock("../shell/model-combobox", () => ({
  ModelCombobox: ({ label, value }: { label: string; value: string }) => (
    <input aria-label={label} value={value} readOnly />
  ),
  useModelCandidates: () => ({ candidates: [], loading: false }),
}))

describe("the compare columns", () => {
  it("starts with two, which is the comparison the screen is named for", () => {
    render(<Compare config={emptyConfig()} />)
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(2)
  })

  it("adds a column on request", async () => {
    render(<Compare config={emptyConfig()} />)
    await userEvent.click(screen.getByRole("button", { name: /add/i }))
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(3)
  })

  it("stops at four, past which no column is wide enough to read", async () => {
    render(<Compare config={emptyConfig()} />)
    const add = screen.getByRole("button", { name: /add/i })
    await userEvent.click(add)
    await userEvent.click(add)
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(MAX_COLUMNS)
    expect(add).toBeDisabled()
  })

  it("removes a column but never the last two", async () => {
    render(<Compare config={emptyConfig()} />)
    await userEvent.click(screen.getByRole("button", { name: /add/i }))
    const removes = screen.getAllByRole("button", { name: /remove/i })
    await userEvent.click(removes[0])
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(2)
    for (const button of screen.queryAllByRole("button", { name: /remove/i })) {
      expect(button).toBeDisabled()
    }
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- compare
```

Expected: FAIL — `MAX_COLUMNS` is not exported.

- [ ] **Step 3: Extract the column**

Create `web/src/features/playground/compare-column.tsx`:

```tsx
import { Link } from "@tanstack/react-router"
import { Button, Card } from "darkraise-ui"
import { X } from "lucide-react"
import { ModelCombobox } from "../shell/model-combobox"

/** Where one column is in its run. Idle is before the first Run, not an error. */
export type ColumnStatus = "idle" | "streaming" | "done" | "error"

export type Column = {
  id: string
  model: string
  text: string
  requestId: string
  error: string
  status: ColumnStatus
  latencyMs: number | undefined
}

export function emptyColumn(id: string): Column {
  return { id, model: "", text: "", requestId: "", error: "", status: "idle", latencyMs: undefined }
}

const DOT: Record<ColumnStatus, string> = {
  idle: "bg-[hsl(var(--legend))]",
  streaming: "bg-[hsl(var(--primary))] motion-safe:animate-pulse",
  done: "bg-[hsl(var(--primary))]",
  error: "bg-[hsl(var(--destructive))]",
}

/** Named rather than drawn only as a colour: a dot alone tells a screen
 *  reader nothing, and colour alone tells a colourblind reader nothing. */
function StatusDot({ status }: { status: ColumnStatus }) {
  return <span role="status" aria-label={status} className={`size-2 shrink-0 rounded-full ${DOT[status]}`} />
}

export function CompareColumn({
  column,
  index,
  onModel,
  onRemove,
  candidates,
  loading,
  removable,
}: {
  column: Column
  index: number
  onModel: (model: string) => void
  onRemove: () => void
  candidates: string[]
  loading?: boolean
  removable: boolean
}) {
  return (
    <Card className="flex min-w-0 flex-col gap-3 p-4">
      <div className="flex items-center gap-2">
        <StatusDot status={column.status} />
        <div className="min-w-0 flex-1">
          <ModelCombobox
            label={`Column ${index + 1} model or alias`}
            value={column.model}
            onChange={onModel}
            candidates={candidates}
            loading={loading}
            className="w-full"
          />
        </div>
        <Button
          variant="ghost"
          aria-label={`Remove column ${index + 1}`}
          disabled={!removable}
          onClick={onRemove}
        >
          <X className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
        </Button>
      </div>

      <div className="min-h-24 rounded border p-3 font-mono text-sm whitespace-pre-wrap">
        {column.text}
      </div>
      {column.error ? <p className="text-destructive text-sm">{column.error}</p> : null}
      <div className="flex items-center gap-3 text-sm text-[hsl(var(--muted-foreground))]">
        {column.latencyMs !== undefined ? <span>{Math.round(column.latencyMs)} ms</span> : null}
        {column.requestId ? (
          <Link to="/requests/$id" params={{ id: column.requestId }} className="underline">
            View the trace for this request
          </Link>
        ) : null}
      </div>
    </Card>
  )
}
```

- [ ] **Step 4: Make Compare a list**

Replace `web/src/features/playground/compare.tsx` with:

```tsx
import { useRef, useState } from "react"
import { Button, Textarea } from "darkraise-ui"
import { stream, type StreamStart } from "../../lib/api"
import { chatBody } from "./lib/request"
import { drainSSE } from "./lib/stream"
import type { PlaygroundConfig } from "./config"
import { CompareColumn, emptyColumn, type Column } from "./compare-column"
import { useModelCandidates } from "../shell/model-combobox"
import type { PlaygroundMessage } from "../../lib/api-types"

/** Past four, no column is wide enough to read a wrapped answer in, and the
 *  comparison the screen exists for stops being possible. */
export const MAX_COLUMNS = 4

/** Two is the comparison the screen is named for. */
const MIN_COLUMNS = 2

async function runColumn(
  model: string,
  prompt: string,
  config: PlaygroundConfig,
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
    )) {
      buffer += chunk
      const { text, rest } = drainSSE(buffer, config.dialect)
      buffer = rest
      if (text) update((c) => ({ ...c, text: c.text + text }))
    }
    update((c) => ({ ...c, status: "done" }))
  } catch (err) {
    update((c) => ({ ...c, error: (err as Error).message, status: "error" }))
  } finally {
    update((c) => ({ ...c, latencyMs: performance.now() - started }))
  }
}

/** Up to four models against the same prompt, run concurrently through the
 *  exact request chat sends — chatBody is shared rather than rebuilt, so a
 *  difference in the transcripts reflects the models, not a second, slightly
 *  different request shape. */
export function Compare({ config }: { config: PlaygroundConfig }) {
  const counter = useRef(MIN_COLUMNS)
  const [prompt, setPrompt] = useState("")
  const [columns, setColumns] = useState<Column[]>(() => [emptyColumn("c0"), emptyColumn("c1")])

  const { candidates, loading } = useModelCandidates()
  const busy = columns.some((c) => c.status === "streaming")
  const canRun = !busy && prompt !== "" && columns.every((c) => c.model !== "")

  const updateColumn = (id: string, fn: (c: Column) => Column) =>
    setColumns((cs) => cs.map((c) => (c.id === id ? fn(c) : c)))

  function run() {
    if (!canRun) return
    // Started in one pass so they overlap: run sequentially and the latency
    // readings beside them would measure the queue, not the providers.
    for (const column of columns) {
      void runColumn(column.model, prompt, config, (fn) => updateColumn(column.id, fn))
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-6">
      <Textarea placeholder="Prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} />
      <div className="flex items-center gap-2">
        <Button onClick={run} disabled={!canRun}>
          {busy ? "Running…" : "Run"}
        </Button>
        <Button
          variant="ghost"
          disabled={columns.length >= MAX_COLUMNS}
          onClick={() => setColumns((cs) => [...cs, emptyColumn(`c${counter.current++}`)])}
        >
          Add a column
        </Button>
      </div>
      <div
        className="grid gap-4"
        style={{ gridTemplateColumns: `repeat(${columns.length}, minmax(0, 1fr))` }}
      >
        {columns.map((column, index) => (
          <CompareColumn
            key={column.id}
            column={column}
            index={index}
            candidates={candidates}
            loading={loading}
            removable={columns.length > MIN_COLUMNS}
            onModel={(model) => updateColumn(column.id, (c) => ({ ...c, model }))}
            onRemove={() => setColumns((cs) => cs.filter((c) => c.id !== column.id))}
          />
        ))}
      </div>
    </div>
  )
}
```

The grid is an inline `gridTemplateColumns` rather than a Tailwind class because the
count is data: `grid-cols-${n}` is not a class Tailwind can see at build time and
would silently not exist. This is a geometry style, not a colour or a font size.

- [ ] **Step 5: Run the gate**

```bash
cd web && npm test && npm run typecheck
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/playground/compare.tsx \
        web/src/features/playground/compare-column.tsx \
        web/src/features/playground/compare.test.tsx
git commit -m "feat(web): grow compare to four columns"
```

---

### Task 6: Redraw the two mockup fragments

**Files:**
- Modify: `docs/ux/mockups/fragments/11-playground.html`
- Modify: `docs/ux/mockups/fragments/12-playground-compare.html`

**Interfaces:** none.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 0 = 3
**Approach:** inline - skip 3: §3's stage 3 gate names both fragments and what each must show

- [ ] **Step 1: Read the pipeline's rules**

```bash
cd docs/ux/mockups && sed -n '1,60p' qa.py
```

The hard rules: a fragment declares no colour of its own (every hex is a gate failure — colour lives in `darkrouter-ui.css`), and nothing is fetched from the network. Read both current fragments before editing; copy their chrome rather than inventing it.

- [ ] **Step 2: Fold the preset picker into `11-playground.html`**

Add the picker directly beneath the "Request" heading and above Model, drawn as the shipped component is: a select showing a saved preset's name, and a Save control beside it. Everything else in the fragment stays as stage 2 left it. Update the `<p class="legend">` to say the pane now remembers.

- [ ] **Step 3: Redraw `12-playground-compare.html` as N columns**

Three columns with an Add control (and a fourth slot implied), each column carrying a model combo, a status dot, an answer, and its latency and trace link. Show one column mid-stream and one done, so the status dot is doing visible work.

**Typography:** introduce no font size below the scale's floor; use the classes the surrounding fragments use.

- [ ] **Step 4: Run the gate**

```bash
cd docs/ux/mockups && python3 qa.py && python3 build.py
```

Expected: both clean.

- [ ] **Step 5: Commit**

```bash
git add docs/ux/mockups/fragments/11-playground.html \
        docs/ux/mockups/fragments/12-playground-compare.html \
        docs/ux/mockups/index.html docs/ux/mockups/artifact.html
git commit -m "docs(ux): draw presets and the compare columns"
```

---

### Task 7: Verify live, then deploy

**Files:** none, until the record commit.

**Interfaces:** none.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 0 - spec 1 - coupling 0 - risk 2 = 3
**Approach:** inline - skip 3: the spec's stage 3 gate and this repository's CLAUDE.md fix the whole procedure

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

- [ ] **Step 3: Confirm the served bundle is this build**

```bash
asset=$(curl -s http://localhost:8091/ | grep -o 'assets/index-[A-Za-z0-9_-]*\.js')
curl -s "http://localhost:8091/$asset" > /tmp/served.js
cmp /tmp/served.js internal/admin/dist/assets/index-*.js && echo "deploy matches source"
```

Compare bytes, not filenames: Vite's hash differs between the image's build path and the repo path.

- [ ] **Step 4: Look at it**

Password from `.uat-credentials`; never copy it into a tracked file. At **1600×1000** and **1280×800**, **light and dark**:

| Check | What passing looks like |
|---|---|
| D4 | Save a preset under anthropic with Top K set. It appears in the picker |
| Round trip | Load that preset with the pane on openai. Model and dialect switch to anthropic, Top K reads 40 and is enabled. Now switch to openai: Top K stays 40 and disables |
| Overwrite | Save again under the same name. The dialog offers to overwrite rather than reporting an error |
| D5 | Compare shows two columns, adds to four, refuses a fifth, and runs them concurrently — the status dots move together, not in sequence |
| Pane height | The pane still scrolls its own contents with the picker added |
| Contrast | §8.1 leaves this stage to decide: with the preset loaded and Top K disabled at 40, judge whether the retained value is readable enough. A disabled input renders at half opacity, putting it near 4:1 |

Any failure is a defect in Tasks 3–5, not a new task: fix it there, re-run the suite, redeploy.

The Contrast row is a decision rather than a pass/fail, and it is yours to make with the
screenshots in front of you. If the retained value is hard to read, dim the field's chrome and keep
the value itself at body contrast — the value is what a preset saves and a dialect switch
resurrects, so it is the part that must stay legible. Record what you decided and why in the Stage 3
result section either way; deciding to leave it is a result, but leaving it undecided is not.

- [ ] **Step 5: Record the gate**

Append a "Stage 3 result" section to this plan recording what each criterion showed, then:

```bash
git add docs/superpowers/plans/2026-08-30-playground-stage3-presets-compare.md
git commit -m "docs(playground): record the stage 3 gate"
```

---

## Notes for whoever picks this up

**The blob is opaque on the server and typed on the client, and both halves are load-bearing.** The server never decodes it, so a field the console learns before the Go struct does still round-trips. The client never trusts it, because `chatBody` calls `.split` and `.trim` on these values without checking and a wrong-typed one is a crash. Task 2 tests the first from the server side; Task 3 tests the second from the client side. Neither test can see the other half.

**`presets` already means something else.** `keys.presets` and `usePresets` are the provider catalogue shipped with the binary and cached forever. Every client symbol this plan adds says `playgroundPresets`. A reviewer should treat a bare `presets` in new client code as a defect.

**The name clash is a feature, not an error path.** The 409 carries the existing preset's id precisely so the dialog can offer to overwrite. A handler that returned a bare error, or a client that toasted it, would make saving over a preset impossible without deleting it first.

**Compare's shared body is the whole point of the screen.** If a future change gives a column its own system prompt or sampling, the transcripts stop being comparable and the screen stops answering the question it exists for.
