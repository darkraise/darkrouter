# Darkrouter Phase 3 — Routing and Failover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Turn the single-candidate request path into deterministic multi-candidate routing with real failover, credential rotation, and commit semantics — the phase that makes Darkrouter a gateway rather than a proxy.

**Architecture:** A pure `Resolve(Query, Snapshot) ([]Candidate, []Skip, error)` decides the ordered candidate sequence from frozen inputs — an evaluation instant, the provider set with credentials, a catalog reader, and health already resolved to availability booleans. `internal/exec` then drives an attempt loop over that fixed list, re-checking live availability per attempt, buffering pre-commit stream events so a failover is invisible to the client, and replaying them once a content-bearing event commits the response.

**Tech Stack:** Go 1.26 and the Phase 1–2 dependencies. No new modules.

**Spec:** `docs/superpowers/specs/2026-08-22-darkrouter-phase3-routing-failover.md` (master design: `docs/superpowers/specs/2026-08-22-darkrouter-design.md`)

## Global Constraints

- Go 1.26. Module path `github.com/darkraise/darkrouter`. `CGO_ENABLED=0` everywhere.
- English only in code, comments, commits, and errors.
- Commits are `<type>(<scope>): <subject>`, subject ≤50 chars, imperative, no period.
- **`Resolve` never reads a clock.** The evaluation instant arrives as `Snapshot.At`, and health arrives already resolved to booleans. Reading `time.Now()` inside the router destroys both purity and reproducibility, and the time-dependent cooling cases become untestable without fixtures.
- **Credentials rotate before providers.** Draining the second key on Groq comes before falling back to Cerebras — that is the point of holding several free-tier keys.
- **Routing is deterministic.** No weighting, no scoring, no learned behavior. Given a request and a health snapshot, the candidate sequence is predictable and explainable.
- The candidate list and every skip reason are persisted on the request row. Health tables are overwritten in place and cannot be replayed after the fact.
- Redirects are never followed. `CheckRedirect` returns `http.ErrUseLastResponse` and 3xx classifies as `RetryableProvider`.
- Outcome classification follows master design §8.1, which is authoritative. The **default buckets** matter as much as the listed codes: any unlisted transport error is `RetryableProvider`, any unlisted status ≥500 is `RetryableProvider`, any other unlisted 4xx is `Fatal`.
- Capability metadata does not exist until Phase 6. Every model's capabilities are `inferred`, and per master design §6.4 inferred capabilities **pass the filter with a warning**. Capability filtering is wired and tested here but admits everything.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/ir/ir.go` | Gains `Surface` — a leaf type both router and catalog need |
| `internal/catalog/catalog.go` | The narrow `Reader` interface Phase 6 implements, plus a provider-backed reader for now |
| `internal/router/model.go` | Model resolution: alias, `provider/model`, bare name |
| `internal/router/filter.go` | Candidate filtering and `Skip` reasons |
| `internal/router/order.go` | Credential ordering, least-recently-used with a deterministic tie-break |
| `internal/router/router.go` | `Resolve`, the types, and the distinguishable zero-candidate errors |
| `internal/health/availability.go` | A frozen, read-only availability snapshot and the credential LRU |
| `internal/exec/body.go` | Replayable inbound body, bounded by `server.max_body_bytes` |
| `internal/exec/deadline.go` | Per-attempt deadlines and the budget gate |
| `internal/exec/commit.go` | The bounded pre-commit event buffer and its replay |
| `internal/exec/exec.go` | The attempt loop |
| `internal/provider/provider.go` | `Provider` carries every credential, not one |

---

### Task 1: Aliases and the retry budget in configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/load.go` (`applyDefaults`, `validate`)
- Test: `internal/config/load_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `cfg.Aliases map[string][]string`, `config.RetryConfig{MaxAttempts int}` reachable as `cfg.Policy.Retry`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

An alias naming a provider that does not exist is **deliberately not validated
here**. Providers live in SQLite from Phase 2 on, so the config loader cannot
see them, and master design §7 is explicit that treating it as an error would
mean deleting a provider in the UI permanently breaks every future reload. The
router records it as a `Skip` instead, where it is visible per request.

`policy.retry` carries only `max_attempts` because outcome classification is
fixed rather than configurable.

- [x] **Step 1: Write the failing test**

Append to `internal/config/load_test.go`:

```go
func TestParseReadsAliases(t *testing.T) {
	body := minimal + `
aliases:
  fast:
    - groq/llama-3.3-70b
    - cerebras/llama-3.3-70b
  coding:
    - anthropic/claude-sonnet-4-5
`
	c, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Aliases) != 2 {
		t.Fatalf("got %d aliases, want 2", len(c.Aliases))
	}
	fast := c.Aliases["fast"]
	if len(fast) != 2 || fast[0] != "groq/llama-3.3-70b" {
		t.Errorf("fast = %v", fast)
	}
	// Order is the chain order and must survive the round trip.
	if fast[1] != "cerebras/llama-3.3-70b" {
		t.Errorf("alias order was not preserved: %v", fast)
	}
}

func TestParseDefaultsMaxAttempts(t *testing.T) {
	c, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Policy.Retry.MaxAttempts != 4 {
		t.Errorf("MaxAttempts = %d, want 4", c.Policy.Retry.MaxAttempts)
	}
}

func TestParseReadsMaxAttempts(t *testing.T) {
	body := minimal + "\npolicy:\n  retry: { max_attempts: 2 }\n"
	c, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Policy.Retry.MaxAttempts != 2 {
		t.Errorf("MaxAttempts = %d, want 2", c.Policy.Retry.MaxAttempts)
	}
}

// A written 0 is indistinguishable from an omitted key by the time validate
// runs, so it becomes the default 4. A negative value is the case that can be
// caught, and the one an operator might actually produce by arithmetic.
func TestParseRejectsNegativeMaxAttempts(t *testing.T) {
	body := minimal + "\npolicy:\n  retry: { max_attempts: -1 }\n"
	if _, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"})); err == nil {
		t.Fatal("expected a negative max_attempts to be rejected")
	}
}

func TestParseRejectsAnEmptyAlias(t *testing.T) {
	body := minimal + "\naliases:\n  broken: []\n"
	if _, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"})); err == nil {
		t.Fatal("expected an alias with no targets to be rejected")
	}
}

// A provider that does not exist is a warning, not an error: providers live in
// SQLite and the loader cannot see them.
func TestParseAcceptsAnAliasNamingAnUnknownProvider(t *testing.T) {
	body := minimal + "\naliases:\n  fast:\n    - nosuchprovider/model\n"
	if _, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"})); err != nil {
		t.Fatalf("an unknown provider in an alias must not fail the load: %v", err)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'Alias|MaxAttempts' -v`
Expected: FAIL — `c.Aliases undefined`, `c.Policy.Retry undefined`.

- [x] **Step 3: Add the types**

In `internal/config/config.go`, add `Aliases` to `Config` and `Retry` to
`PolicyConfig`:

```go
type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Providers []ProviderConfig `yaml:"providers"`
	// Aliases map a friendly name to an ordered fallback chain. Order is the
	// chain order, so a map of slices is the right shape: the values are
	// ordered even though the keys are not.
	Aliases map[string][]string `yaml:"aliases"`
	Policy  PolicyConfig        `yaml:"policy"`
	Log     LogConfig           `yaml:"log"`
	Capture CaptureConfig       `yaml:"capture"`

	// Warnings are non-fatal findings from validation. They are surfaced on
	// /healthz rather than rejecting the document.
	Warnings []string `yaml:"-"`
}

type PolicyConfig struct {
	Cooldown CooldownConfig `yaml:"cooldown"`
	Retry    RetryConfig    `yaml:"retry"`
	Timeout  TimeoutConfig  `yaml:"timeout"`
}

// RetryConfig carries only max_attempts: outcome classification is fixed
// rather than configurable, so there is nothing else to tune.
type RetryConfig struct {
	MaxAttempts int `yaml:"max_attempts"`
}
```

- [x] **Step 4: Add the default and validation**

In `applyDefaults` in `internal/config/load.go`, append before the closing brace:

```go
	if c.Policy.Retry.MaxAttempts == 0 {
		c.Policy.Retry.MaxAttempts = 4
	}
```

In `validate`, add to the block of policy checks at the top:

```go
	if c.Policy.Retry.MaxAttempts < 1 {
		return fmt.Errorf("policy.retry.max_attempts must be at least 1")
	}
	for name, targets := range c.Aliases {
		if name == "" {
			return fmt.Errorf("alias: name is required")
		}
		if len(targets) == 0 {
			return fmt.Errorf("alias %q: at least one target is required", name)
		}
		for _, tgt := range targets {
			if strings.TrimSpace(tgt) == "" {
				return fmt.Errorf("alias %q: target must not be empty", name)
			}
		}
	}
```

A written `max_attempts: 0` reaches `validate` as the default 4 and cannot be
rejected — unlike `trip_after`, which is a pointer for exactly that reason. That
is a deliberate asymmetry: `max_attempts: 0` means "make no attempts", which no
operator writes on purpose, so the pointer ceremony is not worth repeating. The
check above still catches a negative value.

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [x] **Step 6: Update the example configuration**

Add to `darkrouter.example.yaml`, above `policy:`:

```yaml
# Aliases map a friendly name to an ordered fallback chain. Targets are
# provider/model or a bare model name.
aliases:
  fast:
    - groq/openai/gpt-oss-120b
```

and add `retry` inside the existing `policy:` block:

```yaml
  retry:
    max_attempts: 4
