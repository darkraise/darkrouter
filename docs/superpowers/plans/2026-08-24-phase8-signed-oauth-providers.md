# Phase 8 — Signed and Subscription Credentials Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the three credential strategies whose difficulty is authentication rather than payload shape — AWS SigV4, Google service-account JWT, and OAuth subscription accounts — plus the `bedrock` and `vertex` adapter kinds that come with them.

**Architecture:** Authentication becomes a first-class seam. `internal/auth` resolves a `(provider, credential)` pair into an `Authorizer` — a function applied to the built request after its body is materialized and before it is sent. Static styles keep working exactly as they do today, through the adapter's own header. The two new adapter kinds are ordinary `adapter.Adapter` implementations; `vertex` dispatches on the publisher recorded on the catalog entry and reuses phase 4's Gemini and Anthropic translations rather than growing a third.

**Tech Stack:** Go 1.26.1, `aws-sdk-go-v2` (standalone SigV4 signer, eventstream decoder, default credential chain), `golang.org/x/oauth2/google`, `modernc.org/sqlite`, `CGO_ENABLED=0`.

**Spec:** `docs/superpowers/specs/2026-08-22-darkrouter-phase8-signed-oauth-providers.md`
**Master design:** `docs/superpowers/specs/2026-08-22-darkrouter-design.md`

## Global Constraints

- **Module** `github.com/darkraise/darkrouter`, **Go 1.26.1**, **`CGO_ENABLED=0`**. Go lives at `/usr/local/go/bin`; every task's commands assume `export PATH=$PATH:/usr/local/go/bin`.
- **`DARKROUTER_MASTER_KEY` must be set for any run of the binary**, including smoke tests. A throwaway value is fine.
- **English only** — code, comments, docs, commits, configs, errors, tests.
- **Commits** are `<type>(<scope>): <subject>`, subject ≤50 chars, imperative, no period.
- **New dependencies, and no others.** `github.com/aws/aws-sdk-go-v2/aws`, `.../aws/signer/v4`, `.../aws/protocol/eventstream`, `.../config`, and `golang.org/x/oauth2` with `/google` and `/jwt`. Master design §2 names all of them. `aws-sdk-go-v2/config` drags in `sso`, `ssooidc`, `sts` and `imds` as indirect dependencies; that is the price of spec §3.2's "environment, shared config, or instance role" and is accepted.
- **No Bedrock service client.** SigV4 is applied by the standalone signer to a request Darkrouter builds, so one `http.Client`, one transport and one timeout policy cover every adapter. Spec §3.2.
- **No credential material in any admin API response, any log line, or any error string.** This is asserted by a test, not by inspection.
- **This environment has no AWS account, no GCP service account, and no Claude subscription.** Every test in this plan runs against a fake — a known-answer vector, a recorded frame, an `httptest` token endpoint, an `httptest` authorization server. Task 22's verification is fake-backed for the same reason, and says so rather than implying a live run.

## Verifying the whole tree

Every task ends with these. They are not repeated inside each task's steps beyond the specific test being written:

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -count=1 && go vet ./... && gofmt -l .
```

`gofmt -l .` printing a path is a failure even when the tests pass.

## File structure

| File | Responsibility |
|---|---|
| `internal/auth/auth.go` | `Authorizer`, `Style`, `Target`, `Credential`; the `Manager` that resolves a pair into an `Authorizer` |
| `internal/auth/static.go` | The five static styles, so a non-static credential can be told apart from a static one in one place |
| `internal/auth/sigv4.go` | The SigV4 authorizer over the standalone signer, and the AWS credential chain |
| `internal/auth/gcp.go` | Service-account JSON to a cached, lazily refreshed access token |
| `internal/auth/oauth.go` | The OAuth authorizer: per-account mutex, refresh ahead of expiry, rotation-safe persist |
| `internal/auth/token.go` | `Token` — the encrypted JSON document a non-static credential stores |
| `internal/auth/pkce.go` | Verifier, challenge, and the single-use expiring `state` store |
| `internal/adapter/bedrock/adapter.go` | The `Adapter` implementation and its surfaces |
| `internal/adapter/bedrock/build.go` | IR to Converse / ConverseStream |
| `internal/adapter/bedrock/parse.go` | Converse response to IR, and classification |
| `internal/adapter/bedrock/stream.go` | Eventstream frames to `ir.StreamEvent` |
| `internal/adapter/bedrock/discover.go` | `ListFoundationModels` + `ListInferenceProfiles` |
| `internal/adapter/vertex/adapter.go` | The `Adapter` implementation and the publisher dispatch |
| `internal/adapter/vertex/google.go` | `generateContent` / `streamGenerateContent`, reusing the Gemini renderer |
| `internal/adapter/vertex/anthropic.go` | `rawPredict` / `streamRawPredict`, reusing the Anthropic renderer |
| `internal/admin/oauth.go` | `POST /api/providers/:id/oauth/start`, the paste completion, `GET /api/oauth/callback` |
| `internal/admin/listener.go` | The temporary localhost redirect listener |
| `internal/store/credentials.go` | `expires_at` on insert, and the rotation-safe token replace |
| `web/src/routes/settings.tsx` | The connect flow and the reconnection banner |

## Shared test fakes

Three fakes are used by more than one task. Each is written once, in the task
that first needs it, and reused afterwards — not rewritten.

**`serverWithFakeAuthServer(t) (*Server, jar, *fakeAuthServer)`** — Task 12.
`fakeAuthServer` is the `authServer` type Task 14 defines, exported to the
`admin` package's tests or duplicated there if the packages cannot share it. It
records the last `code` and `code_verifier` it received, rotates its refresh
token on every refresh, and has `status` and `errBody` fields a test can set to
make it refuse. Tasks 13, 15 and 18 use all of those.

**`serverWithFakeAWS(t) (*Server, jar, *fakeAWS)`** — Task 15. Serves
`/foundation-models` and `/inference-profiles`, records the `Authorization`
header it saw, and has a `status` field. Task 20 reuses it.

**`serverWithFakeGCP(t) (*Server, jar, *fakeGCP)`** — Task 15. A token endpoint
plus a `:generateContent` endpoint, counting `tokenCalls` and `generateCalls`.
Task 20 reuses it.

`probeResult` and `failed(kind string, err error) probeResult` in Task 15 are
`internal/admin/probe.go`'s existing shapes. **Read that file** — phase 7 named
them, and the field spellings in this plan's snippets are from memory.

---

### Task 1: The authorization seam

**Files:**
- Create: `internal/auth/auth.go`, `internal/auth/static.go`
- Modify: `internal/adapter/adapter.go`, `internal/provider/provider.go`, `internal/provider/sqlsource.go`, `internal/exec/exec.go`
- Test: `internal/auth/auth_test.go`, `internal/exec/authorize_test.go`

**Interfaces:**
- Produces: `auth.Authorizer`, `auth.Style`, `auth.IsStatic(string) bool`, `auth.Target`, `auth.Credential`, `auth.Manager`, `auth.NewManager(Deps) *Manager`, `(*Manager).For(ctx, Target, Credential) (Authorizer, error)`; `adapter.Target.Region/Project/Location/Publisher`; `provider.Provider.Region/Project/Location`; `provider.Credential.Kind`; `exec.Deps.Auth`.

Every adapter today writes its own credential header from `Target.APIKey`, and
every one of them guards on `t.APIKey != ""`. That guard is what makes this
whole phase cheap: for a non-static style the executor leaves `APIKey` empty, the
adapter writes no credential header at all, and the authorizer owns the
credential outright. Nothing has to be un-set, and no code path can leak a token
document into an `x-api-key` header by forgetting a step.

**The authorizer runs after `makeReplayable`, not inside `Build`.** SigV4 signs a
hash of the body, so the body must be fully materialized first; and an authorizer
that ran inside `Build` would sign a request that later code is still allowed to
mutate. `makeReplayable` is the exact point where the body stops changing.

**Region, project and location travel on the target, not in the base URL.** Spec
§3.3 is explicit that Bedrock's region is an endpoint property rather than part
of the model identifier, and §4.2 puts Vertex's project and location on the
provider row. `providers` has carried all three columns since migration 0001 and
nothing has ever read them.

- [ ] **Step 1: Write the failing test**

Create `internal/exec/authorize_test.go`:

```go
package exec

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The authorizer must see a body it can read to the end and a request whose
// body is still replayable afterwards. Signing consumes the body; a retry that
// found it drained would send an empty payload under a valid signature.
func TestAuthorizeRunsOnAMaterializedBody(t *testing.T) {
	hr, err := http.NewRequest("POST", "https://example.invalid/x",
		io.NopCloser(strings.NewReader(`{"a":1}`)))
	if err != nil {
		t.Fatal(err)
	}
	if err := makeReplayable(hr); err != nil {
		t.Fatal(err)
	}

	var sawLen int
	authorizer := func(_ context.Context, r *http.Request) error {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		sawLen = len(body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.Header.Set("Authorization", "signed")
		return nil
	}

	if err := applyAuthorizer(context.Background(), hr, authorizer); err != nil {
		t.Fatal(err)
	}
	if sawLen != len(`{"a":1}`) {
		t.Errorf("authorizer saw %d bytes, want %d", sawLen, len(`{"a":1}`))
	}
	if hr.Header.Get("Authorization") != "signed" {
		t.Error("the authorizer's header did not survive")
	}
	replay, err := hr.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	again, err := io.ReadAll(replay)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != `{"a":1}` {
		t.Errorf("replayed body = %q, want the original", again)
	}
}

func TestAuthorizeIsANoOpWhenNil(t *testing.T) {
	hr, err := http.NewRequest("GET", "https://example.invalid/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyAuthorizer(context.Background(), hr, nil); err != nil {
		t.Fatalf("a nil authorizer must be the static path, got %v", err)
	}
}
```

Create `internal/auth/auth_test.go`:

```go
package auth

import (
	"context"
	"net/http"
	"testing"
)

func TestStaticStylesAreRecognized(t *testing.T) {
	for _, style := range []string{"bearer", "x-api-key", "api-key", "query-param", "none"} {
		if !IsStatic(style) {
			t.Errorf("%q should be static", style)
		}
	}
	// The empty style is the unconfigured provider row, which falls back to
	// bearer everywhere else and must not be routed to a signer.
	if !IsStatic("") {
		t.Error(`"" should be static`)
	}
	for _, style := range []string{"sigv4", "gcp-sa", "oauth"} {
		if IsStatic(style) {
			t.Errorf("%q must not be static", style)
		}
	}
}

func TestManagerReturnsNilForAStaticStyle(t *testing.T) {
	// nil means "the adapter's own header is correct", which is the whole
	// mechanism: no branch anywhere else has to know about static styles.
	m := NewManager(Deps{})
	a, err := m.For(context.Background(),
		Target{ProviderID: "p", Style: "bearer"},
		Credential{ID: "k", Secret: "sk-x"})
	if err != nil {
		t.Fatal(err)
	}
	if a != nil {
		t.Error("a static style must not produce an authorizer")
	}
}

func TestManagerRefusesAnUnknownStyle(t *testing.T) {
	m := NewManager(Deps{})
	_, err := m.For(context.Background(),
		Target{ProviderID: "p", Style: "sigv5"},
		Credential{ID: "k"})
	if err == nil {
		t.Fatal("an unrecognized style must be an error, not a silent bearer")
	}
	if !isUnsupported(err) {
		t.Errorf("error should be ErrUnsupportedStyle, got %v", err)
	}
}

func isUnsupported(err error) bool {
	for e := err; e != nil; {
		if e == ErrUnsupportedStyle {
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

var _ Authorizer = func(context.Context, *http.Request) error { return nil }
```

- [ ] **Step 2: Run them to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/auth/ ./internal/exec/ -run 'Authorize|Static|Manager' 2>&1 | head -20
```

Expected: `internal/auth` fails to build (no such package), and `internal/exec`
fails on the undefined `applyAuthorizer`.

- [ ] **Step 3: Create the package**

Create `internal/auth/auth.go`:

```go
// Package auth resolves a provider's credential into an authorization applied
// to an already-built request.
//
// It exists because authentication is orthogonal to payload shape, master
// design §6.1: a Claude subscription speaks Anthropic Messages and an OpenAI
// one does not, so OAuth cannot be an adapter kind. A preset declares a kind
// and an auth style independently, and this package is where the second half
// lives.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Authorizer attaches a credential to a request whose body is already
// materialized. It runs after the executor's makeReplayable and before the
// request is sent, which is the only point at which SigV4 can hash a payload
// that will not change afterwards.
//
// A nil Authorizer means the adapter's own header is correct — the static
// styles. That is deliberate: making the static path the zero value keeps every
// caller from branching on a style it does not otherwise care about.
type Authorizer func(ctx context.Context, r *http.Request) error

// ErrUnsupportedStyle is returned for a style no strategy implements. It is an
// error rather than a bearer fallback: a provider row naming a style this build
// does not have would otherwise send its credential in a header the upstream
// ignores, and present as an authentication failure with no cause.
var ErrUnsupportedStyle = errors.New("unsupported auth style")

// Target is what a strategy needs to know about the provider. It is a plain
// struct rather than a provider.Provider for the same reason adapter.Target is:
// provider imports config and store, and a credential strategy has no business
// reaching either.
type Target struct {
	ProviderID string
	Style      string
	Region     string
	Project    string
	Location   string
	// Preset names the shipped entry, so the OAuth strategy can reach the
	// token endpoint and client id without importing catalog.
	Preset string
}

// Credential is one usable credential. Secret is the decrypted plaintext: a
// bare key for a static style, a JSON service-account document for gcp-sa, and
// a marshalled Token for oauth.
type Credential struct {
	ID     string
	Kind   string
	Secret string
}

// Deps carries the collaborators the non-static strategies need. A zero Deps is
// valid and yields a Manager that serves static styles only, which is what
// every existing test wants.
type Deps struct {
	// Tokens persists a refreshed OAuth token. Nil disables refresh, which
	// makes an expiring token an error rather than a silent 401.
	Tokens TokenStore
	// OAuth resolves a preset id to its OAuth endpoints. Nil means no preset
	// data, which the oauth strategy reports rather than guesses around.
	OAuth OAuthPresets
	// HTTP is the client the strategies use for token exchange. Nil uses
	// http.DefaultClient.
	HTTP *http.Client
}

// Manager resolves a (target, credential) pair into an Authorizer and caches
// what is expensive to rebuild — a signer, a token source, a per-account mutex.
type Manager struct {
	deps Deps

	mu     sync.Mutex
	gcp    map[string]*gcpSource
	oauth  map[string]*oauthAccount
	awsMu  sync.Once
	awsCfg awsChain
}

func NewManager(d Deps) *Manager {
	return &Manager{
		deps:  d,
		gcp:   map[string]*gcpSource{},
		oauth: map[string]*oauthAccount{},
	}
}

// For returns the authorizer for one credential, or nil for a static style.
func (m *Manager) For(ctx context.Context, t Target, c Credential) (Authorizer, error) {
	if IsStatic(t.Style) {
		return nil, nil
	}
	switch t.Style {
	case StyleSigV4:
		return m.sigv4(ctx, t, c)
	case StyleGCPSA:
		return m.gcpSA(ctx, t, c)
	case StyleOAuth:
		return m.oauthFor(ctx, t, c)
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupportedStyle, t.Style)
}
```

`sync` joins the import block; the three `m.*` methods arrive in Tasks 3, 8 and
15. Until then, stub them so the package builds:

```go
// Placeholders replaced in Tasks 3, 8 and 15. Returning the unsupported error
// rather than nil keeps a half-wired build honest: a provider configured for a
// strategy this commit does not have fails at resolution, naming the style.
func (m *Manager) sigv4(context.Context, Target, Credential) (Authorizer, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnsupportedStyle, StyleSigV4)
}

func (m *Manager) gcpSA(context.Context, Target, Credential) (Authorizer, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnsupportedStyle, StyleGCPSA)
}

func (m *Manager) oauthFor(context.Context, Target, Credential) (Authorizer, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnsupportedStyle, StyleOAuth)
}

// Placeholder types, given their real definitions in Tasks 2, 8 and 15.
type TokenStore interface{}
type OAuthPresets interface{}
type gcpSource struct{}
type oauthAccount struct{}
type awsChain struct{}
```

Create `internal/auth/static.go`:

```go
package auth

// The closed style vocabulary, matching catalog.AuthStyles. Duplicating the
// names rather than importing catalog is deliberate: catalog imports provider,
// which imports store, and a credential strategy that pulled that in would make
// internal/auth unusable from anywhere near the bottom of the tree.
const (
	StyleBearer     = "bearer"
	StyleXAPIKey    = "x-api-key"
	StyleAPIKey     = "api-key"
	StyleQueryParam = "query-param"
	StyleNone       = "none"
	StyleSigV4      = "sigv4"
	StyleGCPSA      = "gcp-sa"
	StyleOAuth      = "oauth"
)

// IsStatic reports whether the style is served by a bare credential the adapter
// writes itself.
//
// The empty string is static. A provider row that never declared a style falls
// back to bearer everywhere else, and treating the gap as non-static would
// route every unconfigured provider into a signer.
func IsStatic(style string) bool {
	switch style {
	case "", StyleBearer, StyleXAPIKey, StyleAPIKey, StyleQueryParam, StyleNone:
		return true
	}
	return false
}
```

- [ ] **Step 4: Widen the target and the provider**

In `internal/adapter/adapter.go`, add to `Target`:

```go
	// Region, Project and Location are endpoint properties rather than parts
	// of the model identifier. Spec §3.3 is explicit for Bedrock — what carries
	// a geo prefix is the inference profile, not the region — and §4.2 puts
	// Vertex's project and location on the provider row.
	Region   string
	Project  string
	Location string

	// Publisher selects Vertex's request builder. Empty for every other kind.
	Publisher string
```

In `internal/provider/provider.go`, add to `Provider`:

```go
	// Region, Project and Location are the endpoint properties bedrock and
	// vertex need. They have been columns on providers since migration 0001
	// and, until this phase, nothing read them.
	Region   string
	Project  string
	Location string
```

and to `Credential`:

```go
	// Kind is static, sigv4, gcp_sa or oauth. It says how to read Secret: a
	// bare key, a service-account document, or a marshalled token.
	Kind string
```

In `internal/provider/sqlsource.go`, widen the query and the scan. The `row`
struct gains three fields and the `SELECT` gains three columns:

```go
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT id, preset, kind, base_url, auth_style, priority, region, project, location
		   FROM providers
		  WHERE enabled = 1
		  ORDER BY priority DESC, id`)
```

```go
	type row struct {
		id, preset, kind, baseURL, authStyle string
		priority                             int
		region, project, location            string
	}
```

```go
		if err := rows.Scan(&r.id, &r.preset, &r.kind, &r.baseURL, &r.authStyle,
			&r.priority, &r.region, &r.project, &r.location); err != nil {
```

```go
		out = append(out, Provider{
			ID: r.id, Kind: r.kind, BaseURL: r.baseURL,
			Preset: r.preset, AuthStyle: r.authStyle,
			Region: r.region, Project: r.project, Location: r.location,
			Credentials: enabled,
			Priority:    r.priority, Models: models,
		})
```

and `enabledOnly` carries the credential kind across:

```go
func enabledOnly(creds []store.Credential) []Credential {
	out := make([]Credential, 0, len(creds))
	for _, c := range creds {
		if !c.Enabled {
			continue
		}
		out = append(out, Credential{ID: c.ID, Secret: c.Secret, Kind: c.Kind, Enabled: true})
	}
	return out
}
```

**Read the existing `enabledOnly` before replacing it** — it may already filter
on something this snippet drops.

- [ ] **Step 5: Wire the seam into the executor**

In `internal/exec/exec.go`, add to `Deps`:

```go
	// Auth resolves a non-static credential into an authorizer. Nil serves
	// static styles only, which is every provider before this phase.
	Auth AuthResolver
```

and beside the other dependency interfaces:

```go
// AuthResolver turns a credential into an authorization applied to the built
// request. It is an interface rather than *auth.Manager so a test can hand over
// a fixed authorizer without constructing a signer.
type AuthResolver interface {
	For(ctx context.Context, t auth.Target, c auth.Credential) (auth.Authorizer, error)
}
```

Add the helper and the resolution beside `secretOf`:

```go
// applyAuthorizer runs the authorizer against a request whose body is already
// materialized. Split out so the ordering — materialize, then authorize, then
// send — is one named thing a test can hold rather than three lines in the
// middle of the attempt loop.
func applyAuthorizer(ctx context.Context, hr *http.Request, a auth.Authorizer) error {
	if a == nil {
		return nil
	}
	return a(ctx, hr)
}

// credentialFor returns the target's authorizer and the api key the adapter
// should write. Exactly one of them is ever non-zero: a non-static style leaves
// the key empty so no adapter writes a token document into its own header.
func (e *Executor) credentialFor(ctx context.Context, p provider.Provider,
	c router.Candidate) (string, auth.Authorizer, error) {

	style := p.AuthStyle
	if style == "" {
		style = presetStyle(p.Preset)
	}
	secret := secretOf(p, c.KeyID)
	if auth.IsStatic(style) {
		return secret, nil, nil
	}
	if e.deps.Auth == nil {
		return "", nil, fmt.Errorf("provider %q needs the %s strategy, which is not wired",
			p.ID, style)
	}
	az, err := e.deps.Auth.For(ctx, auth.Target{
		ProviderID: p.ID, Style: style, Preset: p.Preset,
		Region: p.Region, Project: p.Project, Location: p.Location,
	}, auth.Credential{ID: c.KeyID, Kind: credentialKind(p, c.KeyID), Secret: secret})
	if err != nil {
		return "", nil, err
	}
	return "", az, nil
}

func credentialKind(p provider.Provider, keyID string) string {
	for _, c := range p.Credentials {
		if c.ID == keyID {
			return c.Kind
		}
	}
	return ""
}

// presetStyle reads the shipped style for a provider whose row does not
// override it. It mirrors rerankPath, which already reaches presets from here.
func presetStyle(preset string) string {
	if preset == "" {
		return ""
	}
	return catalog.Embedded()[preset].Auth.Style
}
```

Then in the attempt path, replace the target construction and add the authorize
step. The two edits are at `internal/exec/exec.go:335` and immediately after
`makeReplayable`:

```go
	apiKey, authorizer, err := e.credentialFor(ctx, p, c)
	if err != nil {
		return adapter.OutcomeFatal, 0, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}
	tgt := &adapter.Target{
		BaseURL: p.BaseURL, APIKey: apiKey, Model: c.Model,
		Info:       modelInfo(cat, c.ProviderID, c.Model),
		RerankPath: rerankPath(p.Preset),
		Region:     p.Region, Project: p.Project, Location: p.Location,
		Publisher: c.Publisher,
	}
```

```go
	if err := makeReplayable(hr); err != nil {
		return adapter.OutcomeFatal, 0, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}
	if err := applyAuthorizer(ctx, hr, authorizer); err != nil {
		// A credential that cannot be produced is a credential failure, not a
		// provider one: an expired OAuth grant must cool the account rather
		// than the upstream, which is serving everyone else fine.
		return adapter.OutcomeRetryableCredential, 0,
			&ir.Error{Type: ir.ErrAuthentication, Message: err.Error()}
	}
```

**`exec` already imports `catalog`** for `modelInfo`; confirm before adding it.

- [ ] **Step 6: Run the tests**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/auth/ ./internal/exec/ ./internal/provider/ -count=1
```

Expected: PASS. Every existing executor test still passes because a static
provider resolves to a nil authorizer and an unchanged `APIKey`.

- [ ] **Step 7: Verify the whole tree**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -count=1 && go vet ./... && gofmt -l .
```

- [ ] **Step 8: Commit**

```bash
git add internal/auth internal/adapter/adapter.go internal/provider internal/exec
git commit -m "feat(auth): add the authorization seam"
```

---

### Task 2: The stored token document

**Files:**
- Create: `internal/auth/token.go`
- Modify: `internal/store/credentials.go`
- Test: `internal/auth/token_test.go`, `internal/store/credentials_token_test.go`

**Interfaces:**
- Consumes: `auth.Credential` from Task 1.
- Produces: `auth.Token`, `auth.ParseToken([]byte) (Token, error)`, `(Token).Marshal() ([]byte, error)`, `(Token).Expired(now time.Time, delta time.Duration) bool`; `store.Credential.ExpiresAt *int64`; `(*DB).ReplaceCredentialSecret(ctx, key, id string, secret string, expiresAt *int64) error`; `(*DB).ExpiringCredentials(ctx, key, kind string, before int64) ([]Credential, error)`; `(*DB).DisableCredential(ctx, id, reason string) error`.

**There is no migration in this phase.** `provider_keys` has carried
`kind (static|sigv4|gcp_sa|oauth)`, `expires_at`, `scope` and `enabled` since
migration 0001 — master design §11 wrote the column list for the whole product,
not for phase 2 — and `providers` has carried `region`, `project` and
`location`. Confirm this before writing any code:

```bash
grep -A 12 'CREATE TABLE provider_keys' internal/store/migrations/0001_init.sql
```

**One ciphertext holds the whole token document.** Access token, refresh token
and expiry are marshalled to JSON and sealed together. Separate columns would
make the rotation rule in spec §5.2 — persist the new pair before the old is
considered replaced — a two-column write that a crash can tear in half. One
`UPDATE` of one column cannot tear.

**`expires_at` is mirrored to its own column, unencrypted.** It is not a secret,
and the refresh worker has to find rows expiring soon without decrypting every
credential in the database on every tick.

- [ ] **Step 1: Write the failing test**

Create `internal/auth/token_test.go`:

```go
package auth

import (
	"testing"
	"time"
)

func TestTokenRoundTrips(t *testing.T) {
	want := Token{
		AccessToken:  "at-1",
		RefreshToken: "rt-1",
		TokenType:    "Bearer",
		ExpiresAt:    time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		Scopes:       []string{"user:inference"},
	}
	raw, err := want.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Errorf("tokens did not survive: %+v", got)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("expiry = %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "user:inference" {
		t.Errorf("scopes = %v", got.Scopes)
	}
}

func TestExpiredUsesTheDelta(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	tok := Token{ExpiresAt: now.Add(2 * time.Minute)}

	// Inside the delta: still valid by the clock, but a request started now
	// could easily arrive after it expires. Refreshing early is the whole
	// point of the delta.
	if !tok.Expired(now, 5*time.Minute) {
		t.Error("a token inside the refresh delta must count as expired")
	}
	if tok.Expired(now, time.Minute) {
		t.Error("a token outside the delta must not")
	}
}

func TestAZeroExpiryNeverExpires(t *testing.T) {
	// Some vendors issue no expiry at all. Treating the zero time as "expired
	// in 1970" would refresh on every single request.
	tok := Token{AccessToken: "at"}
	if tok.Expired(time.Now(), time.Minute) {
		t.Error("a token with no stated expiry must not be treated as expired")
	}
}

func TestParseRefusesGarbage(t *testing.T) {
	if _, err := ParseToken([]byte("not json")); err == nil {
		t.Fatal("a malformed document must be an error")
	}
}
```

Create `internal/store/credentials_token_test.go`:

```go
package store

import (
	"context"
	"testing"
)

func TestReplaceCredentialSecretIsAtomic(t *testing.T) {
	ctx := context.Background()
	db, key := credentialFixture(t)

	id, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p", Label: "sub", Kind: "oauth", Secret: `{"access_token":"old"}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	exp := int64(1800000000)
	if err := db.ReplaceCredentialSecret(ctx, key, id, `{"access_token":"new"}`, &exp); err != nil {
		t.Fatal(err)
	}

	creds, err := db.Credentials(ctx, key, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 {
		t.Fatalf("got %d credentials, want 1", len(creds))
	}
	if creds[0].Secret != `{"access_token":"new"}` {
		t.Errorf("secret = %q, want the replacement", creds[0].Secret)
	}
	// The AAD is the credential id, so a replacement that changed the id would
	// silently produce a row nothing can decrypt.
	if creds[0].ID != id {
		t.Errorf("id changed to %q", creds[0].ID)
	}
	if creds[0].ExpiresAt == nil || *creds[0].ExpiresAt != exp {
		t.Errorf("expires_at = %v, want %d", creds[0].ExpiresAt, exp)
	}
}

func TestExpiringCredentialsFindsOnlyItsKind(t *testing.T) {
	ctx := context.Background()
	db, key := credentialFixture(t)

	soon, late := int64(100), int64(9000)
	if _, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p", Kind: "oauth", Secret: "a", ExpiresAt: &soon}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p", Kind: "oauth", Secret: "b", ExpiresAt: &late}); err != nil {
		t.Fatal(err)
	}
	// A static key has no expiry and must never be handed to a refresh worker.
	if _, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p", Kind: "static", Secret: "c"}); err != nil {
		t.Fatal(err)
	}

	got, err := db.ExpiringCredentials(ctx, key, "oauth", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(got), got)
	}
	if got[0].Secret != "a" {
		t.Errorf("returned the wrong row: %q", got[0].Secret)
	}
}

func TestDisableCredentialRecordsWhy(t *testing.T) {
	ctx := context.Background()
	db, key := credentialFixture(t)
	id, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p", Kind: "oauth", Secret: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DisableCredential(ctx, id, "reconnection required"); err != nil {
		t.Fatal(err)
	}
	creds, err := db.Credentials(ctx, key, "p")
	if err != nil {
		t.Fatal(err)
	}
	if creds[0].Enabled {
		t.Error("the credential is still enabled")
	}
	if creds[0].Scope != "reconnection required" {
		t.Errorf("scope = %q; the reason was not recorded", creds[0].Scope)
	}
}
```

`credentialFixture` builds a migrated database, a keyring and one provider row.
**Look for an existing helper in `internal/store/credentials_test.go` first** and
use it if there is one; write this only if there is not:

```go
func credentialFixture(t *testing.T) (*DB, *crypto.Key) {
	t.Helper()
	ctx := context.Background()
	db := migrated(t)
	key, err := OpenKeyring(ctx, db, "test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'anthropic', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	return db, key
}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/auth/ ./internal/store/ -run 'Token|Expiring|DisableCredential|ReplaceCredential' 2>&1 | head -20
```

Expected: undefined `Token`, `ParseToken`, `ReplaceCredentialSecret`,
`ExpiringCredentials`, `DisableCredential`, and `Credential.ExpiresAt`.

- [ ] **Step 3: Write the token document**

Create `internal/auth/token.go`:

```go
package auth

import (
	"encoding/json"
	"fmt"
	"time"
)

// Token is what a non-static credential stores in place of a bare key.
//
// The whole document is sealed into one ciphertext column rather than split
// across several. Spec §5.2 requires the new pair to be persisted before the
// old is considered replaced, and a single-column write is the only version of
// that a crash cannot tear in half.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`

	// Account is whatever the vendor calls the connected identity, kept only
	// so the dashboard can say which account a credential belongs to. It is
	// display text and nothing routes on it.
	Account string `json:"account,omitempty"`
}

