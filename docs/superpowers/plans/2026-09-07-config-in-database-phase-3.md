# Configuration in the Database — Phase 3 (console) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Make the settings screen edit every stored setting — typed from the registry, showing where each value came from, offering reset-to-default, and saying which written keys wait for a restart.

**Architecture:** The registry declares each key's kind, so the console picks an editor from the same table the loader and the write path use. `GET /api/config` stops serving a hand-written `blocks` tree and serves a flat `values` map built from the registry, which is why nine catalogue keys are invisible today and why they stop being invisible by construction. The screen holds one draft over every editable key, diffs it against the served values, and sends one `config.Patch` — so a save carries only what changed, rather than three keys it never touched.

**Tech Stack:** Go 1.x, React 19 + TypeScript, TanStack Query, darkraise-ui 6.7.0, Vitest.

**Spec:** `docs/superpowers/specs/2026-09-07-config-in-database-design.md` — §5 is this phase.

**Phases 1 and 2:** merged. `docs/superpowers/plans/2026-09-07-config-in-database-phase-2.md` ends with a "What phase 3 inherits" section — read it; two live console behaviours listed there are fixed by this plan.

## Global Constraints

- **Typography.** Never `text-xs`. Never a custom size — no `text-[11px]`, no `text-[length:var(…)]`, no `font-size` in a stylesheet. Only darkraise-ui's scale: `text-sm`, `text-base`, `text-lg`, `text-xl`, `text-2xl`, `text-3xl`. 14px (`text-sm`) is the floor, and hierarchy below body text comes from colour (`--legend`, `--muted-foreground`) and weight.
- **Never `npm install`.** `darkraise-ui` is pinned to exactly `6.7.0` because 6.8.0 drops the `.dr-sidebar-layout` rule and breaks the app shell. `npm ci` is safe; `npm install` is not.
- Keys keep their dotted block form: `catalog.sync_interval`.
- An absent row means the compiled default. Writing an empty value is a `DELETE`.
- `server.proxy_token` is never echoed by any endpoint and never writable.
- A restart-only key is accepted and the response names it; `restart_required` is never null.
- A bootstrap key is refused, naming the environment variable that owns it.
- `DARKROUTER_PROXY_TOKEN` must keep authenticating.
- Every test must be proven able to fail: break the implementation, watch it go red, restore it.
- English only, in code, comments, tests and rendered copy.
- Commit style: `<type>(<scope>): <subject>`, imperative, no period. End every commit message with:

```
Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
```

## Two decisions this plan makes that the spec does not

1. **`blocks` is replaced by a flat `values` map**, keyed by the registry's dotted keys, serialised the way the registry serialises them. `blocks` is a hand-written nested literal in `handleConfig`, and hand-maintenance is exactly why nine catalogue keys never reached the console. With `values` built from `store.ConfigRowsFor`, a key added to the registry appears on the screen with no second edit. `configFields` — the 25-key allowlist — goes with it: the registry is the allowlist, and `server.proxy_token` is not in the registry, so it cannot leak. The console is the only client, and both of its two `blocks` readers change in this plan.

2. **Every stored, non-bootstrap key gets an editor.** Spec §5 asks for editors typed from the registry with a set-versus-default indicator and reset on each row, which only reads sensibly if every row is editable. The alternative — a curated list — is what `EDITABLE` is today, and keeping it would mean this phase ships 30 keys of which 5 can be changed. The editors are generated from the kind, so this is less code than the curated list, not more.

## File structure

| File | Responsibility | Task |
|---|---|---|
| `internal/store/configreg.go` | Registry; each key declares its kind | 1 |
| `internal/admin/configapi.go` | `GET /api/config` serves `values` + typed field meta | 2 |
| `cmd/darkrouter/main.go` | Startup warnings reach the console, not only the log | 3 |
| `web/src/lib/api-types.ts` | The new response shape | 4 |
| `web/src/features/connect/connect-screen.tsx` | Reads `values`, not `blocks` | 4 |
| `web/src/features/settings/settings-catalog.ts` | Rows, formatting and copy from `values` | 5 |
| `web/src/features/settings/setting-field.tsx` | One editor per kind | 6 |
| `web/src/features/settings/settings-screen.tsx` | Draft, diff, save, reset, pending restart | 7, 8 |

---

### Task 1: The registry declares each key's kind

**Files:**
- Modify: `internal/store/configreg.go`
- Test: `internal/store/configreg_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `store.ConfigKind` (a string type with constants `KindDuration`, `KindBytes`, `KindInt`, `KindBool`, `KindString`, `KindURL`) and `store.ConfigKindOf(key string) (ConfigKind, bool)`. Task 2 serves it.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: the kind rides on the same `configField` table the validator was added to in phase 2, in the same shape

"Field editors typed from the registry" is only literally true if the registry says what type each key is. It already knows — `duration`, `integer64`, `boolean` and the rest are the closures that build each entry — but the knowledge dies inside them. This lifts it onto the field.

`bytes` is its own kind rather than an integer, because 33554432 is 32 MB and no operator reads it as that; the console formats and parses it differently. `url` is its own kind rather than a string for the same reason: `server.public_url` accepts a bare domain and normalises it, and the editor says so.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/configreg_test.go`:

```go
// The console picks an editor from this, so a key with no kind is a key that
// renders as a text box whatever it actually holds.
func TestEveryRegistryKeyDeclaresAKind(t *testing.T) {
	for _, key := range ConfigKeys() {
		kind, ok := ConfigKindOf(key)
		if !ok {
			t.Errorf("%s declares no kind", key)
			continue
		}
		switch kind {
		case KindDuration, KindBytes, KindInt, KindBool, KindString, KindURL:
		default:
			t.Errorf("%s declares unknown kind %q", key, kind)
		}
	}
}

func TestConfigKindOfNamesTheKindsThatDifferFromTheirGoType(t *testing.T) {
	// Bytes are an int64 in Go and a size to a person; the console formats and
	// parses them differently from a count.
	for key, want := range map[string]ConfigKind{
		"server.max_body_bytes":     KindBytes,
		"capture.max_bytes":         KindBytes,
		"server.sse.max_line_bytes": KindBytes,
		// A URL is a string in Go, but this one takes a bare domain and
		// normalises it, which the editor has to say.
		"server.public_url":         KindURL,
		"catalog.models_dev_url":    KindURL,
		"policy.timeout.total":      KindDuration,
		"policy.retry.max_attempts": KindInt,
		"capture.bodies":            KindBool,
		"catalog.discovery.enabled": KindBool,
	} {
		got, ok := ConfigKindOf(key)
		if !ok {
			t.Errorf("%s declares no kind", key)
			continue
		}
		if got != want {
			t.Errorf("%s kind = %q, want %q", key, got, want)
		}
	}
}

func TestConfigKindOfRejectsAnUnknownKey(t *testing.T) {
	if _, ok := ConfigKindOf("server.nonsense"); ok {
		t.Error("ConfigKindOf accepted a key the registry does not carry")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run TestConfigKind -run TestEveryRegistryKey -v
```

Expected: compile failure, `undefined: ConfigKindOf`.

- [ ] **Step 3: Add the kind to the registry**

In `internal/store/configreg.go`, above `type configField`:

```go
// ConfigKind is what a key holds, as the console needs to know it. It is
// coarser than the Go type in one direction and finer in another: bytes and a
// count are both int64 and are not the same control, while a URL and a name
// are both strings and are not either.
type ConfigKind string

const (
	KindDuration ConfigKind = "duration"
	KindBytes    ConfigKind = "bytes"
	KindInt      ConfigKind = "int"
	KindBool     ConfigKind = "bool"
	KindString   ConfigKind = "string"
	KindURL      ConfigKind = "url"
)
```

Add the field to `configField`:

```go
	// kind is what the console builds an editor from. It lives here rather
	// than being guessed from the key's suffix, because a guess is a second
	// table that drifts from this one.
	kind ConfigKind
```

Set it in each constructor closure inside `buildConfigRegistry`: `str` sets `kind: KindString`, `duration` sets `KindDuration`, `integer` and `optInt` set `KindInt`, `integer64` sets `KindBytes`, `boolean` and `optBool` set `KindBool`, and `domain` overrides its base to `KindURL`.

