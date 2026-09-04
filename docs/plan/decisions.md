# Decision log

Decisions whose reasoning is **not recoverable from the code**. If reading the
source answers "why", it does not belong here.

Most of these were recovered from phase specifications and progress ledgers
deleted on 2026-09-04. Several are marked *do not re-litigate*: they have been
independently "fixed" back to the wrong answer at least once.

---

## Routing and failover

**A 429 advances to the next credential; any other provider failure skips the
provider entirely.** The next credential hits the same dead upstream, whereas
rate limits are usually per credential. This is what makes the credential and
provider outcomes worth distinguishing at all; collapsing them makes the fleet
model meaningless.

**A single 5xx does not cool. It counts toward `trip_after`.** One bad
response is not evidence a provider is down.

**A `RetryableCredential` never resets the breaker ladder**, and a reset
applies only to success and fatal outcomes, scoped to the exact triple.
Otherwise a billing-exhausted key is resurrected by any malformed request.
Easy to "simplify" away.

**Post-commit streams are governed by an idle timeout, not the total.** A
legitimate ten-minute reasoning response must not be killed while a silent
provider still is.

**A content filter is fatal, not retryable.** A refusal is the provider
answering. Retrying it burns the chain to reach the same refusal.

**Only a content delta commits — never a block-start event.** Enforced
identically on the translated path and both raw recognisers. Committing on
block-start would commit one event earlier than the translated path does on
every Anthropic stream, and the two paths must agree or the fast path fails
over in cases the slow path serves.

**A stream that ends having emitted nothing is a success.** Failing over there
burns the whole chain every time a model stops immediately.

**Darkrouter's own timeout is checked before the client-disconnect signal.**
Both cancel the same context; checking the disconnect first reclassifies a
genuine provider timeout as a client hang-up.

**`Available` mutates, so the router never calls it.** Calling it claims the
half-open probe. The router reads a frozen availability map instead, so a
probe is never spent on a candidate the router may not reach.

**Every attempt emits exactly one health signal, and a 2xx is not recorded
from the status line.** The loop claimed the probe on the way in; an exit path
that skipped the recorder would leave the entry shut forever with nothing
testing it.

**The retryable-model counter described in early specs was never built.** The
job is done differently: the catalogue marks a model removed upstream after
three successful listings omit it. The counter would have duplicated that with
worse data.

**Route preview shares the executor's snapshot and the router's own resolve
function.** The criterion is an equality; two constructions of "the same
inputs" would drift.

**A dangling alias reference validates as a warning, not an error.** Otherwise
deleting a provider in the console makes every future reload fail, and the
only affordance is a reload button that keeps failing.

---

## Translation and dialects

**There is no `oauthsub` provider kind.** Kinds are defined by payload shape,
and OAuth is an authentication modifier composed with one. A Claude
subscription speaks Anthropic Messages and an OpenAI one does not, so such a
kind could not say what it emits. This is why credentials resolve into an
authorizer applied after the body is materialised.

**Vertex dispatches per publisher.** Google's publisher takes the Gemini
payload; Anthropic's takes Messages at `rawPredict` with the model moved into
the URL and a mandatory version field. Llama and Mistral MaaS are a third,
OpenAI-compatible route and are out of scope. This is the entire reason a
candidate carries a publisher.

**The catalogue decides a model's request shape, not its name.** A
name-fragment table needs a new entry every generation and is wrong for an
aliased or proxied model. A gateway renaming a model to "default" would get
the permissive fallback and a 400 on every reasoning request.

**An unrecognised model gets the permissive fallback**, and the "shape was
guessed" warning fires only when the request actually asked for thinking — so
self-hosted endpoints do not train operators to ignore warnings.

**Manual thinking loses to a forced tool choice.** The forced tool is the
client's explicit instruction and an agentic loop depends on it; reasoning
depth is the softer ask. Adaptive thinking has no such conflict.

**A thinking budget is clamped below `max_tokens` rather than raising
`max_tokens`.** Raising it silently multiplies the bill on the one control the
client actually set.

**Embedding failover across models is permitted, but flagged.** Vectors from
different models are not comparable, and a client filling an index across a
failover corrupts it silently. Refusing outright would make an alias useless
the moment its first provider rate-limits, so the answer is a warning plus
documentation. *This is why the README carries a section telling operators to
point embedding aliases at one model.*