func (t Token) Marshal() ([]byte, error) {
	raw, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("marshal token: %w", err)
	}
	return raw, nil
}

func ParseToken(raw []byte) (Token, error) {
	var t Token
	if err := json.Unmarshal(raw, &t); err != nil {
		return Token{}, fmt.Errorf("parse stored token: %w", err)
	}
	return t, nil
}

// Header returns the Authorization value. Vendors are inconsistent about the
// token type; an empty one is Bearer, which is what every OAuth2 provider in
// the preset set actually issues.
func (t Token) Header() string {
	typ := t.TokenType
	if typ == "" {
		typ = "Bearer"
	}
	return typ + " " + t.AccessToken
}

// Expired reports whether the token should be refreshed before use.
//
// The zero expiry means the vendor stated none, which is not the same as
// "expired in 1970": treating it that way would refresh on every request and
// burn a rotating refresh token per call.
func (t Token) Expired(now time.Time, delta time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return !now.Add(delta).Before(t.ExpiresAt)
}

// Unix returns the expiry for the expires_at column, or nil when there is none.
func (t Token) Unix() *int64 {
	if t.ExpiresAt.IsZero() {
		return nil
	}
	u := t.ExpiresAt.Unix()
	return &u
}
```

- [ ] **Step 4: Extend the credential store**

In `internal/store/credentials.go`, add to `Credential`:

```go
	// ExpiresAt mirrors the token's expiry into its own column. It is not a
	// secret, and the refresh worker has to find rows expiring soon without
	// decrypting every credential in the database on every tick.
	ExpiresAt *int64
```

Widen the insert to carry it — the existing statement omits `expires_at`
entirely:

```go
	_, err = e.ExecContext(ctx,
		`INSERT INTO provider_keys (id, provider_id, label, kind, ciphertext, nonce, scope, enabled, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, c.ProviderID, c.Label, kind, ciphertext, nonce, c.Scope, enabled, c.ExpiresAt)
```

and the read, in `Credentials`:

```go
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, provider_id, label, kind, ciphertext, nonce, scope, enabled, expires_at
		   FROM provider_keys
		  WHERE provider_id = ?
		  ORDER BY id`, providerID)
```

```go
		if err := rows.Scan(&c.ID, &c.ProviderID, &c.Label, &c.Kind,
			&ciphertext, &nonce, &c.Scope, &enabled, &c.ExpiresAt); err != nil {
```

Then add the three new methods:

```go
// ReplaceCredentialSecret rewrites one credential's sealed payload in place.
//
// In place matters: the ciphertext is bound to the credential id as additional
// authenticated data, so a rotation that inserted a new row and deleted the old
// would change the AAD and produce something nothing can open. It is also what
// makes spec §5.2's ordering hold — one row, one column, one write, so a crash
// leaves either the old pair or the new one and never a mixture.
func (d *DB) ReplaceCredentialSecret(ctx context.Context, key *crypto.Key,
	id, secret string, expiresAt *int64) error {

	if secret == "" {
		return errors.New("refusing to store an empty credential")
	}
	ciphertext, nonce, err := key.Seal([]byte(secret), []byte(id))
	if err != nil {
		return fmt.Errorf("seal credential %s: %w", id, err)
	}
	res, err := d.Sync.ExecContext(ctx,
		`UPDATE provider_keys SET ciphertext = ?, nonce = ?, expires_at = ? WHERE id = ?`,
		ciphertext, nonce, expiresAt, id)
	if err != nil {
		return fmt.Errorf("replace credential %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// The credential was deleted while a refresh was in flight. Silently
		// succeeding would leave the worker believing it had persisted a token
		// that does not exist.
		return fmt.Errorf("credential %s no longer exists", id)
	}
	return nil
}

// ExpiringCredentials returns every enabled credential of one kind whose expiry
// is at or before before, decrypted, oldest first.
func (d *DB) ExpiringCredentials(ctx context.Context, key *crypto.Key,
	kind string, before int64) ([]Credential, error) {

	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, provider_id, label, kind, ciphertext, nonce, scope, enabled, expires_at
		   FROM provider_keys
		  WHERE kind = ? AND enabled = 1 AND expires_at IS NOT NULL AND expires_at <= ?
		  ORDER BY expires_at`, kind, before)
	if err != nil {
		return nil, fmt.Errorf("list expiring credentials: %w", err)
	}
	defer rows.Close()
	return scanCredentials(rows, key)
}

// DisableCredential takes a credential out of rotation and records why.
//
// The reason lands in scope, which is the column master design §11 already
// gives to per-credential text and which nothing else writes for an OAuth row.
// Adding a column for a string the dashboard renders once would be schema
// churn for a display concern.
func (d *DB) DisableCredential(ctx context.Context, id, reason string) error {
	_, err := d.Sync.ExecContext(ctx,
		`UPDATE provider_keys SET enabled = 0, scope = ? WHERE id = ?`, reason, id)
	if err != nil {
		return fmt.Errorf("disable credential %s: %w", id, err)
	}
	return nil
}
```

`Credentials` and `ExpiringCredentials` now share their row loop; factor it into
`scanCredentials(rows *sql.Rows, key *crypto.Key) ([]Credential, error)` carrying
the existing "a row that fails authentication is an error rather than a skip"
comment across, and call it from both.

- [ ] **Step 5: Run the tests**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/auth/ ./internal/store/ -count=1
```

Expected: PASS.

- [ ] **Step 6: Verify the whole tree, then commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/auth internal/store
git commit -m "feat(auth): store rotating token documents"
```

---

### Task 3: SigV4, against known-answer vectors

**Files:**
- Create: `internal/auth/sigv4.go`
- Modify: `internal/auth/auth.go`, `go.mod`
- Test: `internal/auth/sigv4_test.go`

**Interfaces:**
- Consumes: `auth.Target`, `auth.Credential`, `auth.Authorizer` from Task 1.
- Produces: `(*Manager).sigv4`, `auth.AWSCredentials`, `auth.ParseAWSCredentials([]byte) (AWSCredentials, error)`.

Spec §7: "SigV4 is tested against known-answer vectors: fixed request, fixed
credentials, fixed timestamp, fixed `Authorization` header. Canonicalization
mistakes otherwise surface only as an opaque 403 from a live call." The two
vectors below were produced by running `aws-sdk-go-v2`'s signer against exactly
the inputs in the test — they are not transcribed from documentation.

**The signature covers `Content-Length`.** `SignedHeaders` in both vectors
includes `content-length`, because `makeReplayable` sets `ContentLength` before
the authorizer runs. A future change that signs before materializing the body
would silently drop that header from the signature and produce a valid-looking
403. This is the concrete reason Task 1 put the seam where it did.

**The model id is path-escaped.** Bedrock model and inference-profile ids
contain a colon (`anthropic.claude-3-5-sonnet-20241022-v2:0`), and the canonical
URI the signature covers is the escaped path. The vector was generated against
`%3A`, so the builder in Task 4 must use `url.PathEscape`.

- [ ] **Step 1: Add the dependency**

```bash
export PATH=$PATH:/usr/local/go/bin
go get github.com/aws/aws-sdk-go-v2/aws github.com/aws/aws-sdk-go-v2/aws/signer/v4 github.com/aws/aws-sdk-go-v2/config
go mod tidy
```

`config` pulls `sso`, `ssooidc`, `sts` and `imds` in as indirect dependencies.
That is the cost of spec §3.2's "environment, shared config, or instance role"
and is expected, not a mistake to undo.

- [ ] **Step 2: Write the failing test**

Create `internal/auth/sigv4_test.go`:

```go
package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The known-answer vectors. Both were produced by signing exactly these inputs
// with aws-sdk-go-v2's standalone signer; a canonicalization change anywhere in
// the request builder moves the signature and fails here rather than at a live
// 403 with no cause attached.
const (
	katURL  = "https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-3-5-sonnet-20241022-v2%3A0/converse"
	katBody = `{"messages":[{"role":"user","content":[{"text":"hi"}]}]}`

	katAuth = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260824/us-east-1/bedrock/aws4_request, " +
		"SignedHeaders=content-length;content-type;host;x-amz-date, " +
		"Signature=660ed7b1b462eeaeaff156beba4ff25abda5bc3ad8c8bf10cebf4ec2fb4dc740"

	katAuthWithSession = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260824/us-east-1/bedrock/aws4_request, " +
		"SignedHeaders=content-length;content-type;host;x-amz-date;x-amz-security-token, " +
		"Signature=a1ddcb7dfe82d5ee2f205fa96176dd88c463490e87ea4176f22f4143fad1f035"
)

func katRequest(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequest("POST", katURL, strings.NewReader(katBody))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	return r
}

func katTime() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) }

func TestSigV4MatchesTheKnownAnswer(t *testing.T) {
	az := signerFor(AWSCredentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}, "bedrock", "us-east-1", katTime)

	r := katRequest(t)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("X-Amz-Date"); got != "20260824T120000Z" {
		t.Errorf("X-Amz-Date = %q", got)
	}
	if got := r.Header.Get("Authorization"); got != katAuth {
		t.Errorf("Authorization mismatch\n got: %s\nwant: %s", got, katAuth)
	}
}

func TestSigV4CarriesASessionToken(t *testing.T) {
	// Instance-role and assumed-role credentials always carry one, and it is
	// part of the signature rather than an extra header alongside it.
	az := signerFor(AWSCredentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		SessionToken:    "sess-token",
	}, "bedrock", "us-east-1", katTime)

	r := katRequest(t)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("X-Amz-Security-Token"); got != "sess-token" {
		t.Errorf("X-Amz-Security-Token = %q", got)
	}
	if got := r.Header.Get("Authorization"); got != katAuthWithSession {
		t.Errorf("Authorization mismatch\n got: %s\nwant: %s", got, katAuthWithSession)
	}
}

func TestSigV4SignsTheBodyItSees(t *testing.T) {
	// The payload hash is part of the signature, so two different bodies must
	// not produce the same one. This is the assertion that fails if a refactor
	// ever signs before the body is materialized.
	sign := func(body string) string {
		r, err := http.NewRequest("POST", katURL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		az := signerFor(AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "k"},
			"bedrock", "us-east-1", katTime)
		if err := az(context.Background(), r); err != nil {
			t.Fatal(err)
		}
		return r.Header.Get("Authorization")
	}
	if sign(katBody) == sign(katBody+" ") {
		t.Fatal("two different bodies produced the same signature")
	}
}

func TestSigV4LeavesTheBodyReadable(t *testing.T) {
	// Signing hashes the body. A signer that consumed it would send an empty
	// payload under a signature computed over the real one.
	r := katRequest(t)
	az := signerFor(AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "k"},
		"bedrock", "us-east-1", katTime)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(katBody))
	n, _ := r.Body.Read(buf)
	if string(buf[:n]) != katBody {
		t.Errorf("body after signing = %q", buf[:n])
	}
}

func TestExplicitAWSCredentialsParse(t *testing.T) {
	got, err := ParseAWSCredentials([]byte(
		`{"access_key_id":"AKID","secret_access_key":"SECRET","session_token":"S"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != "AKID" || got.SecretAccessKey != "SECRET" || got.SessionToken != "S" {
		t.Errorf("parsed = %+v", got)
	}
}

func TestSigV4NeedsARegion(t *testing.T) {
	// Region is an endpoint property on the provider row, spec §3.3. Signing
	// for the wrong one produces a 403 that reads as a bad key, so an absent
	// one is refused up front.
	m := NewManager(Deps{})
	_, err := m.For(context.Background(),
		Target{ProviderID: "p", Style: StyleSigV4},
		Credential{ID: "k", Kind: "sigv4", Secret: `{"access_key_id":"A","secret_access_key":"B"}`})
	if err == nil {
		t.Fatal("a sigv4 provider with no region must be refused")
	}
	if !strings.Contains(err.Error(), "region") {
		t.Errorf("the error should name the cause, got %v", err)
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/auth/ -run SigV4 2>&1 | head -20
```

Expected: undefined `signerFor`, `AWSCredentials`, `ParseAWSCredentials`.

- [ ] **Step 4: Implement**

Create `internal/auth/sigv4.go`:

```go
package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// emptyPayloadHash is the SHA-256 of the empty string, which SignHTTP requires
// even for a request with no body.
const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// AWSCredentials is the explicit key pair a provider row may carry. Spec §3.2
// prefers it when given and falls back to the standard chain otherwise.
type AWSCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
}

func ParseAWSCredentials(raw []byte) (AWSCredentials, error) {
	var c AWSCredentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return AWSCredentials{}, fmt.Errorf("parse aws credentials: %w", err)
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return AWSCredentials{}, errors.New("aws credentials need an access key id and a secret access key")
	}
	return c, nil
}

// signerFor returns an authorizer that signs with fixed credentials.
//
// now is injected so the known-answer vectors can pin a timestamp. Everything
// else about the signature is derived from the request, which is the property
// the vectors exist to protect.
func signerFor(creds AWSCredentials, service, region string, now func() time.Time) Authorizer {
	signer := v4.NewSigner()
	awsCreds := aws.Credentials{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
	}
	return func(ctx context.Context, r *http.Request) error {
		hash, err := payloadHash(r)
		if err != nil {
			return err
		}
		return signer.SignHTTP(ctx, awsCreds, r, hash, service, region, now().UTC())
	}
}

// payloadHash hashes the body and puts it back. The body is already
// materialized by the time an authorizer runs, so this reads a bytes.Reader
// rather than a network stream — but restoring it is not optional: signing
// hashes the payload and sending it requires the same bytes again.
func payloadHash(r *http.Request) (string, error) {
	if r.Body == nil {
		return emptyPayloadHash, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", fmt.Errorf("read body for signing: %w", err)
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// sigv4 resolves the credential and returns its authorizer.
func (m *Manager) sigv4(ctx context.Context, t Target, c Credential) (Authorizer, error) {
	if t.Region == "" {
		return nil, fmt.Errorf("provider %q uses sigv4 but declares no region", t.ProviderID)
	}
	if c.Secret != "" {
		creds, err := ParseAWSCredentials([]byte(c.Secret))
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", t.ProviderID, err)
		}
		return signerFor(creds, "bedrock", t.Region, time.Now), nil
	}
	// No explicit credential: environment, shared config, or instance role.
	// Resolved through the chain on every call rather than cached, because an
	// instance-role credential expires and the chain is what renews it.
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(t.Region))
	if err != nil {
		return nil, fmt.Errorf("provider %q: aws credential chain: %w", t.ProviderID, err)
	}
	signer := v4.NewSigner()
	return func(ctx context.Context, r *http.Request) error {
		creds, err := cfg.Credentials.Retrieve(ctx)
		if err != nil {
			return fmt.Errorf("retrieve aws credentials: %w", err)
		}
		hash, err := payloadHash(r)
		if err != nil {
			return err
		}
		return signer.SignHTTP(ctx, creds, r, hash, "bedrock", t.Region, time.Now().UTC())
	}, nil
}
```

Delete the `sigv4` placeholder and the `awsChain` placeholder type from
`auth.go`, along with the now-unused `awsMu sync.Once` and `awsCfg` fields.

- [ ] **Step 5: Run the tests**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/auth/ -count=1 -v -run SigV4 2>&1 | tail -20
```

Expected: PASS on all four. **If a signature mismatches, do not edit the
expected value** — the vectors are the specification here, and a mismatch means
the request being signed differs from the one they were generated against. Print
`r.Header` and compare `SignedHeaders` first; a stray header is the usual cause.

- [ ] **Step 6: Verify the whole tree, then commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/auth go.mod go.sum
git commit -m "feat(auth): sign requests with sigv4"
```

---

### Task 4: The Bedrock request builder

**Files:**
- Create: `internal/adapter/bedrock/adapter.go`, `internal/adapter/bedrock/build.go`
- Test: `internal/adapter/bedrock/build_test.go`

**Interfaces:**
- Consumes: `adapter.Target` (with `Region`) from Task 1.
- Produces: `bedrock.New() *Adapter`, `(*Adapter).Kind() string`, `(*Adapter).BuildRequest(ctx, *adapter.Target, *ir.Request) (*http.Request, []ir.Warning, error)`, `bedrock.EndpointFor(region string) string`.

Spec §3.1: Converse, not InvokeModel. `InvokeModel` takes each model family's
native payload — a Claude shape, a Llama shape, a Mistral shape — reintroducing
exactly the per-family branching this design exists to avoid.

**The endpoint is derived from the region, because the preset's base URL is
empty.** `bedrock`'s preset declares `base_url: ""` precisely because there is no
single host: `bedrock-runtime.{region}.amazonaws.com`. A provider row that sets
`base_url` explicitly still wins, which is what makes a VPC endpoint or a test
server reachable.

**Bedrock is never passthrough-eligible.** Master design §4.1 says so, and §3.2
gives the reason: the signature covers a payload hash, so the body must be
materialized. Nothing in this task needs to enforce that — phase 9's eligibility
list already excludes the kind — but it is why no effort goes into keeping the
inbound body intact here.

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/bedrock/build_test.go`:

```go
package bedrock

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func build(t *testing.T, tgt *adapter.Target, req *ir.Request) (map[string]any, string, []ir.Warning) {
	t.Helper()
	hr, warns, err := New().BuildRequest(context.Background(), tgt, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, raw)
	}
	return body, hr.URL.String(), warns
}

func simple() *ir.Request {
	return &ir.Request{
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
	}
}

func TestEndpointComesFromTheRegion(t *testing.T) {
	// The preset declares base_url: "" because there is no single host.
	_, url, _ := build(t, &adapter.Target{Region: "eu-west-1", Model: simple().Model}, simple())
	want := "https://bedrock-runtime.eu-west-1.amazonaws.com/model/" +
		"anthropic.claude-3-5-sonnet-20241022-v2%3A0/converse"
	if url != want {
		t.Errorf("url = %s\nwant %s", url, want)
	}
}

func TestAnExplicitBaseURLWins(t *testing.T) {
	// A VPC endpoint, or a test server.
	_, url, _ := build(t, &adapter.Target{
		BaseURL: "https://vpce-x.bedrock-runtime.eu-west-1.vpce.amazonaws.com",
		Region:  "eu-west-1", Model: "m",
	}, simple())
	if url != "https://vpce-x.bedrock-runtime.eu-west-1.vpce.amazonaws.com/model/m/converse" {
		t.Errorf("url = %s", url)
	}
}

func TestStreamingUsesTheStreamRoute(t *testing.T) {
	req := simple()
	req.Stream = true
	_, url, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)
	if got := url[len(url)-len("converse-stream"):]; got != "converse-stream" {
		t.Errorf("streaming url = %s", url)
	}
}

func TestModelIdIsPathEscaped(t *testing.T) {
	// The colon in a model id is part of the canonical URI the signature
	// covers. Task 3's known-answer vector was generated against %3A.
	_, url, _ := build(t, &adapter.Target{Region: "us-east-1", Model: "us.anthropic.claude-x-v1:0"}, simple())
	if want := "us.anthropic.claude-x-v1%3A0"; url[len(url)-len(want)-len("/converse"):len(url)-len("/converse")] != want {
		t.Errorf("url = %s; the model id is not escaped", url)
	}
}

func TestSystemBecomesItsOwnField(t *testing.T) {
	req := simple()
	req.System = []ir.ContentBlock{{Type: ir.BlockText, Text: "be terse"}}
	body, _, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)

	sys, ok := body["system"].([]any)
	if !ok || len(sys) != 1 {
		t.Fatalf("system = %#v", body["system"])
	}
	if sys[0].(map[string]any)["text"] != "be terse" {
		t.Errorf("system block = %#v", sys[0])
	}
	// Converse takes system separately; a system turn folded into messages is
	// a 400 from the API, not a degraded answer.
	msgs := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("system leaked into messages: %#v", msgs)
	}
}

func TestInferenceConfigCarriesSampling(t *testing.T) {
	req := simple()
	max, temp, topP := 256, 0.5, 0.9
	req.MaxTokens, req.Temperature, req.TopP = &max, &temp, &topP
	req.StopSequences = []string{"END"}
	body, _, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)

	cfg, ok := body["inferenceConfig"].(map[string]any)
	if !ok {
		t.Fatalf("inferenceConfig = %#v", body["inferenceConfig"])
	}
	if cfg["maxTokens"] != float64(256) || cfg["temperature"] != 0.5 || cfg["topP"] != 0.9 {
		t.Errorf("inferenceConfig = %#v", cfg)
	}
	if seqs, _ := cfg["stopSequences"].([]any); len(seqs) != 1 || seqs[0] != "END" {
		t.Errorf("stopSequences = %#v", cfg["stopSequences"])
	}
}

func TestTopKIsWarnedNotDropped(t *testing.T) {
	// Converse has no topK in inferenceConfig; it belongs in
	// additionalModelRequestFields, which is per-family. Master design §5
	// requires the loss to be recorded rather than silent.
	req := simple()
	k := 40
	req.TopK = &k
	_, _, warns := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)
	if len(warns) == 0 {
		t.Fatal("dropping top_k must produce a warning")
	}
	found := false
	for _, w := range warns {
		if w.Field == "top_k" {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning named top_k: %+v", warns)
	}
}

func TestToolsBecomeAToolConfig(t *testing.T) {
	req := simple()
	req.Tools = []ir.Tool{{
		Name: "get_weather", Description: "look it up",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`),
	}}
	req.ToolChoice = &ir.ToolChoice{Mode: "tool", Name: "get_weather"}
	body, _, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)

	tc, ok := body["toolConfig"].(map[string]any)
	if !ok {
		t.Fatalf("toolConfig = %#v", body["toolConfig"])
	}
	tools := tc["tools"].([]any)
	spec := tools[0].(map[string]any)["toolSpec"].(map[string]any)
	if spec["name"] != "get_weather" {
		t.Errorf("toolSpec = %#v", spec)
	}
	// inputSchema wraps the JSON Schema in a json key. Sending the schema bare
	// is a validation error from the API.
	if _, ok := spec["inputSchema"].(map[string]any)["json"]; !ok {
		t.Errorf("inputSchema = %#v", spec["inputSchema"])
	}
	choice := tc["toolChoice"].(map[string]any)
	if _, ok := choice["tool"]; !ok {
		t.Errorf("toolChoice = %#v", choice)
	}
}

func TestImagesBecomeImageBlocks(t *testing.T) {
	req := simple()
	req.Messages[0].Content = append(req.Messages[0].Content, ir.ContentBlock{
		Type:  ir.BlockImage,
		Media: &ir.Media{MIME: "image/png", Data: "aGk="},
	})
	body, _, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)

	content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	var img map[string]any
	for _, b := range content {
		if m, ok := b.(map[string]any)["image"].(map[string]any); ok {
			img = m
		}
	}
	if img == nil {
		t.Fatalf("no image block: %#v", content)
	}
	if img["format"] != "png" {
		t.Errorf("format = %v, want png (Converse takes a bare format, not a mime type)", img["format"])
	}
	if _, ok := img["source"].(map[string]any)["bytes"]; !ok {
		t.Errorf("source = %#v", img["source"])
	}
}

func TestAURLImageIsWarnedNotFetched(t *testing.T) {
	// Converse takes bytes only. Fetching the URL here would make an outbound
	// request from a request builder, which no other adapter does.
	req := simple()
	req.Messages[0].Content = append(req.Messages[0].Content, ir.ContentBlock{
		Type: ir.BlockImage, Media: &ir.Media{URL: "https://example.invalid/x.png"},
	})
	_, _, warns := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)
	if len(warns) == 0 {
		t.Fatal("a URL image must produce a warning")
	}
}

func TestToolResultsBecomeUserContent(t *testing.T) {
	req := simple()
	req.Messages = append(req.Messages, ir.Message{
		Role: ir.RoleTool,
		Content: []ir.ContentBlock{{
			Type: ir.BlockToolResult,
			ToolResult: &ir.ToolResult{
				ToolUseID: "tu_1",
				Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: "17C"}},
			},
		}},
	})
	body, _, _ := build(t, &adapter.Target{Region: "us-east-1", Model: req.Model}, req)

	msgs := body["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	// Converse has no tool role. A tool result is user content carrying a
	// toolResult block, and getting this wrong is a 400 on every tool loop.
	if last["role"] != "user" {
		t.Errorf("tool result role = %v, want user", last["role"])
	}
	res := last["content"].([]any)[0].(map[string]any)["toolResult"].(map[string]any)
	if res["toolUseId"] != "tu_1" {
		t.Errorf("toolResult = %#v", res)
	}
}

