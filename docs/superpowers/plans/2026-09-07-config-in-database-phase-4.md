# Configuration in the Database — Phase 4 (documentation) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Make the documentation true again, and make the one list that has already drifted twice unable to drift silently a third time.

**Architecture:** No production code changes. Two documents carry statements that phases 2 and 3 falsified — `docs/design/configuration.md` and `docs/operations/deploy.md` — and one list inside the first is checkable against `config.RestartOnly`, so a test replaces the reader's diligence.

**Tech Stack:** Markdown, Go (one test).

**Spec:** `docs/superpowers/specs/2026-09-07-config-in-database-design.md` — §7's "Docs to rewrite" is this phase, and §8 names it phase 4.

**Phases 1–3:** merged. Phase 3's plan ends with a "What phase 4 inherits" section — read it; two of its items are tasks here.

## Global Constraints

- **Every statement must be true of the code as it stands.** This phase exists because statements outlived their code; replacing one stale claim with a fresh wrong one is the failure mode, not a lesser version of success. Check each replacement against the source, and cite what you checked.
- Keys keep their dotted block form: `catalog.sync_interval`.
- No production code changes. One new test file is the only Go this phase adds.
- English only.
- Commit style: `<type>(<scope>): <subject>`, imperative, no period. End every commit message with:

```
Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
```

## What is already true, and needs nothing

Checked before writing this plan, so nobody spends a task rediscovering it:

- `README.md:30` — already says there is no configuration file, and describes the bootstrap set correctly. Phase 1 updated it.
- `darkrouter.example.yaml` — already deleted.
- `docs/design/admin-api.md:103-104` — already says a restart-only field is accepted and the response names it. Phase 2 corrected it.
- `docs/design/console.md` — names Settings as a destination and says nothing about what it can edit. Nothing to correct.
- `tools/presetgen` — no dependency on `internal/config`, as §7 says.

---

### Task 1: Make `configuration.md` true

**Files:**
- Modify: `docs/design/configuration.md`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing. Prose only.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 0 - spec 1 - coupling 0 - risk 1 = 2
**Approach:** inline - skip 2: every correction below names the source that settles it

Seven statements in this document are false. Each is given with the code that disproves it; **read that code before writing the replacement** rather than trusting the summary.

- [ ] **Step 1: The `PUT` disposition**

Under "Reload versus restart":

> A **reload** that picks up a changed restart-only field *warns*: the value is
> already stored, and a warning is the only honest answer. A **`PUT` to
> the API** that names one is *refused*, because a request can be rejected
> before anything happens.

The refusal went in phase 2. `internal/admin/configapi.go`'s `commitConfig` answers `{"valid": true, "restart_required": [...]}` and `restartRequired` filters the written keys by `config.RestartOnly`. Replace the second sentence:

```markdown
A **reload** that picks up a changed restart-only field *warns*: the value is
already stored, and a warning is the only honest answer. A **`PUT` to the API**
that names one is *accepted*, and the response lists the written keys that take
effect on restart — the value belongs in the database either way, and refusing
it would leave no way to set it at all.
```

- [ ] **Step 2: The restart-only list, which is wrong in both directions**

The document lists 18 keys. `config.RestartOnly` holds 17, and the two sets differ by three entries: `server.proxy_listen` and `server.admin_listen` are documented but not on the list, and `catalog.seed_free_providers` is on the list but not documented.

The listen addresses left in phase 1, when they became environment-owned: a variable cannot change under a running process, so there is no reload that could warn about one. `catalog.seed_free_providers` joined in phase 1 for the opposite reason — it is consumed once at startup and a reload silently accepted it before.

Restructure the list as a fenced block, one key per line, so Task 3 can test it against the code:

````markdown
Restart-only, because each is captured once when something is constructed:

```
policy.timeout.connect
policy.timeout.first_byte
catalog.models_dev_url
catalog.sync_interval
catalog.sync_timeout
catalog.free_catalog_interval
catalog.free_catalog_url
catalog.free_catalog_sync
catalog.litellm_interval
catalog.litellm_url
catalog.litellm_sync
catalog.seed_free_providers
catalog.discovery.interval
catalog.discovery.timeout
catalog.discovery.concurrency
catalog.discovery.enabled
media.inline
```

That list is checked against `config.RestartOnly` by a test, because it has
drifted from the code twice.