**The Responses API refuses its stateful form rather than emulating it.** A
confident amnesic answer is worse than an explicit error. Response ids carry a
prefix and are stored nowhere, so an echoed id is refused rather than guessed
at.

**Transcription uploads buffer to the size cap rather than streaming
through.** Streaming the multipart body makes failover impossible and the
in-form model field unrewritable. Failover is the product.

**Cohere v2 is the rerank wire shape.** Settled rather than assumed: exactly
one shipped preset declares the rerank surface, and neither of the two obvious
alternatives is a preset at all. Each preset declares its own path through a
quirk; a deviating provider is excluded from the surface rather than
special-cased.

**Gemini array-form streaming is not passthrough-eligible.** The recogniser
finds no event boundary, so a response over the pre-commit cap failed the
whole chain and cooled providers that had answered correctly.

**A blocked Gemini prompt must not be classified as a retryable provider error
in the streaming recogniser.** It failed the request over and recorded a
health failure against a provider that had answered correctly.

**Byte-for-byte passthrough was a false claim and was weakened to semantic
preservation.** Go's JSON encoder HTML-escapes three characters, compacts
whitespace and de-duplicates keys. The encoder now disables escaping and the
rewrite is skipped entirely when the model names already match.

**`n > 1` is refused at parse time rather than forwarded**, and is excluded
from the passthrough field list.

**The shared upstream transport disables compression**, which costs the
translated path bandwidth. A forwarded body must arrive byte-identical to what
the provider sent. An accepted cost that looks like an oversight.

**`InputTokens` excludes cache reads repository-wide.** Anthropic excludes
them; OpenAI and Gemini include them. Any cost formula written before
normalisation is wrong for at least one family.

**`url.PathEscape` is unusable for a Bedrock model id.** It leaves the colon
alone, and every inference-profile id contains one. The AWS SDK's own escaping
covers everything outside the unreserved set. The difference is a 403 on every
request — a trap that costs a day to rediscover.

**SigV4 signs `content-length`, so the credential is applied only after the
body is materialised.** It is the only point at which the payload will not
change again. A refactor that signs earlier fails a known-answer vector rather
than producing an opaque 403.

**A non-static auth style leaves the target's API key empty**, so no adapter
can write a token document into its own header by forgetting a step.

---

## Catalogue and pricing

**Prices are stored per million tokens, not per token.** The upstream index
prices in dollars per million as floats; micro-dollars per token truncates
$0.14/M to integer zero.

**Whether a price is known is stored, not inferred from null columns.** A
genuinely zero-priced model must survive the round trip without becoming
"unpriced".

**An unpriced model records no cost, not a zero one.** Zero reports as free.

**A reseller's self-quote is capped down from measured to indexed.** An
aggregator quoting its own markup is not a measurement. The reseller flag is
partly hand-declared and partly derived, and the cap is why the grade accessor
on the price type is the only public entry point — the per-source one is
deliberately unexported so the cap cannot be bypassed.

**The preset outranks the discovery row for surfaces.** Discovery hardcodes
the LLM surface into every row it inserts, so a row-first merge meant widening
a preset had no effect on any discovered model. Invisible until someone widens
a preset.

**Inferred capabilities route with a warning rather than being filtered out.**
A provider's own error is clearer than Darkrouter silently refusing, and the
trace explains the decision.

**Local model capabilities are probed where possible, then routed with a
warning.** Same reasoning.

**Thirteen OAuth presets were dropped rather than shipped incomplete.** Of the
upstream entries marked OAuth, only a third carried any literal URL and none a
complete block; one had a token URL but no authorization URL and its client id
sat behind a runtime call. Shipping an incomplete entry shows a provider in
the console that cannot be connected. *This is why the catalogue has 208
presets and one OAuth block, which otherwise reads as an omission.*

**Bedrock catalogues inference-profile identifiers, not foundation-model
ones.** The foundation listing returns bare ids that are frequently not
invocable on demand; cataloguing "as discovered" would store exactly the
identifiers that fail. Region is an endpoint property, not part of the id.

**The upstream metadata index has no vision flag** — it is an input-modality
list containing an image entry. Its costs are dollars per million as floats.

**The discovery concurrency cap is global across the fleet, not per
provider.** A per-provider cap cannot stop forty providers opening forty
connections on boot.

**`presets.yaml` is generated; publisher lines are duplicated into the
overrides file** so a regeneration reproduces them. A guard test fails if
either Vertex preset loses one.