func TestNoCredentialHeaderIsWritten(t *testing.T) {
	// Signing is the authorizer's job, Task 1. An adapter that also wrote a
	// header would put a key in a request the signature does not cover.
	hr, _, err := New().BuildRequest(context.Background(),
		&adapter.Target{Region: "us-east-1", Model: "m", APIKey: "should-be-ignored"}, simple())
	if err != nil {
		t.Fatal(err)
	}
	if hr.Header.Get("Authorization") != "" || hr.Header.Get("x-api-key") != "" {
		t.Errorf("the builder wrote a credential header: %v", hr.Header)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/bedrock/ 2>&1 | head -10
```

Expected: no such package.

- [ ] **Step 3: Implement**

Create `internal/adapter/bedrock/adapter.go`:

```go
// Package bedrock renders the IR to AWS Bedrock's Converse API.
//
// Converse rather than InvokeModel, spec §3.1: InvokeModel takes each model
// family's native payload, reintroducing exactly the per-family branching this
// design exists to avoid. Converse takes one message shape across families,
// with tool use and image content, and the IR maps to it directly.
//
// Authentication is not this package's concern. internal/auth signs the built
// request, which is what keeps one HTTP transport and one timeout policy across
// every adapter rather than routing through the Bedrock service client.
package bedrock

import (
	"context"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Kind() string { return "bedrock" }

// Surfaces is llm alone. Bedrock serves embeddings through a different API
// shape entirely, and claiming the surface here would route embedding requests
// to a Converse endpoint that answers 400.
func (a *Adapter) Surfaces() adapter.SurfaceSet {
	return adapter.SurfaceSet{ir.SurfaceLLM: true}
}

func (a *Adapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	return BuildRequest(ctx, t, req)
}

func (a *Adapter) ParseResponse(resp *http.Response) (*ir.Response, error) {
	return ParseResponse(resp)
}

func (a *Adapter) ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return ParseStream(r, maxLine)
}

func (a *Adapter) Classify(resp *http.Response, err error) adapter.Outcome {
	return Classify(resp, err)
}
```

Create `internal/adapter/bedrock/build.go`:

```go
package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// EndpointFor derives the runtime host from the region.
//
// The preset declares base_url: "" because there is no single host — spec §3.3
// makes region an endpoint property rather than part of the model identifier,
// and this is the one place that turns it into a URL.
func EndpointFor(region string) string {
	return "https://bedrock-runtime." + region + ".amazonaws.com"
}

func BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	base := strings.TrimRight(t.BaseURL, "/")
	if base == "" {
		if t.Region == "" {
			return nil, nil, fmt.Errorf("bedrock target has neither a base url nor a region")
		}
		base = EndpointFor(t.Region)
	}

	var warns []ir.Warning
	body := map[string]any{}

	messages, mw := renderMessages(req)
	warns = append(warns, mw...)
	body["messages"] = messages

	if sys := renderSystem(req.System); len(sys) > 0 {
		// Converse takes system separately. A system turn folded into messages
		// is a validation error, not a degraded answer.
		body["system"] = sys
	}
	if cfg := inferenceConfig(req); len(cfg) > 0 {
		body["inferenceConfig"] = cfg
	}
	if tc := toolConfig(req); tc != nil {
		body["toolConfig"] = tc
	}
	if req.TopK != nil {
		// topK lives in additionalModelRequestFields, which is per-family and
		// therefore exactly the branching Converse was chosen to avoid.
		warns = append(warns, ir.Warning{
			Field: "top_k", Target: "bedrock",
			Reason: "Converse has no top_k; it is a per-family additional field",
		})
	}
	if req.ResponseFormat != nil {
		warns = append(warns, ir.Warning{
			Field: "response_format", Target: "bedrock",
			Reason: "Converse has no structured-output field",
		})
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, warns, err
	}

	route := "converse"
	if req.Stream {
		route = "converse-stream"
	}
	// The colon in a model or inference-profile id is part of the canonical URI
	// the signature covers, so it is escaped here rather than left raw.
	u := base + "/model/" + url.PathEscape(t.Model) + "/" + route

	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(buf))
	if err != nil {
		return nil, warns, err
	}
	hr.Header.Set("Content-Type", "application/json")
	// No credential header. internal/auth signs this request, and a key written
	// here would travel in a header the signature does not cover.
	return hr, warns, nil
}

func renderSystem(blocks []ir.ContentBlock) []any {
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == ir.BlockText && b.Text != "" {
			out = append(out, map[string]any{"text": b.Text})
		}
	}
	return out
}

func inferenceConfig(req *ir.Request) map[string]any {
	cfg := map[string]any{}
	if req.MaxTokens != nil {
		cfg["maxTokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		cfg["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		cfg["topP"] = *req.TopP
	}
	if len(req.StopSequences) > 0 {
		cfg["stopSequences"] = req.StopSequences
	}
	return cfg
}

func toolConfig(req *ir.Request) map[string]any {
	if len(req.Tools) == 0 {
		return nil
	}
	tools := make([]any, 0, len(req.Tools))
	for _, t := range req.Tools {
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		tools = append(tools, map[string]any{
			"toolSpec": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				// The schema is wrapped in a json key rather than sent bare.
				"inputSchema": map[string]any{"json": schema},
			},
		})
	}
	cfg := map[string]any{"tools": tools}
	if tc := req.ToolChoice; tc != nil {
		switch tc.Mode {
		case "any":
			cfg["toolChoice"] = map[string]any{"any": map[string]any{}}
		case "tool":
			cfg["toolChoice"] = map[string]any{"tool": map[string]any{"name": tc.Name}}
		case "auto":
			cfg["toolChoice"] = map[string]any{"auto": map[string]any{}}
		}
		// "none" has no Converse spelling. Omitting toolChoice is the closest
		// honest rendering; the model may still call a tool, which is why the
		// caller sees this as a dropped field below.
	}
	return cfg
}

// renderMessages maps IR turns to Converse turns.
//
// Converse has no tool role: a tool result is user content carrying a toolResult
// block. Getting that wrong is a 400 on every tool loop, which is why it is the
// first thing the tests assert.
func renderMessages(req *ir.Request) ([]any, []ir.Warning) {
	var warns []ir.Warning
	out := make([]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := "user"
		if m.Role == ir.RoleAssistant {
			role = "assistant"
		}
		content, w := renderBlocks(m.Content)
		warns = append(warns, w...)
		if len(content) == 0 {
			continue
		}
		out = append(out, map[string]any{"role": role, "content": content})
	}
	return out, warns
}

func renderBlocks(blocks []ir.ContentBlock) ([]any, []ir.Warning) {
	var warns []ir.Warning
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case ir.BlockText:
			if b.Text != "" {
				out = append(out, map[string]any{"text": b.Text})
			}
		case ir.BlockImage:
			blk, w := imageBlock(b.Media)
			warns = append(warns, w...)
			if blk != nil {
				out = append(out, blk)
			}
		case ir.BlockToolUse:
			out = append(out, map[string]any{"toolUse": map[string]any{
				"toolUseId": b.ToolUse.ID,
				"name":      b.ToolUse.Name,
				"input":     b.ToolUse.Input,
			}})
		case ir.BlockToolResult:
			inner, w := renderBlocks(b.ToolResult.Content)
			warns = append(warns, w...)
			res := map[string]any{
				"toolUseId": b.ToolResult.ToolUseID,
				"content":   inner,
			}
			if b.ToolResult.IsError {
				res["status"] = "error"
			}
			out = append(out, map[string]any{"toolResult": res})
		case ir.BlockThinking, ir.BlockRedactedThinking:
			// reasoningContent is emitted by the model, not accepted from the
			// client on Converse. Replaying it would be a 400.
			warns = append(warns, ir.Warning{
				Field: "thinking", Target: "bedrock",
				Reason: "Converse does not accept reasoning blocks as input",
			})
		default:
			warns = append(warns, ir.Warning{
				Field: string(b.Type), Target: "bedrock",
				Reason: "no Converse content block for this type",
			})
		}
	}
	return out, warns
}

// imageBlock renders inline bytes. Converse takes a bare format word rather
// than a mime type, and takes no URL at all.
func imageBlock(m *ir.Media) (map[string]any, []ir.Warning) {
	if m == nil {
		return nil, nil
	}
	if m.Data == "" {
		return nil, []ir.Warning{{
			Field: "image", Target: "bedrock",
			Reason: "Converse takes image bytes only; a URL cannot be sent",
		}}
	}
	format, ok := imageFormat(m.MIME)
	if !ok {
		return nil, []ir.Warning{{
			Field: "image", Target: "bedrock",
			Reason: "Converse accepts png, jpeg, gif and webp only; " + m.MIME + " was dropped",
		}}
	}
	return map[string]any{"image": map[string]any{
		"format": format,
		// The IR carries base64; Converse's bytes member is base64 on the wire.
		"source": map[string]any{"bytes": m.Data},
	}}, nil
}

func imageFormat(mime string) (string, bool) {
	switch mime {
	case "image/png":
		return "png", true
	case "image/jpeg", "image/jpg":
		return "jpeg", true
	case "image/gif":
		return "gif", true
	case "image/webp":
		return "webp", true
	}
	return "", false
}
```

`ParseResponse`, `ParseStream` and `Classify` arrive in Tasks 5 and 6. Stub them
so the package builds, and delete the stubs as each lands:

```go
// Replaced in Tasks 5 and 6.
func ParseResponse(resp *http.Response) (*ir.Response, error) {
	_ = resp.Body.Close()
	return nil, errors.New("bedrock: response parsing arrives in task 5")
}

func ParseStream(io.Reader, int) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		yield(ir.StreamEvent{}, errors.New("bedrock: stream parsing arrives in task 6"))
	}
}

func Classify(*http.Response, error) adapter.Outcome { return adapter.OutcomeFatal }
```

- [ ] **Step 4: Run the tests**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/bedrock/ -count=1 -v 2>&1 | tail -25
```

Expected: PASS. **The Converse field names are the one thing here that cannot be
verified without an AWS account.** They are taken from AWS's Converse API
reference; the tests pin what this build believes, and Task 19's golden files
make a later correction a visible diff rather than a silent behavior change.

- [ ] **Step 5: Verify the whole tree, then commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/bedrock
git commit -m "feat(bedrock): render the IR to Converse"
```

---

### Task 5: Parsing a Converse response

**Files:**
- Create: `internal/adapter/bedrock/parse.go`
- Modify: `internal/adapter/bedrock/build.go` (delete the `ParseResponse` and `Classify` stubs)
- Test: `internal/adapter/bedrock/parse_test.go`

**Interfaces:**
- Produces: `bedrock.ParseResponse(*http.Response) (*ir.Response, error)`, `bedrock.Classify(*http.Response, error) adapter.Outcome`, `(*Adapter).ClassifyBody`.

`ParseResponse` takes ownership of `resp.Body` and always closes it, matching
every other adapter's contract in `adapter.Adapter`.

**Bedrock reports a missing model as a 400, not a 404.** `ValidationException`
carrying "model identifier is invalid" is the shape, and classifying it as fatal
would make a failover chain die on the first provider that does not carry the
model. That is exactly what `adapter.BodyClassifier` exists for; `openaicompat`
already implements it for the same reason.

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/bedrock/parse_test.go`:

```go
package bedrock

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func respWith(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

const converseBody = `{
  "output": {"message": {"role": "assistant", "content": [
    {"text": "It is 17C."},
    {"toolUse": {"toolUseId": "tu_1", "name": "get_weather", "input": {"city": "Oslo"}}}
  ]}},
  "stopReason": "tool_use",
  "usage": {"inputTokens": 12, "outputTokens": 7, "totalTokens": 19},
  "metrics": {"latencyMs": 431}
}`

func TestParseResponseCarriesContentAndUsage(t *testing.T) {
	got, err := ParseResponse(respWith(200, converseBody))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 2 {
		t.Fatalf("content = %+v", got.Content)
	}
	if got.Content[0].Type != ir.BlockText || got.Content[0].Text != "It is 17C." {
		t.Errorf("first block = %+v", got.Content[0])
	}
	tu := got.Content[1].ToolUse
	if tu == nil || tu.ID != "tu_1" || tu.Name != "get_weather" {
		t.Fatalf("tool use = %+v", got.Content[1])
	}
	if string(tu.Input) != `{"city":"Oslo"}` && string(tu.Input) != `{"city": "Oslo"}` {
		t.Errorf("tool input = %s", tu.Input)
	}
	if got.StopReason != ir.StopToolUse {
		t.Errorf("stop reason = %q", got.StopReason)
	}
	if got.Usage.InputTokens != 12 || got.Usage.OutputTokens != 7 {
		t.Errorf("usage = %+v", got.Usage)
	}
}

func TestParseResponseMapsEveryStopReason(t *testing.T) {
	for wire, want := range map[string]ir.StopReason{
		"end_turn":          ir.StopEndTurn,
		"max_tokens":        ir.StopMaxTokens,
		"tool_use":          ir.StopToolUse,
		"stop_sequence":     ir.StopStopSequence,
		"content_filtered":  ir.StopContentFilter,
		"guardrail_intervened": ir.StopContentFilter,
	} {
		body := `{"output":{"message":{"role":"assistant","content":[]}},"stopReason":"` + wire + `"}`
		got, err := ParseResponse(respWith(200, body))
		if err != nil {
			t.Fatal(err)
		}
		if got.StopReason != want {
			t.Errorf("%s -> %q, want %q", wire, got.StopReason, want)
		}
	}
}

func TestParseResponseKeepsReasoningContent(t *testing.T) {
	body := `{"output":{"message":{"role":"assistant","content":[
	  {"reasoningContent":{"reasoningText":{"text":"thinking...","signature":"sig"}}},
	  {"text":"answer"}]}},"stopReason":"end_turn"}`
	got, err := ParseResponse(respWith(200, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 2 || got.Content[0].Type != ir.BlockThinking {
		t.Fatalf("content = %+v", got.Content)
	}
	if got.Content[0].Thinking.Text != "thinking..." || got.Content[0].Thinking.Signature != "sig" {
		t.Errorf("thinking = %+v", got.Content[0].Thinking)
	}
}

func TestParseResponseClosesTheBody(t *testing.T) {
	rc := &closeTracker{Reader: strings.NewReader(converseBody)}
	resp := &http.Response{StatusCode: 200, Body: rc, Header: http.Header{}}
	if _, err := ParseResponse(resp); err != nil {
		t.Fatal(err)
	}
	if !rc.closed {
		t.Error("ParseResponse must take ownership of the body and close it")
	}
}

type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error { c.closed = true; return nil }

func TestClassifyBodyTreatsAnUnknownModelAsRetryable(t *testing.T) {
	// A failover chain must not die on the first provider that does not carry
	// the model. Bedrock says so with a 400, not a 404.
	body := []byte(`{"message":"The provided model identifier is invalid."}`)
	got := New().ClassifyBody(respWith(400, string(body)), body, nil)
	if got != adapter.OutcomeRetryableModel {
		t.Errorf("outcome = %q, want retryable_model", got)
	}
}

func TestClassifyBodyLeavesOtherValidationErrorsFatal(t *testing.T) {
	// A malformed request is the client's fault and retrying it against four
	// more providers wastes four more round trips to reach the same answer.
	body := []byte(`{"message":"messages: at least one message is required"}`)
	if got := New().ClassifyBody(respWith(400, string(body)), body, nil); got != adapter.OutcomeFatal {
		t.Errorf("outcome = %q, want fatal", got)
	}
}

func TestClassifyThrottlingIsRetryable(t *testing.T) {
	// Bedrock's ThrottlingException is a 429, which the shared status rule
	// already handles. Asserted so a future override cannot quietly break it.
	if got := Classify(respWith(429, ""), nil); got != adapter.OutcomeRetryableProvider {
		t.Errorf("outcome = %q", got)
	}
	if got := Classify(respWith(403, ""), nil); got != adapter.OutcomeRetryableCredential {
		t.Errorf("403 -> %q, want retryable_credential", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/bedrock/ -run Parse 2>&1 | head -10
```

Expected: the stub's "response parsing arrives in task 5" error.

- [ ] **Step 3: Implement**

Create `internal/adapter/bedrock/parse.go`:

```go
package bedrock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// maxBody bounds what a misbehaving upstream can make the parser allocate. The
// same bound the other adapters use.
const maxBody = 32 << 20

type wireConverse struct {
	Output struct {
		Message struct {
			Role    string      `json:"role"`
			Content []wireBlock `json:"content"`
		} `json:"message"`
	} `json:"output"`
	StopReason string    `json:"stopReason"`
	Usage      wireUsage `json:"usage"`
}

type wireBlock struct {
	Text    string `json:"text"`
	ToolUse *struct {
		ToolUseID string          `json:"toolUseId"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
	} `json:"toolUse"`
	ReasoningContent *struct {
		ReasoningText *struct {
			Text      string `json:"text"`
			Signature string `json:"signature"`
		} `json:"reasoningText"`
		RedactedContent string `json:"redactedContent"`
	} `json:"reasoningContent"`
}

type wireUsage struct {
	InputTokens        int `json:"inputTokens"`
	OutputTokens       int `json:"outputTokens"`
	CacheReadInputTokens  int `json:"cacheReadInputTokens"`
	CacheWriteInputTokens int `json:"cacheWriteInputTokens"`
}

func (u wireUsage) toIR() ir.Usage {
	return ir.Usage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheWriteInputTokens,
	}
}

// stopReason maps Converse's vocabulary. guardrail_intervened and
// content_filtered are the same fact to a client: the model was stopped by a
// policy rather than by the request.
func stopReason(s string) ir.StopReason {
	switch s {
	case "end_turn":
		return ir.StopEndTurn
	case "max_tokens":
		return ir.StopMaxTokens
	case "tool_use":
		return ir.StopToolUse
	case "stop_sequence":
		return ir.StopStopSequence
	case "content_filtered", "guardrail_intervened":
		return ir.StopContentFilter
	}
	return ir.StopEndTurn
}

func blockToIR(b wireBlock) (ir.ContentBlock, bool) {
	switch {
	case b.ToolUse != nil:
		return ir.ContentBlock{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
			ID: b.ToolUse.ToolUseID, Name: b.ToolUse.Name, Input: b.ToolUse.Input,
		}}, true
	case b.ReasoningContent != nil:
		if rt := b.ReasoningContent.ReasoningText; rt != nil {
			return ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{
				Text: rt.Text, Signature: rt.Signature,
			}}, true
		}
		if b.ReasoningContent.RedactedContent != "" {
			return ir.ContentBlock{Type: ir.BlockRedactedThinking, Thinking: &ir.Thinking{
				Data: b.ReasoningContent.RedactedContent,
			}}, true
		}
		return ir.ContentBlock{}, false
	case b.Text != "":
		return ir.ContentBlock{Type: ir.BlockText, Text: b.Text}, true
	}
	return ir.ContentBlock{}, false
}

// ParseResponse takes ownership of resp.Body and always closes it.
func ParseResponse(resp *http.Response) (*ir.Response, error) {
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read bedrock response: %w", err)
	}
	var w wireConverse
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("parse bedrock response: %w", err)
	}

	out := &ir.Response{
		StopReason: stopReason(w.StopReason),
		Usage:      w.Usage.toIR(),
	}
	for _, b := range w.Output.Message.Content {
		if blk, ok := blockToIR(b); ok {
			out.Content = append(out.Content, blk)
		}
	}
	// Converse returns no id and no model name. Leaving both empty is honest;
	// the executor already knows the model it dispatched to.
	return out, nil
}

func Classify(resp *http.Response, err error) adapter.Outcome {
	return adapter.ClassifyStatus(resp, err)
}

// ClassifyBody refines a 400. Bedrock reports a model it does not serve as a
// ValidationException rather than a 404, and treating that as fatal would kill
// a failover chain at its first provider.
func (a *Adapter) ClassifyBody(resp *http.Response, body []byte, err error) adapter.Outcome {
	base := Classify(resp, err)
	if base != adapter.OutcomeFatal || resp == nil || resp.StatusCode != http.StatusBadRequest {
		return base
	}
	text := strings.ToLower(string(body))
	for _, marker := range []string{
		"model identifier is invalid",
		"model id is invalid",
		"could not find model",
		"don't have access to the model",
		"inference profile",
	} {
		if strings.Contains(text, marker) {
			return adapter.OutcomeRetryableModel
		}
	}
	return base
}

var _ adapter.BodyClassifier = (*Adapter)(nil)
```

Delete the `ParseResponse` and `Classify` stubs from `build.go`, and the now
unused `errors` import if nothing else there uses it.

- [ ] **Step 4: Run the tests, verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/bedrock/ -count=1 -v 2>&1 | tail -20
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/bedrock
git commit -m "feat(bedrock): parse Converse responses"
```

---

### Task 6: Eventstream framing

**Files:**
- Create: `internal/adapter/bedrock/stream.go`
- Modify: `internal/adapter/bedrock/build.go` (delete the `ParseStream` stub), `internal/server/server.go`, `internal/golden/golden_test.go`
- Test: `internal/adapter/bedrock/stream_test.go`

**Interfaces:**
- Produces: `bedrock.ParseStream(io.Reader, int) iter.Seq2[ir.StreamEvent, error]`; `bedrock` registered in the executor's adapter map.

Spec §3.2: "`ConverseStream` returns AWS binary eventstream framing
(`application/vnd.amazon.eventstream`), not SSE. The adapter decodes it with
`aws-sdk-go-v2`'s eventstream package rather than the shared SSE reader. 'One
streaming implementation across every adapter' does not hold here, and
pretending otherwise would leave an implementer parsing binary frames with a
line scanner."

**`maxLine` is ignored, and that is correct.** It bounds an SSE line. An
eventstream frame carries its own length prefix, and the decoder validates it
against a CRC; a caller's line bound has nothing to constrain. The parameter
stays in the signature because it is part of `adapter.Adapter`.

**The tests build their frames with the SDK's own `Encoder`.** That is better
than a checked-in binary fixture: a hand-written blob with a wrong CRC would
fail for a reason that has nothing to do with the code under test, and a reader
cannot see what is in it.

- [ ] **Step 1: Add the dependency and write the failing test**

```bash
export PATH=$PATH:/usr/local/go/bin
go get github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream
```

Create `internal/adapter/bedrock/stream_test.go`:

```go
package bedrock

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/darkraise/darkrouter/internal/ir"
)

// frame encodes one eventstream message the way Bedrock does: an event type in
// the headers, a JSON payload in the body.
func frame(t *testing.T, w io.Writer, eventType string, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	msg := eventstream.Message{
		Headers: eventstream.Headers{
			{Name: ":message-type", Value: eventstream.StringValue("event")},
			{Name: ":event-type", Value: eventstream.StringValue(eventType)},
			{Name: ":content-type", Value: eventstream.StringValue("application/json")},
		},
		Payload: raw,
	}
	if err := eventstream.NewEncoder().Encode(w, msg); err != nil {
		t.Fatal(err)
	}
}

func exceptionFrame(t *testing.T, w io.Writer, exceptionType, message string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"message": message})
	msg := eventstream.Message{
		Headers: eventstream.Headers{
			{Name: ":message-type", Value: eventstream.StringValue("exception")},
			{Name: ":exception-type", Value: eventstream.StringValue(exceptionType)},
			{Name: ":content-type", Value: eventstream.StringValue("application/json")},
		},
		Payload: raw,
	}
	if err := eventstream.NewEncoder().Encode(w, msg); err != nil {
		t.Fatal(err)
	}
}

func collect(t *testing.T, r io.Reader) ([]ir.StreamEvent, error) {
	t.Helper()
	var out []ir.StreamEvent
	for ev, err := range ParseStream(r, 1<<20) {
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
	return out, nil
}

func TestStreamDecodesAWholeTurn(t *testing.T) {
	var buf bytes.Buffer
	frame(t, &buf, "messageStart", map[string]any{"role": "assistant"})
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0, "delta": map[string]any{"text": "It is "}})
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0, "delta": map[string]any{"text": "17C."}})
	frame(t, &buf, "contentBlockStop", map[string]any{"contentBlockIndex": 0})
	frame(t, &buf, "messageStop", map[string]any{"stopReason": "end_turn"})
	frame(t, &buf, "metadata", map[string]any{
		"usage": map[string]any{"inputTokens": 12, "outputTokens": 7}})

	events, err := collect(t, &buf)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var sawStart, sawStop bool
	var usage *ir.Usage
	for _, ev := range events {
		switch ev.Type {
		case ir.EventMessageStart:
			sawStart = true
		case ir.EventContentDelta:
			text += ev.Delta.Text
		case ir.EventMessageStop:
			sawStop = true
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}
	if !sawStart || !sawStop {
		t.Errorf("missing start/stop: %+v", events)
	}
	if text != "It is 17C." {
		t.Errorf("text = %q", text)
	}
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 7 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestStreamHandlesAFrameSplitAcrossReads(t *testing.T) {
	// Spec §7 names this case. A decoder that assumed one Read returns one
	// whole frame works against a bytes.Buffer and fails against a socket.
	var buf bytes.Buffer
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0, "delta": map[string]any{"text": "split"}})
	frame(t, &buf, "messageStop", map[string]any{"stopReason": "end_turn"})

	events, err := collect(t, &oneByteReader{r: bytes.NewReader(buf.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, ev := range events {
		if ev.Type == ir.EventContentDelta {
			text += ev.Delta.Text
		}
	}
	if text != "split" {
		t.Errorf("text = %q; a frame split across reads was not reassembled", text)
	}
}

// oneByteReader returns one byte per Read, which is the worst case a socket can
// present and the one a length-prefixed decoder must survive.
type oneByteReader struct{ r io.Reader }

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func TestStreamSurfacesAMidStreamException(t *testing.T) {
	// Spec §7 names this too. An exception frame after content has flowed is
	// the case where the status line said 200 and the failure arrived later.
	var buf bytes.Buffer
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0, "delta": map[string]any{"text": "partial"}})
	exceptionFrame(t, &buf, "modelStreamErrorException", "the model stream failed")

	events, err := collect(t, &buf)
	if err == nil {
		t.Fatal("a mid-stream exception must surface as an error")
	}
	if len(events) == 0 {
		t.Error("the content before the exception must still be delivered")
	}
}

func TestStreamDecodesAToolCall(t *testing.T) {
	var buf bytes.Buffer
	frame(t, &buf, "contentBlockStart", map[string]any{
		"contentBlockIndex": 1,
		"start": map[string]any{"toolUse": map[string]any{
			"toolUseId": "tu_1", "name": "get_weather"}}})
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 1,
		"delta":             map[string]any{"toolUse": map[string]any{"input": `{"city":`}}})
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 1,
		"delta":             map[string]any{"toolUse": map[string]any{"input": `"Oslo"}`}}})
	frame(t, &buf, "contentBlockStop", map[string]any{"contentBlockIndex": 1})

	events, err := collect(t, &buf)
	if err != nil {
		t.Fatal(err)
	}
	var id, name, input string
	for _, ev := range events {
		if ev.Delta == nil {
			continue
		}
		id += ev.Delta.ToolID
		name += ev.Delta.ToolName
		input += ev.Delta.ToolInput
	}
	if id != "tu_1" || name != "get_weather" {
		t.Errorf("tool id/name = %q/%q", id, name)
	}
	if input != `{"city":"Oslo"}` {
		t.Errorf("tool input = %q", input)
	}
}

func TestStreamDecodesReasoningDeltas(t *testing.T) {
	var buf bytes.Buffer
	frame(t, &buf, "contentBlockDelta", map[string]any{
		"contentBlockIndex": 0,
		"delta": map[string]any{"reasoningContent": map[string]any{"text": "hmm"}}})
	events, err := collect(t, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Delta.Thinking != "hmm" {
		t.Errorf("events = %+v", events)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/bedrock/ -run Stream 2>&1 | head -10
```

Expected: the stub's "stream parsing arrives in task 6" error on every case.

- [ ] **Step 3: Implement**

Create `internal/adapter/bedrock/stream.go`:

```go
package bedrock

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wireStreamDelta struct {
	Text    string `json:"text"`
	ToolUse *struct {
		Input string `json:"input"`
	} `json:"toolUse"`
	ReasoningContent *struct {
		Text      string `json:"text"`
		Signature string `json:"signature"`
	} `json:"reasoningContent"`
}

type wireStreamEvent struct {
	Role              string `json:"role"`
	ContentBlockIndex int    `json:"contentBlockIndex"`
	Start             *struct {
		ToolUse *struct {
			ToolUseID string `json:"toolUseId"`
			Name      string `json:"name"`
		} `json:"toolUse"`
	} `json:"start"`
	Delta      *wireStreamDelta `json:"delta"`
	StopReason string           `json:"stopReason"`
	Usage      *wireUsage       `json:"usage"`
}

// ParseStream decodes AWS binary eventstream framing into IR events.
//
// maxLine is ignored: it bounds an SSE line, and an eventstream frame carries
// its own length prefix which the decoder validates against a CRC. The
// parameter stays because adapter.Adapter requires it.
func ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	_ = maxLine
	return func(yield func(ir.StreamEvent, error) bool) {
		dec := eventstream.NewDecoder()
		// Reused across frames. The decoder grows it as needed and returns a
		// Message whose Payload aliases it, so anything kept beyond one
		// iteration must be copied — which is why the JSON is unmarshalled
		// immediately rather than stashed.
		payload := make([]byte, 0, 8<<10)

		for {
			msg, err := dec.Decode(r, payload)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				yield(ir.StreamEvent{}, fmt.Errorf("decode eventstream frame: %w", err))
				return
			}

			switch messageType(msg.Headers) {
			case "exception":
				// The status line was 200 and the failure arrived later. The
				// executor reclassifies from this error, so naming the
				// exception type matters.
				yield(ir.StreamEvent{}, fmt.Errorf("bedrock stream exception %s: %s",
					headerValue(msg.Headers, ":exception-type"), msg.Payload))
				return
			case "error":
				yield(ir.StreamEvent{}, fmt.Errorf("bedrock stream error %s: %s",
					headerValue(msg.Headers, ":error-code"), msg.Payload))
				return
			}

			evs, err := decodeEvent(headerValue(msg.Headers, ":event-type"), msg.Payload)
			if err != nil {
				yield(ir.StreamEvent{}, err)
				return
			}
			for _, ev := range evs {
				if !yield(ev, nil) {
					return
				}
			}
		}
	}
}

func messageType(h eventstream.Headers) string { return headerValue(h, ":message-type") }

func headerValue(h eventstream.Headers, name string) string {
	v := h.Get(name)
	if v == nil {
		return ""
	}
	s, ok := v.(eventstream.StringValue)
	if !ok {
		return ""
	}
	return string(s)
}

// decodeEvent maps one Converse stream event to zero or more IR events. Zero is
// a real answer: contentBlockStart for a text block carries nothing the IR does
// not already have.
func decodeEvent(eventType string, payload []byte) ([]ir.StreamEvent, error) {
	var w wireStreamEvent
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &w); err != nil {
			return nil, fmt.Errorf("parse %s payload: %w", eventType, err)
		}
	}

	switch eventType {
	case "messageStart":
		return []ir.StreamEvent{{Type: ir.EventMessageStart}}, nil

	case "contentBlockStart":
		if w.Start != nil && w.Start.ToolUse != nil {
			return []ir.StreamEvent{{
				Type:  ir.EventBlockStart,
				Index: w.ContentBlockIndex,
				Delta: &ir.Delta{
					Type:     ir.BlockToolUse,
					ToolID:   w.Start.ToolUse.ToolUseID,
					ToolName: w.Start.ToolUse.Name,
				},
			}}, nil
		}
		return []ir.StreamEvent{{Type: ir.EventBlockStart, Index: w.ContentBlockIndex}}, nil

	case "contentBlockDelta":
		if w.Delta == nil {
			return nil, nil
		}
		switch {
		case w.Delta.ToolUse != nil:
			return []ir.StreamEvent{{
				Type: ir.EventContentDelta, Index: w.ContentBlockIndex,
				Delta: &ir.Delta{Type: ir.BlockToolUse, ToolInput: w.Delta.ToolUse.Input},
			}}, nil
		case w.Delta.ReasoningContent != nil:
			return []ir.StreamEvent{{
				Type: ir.EventContentDelta, Index: w.ContentBlockIndex,
				Delta: &ir.Delta{
					Type:      ir.BlockThinking,
					Thinking:  w.Delta.ReasoningContent.Text,
					Signature: w.Delta.ReasoningContent.Signature,
				},
			}}, nil
		case w.Delta.Text != "":
			return []ir.StreamEvent{{
				Type: ir.EventContentDelta, Index: w.ContentBlockIndex,
				Delta: &ir.Delta{Type: ir.BlockText, Text: w.Delta.Text},
			}}, nil
		}
		return nil, nil

	case "contentBlockStop":
		return []ir.StreamEvent{{Type: ir.EventBlockStop, Index: w.ContentBlockIndex}}, nil

	case "messageStop":
		return []ir.StreamEvent{{
			Type: ir.EventMessageStop, StopReason: stopReason(w.StopReason),
		}}, nil

	case "metadata":
		// Usage arrives after messageStop, exactly as it does for Groq's
		// OpenAI-compatible stream. Completing on messageStop would report
		// zero tokens for every streamed Bedrock request.
		if w.Usage == nil {
			return nil, nil
		}
		u := w.Usage.toIR()
		return []ir.StreamEvent{{Type: ir.EventMessageDelta, Usage: &u}}, nil
	}
	return nil, nil
}
```

Delete the `ParseStream` stub from `build.go`.

- [ ] **Step 4: Register the adapter**

In `internal/server/server.go`:

```go
	ex := exec.New(cfgStore, src, map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		"gemini":       geminiadapter.New(),
		"bedrock":      bedrockadapter.New(),
	}, exec.Deps{
		Log: logw, Health: breaker, Fleet: breaker, Catalog: cat,
		Auth: authManager,
	})
```

`authManager` is built just above it, next to `breaker`:

```go
	// Static styles need nothing here; the manager serves them by returning a
	// nil authorizer. Its collaborators arrive as the OAuth tasks land.
	authManager := auth.NewManager(auth.Deps{})
```

- [ ] **Step 5: Run the tests, verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/bedrock/ -count=1 -v 2>&1 | tail -20
go test ./... -count=1 -race && go vet ./... && gofmt -l .
git add internal/adapter/bedrock internal/server
git commit -m "feat(bedrock): decode eventstream framing"
```

---

### Task 7: Bedrock discovery catalogues inference profiles

**Files:**
- Create: `internal/adapter/bedrock/discover.go`
- Modify: `internal/catalog/discovery.go`, `internal/server/server.go`
- Test: `internal/adapter/bedrock/discover_test.go`, `internal/catalog/discovery_lister_test.go`

**Interfaces:**
- Consumes: `auth.Authorizer` from Task 3.
- Produces: `bedrock.NewLister(client *http.Client) *Lister`, `(*Lister).List(ctx, catalog.Probe) ([]catalog.Discovered, error)`; `catalog.KindLister`, `catalog.DiscoveryOptions.Listers map[string]KindLister`; `catalog.Probe.Region`, `catalog.Probe.Authorize`.

Spec §3.3, and this is the whole reason the task exists:

> `ListFoundationModels` — on the `bedrock` control-plane endpoint, not
> `bedrock-runtime` — returns bare model IDs, many of which are not on-demand
> invocable. The invocable profile IDs come from `ListInferenceProfiles`.
> Cataloguing only what the first call returns would store precisely the
> identifiers that fail.

**Two calls, and the profile ids win.** A bare model id that a profile covers is
not catalogued separately: routing to it would produce a 400 telling the
operator to use an inference profile, which is a worse error than the model
simply not being offered. A bare id that no profile covers and that reports
on-demand throughput is catalogued as itself.

**Discovery reaches the control plane, not the runtime host.** `bedrock` and
`bedrock-runtime` are different endpoints with the same signing service name. A
`ListFoundationModels` sent to the runtime host is a 404 that reads like a
missing model.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/bedrock/discover_test.go`:

```go
package bedrock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// awsFake serves both control-plane listings from one handler, so the test can
// assert which paths were actually called.
func awsFake(t *testing.T, models, profiles string) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/foundation-models":
			_, _ = w.Write([]byte(models))
		case "/inference-profiles":
			_, _ = w.Write([]byte(profiles))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

const foundationModels = `{"modelSummaries":[
  {"modelId":"anthropic.claude-3-5-sonnet-20241022-v2:0","modelName":"Claude 3.5 Sonnet",
   "inferenceTypesSupported":["INFERENCE_PROFILE"],"modelLifecycle":{"status":"ACTIVE"}},
  {"modelId":"amazon.titan-text-express-v1","modelName":"Titan Text Express",
   "inferenceTypesSupported":["ON_DEMAND"],"modelLifecycle":{"status":"ACTIVE"}},
  {"modelId":"amazon.retired-v1","modelName":"Retired",
   "inferenceTypesSupported":["ON_DEMAND"],"modelLifecycle":{"status":"LEGACY"}}
]}`

const inferenceProfiles = `{"inferenceProfileSummaries":[
  {"inferenceProfileId":"us.anthropic.claude-3-5-sonnet-20241022-v2:0",
   "inferenceProfileName":"US Claude 3.5 Sonnet","status":"ACTIVE","type":"SYSTEM_DEFINED",
   "models":[{"modelArn":"arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-3-5-sonnet-20241022-v2:0"}]}
]}`

func listerProbe(base string) catalog.Probe {
	return catalog.Probe{
		ProviderID: "bed", Kind: "bedrock", BaseURL: base, Region: "us-east-1",
		Authorize: func(context.Context, *http.Request) error { return nil },
	}
}

func TestListerCallsBothEndpoints(t *testing.T) {
	srv, paths := awsFake(t, foundationModels, inferenceProfiles)
	if _, err := NewLister(srv.Client()).List(context.Background(), listerProbe(srv.URL)); err != nil {
		t.Fatal(err)
	}
	if len(*paths) != 2 {
		t.Fatalf("called %v, want both listings", *paths)
	}
}

func TestProfileIdsAreTheRoutableIdentifiers(t *testing.T) {
	srv, _ := awsFake(t, foundationModels, inferenceProfiles)
	got, err := NewLister(srv.Client()).List(context.Background(), listerProbe(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, d := range got {
		ids[d.ModelID] = true
	}
	// The profile id, because that is what an invocation must name.
	if !ids["us.anthropic.claude-3-5-sonnet-20241022-v2:0"] {
		t.Errorf("the inference profile is missing: %v", ids)
	}
	// The bare id it covers must NOT be catalogued: routing to it returns a
	// 400 telling the operator to use a profile, which is a worse error than
	// not offering the model.
	if ids["anthropic.claude-3-5-sonnet-20241022-v2:0"] {
		t.Errorf("the bare id behind a profile was catalogued: %v", ids)
	}
	// An on-demand model with no profile is catalogued as itself.
	if !ids["amazon.titan-text-express-v1"] {
		t.Errorf("an on-demand model is missing: %v", ids)
	}
}

func TestARetiredModelIsNotCatalogued(t *testing.T) {
	srv, _ := awsFake(t, foundationModels, inferenceProfiles)
	got, _ := NewLister(srv.Client()).List(context.Background(), listerProbe(srv.URL))
	for _, d := range got {
		if d.ModelID == "amazon.retired-v1" {
			t.Error("a LEGACY model was catalogued")
		}
	}
}

func TestListerSignsBothCalls(t *testing.T) {
	// The control plane refuses an unsigned request. A lister that signed one
	// call and not the other would half-work and be diagnosed as a permissions
	// problem.
	srv, _ := awsFake(t, foundationModels, inferenceProfiles)
	signed := 0
	p := listerProbe(srv.URL)
	p.Authorize = func(_ context.Context, r *http.Request) error {
		signed++
		r.Header.Set("Authorization", "signed")
		return nil
	}
	if _, err := NewLister(srv.Client()).List(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if signed != 2 {
		t.Errorf("signed %d requests, want 2", signed)
	}
}

func TestListerRefusesWithoutAnAuthorizer(t *testing.T) {
	srv, _ := awsFake(t, foundationModels, inferenceProfiles)
	p := listerProbe(srv.URL)
	p.Authorize = nil
	if _, err := NewLister(srv.Client()).List(context.Background(), p); err == nil {
		t.Fatal("an unsigned control-plane call must be refused rather than sent")
	}
}
```

Create `internal/catalog/discovery_lister_test.go`:

```go
package catalog

import (
	"context"
	"testing"

	"github.com/darkraise/darkrouter/internal/provider"
)

type fakeLister struct {
	called bool
	models []Discovered
}

func (f *fakeLister) List(context.Context, Probe) ([]Discovered, error) {
	f.called = true
	return f.models, nil
}

func TestAKindListerReplacesTheGenericListing(t *testing.T) {
	// bedrock has no /models endpoint; ProbeFor refuses the kind, and without
	// a registered lister discovery silently does nothing for it. That silence
	// is correct for vertex and wrong for bedrock.
	f := &fakeLister{models: []Discovered{{ModelID: "us.anthropic.claude-x"}}}
	p := provider.Provider{
		ID: "bed", Kind: "bedrock", Region: "us-east-1", AuthStyle: "sigv4",
		Credentials: []provider.Credential{{ID: "k", Secret: "{}", Enabled: true}},
	}
	pr, err := ProbeForKind(p, Preset{}, "{}", map[string]KindLister{"bedrock": f})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Lister == nil {
		t.Fatal("the registered lister did not reach the probe")
	}
	if pr.Region != "us-east-1" {
		t.Errorf("region = %q; a signed listing needs it", pr.Region)
	}
}

func TestAnUnregisteredKindIsStillUndiscoverable(t *testing.T) {
	// vertex has no listing API at all, spec §4.3. It must stay a silent skip
	// rather than becoming an error the discovery worker logs every tick.
	p := provider.Provider{ID: "v", Kind: "vertex"}
	_, err := ProbeForKind(p, Preset{}, "", nil)
	if err == nil {
		t.Fatal("vertex must remain undiscoverable")
	}
	if !errors.Is(err, ErrKindNotDiscoverable) {
		t.Errorf("error = %v, want ErrKindNotDiscoverable", err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/bedrock/ ./internal/catalog/ -run 'Lister|Profile|Retired|Undiscoverable' 2>&1 | head -15
```

Expected: undefined `NewLister`, `ProbeForKind`, `Probe.Region`,
`Probe.Authorize`, `Probe.Lister`.

- [ ] **Step 3: Widen the probe**

In `internal/catalog/probe.go`, add to `Probe`:

```go
	// Region and Authorize are what a signed control-plane listing needs. Both
	// are empty for every kind whose listing is an unsigned GET with a bearer
	// token, which is all of them before this phase.
	Region    string
	Authorize func(context.Context, *http.Request) error

	// Lister replaces the generic /models call for a kind that has none.
	// Bedrock needs two calls against a different host; vertex has no listing
	// at all and registers nothing, so it stays undiscoverable.
	Lister KindLister
```

and add the interface plus the widened constructor:

```go
// KindLister discovers a kind whose model list does not come from one GET.
//
// An interface rather than a switch on kind because catalog must not import
// internal/adapter/bedrock: the concrete lister is registered by the server,
// which already imports both.
type KindLister interface {
	List(ctx context.Context, p Probe) ([]Discovered, error)
}

// ProbeForKind is ProbeFor with the lister registry consulted first.
//
// ProbeFor stays as it is and delegates here with a nil registry, so every
// existing caller and test keeps its exact behavior.
func ProbeForKind(p provider.Provider, preset Preset, apiKey string,
	listers map[string]KindLister) (Probe, error) {

	if l, ok := listers[p.Kind]; ok {
		base := p.BaseURL
		if base == "" {
			base = preset.BaseURL
		}
		return Probe{
			ProviderID: p.ID, Kind: p.Kind, BaseURL: base,
			Region: p.Region, Lister: l,
		}, nil
	}
	return ProbeFor(p, preset, apiKey)
}
```

`context` and `net/http` join the import block.

In `internal/catalog/discovery.go`, add `Listers map[string]KindLister` to
`DiscoveryOptions`, carry it onto the `Discoverer`, call `ProbeForKind` in
`probe`, and give `list` its early branch:

```go
	pr, err := ProbeForKind(p, preset, cred.Secret, d.listers)
```

```go
func (d *Discoverer) list(ctx context.Context, pr Probe, providerID, keyID string) ([]Discovered, error) {
	if pr.Lister != nil {
		return pr.Lister.List(ctx, pr)
	}
	// ... the existing body, unchanged
}
```

The signed probe still needs an authorizer, which the discoverer builds from the
same manager the executor uses. Add `Auth AuthResolver` to `DiscoveryOptions`
with a local interface mirroring `exec.AuthResolver`, and populate
`pr.Authorize` in `probe` when the style is non-static. **A provider whose
authorizer cannot be built records a discovery failure rather than being
skipped** — unlike an undiscoverable kind, this one is a misconfiguration the
operator can fix, and silence would hide it.

- [ ] **Step 4: Implement the lister**

Create `internal/adapter/bedrock/discover.go`:

```go
package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// ControlPlaneFor is the management endpoint, which is a different host from
// the runtime one. A ListFoundationModels sent to bedrock-runtime is a 404 that
// reads like a missing model.
func ControlPlaneFor(region string) string {
	return "https://bedrock." + region + ".amazonaws.com"
}

type Lister struct{ client *http.Client }

func NewLister(c *http.Client) *Lister {
	if c == nil {
		c = http.DefaultClient
	}
	return &Lister{client: c}
}

type modelSummary struct {
	ModelID                 string   `json:"modelId"`
	ModelName               string   `json:"modelName"`
	InferenceTypesSupported []string `json:"inferenceTypesSupported"`
	ModelLifecycle          struct {
		Status string `json:"status"`
	} `json:"modelLifecycle"`
	OutputModalities []string `json:"outputModalities"`
}

type profileSummary struct {
	InferenceProfileID   string `json:"inferenceProfileId"`
	InferenceProfileName string `json:"inferenceProfileName"`
	Status               string `json:"status"`
	Models               []struct {
		ModelArn string `json:"modelArn"`
	} `json:"models"`
}

// List catalogues what can actually be invoked.
//
// Spec §3.3: ListFoundationModels returns bare model ids, many of which are not
// on-demand invocable, and the invocable identifiers come from
// ListInferenceProfiles. Cataloguing only the first call's output would store
// precisely the identifiers that fail.
func (l *Lister) List(ctx context.Context, p catalog.Probe) ([]catalog.Discovered, error) {
	if p.Authorize == nil {
		return nil, errors.New("bedrock discovery needs a signed request; no authorizer was supplied")
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" || strings.Contains(base, "bedrock-runtime") {
		if p.Region == "" {
			return nil, errors.New("bedrock discovery needs a region")
		}
		base = ControlPlaneFor(p.Region)
	}

	var models struct {
		ModelSummaries []modelSummary `json:"modelSummaries"`
	}
	if err := l.get(ctx, p, base+"/foundation-models", &models); err != nil {
		return nil, err
	}
	var profiles struct {
		Summaries []profileSummary `json:"inferenceProfileSummaries"`
	}
	if err := l.get(ctx, p, base+"/inference-profiles", &profiles); err != nil {
		return nil, err
	}

	// covered is every bare model id reachable through a profile. Those are
	// catalogued under the profile id instead of their own.
	covered := map[string]bool{}
	out := make([]catalog.Discovered, 0, len(profiles.Summaries)+len(models.ModelSummaries))
	for _, pr := range profiles.Summaries {
		if pr.Status != "" && pr.Status != "ACTIVE" {
			continue
		}
		for _, m := range pr.Models {
			covered[modelIDFromARN(m.ModelArn)] = true
		}
		out = append(out, catalog.Discovered{ModelID: pr.InferenceProfileID})
	}

	for _, m := range models.ModelSummaries {
		if covered[m.ModelID] {
			continue
		}
		if m.ModelLifecycle.Status != "" && m.ModelLifecycle.Status != "ACTIVE" {
			continue
		}
		if !supports(m.InferenceTypesSupported, "ON_DEMAND") {
			// PROVISIONED-only and INFERENCE_PROFILE-only models cannot be
			// invoked by id. Cataloguing them stores identifiers that 400.
			continue
		}
		out = append(out, catalog.Discovered{ModelID: m.ModelID})
	}
	return out, nil
}

func (l *Lister) get(ctx context.Context, p catalog.Probe, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if err := p.Authorize(ctx, req); err != nil {
		return fmt.Errorf("sign %s: %w", url, err)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

// modelIDFromARN takes the identifier off the end of a foundation-model ARN.
// The ARN is what a profile names its members by; the catalog keys on ids.
func modelIDFromARN(arn string) string {
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

func supports(types []string, want string) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}
```

Register it in `internal/server/server.go`, in the `NewDiscoverer` options:

```go
		Listers: map[string]catalog.KindLister{"bedrock": bedrockadapter.NewLister(nil)},
		Auth:    authManager,
```

- [ ] **Step 5: Run the tests, verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/bedrock/ ./internal/catalog/ -count=1 2>&1 | tail -15
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/bedrock internal/catalog internal/server
git commit -m "feat(bedrock): catalogue invocable inference profiles"
```

---

### Task 8: The service-account token source

**Files:**
- Create: `internal/auth/gcp.go`
- Modify: `internal/auth/auth.go`, `go.mod`
- Test: `internal/auth/gcp_test.go`

**Interfaces:**
- Produces: `(*Manager).gcpSA`; `auth.gcpSource` with `Token(ctx) (string, error)`; `auth.DefaultRefreshDelta`.

Spec §4.2: "A service-account JSON key exchanged for a short-lived access token
via `golang.org/x/oauth2/google`, which refreshes lazily when a token is inside
its expiry delta. That satisfies the requirement that no request fails on an
expiry race; **Darkrouter wraps the `TokenSource` so the behavior is testable
rather than assumed**."

That last clause is the task. `oauth2.ReuseTokenSource` already caches and
already has an expiry delta, but it is ten seconds and unexported, and a test
that asserted on it would be asserting on somebody else's constant. The wrapper
holds our delta, and the tests below are what "testable rather than assumed"
means in practice.

**`token_uri` comes out of the service-account JSON.** `JWTConfigFromJSON` reads
it, so a fake token endpoint needs no special support in the code under test —
the test writes a service-account document pointing at an `httptest` server and
everything else is the real path.

- [ ] **Step 1: Add the dependency**

```bash
export PATH=$PATH:/usr/local/go/bin
go get golang.org/x/oauth2 golang.org/x/oauth2/google
go mod tidy
```

- [ ] **Step 2: Write the failing test**

Create `internal/auth/gcp_test.go`:

```go
package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// testKey is generated once per test binary. A 2048-bit RSA keygen is about a
// tenth of a second, and this package would otherwise pay it in every case.
var testKey = sync.OnceValue(func() []byte {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
})