`integer64` is `KindBytes` rather than `KindInt` because all three of its keys are byte sizes — `server.max_body_bytes`, `capture.max_bytes` — and `server.sse.max_line_bytes` and `max_precommit_bytes` are `integer`, so those two need an explicit override. Add a `bytes` helper beside `domain`:

```go
	// bytes is integer for a value an operator reads as a size. The SSE limits
	// are declared int in Go and are still sizes to a person.
	bytes := func(key string, ref func(*config.Config) *int) configField {
		f := integer(key, ref)
		f.kind = KindBytes
		return f
	}
```

and use it for `server.sse.max_line_bytes` and `server.sse.max_precommit_bytes`.

For `catalog.models_dev_url`, `catalog.free_catalog_url` and `catalog.litellm_url`, wrap with a small helper the same way — they are `str` today and are URLs:

```go
	urlOf := func(key string, ref func(*config.Config) *string) configField {
		f := str(key, ref)
		f.kind = KindURL
		return f
	}
```

Then add the lookup below `ConfigKeyKnown`:

```go
// ConfigKindOf reports what a key holds. The console builds its editor from
// this, so it is the registry that decides, not a guess from the key's name.
func ConfigKindOf(key string) (ConfigKind, bool) {
	f, ok := configByKey[key]
	if !ok {
		return "", false
	}
	return f.kind, true
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/store/ ./internal/config/ ./internal/admin/ -count=1
```

- [ ] **Step 5: Prove the tests can fail**

Remove `kind: KindBytes` from the `bytes` helper, run `go test ./internal/store/ -run TestConfigKindOfNames`, confirm it goes red, restore it.

- [ ] **Step 6: Commit**

```bash
git add internal/store/configreg.go internal/store/configreg_test.go
git commit -m "$(cat <<'EOF'
feat(config-registry): declare what each key holds

The console picks an editor from the registry, so the registry has to
say whether a key is a duration, a size, a count or a URL.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 2: GET /api/config serves values from the registry

**Files:**
- Modify: `internal/admin/configapi.go`
- Test: `internal/admin/configapi_test.go`

**Interfaces:**
- Consumes: `store.ConfigKindOf` from Task 1; `store.ConfigKeys`, `store.ConfigRowsFor`, `store.StoredConfigKeys` as they are; `config.BootstrapVar` from phase 2.
- Produces: the response shape Tasks 4-8 consume:

```json
{
  "valid": true,
  "warnings": [],
  "values": { "policy.timeout.total": "10m0s", "…": "…" },
  "fields": {
    "policy.timeout.total": { "source": "database", "hot_reloadable": true, "kind": "duration" },
    "server.proxy_listen": { "source": "env", "hot_reloadable": false, "kind": "string", "env": "DARKROUTER_PROXY_LISTEN" }
  },
  "pending_restart": []
}
```

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: the handler keeps its shape and swaps what it builds the body from

`blocks` goes. It is a hand-written nested literal, and hand-maintenance is why `catalog.free_catalog_url`, `catalog.litellm_sync`, `catalog.seed_free_providers`, `catalog.discovery.timeout`, `catalog.discovery.concurrency` and four others have never reached the console — including one a stored row can change. `values` comes from `store.ConfigRowsFor(cfg)`, which walks the registry, so the next key added to the registry is on the screen with no second edit.

`configFields` goes for the same reason. The registry is the allowlist now, and it is a safer one: `server.proxy_token` is not a registry key, so it cannot appear even by mistake.

`sourceOf` returns `env` rather than `environment` (spec §5), and `databaseOwned` goes: with `blocks.aliases` gone there is no block whose source is claimed rather than read.

- [ ] **Step 1: Write the failing test**

In `internal/admin/configapi_test.go`, replace `TestConfigReturnsEveryBlock`, `TestConfigServesPublicURL`, `TestConfigServesAnEmptyPublicURLWhenUnset` and `TestConfigNamesTheSourceOfEachValue` with the block below. Keep `TestConfigNeverEchoesACredential`, `TestConfigReportsAStoredKeyAsComingFromTheDatabase`, `TestPublicURLIsHotReloadable`, `TestConfigMarksRestartOnlyFieldsAsCold`, `TestConfigIsNotValidWhenAKeyWasReverted` and `TestConfigReportsPendingRestartAcrossAnUnrelatedReload`, updating each to read `values` where it read `blocks` — read the file and adapt them rather than deleting them.

```go
// The registry is the allowlist. A key it carries reaches the screen with no
// second edit, which is the whole reason the hand-written block tree went:
// nine catalogue keys never reached the console because nobody added them
// twice.
func TestConfigServesEveryRegistryKey(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, _ := login(t, s)
	var body struct {
		Values map[string]string `json:"values"`
		Fields map[string]struct {
			Source        string `json:"source"`
			HotReloadable bool   `json:"hot_reloadable"`
			Kind          string `json:"kind"`
			Env           string `json:"env"`
		} `json:"fields"`
	}
	w := do(t, s, cookie, "", "GET", "/api/config", "")
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range store.ConfigKeys() {
		if _, ok := body.Values[key]; !ok {
			t.Errorf("values is missing %s", key)
		}
		f, ok := body.Fields[key]
		if !ok {
			t.Errorf("fields is missing %s", key)
			continue
		}
		if f.Kind == "" {
			t.Errorf("%s carries no kind", key)
		}
	}
	// The keys that were invisible before this change.
	for _, key := range []string{
		"catalog.free_catalog_url", "catalog.litellm_sync",
		"catalog.seed_free_providers", "catalog.discovery.timeout",
		"catalog.discovery.concurrency",
	} {
		if _, ok := body.Values[key]; !ok {
			t.Errorf("%s is still not served", key)
		}
	}
}

// The bootstrap keys are shown so an operator can see what the gateway is
// listening on, and named with their variable so it is obvious why the screen
// will not change them.
func TestConfigNamesTheVariableThatOwnsABootstrapKey(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, _ := login(t, s)
	var body struct {
		Fields map[string]struct {
			Source string `json:"source"`
			Env    string `json:"env"`
		} `json:"fields"`
	}
	w := do(t, s, cookie, "", "GET", "/api/config", "")
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	listen, ok := body.Fields["server.proxy_listen"]
	if !ok {
		t.Fatal("server.proxy_listen is not served")
	}
	if listen.Source != "env" {
		t.Errorf("source = %q, want env", listen.Source)
	}
	if listen.Env != "DARKROUTER_PROXY_LISTEN" {
		t.Errorf("env = %q, want the variable name", listen.Env)
	}
	// A stored key carries no variable, and a client must not print one.
	if stored := body.Fields["policy.timeout.total"]; stored.Env != "" {
		t.Errorf("policy.timeout.total names %q as its variable", stored.Env)
	}
}