**The free-tier vocabulary is five values, and `avoid` is vetoed at two
gates** — catalogue import and the router filter — unless the operator opts
that provider in. A discontinued tier is exempt at both: a tier that no longer
exists cannot be abused.

**Pool keys are carried but not rendered.** Several providers can share one
upstream allowance; knowing it is useful, routing on it is not.

---

## Storage and accounting

**Requests and attempts carry no foreign key onto providers**, so deleting a
provider preserves its history.

**An attempt row can carry a cost with zero tokens, and that is correct.** A
fully cached prompt burns cache-read tokens the attempt row cannot express,
but the money was real and the aggregates read cost rather than tokens. **This
has been "fixed" twice by people who checked only the two token fields. Do not
re-litigate.**

**Cost is computed from the model that actually served, not the alias
requested.**

**A failed attempt's spend is attributed to the provider that burned it**, and
the serving attempt is identified by its outcome rather than by matching the
request's final provider — a pre-commit retry can re-attempt the same provider
and model.

**Warnings on a request row are assigned, not accumulated across attempts.**
The record must describe the translation the client actually received, not
every abandoned attempt on the way.

**`log.retention` has a hard floor of 48 hours.** The rollup finalises a day
before freezing it, which needs the previous day still present when the
sweeper runs. The floor replaced a rollup-side guard defending the same
invariant from the wrong end.

**The pre-commit byte cap charges a flat per-event overhead.** Without it the
cap is unreachable, because only committing events carry payload and a ping
flood would buffer without bound.

**`schema_version` is a single-row pointer and versions must be contiguous.** A
skipped file number is a startup error, not a gap to tolerate.

**Migrations are forward-only**, which is why restoring and downgrading are
the same operation and why an older binary refuses a newer database rather
than half-applying it.

**A unit mismatch, not a formatting one, once listed expired sessions as
revocable browsers.** Sessions were written in milliseconds and read as
seconds, landing every row in the year 58633 so the expiry comparison was
always true. Only one reader was seconds-based, so authentication was never
affected. *The class of bug recurs; the units table in `design/data-model.md`
exists because of it.*

---

## Configuration

**`internal/config` may not import `internal/store`.** Store imports config;
the reverse closes a cycle. This is why the alias and policy overlay lives in
store and is injected as a function.

**A `PUT` refuses a restart-only field; a file reload only warns.** A reload
is an operator editing a file the process watches — the change is already
made, and a warning is the only honest answer. An API request can be refused
before anything happens.

**The database overlay is applied before a snapshot is published, not
after.**

**`server.proxy_token` was not removed when per-client tokens landed.**
Removing it in the release that adds them stops every existing client on
upgrade. Both are accepted, and authentication is off only when neither
exists.

**Proxy tokens are hashed with SHA-256, not the password KDF.** The token is
256 bits this process generated, so there is nothing to brute-force, and a
slow hash on the proxy hot path is a self-inflicted denial of service.

**OAuth state is single-use and session-bound, and a session mismatch does not
consume it.** Letting a blocked attempt invalidate the operator's own callback
turns the block into a denial of service. Counter-intuitive; would be
"fixed" by a reviewer.

**OAuth rotation is persisted before the in-memory pair is replaced**, so a
crash mid-refresh loses a refresh rather than the account.

**Constant-time comparison hashes both sides first**, so the comparison's
length-based early return cannot leak token length.

---

## Console and brand

**Eight destinations plus Settings, and no ninth rail item.** Breaker state,
discovery health, preset browsing, aliases and overrides each get a panel
beside their subject. "A ninth rail item is how a console ends up with
twenty-three sections of which six are stubs." The argument lives in the
navigation source so it survives this document.

**The ladder is defined once and copied byte-identically, never
reimplemented.** Fill versus outline is the only separator between "these
attempts happened" and "nothing has been sent", so a filled mark outside a
trace is a bug. Escalated from convention to a type-level constraint.

**A credential cools; a provider degrades.** Provider pips take the four
states the overview endpoint emits, and degraded is not a synonym for cooling.

**Coral is brand only, never state**, so it never joins amber and red in the
ladder gutter.

**The chart ramp is overridden and class-scoped**, because two series colours
derived from the accent land in the reserved cooling-amber and healthy-green
neighbourhoods, making a series look like a state.

**Light mode is a palette swap by design.** An earlier language inverted well
polarity; the current one stacks surfaces upward in both modes, so there is no
inversion to prove. The earlier decision is history, not doctrine.

**Nine token values are repaired against the upstream design system's own**,
several of which sit under the contrast floor their role is held to.