// serviceAccount writes a document pointing at tokenURL. token_uri is read by
// JWTConfigFromJSON, so the fake endpoint needs no hook in the code under test.
func serviceAccount(t *testing.T, tokenURL string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "proj",
		"private_key":  string(testKey()),
		"client_email": "sa@proj.iam.gserviceaccount.com",
		"token_uri":    tokenURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

type tokenFake struct {
	mu        sync.Mutex
	calls     int
	expiresIn int
	status    int
}

func (f *tokenFake) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls++
		n, exp, status := f.calls, f.expiresIn, f.status
		f.mu.Unlock()

		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"server_error"}`))
			return
		}
		if exp == 0 {
			exp = 3600
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"at-%d","token_type":"Bearer","expires_in":%d}`, n, exp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *tokenFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func gcpAuthorizer(t *testing.T, secret string) Authorizer {
	t.Helper()
	m := NewManager(Deps{})
	az, err := m.For(context.Background(),
		Target{ProviderID: "v", Style: StyleGCPSA, Project: "proj", Location: "us-central1"},
		Credential{ID: "k", Kind: "gcp_sa", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	return az
}

func request(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequest("POST", "https://aiplatform.googleapis.invalid/x", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestGCPExchangesTheKeyForABearerToken(t *testing.T) {
	f := &tokenFake{}
	az := gcpAuthorizer(t, serviceAccount(t, f.serve(t).URL))

	r := request(t)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer at-1" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestGCPReusesATokenInsideItsLifetime(t *testing.T) {
	// One exchange per hour, not one per request. Without this every Vertex
	// call pays a JWT signature and a round trip to Google.
	f := &tokenFake{}
	az := gcpAuthorizer(t, serviceAccount(t, f.serve(t).URL))

	for i := 0; i < 5; i++ {
		if err := az(context.Background(), request(t)); err != nil {
			t.Fatal(err)
		}
	}
	if f.count() != 1 {
		t.Errorf("exchanged %d times, want 1", f.count())
	}
}

func TestGCPRefreshesInsideTheDelta(t *testing.T) {
	// The token is still valid by the clock but a request starting now could
	// arrive after it expires. Spec §4.2's "no request fails on an expiry race".
	f := &tokenFake{expiresIn: 30}
	az := gcpAuthorizer(t, serviceAccount(t, f.serve(t).URL))

	r := request(t)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := az(context.Background(), request(t)); err != nil {
		t.Fatal(err)
	}
	if f.count() != 2 {
		t.Errorf("exchanged %d times, want 2: a token 30s from expiry is inside the %v delta",
			f.count(), DefaultRefreshDelta)
	}
}

func TestGCPExchangeFailureIsACredentialFailure(t *testing.T) {
	// The upstream is fine; this key is not. Reporting it as a provider
	// failure would cool an endpoint serving everyone else.
	f := &tokenFake{status: http.StatusUnauthorized}
	az := gcpAuthorizer(t, serviceAccount(t, f.serve(t).URL))

	err := az(context.Background(), request(t))
	if err == nil {
		t.Fatal("a refused token exchange must be an error")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("the error should name the exchange, got %v", err)
	}
	// And it must not leak the key.
	if strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Fatal("the error carries the private key")
	}
}

func TestGCPRefusesAMalformedDocument(t *testing.T) {
	m := NewManager(Deps{})
	_, err := m.For(context.Background(),
		Target{ProviderID: "v", Style: StyleGCPSA, Project: "proj"},
		Credential{ID: "k", Kind: "gcp_sa", Secret: "not json"})
	if err == nil {
		t.Fatal("a malformed service-account document must be refused at resolution")
	}
}

func TestGCPNeedsAProject(t *testing.T) {
	// Project and location construct the endpoint, spec §4.2. Without a
	// project there is no URL to call, and a token would be useless.
	f := &tokenFake{}
	m := NewManager(Deps{})
	_, err := m.For(context.Background(),
		Target{ProviderID: "v", Style: StyleGCPSA},
		Credential{ID: "k", Kind: "gcp_sa", Secret: serviceAccount(t, f.serve(t).URL)})
	if err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("a vertex provider with no project must be refused, got %v", err)
	}
}

func TestGCPSourcesAreCachedPerCredential(t *testing.T) {
	// Two resolutions of the same credential must share a token source, or the
	// cache above is defeated by the executor resolving once per attempt.
	f := &tokenFake{}
	secret := serviceAccount(t, f.serve(t).URL)
	m := NewManager(Deps{})
	tgt := Target{ProviderID: "v", Style: StyleGCPSA, Project: "proj"}
	cred := Credential{ID: "k", Kind: "gcp_sa", Secret: secret}

	for i := 0; i < 3; i++ {
		az, err := m.For(context.Background(), tgt, cred)
		if err != nil {
			t.Fatal(err)
		}
		if err := az(context.Background(), request(t)); err != nil {
			t.Fatal(err)
		}
	}
	if f.count() != 1 {
		t.Errorf("exchanged %d times across three resolutions, want 1", f.count())
	}
}

var _ = time.Second
```

- [ ] **Step 3: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/auth/ -run GCP 2>&1 | head -10
```

Expected: the placeholder's "unsupported auth style: gcp-sa".

- [ ] **Step 4: Implement**

Create `internal/auth/gcp.go`:

```go
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// DefaultRefreshDelta is how far ahead of expiry a token is renewed.
//
// oauth2.ReuseTokenSource has its own delta, but it is ten seconds and
// unexported. Spec §4.2 requires that no request fails on an expiry race, and
// a minute covers a slow upstream on a request that started just inside the
// window. Holding our own constant is also what makes the behavior testable
// rather than an assertion about somebody else's package.
const DefaultRefreshDelta = time.Minute

// cloudPlatformScope is the only scope Vertex inference needs. Narrower scopes
// exist but are not honored uniformly across the aiplatform surface.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// gcpSource wraps an oauth2.TokenSource with our expiry delta and a mutex, so
// concurrent requests finding an expiring token perform one exchange rather
// than one each.
type gcpSource struct {
	mu    sync.Mutex
	src   oauth2.TokenSource
	tok   *oauth2.Token
	delta time.Duration
	now   func() time.Time
}

func (g *gcpSource) Token(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.tok != nil && g.tok.AccessToken != "" {
		if g.tok.Expiry.IsZero() || g.now().Add(g.delta).Before(g.tok.Expiry) {
			return g.tok.AccessToken, nil
		}
	}
	tok, err := oauth2.ReuseTokenSource(nil, g.src).Token()
	if err != nil {
		// Deliberately not wrapping the source error's full text into
		// something that could carry the key: oauth2 reports the endpoint's
		// response body, which is the useful half.
		return "", fmt.Errorf("service-account token exchange failed: %w", err)
	}
	g.tok = tok
	return tok.AccessToken, nil
}

// gcpSA resolves a service-account credential.
//
// The source is cached per credential because the executor resolves once per
// attempt: rebuilding it each time would sign a fresh JWT and make a fresh
// round trip to Google for every single request.
func (m *Manager) gcpSA(ctx context.Context, t Target, c Credential) (Authorizer, error) {
	if t.Project == "" {
		return nil, fmt.Errorf("provider %q uses gcp-sa but declares no project", t.ProviderID)
	}
	cfg, err := google.JWTConfigFromJSON([]byte(c.Secret), cloudPlatformScope)
	if err != nil {
		// The document is malformed. The error from JWTConfigFromJSON names
		// the field, never the key material.
		return nil, fmt.Errorf("provider %q: service-account key: %w", t.ProviderID, err)
	}

	key := c.ID + ":" + fingerprint(c.Secret)
	m.mu.Lock()
	src, ok := m.gcp[key]
	if !ok {
		src = &gcpSource{
			src:   cfg.TokenSource(context.WithoutCancel(ctx)),
			delta: DefaultRefreshDelta,
			now:   time.Now,
		}
		m.gcp[key] = src
	}
	m.mu.Unlock()

	return func(ctx context.Context, r *http.Request) error {
		tok, err := src.Token(ctx)
		if err != nil {
			return err
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		return nil
	}, nil
}

// fingerprint keys the cache on the credential's content as well as its id, so
// replacing a key in place invalidates the cached source rather than serving
// tokens minted from the old one until restart.
func fingerprint(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:8])
}
```

Delete the `gcpSA` and `gcpSource` placeholders from `auth.go`.

**`context.WithoutCancel` on the token source matters.** The context handed to
`For` is the request's, and a source built from it would stop working the moment
that request finished — every subsequent refresh would fail with "context
canceled" on a credential that is perfectly fine.

- [ ] **Step 5: Run the tests, verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/auth/ -count=1 -race 2>&1 | tail -15
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/auth go.mod go.sum
git commit -m "feat(auth): exchange service-account keys for tokens"
```

---

### Task 9: Vertex dispatches per publisher

**Files:**
- Create: `internal/adapter/vertex/adapter.go`, `internal/adapter/vertex/google.go`, `internal/adapter/vertex/anthropic.go`
- Modify: `internal/server/server.go`
- Test: `internal/adapter/vertex/vertex_test.go`

**Interfaces:**
- Consumes: `adapter.Target.Project/Location/Publisher` from Task 1; `gemini.NewWithFetcher`, `anthropic.BuildRequest`, `anthropic.ParseResponse`, `gemini.ParseStream`, `anthropic.ParseStream`.
- Produces: `vertex.New() *Adapter`, `vertex.EndpointFor(project, location string) string`, `vertex.PublisherGoogle`, `vertex.PublisherAnthropic`, `vertex.AnthropicVersion`.

Spec §4.1, quoted because an implementer who skims will get this wrong:

> An earlier draft claimed the Gemini payload covers both and "only transport
> and auth differ." That is false for exactly the models that justify supporting
> two URL forms, and an implementer following it would 400 on every Claude call.

| Publisher | Route | Payload |
|---|---|---|
| `publishers/google` | `:generateContent` / `:streamGenerateContent` | Gemini — phase 4's translation |
| `publishers/anthropic` | `:rawPredict` / `:streamRawPredict` | Anthropic Messages, model moved from the body into the URL, `anthropic_version: "vertex-2023-10-16"` injected |

**Neither renderer is reimplemented.** The Google half hands phase 4's Gemini
builder a target whose base URL already ends in `/publishers/google`, because
that builder appends `/models/{model}:generateContent` — the paths line up
exactly. The Anthropic half calls phase 4's builder, then rewrites the body and
the URL. Two translations of the Anthropic Messages shape would drift, and phase
4's is the one the golden files cover.

**Vertex is never passthrough-eligible.** Master design §4.1: its URL encodes
both model and publisher.

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/vertex/vertex_test.go`:

```go
package vertex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func req() *ir.Request {
	max := 128
	return &ir.Request{
		Model:     "claude-sonnet-4-5",
		MaxTokens: &max,
		Messages:  []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
	}
}

func build(t *testing.T, tgt *adapter.Target, r *ir.Request) (*http.Request, map[string]any) {
	t.Helper()
	hr, _, err := New().BuildRequest(context.Background(), tgt, r)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, raw)
	}
	// Put it back: the executor reads this body after the builder returns.
	hr.Body = io.NopCloser(bytesReader(raw))
	return hr, body
}

func anthropicTarget() *adapter.Target {
	return &adapter.Target{
		Project: "proj", Location: "us-central1",
		Publisher: PublisherAnthropic, Model: "claude-sonnet-4-5",
	}
}

func googleTarget() *adapter.Target {
	return &adapter.Target{
		Project: "proj", Location: "us-central1",
		Publisher: PublisherGoogle, Model: "gemini-2.5-pro",
	}
}

// The single most important pair of assertions in this package. Spec §4.1: an
// implementer following the earlier draft would 400 on every Claude call.
func TestAnthropicPublisherUsesRawPredict(t *testing.T) {
	hr, body := build(t, anthropicTarget(), req())

	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/proj/locations/" +
		"us-central1/publishers/anthropic/models/claude-sonnet-4-5:rawPredict"
	if hr.URL.String() != want {
		t.Errorf("url = %s\nwant %s", hr.URL, want)
	}
	if body["anthropic_version"] != AnthropicVersion {
		t.Errorf("anthropic_version = %v, want %q", body["anthropic_version"], AnthropicVersion)
	}
	// The model moves into the URL. Vertex rejects a body that also names it.
	if _, present := body["model"]; present {
		t.Errorf("the model is still in the body: %v", body["model"])
	}
	// And it is the Anthropic shape, not the Gemini one.
	if _, ok := body["messages"]; !ok {
		t.Errorf("body is not Anthropic Messages: %v", keys(body))
	}
	if _, ok := body["contents"]; ok {
		t.Errorf("body is the Gemini shape: %v", keys(body))
	}
}

func TestGooglePublisherUsesGenerateContent(t *testing.T) {
	hr, body := build(t, googleTarget(), req())

	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/proj/locations/" +
		"us-central1/publishers/google/models/gemini-2.5-pro:generateContent"
	if hr.URL.String() != want {
		t.Errorf("url = %s\nwant %s", hr.URL, want)
	}
	if _, ok := body["contents"]; !ok {
		t.Errorf("body is not the Gemini shape: %v", keys(body))
	}
	if _, ok := body["anthropic_version"]; ok {
		t.Error("anthropic_version leaked into a Gemini body")
	}
}

func TestStreamingRoutesDifferPerPublisher(t *testing.T) {
	r := req()
	r.Stream = true

	hr, _ := build(t, anthropicTarget(), r)
	if got := hr.URL.Path; got[len(got)-len(":streamRawPredict"):] != ":streamRawPredict" {
		t.Errorf("anthropic streaming path = %s", got)
	}
	hr, _ = build(t, googleTarget(), r)
	if got := hr.URL.Path; got[len(got)-len(":streamGenerateContent"):] != ":streamGenerateContent" {
		t.Errorf("google streaming path = %s", got)
	}
	// Vertex speaks SSE for the Gemini route only when asked.
	if hr.URL.RawQuery != "alt=sse" {
		t.Errorf("google streaming query = %q, want alt=sse", hr.URL.RawQuery)
	}
}

func TestAnUnknownPublisherIsAnError(t *testing.T) {
	// Llama and Mistral MaaS use a third, OpenAI-compatible route and are out
	// of scope for v1, spec §4.1. Guessing one of the two implemented shapes
	// would 400 with a message about the wrong payload.
	tgt := googleTarget()
	tgt.Publisher = "publishers/meta"
	if _, _, err := New().BuildRequest(context.Background(), tgt, req()); err == nil {
		t.Fatal("an unsupported publisher must be refused")
	}
}

func TestAnEmptyPublisherDefaultsToGoogle(t *testing.T) {
	// A catalog row seeded before the publisher column was populated, or a
	// provider created by hand. Google is the safe default: the vertex preset
	// declares it and its base URL names an aiplatform host.
	tgt := googleTarget()
	tgt.Publisher = ""
	hr, body := build(t, tgt, req())
	if _, ok := body["contents"]; !ok {
		t.Errorf("body = %v", keys(body))
	}
	if got := hr.URL.Path; got[len(got)-len(":generateContent"):] != ":generateContent" {
		t.Errorf("path = %s", got)
	}
}

func TestNoCredentialHeaderIsWritten(t *testing.T) {
	// internal/auth attaches the bearer token. A key written here would be a
	// second credential on the same request.
	tgt := googleTarget()
	tgt.APIKey = "should-be-ignored"
	hr, _ := build(t, tgt, req())
	if hr.Header.Get("Authorization") != "" || hr.Header.Get("x-goog-api-key") != "" {
		t.Errorf("the builder wrote a credential header: %v", hr.Header)
	}
}

func TestProjectAndLocationAreRequired(t *testing.T) {
	tgt := googleTarget()
	tgt.Project = ""
	if _, _, err := New().BuildRequest(context.Background(), tgt, req()); err == nil {
		t.Fatal("a vertex target with no project must be refused")
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

`bytesReader` is `bytes.NewReader`; import `bytes` and use it directly rather
than adding a helper.

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/vertex/ 2>&1 | head -10
```

Expected: no such package.

- [ ] **Step 3: Implement the dispatch**

Create `internal/adapter/vertex/adapter.go`:

```go
// Package vertex renders the IR to Google Vertex AI.
//
// One adapter kind, two request builders, selected by the publisher recorded on
// the catalog entry — master design §6.2. An earlier draft claimed the Gemini
// payload covers both and that only transport and auth differ; that is false
// for exactly the models that justify supporting two URL forms, and following
// it 400s on every Claude call.
//
// Neither payload is reimplemented here. The Google half hands phase 4's Gemini
// builder a base URL that already ends in the publisher segment; the Anthropic
// half calls phase 4's Anthropic builder and rewrites what Vertex moves.
package vertex

import (
	"context"
	"fmt"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	"github.com/darkraise/darkrouter/internal/ir"
)

const (
	PublisherGoogle    = "publishers/google"
	PublisherAnthropic = "publishers/anthropic"

	// AnthropicVersion is mandatory in the body on the rawPredict route, and
	// is a different value from the anthropic-version header the direct API
	// takes.
	AnthropicVersion = "vertex-2023-10-16"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Kind() string { return "vertex" }

// Surfaces is llm alone for now. The vertex preset declares embedding as well,
// but the embedding route is a third URL shape and phase 8's scope is the two
// generative ones; claiming the surface would route embeddings to a 404.
func (a *Adapter) Surfaces() adapter.SurfaceSet {
	return adapter.SurfaceSet{ir.SurfaceLLM: true}
}

// EndpointFor builds the regional host and project path. Location appears
// twice: once in the hostname and once in the path, which is Vertex's shape and
// not a mistake.
func EndpointFor(project, location string) string {
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s",
		location, project, location)
}

func publisherOf(t *adapter.Target) string {
	if t.Publisher == "" {
		// A catalog row seeded before publisher was populated. Google is the
		// safe default: it is what the vertex preset declares.
		return PublisherGoogle
	}
	return t.Publisher
}

func (a *Adapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	if t.Project == "" || t.Location == "" {
		return nil, nil, fmt.Errorf("vertex target needs a project and a location")
	}
	switch publisherOf(t) {
	case PublisherGoogle:
		return buildGoogle(ctx, t, req)
	case PublisherAnthropic:
		return buildAnthropic(ctx, t, req)
	}
	// Llama and Mistral MaaS use endpoints/openapi/chat/completions and are
	// out of scope for v1. Guessing one of the two implemented shapes would
	// 400 with a message about the wrong payload, which is worse than saying so.
	return nil, nil, fmt.Errorf("vertex publisher %q is not supported", t.Publisher)
}

// ParseResponse and ParseStream dispatch on shape rather than on the publisher,
// because neither is handed a Target. That works because the two payloads are
// unambiguous: an Anthropic response has a content array and a type of
// "message"; a Gemini one has candidates.
func (a *Adapter) ParseResponse(resp *http.Response) (*ir.Response, error) {
	return parseResponse(resp)
}

func (a *Adapter) ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return parseStream(r, maxLine)
}

func (a *Adapter) Classify(resp *http.Response, err error) adapter.Outcome {
	return adapter.ClassifyStatus(resp, err)
}

var _ adapter.Adapter = (*Adapter)(nil)
var _ = anthropicadapter.DefaultVersion
```

- [ ] **Step 4: Implement the Google half**

Create `internal/adapter/vertex/google.go`:

```go
package vertex

import (
	"context"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/ir"
)

// gemini is shared: its only state is the media fetcher, which is safe for
// concurrent use and expensive enough not to rebuild per request.
var gemini = geminiadapter.New()

// buildGoogle reuses phase 4's Gemini builder unchanged.
//
// That builder appends "/models/{model}:generateContent" to the target's base
// URL, so handing it a base URL ending in the publisher segment produces
// exactly Vertex's path. The alternative — a second Gemini renderer — would
// drift from the one the golden files cover.
func buildGoogle(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	inner := *t
	inner.BaseURL = EndpointFor(t.Project, t.Location) + "/" + PublisherGoogle
	// The bearer token comes from internal/auth, so the Gemini builder must
	// not write x-goog-api-key.
	inner.APIKey = ""
	return gemini.BuildRequest(ctx, &inner, req)
}
```

- [ ] **Step 5: Implement the Anthropic half**

Create `internal/adapter/vertex/anthropic.go`:

```go
package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	"github.com/darkraise/darkrouter/internal/ir"
)

// buildAnthropic renders through phase 4's Anthropic builder and then applies
// the two differences Vertex imposes: the model moves from the body into the
// URL, and anthropic_version becomes a body field.
//
// Rewriting the rendered body rather than reimplementing the renderer is
// deliberate. Everything phase 4 encodes — cache breakpoints, thinking modes,
// the assistant-prefill rules — is behavior the golden files pin, and a second
// implementation would drift from it silently.
func buildAnthropic(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	inner := *t
	// A placeholder: the URL is replaced below. Empty would make the builder
	// produce a relative path that http.NewRequest rejects.
	inner.BaseURL = "https://vertex.invalid"
	inner.APIKey = ""

	hr, warns, err := anthropicadapter.BuildRequest(ctx, &inner, req)
	if err != nil {
		return nil, warns, err
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		return nil, warns, err
	}
	_ = hr.Body.Close()

	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, warns, fmt.Errorf("vertex: re-read anthropic body: %w", err)
	}
	// Vertex takes the model in the URL and rejects a body that also names it.
	delete(body, "model")
	body["anthropic_version"], err = json.Marshal(AnthropicVersion)
	if err != nil {
		return nil, warns, err
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, warns, err
	}

	method := ":rawPredict"
	if req.Stream {
		method = ":streamRawPredict"
	}
	endpoint := EndpointFor(t.Project, t.Location) + "/" + PublisherAnthropic +
		"/models/" + url.PathEscape(t.Model) + method

	out, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, warns, err
	}
	out.Header.Set("Content-Type", "application/json")
	// anthropic-version is a body field here, not a header. Sending both is
	// not an error, but sending only the header is: Vertex reads the body.
	return out, warns, nil
}

// parseResponse dispatches on the payload's own shape.
//
// Neither ParseResponse nor ParseStream is handed a Target, so the publisher is
// not available. That is fine because the two shapes are unambiguous: an
// Anthropic response carries a top-level content array and type "message", a
// Gemini one carries candidates.
func parseResponse(resp *http.Response) (*ir.Response, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	closeErr := resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	restore := func() *http.Response {
		clone := *resp
		clone.Body = io.NopCloser(bytes.NewReader(raw))
		return &clone
	}
	if isAnthropicShape(raw) {
		return anthropicadapter.ParseResponse(restore())
	}
	return gemini.ParseResponse(restore())
}

func isAnthropicShape(raw []byte) bool {
	var probe struct {
		Type       string          `json:"type"`
		Content    json.RawMessage `json:"content"`
		Candidates json.RawMessage `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	if len(probe.Candidates) > 0 {
		return false
	}
	return probe.Type == "message" || len(probe.Content) > 0
}
```

`parseStream` needs the same decision before the first byte is consumed, which a
stream cannot offer. Dispatch on the response's content type instead — the
executor hands `ParseStream` the body only, so carry the choice on the adapter
by returning a publisher-specific parser from `BuildRequest`. **That is not
possible through the `adapter.Adapter` interface**, so do the simplest correct
thing: peek one buffered byte.

```go
// parseStream chooses by the stream's first non-space byte after buffering it
// back. Anthropic's SSE begins with "event:"; Vertex's Gemini SSE with "data:".
// Both are line-oriented, so one bufio.Reader peek settles it without consuming
// anything.
func parseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	br := bufio.NewReaderSize(r, 4<<10)
	prefix, err := br.Peek(6)
	if err != nil && len(prefix) == 0 {
		return func(yield func(ir.StreamEvent, error) bool) {
			yield(ir.StreamEvent{}, err)
		}
	}
	if string(prefix) == "event:" {
		return anthropicadapter.ParseStream(br, maxLine)
	}
	return geminiadapter.ParseStream(br, maxLine)
}
```

`bufio` and `iter` join the import block. **Check `anthropic.ParseStream`'s
signature before writing this** — if it is a method on `*Adapter` rather than a
package function, hold a package-level adapter the way `google.go` holds
`gemini`.

- [ ] **Step 6: Register it**

In `internal/server/server.go`, add `"vertex": vertexadapter.New(),` to the
adapter map.

- [ ] **Step 7: Run the tests, verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/vertex/ -count=1 -v 2>&1 | tail -20
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/vertex internal/server
git commit -m "feat(vertex): dispatch per publisher"
```

---

### Task 10: Seeding the Vertex catalog, and routing on publisher

**Files:**
- Create: `internal/catalog/seed.go`
- Modify: `internal/catalog/preset.go`, `internal/catalog/presets.yaml`, `internal/catalog/discovery.go`, `internal/router/router.go`, `internal/exec/exec.go`
- Test: `internal/catalog/seed_test.go`, `internal/router/publisher_test.go`

**Interfaces:**
- Consumes: `vertex.PublisherGoogle`/`PublisherAnthropic` — as string literals, since catalog must not import an adapter.
- Produces: `catalog.Preset.Publisher`, `catalog.SeedFromPreset(p provider.Provider, preset Preset, doc Doc) []store.DiscoveredModel`; `router.Candidate.Publisher` populated.

Spec §4.3: "Vertex offers no practical API for listing which models a project
may actually call... Vertex catalog entries are seeded from presets and
models.dev filtered by the publishers the provider row declares, and the
credential probe confirms reachability of one model. Phase 6's discovery worker
skips Vertex rather than pretending."

**The publisher is read from the preset the provider row names, not from a new
column.** The spec says "the publishers the provider row declares"; the two
shipped presets, `vertex` and `vertex-anthropic`, already encode exactly that
split, and a provider is created from a preset. Adding a column for a fact the
preset already carries would be a second place to get it wrong. This is a
deliberate reading of §4.3, recorded here so a reviewer can disagree with it
knowingly.

**`router.Candidate.Publisher` has existed since phase 3 and has never been
populated.** Confirm before writing:

```bash
grep -rn 'Publisher' internal/router/
```

Without this half, every Vertex request would take the Google route regardless
of the model, and every Claude call would 400 — which is the failure spec §4.1
warns about, reached from the routing side instead of the adapter side.

- [ ] **Step 1: Write the failing tests**

Create `internal/catalog/seed_test.go`:

```go
package catalog

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/provider"
)

func vertexDoc() Doc {
	// Two providers in the models.dev document, so the filter has something to
	// exclude rather than trivially passing.
	return docFrom(t2Models())
}

func TestSeedTakesOnlyThePresetsPublisher(t *testing.T) {
	p := provider.Provider{ID: "v", Kind: "vertex", Preset: "vertex-anthropic"}
	preset := Preset{
		Kind: "vertex", Publisher: "publishers/anthropic",
		ModelsDevID: "google-vertex-anthropic", Surfaces: []string{"llm"},
	}
	got := SeedFromPreset(p, preset, vertexDoc())
	if len(got) == 0 {
		t.Fatal("seeding produced nothing")
	}
	for _, m := range got {
		if m.Publisher != "publishers/anthropic" {
			t.Errorf("%s carries publisher %q", m.ModelID, m.Publisher)
		}
	}
}

func TestSeedCarriesMetadataFromModelsDev(t *testing.T) {
	// The whole point: Vertex has no listing, so everything the router needs
	// to filter on has to come from the document.
	p := provider.Provider{ID: "v", Kind: "vertex", Preset: "vertex"}
	preset := Preset{Kind: "vertex", Publisher: "publishers/google", ModelsDevID: "google-vertex"}
	got := SeedFromPreset(p, preset, vertexDoc())
	for _, m := range got {
		if m.ContextWindow == 0 {
			t.Errorf("%s has no context window; the router cannot size a request", m.ModelID)
		}
	}
}

func TestSeedIsEmptyWithoutAPublisher(t *testing.T) {
	// A preset that is not a vertex one must not be seeded: its models come
	// from a real listing endpoint, and seeding would fight discovery.
	got := SeedFromPreset(
		provider.Provider{ID: "g", Kind: "openaicompat", Preset: "groq"},
		Preset{Kind: "openaicompat", ModelsDevID: "groq"}, vertexDoc())
	if len(got) != 0 {
		t.Errorf("seeded %d models for a discoverable kind", len(got))
	}
}
```

`docFrom` and `t2Models` stand for whatever fixture `internal/catalog`'s
existing tests use to build a `Doc`. **Find it first** — `modelsdev_test.go` or
`merge_test.go` — and reuse it rather than writing a second one.

Create `internal/router/publisher_test.go`:

```go
package router

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestCandidateCarriesThePublisher(t *testing.T) {
	// Without this every Vertex request takes the Google route regardless of
	// the model, and every Claude call 400s.
	cands, _, err := Resolve(
		Query{Model: "claude-sonnet-4-5", Surface: ir.SurfaceLLM},
		vertexSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) == 0 {
		t.Fatal("no candidates")
	}
	if cands[0].Publisher != "publishers/anthropic" {
		t.Errorf("publisher = %q, want publishers/anthropic", cands[0].Publisher)
	}
}

func TestANonVertexCandidateHasNoPublisher(t *testing.T) {
	cands, _, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM},
		fullSnap(twoProviders(), nil, availability{}))
	if err != nil {
		t.Fatal(err)
	}
	if cands[0].Publisher != "" {
		t.Errorf("publisher = %q, want empty for openaicompat", cands[0].Publisher)
	}
}
```

`vertexSnapshot` builds a snapshot whose catalog carries one model with
`Publisher: "publishers/anthropic"`. **Model the helper on `fullSnap`**, which
already exists in `router_test.go`, and use the same `availability` type that
file uses — the sketch above names it generically.

- [ ] **Step 2: Run them to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ ./internal/router/ -run 'Seed|Publisher' 2>&1 | head -15
```

Expected: undefined `SeedFromPreset`, `Preset.Publisher`, and an empty
`Candidate.Publisher`.

- [ ] **Step 3: Declare the publisher on the two presets**

In `internal/catalog/preset.go`, add to `Preset`:

```go
	// Publisher selects Vertex's request builder and marks a preset as
	// seed-only: a kind with no listing endpoint, spec §4.3. Empty for every
	// other preset.
	Publisher string `yaml:"publisher,omitempty"`
```

In `internal/catalog/presets.yaml`, add one line to each Vertex entry:

```yaml
vertex:
  name: Google Vertex AI
  kind: vertex
  publisher: publishers/google
  ...

vertex-anthropic:
  name: Google Vertex AI (Anthropic publisher)
  kind: vertex
  publisher: publishers/anthropic
  ...
```

**`presets.yaml` is generated.** Check whether `internal/catalog/presetgen` (or
whatever phase 6 named it) owns the file before editing it by hand; if it does,
the source declaration is what changes and the file is regenerated. Phase 5's
Task 2 hit exactly this and the answer is in `docs/PROGRESS.md`.

Add a guard test to `internal/catalog/preset_test.go`:

```go
func TestEveryVertexPresetDeclaresAPublisher(t *testing.T) {
	// A vertex preset with no publisher seeds nothing and routes to the Google
	// builder by default, which is a silent wrong answer for a Claude model.
	for id, p := range Embedded() {
		if p.Kind != "vertex" {
			continue
		}
		if p.Publisher != "publishers/google" && p.Publisher != "publishers/anthropic" {
			t.Errorf("%s: publisher = %q", id, p.Publisher)
		}
	}
}
```

- [ ] **Step 4: Implement seeding**

Create `internal/catalog/seed.go`:

```go
package catalog