```

- [x] **Step 7: Commit**

```bash
git add internal/config/ darkrouter.example.yaml
git commit -m "feat(config): add aliases and the retry budget"
```

---

### Task 2: A typed surface

**Files:**
- Modify: `internal/ir/ir.go`
- Test: `internal/ir/ir_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ir.Surface` with `ir.SurfaceLLM`, `SurfaceEmbeddings`, `SurfaceImages`, `SurfaceAudio`, `SurfaceRerank`, `SurfaceModerations`, and `func ir.ParseSurface(s string) (Surface, bool)`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

Both the router and the catalog need to talk about surfaces, and each would
otherwise import the other. `internal/ir` is already a leaf — it imports only
`encoding/json` — so the type lives there and both import it without a cycle.

`edge.Passthrough.Surface` stays a plain `string` in this phase. Retyping it
would ripple through the edge package, its parser, and the executor in one
change, which is more than this task should carry; the executor converts at the
boundary with `ir.ParseSurface`. Phase 4 rewrites the edge layer for Anthropic
and Gemini and is the right moment to type it.

- [x] **Step 1: Write the failing test**

Append to `internal/ir/ir_test.go`:

```go
func TestParseSurface(t *testing.T) {
	cases := []struct {
		in     string
		want   Surface
		wantOK bool
	}{
		{"llm", SurfaceLLM, true},
		{"embeddings", SurfaceEmbeddings, true},
		{"images", SurfaceImages, true},
		{"audio", SurfaceAudio, true},
		{"rerank", SurfaceRerank, true},
		{"moderations", SurfaceModerations, true},
		{"", "", false},
		{"nonsense", "", false},
		// Surfaces come from stored catalog rows and inbound routes, both of
		// which are lower-case by construction; accepting other casings would
		// let two spellings of one surface diverge.
		{"LLM", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseSurface(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("ParseSurface(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ir/ -run Surface -v`
Expected: FAIL — `undefined: ParseSurface`.

- [x] **Step 3: Add the type**

In `internal/ir/ir.go`, add near the other string enums:

```go
// Surface is the kind of work a request asks for. It lives here rather than in
// the router or the catalog because both need it and either import would
// create a cycle.
type Surface string

const (
	SurfaceLLM         Surface = "llm"
	SurfaceEmbeddings  Surface = "embeddings"
	SurfaceImages      Surface = "images"
	SurfaceAudio       Surface = "audio"
	SurfaceRerank      Surface = "rerank"
	SurfaceModerations Surface = "moderations"
)

// ParseSurface converts a stored or inbound string. It reports failure rather
// than defaulting, because a request routed to the wrong surface fails in a
// much more confusing way than one refused up front.
func ParseSurface(s string) (Surface, bool) {
	switch Surface(s) {
	case SurfaceLLM, SurfaceEmbeddings, SurfaceImages,
		SurfaceAudio, SurfaceRerank, SurfaceModerations:
		return Surface(s), true
	default:
		return "", false
	}
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ir/ -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/ir/
git commit -m "feat(ir): add a typed surface"
```

---

### Task 3: Providers carry every credential

**Files:**
- Modify: `internal/provider/provider.go`
- Modify: `internal/provider/sqlsource.go`
- Test: `internal/provider/sqlsource_test.go`

**Interfaces:**
- Consumes: `store.Credential` from Phase 2.
- Produces: `provider.Credential{ID, Secret string, Enabled bool}` and `Provider.Credentials []Credential`, ordered by credential id.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Purely additive. `Provider.APIKey` and `Provider.KeyID` stay populated with the
first enabled credential so the Phase 2 executor keeps compiling and passing;
Task 18 removes them once the attempt loop reads `Credentials` instead.

Doing it additively rather than as one breaking change is what keeps this task
inside Rule S — a single change to `Provider` plus every consumer would be a
shared-interface change touching five file shapes at once.

- [x] **Step 1: Write the failing test**

Append to `internal/provider/sqlsource_test.go`:

```go
func TestSQLSourceLoadsEveryEnabledCredential(t *testing.T) {
	db, key := newTestDB(t)
	ctx := context.Background()
	first := seed(t, db, key, "groq", 0, true, "m")
	second, err := db.AddCredential(ctx, key, store.Credential{
		ProviderID: "groq", Secret: "sk-second", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	src := NewSQLSource(db, key)
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	ps, _ := src.Providers(ctx)
	if len(ps) != 1 {
		t.Fatalf("got %d providers, want 1", len(ps))
	}
	creds := ps[0].Credentials
	if len(creds) != 2 {
		t.Fatalf("got %d credentials, want 2", len(creds))
	}
	// Ordered by id, which for ULIDs is insertion order. Credential rotation
	// depends on a total, deterministic order.
	if creds[0].ID != first || creds[1].ID != second {
		t.Errorf("credential order = %s, %s; want %s, %s",
			creds[0].ID, creds[1].ID, first, second)
	}
	if creds[1].Secret != "sk-second" {
		t.Errorf("secret = %q", creds[1].Secret)
	}
}

func TestSQLSourceExcludesDisabledCredentials(t *testing.T) {
	db, key := newTestDB(t)
	ctx := context.Background()
	enabled := seed(t, db, key, "groq", 0, true, "m")
	if _, err := db.AddCredential(ctx, key, store.Credential{
		ProviderID: "groq", Secret: "sk-off", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}

	src := NewSQLSource(db, key)
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	ps, _ := src.Providers(ctx)
	if len(ps[0].Credentials) != 1 || ps[0].Credentials[0].ID != enabled {
		t.Errorf("credentials = %+v, want only the enabled one", ps[0].Credentials)
	}
}

// The phase 2 fields stay populated until the attempt loop stops reading them.
func TestSQLSourceStillPopulatesTheSingleCredentialFields(t *testing.T) {
	db, key := newTestDB(t)
	first := seed(t, db, key, "groq", 0, true, "m")

	src := NewSQLSource(db, key)
	if err := src.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	ps, _ := src.Providers(context.Background())
	if ps[0].KeyID != first || ps[0].APIKey != "sk-groq" {
		t.Errorf("legacy fields = %s/%s", ps[0].KeyID, ps[0].APIKey)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -run Credential -v`
Expected: FAIL — `ps[0].Credentials undefined`.

- [x] **Step 3: Add the type**

In `internal/provider/provider.go`:

```go
// Credential is one usable key for a provider. Secret is plaintext and lives
// only in memory; the store decrypts once at load.
type Credential struct {
	ID      string
	Secret  string
	Enabled bool
}

type Provider struct {
	ID      string
	Kind    string
	BaseURL string

	// Credentials are every enabled credential, ordered by id. Credential
	// rotation happens before advancing to the next provider, so the router
	// needs all of them rather than a chosen one.
	Credentials []Credential

	// APIKey and KeyID hold the first enabled credential. They exist only
	// until the attempt loop reads Credentials; task 18 removes them.
	APIKey   string
	KeyID    string
	Priority int
	Models   []string
}
```

- [x] **Step 4: Populate it**

In `internal/provider/sqlsource.go`, replace the body of the per-provider loop
in `Reload` that builds the `Provider`:

```go
		creds, err := s.db.Credentials(ctx, s.key, r.id)
		if err != nil {
			return fmt.Errorf("provider %q: %w", r.id, err)
		}
		enabled := enabledOnly(creds)
		if len(enabled) == 0 {
			// A provider with no usable credential cannot serve. Skipping it
			// here is what stops every request against it failing at the
			// transport layer instead.
			continue
		}
		models, err := s.models(ctx, r.id)
		if err != nil {
			return err
		}
		out = append(out, Provider{
			ID: r.id, Kind: r.kind, BaseURL: r.baseURL,
			Credentials: enabled,
			APIKey:      enabled[0].Secret,
			KeyID:       enabled[0].ID,
			Priority:    r.priority, Models: models,
		})
```

Replace `firstEnabled` with:

```go
// enabledOnly keeps the enabled credentials in id order. ULID ids sort by
// insertion time, which gives credential rotation a total and deterministic
// order to start from.
func enabledOnly(creds []store.Credential) []Credential {
	out := make([]Credential, 0, len(creds))
	for _, c := range creds {
		if !c.Enabled {
			continue
		}
		out = append(out, Credential{ID: c.ID, Secret: c.Secret, Enabled: true})
	}
	return out
}
```

- [x] **Step 5: Include credentials in the revision**

In `revisionOf`, hash every credential id rather than the single `KeyID`, so
adding a second key to a provider changes the revision:

```go
	h := fnv.New64a()
	for _, p := range sorted {
		_, _ = h.Write([]byte(p.ID))
		_, _ = h.Write([]byte(p.BaseURL))
		_, _ = h.Write([]byte(strconv.Itoa(p.Priority)))
		for _, c := range p.Credentials {
			_, _ = h.Write([]byte(c.ID))
		}
		for _, m := range p.Models {
			_, _ = h.Write([]byte(m))
		}
	}
```

- [x] **Step 6: Run tests to verify they pass**

Run: `go test ./... -race`
Expected: PASS across every package — this task is additive, so the Phase 2
executor and server tests are untouched.

- [x] **Step 7: Commit**

```bash
git add internal/provider/
git commit -m "feat(provider): carry every enabled credential"
```

---

### Task 4: A frozen availability snapshot

**Files:**
- Create: `internal/health/availability.go`
- Test: `internal/health/availability_test.go`

**Interfaces:**
- Consumes: `health.Breaker`, `health.Key` from Phase 2.
- Produces: `health.Availability` with `func (Availability) Available(k Key) bool`, and `func (*Breaker) SnapshotAvailability(at time.Time) Availability`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

The router filters on `cooling_until`, so "no clock" is impossible unless health
arrives already resolved to booleans at a known instant. That is what this type
is: a frozen answer, not a live query.

The critical property is that it **never claims a half-open probe**.
`Breaker.Available` performs the claim as a side effect, so calling it from the
router would burn the single probe on a candidate the router might not even
attempt. This path only reads.

A zero `Availability` reports everything available, so a caller that forgot to
build one fails open rather than routing to nothing.

- [x] **Step 1: Write the failing test**

Create `internal/health/availability_test.go`:

```go
package health

import (
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestZeroAvailabilityAdmitsEverything(t *testing.T) {
	var a Availability
	if !a.Available(triple) {
		t.Fatal("a zero Availability must fail open")
	}
}

func TestSnapshotReportsACoolingTriple(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}

	a := b.SnapshotAvailability(*now)
	if a.Available(triple) {
		t.Fatal("a cooling triple must be reported unavailable")
	}
	// A different model on the same credential is unaffected by a triple-level
	// cooldown.
	other := Key{ProviderID: "groq", KeyID: "k1", Model: "other"}
	if !a.Available(other) {
		t.Fatal("a triple cooldown must not cool the credential's other models")
	}
}

func TestSnapshotCredentialCooldownCoolsEveryModel(t *testing.T) {
	b, now := newTestBreaker(t)
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableCredential, StatusCode: 402})

	a := b.SnapshotAvailability(*now)
	for _, m := range []string{"m", "other", "third"} {
		k := Key{ProviderID: "groq", KeyID: "k1", Model: m}
		if a.Available(k) {
			t.Errorf("model %q available despite a credential-level cooldown", m)
		}
	}
	// A different credential on the same provider is untouched.
	if !a.Available(Key{ProviderID: "groq", KeyID: "k2", Model: "m"}) {
		t.Error("a credential cooldown must not cool the provider's other credentials")
	}
}

func TestSnapshotAtALaterInstantSeesTheCooldownExpired(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	if b.SnapshotAvailability(*now).Available(triple) {
		t.Fatal("should be cooling at the trip instant")
	}
	// The instant is an input, so the same breaker answers differently for a
	// later evaluation time. This is what makes the router's time-dependent
	// cases testable without sleeping.
	later := now.Add(2 * time.Second)
	if !b.SnapshotAvailability(later).Available(triple) {
		t.Fatal("the level-0 cooldown of 1s should have expired by +2s")
	}
}

// The router must not burn the single half-open probe just by looking.
func TestSnapshotDoesNotClaimTheHalfOpenProbe(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	*now = now.Add(2 * time.Second)

	// Look many times.
	for i := 0; i < 10; i++ {
		b.SnapshotAvailability(*now)
	}
	// The live path must still admit exactly one probe.
	if !b.Available(triple) {
		t.Fatal("snapshotting consumed the half-open probe")
	}
	if b.Available(triple) {
		t.Fatal("a second live caller was admitted; the claim is broken")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/health/ -run Availability -v`
Expected: FAIL — `undefined: Availability`.

- [x] **Step 3: Write the snapshot**

Create `internal/health/availability.go`:

```go
package health

import "time"

// Availability is a frozen view of breaker state at one instant.
//
// The router filters on cooldowns, so it needs an answer rather than a live
// query: reading the breaker directly would mean reading a clock inside a
// function whose whole value is that it does not.
//
// The zero value reports everything available, so a caller that forgot to build
// one fails open rather than routing to nothing.
type Availability struct {
	unavailable map[Key]struct{}
}

// Available reports whether the triple was usable at the snapshot instant. The
// credential-level entry gates every model the credential serves, so it is
// consulted first.
func (a Availability) Available(k Key) bool {
	if len(a.unavailable) == 0 {
		return true
	}
	if k.Model != "" {
		if _, cooling := a.unavailable[Key{ProviderID: k.ProviderID, KeyID: k.KeyID}]; cooling {
			return false
		}
	}
	_, cooling := a.unavailable[k]
	return !cooling
}

// SnapshotAvailability freezes the breaker as of at.
//
// It deliberately does not go through Available: that method claims the
// half-open probe as a side effect, and burning the single probe on a candidate
// the router may never attempt would leave the breaker shut with nothing
// testing it.
func (b *Breaker) SnapshotAvailability(at time.Time) Availability {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out map[Key]struct{}
	for k, st := range b.m {
		if st.coolingUntil.IsZero() || !at.Before(st.coolingUntil) {
			continue
		}
		if out == nil {
			out = make(map[Key]struct{})
		}
		out[k] = struct{}{}
	}
	return Availability{unavailable: out}
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/health/ -race -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/health/
git commit -m "feat(health): add a frozen availability snapshot"
```

---

### Task 5: Credential least-recently-used tracking

**Files:**
- Modify: `internal/health/breaker.go`
- Create: `internal/health/lru.go`
- Test: `internal/health/lru_test.go`

**Interfaces:**
- Consumes: `health.Breaker`.
- Produces: `health.CredKey{ProviderID, KeyID string}`, `func (*Breaker) MarkUsed(ck CredKey, at time.Time)`, `func (*Breaker) LastUsedSnapshot() map[CredKey]time.Time`, `func (*Breaker) RehydrateLastUsed(m map[CredKey]time.Time)`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

`last_used_at` is **authoritative in memory**, updated synchronously under the
health mutex at attempt start. Reading it from SQLite would put the hot path on
the database, and relying on the debounced persisted value would leave
concurrent requests seeing a stale timestamp for seconds — exactly the window
where draining several keys in parallel matters, and exactly when everything
would pile onto one key.

Updating at **attempt start rather than on success** is deliberate: a credential
that always 401s would otherwise keep a stale timestamp and sort first forever.

It lives on the breaker because it shares the breaker's mutex. Two locks over
the same per-credential decision would have to be taken in a fixed order by
every caller, and one caller getting that order wrong is a deadlock.

- [x] **Step 1: Write the failing test**

Create `internal/health/lru_test.go`:

```go
package health

import (
	"sync"
	"testing"
	"time"
)

func TestMarkUsedRecordsTheInstant(t *testing.T) {
	b, now := newTestBreaker(t)
	ck := CredKey{ProviderID: "groq", KeyID: "k1"}

	if got := b.LastUsedSnapshot(); len(got) != 0 {
		t.Fatalf("a fresh breaker should have no usage, got %v", got)
	}
	b.MarkUsed(ck, *now)

	snap := b.LastUsedSnapshot()
	if !snap[ck].Equal(*now) {
		t.Errorf("LastUsed = %s, want %s", snap[ck], *now)
	}
}

func TestLastUsedSnapshotIsACopy(t *testing.T) {
	b, now := newTestBreaker(t)
	ck := CredKey{ProviderID: "groq", KeyID: "k1"}
	b.MarkUsed(ck, *now)

	snap := b.LastUsedSnapshot()
	delete(snap, ck)
	// Mutating the returned map must not corrupt the breaker's own state.
	if len(b.LastUsedSnapshot()) != 1 {
		t.Fatal("LastUsedSnapshot handed out its internal map")
	}
}

func TestRehydrateLastUsedRestoresOrderingAcrossARestart(t *testing.T) {
	b, now := newTestBreaker(t)
	a := CredKey{ProviderID: "groq", KeyID: "k1"}
	c := CredKey{ProviderID: "groq", KeyID: "k2"}
	b.RehydrateLastUsed(map[CredKey]time.Time{
		a: now.Add(-time.Hour),
		c: now.Add(-time.Minute),
	})
	snap := b.LastUsedSnapshot()
	if !snap[a].Before(snap[c]) {
		t.Fatal("restored ordering was lost; a restart would pile onto one key")
	}
}

func TestMarkUsedIsSafeUnderConcurrency(t *testing.T) {
	b, now := newTestBreaker(t)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b.MarkUsed(CredKey{ProviderID: "groq", KeyID: "k1"}, now.Add(time.Duration(i)))
			_ = b.LastUsedSnapshot()
		}(i)
	}
	wg.Wait()
	if len(b.LastUsedSnapshot()) != 1 {
		t.Fatal("concurrent marks produced the wrong map")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/health/ -run LastUsed -v`
Expected: FAIL — `undefined: CredKey`.

- [x] **Step 3: Add the field to the breaker**

In `internal/health/breaker.go`, add one field to `Breaker` and initialize it in
`New`:

```go
type Breaker struct {
	mu        sync.Mutex
	m         map[Key]*state
	lastUsed  map[CredKey]time.Time
	tripAfter int
	max       time.Duration

	// now is swappable so tests can advance time without sleeping.
	now func() time.Time

	dirty bool
}
```

In `New`, add `lastUsed: make(map[CredKey]time.Time),` to the returned struct.

- [x] **Step 4: Write the tracking**

Create `internal/health/lru.go`:

```go
package health

import "time"

// CredKey identifies a credential, without a model. Least-recently-used is a
// property of the credential rather than of a triple: rotating keys is about
// spreading load across quotas, and a quota is per key.
type CredKey struct {
	ProviderID string
	KeyID      string
}

// MarkUsed records a dispatch. It is called at attempt start, not on success:
// a credential that always fails would otherwise keep a stale timestamp and
// sort first forever.
//
// It shares the breaker's mutex because it is part of the same per-credential
// decision. A second lock would have to be ordered against the first by every
// caller, and one caller getting that order wrong is a deadlock.
func (b *Breaker) MarkUsed(ck CredKey, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastUsed == nil {
		b.lastUsed = make(map[CredKey]time.Time)
	}
	b.lastUsed[ck] = at
	b.dirty = true
}

// LastUsedSnapshot returns a copy for the router and the persister. A copy
// rather than the map itself: the router sorts over it without the lock, and a
// concurrent MarkUsed would otherwise be a data race.
func (b *Breaker) LastUsedSnapshot() map[CredKey]time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[CredKey]time.Time, len(b.lastUsed))
	for k, v := range b.lastUsed {
		out[k] = v
	}
	return out
}

// RehydrateLastUsed restores timestamps at startup so a restart does not put
// every credential back on an equal footing and pile the next burst onto one.
func (b *Breaker) RehydrateLastUsed(m map[CredKey]time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastUsed == nil {
		b.lastUsed = make(map[CredKey]time.Time, len(m))
	}
	for k, v := range m {
		b.lastUsed[k] = v
	}
}
```

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/health/ -race -v`
Expected: PASS. `TestMarkUsedIsSafeUnderConcurrency` is the one that matters
under `-race`.

- [x] **Step 6: Commit**

```bash
git add internal/health/
git commit -m "feat(health): track credential least-recently-used"
```

---

### Task 6: Persist credential usage timestamps

**Files:**
- Create: `internal/store/lastused.go`
- Test: `internal/store/lastused_test.go`

**Interfaces:**
- Consumes: `health.CredKey` from Task 5; the `provider_keys.last_used_at` column from Phase 2's schema.
- Produces: `func (*DB) SaveLastUsed(ctx context.Context, m map[health.CredKey]time.Time) error`, `func (*DB) LoadLastUsed(ctx context.Context) (map[health.CredKey]time.Time, error)`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Persistence here is **purely for restart continuity**. The in-memory value is
authoritative on the hot path, and nothing reads these rows to make a routing
decision — only to seed the map at startup so a restart does not put every
credential back on an equal footing and pile the next burst onto one key.

This deliberately does not extend `health.Store`. That interface is what the
health persister depends on, and widening it would make this a shared-interface
change rippling into the persister and its fakes. Task 18 wires the periodic
save as its own worker instead.

An `UPDATE` rather than an upsert: a credential that is not in `provider_keys`
has been deleted, and resurrecting a row for it would violate the foreign key
its provider depends on.

- [x] **Step 1: Write the failing test**

Create `internal/store/lastused_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
)

func TestLastUsedRoundTrip(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	id1, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "a", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "b", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	older := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	newer := time.Now().UTC().Truncate(time.Millisecond)
	if err := db.SaveLastUsed(ctx, map[health.CredKey]time.Time{
		{ProviderID: "groq", KeyID: id1}: older,
		{ProviderID: "groq", KeyID: id2}: newer,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := db.LoadLastUsed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if !got[health.CredKey{ProviderID: "groq", KeyID: id1}].Equal(older) {
		t.Errorf("id1 = %s, want %s", got[health.CredKey{ProviderID: "groq", KeyID: id1}], older)
	}
	// The ordering is the whole point; assert it survived rather than just the
	// individual values.
	a := got[health.CredKey{ProviderID: "groq", KeyID: id1}]
	b := got[health.CredKey{ProviderID: "groq", KeyID: id2}]
	if !a.Before(b) {
		t.Errorf("ordering lost: %s is not before %s", a, b)
	}
}

func TestLoadLastUsedSkipsNeverUsedCredentials(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")
	seededProvider(t, db, "groq")
	if _, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "a", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	got, err := db.LoadLastUsed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A NULL last_used_at means never used. Returning it as the zero time would
	// be correct but noisy; absence says the same thing and keeps the map small.
	if len(got) != 0 {
		t.Fatalf("got %v, want an empty map", got)
	}
}

// A credential deleted between the snapshot and the save must not be recreated.
func TestSaveLastUsedIgnoresUnknownCredentials(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.SaveLastUsed(ctx, map[health.CredKey]time.Time{
		{ProviderID: "gone", KeyID: "deleted"}: time.Now(),
	}); err != nil {
		t.Fatalf("saving a vanished credential must be a no-op, not an error: %v", err)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM provider_keys`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("provider_keys = %d, want 0 — a deleted credential must not be resurrected", n)
	}
}

func TestSaveLastUsedWithNothingIsANoOp(t *testing.T) {
	db := migrated(t)
	if err := db.SaveLastUsed(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run LastUsed -v`
Expected: FAIL — `db.SaveLastUsed undefined`.

- [x] **Step 3: Write the queries**

Create `internal/store/lastused.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
)

// SaveLastUsed records credential dispatch times. This exists only so a restart
// resumes with the rotation order it had; the in-memory map is authoritative on
// the hot path and nothing reads these rows to make a routing decision.
func (d *DB) SaveLastUsed(ctx context.Context, m map[health.CredKey]time.Time) error {
	if len(m) == 0 {
		return nil
	}
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin last_used save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// UPDATE rather than upsert: a credential missing from provider_keys was
	// deleted, and inserting it back would violate the provider foreign key.
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE provider_keys SET last_used_at = ? WHERE id = ? AND provider_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for ck, at := range m {
		if _, err := stmt.ExecContext(ctx, at.UnixMilli(), ck.KeyID, ck.ProviderID); err != nil {
			return fmt.Errorf("update last_used for %s/%s: %w", ck.ProviderID, ck.KeyID, err)
		}
	}
	return tx.Commit()
}

// LoadLastUsed reads the timestamps for rehydration. Credentials that have
// never been dispatched to are absent rather than zero-valued.
func (d *DB) LoadLastUsed(ctx context.Context) (map[health.CredKey]time.Time, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT provider_id, id, last_used_at FROM provider_keys WHERE last_used_at IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("load last_used: %w", err)
	}
	defer rows.Close()

	out := map[health.CredKey]time.Time{}
	for rows.Next() {
		var providerID, keyID string
		var at int64
		if err := rows.Scan(&providerID, &keyID, &at); err != nil {
			return nil, fmt.Errorf("scan last_used: %w", err)
		}
		out[health.CredKey{ProviderID: providerID, KeyID: keyID}] = time.UnixMilli(at).UTC()
	}
	return out, rows.Err()
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): persist credential usage timestamps"
```

---

### Task 7: The catalog reader

**Files:**
- Create: `internal/catalog/catalog.go`
- Test: `internal/catalog/catalog_test.go`

**Interfaces:**
- Consumes: `ir.Surface` from Task 2, `provider.Provider` from Task 3.
- Produces: `catalog.Model`, `catalog.Capabilities`, `catalog.Source` with its four constants, `catalog.Reader` interface, `func catalog.FromProviders(ps []provider.Provider) Reader`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

`Reader` is a narrow interface this phase **defines** and Phase 6 **implements**,
so Phase 3 does not depend on a package that does not exist yet.

Phase 3 has no catalog tables, so `FromProviders` reports every model as
declaring only the `llm` surface with `SourceInferred` capabilities. Per master
design §6.4 inferred capabilities pass the router's filter with a warning, so
capability filtering is wired and exercised here while admitting everything.
Phase 6 supplies the data that makes it selective.

- [x] **Step 1: Write the failing test**

Create `internal/catalog/catalog_test.go`:

```go
package catalog

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

func fleet() []provider.Provider {
	return []provider.Provider{
		{ID: "groq", Models: []string{"llama", "shared"}},
		{ID: "cerebras", Models: []string{"shared"}},
	}
}

func TestLookupFindsAProvidersModel(t *testing.T) {
	r := FromProviders(fleet())
	m, ok := r.Lookup("groq", "llama")
	if !ok {
		t.Fatal("groq/llama not found")
	}
	if m.ProviderID != "groq" || m.ModelID != "llama" {
		t.Errorf("model = %+v", m)
	}
	if m.Source != SourceInferred {
		t.Errorf("Source = %q, want inferred — phase 3 has no catalog data", m.Source)
	}
	if len(m.Surfaces) != 1 || m.Surfaces[0] != ir.SurfaceLLM {
		t.Errorf("Surfaces = %v, want [llm]", m.Surfaces)
	}
}

func TestLookupMissesAreReported(t *testing.T) {
	r := FromProviders(fleet())
	if _, ok := r.Lookup("groq", "nope"); ok {
		t.Error("a model the provider does not offer must not be found")
	}
	if _, ok := r.Lookup("nosuch", "llama"); ok {
		t.Error("a provider that does not exist must not be found")
	}
}

func TestOfferingListsEveryProviderInOrder(t *testing.T) {
	r := FromProviders(fleet())
	got := r.Offering("shared")
	if len(got) != 2 || got[0] != "groq" || got[1] != "cerebras" {
		t.Errorf("Offering = %v, want [groq cerebras] in provider order", got)
	}
	if len(r.Offering("nope")) != 0 {
		t.Error("an unoffered model must yield no providers")
	}
}

func TestModelDeclaresSurface(t *testing.T) {
	r := FromProviders(fleet())
	m, _ := r.Lookup("groq", "llama")
	if !m.DeclaresSurface(ir.SurfaceLLM) {
		t.Error("llm must be declared")
	}
	if m.DeclaresSurface(ir.SurfaceEmbeddings) {
		t.Error("embeddings must not be declared in phase 3")
	}
}

// Inferred capabilities admit everything: hard-filtering on guessed metadata
// would make every local model unroutable for agentic traffic.
func TestInferredCapabilitiesSatisfyEveryRequirement(t *testing.T) {
	r := FromProviders(fleet())
	m, _ := r.Lookup("groq", "llama")
	if !m.Capabilities.Satisfies(Capabilities{Tools: true, Vision: true, Reasoning: true}) {
		t.Error("inferred capabilities must admit the candidate")
	}
	if !m.Inferred() {
		t.Error("phase 3 models are inferred and the router must warn about them")
	}
}

func TestKnownCapabilitiesAreSelective(t *testing.T) {
	// Phase 6 supplies real data; the comparison must already be correct.
	known := Capabilities{Tools: false, Vision: true, Reasoning: false, Known: true}
	if known.Satisfies(Capabilities{Tools: true}) {
		t.Error("a known model without tools must not satisfy a tools requirement")
	}
	if !known.Satisfies(Capabilities{Vision: true}) {
		t.Error("a known model with vision must satisfy a vision requirement")
	}
	if !known.Satisfies(Capabilities{}) {
		t.Error("no requirement is always satisfied")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/catalog/ -v`
Expected: FAIL — `undefined: FromProviders`.

- [x] **Step 3: Write the package**

Create `internal/catalog/catalog.go`:

```go
// Package catalog describes what each provider offers. Phase 3 defines the
// narrow Reader the router needs; phase 6 supplies the real implementation
// backed by models.dev sync and live discovery.
package catalog

import (
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

// Source records where a model's metadata came from. It matters because
// inferred metadata is a guess, and the router treats a guess differently from
// a fact.
type Source string

const (
	SourceModelsDev  Source = "models_dev"
	SourceDiscovered Source = "discovered"
	SourceInferred   Source = "inferred"
	SourceOverride   Source = "override"
)

// Capabilities is what a model can do. Known distinguishes "we checked and it
// cannot" from "we never found out".
type Capabilities struct {
	Tools     bool
	Vision    bool
	Reasoning bool
	Known     bool
}

// Satisfies reports whether these capabilities meet a requirement.
//
// Unknown capabilities satisfy everything, per master design §6.4: a provider's
// own error is clearer than Darkrouter silently refusing to route, and
// hard-filtering on guessed metadata would make every local model unroutable
// for agentic traffic. The router records a warning instead.
func (c Capabilities) Satisfies(need Capabilities) bool {
	if !c.Known {
		return true
	}
	if need.Tools && !c.Tools {
		return false
	}
	if need.Vision && !c.Vision {
		return false
	}
	if need.Reasoning && !c.Reasoning {
		return false
	}
	return true
}

// Model is one model as offered by one provider.
type Model struct {
	ProviderID   string
	ModelID      string
	Publisher    string
	Surfaces     []ir.Surface
	Capabilities Capabilities
	Source       Source
}

func (m Model) DeclaresSurface(s ir.Surface) bool {
	for _, have := range m.Surfaces {
		if have == s {
			return true
		}
	}
	return false
}

// Inferred reports whether this model's metadata is a guess. The router admits
// these and warns.
func (m Model) Inferred() bool { return m.Source == SourceInferred }

// Reader is the narrow view the router needs. Defining it here rather than
// importing phase 6's package is what lets phase 3 be written first.
type Reader interface {
	// Lookup returns the model as offered by one provider.
	Lookup(providerID, modelID string) (Model, bool)
	// Offering returns the provider ids offering modelID, in the order the
	// provider set gave them — which is priority order.
	Offering(modelID string) []string
}

type providerReader struct {
	byProvider map[string]map[string]Model
	offering   map[string][]string
}

// FromProviders builds a Reader over the configured fleet. Phase 3 has no
// catalog tables, so every model declares only the llm surface and its
// capabilities are inferred.
func FromProviders(ps []provider.Provider) Reader {
	r := &providerReader{
		byProvider: make(map[string]map[string]Model, len(ps)),
		offering:   make(map[string][]string),
	}
	for _, p := range ps {
		models := make(map[string]Model, len(p.Models))
		for _, id := range p.Models {
			models[id] = Model{
				ProviderID: p.ID,
				ModelID:    id,
				Surfaces:   []ir.Surface{ir.SurfaceLLM},
				Source:     SourceInferred,
			}
			r.offering[id] = append(r.offering[id], p.ID)
		}
		r.byProvider[p.ID] = models
	}
	return r
}

func (r *providerReader) Lookup(providerID, modelID string) (Model, bool) {
	models, ok := r.byProvider[providerID]
	if !ok {
		return Model{}, false
	}
	m, ok := models[modelID]
	return m, ok
}

func (r *providerReader) Offering(modelID string) []string {
	return r.offering[modelID]
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/catalog/ -race -v`
Expected: PASS, six tests.

- [x] **Step 5: Commit**

```bash
git add internal/catalog/
git commit -m "feat(catalog): add the reader interface"
```

---

### Task 8: Router types and model resolution

**Files:**
- Create: `internal/router/types.go`
- Create: `internal/router/model.go`
- Test: `internal/router/model_test.go`

**Interfaces:**
- Consumes: `catalog.Reader` from Task 7, `ir.Surface` from Task 2, `provider.Provider` from Task 3, `health.Availability` from Task 4.
- Produces: `router.Query`, `router.Snapshot`, `router.Candidate`, `router.Skip`, `router.SkipReason` with its five constants, and the unexported `target` plus `resolveModel`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Model resolution is tried in order, first match wins: exact alias, then
`provider/model`, then bare name.

The `provider/model` form splits on the **first** slash and matches **only when
the prefix names a configured provider**. Model identifiers legitimately contain
slashes — `meta-llama/Llama-3.3-70B-Instruct-Turbo`, and the `openai/gpt-oss-120b`
in the shipped example — so a non-matching prefix must fall through to bare-name
resolution with the full string intact. Getting this wrong makes every
slash-bearing model name unroutable.

Alias targets are expanded through rules 2 and 3 but **not** through rule 1: an
alias naming another alias is not followed. Nested aliases would need cycle
detection for a feature nobody asked for.

- [x] **Step 1: Write the failing test**

Create `internal/router/model_test.go`:

```go
package router

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/provider"
)

func testCatalog() catalog.Reader {
	return catalog.FromProviders([]provider.Provider{
		{ID: "groq", Models: []string{"llama", "shared", "openai/gpt-oss-120b"}},
		{ID: "cerebras", Models: []string{"shared"}},
	})
}

func testProviders() map[string]bool {
	return map[string]bool{"groq": true, "cerebras": true}
}

func targetsOf(ts []target) [][2]string {
	out := make([][2]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, [2]string{t.ProviderID, t.ModelID})
	}
	return out
}

func TestResolveExactAliasExpandsInOrder(t *testing.T) {
	aliases := map[string][]string{
		"fast": {"cerebras/shared", "groq/shared"},
	}
	got, alias := resolveModel("fast", aliases, testProviders(), testCatalog())
	if alias != "fast" {
		t.Errorf("alias = %q, want fast", alias)
	}
	want := [][2]string{{"cerebras", "shared"}, {"groq", "shared"}}
	if g := targetsOf(got); len(g) != 2 || g[0] != want[0] || g[1] != want[1] {
		t.Errorf("targets = %v, want %v", g, want)
	}
}

func TestResolveProviderSlashModel(t *testing.T) {
	got, alias := resolveModel("groq/llama", nil, testProviders(), testCatalog())
	if alias != "" {
		t.Errorf("alias = %q, want empty", alias)
	}
	if g := targetsOf(got); len(g) != 1 || g[0] != [2]string{"groq", "llama"} {
		t.Errorf("targets = %v", g)
	}
}

// The load-bearing case: a slash that is part of the model name, not a prefix.
func TestResolveFallsThroughWhenThePrefixIsNotAProvider(t *testing.T) {
	got, _ := resolveModel("openai/gpt-oss-120b", nil, testProviders(), testCatalog())
	if g := targetsOf(got); len(g) != 1 || g[0] != [2]string{"groq", "openai/gpt-oss-120b"} {
		t.Fatalf("targets = %v, want groq offering the full slashed name", g)
	}
}

func TestResolveBareNameFansOutInProviderOrder(t *testing.T) {
	got, _ := resolveModel("shared", nil, testProviders(), testCatalog())
	g := targetsOf(got)
	if len(g) != 2 || g[0] != [2]string{"groq", "shared"} || g[1] != [2]string{"cerebras", "shared"} {
		t.Errorf("targets = %v, want groq then cerebras", g)
	}
}

func TestResolveUnknownNameYieldsNothing(t *testing.T) {
	got, _ := resolveModel("nope", nil, testProviders(), testCatalog())
	if len(got) != 0 {
		t.Errorf("targets = %v, want none", targetsOf(got))
	}
}

func TestResolveProviderSlashModelTheProviderDoesNotOffer(t *testing.T) {
	// The prefix names a real provider, so rule 2 matches and rule 3 is not
	// tried. The target is produced and filtering rejects it later — that is
	// what makes the skip reason say "this provider", not "no such model".
	got, _ := resolveModel("cerebras/llama", nil, testProviders(), testCatalog())
	if g := targetsOf(got); len(g) != 1 || g[0] != [2]string{"cerebras", "llama"} {
		t.Errorf("targets = %v", g)
	}
}

func TestAliasWinsOverProviderSlashModel(t *testing.T) {
	aliases := map[string][]string{"groq/llama": {"cerebras/shared"}}
	got, alias := resolveModel("groq/llama", aliases, testProviders(), testCatalog())
	if alias != "groq/llama" {
		t.Errorf("alias = %q", alias)
	}
	if g := targetsOf(got); len(g) != 1 || g[0] != [2]string{"cerebras", "shared"} {
		t.Errorf("targets = %v, want the alias expansion", g)
	}
}

// An alias naming another alias is not followed; the inner name goes through
// rules 2 and 3 like any other target.
func TestAliasesAreNotNested(t *testing.T) {
	aliases := map[string][]string{
		"outer": {"inner"},
		"inner": {"groq/llama"},
	}
	got, _ := resolveModel("outer", aliases, testProviders(), testCatalog())
	// "inner" is not a model any provider offers, so it resolves to nothing.
	if len(got) != 0 {
		t.Errorf("targets = %v, want none — nested aliases are not followed", targetsOf(got))
	}
}

func TestAliasWithAnUnknownProviderYieldsThatTargetAnyway(t *testing.T) {
	aliases := map[string][]string{"fast": {"nosuch/model", "groq/llama"}}
	got, _ := resolveModel("fast", aliases, testProviders(), testCatalog())
	// "nosuch" is not a provider, so rule 2 misses and rule 3 finds no offering
	// for the full string. Only the reachable half survives.
	if g := targetsOf(got); len(g) != 1 || g[0] != [2]string{"groq", "llama"} {
		t.Errorf("targets = %v, want only the reachable target", g)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/router/ -v`
Expected: FAIL — `undefined: resolveModel`.

- [x] **Step 3: Write the types**

Create `internal/router/types.go`:

```go
// Package router decides, deterministically, which targets a request may be
// attempted against and in what order.
package router

import (
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

// Query is what the request asks for.
type Query struct {
	Model          string
	Surface        ir.Surface
	NeedsTools     bool
	NeedsVision    bool
	NeedsReasoning bool
}

// Snapshot is every input Resolve is allowed to read. It carries an evaluation
// instant and health already resolved to booleans, because filtering on
// cooling_until means "no clock" is otherwise impossible — and reading
// time.Now() inside Resolve would destroy both purity and reproducibility.
//
// It carries providers *with their credentials* because Candidate.KeyID cannot
// be produced from a model catalog alone.
type Snapshot struct {
	At        time.Time
	Providers []provider.Provider
	Catalog   catalog.Reader
	Config    *config.Config
	Health    health.Availability
	LastUsed  map[health.CredKey]time.Time
}

// Candidate is one attemptable target.
type Candidate struct {
	ProviderID string
	KeyID      string
	Model      string
	Kind       string
	Publisher  string // vertex only; empty in phase 3
}

// SkipReason explains why a target did not become a candidate.
type SkipReason string

const (
	SkipDisabled     SkipReason = "disabled"
	SkipCooling      SkipReason = "cooling"
	SkipSurface      SkipReason = "surface"
	SkipCapability   SkipReason = "capability"
	SkipNoCredential SkipReason = "no_credential"
)

// Skip records a target that was considered and rejected. These are persisted
// on the request row alongside the candidate list, because health tables are
// overwritten in place and cannot be replayed after the fact.
type Skip struct {
	ProviderID string
	KeyID      string
	Model      string
	Reason     SkipReason
}

// target is a resolved (provider, model) pair before filtering.
type target struct {
	ProviderID string
	ModelID    string
}
```

- [x] **Step 4: Write model resolution**

Create `internal/router/model.go`:

```go
package router

import (
	"strings"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// resolveModel expands a requested name into ordered targets, and reports the
// alias it matched if any. An empty result means the name is unresolvable.
//
// Order is: exact alias, then provider/model, then bare name. First match wins.
func resolveModel(name string, aliases map[string][]string,
	providers map[string]bool, cat catalog.Reader) ([]target, string) {

	if entries, ok := aliases[name]; ok {
		var out []target
		for _, entry := range entries {
			// Alias targets go through rules 2 and 3 only. Following a nested
			// alias would need cycle detection for a feature nobody asked for.
			out = append(out, resolveDirect(entry, providers, cat)...)
		}
		return out, name
	}
	return resolveDirect(name, providers, cat), ""
}

// resolveDirect applies rules 2 and 3.
func resolveDirect(name string, providers map[string]bool, cat catalog.Reader) []target {
	// Rule 2: split on the FIRST slash, and only when the prefix names a
	// configured provider. Model identifiers legitimately contain slashes
	// (meta-llama/Llama-3.3-70B-Instruct-Turbo), so a non-matching prefix must
	// fall through with the full string intact.
	if prefix, rest, found := strings.Cut(name, "/"); found && providers[prefix] {
		return []target{{ProviderID: prefix, ModelID: rest}}
	}

	// Rule 3: every enabled provider offering it, in the order the provider set
	// gave them — which is priority order.
	ids := cat.Offering(name)
	out := make([]target, 0, len(ids))
	for _, id := range ids {
		out = append(out, target{ProviderID: id, ModelID: name})
	}
	return out
}
```

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/router/ -race -v`
Expected: PASS, nine tests.

- [x] **Step 6: Commit**

```bash
git add internal/router/
git commit -m "feat(router): resolve aliases and model names"
```

---

### Task 9: Credential ordering

**Files:**
- Create: `internal/router/order.go`
- Test: `internal/router/order_test.go`

**Interfaces:**
- Consumes: `provider.Credential` from Task 3, `health.CredKey` from Task 5.
- Produces: `func orderCredentials(providerID string, creds []provider.Credential, lastUsed map[health.CredKey]time.Time) []provider.Credential`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

Least-recently-used, with ties broken by key id so the result is **total and
deterministic**. A partial order would make the candidate sequence depend on Go's
map iteration order, and the whole promise of this router is that the sequence is
predictable and explainable.

A credential never dispatched to sorts first. That is the point: a freshly added
key should be tried before one that has been carrying the load.

- [x] **Step 1: Write the failing test**

Create `internal/router/order_test.go`:

```go
package router

import (
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
)

func creds(ids ...string) []provider.Credential {
	out := make([]provider.Credential, 0, len(ids))
	for _, id := range ids {
		out = append(out, provider.Credential{ID: id, Secret: "sk-" + id, Enabled: true})
	}
	return out
}

func idsOf(cs []provider.Credential) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func eq(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOrderPutsLeastRecentlyUsedFirst(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lastUsed := map[health.CredKey]time.Time{
		{ProviderID: "groq", KeyID: "a"}: now.Add(-time.Minute),
		{ProviderID: "groq", KeyID: "b"}: now.Add(-time.Hour),
		{ProviderID: "groq", KeyID: "c"}: now.Add(-time.Second),
	}
	got := idsOf(orderCredentials("groq", creds("a", "b", "c"), lastUsed))
	if !eq(got, "b", "a", "c") {
		t.Errorf("order = %v, want [b a c]", got)
	}
}

// A key never dispatched to should be tried before one carrying the load.
func TestOrderPutsNeverUsedFirst(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lastUsed := map[health.CredKey]time.Time{
		{ProviderID: "groq", KeyID: "a"}: now.Add(-time.Hour),
	}
	got := idsOf(orderCredentials("groq", creds("a", "fresh"), lastUsed))
	if !eq(got, "fresh", "a") {
		t.Errorf("order = %v, want [fresh a]", got)
	}
}

// Determinism: equal timestamps must break by id, or the sequence depends on
// map iteration order and stops being explainable.
func TestOrderBreaksTiesByKeyID(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lastUsed := map[health.CredKey]time.Time{
		{ProviderID: "groq", KeyID: "c"}: now,
		{ProviderID: "groq", KeyID: "a"}: now,
		{ProviderID: "groq", KeyID: "b"}: now,
	}
	for i := 0; i < 20; i++ {
		got := idsOf(orderCredentials("groq", creds("c", "a", "b"), lastUsed))
		if !eq(got, "a", "b", "c") {
			t.Fatalf("run %d: order = %v, want [a b c]", i, got)
		}
	}
}

func TestOrderIgnoresOtherProvidersTimestamps(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	lastUsed := map[health.CredKey]time.Time{
		// Same key id, different provider: must not influence groq's order.
		{ProviderID: "cerebras", KeyID: "a"}: now.Add(-time.Hour),
		{ProviderID: "groq", KeyID: "b"}:     now.Add(-time.Hour),
	}
	got := idsOf(orderCredentials("groq", creds("a", "b"), lastUsed))
	// "a" is unused on groq, so it sorts before b's one-hour-old timestamp.
	if !eq(got, "a", "b") {
		t.Errorf("order = %v, want [a b]", got)
	}
}

func TestOrderDoesNotMutateItsInput(t *testing.T) {
	in := creds("c", "a", "b")
	orderCredentials("groq", in, nil)
	if !eq(idsOf(in), "c", "a", "b") {
		t.Errorf("input was reordered: %v", idsOf(in))
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/router/ -run Order -v`
Expected: FAIL — `undefined: orderCredentials`.

- [x] **Step 3: Write the ordering**

Create `internal/router/order.go`:

```go
package router

import (
	"sort"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
)

// orderCredentials returns creds least-recently-used first, ties broken by id.
//
// The tie-break is not cosmetic: without it the order of equally-aged
// credentials would depend on the input slice, and the candidate sequence would
// stop being reproducible from the snapshot alone.
//
// It copies rather than sorting in place, because the input belongs to the
// provider set and is shared by every concurrent request.
func orderCredentials(providerID string, creds []provider.Credential,
	lastUsed map[health.CredKey]time.Time) []provider.Credential {

	out := make([]provider.Credential, len(creds))
	copy(out, creds)

	at := func(id string) (time.Time, bool) {
		t, ok := lastUsed[health.CredKey{ProviderID: providerID, KeyID: id}]
		return t, ok
	}

	sort.SliceStable(out, func(i, j int) bool {
		ti, okI := at(out[i].ID)
		tj, okJ := at(out[j].ID)
		switch {
		case !okI && !okJ:
			// Both never dispatched to: fall through to the id tie-break.
		case !okI:
			return true // never used sorts before ever used
		case !okJ:
			return false
		case !ti.Equal(tj):
			return ti.Before(tj)
		}
		return out[i].ID < out[j].ID
	})
	return out
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/router/ -race -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/router/
git commit -m "feat(router): order credentials least-recently-used"
```

---

### Task 10: Candidate filtering and skip reasons

**Files:**
- Create: `internal/router/filter.go`
- Test: `internal/router/filter_test.go`

**Interfaces:**
- Consumes: `target`, `Skip`, `Candidate`, `Snapshot`, `Query` from Task 8; `orderCredentials` from Task 9.
- Produces: `func filterTarget(t target, q Query, snap Snapshot, byID map[string]provider.Provider) ([]Candidate, []Skip, bool)` — the bool reports whether the provider was found at all.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

A candidate survives when its provider is enabled, it has a credential neither
credential-cooled nor triple-cooled, its model declares the requested surface,
and its capabilities satisfy the query.

Order of checks matters for the **reason recorded**, not for the outcome. A
provider that is both cooling and lacks the surface should report the surface —
the durable configuration problem — rather than the transient one, because the
skip records are what an operator reads to work out why nothing routed.

- [x] **Step 1: Write the failing test**

Create `internal/router/filter_test.go`:

```go
package router

import (
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

func fleetWith(creds ...provider.Credential) []provider.Provider {
	return []provider.Provider{{
		ID: "groq", Kind: "openaicompat", BaseURL: "https://groq.example/v1",
		Credentials: creds, Models: []string{"llama"},
	}}
}

func snapOf(t *testing.T, ps []provider.Provider, avail health.Availability) Snapshot {
	t.Helper()
	return Snapshot{
		At:        time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC),
		Providers: ps,
		Catalog:   catalog.FromProviders(ps),
		Health:    avail,
	}
}

func byIDOf(ps []provider.Provider) map[string]provider.Provider {
	m := make(map[string]provider.Provider, len(ps))
	for _, p := range ps {
		m[p.ID] = p
	}
	return m
}

func llmQuery() Query { return Query{Model: "llama", Surface: ir.SurfaceLLM} }

func TestFilterProducesOneCandidatePerCredential(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true},
		provider.Credential{ID: "k2", Secret: "b", Enabled: true})
	snap := snapOf(t, ps, health.Availability{})

	cands, skips, found := filterTarget(target{"groq", "llama"}, llmQuery(), snap, byIDOf(ps))
	if !found {
		t.Fatal("provider should have been found")
	}
	if len(skips) != 0 {
		t.Errorf("skips = %+v, want none", skips)
	}
	if len(cands) != 2 || cands[0].KeyID != "k1" || cands[1].KeyID != "k2" {
		t.Fatalf("candidates = %+v", cands)
	}
	if cands[0].ProviderID != "groq" || cands[0].Model != "llama" || cands[0].Kind != "openaicompat" {
		t.Errorf("candidate = %+v", cands[0])
	}
}

func TestFilterSkipsAProviderWithNoCredentials(t *testing.T) {
	ps := fleetWith()
	snap := snapOf(t, ps, health.Availability{})

	cands, skips, _ := filterTarget(target{"groq", "llama"}, llmQuery(), snap, byIDOf(ps))
	if len(cands) != 0 {
		t.Fatalf("candidates = %+v, want none", cands)
	}
	if len(skips) != 1 || skips[0].Reason != SkipNoCredential {
		t.Fatalf("skips = %+v, want one no_credential", skips)
	}
}

func TestFilterSkipsACoolingCredentialAndKeepsTheOther(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true},
		provider.Credential{ID: "k2", Secret: "b", Enabled: true})

	b := health.New(3, time.Minute)
	cooled := health.Key{ProviderID: "groq", KeyID: "k1", Model: "llama"}
	for i := 0; i < 3; i++ {
		b.Record(cooled, health.Signal{Outcome: "retryable_provider", StatusCode: 503})
	}
	snap := snapOf(t, ps, b.SnapshotAvailability(time.Now()))

	cands, skips, _ := filterTarget(target{"groq", "llama"}, llmQuery(), snap, byIDOf(ps))
	if len(cands) != 1 || cands[0].KeyID != "k2" {
		t.Fatalf("candidates = %+v, want only k2", cands)
	}
	if len(skips) != 1 || skips[0].Reason != SkipCooling || skips[0].KeyID != "k1" {
		t.Fatalf("skips = %+v, want one cooling on k1", skips)
	}
}

func TestFilterSkipsAModelTheProviderDoesNotOffer(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true})
	snap := snapOf(t, ps, health.Availability{})

	cands, skips, found := filterTarget(target{"groq", "nope"}, Query{Model: "nope", Surface: ir.SurfaceLLM}, snap, byIDOf(ps))
	if !found {
		t.Fatal("the provider exists even though the model does not")
	}
	if len(cands) != 0 {
		t.Fatalf("candidates = %+v", cands)
	}
	if len(skips) != 1 || skips[0].Reason != SkipSurface {
		t.Fatalf("skips = %+v, want one surface", skips)
	}
}

func TestFilterSkipsTheWrongSurface(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true})
	snap := snapOf(t, ps, health.Availability{})
	q := Query{Model: "llama", Surface: ir.SurfaceEmbeddings}

	cands, skips, _ := filterTarget(target{"groq", "llama"}, q, snap, byIDOf(ps))
	if len(cands) != 0 || len(skips) != 1 || skips[0].Reason != SkipSurface {
		t.Fatalf("candidates=%+v skips=%+v", cands, skips)
	}
}

func TestFilterReportsAnUnknownProvider(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true})
	snap := snapOf(t, ps, health.Availability{})

	cands, skips, found := filterTarget(target{"nosuch", "llama"}, llmQuery(), snap, byIDOf(ps))
	if found {
		t.Fatal("an unknown provider must be reported as not found")
	}
	if len(cands) != 0 || len(skips) != 1 || skips[0].Reason != SkipDisabled {
		t.Fatalf("candidates=%+v skips=%+v", cands, skips)
	}
}

// Phase 3 capabilities are inferred, so a tools requirement admits the
// candidate and the filter is exercised without being selective.
func TestFilterAdmitsInferredCapabilities(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true})
	snap := snapOf(t, ps, health.Availability{})
	q := Query{Model: "llama", Surface: ir.SurfaceLLM, NeedsTools: true, NeedsVision: true}

	cands, skips, _ := filterTarget(target{"groq", "llama"}, q, snap, byIDOf(ps))
	if len(cands) != 1 {
		t.Fatalf("inferred capabilities must admit the candidate, got %+v %+v", cands, skips)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/router/ -run Filter -v`
Expected: FAIL — `undefined: filterTarget`.

- [x] **Step 3: Write the filter**

Create `internal/router/filter.go`:

```go
package router

import (
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
)

// filterTarget turns one resolved target into candidates, recording a Skip for
// every rejection. The bool reports whether the provider exists at all, which
// the caller needs to distinguish "no such provider" from "nothing survived".
//
// The order of the checks decides which reason is recorded when several apply.
// Durable configuration problems are reported ahead of transient ones, because
// the skips are what an operator reads to work out why nothing routed — and
// "cooling" sends them looking at health when the real problem is that the
// model was never offered on that surface.
func filterTarget(t target, q Query, snap Snapshot,
	byID map[string]provider.Provider) ([]Candidate, []Skip, bool) {

	p, ok := byID[t.ProviderID]
	if !ok {
		// Either the provider does not exist or it is disabled; the snapshot
		// only carries enabled providers, so the two are indistinguishable here
		// and "disabled" is the honest label for both.
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipDisabled}}, false
	}

	m, known := snap.Catalog.Lookup(t.ProviderID, t.ModelID)
	if !known || !m.DeclaresSurface(q.Surface) {
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipSurface}}, true
	}

	if !m.Capabilities.Satisfies(catalog.Capabilities{
		Tools: q.NeedsTools, Vision: q.NeedsVision, Reasoning: q.NeedsReasoning,
	}) {
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipCapability}}, true
	}

	if len(p.Credentials) == 0 {
		return nil, []Skip{{ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipNoCredential}}, true
	}

	var cands []Candidate
	var skips []Skip
	for _, c := range orderCredentials(p.ID, p.Credentials, snap.LastUsed) {
		k := health.Key{ProviderID: p.ID, KeyID: c.ID, Model: t.ModelID}
		if !snap.Health.Available(k) {
			skips = append(skips, Skip{
				ProviderID: p.ID, KeyID: c.ID, Model: t.ModelID, Reason: SkipCooling,
			})
			continue
		}
		cands = append(cands, Candidate{
			ProviderID: p.ID, KeyID: c.ID, Model: t.ModelID, Kind: p.Kind,
		})
	}
	return cands, skips, true
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/router/ -race -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/router/
git commit -m "feat(router): filter candidates and record skips"
```

---

### Task 11: Resolve, and the distinguishable empty-result errors

**Files:**
- Create: `internal/router/router.go`
- Test: `internal/router/router_test.go`

**Interfaces:**
- Consumes: everything from Tasks 8 through 10.
- Produces: `func router.Resolve(q Query, snap Snapshot) ([]Candidate, []Skip, error)` and the sentinels `router.ErrModelNotFound`, `ErrSurfaceUnsupported`, `ErrAllCooling`, `ErrCapabilityUnsatisfied`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Zero surviving candidates returns a **distinguishable** error naming which case
applies. "No provider offers the model", "no provider offers it on this surface",
and "every provider offering it is cooling" are different problems with different
fixes, and conflating them turns a five-second diagnosis into a hunt.

`ErrCapabilityUnsatisfied` is a fourth sentinel the spec does not list. It is
unreachable in Phase 3, because inferred capabilities admit everything — but
Phase 6 makes it reachable, and returning `ErrSurfaceUnsupported` for a
capability mismatch would be actively misleading once it can happen.

The signature returns skips alongside candidates rather than the spec's literal
two-value form, because §3 requires both to be persisted and a router that
computed skips without returning them would be useless.

`Resolve` does **not** apply `policy.retry.max_attempts`. The full ordered list
is returned so the trace records everything that was eligible; the attempt loop
truncates. Truncating here would make the recorded candidate list lie about what
the router decided.

- [x] **Step 1: Write the failing test**

Create `internal/router/router_test.go`:

```go
package router