The listen addresses are **not** on it. They come from the environment, and a
variable cannot change under a running process, so there is no reload that
could warn about one — the settings screen says `environment` instead.

`policy.timeout.connect` and `first_byte` are on it because they configure a
shared HTTP transport built once. `server.max_body_bytes` deliberately is
**not**: the executor reads it from a per-request snapshot.
````

Confirm the seventeen names and their order against the code before you commit — the fenced block must match `config.RestartOnly` exactly, because Task 3's test compares them as sets and reports any difference.

- [ ] **Step 3: The retry cap**

The keys table says:

> | `policy.retry.max_attempts` | 4 | The loader enforces only `>= 1`; the admin API additionally caps it at 10. |

Phase 2 moved the cap into the registry, so both paths enforce it — `internal/store/configreg.go`'s `maxRetryAttempts` and the `withValidate` entry for that key, run by `ApplyConfigRows` on the load path and by `WriteConfig` on the write path. Replace the note:

> | `policy.retry.max_attempts` | 4 | Between 1 and 10. The registry enforces it, so a save and a later load agree. |

- [ ] **Step 4: The `seed_free_providers` "known gap"**

The keys table says:

> | `catalog.seed_free_providers` | `true` | Consumed once at startup, but **not** on the restart-only list, so a reload accepts a change that cannot take effect. Known gap. |