import (
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// SeedFromPreset builds catalog rows for a kind with no listing endpoint.
//
// Spec §4.3: Vertex offers no practical API for listing which models a project
// may actually call — Model Garden enumeration is noisy and partner entitlement
// is not cleanly queryable. Seeding from models.dev, filtered by the publisher
// the provider's preset declares, is the honest alternative to pretending
// discovery works.
//
// A preset with no publisher seeds nothing. That is what keeps this from
// fighting the discovery worker on every kind that does have a listing.
func SeedFromPreset(p provider.Provider, preset Preset, doc Doc) []store.DiscoveredModel {
	if preset.Publisher == "" {
		return nil
	}
	ids := doc.ModelsFor(preset.ModelsDevID)
	out := make([]store.DiscoveredModel, 0, len(ids))
	for _, id := range ids {
		meta, ok := doc.Metadata(preset.ModelsDevID, id)
		if !ok {
			continue
		}
		out = append(out, store.DiscoveredModel{
			ModelID:         id,
			Publisher:       preset.Publisher,
			ContextWindow:   meta.ContextWindow,
			MaxOutputTokens: meta.MaxOutputTokens,
		})
	}
	return out
}
```

`Doc.ModelsFor(providerKey string) []string` may not exist. **Look at
`internal/catalog/modelsdev.go` first**: `Metadata(modelsDevID, modelID)` does,
so the document is already keyed this way and enumerating one provider's models
is a small addition beside it. Add it there rather than reaching into the
document's internals from `seed.go`.

`store.DiscoveredModel` gains a `Publisher` field, and
`RecordDiscoverySuccess` writes it to the column that has existed since
migration 0001. Confirm with:

```bash
grep -n 'publisher' internal/store/catalog.go
```

In `internal/catalog/discovery.go`, seed instead of listing when the preset says
so, before `ProbeForKind` is consulted:

```go
	if seeded := SeedFromPreset(p, preset, d.doc()); len(seeded) > 0 {
		// No credential is spent and no request is made: spec §4.3's "discovery
		// is not pretended". The credential probe confirms reachability
		// separately, on the operator's schedule rather than every tick.
		if err := d.db.RecordDiscoverySuccess(context.WithoutCancel(ctx), p.ID, seeded, now); err != nil {
			log.Printf("discovery: %s: seed: %v", p.ID, err)
		}
		return
	}
```

`d.doc()` is however the discoverer already reaches the models.dev document —
**find it rather than adding a second path**; phase 6 wired one for `Merge`.

- [ ] **Step 5: Populate the candidate's publisher**

In `internal/router/router.go`, wherever a `Candidate` is constructed, carry the
catalog entry's publisher across. The catalog `Model` already has the field; it
has simply never been read.

```go
		cands = append(cands, Candidate{
			ProviderID: p.ID, KeyID: cred.ID, Model: m,
			Kind: p.Kind, Publisher: entry.Publisher, Inferred: entry.Inferred(),
		})
```

**`entry` is whatever the surrounding code calls the resolved catalog model** —
read the construction site rather than pattern-matching this snippet onto it.

`internal/exec/exec.go` already passes `Publisher: c.Publisher` onto the target,
from Task 1.

- [ ] **Step 6: Run the tests, verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/catalog/ ./internal/router/ ./internal/exec/ -count=1 2>&1 | tail -15
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/catalog internal/router internal/exec internal/store
git commit -m "feat(vertex): seed the catalog and route on publisher"
```

---

### Task 11: PKCE and the single-use state

**Files:**
- Create: `internal/auth/pkce.go`
- Test: `internal/auth/pkce_test.go`

**Interfaces:**
- Produces: `auth.NewVerifier() (string, error)`, `auth.Challenge(verifier string) string`, `auth.Flow`, `auth.FlowStore`, `auth.NewFlowStore(ttl time.Duration) *FlowStore`, `(*FlowStore).Begin(f Flow) (state string, err error)`, `(*FlowStore).Claim(state, sessionID string) (Flow, error)`, `(*FlowStore).Sweep(now time.Time)`.

Spec §5.1: "`state` is single-use, expiring, and validated against the
initiating session on both paths; the code exchange happens server-side with the
stored PKCE verifier."

**`state` is the only defense against forced account binding.** The spec is
blunt about why: `GET /api/oauth/callback` is a state-changing GET reachable via
a cross-site top-level redirect, which works *because* the session cookie is
`SameSite=Lax`. CSRF's usual header check cannot apply to a navigation, so
everything rests on state being unguessable, single-use, expiring, and bound to
the session that started the flow.

**The store is in memory, deliberately.** A flow lives for the minute or two an
operator spends in a consent screen. Persisting it would put a PKCE verifier on
disk for no benefit — a restart mid-flow means clicking Connect again, which is
a better failure than a verifier surviving in a backup.

- [ ] **Step 1: Write the failing test**

Create `internal/auth/pkce_test.go`:

```go
package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestVerifiersAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		v, err := NewVerifier()
		if err != nil {
			t.Fatal(err)
		}
		if seen[v] {
			t.Fatal("a verifier repeated")
		}
		seen[v] = true
		// RFC 7636 requires 43 to 128 characters from the unreserved set.
		if len(v) < 43 || len(v) > 128 {
			t.Errorf("verifier length %d is outside RFC 7636's range", len(v))
		}
		if strings.ContainsAny(v, "+/=") {
			t.Errorf("verifier %q is not URL-safe base64", v)
		}
	}
}

func TestChallengeIsS256(t *testing.T) {
	// The server recomputes this. A plain challenge would let anyone who
	// intercepts the redirect exchange the code themselves.
	v := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHI"
	sum := sha256.Sum256([]byte(v))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got := Challenge(v); got != want {
		t.Errorf("challenge = %q, want %q", got, want)
	}
}

func flowStore(t *testing.T) *FlowStore {
	t.Helper()
	return NewFlowStore(10 * time.Minute)
}

func TestStateIsSingleUse(t *testing.T) {
	// Replaying a callback must not bind a second account, and must not let an
	// attacker who saw one redirect reuse it.
	s := flowStore(t)
	state, err := s.Begin(Flow{ProviderID: "p", SessionID: "sess", Verifier: "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(state, "sess"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(state, "sess"); err == nil {
		t.Fatal("a state must not be claimable twice")
	}
}

func TestStateIsBoundToTheSession(t *testing.T) {
	// The forced-binding attack: the victim's browser follows an attacker's
	// redirect carrying the attacker's code. Without this check the victim's
	// gateway ends up serving traffic on the attacker's account.
	s := flowStore(t)
	state, err := s.Begin(Flow{ProviderID: "p", SessionID: "victim", Verifier: "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(state, "attacker"); err == nil {
		t.Fatal("a state from another session must be refused")
	}
	// And a refused claim must not consume it: the legitimate operator's own
	// callback is still on its way.
	if _, err := s.Claim(state, "victim"); err != nil {
		t.Fatalf("the real session lost its flow: %v", err)
	}
}

func TestStateExpires(t *testing.T) {
	s := NewFlowStore(time.Millisecond)
	state, err := s.Begin(Flow{ProviderID: "p", SessionID: "sess", Verifier: "v"})
	if err != nil {
		t.Fatal(err)
	}
	s.Sweep(time.Now().Add(time.Minute))
	if _, err := s.Claim(state, "sess"); err == nil {
		t.Fatal("an expired state must be refused")
	}
}

func TestAnUnknownStateIsRefused(t *testing.T) {
	if _, err := flowStore(t).Claim("never-issued", "sess"); err == nil {
		t.Fatal("an unknown state must be refused")
	}
}

func TestStatesAreUnguessable(t *testing.T) {
	s := flowStore(t)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		state, err := s.Begin(Flow{ProviderID: "p", SessionID: "sess", Verifier: "v"})
		if err != nil {
			t.Fatal(err)
		}
		if seen[state] {
			t.Fatal("a state repeated")
		}
		seen[state] = true
		// 32 bytes of entropy. Anything a person could brute-force is the
		// whole vulnerability, since state is the only defense here.
		if len(state) < 43 {
			t.Errorf("state %q is too short to be unguessable", state)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails, then implement**

Create `internal/auth/pkce.go`:

```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// verifierBytes yields a 43-character verifier, the RFC 7636 minimum, from 32
// bytes of entropy.
const verifierBytes = 32

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewVerifier returns a PKCE code verifier.
func NewVerifier() (string, error) { return randomURLSafe(verifierBytes) }

// Challenge is the S256 transformation. Plain is permitted by the RFC and is
// worthless: anyone who intercepts the redirect could exchange the code.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Flow is one in-progress connect attempt.
type Flow struct {
	ProviderID string
	// SessionID binds the flow to the admin session that started it. Spec
	// §5.1: the callback is a state-changing GET reachable via a cross-site
	// top-level redirect, so this check and the unguessable state are the only
	// things standing between a victim's gateway and an attacker's account.
	SessionID string
	Verifier  string
	Label     string
	// RedirectURI is echoed to the token endpoint, which validates that it
	// matches the one the authorize step carried.
	RedirectURI string
	CreatedAt   time.Time
}

var (
	ErrUnknownState = errors.New("unknown or already-used state")
	ErrWrongSession = errors.New("this authorization was not started by this session")
	ErrStateExpired = errors.New("this authorization expired; start it again")
)

// FlowStore holds in-progress flows in memory.
//
// In memory deliberately: a flow lives for the minute or two an operator spends
// in a consent screen, and persisting it would put a PKCE verifier on disk for
// no benefit. A restart mid-flow means clicking Connect again, which is a
// better failure than a verifier surviving into a backup.
type FlowStore struct {
	ttl time.Duration

	mu    sync.Mutex
	flows map[string]Flow
}

func NewFlowStore(ttl time.Duration) *FlowStore {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &FlowStore{ttl: ttl, flows: map[string]Flow{}}
}

// Begin records a flow and returns its state parameter.
func (s *FlowStore) Begin(f Flow) (string, error) {
	state, err := randomURLSafe(32)
	if err != nil {
		return "", err
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows[state] = f
	return state, nil
}

// Claim consumes a state and returns its flow.
//
// A session mismatch does NOT consume the state: the legitimate operator's own
// callback may still be on its way, and letting an attacker's failed attempt
// invalidate it turns a blocked attack into a denial of service.
func (s *FlowStore) Claim(state, sessionID string) (Flow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, ok := s.flows[state]
	if !ok {
		return Flow{}, ErrUnknownState
	}
	if time.Since(f.CreatedAt) > s.ttl {
		delete(s.flows, state)
		return Flow{}, ErrStateExpired
	}
	// Constant time for the same reason the CSRF check is: the comparison is
	// against a value an attacker supplies and can iterate on.
	if subtle.ConstantTimeCompare([]byte(f.SessionID), []byte(sessionID)) != 1 {
		return Flow{}, ErrWrongSession
	}
	delete(s.flows, state)
	return f, nil
}

// Sweep drops expired flows. Called from the same worker that sweeps sessions.
func (s *FlowStore) Sweep(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for state, f := range s.flows {
		if now.Sub(f.CreatedAt) > s.ttl {
			delete(s.flows, state)
		}
	}
}
```

- [ ] **Step 3: Run the tests, verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/auth/ -count=1 -race 2>&1 | tail -10
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/auth
git commit -m "feat(auth): add pkce and single-use state"
```

---

### Task 12: Starting a connect flow, and completing it by paste

**Files:**
- Create: `internal/admin/oauth.go`
- Modify: `internal/admin/admin.go`, `internal/server/server.go`
- Test: `internal/admin/oauth_test.go`

**Interfaces:**
- Consumes: `auth.FlowStore`, `auth.NewVerifier`, `auth.Challenge`, `auth.Token`, `store.AddCredential`.
- Produces: `POST /api/providers/{id}/oauth/start`, `POST /api/providers/{id}/oauth/complete`; `admin.Deps.Flows`, `admin.Deps.HTTP`.

Spec §5.1:

> **The redirect cannot generally target the admin port.** Subscription vendors
> register public-client redirect URIs — typically `http://localhost:{port}/callback`
> or an out-of-band paste page — and a homelab admin origin like
> `http://192.168.0.196:8081/api/oauth/callback` will be rejected at the
> authorize step before any code exists.
>
> - **Manual paste.** The UI presents the authorize URL; the operator completes it in a browser and pastes the full redirected URL back into the UI. **This is the default and always works.**

That is why the paste path lands before the listener path. It is not the
fallback; it is the one that works everywhere, and the listener is an
optimisation for the case where Darkrouter runs on the operator's own machine.

**`/oauth/complete` is a POST and carries the CSRF header** like every other
mutation. It is `GET /api/oauth/callback` in Task 13 that cannot, and that is
exactly why state does the work there.

**The pasted value is a whole URL, not a code.** Asking for the code alone makes
the operator parse a query string by eye, and they will paste the wrong
substring. Taking the URL means the `state` travels with it, which is what makes
the paste path validate identically to the listener path — a property spec §7
asks for by name.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/oauth_test.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// oauthProvider creates a provider from the anthropic-oauth preset, which is
// the only shipped preset declaring an OAuth block. Confirm that is still true
// against internal/catalog/presets.yaml before relying on it.
func oauthProvider(t *testing.T, s *Server) string {
	t.Helper()
	jar := sessionJar(t, s)
	var got struct {
		ID string `json:"id"`
	}
	post(t, s, jar, "/api/providers", `{"id":"sub","preset":"anthropic-oauth"}`, &got)
	if got.ID == "" {
		t.Fatal("the provider was not created")
	}
	return got.ID
}

func TestStartReturnsAnAuthorizeURL(t *testing.T) {
	s, jar := testServerWithSession(t)
	id := oauthProvider(t, s)

	var body struct {
		AuthorizeURL string `json:"authorize_url"`
		State        string `json:"state"`
		RedirectURI  string `json:"redirect_uri"`
		Style        string `json:"style"`
	}
	post(t, s, jar, "/api/providers/"+id+"/oauth/start", `{"label":"personal"}`, &body)

	u, err := url.Parse(body.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q; plain PKCE is worthless here",
			q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == "" {
		t.Error("no code_challenge")
	}
	if q.Get("state") != body.State {
		t.Errorf("the state in the URL and the response disagree")
	}
	if q.Get("client_id") == "" {
		t.Error("no client_id; the preset's value did not reach the URL")
	}
	// The verifier must never leave the server.
	if strings.Contains(body.AuthorizeURL, "code_verifier") {
		t.Fatal("the PKCE verifier is in the authorize URL")
	}
}

func TestStartRefusesANonOAuthProvider(t *testing.T) {
	s, jar := testServerWithSession(t)
	// A groq provider, created the way providers_test.go does.
	code := postStatus(t, s, jar, "/api/providers/groq/oauth/start", `{}`)
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestCompleteExchangesThePastedURL(t *testing.T) {
	// The operator pastes the whole redirected URL. Asking for the code alone
	// makes them parse a query string by eye.
	s, jar, token := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s)

	var start struct {
		State       string `json:"state"`
		RedirectURI string `json:"redirect_uri"`
	}
	post(t, s, jar, "/api/providers/"+id+"/oauth/start", `{"label":"personal"}`, &start)

	pasted := start.RedirectURI + "?code=the-code&state=" + url.QueryEscape(start.State)
	body, _ := json.Marshal(map[string]string{"redirected_url": pasted})
	var done struct {
		CredentialID string `json:"credential_id"`
		Account      string `json:"account"`
	}
	post(t, s, jar, "/api/providers/"+id+"/oauth/complete", string(body), &done)

	if done.CredentialID == "" {
		t.Fatal("no credential was created")
	}
	if token.verifier == "" {
		t.Error("the token exchange did not send a code_verifier")
	}
	if token.code != "the-code" {
		t.Errorf("the exchange sent code %q", token.code)
	}
	// Spec §4.1: no credential material in any response.
	raw := rawBody(t, s, jar, "GET", "/api/providers", "")
	if strings.Contains(raw, "the-refresh-token") || strings.Contains(raw, "the-access-token") {
		t.Fatal("a token appeared in GET /api/providers")
	}
}

func TestCompleteRefusesAMismatchedState(t *testing.T) {
	s, jar, _ := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s)

	var start struct {
		RedirectURI string `json:"redirect_uri"`
	}
	post(t, s, jar, "/api/providers/"+id+"/oauth/start", `{}`, &start)

	pasted := start.RedirectURI + "?code=c&state=not-the-state"
	body, _ := json.Marshal(map[string]string{"redirected_url": pasted})
	if code := postStatus(t, s, jar, "/api/providers/"+id+"/oauth/complete", string(body)); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestCompleteRefusesAReusedState(t *testing.T) {
	s, jar, _ := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s)

	var start struct {
		State       string `json:"state"`
		RedirectURI string `json:"redirect_uri"`
	}
	post(t, s, jar, "/api/providers/"+id+"/oauth/start", `{}`, &start)
	pasted := start.RedirectURI + "?code=c&state=" + url.QueryEscape(start.State)
	body, _ := json.Marshal(map[string]string{"redirected_url": pasted})

	post(t, s, jar, "/api/providers/"+id+"/oauth/complete", string(body), nil)
	if code := postStatus(t, s, jar, "/api/providers/"+id+"/oauth/complete", string(body)); code == http.StatusOK {
		t.Error("a state was accepted twice")
	}
}

func TestCompleteSurfacesTheProvidersError(t *testing.T) {
	// The redirect carries error=access_denied when the operator clicks Deny.
	// Reporting "no code" would send them looking for a bug.
	s, jar, _ := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s)

	var start struct {
		State       string `json:"state"`
		RedirectURI string `json:"redirect_uri"`
	}
	post(t, s, jar, "/api/providers/"+id+"/oauth/start", `{}`, &start)
	pasted := start.RedirectURI + "?error=access_denied&error_description=User+denied&state=" +
		url.QueryEscape(start.State)
	body, _ := json.Marshal(map[string]string{"redirected_url": pasted})

	raw, code := postRaw(t, s, jar, "/api/providers/"+id+"/oauth/complete", string(body))
	if code == http.StatusOK {
		t.Fatal("a denied authorization must not succeed")
	}
	if !strings.Contains(raw, "access_denied") {
		t.Errorf("the error did not reach the operator: %s", raw)
	}
}

func TestCompleteRequiresCSRF(t *testing.T) {
	s, jar, _ := serverWithFakeAuthServer(t)
	req := httptest.NewRequest("POST", "/api/providers/sub/oauth/complete", strings.NewReader("{}"))
	// A session cookie but no CSRF header.
	applyJar(req, jar)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}
```

`testServerWithSession`, `post`, `postStatus`, `postRaw`, `rawBody` and
`applyJar` stand for the helpers `internal/admin`'s existing tests already have.
**Read `internal/admin/providers_test.go` and reuse them**; write new ones only
for what is genuinely missing. `serverWithFakeAuthServer` is new: it stands up
an `httptest` token endpoint, records the `code` and `code_verifier` it receives,
returns `{"access_token":"the-access-token","refresh_token":"the-refresh-token","expires_in":3600}`,
and points the provider's preset OAuth config at it.

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run OAuth 2>&1 | head -15
```

Expected: 404 from every route.

- [ ] **Step 3: Implement**

Create `internal/admin/oauth.go`:

```go
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/store"
)

type oauthStartBody struct {
	Label string `json:"label"`
}

// handleOAuthStart begins an authorization-code flow with PKCE.
//
// The response carries the authorize URL rather than redirecting to it. The
// caller is a fetch from the SPA, and a 302 to a vendor's consent screen would
// be followed by the fetch rather than by the browser.
func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	preset, cfg, ok := s.oauthConfig(r.Context(), id)
	if !ok {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("provider %q is not configured for an OAuth credential", id))
		return
	}

	var body oauthStartBody
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)

	verifier, err := auth.NewVerifier()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	redirect := redirectURI(cfg)
	state, err := s.deps.Flows.Begin(auth.Flow{
		ProviderID: id, SessionID: sessionIDFrom(r), Verifier: verifier,
		Label: body.Label, RedirectURI: redirect, CreatedAt: time.Now(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirect)
	q.Set("scope", strings.Join(cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", auth.Challenge(verifier))
	// S256 only. Plain is permitted by RFC 7636 and worthless: anyone who
	// intercepts the redirect could exchange the code themselves.
	q.Set("code_challenge_method", "S256")

	writeJSON(w, http.StatusOK, map[string]any{
		"authorize_url": cfg.AuthorizeURL + "?" + q.Encode(),
		"state":         state,
		"redirect_uri":  redirect,
		// The UI shows a paste box for "manual" and waits for the listener for
		// "localhost". Both validate identically on the server.
		"style":  cfg.Redirect.Style,
		"preset": preset,
	})
}

// redirectURI is what the vendor registered, not Darkrouter's admin origin.
//
// Spec §5.1: a homelab admin origin is rejected at the authorize step before
// any code exists, which is why the paste path is the default rather than the
// fallback.
func redirectURI(cfg catalog.OAuth) string {
	if cfg.Redirect.Style == "localhost" && cfg.Redirect.Port != 0 {
		return fmt.Sprintf("http://localhost:%d/callback", cfg.Redirect.Port)
	}
	// The out-of-band page. The operator still pastes the resulting URL back.
	return "urn:ietf:wg:oauth:2.0:oob"
}

type oauthCompleteBody struct {
	// RedirectedURL is the whole URL the browser landed on. Asking for the
	// code alone makes the operator parse a query string by eye, and taking
	// the URL means state travels with it — which is what makes this path
	// validate identically to the listener path.
	RedirectedURL string `json:"redirected_url"`
}

func (s *Server) handleOAuthComplete(w http.ResponseWriter, r *http.Request) {
	var body oauthCompleteBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	code, state, err := parseRedirected(body.RedirectedURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.finishOAuth(w, r, code, state, sessionIDFrom(r))
}

// parseRedirected pulls the code and state out of a pasted URL, and surfaces
// the provider's own refusal rather than reporting a missing code.
func parseRedirected(raw string) (code, state string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("paste the whole URL the browser landed on")
	}
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", "", fmt.Errorf("that does not look like a URL: %w", perr)
	}
	q := u.Query()
	if e := q.Get("error"); e != "" {
		desc := q.Get("error_description")
		if desc == "" {
			return "", "", fmt.Errorf("the provider refused the authorization: %s", e)
		}
		return "", "", fmt.Errorf("the provider refused the authorization: %s (%s)", e, desc)
	}
	code, state = q.Get("code"), q.Get("state")
	if code == "" || state == "" {
		return "", "", fmt.Errorf("that URL carries no authorization code")
	}
	return code, state, nil
}

// finishOAuth is shared by the paste path and Task 13's listener path, which is
// how spec §7's "the manual-paste path validating identically to the listener
// path" is true by construction rather than by inspection.
func (s *Server) finishOAuth(w http.ResponseWriter, r *http.Request, code, state, sessionID string) {
	flow, err := s.deps.Flows.Claim(state, sessionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, cfg, ok := s.oauthConfig(r.Context(), flow.ProviderID)
	if !ok {
		writeError(w, http.StatusBadRequest, "this provider is no longer configured for OAuth")
		return
	}

	tok, err := auth.ExchangeCode(r.Context(), s.httpClient(), authConfig(cfg), auth.ExchangeInput{
		Code: code, Verifier: flow.Verifier, RedirectURI: flow.RedirectURI,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	raw, err := tok.Marshal()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	label := flow.Label
	if label == "" {
		label = tok.Account
	}
	if label == "" {
		label = "subscription"
	}
	credID, err := s.deps.DB.AddCredential(r.Context(), s.deps.Key, store.Credential{
		ProviderID: flow.ProviderID, Label: label, Kind: "oauth",
		Secret: string(raw), Enabled: true, ExpiresAt: tok.Unix(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reloadProviders(r.Context())

	// The token is never echoed. What comes back is what the dashboard shows.
	writeJSON(w, http.StatusCreated, map[string]any{
		"credential_id": credID,
		"label":         label,
		"account":       tok.Account,
	})
}

// oauthConfig resolves the provider's preset OAuth block.
func (s *Server) oauthConfig(ctx context.Context, providerID string) (string, catalog.OAuth, bool) {
	rows, err := s.deps.DB.ProviderRows(ctx)
	if err != nil {
		return "", catalog.OAuth{}, false
	}
	for _, p := range rows {
		if p.ID != providerID {
			continue
		}
		preset, ok := s.deps.Presets[p.Preset]
		if !ok || preset.OAuth == nil {
			return "", catalog.OAuth{}, false
		}
		return p.Preset, *preset.OAuth, true
	}
	return "", catalog.OAuth{}, false
}

func (s *Server) httpClient() *http.Client {
	if s.deps.HTTP != nil {
		return s.deps.HTTP
	}
	return http.DefaultClient
}

// authConfig translates the preset's OAuth block into the shape internal/auth
// takes. The two carry the same fields under different names on purpose: auth
// must not import catalog, which imports provider, which imports store.
func authConfig(c catalog.OAuth) auth.OAuthConfig {
	return auth.OAuthConfig{
		AuthorizeURL: c.AuthorizeURL, TokenURL: c.TokenURL,
		ClientID: c.ClientID, Scopes: c.Scopes,
	}
}
```

`sessionIDFrom(r)` reads the session the `requireSession` middleware already
resolved. **Read `internal/admin/auth.go`**: phase 7 put the session on the
request context or in a struct, and this must use that rather than re-reading
the cookie.

`auth.ExchangeCode` and `auth.ExchangeInput` land in Task 14 alongside refresh,
because the two share their parsing of a token response. Until then, a minimal
version in `internal/auth/oauth.go` is enough to make this task's tests pass —
Task 14 replaces it with the mutexed, rotation-aware one.

Register the routes in `internal/admin/admin.go`, both behind CSRF:

```go
	s.mux.HandleFunc("POST /api/providers/{id}/oauth/start", s.requireCSRF(s.handleOAuthStart))
	s.mux.HandleFunc("POST /api/providers/{id}/oauth/complete", s.requireCSRF(s.handleOAuthComplete))
```

and add `Flows *auth.FlowStore` and `HTTP *http.Client` to `admin.Deps`, built
in `internal/server/server.go` beside the admin server.

- [ ] **Step 4: Run the tests, verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -count=1 2>&1 | tail -15
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/admin internal/auth internal/server
git commit -m "feat(oauth): connect an account by paste"
```

---

### Task 13: The localhost listener and the callback GET

**Files:**
- Create: `internal/admin/listener.go`
- Modify: `internal/admin/oauth.go`, `internal/admin/admin.go`
- Test: `internal/admin/listener_test.go`

**Interfaces:**
- Produces: `GET /api/oauth/callback`; `admin.redirectListener`, `(*Server).startListener(flow auth.Flow, port int) error`.

Spec §5.1:

> **Localhost listener.** Where a vendor's registered URI is a localhost
> callback and Darkrouter runs on the operator's own machine, a temporary
> listener on the registered port receives the redirect directly.
>
> `GET /api/oauth/callback` exists for the listener path and is a state-changing
> GET reachable via a cross-site top-level redirect — which works only because
> the session cookie is `SameSite=Lax`, making `state` validation the sole
> defense against forced account binding. **Admin-port access logging must never
> record query strings**, since the authorization code arrives in one.

**There is no admin-port access logging today.** Confirm it, then pin it:

```bash
grep -rn 'RawQuery\|r.URL.String()' internal/server/ internal/admin/ | grep -v _test
```

An empty result means the requirement holds vacuously. The test below turns that
into something a future middleware cannot quietly break.

**The listener binds to loopback only.** The vendor's registered URI is
`http://localhost:{port}/callback`, and binding `0.0.0.0` would put a
code-accepting endpoint on the LAN for the duration of the flow.

**The listener is torn down on completion, on expiry, and on shutdown.** A
listener that outlived its flow would hold a port an operator's own tooling may
want, and would accept a code with no flow to match it.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/listener_test.go`:

```go
package admin

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCallbackCompletesAListenerFlow(t *testing.T) {
	s, jar, token := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s)

	var start struct {
		State       string `json:"state"`
		RedirectURI string `json:"redirect_uri"`
		Style       string `json:"style"`
	}
	post(t, s, jar, "/api/providers/"+id+"/oauth/start", `{}`, &start)
	if start.Style != "localhost" {
		t.Skipf("this preset uses the %s redirect style", start.Style)
	}

	// The browser follows the vendor's redirect to the loopback listener,
	// which forwards to the admin port. Driving the listener directly is the
	// same request the browser would make.
	u, err := url.Parse(start.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(u.String() + "?code=the-code&state=" + url.QueryEscape(start.State))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listener returned %d", resp.StatusCode)
	}
	if token.code != "the-code" {
		t.Errorf("the exchange sent code %q", token.code)
	}
}

func TestTheListenerBindsLoopbackOnly(t *testing.T) {
	// Binding 0.0.0.0 would put a code-accepting endpoint on the LAN for the
	// duration of the flow.
	l, err := newRedirectListener(0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	host, _, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Errorf("listening on %s, want loopback", host)
	}
}

func TestTheListenerStopsAfterOneCallback(t *testing.T) {
	// A listener that outlived its flow holds a port the operator's own
	// tooling may want, and accepts a code with no flow to match it.
	s, jar, _ := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s)

	var start struct {
		State       string `json:"state"`
		RedirectURI string `json:"redirect_uri"`
	}
	post(t, s, jar, "/api/providers/"+id+"/oauth/start", `{}`, &start)
	u, _ := url.Parse(start.RedirectURI)
	if _, err := http.Get(u.String() + "?code=c&state=" + url.QueryEscape(start.State)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := net.DialTimeout("tcp", u.Host, 100*time.Millisecond); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the listener on %s is still accepting", u.Host)
}

func TestCallbackRefusesAForeignState(t *testing.T) {
	// The forced-binding attack, at the endpoint where it applies. SameSite=Lax
	// means the victim's cookie travels with a cross-site top-level redirect,
	// so state is the whole defense.
	s, jar := testServerWithSession(t)
	req := httptest.NewRequest("GET", "/api/oauth/callback?code=attacker-code&state=forged", nil)
	applyJar(req, jar)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatal("a forged state was accepted")
	}
}

func TestCallbackRefusesWithoutASession(t *testing.T) {
	s, _ := testServerWithSession(t)
	req := httptest.NewRequest("GET", "/api/oauth/callback?code=c&state=s", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestNoQueryStringReachesALog(t *testing.T) {
	// Spec §5.1. There is no admin access logging today; this is what stops a
	// future middleware from logging the authorization code.
	s, jar := testServerWithSession(t)
	var logged strings.Builder
	restore := captureLog(t, &logged)
	defer restore()

	req := httptest.NewRequest("GET", "/api/oauth/callback?code=SECRET-CODE&state=x", nil)
	applyJar(req, jar)
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)

	if strings.Contains(logged.String(), "SECRET-CODE") {
		t.Fatalf("the authorization code reached a log line:\n%s", logged.String())
	}
}

var _ = fmt.Sprintf
```

`captureLog` redirects the standard logger's output into the builder and returns
a restore func. **Check whether `internal/admin` or `internal/store` already has
one** before adding it.

- [ ] **Step 2: Run it to verify it fails, then implement the listener**

Create `internal/admin/listener.go`:

```go
package admin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/auth"
)

// newRedirectListener binds the vendor's registered loopback port.
//
// Loopback only: the registered URI is http://localhost:{port}/callback, and
// binding 0.0.0.0 would put a code-accepting endpoint on the LAN for as long as
// the flow lasts.
func newRedirectListener(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}

// redirectListener receives exactly one callback and stops.
type redirectListener struct {
	srv  *http.Server
	once sync.Once
	done chan struct{}
}

// startListener runs a temporary server on the vendor's registered port and
// forwards the first callback to the admin port's own handler.
//
// Forwarding rather than completing the exchange here keeps one code path:
// spec §7 wants the paste path and the listener path to validate identically,
// and they do because both end in finishOAuth.
func (s *Server) startListener(flow auth.Flow, port int, sessionID string, ttl time.Duration) error {
	ln, err := newRedirectListener(port)
	if err != nil {
		// The port is taken — often by the vendor's own CLI, which is exactly
		// the situation the paste path exists for.
		return fmt.Errorf("cannot listen on the registered redirect port %d: %w", port, err)
	}

	rl := &redirectListener{done: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		defer rl.stop()
		code, state, perr := parseRedirected(r.URL.String())
		if perr != nil {
			// Plain text, and never the query string: the browser shows this
			// page and the operator may screenshot it.
			http.Error(w, perr.Error(), http.StatusBadRequest)
			return
		}
		if err := s.completeFromListener(r.Context(), code, state, sessionID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Connected. You can close this tab and return to Darkrouter."))
	})
	rl.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() { _ = rl.srv.Serve(ln) }()
	// Torn down on expiry as well as on completion. A listener that outlived
	// its flow would hold a port and accept a code with no flow to match.
	go func() {
		select {
		case <-rl.done:
		case <-time.After(ttl):
			rl.stop()
		}
	}()
	s.trackListener(flow.ProviderID, rl)
	return nil
}