import (
	"errors"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

func twoProviders() []provider.Provider {
	return []provider.Provider{
		{ID: "groq", Kind: "openaicompat", BaseURL: "https://groq.example/v1",
			Priority: 10, Models: []string{"shared"},
			Credentials: []provider.Credential{
				{ID: "g1", Secret: "a", Enabled: true},
				{ID: "g2", Secret: "b", Enabled: true},
			}},
		{ID: "cerebras", Kind: "openaicompat", BaseURL: "https://cerebras.example/v1",
			Priority: 5, Models: []string{"shared"},
			Credentials: []provider.Credential{{ID: "c1", Secret: "c", Enabled: true}}},
	}
}

func fullSnap(ps []provider.Provider, cfg *config.Config, avail health.Availability) Snapshot {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return Snapshot{
		At:        time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC),
		Providers: ps,
		Catalog:   catalog.FromProviders(ps),
		Config:    cfg,
		Health:    avail,
	}
}

func seq(cands []Candidate) [][2]string {
	out := make([][2]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, [2]string{c.ProviderID, c.KeyID})
	}
	return out
}

// Credentials rotate before providers: that is the whole point of holding
// several free-tier keys.
func TestResolveDrainsCredentialsBeforeAdvancingProviders(t *testing.T) {
	ps := twoProviders()
	cands, skips, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM},
		fullSnap(ps, nil, health.Availability{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Errorf("skips = %+v", skips)
	}
	got := seq(cands)
	want := [][2]string{{"groq", "g1"}, {"groq", "g2"}, {"cerebras", "c1"}}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("sequence = %v, want %v", got, want)
		}
	}
	if len(got) != 3 {
		t.Fatalf("sequence = %v, want exactly 3", got)
	}
}