// The registry is the allowlist, so the one key that must never be echoed is
// excluded by construction rather than by remembering to leave it out.
func TestConfigNeverServesTheProxyToken(t *testing.T) {
	s, _ := testServerFullWithConfig(t, func(c *config.Config) {
		c.Server.ProxyToken = "sekrit"
	})
	cookie, _ := login(t, s)
	w := do(t, s, cookie, "", "GET", "/api/config", "")
	if strings.Contains(w.Body.String(), "sekrit") {
		t.Errorf("the response carries the token: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "proxy_token") {
		t.Errorf("the response names the token key: %s", w.Body.String())
	}
}

// The values are the registry's own serialisation, which is what the write
// path parses back. A screen that displayed one spelling and submitted another
// would round-trip wrong on every save.
func TestConfigServesValuesInTheirStoredSpelling(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, _ := login(t, s)
	var body struct {
		Values map[string]string `json:"values"`
	}
	w := do(t, s, cookie, "", "GET", "/api/config", "")
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got := body.Values["policy.timeout.total"]; got != "10m0s" {
		t.Errorf("total = %q, want the Go duration spelling", got)
	}
	if got := body.Values["server.max_body_bytes"]; got != "33554432" {
		t.Errorf("max_body_bytes = %q, want the decimal count", got)
	}
	if got := body.Values["capture.bodies"]; got != "false" {
		t.Errorf("capture.bodies = %q, want a parseable bool", got)
	}
}
```

Note the `do` helper takes a CSRF token as its fourth argument; a GET passes `""`. Check its signature in `fixtures_test.go` and match it.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/admin/ -run TestConfig -v 2>&1 | head -40
```

Expected: FAIL — `values` is absent, so every lookup misses.

- [ ] **Step 3: Rewrite the handler body**

In `internal/admin/configapi.go`, delete `databaseOwned`, `bootstrapOwned` and `configFields`. Replace `fieldMeta` and `sourceOf`, and the body-building half of `handleConfig`:

```go
// fieldMeta annotates one setting with where it came from, whether a reload
// applies it, and what it holds. The console builds its editor from kind, so
// this is the whole of what a row needs beyond its value.
type fieldMeta struct {
	Source        string `json:"source"`
	HotReloadable bool   `json:"hot_reloadable"`
	Kind          string `json:"kind"`
	// Env names the variable that owns a bootstrap key, and is empty for a
	// stored one. It is what lets the screen say why a row cannot be edited
	// instead of merely disabling it.
	Env string `json:"env,omitempty"`
}

// bootstrapShown are the environment-owned keys the settings screen displays.
// They are not registry keys and never will be -- a value needed to reach the
// console cannot live in the database the console writes -- but an operator
// still needs to see what the gateway is listening on.
var bootstrapShown = []string{"server.proxy_listen", "server.admin_listen"}

// sourceOf says where one value came from. stored names the registry keys the
// database carries; a key absent from it is on its compiled default, which is
// the distinction the settings screen exists to show.
func sourceOf(field string, stored map[string]bool) string {
	if _, ok := config.BootstrapVar(field); ok {
		return "env"
	}
	if stored[field] {
		return "database"
	}
	return "default"
}
```

Then, in `handleConfig`, replace everything from `fields := make(...)` through the end of the `body` literal:

```go
	// Values and fields both come from the registry, so a key added there is on
	// the screen with no second edit. The hand-written block tree this replaces
	// is why nine catalogue keys were never visible.
	values := store.ConfigRowsFor(cfg)
	fields := make(map[string]fieldMeta, len(values)+len(bootstrapShown))
	for key := range values {
		kind, _ := store.ConfigKindOf(key)
		fields[key] = fieldMeta{
			Source:        sourceOf(key, stored),
			HotReloadable: !slices.Contains(config.RestartOnly, key),
			Kind:          string(kind),
		}
	}
	for _, key := range bootstrapShown {
		name, _ := config.BootstrapVar(key)
		fields[key] = fieldMeta{
			Source: "env",
			// An environment value is technically hot -- nothing captures it at
			// construction -- but a variable cannot change under a running
			// process, so calling it hot would promise a live edit that is
			// impossible.
			HotReloadable: false,
			Kind:          string(store.KindString),
			Env:           name,
		}
	}
	values["server.proxy_listen"] = cfg.Server.ProxyListen
	values["server.admin_listen"] = cfg.Server.AdminListen

	pending := s.deps.Config.PendingRestart()
	if pending == nil {
		pending = []string{}
	}

	body := map[string]any{
		// The same expression /healthz keys config_valid on. A skipped key is
		// a default the operator did not choose, and the settings banner reads
		// this field: the two endpoints must not disagree about it.
		"valid":           cfgErr == nil && len(cfg.Skipped) == 0,
		"warnings":        append(append([]string{}, s.deps.Warnings...), cfg.Warnings...),
		"values":          values,
		"fields":          fields,
		"pending_restart": pending,
	}
	if cfgErr != nil {
		// Stated alongside the error, because a config that failed validation
		// is not a config that stopped serving: the previous one is still live.
		body["error"] = cfgErr.Error()
		body["serving"] = "the previous configuration is still serving"
	}
	writeJSON(w, http.StatusOK, body)
```

Update the `s.deps.Config == nil` early return at the top of the handler to the same shape:

```go
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": true, "warnings": []string{},
			"values": map[string]string{}, "fields": map[string]fieldMeta{},
			"pending_restart": []string{},
		})
```

`policyBlock` is still used by `handlePolicy` in `aliasapi.go`; leave it. If `handleConfig` was its only other caller, that is fine — it keeps one.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go build ./... && go test ./internal/admin/ ./internal/e2e/ -count=1
```

A compile error names a test still reading `blocks`; update it to read `values`.

- [ ] **Step 5: Prove the tests can fail**

Delete the `catalog.discovery.timeout` entry from the registry's returned slice in `internal/store/configreg.go`, run `go test ./internal/admin/ -run TestConfigServesEveryRegistryKey`, confirm it goes red on the "still not served" assertion, restore it.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/configapi.go internal/admin/configapi_test.go
git commit -m "$(cat <<'EOF'
feat(config-api): serve every setting from the registry

The hand-written block tree kept nine catalogue keys off the console
because nobody added them twice. A flat map from the registry cannot.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 3: Startup warnings reach the console

**Files:**
- Modify: `cmd/darkrouter/main.go`
- Test: `cmd/darkrouter/main_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing named. The `warnings` slice passed to `server.New` now has a producer, so `/healthz`'s `warnings` and `GET /api/config`'s `warnings` carry the startup notices.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: the slice, the call site and both notices already exist; this connects them

This is the adjacent finding from phase 2, verified on the running UAT instance: `cmd/darkrouter/main.go` declares `var warnings []string`, passes it to `server.New`, and **nothing ever appends to it**. So `admin.Deps.Warnings`, `/healthz`'s `warnings` array and `GET /api/config`'s `warnings` carry only `cfg.Warnings`.

The two startup notices that exist — the leftover `darkrouter.yaml` and the ignored `-config` flag — go to `slog.Warn` and nowhere else. Spec §7 says "a leftover file is called out"; it is called out in the container log, not where an operator wondering why their settings reverted is looking.

Both notices are produced before `warnings` is declared, so this task returns them rather than only logging them.

- [ ] **Step 1: Write the failing test**

Read `cmd/darkrouter/main_test.go` first — it already tests `parseFlags` and `warnIgnoredConfigFlag`. Append:

```go
// The console reads this list. A warning that only reaches the log is one an
// operator working in the console never sees, which is the same as not
// warning at all for the person the notice is for.
func TestStartupWarningsNameALeftoverFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "darkrouter.yaml")
	if err := os.WriteFile(legacy, []byte("server: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := startupWarnings(filepath.Join(dir, "darkrouter.db"), "")
	if len(got) != 1 {
		t.Fatalf("warnings = %v, want one", got)
	}
	if !strings.Contains(got[0], "darkrouter.yaml") {
		t.Errorf("the warning does not name the file: %q", got[0])
	}
	if !strings.Contains(got[0], "no longer read") {
		t.Errorf("the warning does not say what changed: %q", got[0])
	}
}

func TestStartupWarningsAreEmptyWithNoLeftoverFile(t *testing.T) {
	dir := t.TempDir()
	if got := startupWarnings(filepath.Join(dir, "darkrouter.db"), ""); len(got) != 0 {
		t.Errorf("warnings = %v, want none", got)
	}
}

// The flag is accepted as a no-op for one release, and an operator whose
// entrypoint still passes it should be told in the console rather than only in
// the log they are not reading.
func TestStartupWarningsNameAnIgnoredConfigFlag(t *testing.T) {
	dir := t.TempDir()
	got := startupWarnings(filepath.Join(dir, "darkrouter.db"), "/etc/darkrouter/darkrouter.yaml")
	if len(got) != 1 {
		t.Fatalf("warnings = %v, want one", got)
	}
	if !strings.Contains(got[0], "/etc/darkrouter/darkrouter.yaml") {
		t.Errorf("the warning does not name the flag's value: %q", got[0])
	}
}
```

Add `os`, `path/filepath` and `strings` to the test file's imports if they are not there.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./cmd/darkrouter/ -run TestStartupWarnings -v
```

Expected: compile failure, `undefined: startupWarnings`.

- [ ] **Step 3: Extract the notices into a function that returns them**

In `cmd/darkrouter/main.go`, read the existing leftover-file block and `warnIgnoredConfigFlag`, then replace both with one function that returns the warnings and logs them:

```go
// startupWarnings are the notices that explain state the stored settings
// cannot: a file the process no longer reads, and a flag it now ignores.
//
// Returned rather than only logged. They reach admin.Deps.Warnings, and from
// there /healthz and the settings screen — which is where an operator asking
// why their settings reverted is looking, rather than in the container log.
func startupWarnings(dbPath, legacyConfig string) []string {
	var out []string

	// The file stopped being read in this release. Saying so once is what
	// turns "my settings reverted" into an obvious morning rather than a
	// confusing one.
	legacy := filepath.Join(filepath.Dir(dbPath), "darkrouter.yaml")
	// dbPath is commonly a bare filename, which would otherwise name a path
	// with no directory -- useless to an operator reading docker logs on a
	// host they did not set up themselves.
	if abs, err := filepath.Abs(legacy); err == nil {
		legacy = abs
	}
	if _, err := os.Stat(legacy); err == nil {
		out = append(out, fmt.Sprintf(
			"a configuration file is present at %s but no longer read; settings now live in the database and are changed in the console",
			legacy))
	}

	if legacyConfig != "" {
		out = append(out, fmt.Sprintf(
			"-config %s was ignored; configuration now lives in the database",
			legacyConfig))
	}
	return out
}
```

At the call site, replace the old leftover-file block and the `warnIgnoredConfigFlag(legacyConfig)` call with nothing, and replace `var warnings []string` with:

```go
	warnings := startupWarnings(dbPath, legacyConfig)
	for _, w := range warnings {
		slog.Warn("startup warning", "warning", w)
	}
```

Delete `warnIgnoredConfigFlag` and any test that tested it directly, replacing that coverage with the tests above. If a test asserts on the exact old log message, update it to the new one and say so in your report.

Check where `warnings` is declared relative to `dbPath` and `legacyConfig` — it must come after both are known. If the existing `var warnings []string` sits earlier than that, move the assignment down rather than moving the variable up.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go build ./... && go test ./cmd/... ./internal/server/ ./internal/admin/ -count=1
```

- [ ] **Step 5: Prove the tests can fail**

Make `startupWarnings` return `nil` unconditionally, run `go test ./cmd/darkrouter/ -run TestStartupWarningsNameALeftoverFile`, confirm it goes red, restore it.

- [ ] **Step 6: Commit**

```bash
git add cmd/darkrouter/
git commit -m "$(cat <<'EOF'
fix(startup): surface startup warnings in the console

The warnings slice had no producer, so the leftover-file notice and
the ignored -config flag reached the log and nowhere an operator looks.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 4: Console types and the Connect screen follow the new shape

**Files:**
- Modify: `web/src/lib/api-types.ts`
- Modify: `web/src/features/connect/connect-screen.tsx`
- Test: `web/src/features/connect/connect-render.test.tsx`

**Interfaces:**
- Consumes: Task 2's response shape.
- Produces: the types Tasks 5-8 consume:

```ts
export type ConfigSource = "env" | "database" | "default"
export type ConfigKind = "duration" | "bytes" | "int" | "bool" | "string" | "url"
export type ConfigFieldMeta = {
  source: ConfigSource
  hot_reloadable: boolean
  kind: ConfigKind
  /** The variable that owns a bootstrap key; absent for a stored one. */
  env?: string
}
export type ConfigResponse = {
  valid: boolean
  warnings: string[]
  values: Record<string, string>
  fields: Record<string, ConfigFieldMeta>
  pending_restart: string[]
  error?: string
  serving?: string
}
```

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: a type change and a one-line read, both mechanical once the shape is fixed

`ConfigBlocks` and `PolicyBlock`'s use inside `ConfigResponse` go. `PolicyBlock` itself stays — `/api/policy` still serves it and `usePolicy` still reads it, until Task 7 removes that dependency.

- [ ] **Step 1: Write the failing test**

`connect-screen.tsx:189` reads `config.data?.blocks.server`. Find the assertion in `connect-render.test.tsx` that supplies a `blocks` fixture and change it to `values`. Add:

```ts
it("reads the public URL from the flat values map", () => {
  // The block tree is gone; a screen still walking it would render the LAN
  // address as though no public URL were set, which is the one state this
  // page exists to distinguish.
  const cfg = {
    valid: true,
    warnings: [],
    values: { "server.public_url": "https://llm.example.test" },
    fields: {},
    pending_restart: [],
  }
  expect(publicOrigin(cfg)).toBe("https://llm.example.test")
})
```

Read the file first: if `connect-screen.tsx` has no exported helper to test this way, export the smallest one that does the read — a `publicOrigin(cfg)` returning the string or `""` — and test that. Do not restructure the screen beyond extracting that read.

- [ ] **Step 2: Run to verify it fails**

```bash
cd web && npx vitest run src/features/connect/ 2>&1 | tail -20
```

Expected: FAIL — the fixture no longer matches what the screen reads.

- [ ] **Step 3: Update the types**

In `web/src/lib/api-types.ts`, replace the `// --- config ---` section's `ConfigSource`, `ConfigFieldMeta`, `ConfigBlocks` and `ConfigResponse` with the shapes in Interfaces above. Delete `ConfigBlocks` entirely. Update `ConfigSource`'s doc comment — it currently says "`database` means editing the YAML has no effect", and there is no YAML:

```ts
/** Where a value came from. `env` is read at startup and needs a restart;
 *  `database` is what the console writes; `default` is not set anywhere. */
export type ConfigSource = "env" | "database" | "default"
```

- [ ] **Step 4: Update the Connect screen**

Replace the `blocks.server` read with a `values` read. Keep the behaviour identical: an absent or empty `server.public_url` means no public origin.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd web && npm run lint && npm test -- --run 2>&1 | tail -6
```

Other test files supply `blocks` fixtures and will fail to type-check or assert. Update each fixture to `values`; do not change what any test asserts. Report any test whose assertion you had to change and why.

- [ ] **Step 6: Prove the test can fail**

Make the public-URL read return `""` unconditionally, run the connect tests, confirm red, restore.

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/api-types.ts web/src/features/connect/
git commit -m "$(cat <<'EOF'
refactor(console): read config from the flat values map

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 5: The catalog builds rows from values

**Files:**
- Modify: `web/src/features/settings/settings-catalog.ts`
- Test: `web/src/features/settings/settings-catalog.test.ts`

**Interfaces:**
- Consumes: `ConfigResponse`, `ConfigFieldMeta`, `ConfigKind` from Task 4.
- Produces:

```ts
export type SettingRow = {
  field: string
  meta: SettingMeta
  /** The stored spelling, which is what a save submits. */
  value: string
  /** The same value as a person reads it: 720h0m0s as "30 days". */
  display: string
  source: ConfigSource
  hotReloadable: boolean
  kind: ConfigKind
  /** The variable that owns a bootstrap key, or "". */
  env: string
  editable: boolean
}
export function settingGroups(cfg: ConfigResponse): { group: Group; rows: SettingRow[] }[]
export function displayOf(value: string, kind: ConfigKind): string
export function parseBytes(text: string): number | undefined
```

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: `settingRow` keeps its job and changes where it reads from

`raw()` and its `cfg.blocks` walk go. `EDITABLE` goes — every stored key is editable now, and `editable` on the row is `source !== "env"`.

`SOURCE_NOTE` and `SOURCE_LABEL` re-key from `environment` to `env` and lose the file-era wording. The server group's blurb — "The listen addresses come from the environment and take a restart to change; every other key here applies on reload" — is already wrong for nothing now, but check it against `RestartOnly` and fix it if a server key in it is restart-only.

Add `SETTINGS` entries for the nine keys that were never served, so they arrive named rather than as bare dotted keys: `catalog.free_catalog_url`, `catalog.free_catalog_interval`, `catalog.free_catalog_sync`, `catalog.litellm_url`, `catalog.litellm_interval`, `catalog.litellm_sync`, `catalog.seed_free_providers`, `catalog.discovery.timeout`, `catalog.discovery.concurrency`. Write each `name` and `description` the way the existing entries are written — what it does for an operator, not what the field is called. All nine are `group: "catalogue"`.

- [ ] **Step 1: Write the failing test**

Read the existing test file and keep every test whose subject survives (`formatBytes`, `formatDuration`, grouping, the unnamed-field fallback), adapting fixtures from `blocks` to `values`. Add:

```ts
describe("settingRow", () => {
  it("carries the stored spelling and a readable one", () => {
    const cfg = cfgWith({ "policy.timeout.total": "720h0m0s" }, {
      "policy.timeout.total": { source: "database", hot_reloadable: true, kind: "duration" },
    })
    const row = settingGroups(cfg).flatMap((s) => s.rows).find((r) => r.field === "policy.timeout.total")
    // Both, because the save submits one and the operator reads the other.
    expect(row?.value).toBe("720h0m0s")
    expect(row?.display).toBe("30 days")
  })

  it("marks an environment row as not editable and names its variable", () => {
    const cfg = cfgWith({ "server.proxy_listen": ":18080" }, {
      "server.proxy_listen": {
        source: "env", hot_reloadable: false, kind: "string",
        env: "DARKROUTER_PROXY_LISTEN",
      },
    })
    const row = settingGroups(cfg).flatMap((s) => s.rows).find((r) => r.field === "server.proxy_listen")
    expect(row?.editable).toBe(false)
    expect(row?.env).toBe("DARKROUTER_PROXY_LISTEN")
  })

  it("marks a stored row as editable", () => {
    const cfg = cfgWith({ "log.retention": "720h0m0s" }, {
      "log.retention": { source: "database", hot_reloadable: true, kind: "duration" },
    })
    const row = settingGroups(cfg).flatMap((s) => s.rows).find((r) => r.field === "log.retention")
    expect(row?.editable).toBe(true)
  })

  it("names the nine catalogue keys that used to arrive unnamed", () => {
    for (const field of [
      "catalog.free_catalog_url", "catalog.free_catalog_interval",
      "catalog.free_catalog_sync", "catalog.litellm_url",
      "catalog.litellm_interval", "catalog.litellm_sync",
      "catalog.seed_free_providers", "catalog.discovery.timeout",
      "catalog.discovery.concurrency",
    ]) {
      // A key with no entry falls back to printing itself, which is the state
      // this fixes: the console served them as bare dotted keys or not at all.
      expect(SETTINGS[field]?.name, field).toBeTruthy()
      expect(SETTINGS[field]?.name, field).not.toBe(field)
    }
  })
})

describe("displayOf", () => {
  it("reads a size at the scale it was written in", () => {
    expect(displayOf("33554432", "bytes")).toBe("32 MB")
  })
  it("reads a bool as on or off", () => {
    expect(displayOf("true", "bool")).toBe("On")
    expect(displayOf("false", "bool")).toBe("Off")
  })
  it("leaves a string alone", () => {
    expect(displayOf("https://models.dev/api.json", "url")).toBe("https://models.dev/api.json")
  })
})

describe("parseBytes", () => {
  it("accepts what displayOf produces, so a round trip holds", () => {
    expect(parseBytes("32 MB")).toBe(33554432)
    expect(parseBytes("32MB")).toBe(33554432)
    expect(parseBytes("33554432")).toBe(33554432)
  })
  it("refuses what is not a size", () => {
    expect(parseBytes("")).toBeUndefined()
    expect(parseBytes("many")).toBeUndefined()
    expect(parseBytes("-1")).toBeUndefined()
  })
})
```

Write a `cfgWith(values, fields)` helper at the top of the describe block that fills in `valid: true, warnings: [], pending_restart: []`.

- [ ] **Step 2: Run to verify it fails**

```bash
cd web && npx vitest run src/features/settings/settings-catalog.test.ts 2>&1 | tail -20
```

- [ ] **Step 3: Rewrite the row builder**

Delete `raw()`. `settingRow` reads `cfg.values[field]` and `cfg.fields[field]`:

```ts
/** One setting, formatted for reading and for writing back. */
export function settingRow(
  cfg: ConfigResponse,
  field: string,
  meta: SettingMeta,
): SettingRow {
  const fieldMeta = cfg.fields[field]
  const value = cfg.values[field] ?? ""
  const source = fieldMeta?.source ?? "default"
  const kind = fieldMeta?.kind ?? "string"
  return {
    field,
    meta,
    value,
    display: displayOf(value, kind),
    source,
    hotReloadable: fieldMeta?.hot_reloadable ?? false,
    kind,
    env: fieldMeta?.env ?? "",
    // The environment owns its keys and the console cannot write them. Every
    // other key is a registry row, and the write path takes all of them.
    editable: source !== "env",
  }
}
```

Add `displayOf` and `parseBytes`:

```ts
/** The stored spelling as a person reads it. The row keeps both: one is what
 *  the operator reads, the other is what a save submits. */
export function displayOf(value: string, kind: ConfigKind): string {
  if (value === "") return "—"
  switch (kind) {
    case "duration":
      return formatDuration(value)
    case "bytes": {
      const n = Number(value)
      return Number.isFinite(n) ? formatBytes(n) : value
    }
    case "bool":
      return value === "true" ? "On" : "Off"
    default:
      return value
  }
}

const BYTE_UNITS: Record<string, number> = {
  b: 1, kb: 1024, mb: 1024 ** 2, gb: 1024 ** 3,
}

/**
 * A size an operator typed, as bytes.
 *
 * It accepts what `formatBytes` produces, so what the screen shows can be
 * edited in place and submitted unchanged. A bare number is bytes, which is
 * what the store holds.
 */
export function parseBytes(text: string): number | undefined {
  const m = /^\s*(\d+(?:\.\d+)?)\s*([a-zA-Z]*)\s*$/.exec(text)
  if (!m) return undefined
  const n = Number(m[1])
  const unit = (m[2] ?? "").toLowerCase()
  if (!Number.isFinite(n) || n < 0) return undefined
  if (unit === "" || unit === "bytes") return Math.round(n)
  const scale = BYTE_UNITS[unit]
  if (scale === undefined) return undefined
  return Math.round(n * scale)
}
```

Update `settingGroups` to iterate `cfg.fields` rather than `cfg.blocks`, keeping the "a field the API sends that has no entry here is not dropped" behaviour and the `aliases` exclusion (there is no `aliases` field any more, so that filter can go — confirm against Task 2's response before removing it).

Replace `SOURCE_NOTE` and `SOURCE_LABEL`:

```ts
export const SOURCE_NOTE = {
  env: "Read from the environment at startup; a restart applies a change",
  database: "Stored in the database, where the console reads and writes it",
  default: "Not set anywhere; this is the built-in default",
} as const

export const SOURCE_LABEL = {
  env: "environment",
  database: "database",
  default: "default",
} as const
```

The label stays the full word — it is what an operator reads — while the key matches the wire.

Delete `EditableSetting` and `EDITABLE`.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd web && npx vitest run src/features/settings/settings-catalog.test.ts
```

`settings-screen.tsx` will not compile until Task 7. That is expected; do not fix it here beyond what the type change forces, and say so in your report.

- [ ] **Step 5: Prove the tests can fail**

Change `editable` to `true` unconditionally, run the catalog tests, confirm the environment row test goes red, restore it.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/settings/settings-catalog.ts web/src/features/settings/settings-catalog.test.ts
git commit -m "$(cat <<'EOF'
refactor(settings): build rows from the values map

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 6: One editor per kind

**Files:**
- Create: `web/src/features/settings/setting-field.tsx`
- Test: `web/src/features/settings/setting-field.test.tsx`

**Interfaces:**
- Consumes: `SettingRow` from Task 5; `NumberBox` from `../shell/number-box`; `Input`, `Switch`, `Label`, `Badge`, `Button` from `darkraise-ui`.
- Produces:

```tsx
export function SettingField(props: {
  row: SettingRow
  /** The draft value, in the stored spelling. */
  value: string
  onChange: (next: string) => void
  /** Null when the key is on its default and there is nothing to reset. */
  onReset: (() => void) | null
  /** The server's complaint about this key from the last refused save. */
  error?: string
}): JSX.Element
```

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: the row layout follows the existing `SettingField` and `ReadOnlySetting`, and the controls are the ones the console already uses

Every editor holds and emits the **stored spelling** — `"10m0s"`, `"33554432"`, `"true"` — so what the screen submits is what the store parses. A bytes field is the one that formats on display, because nobody reads 33554432 as 32 MB; it round-trips through `parseBytes`.

An environment row renders read-only with an *env* chip naming its variable, and no reset.

- [ ] **Step 1: Write the failing test**

Create `web/src/features/settings/setting-field.test.tsx`. Use the repo's existing component-test setup — read `change-password-dialog.test.tsx` for how a component test is bootstrapped here, and follow it.

```tsx
import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { SettingField } from "./setting-field"
import type { SettingRow } from "./settings-catalog"

function row(over: Partial<SettingRow> = {}): SettingRow {
  return {
    field: "log.retention",
    meta: { name: "Keep request records for", description: "", group: "logging" },
    value: "720h0m0s",
    display: "30 days",
    source: "database",
    hotReloadable: true,
    kind: "duration",
    env: "",
    editable: true,
    ...over,
  }
}

describe("SettingField", () => {
  it("edits a duration in its stored spelling", async () => {
    const onChange = vi.fn()
    render(<SettingField row={row()} value="720h0m0s" onChange={onChange} onReset={null} />)
    const box = screen.getByLabelText("Keep request records for")
    await userEvent.clear(box)
    await userEvent.type(box, "48h")
    // The store parses what the box holds, so the box holds what the store
    // parses. A field that emitted "2 days" would round-trip to a 400.
    expect(onChange).toHaveBeenLastCalledWith("48h")
  })

  it("renders a boolean as a switch and emits a parseable bool", async () => {
    const onChange = vi.fn()
    render(
      <SettingField
        row={row({ field: "capture.bodies", kind: "bool", value: "false", display: "Off",
          meta: { name: "Record request bodies", description: "", group: "logging" } })}
        value="false"
        onChange={onChange}
        onReset={null}
      />,
    )
    await userEvent.click(screen.getByRole("switch"))
    expect(onChange).toHaveBeenLastCalledWith("true")
  })

  it("shows a size at its scale and emits bytes", async () => {
    const onChange = vi.fn()
    render(
      <SettingField
        row={row({ field: "capture.max_bytes", kind: "bytes", value: "256000", display: "250 KB",
          meta: { name: "Largest body recorded", description: "", group: "logging" } })}
        value="256000"
        onChange={onChange}
        onReset={null}
      />,
    )
    const box = screen.getByLabelText("Largest body recorded")
    await userEvent.clear(box)
    await userEvent.type(box, "1 MB")
    expect(onChange).toHaveBeenLastCalledWith("1048576")
  })

  it("renders an environment row read-only, naming its variable", () => {
    render(
      <SettingField
        row={row({
          field: "server.proxy_listen", kind: "string", value: ":18080", display: ":18080",
          source: "env", env: "DARKROUTER_PROXY_LISTEN", editable: false, hotReloadable: false,
          meta: { name: "Gateway address", description: "", group: "server" },
        })}
        value=":18080"
        onChange={vi.fn()}
        onReset={null}
      />,
    )
    expect(screen.queryByRole("textbox")).toBeNull()
    // The variable is the whole reason the row cannot be edited, so it is on
    // the row rather than in a tooltip nobody opens.
    expect(screen.getByText(/DARKROUTER_PROXY_LISTEN/)).toBeTruthy()
  })

  it("offers reset only when the key is stored", async () => {
    const onReset = vi.fn()
    const { rerender } = render(
      <SettingField row={row({ source: "default" })} value="720h0m0s" onChange={vi.fn()} onReset={null} />,
    )
    expect(screen.queryByRole("button", { name: /reset/i })).toBeNull()

    rerender(
      <SettingField row={row({ source: "database" })} value="48h" onChange={vi.fn()} onReset={onReset} />,
    )
    await userEvent.click(screen.getByRole("button", { name: /reset/i }))
    expect(onReset).toHaveBeenCalled()
  })

  it("shows the server's complaint against the field it names", () => {
    render(
      <SettingField
        row={row()}
        value="1h"
        onChange={vi.fn()}
        onReset={null}
        error="log.retention must be at least 48h, got 1h"
      />,
    )
    expect(screen.getByText(/must be at least 48h/)).toBeTruthy()
  })
})
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd web && npx vitest run src/features/settings/setting-field.test.tsx 2>&1 | tail -20
```

Expected: the module does not exist.

- [ ] **Step 3: Write the component**

Create `web/src/features/settings/setting-field.tsx`. The layout follows the existing `SettingField` in `settings-screen.tsx` — name, description, dotted key in mono beneath — with the control on the right and the source and restart badges under it.

Requirements the tests pin, plus the ones they do not:

- **Every control's accessible name is the setting's name**, via `<Label htmlFor={row.field}>` for inputs and `aria-label` for the switch. `Switch` renders a `role="switch"` button, and a button takes its name from `aria-label` or its own subtree — never from an enclosing label. `sampling.tsx:81-90` has the working pattern; follow it.
- **`duration`, `string`, `url`** render `Input` holding the stored spelling. `url` gets `placeholder="llm.example.com"` and a description note that a bare domain is assumed https.
- **`int`** renders `NumberBox` with `step={1} precision={0}`, emitting the typed string.
- **`bytes`** renders `Input` and emits `String(parseBytes(text))`; when `parseBytes` returns undefined it emits the raw text so the server can refuse it and say why, rather than the screen silently dropping the keystroke.
  **Seeding is the subtle part.** `formatBytes` rounds to one decimal, so `33554433` shows as `"32.0 MB"` and parses back one byte short. Seeding the box from `row.display` would therefore rewrite a non-round stored value on a save the operator never touched. Seed from `row.display` **only when the round trip is exact** — `parseBytes(row.display) === Number(row.value)` — and from `row.value` otherwise, so the common case reads as "32 MB" and the odd one keeps its digits.
- **`bool`** renders `Switch`, emitting `"true"` / `"false"`.
- **A non-editable row** renders the value as text with the *env* chip naming `row.env`, and no reset.
- **Badges:** the source badge always, using `SOURCE_LABEL[row.source]` and `SOURCE_NOTE[row.source]` as its title. An environment row gets neither `hot` nor `restart`; every other row gets `hot` (green) or `restart` (secondary) from `row.hotReloadable`.
- **Reset** is a small ghost button labelled "Reset", rendered only when `onReset` is non-null. Its title says what it does: "Delete the stored row and fall back to the built-in default".
- **Error**, when present, renders under the control in `text-sm` with `text-[hsl(var(--destructive))]`.

Typography: `text-sm` floor, no custom sizes. The description and the key use the same classes the existing rows use (`text-sm text-[hsl(var(--muted-foreground))]` and `font-mono text-sm text-[hsl(var(--legend))]`).

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd web && npx vitest run src/features/settings/setting-field.test.tsx && npm run lint
```

- [ ] **Step 5: Prove the tests can fail**

Make the bytes editor emit the raw typed text unconditionally, run the tests, confirm the size test goes red, restore it.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/settings/setting-field.tsx web/src/features/settings/setting-field.test.tsx
git commit -m "$(cat <<'EOF'
feat(settings): add an editor per setting kind

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 7: The settings screen saves a patch

**Files:**
- Modify: `web/src/features/settings/settings-screen.tsx`
- Test: `web/src/features/settings/settings-screen.test.tsx`

**Interfaces:**
- Consumes: `settingGroups`, `SettingRow` from Task 5; `SettingField` from Task 6.
- Produces:

```ts
export type ConfigPatch = { set?: Record<string, string>; reset?: string[] }
export type SaveResult = { valid: boolean; restart_required?: string[]; error?: string; serving?: string }
export function settingsPatch(draft: Record<string, string>, reset: Set<string>, cfg: ConfigResponse): ConfigPatch
export function fieldErrors(message: string, fields: string[]): Record<string, string>
```

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: the draft, the dirty check and the sticky Save bar are the screen's existing shape, and `settingsPatch` mirrors `provider-settings-dialog.tsx:50`

`PolicySettings`, `toDraft`, `toWrite`, `PolicyWrite`, `readOnlyGroups` and `ReadOnlySettings` all go. One list of rows, each editable or not.

**This fixes the two live behaviours phase 3 inherited.** A save sends only what the draft changed, so an untouched key is not written, and an emptied box is no longer sent as `""` for three keys nobody touched. Emptying a box still means reset — that is the spec's rule — but now it means it for the one key the operator emptied, and the row says so.

The screen stops using `usePolicy`: `/api/config` carries the policy values like any other. Leave `usePolicy` in `queries.ts` — `/api/policy` still exists and other code may read it — but if nothing else does, say so in your report and I will rule on removing it.

- [ ] **Step 1: Write the failing test**

Read the existing `settings-screen.test.tsx` and keep what survives: `reloadMessage`, `syncMessage`, `orderSessions` and the session/password tests are untouched by this task. Replace the `toDraft`/`toWrite`/`readOnlyGroups` tests with:

```ts
describe("settingsPatch", () => {
  const cfg = cfgWith(
    { "log.retention": "720h0m0s", "capture.bodies": "false", "policy.timeout.total": "10m0s" },
    {
      "log.retention": { source: "default", hot_reloadable: true, kind: "duration" },
      "capture.bodies": { source: "default", hot_reloadable: true, kind: "bool" },
      "policy.timeout.total": { source: "database", hot_reloadable: true, kind: "duration" },
    },
  )

  it("sends nothing when nothing changed", () => {
    expect(settingsPatch({ "log.retention": "720h0m0s" }, new Set(), cfg)).toEqual({})
  })

  it("sends only the keys the draft changed", () => {
    // The screen used to write three policy keys on every save whatever the
    // operator touched, which reported them all as stored until the next
    // restart reconciled them away.
    const draft = { "log.retention": "96h", "capture.bodies": "false", "policy.timeout.total": "10m0s" }
    expect(settingsPatch(draft, new Set(), cfg)).toEqual({ set: { "log.retention": "96h" } })
  })

  it("sends an emptied box as a reset for that key alone", () => {
    const draft = { "policy.timeout.total": "" }
    expect(settingsPatch(draft, new Set(), cfg)).toEqual({ reset: ["policy.timeout.total"] })
  })

  it("sends an explicit reset even when the box still holds the value", () => {
    expect(settingsPatch({}, new Set(["policy.timeout.total"]), cfg)).toEqual({
      reset: ["policy.timeout.total"],
    })
  })

  it("never resets a key that has no stored row", () => {
    // log.retention is on its default; resetting it would be a no-op the
    // answer would still report as a restart-pending write.
    expect(settingsPatch({}, new Set(["log.retention"]), cfg)).toEqual({})
  })
})

describe("fieldErrors", () => {
  it("attaches a refusal to the key it names", () => {
    expect(fieldErrors("log.retention must be at least 48h, got 1h", ["log.retention", "capture.bodies"]))
      .toEqual({ "log.retention": "log.retention must be at least 48h, got 1h" })
  })

  it("attaches a cross-key refusal to every key it names", () => {
    const msg =
      "[policy.timeout.total policy.timeout.connect policy.timeout.first_byte] broke the timeout budget rule (policy.timeout.total (5s) must be at least connect + first_byte (1m10s))"
    expect(fieldErrors(msg, ["policy.timeout.total", "policy.timeout.connect", "log.retention"]))
      .toEqual({ "policy.timeout.total": msg, "policy.timeout.connect": msg })
  })

  it("attaches nothing when the message names no key", () => {
    expect(fieldErrors("something went wrong", ["log.retention"])).toEqual({})
  })
})
```

Write `cfgWith` the same way Task 5's test file does; if it is exported there, import it rather than writing a second one.

- [ ] **Step 2: Run to verify it fails**

```bash
cd web && npx vitest run src/features/settings/settings-screen.test.tsx 2>&1 | tail -20
```

- [ ] **Step 3: Write the patch builder and the error mapper**

```tsx
export type ConfigPatch = { set?: Record<string, string>; reset?: string[] }

/**
 * The save, built from the draft.
 *
 * Only what changed. The previous screen sent three policy keys on every save
 * whatever the operator touched, so those three reported as stored until the
 * next start reconciled them back to default -- the flip retiring the old
 * policy write path was meant to end.
 *
 * An emptied box is a reset, because the store cannot hold "" as a value
 * distinct from absent. It resets the one key that was emptied.
 */
export function settingsPatch(
  draft: Record<string, string>,
  reset: Set<string>,
  cfg: ConfigResponse,
): ConfigPatch {
  const set: Record<string, string> = {}
  const clear: string[] = []

  for (const [field, next] of Object.entries(draft)) {
    const meta = cfg.fields[field]
    // An environment key is not ours to write, and a key the gateway does not
    // report is not one this build knows how to send.
    if (!meta || meta.source === "env") continue
    if (reset.has(field)) continue
    const current = cfg.values[field] ?? ""
    if (next.trim() === "") {
      // Only if there is a row to delete. Resetting a key already on its
      // default writes nothing and would still be named in restart_required.
      if (meta.source === "database") clear.push(field)
      continue
    }
    if (next !== current) set[field] = next
  }

  for (const field of reset) {
    const meta = cfg.fields[field]
    if (!meta || meta.source !== "database") continue
    if (!clear.includes(field)) clear.push(field)
  }

  const patch: ConfigPatch = {}
  if (Object.keys(set).length > 0) patch.set = set
  if (clear.length > 0) patch.reset = clear
  return patch
}

/**
 * The server's refusal, against the fields it names.
 *
 * One refusal can belong to several keys: a cross-key rule reverts its whole
 * set together and its message lists them, so the operator sees the complaint
 * on every field that has to move for it to pass rather than on one of them.
 */
export function fieldErrors(message: string, fields: string[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const field of fields) {
    if (message.includes(field)) out[field] = message
  }
  return out
}
```

- [ ] **Step 4: Rewrite the screen**

Replace `PolicySettings` and `ReadOnlySettings` with one `SettingsForm` holding:

- `draft: Record<string, string>` seeded from `cfg.values` for every editable field, reseeded when `cfg` changes (the existing `seededFrom` guard handles the reseed — keep it, and note why: Go normalises `"10m"` to `"10m0s"` on the way out, so a draft compared against an un-reseeded snapshot stays dirty forever).
- `reset: Set<string>`, cleared on a successful save.
- `errors: Record<string, string>` from the last refused save, cleared when the field changes or a save succeeds.
- One sticky Save bar, shown when `settingsPatch(...)` is non-empty, with Discard and Save. Keep the existing bar's markup and copy.
- The save mutation: `api.put<SaveResult>("/api/config", patch)`, invalidating `keys.config`. On `valid: true` with a non-empty `restart_required`, the success toast names those keys; on `valid: false`, the banner shape the reload already uses.
- A 400 arrives as an `ApiError`; read its message and run it through `fieldErrors` over the keys the patch touched, so the complaint lands on the row. Check how `ApiError` exposes the body — read `web/src/lib/api.ts` — and use whatever field carries the server's `error` string.

Group rendering stays as it is: `settingGroups(cfg)` per group card, each row a `<SettingField>`.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd web && npm run lint && npm test -- --run 2>&1 | tail -6
```

- [ ] **Step 6: Prove the tests can fail**

Make `settingsPatch` send every draft key rather than only the changed ones, run the screen tests, confirm "sends only the keys the draft changed" goes red, restore it.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/settings/settings-screen.tsx web/src/features/settings/settings-screen.test.tsx
git commit -m "$(cat <<'EOF'
feat(settings): edit every stored setting

One draft over every editable key, sending only what changed. The
policy-only form and its always-send shape are gone.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 8: The pending-restart notice

**Files:**
- Modify: `web/src/features/settings/settings-screen.tsx`
- Test: `web/src/features/settings/settings-screen.test.tsx`

**Interfaces:**
- Consumes: `ConfigResponse.pending_restart` from Task 4; the screen from Task 7.
- Produces: `export function pendingRestartMessage(fields: string[]): string`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: the banner follows the two the screen already renders

`pending_restart` has been served by `/healthz` and `GET /api/config` since phase 1 and rendered by nothing. It is boot-versus-current, so unlike the transient "changed; takes effect on restart" warning it survives the next unrelated save — which is the whole reason it exists.

- [ ] **Step 1: Write the failing test**

```ts
describe("pendingRestartMessage", () => {
  it("names one key", () => {
    expect(pendingRestartMessage(["catalog.sync_interval"])).toContain("catalog.sync_interval")
  })
  it("names several", () => {
    const msg = pendingRestartMessage(["catalog.sync_interval", "media.inline"])
    expect(msg).toContain("catalog.sync_interval")
    expect(msg).toContain("media.inline")
  })
})
```

Add a render test asserting the banner appears when `pending_restart` is non-empty and does not when it is empty. Follow the existing render tests in the file for how the screen is mounted with a config fixture.

- [ ] **Step 2: Run to verify it fails**

```bash
cd web && npx vitest run src/features/settings/settings-screen.test.tsx 2>&1 | tail -20
```

- [ ] **Step 3: Render the banner**

```tsx
/**
 * What is stored but not yet running.
 *
 * Measured against the snapshot this process booted on, not against the
 * previous reload, so it survives the next unrelated save. The transient
 * warning in `warnings` does not, and that is why this is a separate notice
 * rather than one more line in that list.
 */
export function pendingRestartMessage(fields: string[]): string {
  const list = fields.join(", ")
  return fields.length === 1
    ? `${list} is stored but the gateway is still running the value it started with.`
    : `${list} are stored but the gateway is still running the values it started with.`
}
```

Render it above the invalid-config banner, as a `Banner` without `variant="destructive"` — this is a state to act on, not a failure. Its heading says "Waiting for a restart".

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd web && npm run lint && npm test -- --run 2>&1 | tail -6
```

- [ ] **Step 5: Prove the tests can fail**

Make the banner render only when `pending_restart.length > 1`, run the screen tests, confirm the one-key render test goes red, restore it.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/settings/
git commit -m "$(cat <<'EOF'
feat(settings): show what waits for a restart

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 9: Verify against the running console

**Files:** none. This task changes nothing; it proves the phase.

**Interfaces:**
- Consumes: every task above.
- Produces: nothing.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2
**Approach:** inline - skip 2: the build and deploy procedure is written down in `docs/operations/deploy.md`

This phase is almost entirely rendered surface, so the suite proves less here than in either phase before it. CLAUDE.md requires looking at the screen, and in phase 2 the UI check was the only thing that caught two strings telling operators a capability did not exist.

**Whoever runs this: subagents on this machine cannot launch a browser (Chromium refuses to run as root). The controller session does the console check itself.**

- [ ] **Step 1: Full suite, both sides**

```bash
export PATH=$PATH:/usr/local/go/bin
go build ./... && go vet ./... && go test ./... -count=1
cd web && npm run lint && npm test -- --run && npm run build && cd ..
```

- [ ] **Step 2: Build and deploy**

Follow "Local build (UAT)" in `docs/operations/deploy.md`. The `compose.uat.yml` overlay is required or the published image is pulled over the local build. The admin port on this machine is 8091. Confirm the served bundle matches the source byte-for-byte with the `cmp` check that document gives.

- [ ] **Step 3: Look at the settings screen**

Log in (the password is in `.uat-credentials`; the login needs `Origin` and `Sec-Fetch-Site` headers or CSRF returns 403) and check:

- **Every stored setting has an editor**, including the nine catalogue keys that were invisible before — `catalog.free_catalog_url`, `catalog.litellm_sync`, `catalog.seed_free_providers`, `catalog.discovery.timeout`, `catalog.discovery.concurrency` and the rest. Each is named, not a bare dotted key.
- **The two listen addresses render read-only** with an *env* chip naming `DARKROUTER_PROXY_LISTEN` and `DARKROUTER_ADMIN_LISTEN`, and no reset control.
- **Source badges** read `database` for keys with a stored row and `default` for the rest. `server.public_url` is set on this machine, so it should read `database`.
- **Nothing shows `server.proxy_token`.**
- Typography: no text smaller than 14px anywhere on the screen.

- [ ] **Step 4: Exercise a save, a reset and a refusal**

- Change `log.retention` to `800h`, save. The toast reports success, the badge flips to `database`, and the value persists across a reload of the page.
- Reset it. The row returns to `720h0m0s` with a `default` badge.
- Set `policy.timeout.total` to `5s` and save. The save is refused, and the complaint — which names the rule and the numbers — appears against the timeout fields rather than only in a toast.
- Change a restart-only key (`catalog.sync_interval` to `6h`), save, and confirm the **Waiting for a restart** banner appears naming it. Reset it afterwards so the machine is left as found.

- [ ] **Step 5: Confirm the startup warning now reaches the screen**

`data/darkrouter.yaml` is still on disk. The settings screen's warnings card should now name it — that is Task 3's whole point, and before this phase it appeared only in `docker logs`. Check `/healthz` carries it too.

- [ ] **Step 6: Leave the machine as you found it**

`server.public_url` stays `http://192.168.0.250:8090`. Every other key you touched goes back to what it was.

- [ ] **Step 7: Report**

State what was checked and what was seen, with screenshots of the settings screen. If a screen is wrong, that is a finding for this phase.

---

## Self-review

**Spec §5 coverage:**

| Spec requirement | Task |
|---|---|
| Field editors typed from the registry | 1, 6 |
| duration, bytes, integer, boolean, string, URL | 1, 6 |
| Server-side validation surfaced inline | 7 |
| A set-versus-default indicator per row | 5, 6 |
| Reset-to-default | 6, 7 |
| Bootstrap fields read-only with an env chip naming the variable | 2, 6 |
| Restart-only fields keep their badge | 6 |
| "Takes effect on restart" from boot-versus-current | 8 |
| `ConfigSource` becomes `env \| database \| default` | 2, 4 |
| `SOURCE_NOTE`, `SOURCE_LABEL` and the file-era copy go | 5 |
| The server-group blurb corrected | 5 |
| Typography: `text-sm` floor, no custom sizes | 6, 9 |

Carried from phase 2: the nine invisible catalogue keys (2, 5), `pending_restart` rendered (8), `databaseOwned`/`sourceOf` retired (2), the emptied-duration reset scoped to one key (7), the three re-materialised default rows (7), and the `startupWarnings` producer (3).

**Type consistency:** `ConfigKind` is defined in Task 1 (Go) and Task 4 (TS) with the same six values. `SettingRow` is defined in Task 5 and consumed in Tasks 6-8. `ConfigPatch` matches the Go `config.Patch` JSON shape from phase 2 — `set`, `reset`, and the `aliases` field this screen never sends.

**Rule S:** every task has `files + spec + coupling <= 3` and `spec <= 2`; no task scores 3 on spec completeness.

---

## What phase 4 inherits

Recorded here rather than in the execution ledger, which is scratch and does
not survive the branch.

### Deferred to phase 4 or later, by design

- `log.level` and `log.format` are bootstrap-owned (spec §1 lists five such
  keys) but are not in `bootstrapShown`, so the settings screen displays only
  the two listen addresses. Reasonable, but phase 4's documentation should say
  which bootstrap keys the console shows and which it does not.
- `docs/design/configuration.md` still documents the retry cap as an
  admin-side rule; it has been a registry rule enforced on both paths since
  phase 2.

### Live behaviour worth a decision

- **A background refetch discards an in-progress draft.** `useConfig` has no
  `staleTime` and refetches on window focus. Structural sharing keeps the
  reference unless the answer actually changed, so a draft is lost only when
  another operator wrote, or when `warnings`/`pending_restart` changed. The
  guard — skip the reseed while the form is dirty and say the server changed
  underneath you — is a conflict-handling decision rather than a bug fix, so
  it was left for whoever owns that call.
- **An emptied box on a `default`-source row is a silent no-op.** There is no
  stored row to delete, so no badge appears, no patch is built and the Save
  bar does not arm; the box just sits empty. A chip reading "already the
  default" would close it.

### Findings outside this phase's scope

- `internal/store/configreg.go`'s `optBool.get` returns `"false"` for a nil
  pointer. That equals the effective value only because `applyDefaults` pins
  all six optional bools before anything reads them. If a future change left
  one nil, the console would show "Off" for a key that behaves as on, and
  `ReconcileConfig` would compare an operator's explicit `false` against the
  same `"false"` and delete the row. Worth a comment at that getter stating
  the dependency.
- `internal/catalog/merge.go`'s `priceSource` tolerates `override`, `litellm`
  and `registry` price stamps that nothing in production ever writes — dead
  tolerance, or a feature half-wired.
- Two stale comments still teach a contract this phase changed: the env-row
  comment in `web/src/features/settings/settings-screen.test.tsx` (around the
  "shows an environment value as a reading" test) says `hot_reloadable` is
  true for a listen address, which the handler stopped emitting; and
  `internal/store/configreg_test.go`'s "takes a bare domain and normalises it"
  comment lists `catalog.models_dev_url` alongside `server.public_url`, though
  only the latter normalises.
- `fieldErrors` in the settings screen matches a refusal to a key by substring.
  Safe today — no registry key contains another — and brittle if one ever does.
- Every 400 used to be toasted as well as shown inline; that is fixed, but the
  seam added for it (`quietError` on `useApiMutation`) is the only opt-out and
  is documented nowhere but its own comment.