func (r *redirectListener) stop() {
	r.once.Do(func() {
		close(r.done)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = r.srv.Shutdown(ctx)
	})
}

var _ = errors.New
var _ = url.Parse
```

`trackListener` keeps the live listeners on the `Server` so `Close` can stop
them at shutdown. A `map[string]*redirectListener` behind the existing mutex is
enough; starting a second flow for the same provider replaces and stops the
first.

`completeFromListener` is `finishOAuth`'s body without the `http.ResponseWriter`
— refactor `finishOAuth` into a function returning `(credentialID string, err
error)` and have both the HTTP handler and the listener call it. **That
refactor is the point of the task**: it is what makes "the manual-paste path
validates identically to the listener path" true by construction.

- [ ] **Step 3: Add the callback route**

In `internal/admin/oauth.go`:

```go
// handleOAuthCallback receives the redirect on the listener path.
//
// A state-changing GET, which is normally a mistake. It is unavoidable here:
// the browser arrives by top-level navigation, so no header can be attached and
// the CSRF check cannot apply. The session cookie is SameSite=Lax, so it does
// travel — which is precisely why state must be unguessable, single-use,
// expiring, and bound to the initiating session. That check is the whole
// defense against forced account binding.
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code, state, err := parseRedirected(r.URL.String())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.finishOAuth(w, r, code, state, sessionIDFrom(r))
}
```

Registered with `requireSession`, not `requireCSRF`:

```go
	// requireSession rather than requireCSRF: a top-level navigation carries no
	// header to check. State does that work, see handleOAuthCallback.
	s.mux.HandleFunc("GET /api/oauth/callback", s.requireSession(s.handleOAuthCallback))
```

And in `handleOAuthStart`, start the listener when the preset asks for one:

```go
	if cfg.Redirect.Style == "localhost" && cfg.Redirect.Port != 0 {
		if err := s.startListener(flow, cfg.Redirect.Port, sessionIDFrom(r), listenerTTL); err != nil {
			// Not fatal. The paste path always works, spec §5.1, and telling
			// the operator to use it beats failing the whole flow because a
			// port is busy.
			writeJSON(w, http.StatusOK, map[string]any{
				"authorize_url": authorizeURL, "state": state, "redirect_uri": redirect,
				"style": "manual", "listener_error": err.Error(),
			})
			return
		}
	}
```

**`flow` must be built before `Begin` rather than reconstructed** — restructure
the handler so the `auth.Flow` value is a local that both `Begin` and
`startListener` see.

- [ ] **Step 4: Run the tests, verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -count=1 -race 2>&1 | tail -15
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/admin
git commit -m "feat(oauth): receive the redirect on localhost"
```

---

### Task 14: Refresh, rotation, and the two failure modes

**Files:**
- Create: `internal/auth/oauth.go`, `internal/auth/refresh.go`
- Modify: `internal/auth/auth.go`, `internal/server/server.go`
- Test: `internal/auth/oauth_test.go`

**Interfaces:**
- Produces: `auth.ExchangeCode`, `auth.ExchangeInput`, `(*Manager).oauthFor`, `auth.TokenStore`, `auth.OAuthPresets`, `auth.RefreshWorker`, `auth.NewRefreshWorker(Deps, RefreshOptions) *RefreshWorker`, `(*RefreshWorker).Run(ctx) error`.

Spec §5.2, in full, because every clause is a test below:

> A background worker refreshes tokens ahead of expiry **with jitter** so a
> fleet does not refresh simultaneously. A **per-account mutex** means concurrent
> requests finding an expired token wait for one refresh rather than each
> starting their own; **the credential probe shares that mutex**, since a probe
> that consumes a refresh would otherwise race the worker.
>
> **Many vendors rotate the refresh token on every refresh**, some invalidating
> the old one immediately. The new pair is therefore **persisted before the old
> is considered replaced** — a crash between refresh and persist would otherwise
> brick the account until manual reconnection.

| Failure | Handling |
|---|---|
| `invalid_grant` or an equivalent terminal refusal | Disable the credential pending reconnection. **No retries** — hammering a refused refresh endpoint is how an account gets locked rather than recovered. |
| 5xx, timeout, network error from the token endpoint | Transient. Back off on the standard ladder and retry. |

**Darkrouter is single-instance by design.** Two instances sharing one account
would trip rotation-reuse detection, which some vendors treat as theft and
respond to by revoking the grant entirely. Nothing here tries to make
multi-instance work; the mutex is per-process and that is the honest scope.

- [ ] **Step 1: Write the failing test**

Create `internal/auth/oauth_test.go`:

```go
package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// authServer is a fake token endpoint that rotates its refresh token on every
// call, which is the behavior spec §5.2 says many vendors have.
type authServer struct {
	mu        sync.Mutex
	refreshes int
	grants    map[string]bool // refresh tokens it will still accept
	status    int
	errBody   string
	expiresIn int
}

func newAuthServer(t *testing.T) (*authServer, *httptest.Server) {
	t.Helper()
	a := &authServer{grants: map[string]bool{"rt-0": true}, expiresIn: 3600}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		a.mu.Lock()
		defer a.mu.Unlock()

		if a.status != 0 && a.status != http.StatusOK {
			w.WriteHeader(a.status)
			_, _ = w.Write([]byte(a.errBody))
			return
		}
		if r.Form.Get("grant_type") == "refresh_token" {
			old := r.Form.Get("refresh_token")
			if !a.grants[old] {
				// Rotation-reuse detection: the vendor treats this as theft.
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			a.refreshes++
			delete(a.grants, old)
			next := fmt.Sprintf("rt-%d", a.refreshes)
			a.grants[next] = true
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"access_token":"at-%d","refresh_token":%q,"token_type":"Bearer","expires_in":%d}`,
				a.refreshes, next, a.expiresIn)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"at-0","refresh_token":"rt-0","token_type":"Bearer","expires_in":%d}`,
			a.expiresIn)
	}))
	t.Cleanup(srv.Close)
	return a, srv
}

func (a *authServer) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refreshes
}

// memTokens is an in-memory TokenStore recording every persist.
type memTokens struct {
	mu       sync.Mutex
	secrets  map[string]string
	writes   int
	disabled map[string]string
}

func newMemTokens() *memTokens {
	return &memTokens{secrets: map[string]string{}, disabled: map[string]string{}}
}

func (m *memTokens) ReplaceCredentialSecret(_ context.Context, id, secret string, _ *int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[id] = secret
	m.writes++
	return nil
}

func (m *memTokens) DisableCredential(_ context.Context, id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disabled[id] = reason
	return nil
}

func (m *memTokens) stored(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.secrets[id]
}

type fixedPresets struct{ cfg OAuthConfig }

func (f fixedPresets) OAuthFor(string) (OAuthConfig, bool) { return f.cfg, true }

func oauthManager(t *testing.T, srv *httptest.Server, tokens *memTokens) *Manager {
	t.Helper()
	return NewManager(Deps{
		Tokens: tokens,
		OAuth:  fixedPresets{cfg: OAuthConfig{TokenURL: srv.URL, ClientID: "client"}},
		HTTP:   srv.Client(),
	})
}

func expiring(t *testing.T, in time.Duration) string {
	t.Helper()
	tok := Token{AccessToken: "at-0", RefreshToken: "rt-0", ExpiresAt: time.Now().Add(in)}
	raw, err := tok.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func oauthAz(t *testing.T, m *Manager, secret string) Authorizer {
	t.Helper()
	az, err := m.For(context.Background(),
		Target{ProviderID: "sub", Style: StyleOAuth, Preset: "anthropic-oauth"},
		Credential{ID: "cred-1", Kind: "oauth", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	return az
}

func TestAValidTokenIsUsedWithoutRefreshing(t *testing.T) {
	a, srv := newAuthServer(t)
	tokens := newMemTokens()
	az := oauthAz(t, oauthManager(t, srv, tokens), expiring(t, time.Hour))

	r, _ := http.NewRequest("POST", "https://api.invalid/x", nil)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer at-0" {
		t.Errorf("Authorization = %q", got)
	}
	if a.count() != 0 {
		t.Errorf("refreshed %d times for a token valid for an hour", a.count())
	}
}

func TestRotationIsPersistedBeforeTheOldPairIsDropped(t *testing.T) {
	// Spec §5.2's crash window. If the new pair is not durable before the old
	// one stops being valid, a crash here bricks the account.
	a, srv := newAuthServer(t)
	tokens := newMemTokens()
	az := oauthAz(t, oauthManager(t, srv, tokens), expiring(t, -time.Minute))

	r, _ := http.NewRequest("POST", "https://api.invalid/x", nil)
	if err := az(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if a.count() != 1 {
		t.Fatalf("refreshed %d times, want 1", a.count())
	}
	stored, err := ParseToken([]byte(tokens.stored("cred-1")))
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefreshToken != "rt-1" {
		t.Errorf("stored refresh token = %q, want the rotated one", stored.RefreshToken)
	}
	if stored.AccessToken != "at-1" {
		t.Errorf("stored access token = %q", stored.AccessToken)
	}
	if tokens.writes != 1 {
		t.Errorf("persisted %d times, want exactly 1", tokens.writes)
	}
}

func TestConcurrentRequestsTriggerOneRefresh(t *testing.T) {
	// Without the per-account mutex, twenty concurrent requests start twenty
	// refreshes; nineteen of them present the rotated-away token and the
	// vendor sees rotation reuse, which some treat as theft.
	a, srv := newAuthServer(t)
	az := oauthAz(t, oauthManager(t, srv, newMemTokens()), expiring(t, -time.Minute))

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _ := http.NewRequest("POST", "https://api.invalid/x", nil)
			errs <- az(context.Background(), r)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if a.count() != 1 {
		t.Errorf("refreshed %d times under concurrency, want 1", a.count())
	}
}

func TestInvalidGrantDisablesWithoutRetrying(t *testing.T) {
	// Hammering a refused refresh endpoint is how an account gets locked
	// rather than recovered.
	a, srv := newAuthServer(t)
	a.status, a.errBody = http.StatusBadRequest, `{"error":"invalid_grant"}`
	tokens := newMemTokens()
	az := oauthAz(t, oauthManager(t, srv, tokens), expiring(t, -time.Minute))

	for i := 0; i < 3; i++ {
		r, _ := http.NewRequest("POST", "https://api.invalid/x", nil)
		if err := az(context.Background(), r); err == nil {
			t.Fatal("a refused refresh must be an error")
		}
	}
	tokens.mu.Lock()
	reason, disabled := tokens.disabled["cred-1"]
	tokens.mu.Unlock()
	if !disabled {
		t.Fatal("an invalid_grant must disable the credential pending reconnection")
	}
	if !strings.Contains(strings.ToLower(reason), "reconnect") {
		t.Errorf("the reason must tell the operator what to do, got %q", reason)
	}
}

func TestATransientFailureDoesNotDisable(t *testing.T) {
	// A 500 from the token endpoint is the vendor's problem, not the account's.
	// Disabling here turns a five-minute outage into a manual reconnection.
	a, srv := newAuthServer(t)
	a.status, a.errBody = http.StatusInternalServerError, `{"error":"server_error"}`
	tokens := newMemTokens()
	az := oauthAz(t, oauthManager(t, srv, tokens), expiring(t, -time.Minute))

	r, _ := http.NewRequest("POST", "https://api.invalid/x", nil)
	if err := az(context.Background(), r); err == nil {
		t.Fatal("a failed refresh must be an error")
	}
	tokens.mu.Lock()
	_, disabled := tokens.disabled["cred-1"]
	tokens.mu.Unlock()
	if disabled {
		t.Error("a 5xx from the token endpoint must not disable the credential")
	}
}

func TestNoTokenReachesAnErrorString(t *testing.T) {
	a, srv := newAuthServer(t)
	a.status, a.errBody = http.StatusBadRequest, `{"error":"invalid_grant"}`
	az := oauthAz(t, oauthManager(t, srv, newMemTokens()), expiring(t, -time.Minute))

	r, _ := http.NewRequest("POST", "https://api.invalid/x", nil)
	err := az(context.Background(), r)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, secret := range []string{"rt-0", "at-0"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error carries %q: %v", secret, err)
		}
	}
}

func TestRefreshJitterStaysInsideItsWindow(t *testing.T) {
	// Spec §5.2 wants jitter so a fleet does not refresh simultaneously. The
	// property that matters is bounded, not the distribution.
	base := 10 * time.Minute
	for i := 0; i < 200; i++ {
		d := jitter(base)
		if d < base/2 || d > base+base/2 {
			t.Fatalf("jitter(%v) = %v, outside the window", base, d)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails, then implement**

Create `internal/auth/oauth.go` with the exchange, the per-account state, and
the two failure modes:

```go
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OAuthConfig is the preset's OAuth block, restated here so internal/auth does
// not import catalog. The admin package translates.
type OAuthConfig struct {
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	Scopes       []string
}

// OAuthPresets resolves a preset id to its OAuth endpoints.
type OAuthPresets interface {
	OAuthFor(preset string) (OAuthConfig, bool)
}

// TokenStore persists a refreshed credential. It is the narrow half of *store.DB
// this package needs, which keeps auth from importing store.
type TokenStore interface {
	ReplaceCredentialSecret(ctx context.Context, id, secret string, expiresAt *int64) error
	DisableCredential(ctx context.Context, id, reason string) error
}

// ErrNeedsReconnect marks a terminal refusal. The credential is disabled and
// no retry follows: hammering a refused refresh endpoint is how an account gets
// locked rather than recovered.
var ErrNeedsReconnect = errors.New("this account must be reconnected")

const reconnectReason = "reconnect required: the provider refused the refresh"

type ExchangeInput struct {
	Code        string
	Verifier    string
	RedirectURI string
}

// ExchangeCode trades an authorization code for a token pair.
func ExchangeCode(ctx context.Context, c *http.Client, cfg OAuthConfig, in ExchangeInput) (Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {in.Code},
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {in.RedirectURI},
		"code_verifier": {in.Verifier},
	}
	return postToken(ctx, c, cfg.TokenURL, form)
}

func refreshToken(ctx context.Context, c *http.Client, cfg OAuthConfig, refresh string) (Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {cfg.ClientID},
	}
	return postToken(ctx, c, cfg.TokenURL, form)
}

type wireToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Account      struct {
		EmailAddress string `json:"email_address"`
	} `json:"account"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func postToken(ctx context.Context, c *http.Client, tokenURL string, form url.Values) (Token, error) {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		// Network error: transient by definition, so no terminal marker.
		return Token{}, fmt.Errorf("token endpoint unreachable: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, err
	}

	var w wireToken
	// A body that is not JSON is still a failure worth reporting by status.
	_ = json.Unmarshal(raw, &w)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if terminal(resp.StatusCode, w.Error) {
			return Token{}, fmt.Errorf("%w: %s", ErrNeedsReconnect, describe(w))
		}
		return Token{}, fmt.Errorf("token endpoint returned %s: %s", resp.Status, describe(w))
	}
	if w.AccessToken == "" {
		return Token{}, fmt.Errorf("%w: the token endpoint returned no access token", ErrNeedsReconnect)
	}

	tok := Token{
		AccessToken:  w.AccessToken,
		RefreshToken: w.RefreshToken,
		TokenType:    w.TokenType,
		Account:      w.Account.EmailAddress,
	}
	if w.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(w.ExpiresIn) * time.Second)
	}
	if w.Scope != "" {
		tok.Scopes = strings.Fields(w.Scope)
	}
	return tok, nil
}

// terminal separates the two failure modes spec §5.2 distinguishes. Getting
// this wrong is bad in both directions: a transient 500 treated as terminal
// turns a five-minute outage into a manual reconnection, and a terminal
// refusal treated as transient hammers the endpoint until the account locks.
func terminal(status int, code string) bool {
	switch code {
	case "invalid_grant", "invalid_client", "unauthorized_client", "invalid_scope":
		return true
	}
	// A bare 400 or 401 with no recognizable code is still a refusal of this
	// credential rather than an outage.
	return status == http.StatusBadRequest || status == http.StatusUnauthorized
}

// describe renders the provider's own words without any token material: the
// error code and description are the useful half of the body.
func describe(w wireToken) string {
	switch {
	case w.Error != "" && w.ErrorDescription != "":
		return w.Error + ": " + w.ErrorDescription
	case w.Error != "":
		return w.Error
	}
	return "no error detail"
}

// oauthAccount is one credential's refresh state. The mutex is what makes
// concurrent requests wait for one refresh rather than each starting their own
// — and the credential probe takes the same mutex, spec §5.2, so a probe cannot
// consume a refresh out from under the worker.
type oauthAccount struct {
	mu  sync.Mutex
	tok Token
	// dead marks a credential whose refresh was terminally refused. Checked
	// before the endpoint is called again, so "no retries" is enforced within
	// the process as well as across ticks.
	dead bool
}

func (m *Manager) oauthFor(ctx context.Context, t Target, c Credential) (Authorizer, error) {
	if m.deps.OAuth == nil {
		return nil, fmt.Errorf("provider %q uses oauth but no preset data is available", t.ProviderID)
	}
	cfg, ok := m.deps.OAuth.OAuthFor(t.Preset)
	if !ok || cfg.TokenURL == "" {
		return nil, fmt.Errorf("preset %q declares no oauth endpoints", t.Preset)
	}
	tok, err := ParseToken([]byte(c.Secret))
	if err != nil {
		return nil, fmt.Errorf("credential %s: %w", c.ID, err)
	}

	m.mu.Lock()
	acct, ok := m.oauth[c.ID]
	if !ok {
		acct = &oauthAccount{tok: tok}
		m.oauth[c.ID] = acct
	}
	m.mu.Unlock()

	return func(ctx context.Context, r *http.Request) error {
		header, err := m.accessToken(ctx, acct, cfg, c.ID)
		if err != nil {
			return err
		}
		r.Header.Set("Authorization", header)
		return nil
	}, nil
}

// accessToken returns a usable Authorization value, refreshing under the
// account mutex when the stored one is inside its expiry delta.
func (m *Manager) accessToken(ctx context.Context, acct *oauthAccount,
	cfg OAuthConfig, credID string) (string, error) {

	acct.mu.Lock()
	defer acct.mu.Unlock()

	if acct.dead {
		return "", fmt.Errorf("%w: credential %s", ErrNeedsReconnect, credID)
	}
	if !acct.tok.Expired(time.Now(), DefaultRefreshDelta) && acct.tok.AccessToken != "" {
		return acct.tok.Header(), nil
	}
	if acct.tok.RefreshToken == "" {
		acct.dead = true
		m.disable(ctx, credID)
		return "", fmt.Errorf("%w: credential %s has no refresh token", ErrNeedsReconnect, credID)
	}

	next, err := refreshToken(ctx, m.deps.HTTP, cfg, acct.tok.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrNeedsReconnect) {
			acct.dead = true
			m.disable(ctx, credID)
		}
		// Transient failures leave the stored pair alone: the old refresh
		// token is still the only one that exists.
		return "", err
	}
	// Many vendors rotate on every refresh and some invalidate the old token
	// immediately, so a response with no new refresh token means "keep using
	// the one you have" rather than "you now have none".
	if next.RefreshToken == "" {
		next.RefreshToken = acct.tok.RefreshToken
	}

	// Persisted BEFORE the in-memory pair is replaced. A crash between the two
	// then loses a refresh rather than the account: the durable row already
	// names the token the vendor now expects.
	if err := m.persist(ctx, credID, next); err != nil {
		return "", err
	}
	acct.tok = next
	return next.Header(), nil
}

func (m *Manager) persist(ctx context.Context, credID string, tok Token) error {
	if m.deps.Tokens == nil {
		return nil
	}
	raw, err := tok.Marshal()
	if err != nil {
		return err
	}
	// WithoutCancel: this runs on a request's context, and a client that hangs
	// up mid-refresh must not leave the rotated pair unpersisted.
	if err := m.deps.Tokens.ReplaceCredentialSecret(
		context.WithoutCancel(ctx), credID, string(raw), tok.Unix()); err != nil {
		return fmt.Errorf("persist refreshed credential: %w", err)
	}
	return nil
}

func (m *Manager) disable(ctx context.Context, credID string) {
	if m.deps.Tokens == nil {
		return
	}
	_ = m.deps.Tokens.DisableCredential(context.WithoutCancel(ctx), credID, reconnectReason)
}
```

Create `internal/auth/refresh.go` with the worker and `jitter`:

```go
package auth

import (
	"context"
	"log"
	"math/rand/v2"
	"time"
)

// RefreshOptions configures the background worker.
type RefreshOptions struct {
	// Interval is how often the worker looks for expiring credentials, before
	// jitter.
	Interval time.Duration
	// Ahead is how far into the future a credential counts as expiring. Wider
	// than the per-request delta so the worker normally wins the race and no
	// request ever pays for a refresh.
	Ahead time.Duration
}

// jitter spreads a duration over half to one and a half times its base.
//
// Spec §5.2: without it, a fleet of accounts connected in the same session
// refreshes in the same second, every time, forever.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d/2 + time.Duration(rand.Int64N(int64(d)))
}
```

The worker's `Run` loop reads `ExpiringCredentials(ctx, key, "oauth", now+Ahead)`,
resolves each through `Manager.For`, and calls the resulting authorizer against a
throwaway `*http.Request` — which drives exactly the same refresh path a real
request would, under the same mutex, rather than a parallel implementation that
could drift. **That is the point**: one refresh path, exercised two ways.

Wire it in `internal/server/server.go` with `startWorker("token refresh",
refresher.Run)`, and populate `auth.Deps` with the real `TokenStore` (a thin
adapter over `*store.DB` binding the keyring) and an `OAuthPresets` translating
`catalog.Preset.OAuth`.

- [ ] **Step 3: Run the tests, verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/auth/ -count=1 -race -v 2>&1 | tail -25
go test ./... -count=1 -race && go vet ./... && gofmt -l .
git add internal/auth internal/server
git commit -m "feat(oauth): refresh tokens safely under rotation"
```

---

### Task 15: The credential probe covers all three strategies

**Files:**
- Modify: `internal/admin/probe.go`
- Test: `internal/admin/probe_strategies_test.go`

**Interfaces:**
- Consumes: `bedrock.NewLister`, `auth.Manager`, `vertex.EndpointFor`.
- Produces: `(*Server).probeSigV4`, `(*Server).probeGCP`, `(*Server).probeOAuth`.

Spec §6: "`POST /api/providers/:id/test` extends to all three strategies,
reporting **what specifically failed** — signature, permission, expiry, or
reachability — because 'it doesn't work' is not actionable for any of them."

- **SigV4** — a real `ListFoundationModels` call, which also exercises region and endpoint configuration.
- **Vertex** — a token exchange followed by a single-token generation against one catalogued model, since no listing exists.
- **OAuth** — a token refresh under the per-account mutex.

**The OAuth probe goes through `Manager.For` and the returned authorizer**, not
through a private refresh call. Spec §5.2: "the credential probe shares that
mutex, since a probe that consumes a refresh would otherwise race the worker."
Sharing the mutex is automatic if the probe uses the same path a request does,
and impossible to guarantee if it does not.

**A successful probe resets the ladder for the probed credential**, per phase 7
§4.3. Task 14 of phase 7 already does this and fixed a bug in it; do not
reimplement, just make sure the new branches reach the same `clearCooldowns`.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/probe_strategies_test.go`:

```go
package admin

import (
	"net/http"
	"strings"
	"testing"
)

func TestSigV4ProbeReportsTheModelCount(t *testing.T) {
	// The same signal an openaicompat probe gives: a number the operator can
	// sanity-check against what they expect the account to have.
	s, jar, aws := serverWithFakeAWS(t)
	id := bedrockProvider(t, s, aws.URL)

	var got struct {
		OK         bool   `json:"ok"`
		Probe      string `json:"probe"`
		ModelCount int    `json:"model_count"`
		Error      string `json:"error"`
	}
	post(t, s, jar, "/api/providers/"+id+"/test", "", &got)
	if !got.OK {
		t.Fatalf("probe failed: %s", got.Error)
	}
	if got.ModelCount == 0 {
		t.Error("model_count = 0")
	}
	if got.Probe != "listing" {
		t.Errorf("probe = %q", got.Probe)
	}
}

func TestSigV4ProbeNamesAPermissionFailure(t *testing.T) {
	// "It doesn't work" is not actionable. A 403 from the control plane means
	// the key is valid and the policy is not, which is a different fix from a
	// wrong region.
	s, jar, aws := serverWithFakeAWS(t)
	aws.status = http.StatusForbidden
	id := bedrockProvider(t, s, aws.URL)

	var got struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	post(t, s, jar, "/api/providers/"+id+"/test", "", &got)
	if got.OK {
		t.Fatal("a 403 must not report success")
	}
	if !strings.Contains(strings.ToLower(got.Error), "permission") &&
		!strings.Contains(got.Error, "403") {
		t.Errorf("the error does not say what failed: %q", got.Error)
	}
}

func TestVertexProbeExchangesATokenAndGenerates(t *testing.T) {
	// No listing exists, spec §4.3, so the probe is a token exchange plus one
	// single-token generation against a catalogued model.
	s, jar, gcp := serverWithFakeGCP(t)
	id := vertexProvider(t, s, gcp)

	var got struct {
		OK    bool   `json:"ok"`
		Probe string `json:"probe"`
		Error string `json:"error"`
	}
	post(t, s, jar, "/api/providers/"+id+"/test", "", &got)
	if !got.OK {
		t.Fatalf("probe failed: %s", got.Error)
	}
	if got.Probe != "completion" {
		t.Errorf("probe = %q, want completion", got.Probe)
	}
	if gcp.tokenCalls == 0 {
		t.Error("no token was exchanged")
	}
	if gcp.generateCalls == 0 {
		t.Error("no generation was attempted")
	}
}

func TestVertexProbeSaysWhenThereIsNothingToProbe(t *testing.T) {
	// A vertex provider whose catalog has not been seeded yet. Reporting a
	// generic failure would send the operator looking at credentials.
	s, jar, gcp := serverWithFakeGCP(t)
	id := vertexProviderWithNoModels(t, s, gcp)

	var got struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	post(t, s, jar, "/api/providers/"+id+"/test", "", &got)
	if got.OK {
		t.Fatal("a provider with no catalogued model cannot be probed")
	}
	if !strings.Contains(got.Error, "model") {
		t.Errorf("the error should name the cause: %q", got.Error)
	}
}

func TestOAuthProbeRefreshes(t *testing.T) {
	s, jar, authsrv := serverWithFakeAuthServer(t)
	id := oauthProviderWithCredential(t, s, authsrv)

	var got struct {
		OK    bool   `json:"ok"`
		Probe string `json:"probe"`
		Error string `json:"error"`
	}
	post(t, s, jar, "/api/providers/"+id+"/test", "", &got)
	if !got.OK {
		t.Fatalf("probe failed: %s", got.Error)
	}
	if got.Probe != "refresh" {
		t.Errorf("probe = %q, want refresh", got.Probe)
	}
}

func TestOAuthProbeReportsAnExpiredGrant(t *testing.T) {
	s, jar, authsrv := serverWithFakeAuthServer(t)
	authsrv.status, authsrv.errBody = http.StatusBadRequest, `{"error":"invalid_grant"}`
	id := oauthProviderWithCredential(t, s, authsrv)

	var got struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	post(t, s, jar, "/api/providers/"+id+"/test", "", &got)
	if got.OK {
		t.Fatal("a refused refresh must not report success")
	}
	if !strings.Contains(strings.ToLower(got.Error), "reconnect") {
		t.Errorf("the operator must be told to reconnect: %q", got.Error)
	}
}

func TestNoProbeResponseCarriesCredentialMaterial(t *testing.T) {
	s, jar, authsrv := serverWithFakeAuthServer(t)
	id := oauthProviderWithCredential(t, s, authsrv)
	raw, _ := postRaw(t, s, jar, "/api/providers/"+id+"/test", "")
	for _, secret := range []string{"rt-0", "rt-1", "at-0", "at-1"} {
		if strings.Contains(raw, secret) {
			t.Errorf("the probe response carries %q:\n%s", secret, raw)
		}
	}
}
```