func TestResolveExpandsAnAliasInChainOrder(t *testing.T) {
	ps := twoProviders()
	cfg := &config.Config{Aliases: map[string][]string{
		"fast": {"cerebras/shared", "groq/shared"},
	}}
	cands, _, err := Resolve(Query{Model: "fast", Surface: ir.SurfaceLLM},
		fullSnap(ps, cfg, health.Availability{}))
	if err != nil {
		t.Fatal(err)
	}
	got := seq(cands)
	if len(got) != 3 || got[0] != [2]string{"cerebras", "c1"} || got[1] != [2]string{"groq", "g1"} {
		t.Errorf("sequence = %v, want cerebras first", got)
	}
}

func TestResolveUnknownModel(t *testing.T) {
	_, _, err := Resolve(Query{Model: "nope", Surface: ir.SurfaceLLM},
		fullSnap(twoProviders(), nil, health.Availability{}))
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("err = %v, want ErrModelNotFound", err)
	}
}

func TestResolveWrongSurfaceIsDistinguishable(t *testing.T) {
	_, skips, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceEmbeddings},
		fullSnap(twoProviders(), nil, health.Availability{}))
	if !errors.Is(err, ErrSurfaceUnsupported) {
		t.Fatalf("err = %v, want ErrSurfaceUnsupported", err)
	}
	if len(skips) != 2 {
		t.Errorf("skips = %+v, want one per provider", skips)
	}
}

func TestResolveEverythingCoolingIsDistinguishable(t *testing.T) {
	ps := twoProviders()
	b := health.New(3, time.Minute)
	for _, k := range []health.Key{
		{ProviderID: "groq", KeyID: "g1", Model: "shared"},
		{ProviderID: "groq", KeyID: "g2", Model: "shared"},
		{ProviderID: "cerebras", KeyID: "c1", Model: "shared"},
	} {
		for i := 0; i < 3; i++ {
			b.Record(k, health.Signal{Outcome: "retryable_provider", StatusCode: 503})
		}
	}
	snap := fullSnap(ps, nil, b.SnapshotAvailability(time.Now()))

	cands, skips, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM}, snap)
	if !errors.Is(err, ErrAllCooling) {
		t.Fatalf("err = %v, want ErrAllCooling", err)
	}
	if len(cands) != 0 {
		t.Errorf("candidates = %+v", cands)
	}
	// The skips must still explain the ordering without live health.
	if len(skips) != 3 {
		t.Fatalf("skips = %+v, want three", skips)
	}
	for _, s := range skips {
		if s.Reason != SkipCooling {
			t.Errorf("skip = %+v, want cooling", s)
		}
	}
}

// One cooling credential does not make the request fail; it makes the sequence
// shorter and the trace explain why.
func TestResolveSkipsOneCoolingCredentialAndContinues(t *testing.T) {
	ps := twoProviders()
	b := health.New(3, time.Minute)
	k := health.Key{ProviderID: "groq", KeyID: "g1", Model: "shared"}
	for i := 0; i < 3; i++ {
		b.Record(k, health.Signal{Outcome: "retryable_provider", StatusCode: 503})
	}
	snap := fullSnap(ps, nil, b.SnapshotAvailability(time.Now()))

	cands, skips, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM}, snap)
	if err != nil {
		t.Fatal(err)
	}
	got := seq(cands)
	if len(got) != 2 || got[0] != [2]string{"groq", "g2"} {
		t.Errorf("sequence = %v, want g2 then cerebras", got)
	}
	if len(skips) != 1 || skips[0].KeyID != "g1" || skips[0].Reason != SkipCooling {
		t.Errorf("skips = %+v", skips)
	}
}

// Purity: the same snapshot must give the same answer every time, and Resolve
// must not consult a clock.
func TestResolveIsDeterministic(t *testing.T) {
	ps := twoProviders()
	snap := fullSnap(ps, nil, health.Availability{})
	first, _, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM}, snap)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, _, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM}, snap)
		if err != nil {
			t.Fatal(err)
		}
		a, b := seq(first), seq(again)
		for j := range a {
			if a[j] != b[j] {
				t.Fatalf("run %d diverged: %v vs %v", i, a, b)
			}
		}
	}
}