It is on the list (`config.RestartOnly`, and Step 2's block). Replace the note:

> | `catalog.seed_free_providers` | `true` | Restart-only: the seed runs once at startup. Adds a provider for every hosted preset that needs no credential. |

Check "hosted preset that needs no credential" against `internal/catalog/preset.go`'s `SelfServing()` before you write it — that phrasing was settled in phase 3 against the same function.

- [ ] **Step 5: The `providers[]` row**

The keys table opens "Every key below is a row in `settings`" and then lists:

> | `providers[]` | — | id, kind, preset, base_url, api_key, priority, models. Overlaid from the database. |

`config.Config.Providers` was deleted in phase 2 — providers have their own tables and their own encryption and were never a `settings` row. The `aliases` row beneath it is in the same position: a table, not a settings key, overlaid onto the snapshot by `store.OverlayConfig`.

Delete the `providers[]` row. Move the `aliases` row out of the table and state both facts in a sentence after it, so the table's opening claim is true of every row it still contains:

```markdown
Providers and aliases are not in this table. Each has its own tables — providers
carry encrypted credentials, aliases are ordered chains — and `store.OverlayConfig`
merges the alias set onto every published snapshot. They are edited on the
Providers and Routing screens rather than in Settings.
```

- [ ] **Step 6: The settings-screen paragraph**

Under "Bootstrap variables":

> The settings screen shows the two listen addresses with an `environment`
> source and no reload badge: the API reports them as hot-reloadable, since
> nothing captures them at construction, but an environment variable cannot
> change under a running process and a badge saying otherwise would promise an
> edit that is impossible.

Phase 3 changed the handler: `internal/admin/configapi.go` now emits `HotReloadable: false` for the two bootstrap keys, for exactly the reason the old sentence gave. Replace it:

```markdown
The settings screen shows the two listen addresses read-only, with an
`environment` source badge and a chip naming the variable that owns each. The
API reports them as not hot-reloadable — nothing captures them at construction,
but a variable cannot change under a running process, so calling them hot would
promise an edit that is impossible.
```

- [ ] **Step 7: The closing sentence**

The document ends:

> There is no example file to keep in step with this table: every key here is a
> row in `settings`, written by the console where a write endpoint exists and
> left at its default where one does not.

Phase 3 gave every stored key an editor. Replace the clause after the colon:

```markdown
There is no example file to keep in step with this table: every key here is a
row in `settings`, edited on the Settings screen, and absent from the table in
the database until something writes it.
```

- [ ] **Step 8: Read the whole document once more**

The seven corrections above are the ones found by searching for known changes. Read the document start to finish against the code and report anything else that is no longer true. Do not fix what you find without telling me first — report it and I will rule.

- [ ] **Step 9: Commit**

```bash
git add docs/design/configuration.md
git commit -m "$(cat <<'EOF'
docs(config): correct what the phases changed

The PUT disposition, the retry cap, the restart-only list and the
providers row all describe behaviour that no longer exists.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 2: Make `deploy.md` true

**Files:**
- Modify: `docs/operations/deploy.md`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing. Prose only.

**Implementer:** dcc-superpower-companions:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 0 - risk 1 = 1
**Approach:** inline - skip 2: one paragraph, with its replacement given

- [ ] **Step 1: The read-only claim**

Under "Production":

> Settings are changed in the console. Policy and aliases have write endpoints
> today; the rest are shown read-only on the Settings screen, which says where
> each value came from and which need a restart. A `darkrouter.yaml` left over
> from an older deployment is ignored — the process warns about it at startup
> and reads nothing from it.

The second sentence stopped being true in phase 3: every stored setting has an editor. Replace the paragraph:

```markdown
Settings are changed in the console. The Settings screen edits every stored
setting, says where each value came from — the database, the environment, or a
compiled default — offers a reset to the default, and names the keys whose
change waits for a restart. A `darkrouter.yaml` left over from an older
deployment is ignored: the process warns about it at startup, in the log and on
the Settings screen, and reads nothing from it.
```

The clause about the warning reaching the screen is new and is true as of phase 3 — `cmd/darkrouter/main.go`'s `startupWarnings` feeds `server.New`, which feeds `admin.Deps.Warnings` and `/healthz`. Confirm that chain before you write it.

- [ ] **Step 2: Read the whole document once more**

Same instruction as Task 1's Step 8: read it against the code, report anything else stale, and do not fix beyond this step without telling me.

Pay particular attention to the "Local build (UAT)" section — it is the procedure this project's own work follows, so an error there costs every future session.

- [ ] **Step 3: Commit**

```bash
git add docs/operations/deploy.md
git commit -m "$(cat <<'EOF'
docs(deploy): the console edits every setting now

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 3: Pin the restart-only list to the code

**Files:**
- Create: `internal/config/docs_test.go`

**Interfaces:**
- Consumes: Task 1's fenced block in `docs/design/configuration.md`.
- Produces: nothing.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 0 - spec 0 - coupling 1 - risk 1 = 2
**Approach:** inline - skip 2: a file read and a set comparison, with the code given

The documented restart-only list has drifted from `config.RestartOnly` twice: once when the listen addresses left it, and once when `catalog.seed_free_providers` joined. Both times the document kept saying the old thing, and nothing noticed. This is the one part of the documentation a test can hold.

- [ ] **Step 1: Write the failing test**

Create `internal/config/docs_test.go`:

```go
package config

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The documented restart-only list has drifted from this one twice: once when
// the listen addresses left it and became environment-owned, and once when
// catalog.seed_free_providers joined it. Both times the document went on
// saying the old thing, because nothing compared them.
//
// This is the one list in the documentation a test can hold: it is exactly the
// contents of RestartOnly, and an operator reads it to decide whether a change
// needs a restart.
func TestDocumentedRestartOnlyListMatchesTheCode(t *testing.T) {
	const doc = "../../docs/design/configuration.md"
	raw, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("read %s: %v", doc, err)
	}

	documented, err := restartOnlyBlock(string(raw))
	if err != nil {
		t.Fatalf("%s: %v", doc, err)
	}

	want := slices.Clone(RestartOnly)
	slices.Sort(want)
	got := slices.Clone(documented)
	slices.Sort(got)

	for _, key := range got {
		if !slices.Contains(want, key) {
			t.Errorf("%s documents %s as restart-only; the code does not", doc, key)
		}
	}
	for _, key := range want {
		if !slices.Contains(got, key) {
			t.Errorf("%s is restart-only in the code and not documented in %s", key, doc)
		}
	}
}

// restartOnlyBlock reads the fenced list that follows the restart-only
// sentence. Anchored on that sentence rather than on "the first fence in the
// file", so an unrelated code block added above it cannot silently become the
// thing under test.
func restartOnlyBlock(md string) ([]string, error) {
	const anchor = "Restart-only, because each is captured once"
	i := strings.Index(md, anchor)
	if i < 0 {
		return nil, errors.New("the restart-only sentence is missing; this test cannot find its list")
	}
	fence := regexp.MustCompile("(?s)```\n(.*?)```")
	m := fence.FindStringSubmatch(md[i:])
	if m == nil {
		return nil, errors.New("no fenced block follows the restart-only sentence")
	}
	var keys []string
	for _, line := range strings.Split(m[1], "\n") {
		if line = strings.TrimSpace(line); line != "" {
			keys = append(keys, line)
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("the fenced block after the restart-only sentence is empty")
	}
	return keys, nil
}
```

Add `"errors"` to the imports.

- [ ] **Step 2: Run it to verify it passes against Task 1's list**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/config/ -run TestDocumentedRestartOnlyList -v
```

Expected: PASS. If it fails, Task 1's fenced block and `config.RestartOnly` disagree — read both and report which is wrong rather than editing whichever is easier.

- [ ] **Step 3: Prove it can fail, in both directions**

This test's whole value is catching drift, so prove it catches both kinds:

1. Delete one key from the fenced block in `docs/design/configuration.md`, run the test, confirm it reports that key as "restart-only in the code and not documented". Restore it.
2. Add a plausible but wrong key — `server.shutdown_grace` — to the fenced block, run the test, confirm it reports that key as documented but not in the code. Restore it.

Quote both.

- [ ] **Step 4: Run the package**

```bash
go test ./internal/config/ -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/config/docs_test.go
git commit -m "$(cat <<'EOF'
test(config): pin the documented restart-only list

It has drifted from the code twice, in both directions, and nothing
compared them.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 4: Sweep the rest of the documentation

**Files:**
- Modify: whatever the sweep finds, with a ruling first.

**Interfaces:**
- Consumes: Tasks 1 and 2.
- Produces: nothing.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 0 = 3
**Approach:** inline - skip 2: a search with a fixed vocabulary, and a report rather than an edit

Phases 1 to 3 changed enough that a claim can be stale in a document nobody thought to open. This task looks, and reports; it does not fix without a ruling.

- [ ] **Step 1: Search for the vocabulary of the old world**

```bash
grep -rniE "darkrouter\.yaml|config file|configuration file|yaml|read-only|refused|blocks|environment" docs/ README.md --include='*.md' | grep -v "docs/superpowers/"
```

`docs/superpowers/` is excluded deliberately: specs and plans are historical records of what was decided at the time, and correcting them would destroy the record. Everything else is a live document and is expected to be true today.

- [ ] **Step 2: Judge each hit**

For every hit, decide: true, false, or intentionally historical (an upgrade note describing what a past release did is not stale — it is why the note exists).

- [ ] **Step 3: Report before fixing**

Write what you found into your report as a table — file, line, the claim, and your verdict — and **stop**. Do not edit. I will rule on which to change, and you will apply that ruling in a fix round.

The reason for the stop: this sweep's vocabulary is deliberately broad and will hit many lines that are perfectly correct. Deciding which of those to rewrite is a judgement about what the documentation is for, not a mechanical fix.

- [ ] **Step 4: There is no commit in this task**

Unless the ruling in Step 3 produces edits, this task commits nothing. Say so in your report.

---

### Task 5: Verify

**Files:** none.

**Interfaces:**
- Consumes: every task above.
- Produces: nothing.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2
**Approach:** inline - skip 2: run the suites and read the two documents

This phase changes no behaviour, so there is nothing to deploy and nothing to look at in a browser. What it needs instead is a reader.

- [ ] **Step 1: The suites**

```bash
export PATH=$PATH:/usr/local/go/bin
go build ./... && go vet ./... && go test ./... -count=1
```

The console suite is untouched by this phase; run it once anyway to confirm that.

```bash
cd web && npm test -- --run && cd ..
```

- [ ] **Step 2: Read both documents end to end**

`docs/design/configuration.md` and `docs/operations/deploy.md`, as an operator would. Not looking for the corrections — those are reviewed — but for whether the documents still *read* as documents: whether a correction left a paragraph contradicting the one above it, a table with a dangling column, or a sentence that now says the same thing twice.

- [ ] **Step 3: Report**

State what you ran, what you read, and anything that reads wrong.

---

## Self-review

**Spec §7 coverage:**

| §7 requirement | Task |
|---|---|
| `docs/design/configuration.md` — precedence, keys table, hot/restart contract | 1 |
| `docs/operations/deploy.md` | 2 |
| `darkrouter.example.yaml` deleted | already done (phase 1) |
| `README.md` touched | already true (phase 1) |
| `presetgen` needs nothing | confirmed |

Carried from phase 3's handover: the retry cap note (Task 1 Step 3), and which bootstrap keys the console shows (Task 1 Step 6 states the two, and Step 2 says why the others are not on the restart-only list).

**Rule S:** every task has `files + spec + coupling <= 3` and `spec <= 2`; no task scores 3 on spec completeness.
