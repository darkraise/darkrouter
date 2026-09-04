# Development

## Layout

Go gateway at the root, React console under `web/`. `web/go.mod` exists only
to stop `go ./...` descending into `node_modules`.

The console is embedded into the binary with `//go:embed`, so `internal/admin/dist`
must exist at compile time. It is gitignored apart from a `.gitkeep` and the
icon assets.

## Running locally

```bash
docker compose up --build     # gateway on 8080, console on 8081
cd web && npm run dev         # console with hot reload, proxied to the gateway
```

For anything you intend to look at in the real console, see
[`operations/deploy.md`](operations/deploy.md) — a local `npm run build` does
not change what a running container serves.

## Before committing

Run every gate in [`operations/verification.md`](operations/verification.md).
The repository-wide Go gate, not a narrower one.

## Tests

Test-first. Write the assertion, watch it fail for the right reason, then make
it pass. See the last section of `operations/verification.md` for why this is
not negotiable here.

Golden fixtures live in `internal/golden`. Regenerating one means reviewing
the diff, not trusting it — a fixture that silently stopped exercising its
path is the failure mode.

## The preset catalogue

`internal/catalog/presets.yaml` is **generated**. Corrections go in
`presets.overrides.yaml`; a regeneration reproduces them.
`presetgen-conflicts.md` is regenerated on every run and records how each
conflict resolved.

Regeneration is a scheduled CI workflow that clones the upstream registries,
regenerates the presets, snapshot, free-tier register and logos, and opens a
pull request. There is no need for a local checkout of either registry.

That workflow is deliberately split in two: the half that runs untrusted
upstream code holds a read-only token and no build cache, and the half that
holds a write token runs none of it.

## Conventions

- English only, everywhere.
- Commits are `<type>(<scope>): <subject>`, subject in the imperative, 50
  characters or fewer, no trailing period. Split commits that address
  different concerns.
- Comments explain **why**, never what. A comment that restates the code is
  noise; one that records a constraint, an invariant, or a bug that was fixed
  once already is the most valuable line in the file.
- Never hardcode a font size in the console. See
  [`design/console.md`](design/console.md).

`CLAUDE.md` at the repository root carries the rules an agent working here
must follow, including the deploy-after-a-fix rule. It is authoritative; this
document links to it rather than restating it.

## Releasing

Only a push to the main branch releases. CI derives the version before
building the image, so it can be compiled in and published as tags in the same
build, and creates the git tag only after the image is in the registry — so a
release never points at a version nobody can pull.

Below 1.0.0 a breaking change bumps the minor. Reaching 1.0.0 is a deliberate
manual tag.