func TestResolveDoesNotTruncateToMaxAttempts(t *testing.T) {
	ps := twoProviders()
	cfg := &config.Config{Policy: config.PolicyConfig{Retry: config.RetryConfig{MaxAttempts: 1}}}
	cands, _, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM},
		fullSnap(ps, cfg, health.Availability{}))
	if err != nil {
		t.Fatal(err)
	}
	// The trace must record everything that was eligible; the loop truncates.
	if len(cands) != 3 {
		t.Errorf("got %d candidates, want the full list of 3", len(cands))
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/router/ -run Resolve -v`
Expected: FAIL — `undefined: Resolve`.

- [x] **Step 3: Write Resolve**

Create `internal/router/router.go`:

```go
package router

import (
	"errors"

	"github.com/darkraise/darkrouter/internal/provider"
)

// The empty-result cases are separate sentinels because they are separate
// problems with separate fixes. Collapsing them into one "no candidates" error
// turns a five-second diagnosis into a hunt.
var (
	ErrModelNotFound         = errors.New("no provider offers this model")
	ErrSurfaceUnsupported    = errors.New("no provider offers this model on this surface")
	ErrAllCooling            = errors.New("every provider offering this model is cooling")
	ErrCapabilityUnsatisfied = errors.New("no provider offering this model has the required capabilities")
)

// Resolve produces the ordered candidate sequence and the skips explaining
// every target that did not make it.
//
// It is a pure function of its inputs: no clock, no database, no network. The
// evaluation instant and resolved health arrive in the snapshot, which is what
// makes the time-dependent cooling cases testable without fixtures.
//
// The returned list is not truncated to policy.retry.max_attempts. The trace
// records what was eligible; the attempt loop decides how much of it to spend.
func Resolve(q Query, snap Snapshot) ([]Candidate, []Skip, error) {
	byID := make(map[string]provider.Provider, len(snap.Providers))
	providerIDs := make(map[string]bool, len(snap.Providers))
	for _, p := range snap.Providers {
		byID[p.ID] = p
		providerIDs[p.ID] = true
	}

	var aliases map[string][]string
	if snap.Config != nil {
		aliases = snap.Config.Aliases
	}

	targets, _ := resolveModel(q.Model, aliases, providerIDs, snap.Catalog)
	if len(targets) == 0 {
		return nil, nil, ErrModelNotFound
	}

	var cands []Candidate
	var skips []Skip
	for _, t := range targets {
		c, s, _ := filterTarget(t, q, snap, byID)
		cands = append(cands, c...)
		skips = append(skips, s...)
	}

	if len(cands) == 0 {
		return nil, skips, emptyReason(skips)
	}
	return cands, skips, nil
}

// emptyReason picks the error that best explains an empty candidate list.
//
// Cooling wins only when it explains *everything*: a mixed result where one
// provider lacks the surface and another is cooling is a configuration problem
// wearing a health problem's clothes, and reporting "cooling" would send the
// operator to the wrong place.
func emptyReason(skips []Skip) error {
	if len(skips) == 0 {
		return ErrModelNotFound
	}
	allCooling := true
	sawSurface, sawCapability := false, false
	for _, s := range skips {
		switch s.Reason {
		case SkipCooling:
		case SkipSurface:
			allCooling, sawSurface = false, true
		case SkipCapability:
			allCooling, sawCapability = false, true
		default:
			allCooling = false
		}
	}
	switch {
	case allCooling:
		return ErrAllCooling
	case sawSurface:
		return ErrSurfaceUnsupported
	case sawCapability:
		return ErrCapabilityUnsatisfied
	default:
		return ErrModelNotFound
	}
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/router/ -race -v`
Expected: PASS across the whole package.

- [x] **Step 5: Commit**

```bash
git add internal/router/
git commit -m "feat(router): resolve candidates deterministically"
```

---

### Task 12: Per-attempt deadlines and the budget gate

**Files:**
- Create: `internal/exec/deadline.go`
- Test: `internal/exec/deadline_test.go`

**Interfaces:**
- Consumes: `config.TimeoutConfig` from Phase 1.
- Produces: `exec.budget` with `func newBudget(t config.TimeoutConfig, start time.Time) budget`, `func (budget) remaining(now time.Time) time.Duration`, `func (budget) canStartAttempt(now time.Time) bool`, `func (budget) attemptDeadline(now time.Time) time.Time`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

An attempt is only started when the remaining total is at least
`connect + first_byte`. Beginning an attempt that cannot possibly complete wastes
both the budget and the provider's quota, and it turns a clean
attempts-exhausted error into a bare timeout that says nothing.

Worst-case pre-commit silence is `max_attempts × first_byte` — four minutes with
the defaults. That is longer than some clients tolerate, and it is the reason
`first_byte` defaults to 60s rather than higher. Do not raise it here.

- [x] **Step 1: Write the failing test**

Create `internal/exec/deadline_test.go`:

```go
package exec

import (
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

func timeouts() config.TimeoutConfig {
	return config.TimeoutConfig{
		Connect: 10 * time.Second, FirstByte: 60 * time.Second,
		Total: 10 * time.Minute, Idle: 120 * time.Second,
	}
}

func TestBudgetRemainingCountsDownFromTotal(t *testing.T) {
	start := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	b := newBudget(timeouts(), start)
	if got := b.remaining(start); got != 10*time.Minute {
		t.Errorf("remaining at start = %s, want 10m", got)
	}
	if got := b.remaining(start.Add(4 * time.Minute)); got != 6*time.Minute {
		t.Errorf("remaining = %s, want 6m", got)
	}
	if got := b.remaining(start.Add(11 * time.Minute)); got != 0 {
		t.Errorf("remaining past total = %s, want 0", got)
	}
}

// An attempt that cannot possibly complete must not be started.
func TestBudgetGateRefusesAnAttemptThatCannotFinish(t *testing.T) {
	start := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	b := newBudget(timeouts(), start)

	// 10m total, 70s needed per attempt.
	if !b.canStartAttempt(start.Add(8 * time.Minute)) {
		t.Error("2m remaining is more than connect+first_byte; the attempt should start")
	}
	if b.canStartAttempt(start.Add(9*time.Minute + 1*time.Second)) {
		t.Error("59s remaining is less than connect+first_byte; the attempt must be refused")
	}
	if b.canStartAttempt(start.Add(10 * time.Minute)) {
		t.Error("no budget left; the attempt must be refused")
	}
}

// The per-attempt deadline is the smaller of connect+first_byte and whatever
// remains, so a long-running request cannot overrun its total.
func TestAttemptDeadlineIsBoundedByBothLimits(t *testing.T) {
	start := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	b := newBudget(timeouts(), start)

	if got := b.attemptDeadline(start); !got.Equal(start.Add(70 * time.Second)) {
		t.Errorf("deadline = %s, want start+70s", got)
	}
	// With 90s left, connect+first_byte (70s) still wins.
	at := start.Add(8*time.Minute + 30*time.Second)
	if got := b.attemptDeadline(at); !got.Equal(at.Add(70 * time.Second)) {
		t.Errorf("deadline = %s, want +70s", got)
	}
	// With 75s left the remaining total wins by 5s.
	at = start.Add(8*time.Minute + 45*time.Second)
	if got := b.attemptDeadline(at); !got.Equal(start.Add(10 * time.Minute)) {
		t.Errorf("deadline = %s, want the total deadline", got)
	}
}

func TestBudgetHandlesAZeroTotal(t *testing.T) {
	tc := timeouts()
	tc.Total = 0 // config defaults prevent this, but the type must not divide by it
	b := newBudget(tc, time.Now())
	if b.canStartAttempt(time.Now()) {
		t.Error("a zero total must refuse every attempt rather than allowing all")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/exec/ -run Budget -v`
Expected: FAIL — `undefined: newBudget`.

- [x] **Step 3: Write the budget**

Create `internal/exec/deadline.go`:

```go
package exec

import (
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// budget tracks what remains of policy.timeout.total and decides whether
// another attempt is worth starting.
//
// It takes instants rather than reading a clock so the gate is testable without
// sleeping, the same reason the router takes its evaluation instant as input.
type budget struct {
	deadline time.Time
	perTry   time.Duration
}

func newBudget(t config.TimeoutConfig, start time.Time) budget {
	return budget{
		deadline: start.Add(t.Total),
		// Pre-commit, an attempt needs to connect and then receive headers.
		perTry: t.Connect + t.FirstByte,
	}
}

func (b budget) remaining(now time.Time) time.Duration {
	d := b.deadline.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// canStartAttempt reports whether enough budget remains for an attempt to
// possibly complete. Starting one that cannot wastes the budget and the
// provider's quota, and replaces a clear attempts-exhausted error with a bare
// timeout.
func (b budget) canStartAttempt(now time.Time) bool {
	return b.remaining(now) >= b.perTry
}

// attemptDeadline is the earlier of connect+first_byte and the remaining total.
func (b budget) attemptDeadline(now time.Time) time.Time {
	byTry := now.Add(b.perTry)
	if byTry.After(b.deadline) {
		return b.deadline
	}
	return byTry
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/exec/ -race -v`
Expected: PASS, including the Phase 1–2 tests.

- [x] **Step 5: Commit**

```bash
git add internal/exec/
git commit -m "feat(exec): bound attempts by the total budget"
```

---

### Task 13: The bounded pre-commit event buffer

**Files:**
- Create: `internal/exec/commit.go`
- Test: `internal/exec/commit_test.go`

**Interfaces:**
- Consumes: `ir.StreamEvent` from Phase 1.
- Produces: `exec.preCommitBuffer` with `func newPreCommitBuffer(maxBytes int) *preCommitBuffer`, `func (*preCommitBuffer) add(ev ir.StreamEvent) error`, `func (*preCommitBuffer) events() []ir.StreamEvent`, `func exec.IsContentBearing(ev ir.StreamEvent) bool`, and `exec.ErrPreCommitBufferFull`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Pre-commit events from the in-flight attempt are buffered and **replayed at
commit**; events from failed attempts are discarded. Without buffer-and-replay
the client receives attempt one's `message_start` followed by attempt two's
content, or no `message_start` at all.

The buffer is bounded in **bytes as well as by `first_byte` in time** — a
provider can emit megabytes of pings inside sixty seconds — and a cap breach
classifies as an attempt failure rather than as a client error.

What counts as content is load-bearing. `message_start`, pings, keepalives, and
role-only deltas do **not** commit: committing on a keepalive forfeits failover
for nothing. Thinking **does** count, because a reasoning model can legitimately
think for a minute before its first text token and holding the response open
that long would blow the pre-commit budget.

- [x] **Step 1: Write the failing test**

Create `internal/exec/commit_test.go`:

```go
package exec

import (
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestContentBearingEvents(t *testing.T) {
	cases := []struct {
		name string
		ev   ir.StreamEvent
		want bool
	}{
		{"message start does not commit", ir.StreamEvent{Type: ir.EventMessageStart}, false},
		{"ping does not commit", ir.StreamEvent{Type: ir.EventPing}, false},
		{"block start does not commit", ir.StreamEvent{Type: ir.EventBlockStart}, false},
		{"empty delta does not commit", ir.StreamEvent{
			Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: ""}}, false},
		{"text delta commits", ir.StreamEvent{
			Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}}, true},
		// Thinking commits: a reasoning model can think for a minute before its
		// first text token, and holding the response open that long would blow
		// the pre-commit budget.
		{"thinking delta commits", ir.StreamEvent{
			Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: "hmm"}}, true},
		{"tool input commits", ir.StreamEvent{
			Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockToolUse, ToolInput: `{"a`}}, true},
		{"nil delta does not commit", ir.StreamEvent{Type: ir.EventContentDelta}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsContentBearing(tc.ev); got != tc.want {
				t.Errorf("IsContentBearing = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBufferReplaysEventsExactlyOnceInOrder(t *testing.T) {
	b := newPreCommitBuffer(1 << 20)
	for _, ev := range []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "x", Model: "m"},
		{Type: ir.EventPing},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}},
	} {
		if err := b.add(ev); err != nil {
			t.Fatal(err)
		}
	}
	got := b.events()
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[0].Type != ir.EventMessageStart || got[2].Delta.Text != "hi" {
		t.Errorf("events = %+v", got)
	}
	// Replaying twice would duplicate the client's stream.
	if len(b.events()) != 3 {
		t.Error("events() is not repeatable")
	}
}

// A ping flood must breach the byte cap rather than waiting out first_byte.
func TestBufferBreachesTheByteCapOnAPingFlood(t *testing.T) {
	b := newPreCommitBuffer(256)
	var err error
	for i := 0; i < 10000 && err == nil; i++ {
		err = b.add(ir.StreamEvent{
			Type:  ir.EventContentDelta,
			Delta: &ir.Delta{Type: ir.BlockText, Text: strings.Repeat("x", 64)},
		})
	}
	if !errors.Is(err, ErrPreCommitBufferFull) {
		t.Fatalf("err = %v, want ErrPreCommitBufferFull", err)
	}
}

func TestBufferCountsOnlyPayloadBytes(t *testing.T) {
	b := newPreCommitBuffer(100)
	// Fifty pings carry no payload and must not exhaust a byte cap on their own;
	// the time bound is what stops an infinite ping stream.
	for i := 0; i < 50; i++ {
		if err := b.add(ir.StreamEvent{Type: ir.EventPing}); err != nil {
			t.Fatalf("ping %d exhausted the byte cap: %v", i, err)
		}
	}
}

func TestBufferWithNoCapIsUnbounded(t *testing.T) {
	b := newPreCommitBuffer(0)
	for i := 0; i < 1000; i++ {
		if err := b.add(ir.StreamEvent{
			Type:  ir.EventContentDelta,
			Delta: &ir.Delta{Type: ir.BlockText, Text: strings.Repeat("y", 100)},
		}); err != nil {
			t.Fatalf("a zero cap must mean unbounded, got %v", err)
		}
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/exec/ -run 'Buffer|ContentBearing' -v`
Expected: FAIL — `undefined: newPreCommitBuffer`.

- [x] **Step 3: Write the buffer**

Create `internal/exec/commit.go`:

```go
package exec

import (
	"errors"

	"github.com/darkraise/darkrouter/internal/ir"
)

// ErrPreCommitBufferFull means an attempt emitted more pre-commit payload than
// the cap allows. It is an attempt failure, not a client error: the provider is
// misbehaving and another one may not.
var ErrPreCommitBufferFull = errors.New("exec: pre-commit buffer exceeded")

// IsContentBearing reports whether an event commits the response.
//
// message_start, pings, keepalives, and role-only deltas do not: committing on
// a keepalive forfeits failover for nothing. Thinking does, because a reasoning
// model can legitimately think for a minute before its first text token, and
// holding the response open that long would blow the pre-commit budget.
func IsContentBearing(ev ir.StreamEvent) bool {
	if ev.Type != ir.EventContentDelta || ev.Delta == nil {
		return false
	}
	switch ev.Delta.Type {
	case ir.BlockText:
		return ev.Delta.Text != ""
	case ir.BlockThinking, ir.BlockRedactedThinking:
		return ev.Delta.Thinking != ""
	case ir.BlockToolUse:
		return ev.Delta.ToolInput != ""
	default:
		return false
	}
}

// preCommitBuffer holds the in-flight attempt's events until it commits.
//
// It is bounded in bytes as well as by first_byte in time: a provider can emit
// megabytes of pings inside sixty seconds, and the time bound alone would let it.
type preCommitBuffer struct {
	evs      []ir.StreamEvent
	bytes    int
	maxBytes int
}

// newPreCommitBuffer returns a buffer capped at maxBytes of payload. A
// non-positive cap means unbounded.
func newPreCommitBuffer(maxBytes int) *preCommitBuffer {
	return &preCommitBuffer{maxBytes: maxBytes}
}

func (b *preCommitBuffer) add(ev ir.StreamEvent) error {
	n := payloadBytes(ev)
	if b.maxBytes > 0 && b.bytes+n > b.maxBytes {
		return ErrPreCommitBufferFull
	}
	b.bytes += n
	b.evs = append(b.evs, ev)
	return nil
}

// events returns the buffered sequence for replay. It is repeatable, but the
// caller must replay exactly once — twice would duplicate the client's stream.
func (b *preCommitBuffer) events() []ir.StreamEvent { return b.evs }

// payloadBytes counts only what a provider can inflate. Pings carry nothing, so
// a ping flood is stopped by the time bound rather than by this cap.
func payloadBytes(ev ir.StreamEvent) int {
	if ev.Delta == nil {
		return 0
	}
	return len(ev.Delta.Text) + len(ev.Delta.Thinking) + len(ev.Delta.ToolInput)
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/exec/ -race -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/exec/
git commit -m "feat(exec): buffer pre-commit stream events"
```

---

### Task 14: Advance semantics as a pure function

**Files:**
- Create: `internal/exec/advance.go`
- Test: `internal/exec/advance_test.go`

**Interfaces:**
- Consumes: `adapter.Outcome` from Phase 1, `router.Candidate` from Task 8.
- Produces: `exec.advanceAction` with the constants `actionFinish`, `actionReturn`, `actionNext`, and `func nextIndex(cands []router.Candidate, i int, o adapter.Outcome, statusCode int) (int, advanceAction)`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Extracting this from the loop is what makes the whole outcome table testable as
a table, with no fake fleet and no network. The loop then has one job: run an
attempt and do what this says.

The distinction that matters most: on a **429** the next credential of the same
provider is worth trying, because rate limits are per credential. On **anything
else retryable** the provider itself is in trouble and every remaining
credential will hit the same wall, so they are skipped wholesale.

- [x] **Step 1: Write the failing test**

Create `internal/exec/advance_test.go`:

```go
package exec

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/router"
)

// groq has two credentials, cerebras one.
func chain() []router.Candidate {
	return []router.Candidate{
		{ProviderID: "groq", KeyID: "g1", Model: "m"},
		{ProviderID: "groq", KeyID: "g2", Model: "m"},
		{ProviderID: "cerebras", KeyID: "c1", Model: "m"},
	}
}

func TestAdvanceTable(t *testing.T) {
	cases := []struct {
		name       string
		from       int
		outcome    adapter.Outcome
		statusCode int
		wantIndex  int
		wantAction advanceAction
	}{
		{"success finishes", 0, adapter.OutcomeSuccess, 200, 0, actionFinish},
		{"fatal returns immediately", 0, adapter.OutcomeFatal, 422, 0, actionReturn},
		{"client cancellation stops", 0, adapter.OutcomeClientCancelled, 0, 0, actionReturn},

		// 429 is per credential, so the next key on the same provider is worth trying.
		{"429 tries the next credential", 0, adapter.OutcomeRetryableProvider, 429, 1, actionNext},
		{"429 on the last credential advances the provider", 1, adapter.OutcomeRetryableProvider, 429, 2, actionNext},

		// Everything else retryable means the provider is down; its remaining
		// credentials will hit the same wall.
		{"503 skips the provider's remaining credentials", 0, adapter.OutcomeRetryableProvider, 503, 2, actionNext},
		{"timeout skips the provider's remaining credentials", 0, adapter.OutcomeRetryableProvider, 0, 2, actionNext},

		// A bad credential is worth rotating past.
		{"401 tries the next credential", 0, adapter.OutcomeRetryableCredential, 401, 1, actionNext},
		{"402 on the last credential advances the provider", 1, adapter.OutcomeRetryableCredential, 402, 2, actionNext},

		// An unknown model says nothing about the credential.
		{"404 advances one step", 0, adapter.OutcomeRetryableModel, 404, 1, actionNext},

		// Running off the end is exhaustion, not a wrap-around.
		{"503 on the last provider exhausts", 2, adapter.OutcomeRetryableProvider, 503, 3, actionNext},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotIdx, gotAct := nextIndex(chain(), tc.from, tc.outcome, tc.statusCode)
			if gotIdx != tc.wantIndex || gotAct != tc.wantAction {
				t.Errorf("nextIndex = %d/%v, want %d/%v", gotIdx, gotAct, tc.wantIndex, tc.wantAction)
			}
		})
	}
}

// A provider with three credentials must be skipped in one step, not three.
func TestAdvanceSkipsEveryRemainingCredentialOfTheProvider(t *testing.T) {
	cands := []router.Candidate{
		{ProviderID: "groq", KeyID: "g1"},
		{ProviderID: "groq", KeyID: "g2"},
		{ProviderID: "groq", KeyID: "g3"},
		{ProviderID: "cerebras", KeyID: "c1"},
	}
	got, act := nextIndex(cands, 0, adapter.OutcomeRetryableProvider, 500)
	if got != 3 || act != actionNext {
		t.Errorf("nextIndex = %d/%v, want 3/next", got, act)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/exec/ -run Advance -v`
Expected: FAIL — `undefined: nextIndex`.

- [x] **Step 3: Write the advance rules**

Create `internal/exec/advance.go`:

```go
package exec

import (
	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/router"
)

type advanceAction int

const (
	// actionFinish: the attempt succeeded; serve it.
	actionFinish advanceAction = iota
	// actionReturn: stop the chain and return this outcome to the client.
	actionReturn
	// actionNext: continue at the returned index, which may be past the end.
	actionNext
)

// nextIndex applies master design §8.1's advance behavior.
//
// The returned index may be len(cands), which means the chain is exhausted.
func nextIndex(cands []router.Candidate, i int, o adapter.Outcome, statusCode int) (int, advanceAction) {
	switch o {
	case adapter.OutcomeSuccess:
		return i, actionFinish

	case adapter.OutcomeFatal:
		// One malformed client request must not become a fleet-wide burst of
		// identical failures.
		return i, actionReturn

	case adapter.OutcomeClientCancelled:
		return i, actionReturn

	case adapter.OutcomeRetryableCredential, adapter.OutcomeRetryableModel:
		// A bad credential or a missing model says nothing about the provider's
		// other credentials, so step one at a time.
		return i + 1, actionNext

	case adapter.OutcomeRetryableProvider:
		if statusCode == 429 {
			// Rate limits are per credential: the next key is worth trying.
			return i + 1, actionNext
		}
		// The upstream is down. Every remaining credential on this provider
		// will hit the same wall, so skip them all in one step.
		return skipProvider(cands, i), actionNext

	default:
		return i + 1, actionNext
	}
}

// skipProvider returns the index of the first candidate belonging to a
// different provider than cands[i].
func skipProvider(cands []router.Candidate, i int) int {
	if i >= len(cands) {
		return i
	}
	id := cands[i].ProviderID
	j := i + 1
	for j < len(cands) && cands[j].ProviderID == id {
		j++
	}
	return j
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/exec/ -race -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/exec/
git commit -m "feat(exec): add outcome advance semantics"
```

---

### Task 15: The attempt loop for unary responses

**Files:**
- Modify: `internal/exec/exec.go`
- Test: `internal/exec/loop_test.go`

**Interfaces:**
- Consumes: `router.Resolve` from Task 11, `budget` from Task 12, `nextIndex` from Task 14.
- Produces: `exec.Fleet` interface and `Deps.Fleet`, plus `func (*Executor) Handle` driving multiple candidates.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

`Deps` gains a **second** field rather than widening `Deps.Health`. The same
`*health.Breaker` satisfies both, but widening the existing interface would make
this a shared-interface change rippling into every fake — and Rule S would then
require splitting a task that is already one coherent change.

Availability is re-checked **per attempt**, not just at snapshot time, because
another request may have tripped a breaker in the meantime. The ordered list is
fixed at snapshot time and never re-ordered; only skipping is dynamic. A skip
for that reason is recorded on the trace so it still explains the realized
sequence.

`MarkUsed` fires at **attempt start**, before the request is sent. A credential
that always 401s would otherwise keep a stale timestamp and sort first forever.

- [x] **Step 1: Write the failing test**

Create `internal/exec/loop_test.go`:

```go
package exec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// fleetSource serves a fixed provider set, standing in for the SQLite source.
type fleetSource struct{ ps []provider.Provider }

func (f *fleetSource) Providers(context.Context) ([]provider.Provider, error) { return f.ps, nil }
func (f *fleetSource) Revision() uint64                                      { return 1 }

// handlerFor builds an upstream whose behavior is scripted per credential, so a
// test can say "this key 429s and that one succeeds".
type scripted struct {
	mu   sync.Mutex
	seen []string
	by   map[string]http.HandlerFunc
}

func (s *scripted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	s.seen = append(s.seen, key)
	h, ok := s.by[key]
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(500)
		return
	}
	h(w, r)
}

func (s *scripted) order() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func ok200(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"message":
		{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}

// loopExecutor wires a real breaker, a scripted upstream, and a fleet whose
// credentials are the secrets the upstream scripts on.
func loopExecutor(t *testing.T, up *httptest.Server, fleet []provider.Provider,
	logger *captureLogger) (*Executor, *health.Breaker) {

	t.Helper()
	for i := range fleet {
		fleet[i].BaseURL = up.URL
		fleet[i].Kind = "openaicompat"
	}
	dir := t.TempDir()
	path := dir + "/darkrouter.yaml"
	if err := writeFile(path, "server:\n  proxy_listen: :0\n  admin_listen: :0\n"); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	b := health.New(3, 15*time.Minute)
	e := New(cfgStore, &fleetSource{ps: fleet}, openaicompatAdapter(), Deps{
		Log: logger, Health: b, Fleet: b,
	})
	return e, b
}

func twoKeyFleet() []provider.Provider {
	return []provider.Provider{{
		ID: "groq", Models: []string{"m"},
		Credentials: []provider.Credential{
			{ID: "g1", Secret: "g1", Enabled: true},
			{ID: "g2", Secret: "g2", Enabled: true},
		}}}
}

func twoProviderFleet() []provider.Provider {
	return []provider.Provider{
		{ID: "groq", Priority: 10, Models: []string{"m"},
			Credentials: []provider.Credential{
				{ID: "g1", Secret: "g1", Enabled: true},
				{ID: "g2", Secret: "g2", Enabled: true},
			}},
		{ID: "cerebras", Priority: 5, Models: []string{"m"},
			Credentials: []provider.Credential{{ID: "c1", Secret: "c1", Enabled: true}}},
	}
}

// A done criterion: two credentials on one provider are both exercised on a 429.
func TestLoop429RotatesToTheSecondCredential(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": status(429),
		"g2": ok200,
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoKeyFleet(), logger)
	rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := sc.order(); len(got) != 2 || got[0] != "g1" || got[1] != "g2" {
		t.Errorf("credential order = %v, want [g1 g2]", got)
	}
	r := logger.only(t)
	if len(r.Attempts) != 2 {
		t.Fatalf("attempts = %+v, want 2", r.Attempts)
	}
	if r.Attempts[0].Outcome != "retryable_provider" || r.Attempts[1].Outcome != "success" {
		t.Errorf("outcomes = %s, %s", r.Attempts[0].Outcome, r.Attempts[1].Outcome)
	}
	if rec.Header().Get("X-Darkrouter-Attempts") != "2" {
		t.Errorf("attempts header = %q", rec.Header().Get("X-Darkrouter-Attempts"))
	}
}

// A done criterion: both credentials are skipped on a 5xx.
func TestLoop5xxSkipsTheProvidersRemainingCredentials(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": status(503),
		"g2": status(503),
		"c1": ok200,
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger)
	if rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	got := sc.order()
	if len(got) != 2 || got[0] != "g1" || got[1] != "c1" {
		t.Errorf("order = %v, want [g1 c1] — g2 must be skipped wholesale", got)
	}
}

// A done criterion: a malformed request produces exactly one attempt.
func TestLoopFatalProducesExactlyOneAttempt(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": status(422), "g2": status(422), "c1": status(422),
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger)
	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	if got := sc.order(); len(got) != 1 {
		t.Errorf("attempts = %v, want exactly 1 — a fatal must not burn candidates", got)
	}
	if r := logger.only(t); len(r.Attempts) != 1 {
		t.Errorf("recorded attempts = %d, want 1", len(r.Attempts))
	}
}

// A done criterion: an unknown model advances without penalizing anyone.
func TestLoop404AdvancesWithoutCoolingTheProvider(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": status(404), "g2": status(404), "c1": ok200,
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, b := loopExecutor(t, up, twoProviderFleet(), logger)
	if rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	// 404 steps one at a time, so both groq keys are tried.
	if got := sc.order(); len(got) != 3 {
		t.Errorf("order = %v, want all three tried", got)
	}
	if !b.Available(health.Key{ProviderID: "groq", KeyID: "g1", Model: "m"}) {
		t.Error("a 404 must not cool the provider")
	}
}

func TestLoopExhaustionReturnsTheLastError(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": status(503), "g2": status(503), "c1": status(503),
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger)
	rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if rec.Code < 500 {
		t.Errorf("status = %d, want an upstream error", rec.Code)
	}
	r := logger.only(t)
	if r.Status != "error" {
		t.Errorf("Status = %q", r.Status)
	}
	if len(r.Attempts) != 2 {
		t.Errorf("attempts = %d, want 2 (groq skipped wholesale, then cerebras)", len(r.Attempts))
	}
}

func TestLoopHonoursMaxAttempts(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": status(429), "g2": status(429), "c1": ok200,
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoProviderFleet(), logger)
	// Rewrite the config with a budget of two attempts.
	setMaxAttempts(t, e, 2)

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if got := sc.order(); len(got) != 2 {
		t.Errorf("attempts = %v, want exactly 2", got)
	}
}

func TestLoopMarksCredentialsUsedAtAttemptStart(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": status(429), "g2": ok200}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, b := loopExecutor(t, up, twoKeyFleet(), &captureLogger{})
	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	lu := b.LastUsedSnapshot()
	// Both were dispatched to, including the one that failed — a credential
	// that always fails must not keep a stale timestamp and sort first forever.
	if _, ok := lu[health.CredKey{ProviderID: "groq", KeyID: "g1"}]; !ok {
		t.Error("the failing credential was not marked used")
	}
	if _, ok := lu[health.CredKey{ProviderID: "groq", KeyID: "g2"}]; !ok {
		t.Error("the succeeding credential was not marked used")
	}
}

func TestLoopUnroutableModelMakesNoAttempt(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": ok200}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoKeyFleet(), logger)
	rec := post(t, e, `{"model":"nope","messages":[]}`)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if len(sc.order()) != 0 {
		t.Error("an unroutable model must not consume an attempt")
	}
	if r := logger.only(t); len(r.Attempts) != 0 {
		t.Errorf("attempts = %d, want 0", len(r.Attempts))
	}
}
```

The test file's imports are `context`, `net/http`, `net/http/httptest`,
`strings`, `sync`, `testing`, `time`, plus `internal/config`,
`internal/edge/openai`, `internal/health`, `internal/provider`, and
`internal/store`.

Add these two helpers beside the others in `internal/exec/exec_test.go`:

```go
func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}

func openaicompatAdapter() adapter.Adapter { return openaicompat.New() }

// setMaxAttempts rewrites the executor's config file and reloads it.
func setMaxAttempts(t *testing.T, e *Executor, n int) {
	t.Helper()
	body := fmt.Sprintf("server:\n  proxy_listen: :0\n  admin_listen: :0\n"+
		"policy:\n  retry:\n    max_attempts: %d\n", n)
	if err := os.WriteFile(e.store.Path(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Reload(); err != nil {
		t.Fatal(err)
	}
}
```

`config.Store` has no `Path()` accessor. Add one to `internal/config/store.go`:

```go
// Path reports the file the store watches. Tests rewrite it to exercise a
// reload; nothing on the request path needs it.
func (s *Store) Path() string { return s.path }
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/exec/ -run Loop -v`
Expected: FAIL — `unknown field Fleet in struct literal of type Deps`.

- [x] **Step 3: Add the Fleet dependency**

In `internal/exec/exec.go`:

```go
// Fleet is the live health state the loop consults between attempts. It is the
// same *health.Breaker as Deps.Health; a separate interface keeps the recorder
// narrow for the callers that only record.
type Fleet interface {
	SnapshotAvailability(at time.Time) health.Availability
	LastUsedSnapshot() map[health.CredKey]time.Time
	MarkUsed(ck health.CredKey, at time.Time)
	Available(k health.Key) bool
}

type Deps struct {
	Log    Logger
	Health HealthRecorder
	Fleet  Fleet
}
```

- [x] **Step 4: Rewrite Handle around the loop**

Replace `Handle` in `internal/exec/exec.go`. The body-parsing and record setup
are unchanged from Phase 2 down to the point where the provider was resolved;
from there:

```go
	surface := ir.SurfaceLLM
	if pt != nil {
		if s, ok := ir.ParseSurface(pt.Surface); ok {
			surface = s
		}
	}
	rec.Surface = string(surface)
	rec.RequestedModel = req.Model

	providers, err := e.src.Providers(r.Context())
	if err != nil {
		rec.ErrorCode = string(ir.ErrDarkrouter)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return
	}

	// The snapshot freezes every input the router is allowed to read. Health is
	// resolved to booleans here rather than inside Resolve, which is what keeps
	// the router a pure function of its arguments.
	snap := router.Snapshot{
		At:        start,
		Providers: providers,
		Catalog:   catalog.FromProviders(providers),
		Config:    cfg,
	}
	if e.deps.Fleet != nil {
		snap.Health = e.deps.Fleet.SnapshotAvailability(start)
		snap.LastUsed = e.deps.Fleet.LastUsedSnapshot()
	}

	needs := req.Needs()
	cands, skips, rerr := router.Resolve(router.Query{
		Model: req.Model, Surface: surface,
		NeedsTools: needs.Tools, NeedsVision: needs.Vision, NeedsReasoning: needs.Reasoning,
	}, snap)

	rec.Candidates = traceCandidates(cands)
	rec.Skips = traceSkips(skips)

	if rerr != nil {
		e2 := routerError(rerr)
		rec.ErrorCode = string(e2.Type)
		_ = d.WriteError(w, e2)
		return
	}

	e.runAttempts(w, r, d, cfg, req, cands, rec, start)
}

// runAttempts drives the chain. The ordered list is fixed; only skipping is
// dynamic, because another request may have tripped a breaker since the
// snapshot was taken.
func (e *Executor) runAttempts(w http.ResponseWriter, r *http.Request, d edge.Dialect,
	cfg *config.Config, req *ir.Request, cands []router.Candidate,
	rec *store.RequestRecord, start time.Time) {

	bud := newBudget(cfg.Policy.Timeout, start)
	maxAttempts := cfg.Policy.Retry.MaxAttempts
	secrets := e.secretsByKey()

	var lastErr *ir.Error
	attempts := 0

	for i := 0; i < len(cands); {
		if attempts >= maxAttempts {
			break
		}
		c := cands[i]
		now := time.Now()

		// The budget gate: an attempt that cannot possibly complete wastes the
		// budget and the provider's quota.
		if !bud.canStartAttempt(now) {
			rec.ErrorCode = string(ir.ErrDarkrouter)
			if lastErr == nil {
				lastErr = &ir.Error{Type: ir.ErrDarkrouter, Message: "attempts exhausted by deadline"}
			} else {
				lastErr.Message += " (attempts exhausted by deadline)"
			}
			break
		}

		// Re-check live health: another request may have tripped this breaker
		// since the snapshot. Record the skip so the trace still explains the
		// realized sequence.
		hk := health.Key{ProviderID: c.ProviderID, KeyID: c.KeyID, Model: c.Model}
		if e.deps.Fleet != nil && !e.deps.Fleet.Available(hk) {
			rec.Skips = append(rec.Skips, traceSkipOf(c, "cooling"))
			i++
			continue
		}

		// At attempt start, not on success: a credential that always fails must
		// not keep a stale timestamp and sort first forever.
		if e.deps.Fleet != nil {
			e.deps.Fleet.MarkUsed(health.CredKey{ProviderID: c.ProviderID, KeyID: c.KeyID}, now)
		}

		attempts++
		outcome, status, aerr := e.attempt(w, r, d, cfg, req, c, secrets[c.KeyID], bud, rec, attempts)
		if aerr != nil {
			lastErr = aerr
		}

		next, action := nextIndex(cands, i, outcome, status)
		switch action {
		case actionFinish:
			rec.Status = "success"
			return
		case actionReturn:
			if outcome == adapter.OutcomeClientCancelled {
				rec.Status = "cancelled"
			}
			if lastErr != nil {
				rec.ErrorCode = string(lastErr.Type)
				_ = d.WriteError(w, lastErr)
			}
			return
		default:
			i = next
		}
	}

	if lastErr == nil {
		lastErr = &ir.Error{Type: ir.ErrAPI, Message: "every candidate failed"}
	}
	rec.ErrorCode = string(lastErr.Type)
	e.writeErrorDiagnostics(w, rec, attempts)
	_ = d.WriteError(w, lastErr)
}

// writeErrorDiagnostics names the last attempted target on an error response,
// per master design §8. Provider and model are omitted when no attempt was
// made, because naming one would imply it was tried.
func (e *Executor) writeErrorDiagnostics(w http.ResponseWriter, rec *store.RequestRecord, attempts int) {
	w.Header().Set("X-Darkrouter-Attempts", strconv.Itoa(attempts))
	if len(rec.Attempts) == 0 {
		return
	}
	last := rec.Attempts[len(rec.Attempts)-1]
	w.Header().Set("X-Darkrouter-Provider", last.ProviderID)
	w.Header().Set("X-Darkrouter-Model", last.Model)
}
```

Call `e.writeErrorDiagnostics(w, rec, attempts)` on the `actionReturn` branch as
well, immediately before its `d.WriteError`.

Add the supporting helpers to `internal/exec/exec.go`:

```go
// secretsByKey indexes credentials so an attempt can find its secret from the
// candidate's KeyID, which is all the router carries.
func (e *Executor) secretsByKey() map[string]string {
	ps, err := e.src.Providers(context.Background())
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	for _, p := range ps {
		for _, c := range p.Credentials {
			out[c.ID] = c.Secret
		}
	}
	return out
}

func traceCandidates(cs []router.Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ProviderID+"/"+c.KeyID+"/"+c.Model)
	}
	return out
}

func traceSkips(ss []router.Skip) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ProviderID+"/"+s.KeyID+"/"+s.Model+":"+string(s.Reason))
	}
	return out
}

