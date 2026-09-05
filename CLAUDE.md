# darkrouter

## Typography: never hardcode a font size

Two rules, in the console (`web/`) and anywhere else that renders UI:

1. **Never `text-xs`.** 14px (`text-sm`) is the floor. Hierarchy below body
   text comes from colour (`--legend`, `--muted-foreground`) and weight, not
   from a smaller size.
2. **Never a custom size.** No `text-[11px]`, no `text-[length:var(--…)]`, no
   `font-size: 13px` in a stylesheet. Only darkraise-ui's predefined scale:
   `text-sm`, `text-base`, `text-lg`, `text-xl`, `text-2xl`, `text-3xl`.

The reason is the font-size axis. Its four steps rebind `--text-*`, so a
`text-sm` label moves 14 → 18px when an operator picks extra-large while a
`text-[11px]` one stays at 11px. A hardcoded size is not a slightly-off size;
it is a size that silently opts out of a setting the operator changed.

In a stylesheet, where a utility class cannot be applied, use the same scale
through its token — `font-size: var(--text-sm)` — never a pixel value.

**The one exception: `docs/ux/mockups/`.** The mockup set is a standalone HTML
document with its own stylesheet. It never loads darkraise-ui and never
participates in the font-size axis, so a pixel size there cannot silently opt
out of a setting an operator changed — which is the entire reason for the rule
above. Pixel sizes in `docs/ux/mockups/css/` are therefore allowed, and
`qa.py`'s 30px ceiling is the only limit that applies to them. This was
deferred twice and reviewed twice; it is written here so it stops being
re-litigated. Everything under `web/` is bound by the rule without exception.

## Verifying a change in the running console

The admin console at **http://localhost:8091** needs a password, so a change
that is only testable by looking at it cannot be checked from tests alone.

The password for this machine's UAT instance is in **`.uat-credentials`** at
the repository root. That file is gitignored, and it stays that way: the
console checks a password against the bcrypt hash in `.env`
(`DARKROUTER_ADMIN_PASSWORD_HASH`), and a hash is committed precisely so the
plaintext is not. Read the file for the password; do not copy it into here, a
commit message, or any other tracked file.

Log in before claiming a UI change looks right. Test suites cover behaviour —
what a component renders, what a request carries — and cannot see layout,
contrast, or a control that has been pushed off the edge at a narrow width.

## Always redeploy after a feature or bug fix

When a change is finished and verified, redeploy without being asked. A change
that only exists in the working tree is not done: the Go binary embeds the
console bundle at compile time, so neither `npm run build` nor `go build`
changes what the running container serves.

The build, deploy and byte-level verification procedure is in
**[`docs/operations/deploy.md`](docs/operations/deploy.md)** under "Local build (UAT)". Two things
from it that are easy to get wrong: the `compose.uat.yml` overlay is required
or the published image is pulled over the local build, and this machine
publishes the admin port on **8091** (8081 is a different container).

## darkraise-ui is pinned to an exact version

`web/package.json` names `6.7.0` with no caret, alone among its dependencies.
That is deliberate: **6.8.0 breaks the app shell.** Its `dist/styles.css` drops
the `.dr-sidebar-layout` rule — `@apply flex h-screen overflow-hidden` — that
6.5.0 and 6.7.0 both ship, so the container computes to `display: block`, the
sidebar stops spanning the viewport, and the whole main column renders
underneath it rather than beside it.

A caret range is enough to reach it: `npm ci` is safe because the lockfile
pins the version, but any `npm install` resolves to the newest match and
silently breaks every screen. Nothing in the test suite sees it — the layout
is CSS, and the components render exactly as they should.

Take a bump past 6.7.0 only after logging into the running console and looking
at a screen, per "Verifying a change in the running console" above. 6.8.0 also
adds two required `ThemeConfig` axes, `shellStyle` and `sidebarActiveBar`, so
`theme.config.ts` will need both before it compiles.
