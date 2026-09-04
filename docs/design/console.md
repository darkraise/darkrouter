# The operator console

React and Vite, built with darkraise-ui, embedded into the binary. See
[`../operations/deploy.md`](../operations/deploy.md) for why a console change
is not live until the image is rebuilt.

## Destinations

Eight in the rail, in three groups, plus Settings pinned to the footer:
Overview, Requests, Usage, Providers, Models, Routing, Playground, Connect,
Settings.

**There is deliberately no ninth.** Breaker state, discovery health, preset
browsing, aliases and model overrides each get a panel beside the subject they
describe rather than a destination of their own. A ninth rail item is how a
console ends up with twenty-three sections of which six are stubs. That
argument lives in the navigation source itself, so it survives this document.

The overview carries a routing flow graph rather than a health grid: aliases
on the left, the router in the middle, providers on the right in priority
order, edge thickness as share, dashed returns for traffic that arrived
somewhere because somewhere else refused it, and no edge at all for a
non-candidate.

## The ladder

The attempt ladder is defined once and copied byte-identically wherever it
appears. It has three modes:

- **Retrospective** — filled marks. The attempts happened.
- **Predictive** and **compressed** — hollow marks. Nothing has been sent.

Fill versus outline is the only separator, so **a filled mark outside a trace
is a bug**. The mark union is narrowed by mode at the type level, so it does
not compile.

The four ladder states must be distinguishable with colour stripped.

## Colour

State colour has exactly three carve-outs: a destructive affordance, a request
outcome, and attention. Attention is the widest and the one worth policing.
Neither carve-out permits a state colour in the ladder gutter, nor the trace
colour on a provider pip.

**A credential cools; a provider degrades.** Provider pips take the four
states the overview endpoint actually emits — healthy, degraded, disabled,
unconfigured — and `degraded` is not a synonym for cooling. The ladder keeps
its cooling mark because down there the subject really is one credential.

Coral is brand only — position and primary action, never state — so it never
joins amber and red in the ladder gutter. Inter carries the chrome; IBM Plex
Mono carries data.

The chart ramp is overridden and scoped to a class, because the theme engine
derives two of its series colours from the accent, which at coral lands them
in the orange and lime neighbourhood — on the usage charts that reads as the
reserved cooling amber and healthy green, so a series would look like a state.

## Typography

Never a hardcoded font size. 14px is the floor, and only the predefined scale
may be used. Hierarchy below body text comes from colour and weight.

The reason is the font-size axis: its steps rebind the scale, so a scale-bound
label moves with an operator's setting while a pixel-valued one does not. A
hardcoded size is not a slightly-off size; it is a size that silently opts out
of a setting the operator changed.

The mockup set under `../ux/mockups/` is the one exception — it never loads
the design system and never participates in the axis. This is recorded in
`CLAUDE.md`, which is authoritative for it.

## Light mode

Light is a palette swap by design. An earlier design language inverted well
polarity between modes; the current one stacks surfaces upward in both, so
there is no inversion to prove.

Nine token values are repaired against the upstream design system's own,
because several shipped values sit under the contrast floor their role is held
to. One floor is deliberately left where it is: the button-label floor stays
at 3:1, because raising it to AA broke tests encoding two separate design
decisions, and only one accent-and-vibrancy combination clears AA — which is
why the console pins that combination.

## The API type module

`web/src/lib/api-types.ts` mirrors Go json tags **by hand and is not
enforced**. Its first version had every request-row field wrong, invented
rather than read, and typechecked cleanly for two commits. Anyone changing a
response shape must change it here too; nothing will catch them.