func traceSkipOf(c router.Candidate, reason string) string {
	return c.ProviderID + "/" + c.KeyID + "/" + c.Model + ":" + reason
}

// routerError maps the router's distinguishable empty-result cases onto the
// dialect's error shape. Collapsing them here would undo the whole point of
// having separate sentinels.
func routerError(err error) *ir.Error {
	switch {
	case errors.Is(err, router.ErrModelNotFound):
		return &ir.Error{Type: ir.ErrNotFound, Message: "no configured provider offers this model"}
	case errors.Is(err, router.ErrSurfaceUnsupported):
		return &ir.Error{Type: ir.ErrNotFound, Message: "no configured provider offers this model on this surface"}
	case errors.Is(err, router.ErrAllCooling):
		return &ir.Error{Type: ir.ErrAPI, Message: "every provider offering this model is cooling"}
	case errors.Is(err, router.ErrCapabilityUnsatisfied):
		return &ir.Error{Type: ir.ErrInvalidRequest, Message: "no provider offering this model has the required capabilities"}
	default:
		return &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}
}
```

`attempt` performs one upstream call and is where Task 16 adds streaming. For
this task it handles the unary path only:

```go
// attempt performs one upstream call and records it. It returns the outcome,
// the upstream status code, and the dialect error to serve if this turns out to
// be the last attempt.
func (e *Executor) attempt(w http.ResponseWriter, r *http.Request, d edge.Dialect,
	cfg *config.Config, req *ir.Request, c router.Candidate, secret string,
	bud budget, rec *store.RequestRecord, seq int) (adapter.Outcome, int, *ir.Error) {

	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	ctx, cancelTimeout := context.WithDeadlineCause(ctx, bud.attemptDeadline(time.Now()),
		errDarkrouterTimeout)
	defer cancelTimeout()

	tgt := &adapter.Target{BaseURL: e.baseURLOf(c.ProviderID), APIKey: secret, Model: c.Model}
	hr, err := e.ad.BuildRequest(ctx, tgt, req)
	if err != nil {
		return adapter.OutcomeFatal, 0, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}
	if err := makeReplayable(hr); err != nil {
		return adapter.OutcomeFatal, 0, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}

	attemptStart := time.Now()
	resp, doErr := e.client.Do(hr)
	outcome := e.classify(r.Context(), ctx, resp, doErr)

	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}
	e.recordAttempt(rec, c, outcome, statusCode, doErr, time.Since(attemptStart))
	e.recordHealthFor(c, outcome, resp)

	if outcome != adapter.OutcomeSuccess {
		if resp != nil {
			resp.Body.Close()
		}
		return outcome, statusCode, errorFor(outcome, doErr)
	}

	out, perr := e.ad.ParseResponse(resp)
	if perr != nil {
		// A 2xx that cannot be read is a provider fault, so it rejoins the
		// outcome path rather than going around it.
		e.recordHealthFor(c, adapter.OutcomeRetryableProvider, resp)
		rec.Attempts[len(rec.Attempts)-1].Outcome = string(adapter.OutcomeRetryableProvider)
		rec.Attempts[len(rec.Attempts)-1].Error = perr.Error()
		return adapter.OutcomeRetryableProvider, statusCode, errorFor(adapter.OutcomeRetryableProvider, perr)
	}

	ttft := time.Since(rec.TS).Milliseconds()
	rec.TTFTMs = &ttft
	applyUsage(rec, &out.Usage)
	rec.FinalProviderID = c.ProviderID
	rec.FinalModel = c.Model
	e.writeDiagnostics(w, rec.ID, c, seq)
	_ = d.WriteResponse(w, out)
	return adapter.OutcomeSuccess, statusCode, nil
}
```

Add the helpers `makeReplayable`, `baseURLOf`, `recordAttempt`,
`recordHealthFor`, and `writeDiagnostics` (`bytes` and `io` join the imports):

```go
// makeReplayable sets GetBody so retries inside the transport can resend the
// body. Each attempt re-renders from the IR, so this is not what makes failover
// work — it is what stops a transport-level retry from sending an empty body.
func makeReplayable(hr *http.Request) error {
	if hr.Body == nil || hr.GetBody != nil {
		return nil
	}
	buf, err := io.ReadAll(hr.Body)
	if err != nil {
		return err
	}
	_ = hr.Body.Close()
	hr.Body = io.NopCloser(bytes.NewReader(buf))
	hr.ContentLength = int64(len(buf))
	hr.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf)), nil
	}
	return nil
}