The three `serverWithFake*` helpers and the provider builders follow the pattern
`internal/admin/probe_test.go` already uses for the openaicompat probe. **Read
that file first**; most of what these need already exists there.

- [ ] **Step 2: Implement**

In `internal/admin/probe.go`, branch `runProbe` on the resolved auth style
before the existing listing path:

```go
	switch style {
	case auth.StyleSigV4:
		return s.probeSigV4(ctx, p, cred)
	case auth.StyleGCPSA:
		return s.probeGCP(ctx, p, cred)
	case auth.StyleOAuth:
		return s.probeOAuth(ctx, p, cred)
	}
	// ... the existing listing probe, unchanged
```

Each branch reports what specifically failed:

```go
// probeSigV4 makes a real ListFoundationModels call, which exercises the
// signature, the region and the endpoint in one request. A signature mistake, a
// wrong region and a missing IAM permission all produce different statuses, and
// naming which one arrived is the difference between a fix and a guess.
func (s *Server) probeSigV4(ctx context.Context, p provider.Provider, cred provider.Credential) probeResult {
	az, err := s.deps.Auth.For(ctx, authTargetFor(p), authCredentialFor(cred))
	if err != nil {
		return failed("signature", err)
	}
	start := time.Now()
	models, err := bedrock.NewLister(nil).List(ctx, catalog.Probe{
		ProviderID: p.ID, Kind: p.Kind, Region: p.Region, Authorize: az,
	})
	if err != nil {
		return failed(classifyAWSProbe(err), err)
	}
	s.clearCooldowns(p.ID, cred.ID)
	return probeResult{
		OK: true, Probe: "listing", ModelCount: len(models),
		LatencyMs: time.Since(start).Milliseconds(),
	}
}
```

`classifyAWSProbe` maps the status embedded in the lister's error text to one of
`signature`, `permission`, `region` or `reachability`. **A 403 is `permission`,
not `signature`**: the signature validated, which is exactly the distinction the
spec asks for.

`probeGCP` exchanges a token, picks one catalogued model, and sends a
one-token generation — reporting `expiry` when the token exchange fails and
`reachability` when the generation does. `probeOAuth` resolves through
`Manager.For` and calls the returned authorizer against a throwaway request,
which refreshes under the per-account mutex; an `auth.ErrNeedsReconnect` becomes
an error naming reconnection.

- [ ] **Step 3: Run the tests, verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -count=1 2>&1 | tail -15
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/admin
git commit -m "feat(admin): probe signed and oauth credentials"
```

---

### Task 16: Golden files for both new kinds

**Files:**
- Modify: `internal/golden/golden_test.go`
- Create: `internal/golden/testdata/golden/*/rendered/bedrock`, `.../vertex-google`, `.../vertex-anthropic`

**Interfaces:**
- Consumes: `bedrock.New`, `vertex.New`.

Master design §15 requires the golden suite to cover every adapter kind. Spec
§7: "This phase adds `bedrock` and `vertex` rendered outputs — **both publisher
variants** — to phase 4's suite."

**Vertex appears twice under one kind.** The harness keys rendered files by the
adapter's name, and `vertex` renders two different payloads depending on the
target. Register two entries pointing at the same adapter with different targets
rather than one that silently covers half the behavior.

- [ ] **Step 1: Read the harness before changing it**

```bash
export PATH=$PATH:/usr/local/go/bin
sed -n '1,120p' internal/golden/golden_test.go
ls internal/golden/testdata/golden/
```

The `adapters()` map keys the rendered file's base name. Note how the harness
supplies an `adapter.Target` — bedrock needs `Region`, vertex needs `Project`,
`Location` and `Publisher`, and if the harness hands every adapter one fixed
target it needs a per-adapter one first.

- [ ] **Step 2: Register the three renderers**

```go
func adapters() map[string]adapter.Adapter {
	return map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		"gemini":       geminiadapter.NewWithFetcher(&offlineFetcher),
		"bedrock":      bedrockadapter.New(),
		// Two entries, one adapter. vertex renders two different payloads
		// depending on the target's publisher, and one entry would cover half
		// the behavior while looking complete.
		"vertex-google":    vertexadapter.New(),
		"vertex-anthropic": vertexadapter.New(),
	}
}

// targetFor supplies the endpoint properties a kind needs. Everything before
// this phase took a bare base URL.
func targetFor(name string) *adapter.Target {
	switch name {
	case "bedrock":
		return &adapter.Target{Region: "us-east-1", Model: goldenModel}
	case "vertex-google":
		return &adapter.Target{
			Project: "proj", Location: "us-central1",
			Publisher: vertexadapter.PublisherGoogle, Model: goldenModel,
		}
	case "vertex-anthropic":
		return &adapter.Target{
			Project: "proj", Location: "us-central1",
			Publisher: vertexadapter.PublisherAnthropic, Model: goldenModel,
		}
	}
	return &adapter.Target{BaseURL: "https://upstream.invalid/v1", Model: goldenModel}
}
```

- [ ] **Step 3: Generate and read the files**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/golden/ -update
git status --porcelain internal/golden/testdata | head -40
```

**Read every generated file before committing it.** A golden file records what
the code does, so `-update` makes any bug permanent and invisible. Check at
minimum:

- every `vertex-anthropic/rendered` carries `anthropic_version` and **no** `model` key;
- every `vertex-google/rendered` carries `contents` and **no** `anthropic_version`;
- `bedrock/rendered` carries `messages` with `content` arrays of `{"text": …}`, and no `system` turn inside `messages`;
- the tool fixtures produce `toolConfig` with `inputSchema.json`;
- the warnings files name `top_k` for bedrock, which has no Converse spelling.

```bash
cat internal/golden/testdata/golden/anthropic/*/rendered/vertex-anthropic | head -40
cat internal/golden/testdata/golden/openai/*/warnings/bedrock | head -20
```

- [ ] **Step 4: Confirm the suite is real**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/golden/ -count=1
```

Then prove a diff fails: change one character in a rendered file, re-run, and
confirm the test reports it. Restore it afterwards.

- [ ] **Step 5: Commit**

```bash
git add internal/golden
git commit -m "test(golden): cover bedrock and both vertex routes"
```

---

### Task 17: Connecting an account from the dashboard

**Files:**
- Modify: `web/src/routes/settings.tsx`, `web/src/routes/overview.tsx`, `web/src/lib/api.ts`
- Test: `web/src/routes/settings.test.tsx`

**Interfaces:**
- Consumes: `POST /api/providers/{id}/oauth/start`, `POST /api/providers/{id}/oauth/complete`.

Spec §5.3: "The overview surfaces a disabled-pending-reconnect credential
prominently, since only the operator can resolve it." Phase 7's overview already
renders `needs_reauth` — confirm before adding anything:

```bash
grep -n 'needs_reauth' web/src/routes/overview.tsx internal/admin/usage.go
```

What is missing is the way out of that state. Today the settings screen can add
a static key and nothing else, so an operator seeing "needs reconnection" has no
button to press.

**The paste box is always shown, even on the localhost path.** Spec §5.1 makes
paste the one that always works; the listener is an optimisation. A UI that hid
the paste box whenever a preset declared a localhost redirect would strand every
operator whose Darkrouter runs on a different machine from their browser — which
is the normal homelab case.

- [ ] **Step 1: Write the failing test**

Add to `web/src/routes/settings.test.tsx`:

```tsx
it("offers a connect button for an oauth provider", async () => {
  // A static key form is useless here: there is no key to type.
  renderSettings({
    providers: [{
      id: "sub", name: "Claude subscription", preset: "anthropic-oauth",
      kind: "anthropic", base_url: "https://api.anthropic.com/v1",
      auth_style: "oauth", priority: 0, enabled: true, credentials: [],
    }],
  })
  expect(await screen.findByRole("button", { name: /connect/i })).toBeInTheDocument()
  expect(screen.queryByPlaceholderText("secret")).not.toBeInTheDocument()
})

it("shows the authorize link and a paste box after starting", async () => {
  const user = userEvent.setup()
  renderSettings({ providers: [oauthProvider], startResponse: {
    authorize_url: "https://claude.ai/oauth/authorize?state=abc",
    state: "abc", redirect_uri: "http://localhost:54545/callback", style: "localhost",
  }})
  await user.click(await screen.findByRole("button", { name: /connect/i }))

  const link = await screen.findByRole("link", { name: /authorize/i })
  expect(link).toHaveAttribute("href", expect.stringContaining("claude.ai/oauth/authorize"))
  // Always shown, even on the localhost path: the listener only works when
  // Darkrouter and the browser are on the same machine.
  expect(await screen.findByPlaceholderText(/paste/i)).toBeInTheDocument()
})

it("says an account needs reconnection rather than just disabling it", async () => {
  renderSettings({
    providers: [{ ...oauthProvider, credentials: [{
      id: "c1", label: "personal", masked: "…", enabled: false, cooling: false,
    }]}],
  })
  expect(await screen.findByText(/needs reconnection/i)).toBeInTheDocument()
  // And the way out is on screen, not in a manual.
  expect(await screen.findByRole("button", { name: /reconnect/i })).toBeInTheDocument()
})
```

`renderSettings` centralises the fetch stub the existing settings test writes
inline. **Refactor the existing test to use it** rather than keeping two stubs.

- [ ] **Step 2: Implement**

In `web/src/routes/settings.tsx`, branch the credential form on the provider's
`auth_style`. The static form stays exactly as it is; the OAuth one is a
Connect button, an authorize link, and a paste box:

```tsx
{p.auth_style === "oauth" ? (
  <ConnectAccount provider={p} onConnected={invalidate} onError={setError} />
) : (
  <AddCredential providerID={p.id} onAdded={invalidate} onError={setError} />
)}
```

`ConnectAccount` calls `/oauth/start`, renders the returned `authorize_url` as a
real link with `target="_blank"` and `rel="noreferrer"`, and posts the pasted URL
to `/oauth/complete`. When the response carries `listener_error`, say so
plainly — the operator needs to know the paste box is now the only path.

Add `auth_style` to the `Provider` type, and confirm the API sends it:

```bash
grep -n 'auth_style' internal/admin/providers.go
```

For the reconnection state, extend the existing credential row: a credential
that is `!enabled` on an OAuth provider reads "needs reconnection" with a
Reconnect button that starts the same flow.

- [ ] **Step 3: Run the frontend suite, build, verify the tree, commit**

```bash
npm --prefix web test
npm --prefix web run build
export PATH=$PATH:/usr/local/go/bin
go test ./... -count=1 && go vet ./... && gofmt -l .
git add web internal/admin
git commit -m "feat(web): connect and reconnect oauth accounts"
```

---

### Task 18: No credential material anywhere

**Files:**
- Create: `internal/admin/leak_test.go`
- Modify: whatever the test finds

**Interfaces:**
- Consumes: everything.

Spec §7: "**Storage** tests assert service-account JSON and refresh tokens
round-trip and appear in no admin API response." Spec §8: "No credential
material appears in any admin API response **or access log**."

Phase 7 has a sweep over the endpoints that existed then. This widens it to the
three new credential shapes, which are the ones that can leak in new ways: a
service-account document is a multi-line JSON blob that a naive `masked` helper
would show whole, and a refresh token is a long opaque string that looks exactly
like an id.

**This task is allowed to find nothing.** If every endpoint is already clean the
deliverable is the test, and that is worth the commit on its own — the property
is currently true by construction and nothing stops the next handler from
breaking it.

- [ ] **Step 1: Write the sweep**

Create `internal/admin/leak_test.go`:

```go
package admin

import (
	"strings"
	"testing"
)

// canaries are values that must never appear in any response or log line. Each
// is planted in a credential of a different kind, because they fail in
// different ways: a bare key is short and looks like an id, a service-account
// document is multi-line JSON, and a refresh token is long and opaque.
var canaries = map[string]string{
	"static key":            "sk-CANARY-STATIC-0001",
	"aws secret":            "CANARY/AWS/SECRET/0002",
	"service-account key":   "CANARY-PRIVATE-KEY-0003",
	"oauth refresh token":   "CANARY-REFRESH-0004",
	"oauth access token":    "CANARY-ACCESS-0005",
}

// everyReadEndpoint is every GET the dashboard makes, plus the two probe and
// trace paths that render provider detail.
func everyReadEndpoint(providerID, requestID string) []string {
	return []string{
		"/api/overview", "/api/providers", "/api/presets", "/api/models",
		"/api/requests", "/api/requests/" + requestID,
		"/api/usage", "/api/config", "/api/auth/status",
	}
}

func TestNoCredentialMaterialInAnyResponse(t *testing.T) {
	s, jar, ids := serverWithEveryCredentialKind(t)

	for _, path := range everyReadEndpoint(ids.providerID, ids.requestID) {
		raw := rawBody(t, s, jar, "GET", path, "")
		for name, canary := range canaries {
			if strings.Contains(raw, canary) {
				t.Errorf("%s leaked the %s", path, name)
			}
		}
	}
}

func TestNoCredentialMaterialInAProbeResponse(t *testing.T) {
	// The probe talks to the credential, so its error paths are the likeliest
	// place for one to be echoed back inside a provider's message.
	s, jar, ids := serverWithEveryCredentialKind(t)
	for _, id := range ids.allProviders {
		raw, _ := postRaw(t, s, jar, "/api/providers/"+id+"/test", "")
		for name, canary := range canaries {
			if strings.Contains(raw, canary) {
				t.Errorf("the probe on %s leaked the %s:\n%s", id, name, raw)
			}
		}
	}
}

func TestNoCredentialMaterialInALogLine(t *testing.T) {
	// Spec §8 names the access log alongside the API. There is none today, so
	// this is what stops one being added that logs a signed URL or a callback.
	s, jar, ids := serverWithEveryCredentialKind(t)
	var logged strings.Builder
	restore := captureLog(t, &logged)
	defer restore()

	for _, path := range everyReadEndpoint(ids.providerID, ids.requestID) {
		_ = rawBody(t, s, jar, "GET", path, "")
	}
	for _, id := range ids.allProviders {
		_, _ = postRaw(t, s, jar, "/api/providers/"+id+"/test", "")
	}
	for name, canary := range canaries {
		if strings.Contains(logged.String(), canary) {
			t.Errorf("a log line carries the %s:\n%s", name, logged.String())
		}
	}
}

func TestAServiceAccountDocumentIsNeverPartiallyShown(t *testing.T) {
	// A masked helper written for a 40-character key shows the last four
	// characters. On a multi-line JSON document that is harmless; on one whose
	// last field happens to be the key it is not. The masked value must be
	// derived from the credential's identity, not its content.
	s, jar, ids := serverWithEveryCredentialKind(t)
	raw := rawBody(t, s, jar, "GET", "/api/providers", "")
	for _, fragment := range []string{"BEGIN PRIVATE KEY", "private_key", "-----"} {
		if strings.Contains(raw, fragment) {
			t.Errorf("a service-account document fragment %q is in the response", fragment)
		}
	}
	_ = ids
}
```

`serverWithEveryCredentialKind` builds one provider per strategy, each carrying
a credential containing its canary, plus one logged request so
`/api/requests/{id}` has something to render.

- [ ] **Step 2: Run it**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run Leak -count=1 -v 2>&1 | tail -20
```

**A failure here is a real defect, not a test to relax.** Fix the handler.

- [ ] **Step 3: Prove the sweep works**

Temporarily make `maskSecret` return its input, re-run, and confirm the test
fails. A leak test that cannot fail is decoration. Restore it afterwards.

- [ ] **Step 4: Verify the tree, commit**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -count=1 && go vet ./... && gofmt -l .
git add internal/admin
git commit -m "test(admin): sweep every credential kind for leaks"
```

---

### Task 19: Documentation

**Files:**
- Modify: `README.md`, `darkrouter.example.yaml`, `docs/PROGRESS.md`

**Interfaces:**
- Consumes: everything. Produces: nothing code depends on.

- [ ] **Step 1: Document the three strategies in the README**

Add after the dashboard section:

> ## Signed and subscription credentials
>
> Beyond a static API key, Darkrouter carries three credential strategies. Each
> composes with an adapter kind rather than being one: a Claude subscription
> speaks Anthropic Messages and an OpenAI one does not, so OAuth cannot be a
> provider kind of its own.
>
> | Style | Used by | What Darkrouter stores |
> |---|---|---|
> | `sigv4` | `bedrock` | An access key and secret, or nothing — the AWS chain covers environment, shared config and instance role |
> | `gcp-sa` | `vertex` | The service-account JSON, encrypted, exchanged for a short-lived token |
> | `oauth` | `anthropic` and any other kind an OAuth preset declares | A refresh token, encrypted, rotated on every refresh |
>
> **Bedrock** speaks Converse, not InvokeModel, so one message shape covers every
> model family. Its region is a provider property rather than part of the model
> id; what carries a `us.` or `eu.` prefix is the cross-region inference profile,
> and discovery catalogues **profile ids** because those are what an invocation
> must name. Streaming arrives as AWS binary eventstream framing rather than SSE.
>
> **Vertex** is one kind with two request builders. A `publishers/google` model
> goes to `:generateContent` with a Gemini payload; a `publishers/anthropic` one
> goes to `:rawPredict` with an Anthropic Messages payload carrying
> `anthropic_version`. Vertex has no usable listing API, so its catalog is seeded
> from the preset and models.dev and the credential probe confirms reachability.
> Llama and Mistral MaaS use a third route and are not served.
>
> **OAuth accounts** connect from Settings. The dashboard shows an authorize link
> and a box to paste the redirected URL back into — the paste path always works,
> because vendors register `localhost` redirect URIs that a homelab admin origin
> cannot satisfy. Where the vendor's registered URI is a localhost callback and
> Darkrouter runs on your own machine, a temporary listener receives the redirect
> directly instead.
>
> Tokens refresh in the background ahead of expiry. Many vendors rotate the
> refresh token on every refresh, so **run one Darkrouter against one account**:
> two instances sharing a grant trip rotation-reuse detection, which some vendors
> treat as theft and answer by revoking the grant. A refresh the provider refuses
> outright disables the credential and says "needs reconnection" on the overview
> — it is not retried, because hammering a refused endpoint is how an account
> gets locked rather than recovered.
>
> **No credential material is ever returned by the API**, for any of the three.

- [ ] **Step 2: Document the provider fields**

In `darkrouter.example.yaml`, add a commented Bedrock and Vertex block showing
`region`, `project` and `location`, and note that both are database-managed like
every other provider since phase 7 — the `providers:` block is imported once.

- [ ] **Step 3: Update the progress document**

Set phase 8 complete in the status table. Add "Closed by phase 8":

- **Three credential strategies exist**, composing with adapter kinds rather than being kinds. `internal/auth` resolves a credential into an authorizer applied after the body is materialized, which is the only point at which SigV4 can hash a payload that will not change.
- **SigV4 is pinned by known-answer vectors** rather than by a live call. `SignedHeaders` includes `content-length`, so a refactor that signed before materializing the body fails the vector instead of producing an opaque 403.
- **Bedrock discovery catalogues inference profile ids.** `ListFoundationModels` alone would store precisely the identifiers that fail.
- **Eventstream framing is decoded with the SDK's decoder**, including a frame split across reads and a mid-stream exception. The tests build their frames with the SDK's own encoder rather than checking in a binary blob.
- **Vertex dispatches per publisher**, reusing phase 4's Gemini and Anthropic renderers rather than growing a third.
- **`router.Candidate.Publisher` is populated.** It was declared in phase 3 and never read; without it every Vertex request would take the Google route and every Claude call would 400.
- **OAuth state is single-use, expiring and session-bound**, and a session mismatch does not consume it — letting a blocked attack invalidate the legitimate operator's own callback would turn it into a denial of service.
- **Rotation is persisted before the in-memory pair is replaced**, so a crash mid-refresh loses a refresh rather than the account.
- **`provider_keys` needed no migration.** `kind`, `expires_at` and `scope`, and `providers.region/project/location`, have all been columns since migration 0001 — master design §11 wrote the column list for the whole product.

And "Carried forward from phase 8":

- **Nothing in this phase was verified against a real vendor.** There is no AWS account, no GCP service account and no Claude subscription in this environment. Every test runs against a known-answer vector, an SDK-encoded frame, or an `httptest` server, and Task 20's verification is fake-backed. The Converse and `rawPredict` field names are the specific risk: they are taken from vendor documentation and pinned by golden files, so a correction later is a visible diff rather than a silent behavior change.
- **Bedrock serves `llm` only.** Its embedding API is a different shape, and claiming the surface would route embeddings to a Converse endpoint that answers 400. The `vertex` preset declares `embedding` and the adapter does not serve it, for the same reason.
- **Llama and Mistral MaaS on Vertex are not served**, per spec §4.1: they use a third, OpenAI-compatible route that is out of scope for v1.
- **The refresh worker is per-process.** Darkrouter is single-instance by design and nothing here makes two instances safe to run against one OAuth account.
- **`PATCH /api/providers/:id` now accepts `region` and `project`**, closing the phase 7 carry-forward — but `location` is settable only at creation, since changing it moves every catalogued model to a different endpoint.

- [ ] **Step 4: Check the docs against the code**

```bash
export PATH=$PATH:/usr/local/go/bin
grep -oE '/api/[a-z/{}._-]+' README.md | sort -u
grep -oE '"(GET|POST|PATCH|DELETE) /api/[a-z/{}._-]+' internal/admin/*.go | sed 's/.*"//' | sort -u
```

Twenty-one endpoints now, which is master design §10's full count. **Read the
two lists side by side.**

- [ ] **Step 5: Commit**

```bash
git add README.md darkrouter.example.yaml docs/PROGRESS.md
git commit -m "docs: document signed and oauth credentials"
```

---

### Task 20: Fake-backed verification of the whole phase

**Files:**
- Create: `internal/e2e/phase8_test.go`
- Modify: `docs/PROGRESS.md`

**Interfaces:**
- Consumes: everything.

Every prior phase ended with a live run against Groq. **This one cannot**, and
saying so is the task's first obligation. There is no AWS account, no GCP
service account and no Claude subscription in this environment, so spec §8's
first four criteria are exercised against fakes and the note records exactly
that.

What a fake can still prove is most of what matters: that the wiring holds end
to end, that the right URL and payload reach the upstream for each publisher,
that a signature is attached, that failover treats these kinds like any other,
and that no credential material escapes. What it cannot prove is that a real
vendor accepts the payload. That distinction goes in the record verbatim rather
than being blurred.

- [ ] **Step 1: Write the end-to-end test**

Create `internal/e2e/phase8_test.go` — a new package, so it can import the
server without any cycle. It stands up a real `server.Server` against a
temporary database and points three fake upstreams at it:

```go
// Package e2e drives the assembled gateway. It exists because phase 8's
// credential strategies only compose at the top: internal/auth, the adapters,
// the router and the admin API each have unit tests, and none of them can show
// that a request entering the proxy port leaves it signed.
package e2e
```

The cases, one per spec §8 criterion that a fake can reach:

```go
func TestABedrockRequestLeavesSigned(t *testing.T) {
	// Criterion 1, minus "a real Bedrock serves it". The fake asserts the
	// Authorization header is an AWS4-HMAC-SHA256 credential scope naming the
	// configured region, and that the body is Converse-shaped.
}

func TestABedrockStreamDecodesEventstream(t *testing.T) {
	// The fake replies with SDK-encoded frames and the client receives SSE
	// with the text reassembled. This is the whole streaming path, not just
	// the decoder.
}

func TestVertexRoutesEachPublisherToItsOwnURL(t *testing.T) {
	// Criterion 3. Two models on one provider, one per publisher; the fake
	// records the path and the body shape for each.
}

func TestAnOAuthAccountConnectsAndServes(t *testing.T) {
	// Criterion 4's first half, through the admin API: start, paste, then a
	// proxy request that arrives carrying the access token as a bearer.
}

func TestARotatedRefreshSurvivesARestart(t *testing.T) {
	// The half a unit test cannot reach: refresh, then rebuild the server from
	// the same database file and confirm the rotated token is what comes back.
	// A crash between refresh and persist is what this guards.
}

func TestAnInvalidGrantShowsAsNeedsReconnection(t *testing.T) {
	// Criterion 4's second half, read from GET /api/overview the way the
	// dashboard reads it.
}

func TestASignedProviderFailsOverLikeAnyOther(t *testing.T) {
	// Criterion 5. A bedrock provider at a dead address, first by priority,
	// with an openaicompat provider behind it. The trace must show two
	// attempts and the request must succeed.
}

func TestNoCredentialMaterialLeavesTheProcess(t *testing.T) {
	// Criterion 6, across both ports: canaries in every credential, then every
	// admin endpoint and a proxy error response swept.
}
```

- [ ] **Step 2: Run it**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/e2e/ -count=1 -race -v 2>&1 | tail -40
```

- [ ] **Step 3: Run the whole suite, the frontend, and the image**

```bash
export PATH=$PATH:/usr/local/go/bin
npm --prefix web run build && npm --prefix web test
go test ./... -count=1 -race && go vet ./... && gofmt -l .
docker build -t darkrouter:p8 . 2>&1 | tail -3
```

The image matters here for one specific reason: `aws-sdk-go-v2/config` pulls a
large dependency tree, and `CGO_ENABLED=0` must still produce a static binary.
Confirm the image size against phase 7's 53.3 MB and note the delta.

- [ ] **Step 4: Confirm nothing is left running**

```bash
docker rm -f dr-p8 2>/dev/null; docker ps --filter name=dr-p8
ss -ltnp 2>/dev/null | grep -E ':1808[01]' || echo "ports free"
```

- [ ] **Step 5: Record the result**

Add a numbered section to `docs/PROGRESS.md`'s "Open items" with the real
numbers: the signature scope the fake observed, the two Vertex URLs, the
refresh count under concurrency, the failover trace's attempt count, the image
size, and the canary sweep's result.

**Say plainly, in the first paragraph, that no vendor was contacted**, and list
which of spec §8's seven criteria were exercised against a fake rather than
live. A verification note that overstates its coverage is worse than none — and
this one has more to be careful about than any before it.

- [ ] **Step 6: Commit**

```bash
git add internal/e2e docs/PROGRESS.md
git commit -m "test(e2e): verify phase 8 against fakes"
```

---

## Self-review

Run after the last task, against the spec with fresh eyes.

**Spec coverage.** Every section maps to a task:

| Spec | Tasks |
|---|---|
| §2.1 OAuth is a strategy, not a kind | 1 (the seam), 14 (composed with `anthropic`) |
| §3.1 Converse not InvokeModel | 4 |
| §3.2 signing and transport | 3 (signer), 6 (eventstream) |
| §3.3 model identifiers and profiles | 7 |
| §4.1 two request builders | 9 |
| §4.2 service-account authentication | 8 |
| §4.3 no Vertex discovery | 10 |
| §5.1 connect flow, both paths | 11 (PKCE, state), 12 (paste), 13 (listener) |
| §5.2 refresh, rotation, failure modes | 14 |
| §5.3 expectations | 15 (probe), 17 (overview) |
| §6 credential health | 15 |
| §7 testing | 3, 6, 8, 9, 11, 14 (unit); 16 (golden); 18 (storage sweep) |
| §8 done criteria | 20 |

**Deliberate deviations, both recorded in the tasks that make them:**

1. **The Vertex publisher is read from the preset, not a provider-row column.** Spec §4.3 says "the publishers the provider row declares"; the row declares a preset, and the two shipped Vertex presets already encode exactly that split. Task 10 says so where a reviewer will see it.
2. **No migration.** Spec §11's column list has been in the schema since migration 0001. Task 2 verifies this before relying on it rather than assuming.

**Known gaps, all recorded in Task 19 rather than silently skipped.** Nothing
verified against a real vendor; Bedrock and Vertex serve `llm` only; Llama and
Mistral MaaS on Vertex unserved; the refresh worker is single-instance.

---

## Finishing

With Task 20 committed, use superpowers:finishing-a-development-branch. The
merge is `--no-ff` onto `master`, so the phase stays legible as a unit:

```bash
export PATH=$PATH:/usr/local/go/bin
npm --prefix web run build && npm --prefix web test
go test ./... -race -count=1 && go vet ./... && gofmt -l .
git checkout master
git merge --no-ff phase8-signed-oauth -m "feat: phase 8 signed and oauth credentials"
```

Do not push. Master is far ahead of origin and pushing is the operator's call.