**The button-label contrast floor is deliberately left at 3:1.** Raising it to
AA broke tests encoding two design decisions — a label stays white rather than
varying by accent, and light mode ignores the vibrancy axis. One combination
clears AA, which is why the console pins it. Raising the floor is a major
version.

**The error boundary's fallback is deliberately plain markup.** Written with
the design system's components first, it threw for want of router context — an
error screen failing for the reason the screen it reports on did.

**Attempt sequence is zero-indexed at the source and rendered one-based.**
"Attempt 0" reads as a bug to an operator.

**Playground presets are not brought under the conversation-retention switch
or the purge.** That key governs retention happening *without the operator
asking*; a preset is named, saved deliberately and individually deletable.
The claim that conversations were the first place prompt text was retained at
rest was false — a preset's system prompt had been stored since the feature
shipped.

**The destructive purge stays leftmost of the settings header actions.** Two
reviewers independently defended it: the rightmost slot is the habitual
primary target, so leftmost keeps a destructive action away from muscle
memory.

**Three lint warnings are left visible rather than silenced**, because adding
the missing dependencies would change behaviour in effects whose re-firing is
the point.

**The brand ships three drawings, not one scaled.** On the main grid a
hairline scaled to 16px lands on two thirds of a pixel and renders as two grey
rows, so the favicon is redrawn on its own grid where every stroke is a whole
unit. It also drops the closed bezel for corner brackets, because at 16px the
full square fills the canvas and the pip loses contrast against the edge it
touches. The tile is the third drawing, with inverted rank: its pip cannot be
coral on a coral ground.

**Brand colours are frozen in the files**, because the console computes its
own at runtime against whatever the operator's axes resolved to, and a file on
disk cannot.

**There is no committed rasteriser script.** The machine that produced the
PNGs had no suitable tool installed and drove headless Chrome directly. The
size table is the reproduction recipe.

---

## Build, deploy and CI

**No CORS is configured anywhere, because each surface gets a whole origin.**
What would break it: splitting the console host from the API host, or mounting
under a subpath. A cross-site mutating request refused with 403 is the CSRF
check working, not a CORS problem to configure away.

**The published image is built without the optional local CLI**, whose licence
restricts redistribution. A local build includes it. A request routed to that
preset then fails naming the missing binary, and everything else is unchanged.

**Base images are pinned by tag, not digest.** A digest pins harder but
changes with every upstream rebuild and must be resolved by pulling;
dependency automation tracks the tags and opens a pull request, which is the
review point a digest bump would need anyway.

**`npm ci`, never `npm install`**, so the build cannot resolve a newer minor
and produce a different bundle from the one that was tested.

**The healthcheck uses readiness, not liveness.** Readiness fails while the
store or configuration is unusable, which is the state an orchestrator should
route away from.

**A bcrypt hash contains `$`, which compose reads as a variable.** Every one
must be doubled in `.env` or the value silently arrives truncated and a
correct password still fails.

**Verify a deploy by comparing bytes, not filenames.** The asset hash is not
stable across build environments — the image builds at one path and a host
build at another — so a filename comparison reports a false mismatch on a good
deploy.

**A backup is the data directory plus the master key, stored apart.** A
database without its key holds nothing readable; a key without its database is
harmless. If the key has been rotated since a backup was taken, that backup
still needs the key that was current when it was taken.

**CI derives the version before building the image**, so it can be compiled
into the binary and published as tags in the same build, and creates the git
tag only *after* the image is in the registry — so a release never points at a
version nobody can pull. Only a push to the main branch releases.

**Release-notes heredocs use a random delimiter**, because a commit author
could otherwise close the heredoc early and append text of their own.

**The catalogue-refresh workflow is split in two.** The half that runs
untrusted upstream code holds a read-only token and no build cache; the half
that holds a write token runs none of it. The pull-request action is pinned by
commit rather than tag because that job has write access. It compares with
porcelain status rather than a diff, so a newly copied provider logo — an
untracked file — is not missed.

**Never accept a task's narrower test gate as sufficient.** A change gave a
media fetcher an enabled flag, and a struct literal in an unrelated golden
test took the zero value — silently disabling inlining and making the fixture
stop exercising the refusal path it was recorded to cover. Only the
repository-wide gate caught it.

**A test that cannot fail is not a test.** Five tests once shipped that passed
against deliberately broken code. Prove a guard by breaking the code and
watching it go red.