func (e *Executor) baseURLOf(providerID string) string {
	ps, err := e.src.Providers(context.Background())
	if err != nil {
		return ""
	}
	for _, p := range ps {
		if p.ID == providerID {
			return p.BaseURL
		}
	}
	return ""
}

func (e *Executor) recordAttempt(rec *store.RequestRecord, c router.Candidate,
	o adapter.Outcome, statusCode int, err error, latency time.Duration) {

	a := store.AttemptRecord{
		Seq: len(rec.Attempts), ProviderID: c.ProviderID, KeyID: c.KeyID, Model: c.Model,
		Outcome: string(o), StatusCode: statusCode, LatencyMs: latency.Milliseconds(),
	}
	if err != nil {
		a.Error = err.Error()
	}
	rec.Attempts = append(rec.Attempts, a)
}

func (e *Executor) recordHealthFor(c router.Candidate, o adapter.Outcome, resp *http.Response) {
	sig := health.Signal{Outcome: o}
	if resp != nil {
		sig.StatusCode = resp.StatusCode
		if d, ok := health.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			sig.RetryAfter, sig.HasRetryAfter = d, true
		}
	}
	e.recordHealth(health.Key{ProviderID: c.ProviderID, KeyID: c.KeyID, Model: c.Model}, sig)
}

// writeDiagnostics names the target that served the response. Master design §8
// requires these on commit and on Darkrouter-originated errors.
func (e *Executor) writeDiagnostics(w http.ResponseWriter, reqID string, c router.Candidate, attempts int) {
	w.Header().Set("X-Darkrouter-Request", reqID)
	w.Header().Set("X-Darkrouter-Provider", c.ProviderID)
	w.Header().Set("X-Darkrouter-Model", c.Model)
	w.Header().Set("X-Darkrouter-Attempts", strconv.Itoa(attempts))
}
```

`store.RequestRecord` needs a `Skips []string` field; add it beside
`Candidates` in `internal/store/log.go` and marshal it into `candidates_json`
alongside the candidates in Task 19.

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/exec/ -race -v`
Expected: PASS, including every Phase 1–2 test in the package.

- [x] **Step 6: Commit**

```bash
git add internal/exec/ internal/config/ internal/store/
git commit -m "feat(exec): drive an attempt loop over candidates"
```

---

### Task 16: Streaming, commit, and replay

**Files:**
- Modify: `internal/exec/exec.go`
- Test: `internal/exec/stream_test.go`

**Interfaces:**
- Consumes: `preCommitBuffer`, `IsContentBearing`, `ErrPreCommitBufferFull` from Task 13.
- Produces: `func (*Executor) attemptStream(...) (adapter.Outcome, int, *ir.Error)`, called from `attempt` when `req.Stream`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Nothing reaches the client until a content-bearing event arrives. Until then the
attempt can still fail over, and the buffered events are replayed once at commit
so the client sees one coherent stream rather than attempt one's `message_start`
followed by attempt two's content.

The case that makes this necessary rather than merely tidy: **a 2xx whose stream
fails before commit is classified from the stream error, not the status line.**
Anthropic delivers `overloaded_error` as an in-stream event under a 200, and
treating the 200 as success would hand the client an error body with no failover
attempted.

- [x] **Step 1: Write the failing test**

Create `internal/exec/stream_test.go`:

```go
package exec

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseOK(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n"))
}

// A 200 whose stream carries an error before any content must fail over.
func sseErrorUnder200(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(
		"data: {\"error\":{\"message\":\"overloaded\",\"type\":\"overloaded_error\"}}\n\n"))
}

func TestStreamFailsOverOnAnInStreamErrorBeforeCommit(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": sseErrorUnder200,
		"g2": sseOK,
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoKeyFleet(), logger)
	rec := post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	body := rec.Body.String()
	if strings.Contains(body, "overloaded") {
		t.Fatal("the failed attempt's error reached the client; it must be discarded")
	}
	if !strings.Contains(body, `"content":"hi"`) {
		t.Fatalf("the second attempt's content is missing: %s", body)
	}
	if got := sc.order(); len(got) != 2 {
		t.Errorf("order = %v, want both credentials tried", got)
	}
}

// The client must see exactly one coherent stream, not two spliced together.
func TestStreamReplaysPreCommitEventsExactlyOnce(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": sseOK}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, _ := loopExecutor(t, up, twoKeyFleet(), &captureLogger{})
	rec := post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	body := rec.Body.String()
	if n := strings.Count(body, `"role":"assistant"`); n != 1 {
		t.Errorf("role delta appears %d times, want exactly 1", n)
	}
	if n := strings.Count(body, `"content":"hi"`); n != 1 {
		t.Errorf("content appears %d times, want exactly 1", n)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Errorf("stream did not terminate: %q", body[max(0, len(body)-40):])
	}
}

// A ping flood must breach the byte cap and be treated as an attempt failure.
func TestStreamPingFloodFailsTheAttempt(t *testing.T) {
	flood := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Deltas rather than pings: pings carry no payload, so only real
		// payload can breach a byte cap. Each is below the commit threshold
		// because the content is empty.
		for i := 0; i < 5000; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"}}],\"x\":%q}\n\n",
				strings.Repeat("p", 200))
		}
	}
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": flood, "g2": sseOK}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoKeyFleet(), logger)
	setPreCommitCap(t, e, 4096)

	rec := post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)
	if !strings.Contains(rec.Body.String(), `"content":"hi"`) {
		t.Fatalf("the flood should have failed over to g2: %s", rec.Body.String()[:200])
	}
	r := logger.only(t)
	if len(r.Attempts) != 2 {
		t.Errorf("attempts = %d, want 2", len(r.Attempts))
	}
}

func TestStreamRecordsTTFTAndUsage(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{"g1": func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n" +
				"data: [DONE]\n\n"))
	}}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoKeyFleet(), logger)
	post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	r := logger.only(t)
	if r.TTFTMs == nil {
		t.Fatal("TTFT was not recorded")
	}
	if r.TokensOut != 5 {
		t.Errorf("TokensOut = %d, want 5", r.TokensOut)
	}
}
```

Add this helper to `internal/exec/exec_test.go`:

```go
// setPreCommitCap rewrites the executor's config with a small SSE buffer cap.
func setPreCommitCap(t *testing.T, e *Executor, n int) {
	t.Helper()
	body := fmt.Sprintf("server:\n  proxy_listen: :0\n  admin_listen: :0\n"+
		"  sse:\n    max_precommit_bytes: %d\n", n)
	if err := os.WriteFile(e.store.Path(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Reload(); err != nil {
		t.Fatal(err)
	}
}
```

That needs a new config field. Add it to `SSEConfig` in
`internal/config/config.go` and default it in `applyDefaults`:

```go
type SSEConfig struct {
	MaxLineBytes int `yaml:"max_line_bytes"`
	// MaxPrecommitBytes bounds what one attempt may buffer before committing.
	// The first_byte deadline alone is not enough: a provider can emit
	// megabytes inside sixty seconds.
	MaxPrecommitBytes int `yaml:"max_precommit_bytes"`
}
```

```go
	if c.Server.SSE.MaxPrecommitBytes == 0 {
		c.Server.SSE.MaxPrecommitBytes = 1048576
	}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/exec/ -run Stream -v`
Expected: FAIL — the streaming path still writes the first attempt straight
through, so the in-stream error reaches the client.

- [x] **Step 3: Write the streaming attempt**

In `internal/exec/exec.go`, branch inside `attempt` before the unary path:

```go
	if req.Stream {
		return e.attemptStream(w, r, d, cfg, c, resp, statusCode, rec, seq)
	}
```

and add:

```go
// attemptStream buffers the upstream's events until one of them commits the
// response, then replays the buffer and streams the rest.
//
// Nothing reaches the client before commit, which is what makes a pre-commit
// failure invisible: the buffered events are simply discarded.
func (e *Executor) attemptStream(w http.ResponseWriter, r *http.Request, d edge.Dialect,
	cfg *config.Config, c router.Candidate, resp *http.Response, statusCode int,
	rec *store.RequestRecord, seq int) (adapter.Outcome, int, *ir.Error) {

	defer resp.Body.Close()

	buf := newPreCommitBuffer(cfg.Server.SSE.MaxPrecommitBytes)
	committed := false
	var streamErr *ir.Error

	// The sequence handed to the dialect yields the replayed buffer first, then
	// everything that follows. It is built lazily so that nothing is written
	// until the first content-bearing event arrives.
	events := func(yield func(ir.StreamEvent, error) bool) {
		for ev, err := range e.ad.ParseStream(resp.Body, cfg.Server.SSE.MaxLineBytes) {
			if err != nil {
				var e2 *ir.Error
				if !errors.As(err, &e2) {
					e2 = &ir.Error{Type: ir.ErrAPI, Message: err.Error()}
				}
				if !committed {
					// A 2xx whose stream fails before commit is classified from
					// the stream error, not the status line.
					streamErr = e2
					return
				}
				yield(ev, err)
				return
			}
			if ev.Usage != nil {
				applyUsage(rec, ev.Usage)
			}
			if !committed {
				if !IsContentBearing(ev) {
					if berr := buf.add(ev); berr != nil {
						// A cap breach is an attempt failure, not a client
						// error: the provider is misbehaving and another may not.
						streamErr = &ir.Error{Type: ir.ErrAPI, Message: berr.Error()}
						return
					}
					continue
				}
				committed = true
				ttft := time.Since(rec.TS).Milliseconds()
				rec.TTFTMs = &ttft
				rec.FinalProviderID = c.ProviderID
				rec.FinalModel = c.Model
				e.writeDiagnostics(w, rec.ID, c, seq)
				for _, buffered := range buf.events() {
					if !yield(buffered, nil) {
						return
					}
				}
			}
			if !yield(ev, nil) {
				return
			}
		}
	}

	werr := d.WriteStream(w, events)

	switch {
	case streamErr != nil && !committed:
		// Reclassify: the transport said 2xx but the stream said otherwise.
		e.recordHealthFor(c, adapter.OutcomeRetryableProvider, resp)
		last := len(rec.Attempts) - 1
		rec.Attempts[last].Outcome = string(adapter.OutcomeRetryableProvider)
		rec.Attempts[last].Error = streamErr.Message
		return adapter.OutcomeRetryableProvider, statusCode, streamErr
	case werr != nil && !committed:
		return adapter.OutcomeRetryableProvider, statusCode, &ir.Error{
			Type: ir.ErrAPI, Message: werr.Error()}
	default:
		return adapter.OutcomeSuccess, statusCode, nil
	}
}
```

`d.WriteStream` writes the `[DONE]` sentinel itself, so a stream that commits
and then ends normally needs nothing further here.

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/exec/ -race -v`
Expected: PASS.

If `TestStreamFailsOverOnAnInStreamErrorBeforeCommit` still writes the error to
the client, `WriteStream` is emitting headers before the first yielded event.
Check that the dialect writes nothing until its first `send`.

- [x] **Step 5: Commit**

```bash
git add internal/exec/ internal/config/
git commit -m "feat(exec): commit streams on first content"
```

---

### Task 17: Post-commit failures and the idle deadline

**Files:**
- Modify: `internal/exec/exec.go`
- Test: `internal/exec/postcommit_test.go`

**Interfaces:**
- Consumes: `attemptStream` from Task 16.
- Produces: no new exported surface; `attemptStream` switches its context to an idle-bounded one at commit.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

After commit, failover is impossible — the client already has bytes. A failure
becomes an error event **inside** the stream, and the stream then ends. Returning
a second response would be a protocol violation the client cannot parse.

`policy.timeout.total` stops applying at commit; `policy.timeout.idle` bounds the
gap between events instead. A legitimate ten-minute reasoning response must not
be killed, while a provider that goes silent must be.

Phase 3 defines only that an error event is emitted and the stream ends. Its
per-dialect shape is Phase 4's problem, because OpenAI has no standard in-stream
error and Gemini's SSE has no error event type at all.

- [x] **Step 1: Write the failing test**

Create `internal/exec/postcommit_test.go`:

```go
package exec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A provider that commits and then dies must not produce a second response.
func TestPostCommitFailureBecomesAnInStreamError(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
			w.(http.Flusher).Flush()
			// Malformed frame after committing.
			_, _ = w.Write([]byte("data: {not json\n\n"))
		},
		"g2": sseOK,
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	logger := &captureLogger{}
	e, _ := loopExecutor(t, up, twoKeyFleet(), logger)
	rec := post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	body := rec.Body.String()
	if !strings.Contains(body, "partial") {
		t.Fatalf("committed content is missing: %s", body)
	}
	// The second credential must never be tried: failover is impossible once
	// the client has bytes.
	if got := sc.order(); len(got) != 1 {
		t.Errorf("order = %v, want only g1 — a committed stream cannot fail over", got)
	}
	if !strings.Contains(body, "error") {
		t.Errorf("a post-commit failure must surface as an in-stream error: %s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Errorf("the stream must still terminate: %q", body[max(0, len(body)-40):])
	}
}

