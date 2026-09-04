# Darkrouter documentation

Darkrouter is a self-hosted LLM gateway. It accepts OpenAI, Anthropic and
Gemini requests, routes each one across a priority-ordered fleet of providers
and credentials, fails over on the ones that break, and records what happened.

## Reading order

Read down. Each layer answers a question the next one depends on.

| Layer | Question | Start at |
|---|---|---|
| [`requirements/`](requirements/) | What must the gateway do? | [01-product.md](requirements/01-product.md) |
| [`design/`](design/) | How does it do it? | [architecture.md](design/architecture.md) |
| [`plan/`](plan/) | What is built, what is next, and why was it decided that way? | [status.md](plan/status.md) |
| [`operations/`](operations/) | How is it run? | [deploy.md](operations/deploy.md) |
| [`development.md`](development.md) | How is it changed? | — |

A newcomer should read `requirements/01-product.md`, then
`design/architecture.md`, then stop. Everything else is reference, consulted
when a specific question arises.

## Authority

Where two documents disagree, the higher layer wins and the lower one is a
defect to be fixed:

**requirements → design → plan.**

Where a document and the code disagree, **the code wins** and the document is
a defect. Every statement here was checked against the tree on 2026-09-04;
statements that could not be verified say so rather than guessing.

## Ownership

One home per subject. These boundaries exist because the documentation this
set replaces had several, and they disagreed with each other.

- **`README.md`** at the repository root is the front door: what Darkrouter
  is, how to start it, and links to here. It does not carry procedure.
- **`operations/`** owns every procedure an operator runs — deploying,
  backing up, restoring, rotating a key, reading a runbook.
- **`plan/status.md`** owns verification state. What is met, what is
  unverified, what is open. No other document tracks it.
- **`plan/decisions.md`** owns reasoning that the code does not carry. If a
  decision's "why" is recoverable by reading the source, it does not belong
  here.
- **`design/`** owns mechanism, and cites requirement identifiers only where
  a design choice exists to satisfy a specific one.
- **`ux/`** owns the console's visual language and its mockup set. It does
  not track what has shipped.
- **`brand/`** owns the identity marks. `web/src/features/shell/brand-assets.test.ts`
  reads that directory, so it stays where it is.
- **`CLAUDE.md`** owns the rules an agent working in this repository must
  follow. `development.md` links to it rather than restating it.

## Identifiers

Requirements carry stable identifiers — `FR-<area>-<n>` and `NFR-<area>-<n>`.
They are cited from `plan/status.md`, which is the one place that says whether
a requirement is met. Design documents do not cite them: keeping three files
in step for one behaviour change is how a traceability scheme dies quietly.

An identifier is never reused. A withdrawn requirement keeps its number and is
marked withdrawn.

## History

The phase-by-phase specifications and implementation plans this set replaces
were deleted on 2026-09-04. They are in git history; the durable half of their
content — the decisions whose reasoning is not in the code — was carried into
[`plan/decisions.md`](plan/decisions.md).
