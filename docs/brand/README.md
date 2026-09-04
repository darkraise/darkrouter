# Brand assets

The identity mark's design rationale is in
[`../plan/decisions.md`](../plan/decisions.md) under "Console and brand"; it is
drawn in the console by `web/src/features/shell/identity-mark.tsx`.
The files here are the same mark for everything outside the React app.

| File | What it is |
|---|---|
| `mark.svg` | The mark on its native 24-unit grid, matching the console component |
| `wordmark.svg` | The lockup: mark, 12px gap, `darkrouter` in Inter 600 lowercase |
| `wordmark.png` | The same at 2×, with Inter baked in, for contexts that will not load a webfont |
| `tile.svg` | §3.5's coral tile with the mark in white — the source for the platform icons |
| `tile-maskable.svg` | The tile with the mark pulled in to Android's maskable safe zone |

The shipped icons live in `web/public/` and are copied into the binary by
Vite's `publicDir`, which writes to `internal/admin/dist` where `go:embed`
picks them up. Nothing in the Go tree needs changing to add one.

## Three drawings, not one

`favicon.svg` in `web/public/` is deliberately **not** `mark.svg` scaled down.
On the 24-unit grid a hairline scaled to a 16px tab lands on two thirds of a
pixel and renders as two grey rows. The favicon is redrawn on a 16-unit grid so
every stroke is one whole unit, which also makes 32, 48 and 64 exact. It drops
the closed bezel to four corner brackets: at 16px the full square fills the
canvas, the mark reads as a grid in a box, and the pip loses its contrast
against the bezel edge it touches. `brand-assets.test.ts` fails if a fractional
coordinate ever gets into that file.

The tile is the third drawing. Its pip cannot be coral on a coral ground, so
the branch keeps its rank the other way round: the pip is the one element at
full white and the graticule around it sits at 85%.

## Colours are frozen here

The console draws the mark in `--legend` and `--primary`, which the theme
computes at runtime against whatever background the operator's axes resolved
to. A file on disk cannot do that, so these assets carry fixed values sampled
from the shipped theme:

| Role | Light | Dark |
|---|---|---|
| Hairline | `#7B6C60` | `#89786C` |
| Hairline, favicon only | `#7B6C60` | `#B0A49B` |
| Pip | `#E56748` | `#E56748` |
| Name, in the wordmark | `#1F1714` | `#FAF8F5` |

The favicon's dark hairline is two steps lighter than `--legend`'s own dark
value on purpose. A tab strip is not the app background: against Chrome's dark
chrome `#89786C` clears only 2.9:1, short of the 3:1 a graphic needs, and
`#B0A49B` clears 4.9:1.

## Regenerating the rasters

The PNGs and `favicon.ico` are rendered from the SVGs above; there is no
committed script, because the machine that made them had no `rsvg-convert`,
ImageMagick or `cairosvg` and drove headless Chrome over the DevTools protocol
instead. Any SVG rasteriser reproduces them from these sizes:

| Output | Source | Size | Notes |
|---|---|---|---|
| `apple-touch-icon.png` | `tile.svg` | 180 | Opaque, square, no rounding — iOS masks it itself |
| `icon-192.png`, `icon-512.png` | `tile.svg` | 192, 512 | Corners rounded at 22.2%, matching `--radius` on a 36px tile |
| `icon-512-maskable.png` | `tile-maskable.svg` | 512 | Opaque, full bleed |
| `favicon.ico` | `favicon.svg` | 16, 32, 48 | Hairline forced to `#8F7D70`: an `.ico` cannot answer a media query |