// A committed stream survives past policy.timeout.total.
func TestCommittedStreamOutlivesTheTotalBudget(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
			w.(http.Flusher).Flush()
			// Past the 300ms total, but inside the idle gap.
			time.Sleep(500 * time.Millisecond)
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		},
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, _ := loopExecutor(t, up, twoKeyFleet(), &captureLogger{})
	setTimeouts(t, e, 300*time.Millisecond, 5*time.Second)

	rec := post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"b"`) {
		t.Fatalf("a committed stream was killed by the total budget: %s", body)
	}
}

// A committed stream that goes silent is cut at idle.
func TestCommittedStreamIsCutAtIdle(t *testing.T) {
	sc := &scripted{by: map[string]http.HandlerFunc{
		"g1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
			w.(http.Flusher).Flush()
			time.Sleep(3 * time.Second) // far past idle
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		},
	}}
	up := httptest.NewServer(sc)
	defer up.Close()

	e, _ := loopExecutor(t, up, twoKeyFleet(), &captureLogger{})
	setTimeouts(t, e, 10*time.Second, 200*time.Millisecond)

	done := make(chan string, 1)
	go func() {
		done <- post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`).Body.String()
	}()
	select {
	case body := <-done:
		if !strings.Contains(body, `"content":"a"`) {
			t.Errorf("committed content missing: %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a silent committed stream was not cut at idle")
	}
}
```

Add this helper to `internal/exec/exec_test.go`:

```go
func setTimeouts(t *testing.T, e *Executor, total, idle time.Duration) {
	t.Helper()
	body := fmt.Sprintf("server:\n  proxy_listen: :0\n  admin_listen: :0\n"+
		"policy:\n  timeout:\n    total: %s\n    idle: %s\n", total, idle)
	if err := os.WriteFile(e.store.Path(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Reload(); err != nil {
		t.Fatal(err)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/exec/ -run 'PostCommit|Committed' -v`
Expected: FAIL — the committed stream is still bounded by the attempt deadline,
so `TestCommittedStreamOutlivesTheTotalBudget` is cut short.

- [x] **Step 3: Swap the deadline at commit**

`attempt` currently derives one context from `bud.attemptDeadline`. Streams need
that bound only until commit, so `attemptStream` takes the cancel function and
replaces the deadline when it commits.

Change `attempt` to build the attempt context with an explicit resettable
deadline and pass its handle down:

```go
	// A timer rather than a context deadline, because the bound changes at
	// commit: total stops applying and idle takes over.
	deadline := bud.attemptDeadline(time.Now())
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	timer := time.AfterFunc(time.Until(deadline), func() { cancel(errDarkrouterTimeout) })
	defer timer.Stop()
```

and hand `timer` plus the idle duration to `attemptStream`, which resets it on
every event once committed:

```go
			if committed {
				// Post-commit, idle bounds the gap between events. A legitimate
				// ten-minute reasoning response must not be killed, while a
				// provider that goes silent must be.
				timer.Reset(cfg.Policy.Timeout.Idle)
			}
```

Place that reset immediately after each successful `yield`, and add the same
reset at the moment of commit so the first post-commit gap is measured from
there rather than from the attempt's start.

- [x] **Step 4: Emit the in-stream error**

In `attemptStream`, the committed branch already forwards the error to `yield`,
which the dialect renders as an in-stream error event before writing `[DONE]`.
Confirm the committed path returns `OutcomeSuccess` so the loop does not attempt
another candidate:

```go
	case streamErr != nil && committed:
		// Failover is impossible: the client already has bytes. The error went
		// out inside the stream and the stream has ended.
		rec.Status = "success"
		return adapter.OutcomeSuccess, statusCode, nil
```

Place this case **before** the `streamErr != nil && !committed` case.

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/exec/ -race -v`
Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add internal/exec/
git commit -m "feat(exec): bound committed streams by idle"
```

---

### Task 18: An unknown model reported as a 400

**Files:**
- Modify: `internal/adapter/openaicompat/classify.go`
- Test: `internal/adapter/openaicompat/classify_test.go`

**Interfaces:**
- Consumes: `adapter.Outcome`.
- Produces: `func openaicompat.ClassifyBody(resp *http.Response, body []byte, err error) adapter.Outcome`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Some OpenAI-compatible providers report an unknown model as a **400 with an
identifying error code** rather than a 404. Classifying that as `Fatal` makes
failover die on the first provider in the chain that does not carry the model —
which is exactly the arrangement an alias exists to create.

The body is needed to tell that 400 from a genuinely malformed request, so this
is a second entry point rather than a change to `Classify`: the existing
signature has no body and every current caller is happy without one.

- [x] **Step 1: Write the failing test**

Append to `internal/adapter/openaicompat/classify_test.go`:

```go
func TestClassifyBodyDetectsAnUnknownModel400(t *testing.T) {
	cases := []struct {
		name string
		body string
		want adapter.Outcome
	}{
		{
			name: "model_not_found code",
			body: `{"error":{"message":"The model does not exist","code":"model_not_found"}}`,
			want: adapter.OutcomeRetryableModel,
		},
		{
			name: "invalid_model code",
			body: `{"error":{"message":"bad model","code":"invalid_model"}}`,
			want: adapter.OutcomeRetryableModel,
		},
		{
			name: "model named in the message",
			body: `{"error":{"message":"model \"llama-9\" does not exist","type":"invalid_request_error"}}`,
			want: adapter.OutcomeRetryableModel,
		},
		{
			name: "a genuinely malformed request stays fatal",
			body: `{"error":{"message":"messages: field required","type":"invalid_request_error"}}`,
			want: adapter.OutcomeFatal,
		},
		{
			name: "an unparseable body stays fatal",
			body: `not json`,
			want: adapter.OutcomeFatal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: 400, Header: http.Header{}}
			if got := ClassifyBody(resp, []byte(tc.body), nil); got != tc.want {
				t.Errorf("ClassifyBody = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyBodyDefersToClassifyForEveryOtherStatus(t *testing.T) {
	for _, code := range []int{200, 401, 404, 429, 500, 503} {
		resp := &http.Response{StatusCode: code, Header: http.Header{}}
		want := Classify(resp, nil)
		if got := ClassifyBody(resp, []byte(`{"error":{"code":"model_not_found"}}`), nil); got != want {
			t.Errorf("status %d: ClassifyBody = %q, want %q", code, got, want)
		}
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/openaicompat/ -run ClassifyBody -v`
Expected: FAIL — `undefined: ClassifyBody`.

- [x] **Step 3: Write the refinement**

Append to `internal/adapter/openaicompat/classify.go`:

```go
// unknownModelCodes are the error codes OpenAI-compatible providers use when a
// 400 means "I do not have that model" rather than "your request is malformed".
var unknownModelCodes = map[string]bool{
	"model_not_found": true,
	"invalid_model":   true,
	"unknown_model":   true,
}

// ClassifyBody refines Classify for the one case the status line cannot express:
// a 400 that means the model is unknown.
//
// Treating that as Fatal would make failover die on the first provider in a
// chain that does not carry the model — which is exactly the arrangement an
// alias exists to create.
func ClassifyBody(resp *http.Response, body []byte, err error) adapter.Outcome {
	base := Classify(resp, err)
	if base != adapter.OutcomeFatal || resp == nil || resp.StatusCode != 400 {
		return base
	}

	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return base
	}
	if unknownModelCodes[strings.ToLower(parsed.Error.Code)] {
		return adapter.OutcomeRetryableModel
	}
	// Some providers send no code and only say so in prose. Requiring both
	// "model" and a not-found phrase keeps this from swallowing every 400 that
	// happens to mention a model name.
	msg := strings.ToLower(parsed.Error.Message)
	if strings.Contains(msg, "model") &&
		(strings.Contains(msg, "does not exist") ||
			strings.Contains(msg, "not found") ||
			strings.Contains(msg, "unknown model")) {
		return adapter.OutcomeRetryableModel
	}
	return base
}
```

Add `encoding/json` and `strings` to the file's imports.

- [x] **Step 4: Use it from the executor**

In `internal/exec/exec.go`, the unary error path currently classifies from the
status line alone. Read the body once on a 400 so the refinement can run:

```go
	if outcome == adapter.OutcomeFatal && resp != nil && resp.StatusCode == 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if refined := openaicompat.ClassifyBody(resp, body, doErr); refined != outcome {
			outcome = refined
		}
	}
```

Place it immediately after `outcome := e.classify(...)` and before the attempt is
recorded, so the trace carries the refined outcome. The 64 KiB bound matters: an
error body is small, and reading an unbounded one from a misbehaving provider is
the same hazard `max_body_bytes` exists to prevent.

Add `io` and the `openaicompat` import to `internal/exec/exec.go`.

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./... -race`
Expected: PASS across every package.

- [x] **Step 6: Commit**

```bash
git add internal/adapter/ internal/exec/
git commit -m "fix(adapter): treat an unknown-model 400 as retryable"
```

---

### Task 19: Persist the trace and wire the fleet into the server

**Files:**
- Modify: `internal/store/log.go`
- Modify: `internal/server/server.go`
- Test: `internal/store/log_test.go`, `internal/server/run_test.go`

**Interfaces:**
- Consumes: `Deps.Fleet` from Task 15, `(*DB).SaveLastUsed`/`LoadLastUsed` from Task 6.
- Produces: `store.RequestRecord.Skips []string`; `candidates_json` holding `{"candidates":[...],"skips":[...]}`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

The realized candidate list **and every skip reason** go on the request row.
Health tables are overwritten in place, so a trace that recorded only the
attempts could never answer "why was cerebras never tried?" after the fact —
which is the question an operator actually asks.

Both live in `candidates_json` rather than a new column, because that keeps the
Phase 2 schema unchanged and the two are only ever read together.

- [x] **Step 1: Write the failing test**

Append to `internal/store/log_test.go`:

```go
func TestLogWriterPersistsCandidatesAndSkips(t *testing.T) {
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{Buffer: 4, BatchSize: 1, FlushEvery: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	r := rec("trace")
	r.Candidates = []string{"groq/g1/m", "cerebras/c1/m"}
	r.Skips = []string{"groq/g2/m:cooling"}
	w.Log(r)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	var raw string
	if err := db.Read.QueryRowContext(context.Background(),
		`SELECT candidates_json FROM requests WHERE id = 'trace'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Candidates []string `json:"candidates"`
		Skips      []string `json:"skips"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("candidates_json is not the expected shape: %v (%s)", err, raw)
	}
	if len(got.Candidates) != 2 || got.Candidates[0] != "groq/g1/m" {
		t.Errorf("candidates = %v", got.Candidates)
	}
	// The skip is what explains why g2 was never attempted.
	if len(got.Skips) != 1 || got.Skips[0] != "groq/g2/m:cooling" {
		t.Errorf("skips = %v", got.Skips)
	}
}

func TestLogWriterWritesEmptyArraysNotNull(t *testing.T) {
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{Buffer: 4, BatchSize: 1, FlushEvery: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	w.Log(rec("empty"))
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	var raw string
	if err := db.Read.QueryRowContext(context.Background(),
		`SELECT candidates_json FROM requests WHERE id = 'empty'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "null") {
		t.Errorf("candidates_json = %s, want empty arrays rather than null", raw)
	}
}
```

Add `encoding/json` and `strings` to that file's imports.

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'CandidatesAndSkips|EmptyArrays' -v`
Expected: FAIL — `r.Skips undefined`.

- [x] **Step 3: Add the field and the trace shape**

In `internal/store/log.go`, add `Skips` beside `Candidates`:

```go
	Candidates      []string
	Skips           []string
```

and replace the `candidates` marshalling in `insertOne`:

```go
	// Candidates and skips travel together in one column: they are only ever
	// read together, and keeping them there leaves the phase 2 schema alone.
	trace, err := json.Marshal(struct {
		Candidates []string `json:"candidates"`
		Skips      []string `json:"skips"`
	}{nonNil(r.Candidates), nonNil(r.Skips)})
	if err != nil {
		return err
	}
```

and pass `string(trace)` where `string(candidates)` was passed.

- [x] **Step 4: Wire the fleet and the usage worker into the server**

In `internal/server/server.go`, pass the breaker as the fleet when building the
executor:

```go
		ex: exec.New(cfgStore, src, openaicompat.New(), exec.Deps{
			Log: logw, Health: breaker, Fleet: breaker,
		}),
```

In `Run`, restore credential usage alongside health, and add a worker that
persists it:

```go
	if lu, err := s.db.LoadLastUsed(workerCtx); err != nil {
		s.store.RecordError(fmt.Errorf("credential usage rehydration: %w", err))
	} else {
		s.breaker.RehydrateLastUsed(lu)
	}

	startWorker("credential usage", func(c context.Context) error {
		// Persisted purely for restart continuity: the in-memory map is
		// authoritative, so a missed write costs ordering accuracy across one
		// restart and nothing else.
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-c.Done():
				return s.db.SaveLastUsed(context.Background(), s.breaker.LastUsedSnapshot())
			case <-t.C:
				if err := s.db.SaveLastUsed(c, s.breaker.LastUsedSnapshot()); err != nil {
					log.Printf("credential usage: %v", err)
				}
			}
		}
	})
```

- [x] **Step 5: Assert the trace survives a real request**

Append to `internal/server/run_test.go`:

```go
func TestRequestRowRecordsTheCandidateChain(t *testing.T) {
	dir := t.TempDir()
	cfgStore := testConfigStore(t, dir)
	db, err := store.Open(filepath.Join(dir, "darkrouter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, _ := store.OpenKeyring(ctx, db, "master")
	s, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	// No providers are configured, so the router refuses before any attempt and
	// the row still has to exist.
	rr := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rr, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"nope","messages":[]}`)))
	if rr.Code != 404 {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if rr.Header().Get("X-Darkrouter-Request") == "" {
		t.Error("the request id must be returned even when no attempt was made")
	}
}
```

- [x] **Step 6: Run tests to verify they pass**

Run: `go test ./... -race`
Expected: PASS across every package.

- [x] **Step 7: Commit**

```bash
git add internal/store/ internal/server/
git commit -m "feat(server): persist the candidate trace"
```

---

### Task 20: Drop the single-credential fields

**Files:**
- Modify: `internal/provider/provider.go`
- Modify: `internal/provider/sqlsource.go`
- Test: `internal/provider/sqlsource_test.go`, `internal/provider/provider_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Provider` without `APIKey` and `KeyID`; `YAMLSource` populates `Credentials` with a single synthetic entry.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Task 3 added `Credentials` alongside the old fields so nothing broke while the
executor still read them. The attempt loop reads `Credentials` now, so the old
fields are dead weight — and leaving two sources of truth for the same fact is
how they drift.

`YAMLSource` gets a synthetic credential with an empty id. That is honest: a
config credential has no database row, and the breaker keying on an empty key id
is the same behavior Phase 2 already had.

- [x] **Step 1: Delete the fields**

In `internal/provider/provider.go`, remove `APIKey` and `KeyID` from `Provider`:

```go
type Provider struct {
	ID      string
	Kind    string
	BaseURL string

	// Credentials are every enabled credential, ordered by id. Credential
	// rotation happens before advancing to the next provider, so the router
	// needs all of them rather than a chosen one.
	Credentials []Credential

	Priority int
	Models   []string
}
```

- [x] **Step 2: Run the build to find every consumer**

Run: `go build ./... && go vet ./...`
Expected: FAIL, listing each remaining reference. Work through them; the
executor should already be clean, and `YAMLSource` and the Phase 2 tests are the
expected hits.

- [x] **Step 3: Update YAMLSource**

In `internal/provider/provider.go`, replace the `Provider` construction in
`YAMLSource.Providers`:

```go
	for _, p := range cfg.Providers {
		out = append(out, Provider{
			ID: p.ID, Kind: p.Kind, BaseURL: p.BaseURL,
			// A config credential has no database row, so its id is empty. The
			// breaker keys on that empty id, which is what phase 2 already did.
			Credentials: []Credential{{ID: "", Secret: p.APIKey, Enabled: true}},
			Priority:    p.Priority, Models: p.Models,
		})
	}
```

- [x] **Step 4: Update SQLSource**

Delete the `APIKey` and `KeyID` assignments from the `Provider` literal in
`Reload`, and delete `TestSQLSourceStillPopulatesTheSingleCredentialFields` from
`sqlsource_test.go` — it asserted exactly the thing this task removes.

Update `TestSQLSourceLoadsProvidersWithDecryptedCredentials` to assert on
`Credentials[0]` instead of `APIKey`/`KeyID`.

- [x] **Step 5: Run the whole suite**

Run: `go test ./... -race -count=1 && go vet ./...`
Expected: PASS, no output from vet.

- [x] **Step 6: Verify the binary still builds statically**

```bash
CGO_ENABLED=0 go build -o /tmp/dr ./cmd/darkrouter && file /tmp/dr && rm /tmp/dr
```
Expected: `statically linked`.

- [x] **Step 7: Commit**

```bash
git add internal/provider/
git commit -m "refactor(provider): drop the single-credential fields"
```

---

## Done criteria

Check each against spec §10 before calling the phase complete.

- [x] Killing the first provider in an alias chain is invisible to the client, and the trace shows every attempt and every skip with reasons. *(Tasks 11, 15, 16, 19)*
- [x] Two credentials on one provider are both exercised on a 429, and both skipped on a 5xx. *(Tasks 14, 15)*
- [x] A malformed request produces exactly one attempt; an unknown model advances without penalizing anyone. *(Tasks 14, 15, 18)*
- [x] A client disconnect leaves all providers healthy. *(Phase 2's Task 15, still asserted by `TestClientDisconnectIsNotAProviderFailure`)*
- [x] The candidate list and skip reasons on the request row explain the ordering without needing live health. *(Task 19)*
- [x] `go test ./... -race` passes and `go vet ./...` is clean. *(Task 20)*

## Carried into Phase 4 and beyond

- **Capability filtering admits everything.** Every model's capabilities are `inferred` until Phase 6 supplies real data, and per master design §6.4 inferred capabilities pass with a warning. The filter is wired and tested; it is not yet selective.
- **`ErrCapabilityUnsatisfied` is unreachable.** It exists so Phase 6 does not have to invent it, and so a capability mismatch never gets reported as a surface problem.
- **The per-target `RetryableModel` counter is not yet surfaced.** Spec §7.2 wants a permanently misconfigured base URL to become visible once the count crosses a threshold; the overview that shows it is Phase 7.
- **In-stream error shape is generic.** Phase 3 defines only that an error event is emitted and the stream ends. OpenAI has no standard in-stream error and Gemini's SSE has no error event type at all, so the per-dialect shape is Phase 4's.
- **Failed attempts burn tokens invisibly.** A pre-commit failover after the body was sent may mean the first provider already processed and billed the prompt. `request_attempts` carries no usage columns, so those tokens never reach `usage_daily`. This is an accepted gateway trade-off, recorded rather than solved.
- **`edge.Passthrough.Surface` is still a plain string.** Phase 4 rewrites the edge layer and is the right moment to type it against `ir.Surface`.
