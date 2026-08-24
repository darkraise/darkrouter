# Phase 10 Mockup Phase Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce eighteen annotated, self-contained HTML mockup screens for the Darkrouter operator console, gated by an automated QA script and published as a Claude artifact, so the design is approved before any TSX is written.

**Architecture:** Per-screen HTML fragments plus one canonical chrome partial and one shell, assembled by `build.py` into a single self-contained `index.html` (document) and `artifact.html` (same content, no document wrapper). All colour comes from CSS custom properties declared in one token stylesheet that maps 1:1 onto `darkraise-ui` theme tokens; fragments may not contain raw hex. Two keyboard toggles ride on the shell: `A` reveals the annotation pin layer, `T` swaps to light mode. `qa.py` is the gate and is written first.

**Tech Stack:** Python 3.13 (stdlib only — no pip installs), Google Chrome headless at `/usr/bin/google-chrome` for screenshot verification, hand-written HTML and CSS. No build tooling, no npm, no framework. The mockups are static files.

**Spec:** `docs/superpowers/specs/2026-08-24-darkrouter-phase10-operator-console.md`

## Global Constraints

Copy these values verbatim. Every task's requirements implicitly include this section.

**Palette — declared once in `docs/ux/mockups/darkrouter-ui.css`, referenced everywhere as `var(--token)`:**

| Token | Dark | Light |
|---|---|---|
| `--ground` | `#101214` | `#E5E8EB` |
| `--rail` | `#14171A` | `#EEF0F2` |
| `--panel` | `#191C1F` | `#F9FAFB` |
| `--overlay` | `#22262A` | `#FFFFFF` |
| `--well` | `#090A0C` | `#FFFFFF` |
| `--ink` | `#EBEDF0` | `#1A2128` |
| `--ink-muted` | `#98A1A9` | `#5A636D` |
| `--legend` | `#808A93` | `#68737D` |
| `--hairline-subtle` | `#272B2F` | `#DDE0E4` |
| `--hairline` | `#383D43` | `#C3C9D0` |
| `--bezel` | `#6A737C` | `#818C98` |
| `--trace` | `#3ABFF8` | `#0284C5` |
| `--trace-fill` | `#0777AB` | `#0DA2E7` |
| `--accent-ink` | `#3ABFF8` | `#0369A0` |
| `--focus` | `#7ED4FC` | `#0284C5` |
| `--state-healthy` | `#36D399` | `#059467` |
| `--state-cooling` | `#FBBD23` | `#DB7706` |
| `--state-failed` | `#F87272` | `#DC2828` |

**Colour discipline (spec §3.1):** green, amber and red describe what a provider *is*. Sky describes what the *router decided*. A ladder gutter mark uses `--trace`; a provider identity pip uses a state colour. Never the reverse. Amber appears in exactly two places: a health pip, and the cooling skip mark's hollow square.

**Type (spec §3.2):** IBM Plex Mono 400/500/600 and IBM Plex Sans 400/600, self-hosted — no `fonts.googleapis.com`. Mono means "this came off the wire"; sans means "a person is telling you something."

| Role | Spec |
|---|---|
| Legend | 10px / Sans 600 / +0.09em / uppercase |
| Micro data | 11px / Mono 400 / tabular |
| Table and body data | 12.5px / Mono 400 / 1.45 / tabular |
| Emphasis data | 12.5px / Mono 500 |
| Prose | 13.5px / Sans 400 / 1.55 |
| Section title | 12px / Sans 600 / +0.06em / uppercase |
| Page title | 15px / Sans 600 |
| Primary readout | 30px / Mono 600 / tabular / −0.015em |

Nothing exceeds 30px anywhere. Numerals are tabular always. Units set at 10px in `--legend` immediately after the value with no space: `142ms`, `1.2k`, `$0.0021`. Numeric columns right-align on the decimal; identifier columns truncate from the middle.

**Surface (spec §3.3):** radius 2px on everything. Elevation flat, `--shadow-card: none`. Exactly one shadow in the product, on `--overlay`: `0 8px 24px -8px rgba(0,0,0,0.7)` dark, `rgba(15,23,41,0.18)` light. Separation is a value step plus a 1px hairline, never a shadow. Density compact: 30px table rows, 30px form controls, 44px top bar, 200px rail, 12px section padding. Panels tile with a 1px shared seam, not a gutter; gutters appear only between the three nav groups.

**Motion (spec §3.4):** three animations only — 120ms opacity on hover, 160ms height on disclosure, 90ms cross-fade on a live value swap. No page transitions, no entrances, no shimmer, no spinners. Loading is a 2px determinate bar in `--trace` at the top of a well.

**Page titles** are the route in lowercase mono: `operate/requests`, `configure/routing`.

**Every fragment must pass `qa.py`:** no raw hex, no external resource loads, at least one pin and exactly one legend per screen, no duplicate ids across the built index, balanced tags, no font-size above 30px.

**Data realism:** every value shown must be plausible for this gateway and use real field names from the admin API. Provider ids come from the real preset set (`groq`, `anthropic`, `openrouter`, `cerebras`, `deepinfra`, `together`, `bedrock`, `vertex`). Request ids are 26-character ULIDs. Never use `foo`, `lorem`, `Example Provider`, or `user@example.com`.

---

## File Structure

```
docs/ux/mockups/
  darkrouter-ui.css      token declarations, both modes, and every shared class
  _shell.html            document shell: head, nav TOC, the A and T key handlers
  _chrome.html           canonical console chrome (rail, top bar) copied into each screen
  build.py               assembles index.html and artifact.html
  qa.py                  the gate
  fonts/                 self-hosted IBM Plex woff2 subsets
  fragments/
    00-design-language.html
    01-ladder-specimen.html
    02-overview.html
    03-requests.html
    04-request-trace.html
    05-usage.html
    06-providers.html
    07-provider-detail.html
    08-preset-browser.html
    09-models.html
    10-routing.html
    11-playground.html
    12-playground-compare.html
    13-connect.html
    14-settings.html
    15-login.html
    16-first-run.html
    17-light-proof.html
  index.html             built, committed
  artifact.html          built, committed
```

`qa.py` and `build.py` are separate because a reviewer can reject a broken gate while accepting a working assembler, and because the gate must exist before there is anything to gate.

Each fragment is one `<section class="screen" id="s-NN-slug" data-screen-title="…">`. Fragments never contain `<html>`, `<head>`, `<body>` or `<style>`.

---

## Task 1: The QA gate

**Files:**
- Create: `docs/ux/mockups/qa.py`
- Create: `docs/ux/mockups/tests/fixtures/bad_hex.html`
- Create: `docs/ux/mockups/tests/fixtures/bad_external.html`
- Create: `docs/ux/mockups/tests/fixtures/no_pin.html`
- Create: `docs/ux/mockups/tests/fixtures/good.html`
- Test: `docs/ux/mockups/tests/test_qa.py`

**Interfaces:**
- Consumes: nothing.
- Produces: `qa.check_fragment(path: Path) -> list[str]` returning a list of human-readable failure strings, empty when clean. `qa.check_index(path: Path) -> list[str]` for whole-document checks. `qa.main() -> int` returning a process exit code, 0 on pass.

- [ ] **Step 1: Write the failing test**

Create `docs/ux/mockups/tests/test_qa.py`:

```python
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import qa

FIX = Path(__file__).parent / "fixtures"


class TestFragmentChecks(unittest.TestCase):
    def test_raw_hex_is_rejected(self):
        problems = qa.check_fragment(FIX / "bad_hex.html")
        self.assertTrue(any("raw hex" in p for p in problems), problems)

    def test_external_resource_is_rejected(self):
        problems = qa.check_fragment(FIX / "bad_external.html")
        self.assertTrue(any("external resource" in p for p in problems), problems)

    def test_anchor_to_external_site_is_allowed(self):
        # A link to a provider's website is content the screen depicts, not an
        # asset the page fetches, so it must not trip the self-contained rule.
        self.assertEqual(qa.check_fragment(FIX / "good.html"), [])

    def test_multiple_classes_still_count(self):
        # class="legend prose" is one legend. Plain substring matching on
        # 'class="legend"' misses it and reports zero.
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "multi.html"
            f.write_text(
                '<section class="screen" id="s-1-m" data-screen-title="m">'
                '<p class="legend prose">only legend</p>'
                '<b class="pin big" data-pin="1">1</b></section>',
                encoding="utf-8",
            )
            self.assertEqual(qa.check_fragment(f), [])

    def test_missing_pin_is_rejected(self):
        problems = qa.check_fragment(FIX / "no_pin.html")
        self.assertTrue(any("pin" in p for p in problems), problems)

    def test_oversized_type_is_rejected(self):
        problems = qa.check_fragment(FIX / "bad_hex.html")
        self.assertTrue(any("font-size" in p for p in problems), problems)

    def test_protocol_relative_src_is_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "proto_rel.html"
            f.write_text(
                '<section class="screen" id="s-1-pr" data-screen-title="pr">'
                '<p class="legend">proto rel</p>'
                '<b class="pin" data-pin="1">1</b>'
                '<img src="//cdn.example.com/x.png" alt=""></section>',
                encoding="utf-8",
            )
            problems = qa.check_fragment(f)
            self.assertTrue(any("external resource" in p for p in problems), problems)

    def test_svg_xlink_href_is_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "svg_xlink.html"
            f.write_text(
                '<section class="screen" id="s-1-sx" data-screen-title="sx">'
                '<p class="legend">svg xlink</p>'
                '<b class="pin" data-pin="1">1</b>'
                '<svg><use xlink:href="https://example.com/s.svg#i"/></svg></section>',
                encoding="utf-8",
            )
            problems = qa.check_fragment(f)
            self.assertTrue(any("external resource" in p for p in problems), problems)

    def test_svg_image_href_is_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "svg_image.html"
            f.write_text(
                '<section class="screen" id="s-1-si" data-screen-title="si">'
                '<p class="legend">svg image</p>'
                '<b class="pin" data-pin="1">1</b>'
                '<svg><image href="https://example.com/p.png"/></svg></section>',
                encoding="utf-8",
            )
            problems = qa.check_fragment(f)
            self.assertTrue(any("external resource" in p for p in problems), problems)

    def test_anchor_href_is_still_allowed(self):
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "anchor_href.html"
            f.write_text(
                '<section class="screen" id="s-1-ah" data-screen-title="ah">'
                '<p class="legend">anchor href</p>'
                '<b class="pin" data-pin="1">1</b>'
                '<a href="https://groq.com" rel="noreferrer">groq.com</a></section>',
                encoding="utf-8",
            )
            problems = qa.check_fragment(f)
            self.assertEqual(problems, [])


class TestIndexChecks(unittest.TestCase):
    def test_duplicate_ids_are_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            doc = Path(d) / "index.html"
            doc.write_text(
                "<html><body><div id=\'a\'></div><div id=\'a\'></div></body></html>",
                encoding="utf-8",
            )
            problems = qa.check_index(doc)
            self.assertTrue(any("duplicate id" in p for p in problems), problems)

    def test_unbalanced_tags_are_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            doc = Path(d) / "index.html"
            doc.write_text(
                "<html><body><div><span></div></body></html>", encoding="utf-8"
            )
            problems = qa.check_index(doc)
            self.assertTrue(any("unbalanced" in p for p in problems), problems)


if __name__ == "__main__":
    unittest.main()
```

Create `docs/ux/mockups/tests/fixtures/bad_hex.html`:

```html
<section class="screen" id="s-99-bad" data-screen-title="bad">
  <p class="legend">bad</p>
  <b class="pin" data-pin="1">1</b>
  <div style="color: #ff0000; font-size: 48px">nope</div>
</section>
```

Create `docs/ux/mockups/tests/fixtures/bad_external.html`:

```html
<section class="screen" id="s-98-ext" data-screen-title="ext">
  <p class="legend">ext</p>
  <b class="pin" data-pin="1">1</b>
  <img src="https://example.com/logo.png" alt="">
</section>
```

Create `docs/ux/mockups/tests/fixtures/no_pin.html`:

```html
<section class="screen" id="s-97-nopin" data-screen-title="nopin">
  <p class="legend">nopin</p>
  <div style="color: var(--ink)">no pins here</div>
</section>
```

Create `docs/ux/mockups/tests/fixtures/good.html`:

```html
<section class="screen" id="s-96-good" data-screen-title="good">
  <p class="legend">good</p>
  <b class="pin" data-pin="1">1</b>
  <div style="color: var(--ink); background: rgba(58,191,248,.08)">
    <a href="https://groq.com" rel="noreferrer">groq.com</a>
  </div>
</section>
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd docs/ux/mockups && python3 -m unittest discover -s tests -p 'test_qa.py' -v`
Expected: ERROR — `ModuleNotFoundError: No module named 'qa'`.

- [ ] **Step 3: Write the implementation**

Create `docs/ux/mockups/qa.py`:

```python
#!/usr/bin/env python3
"""Gate for the Darkrouter mockup set.

Fragments declare no colour of their own and load nothing from the network,
so the built page is provably self-contained and provably themed by one
stylesheet. Both properties are what let the set be published as an artifact
and trusted as a design reference.
"""
import re
import sys
from html.parser import HTMLParser
from pathlib import Path

HERE = Path(__file__).resolve().parent
FRAGMENTS = HERE / "fragments"

# Colour lives in darkrouter-ui.css. A hex in a fragment is a value that
# escaped the token system and will not follow the light-mode swap.
HEX = re.compile(r"#[0-9a-fA-F]{3,8}\b")

# Attributes that fetch, wherever they appear. The optional scheme catches
# protocol-relative URLs, which fetch just as happily as an absolute one.
FETCHING_ATTR = re.compile(
    r"""\b(?:src|srcset|xlink:href|poster)\s*=\s*["'](?:https?:)?//""",
    re.IGNORECASE,
)

# Stylesheet fetches.
CSS_FETCH = re.compile(r"""(?:@import\s+|url\(\s*)["']?(?:https?:)?//""", re.IGNORECASE)

# href only fetches on elements that LOAD what it points at. On an anchor it is
# navigation, which is why <a href="https://groq.com"> stays legal.
LOADING_HREF = re.compile(
    r"""<\s*(?:link|use|image|iframe|embed|object|track|source)\b[^>]*?"""
    r"""\bhref\s*=\s*["'](?:https?:)?//""",
    re.IGNORECASE | re.DOTALL,
)

FONT_SIZE = re.compile(r"font-size\s*:\s*(\d+(?:\.\d+)?)px")
MAX_FONT_PX = 30.0


def class_token(name: str) -> re.Pattern:
    """Match one class among several. `class="legend prose"` still counts."""
    return re.compile(rf'class\s*=\s*["\'][^"\']*\b{name}\b[^"\']*["\']')

VOID = {
    "area", "base", "br", "col", "embed", "hr", "img", "input",
    "link", "meta", "param", "source", "track", "wbr",
}


class Balance(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.stack = []
        self.problems = []
        self.ids = {}

    def handle_starttag(self, tag, attrs):
        for k, v in attrs:
            if k == "id" and v:
                if v in self.ids:
                    self.problems.append(
                        f"duplicate id {v!r} (first seen line {self.ids[v]}, "
                        f"again line {self.getpos()[0]})"
                    )
                else:
                    self.ids[v] = self.getpos()[0]
        if tag not in VOID:
            self.stack.append((tag, self.getpos()[0]))

    def handle_endtag(self, tag):
        if tag in VOID:
            return
        if not self.stack:
            self.problems.append(f"unbalanced: </{tag}> with nothing open "
                                 f"at line {self.getpos()[0]}")
            return
        open_tag, line = self.stack.pop()
        if open_tag != tag:
            self.problems.append(
                f"unbalanced: <{open_tag}> opened line {line} closed by "
                f"</{tag}> at line {self.getpos()[0]}"
            )

    def finish(self):
        for tag, line in self.stack:
            self.problems.append(f"unbalanced: <{tag}> opened line {line} never closed")
        return self.problems


def check_fragment(path: Path) -> list[str]:
    text = path.read_text(encoding="utf-8")
    problems = []

    for m in HEX.finditer(text):
        line = text.count("\n", 0, m.start()) + 1
        problems.append(f"{path.name}:{line}: raw hex {m.group(0)} — use var(--token) or rgba()")

    for pattern in (FETCHING_ATTR, CSS_FETCH, LOADING_HREF):
        for m in pattern.finditer(text):
            line = text.count("\n", 0, m.start()) + 1
            problems.append(f"{path.name}:{line}: external resource load — the page must be self-contained")

    for m in FONT_SIZE.finditer(text):
        if float(m.group(1)) > MAX_FONT_PX:
            line = text.count("\n", 0, m.start()) + 1
            problems.append(
                f"{path.name}:{line}: font-size {m.group(1)}px exceeds the {MAX_FONT_PX:.0f}px ceiling"
            )

    if not class_token("pin").search(text):
        problems.append(f"{path.name}: no annotation pin — every screen must be annotated")

    legends = len(class_token("legend").findall(text))
    if legends != 1:
        problems.append(f"{path.name}: found {legends} legend elements, expected exactly 1")

    if 'data-screen-title=' not in text:
        problems.append(f"{path.name}: missing data-screen-title")

    for forbidden in ("<html", "<head", "<body", "<style"):
        if forbidden in text.lower():
            problems.append(f"{path.name}: fragment contains {forbidden}> — fragments are sections only")

    return problems


def check_index(path: Path) -> list[str]:
    text = path.read_text(encoding="utf-8")
    parser = Balance()
    parser.feed(text)
    # finish() appends any never-closed tags to self.problems and returns it,
    # so duplicate-id findings collected during the parse are already in there.
    problems = parser.finish()
    if "fonts.googleapis.com" in text:
        problems.append("index: links fonts.googleapis.com — fonts must be self-hosted")
    return problems


def main() -> int:
    problems = []
    fragments = sorted(FRAGMENTS.glob("*.html"))
    if not fragments:
        print("qa: no fragments found", file=sys.stderr)
        return 1
    for f in fragments:
        problems.extend(check_fragment(f))

    index = HERE / "index.html"
    if index.exists():
        problems.extend(check_index(index))

    if problems:
        for p in problems:
            print(f"FAIL {p}", file=sys.stderr)
        print(f"\nqa: {len(problems)} problem(s) across {len(fragments)} fragment(s)", file=sys.stderr)
        return 1

    print(f"qa: PASS — {len(fragments)} fragment(s) clean")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd docs/ux/mockups && python3 -m unittest discover -s tests -p 'test_qa.py' -v`
Expected: OK, 12 tests.

- [ ] **Step 5: Commit**

```bash
git add docs/ux/mockups/qa.py docs/ux/mockups/tests
git commit -m "test(ux): gate the mockup set before there is a set"
```

---

## Task 2: The assembler, the shell, the chrome, and the tokens

**Files:**
- Create: `docs/ux/mockups/build.py`
- Create: `docs/ux/mockups/_shell.html`
- Create: `docs/ux/mockups/_chrome.html`
- Create: `docs/ux/mockups/darkrouter-ui.css`
- Create: `docs/ux/mockups/fonts/` (four woff2 files)
- Create: `docs/ux/mockups/fragments/99-smoke.html` (deleted in step 6)
- Test: `docs/ux/mockups/tests/test_build.py`

**Interfaces:**
- Consumes: `qa.check_fragment`, `qa.check_index` from Task 1.
- Produces: `build.build() -> tuple[Path, Path]` returning the written `(index_path, artifact_path)`. The shell exposes CSS classes every later task depends on: `.screen`, `.legend`, `.pin`, `.chrome`, `.rail`, `.topbar`, `.panel`, `.well`, `.ladder`, `.ladder-row`, `.rank`, `.spine`, `.mark`, `.mark-skipped`, `.mark-cooling`, `.mark-failed`, `.mark-served`, `.stub`, `.stub-dashed`, `.reason`, `.reason-code`, `.reason-prose`, `.legend-caps`, `.readout`, `.unit`, `.mono`, `.prose`, `.state-pip`, `.chip`, `.table`, `.row`. Body classes `pins-on` and `theme-light` drive the two toggles.

- [ ] **Step 1: Vendor the fonts**

IBM Plex Sans and IBM Plex Mono are SIL OFL. Fetch four woff2 files — Sans 400, Sans 600, Mono 400, Mono 500 — into `docs/ux/mockups/fonts/`. Mono 600 is used only for the 30px readout; include it as a fifth file if the readout looks thin, otherwise `font-weight: 600` on the 500 face is acceptable for a mockup.

```bash
cd docs/ux/mockups && mkdir -p fonts && cd fonts
for f in ibm-plex-sans-400 ibm-plex-sans-600 ibm-plex-mono-400 ibm-plex-mono-500; do
  echo "fetch $f.woff2 from the IBM Plex release into $(pwd)/$f.woff2"
done
ls -la
```

Source: https://github.com/IBM/plex releases, or the `@fontsource/ibm-plex-sans` and `@fontsource/ibm-plex-mono` npm packages (`npm pack` then extract `files/*.woff2`). Do not link Google Fonts — `qa.py` rejects it and the artifact CSP would block it anyway.

Add `THIRD_PARTY_NOTICES.md` entries for both families.

- [ ] **Step 2: Write the failing test**

Create `docs/ux/mockups/tests/test_build.py`:

```python
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import build
import qa


class TestBuild(unittest.TestCase):
    def setUp(self):
        self.index, self.artifact = build.build()

    def test_build_produces_both_outputs(self):
        self.assertTrue(self.index.exists())
        self.assertTrue(self.artifact.exists())

    def test_index_is_a_document_and_artifact_is_not(self):
        head = self.index.read_text(encoding="utf-8").lstrip().lower()
        self.assertTrue(head.startswith("<!doctype html>"))
        body = self.artifact.read_text(encoding="utf-8").lower()
        for wrapper in ("<!doctype", "<html", "<head", "<body"):
            self.assertNotIn(wrapper, body, f"artifact.html must not contain {wrapper}")

    def test_css_is_inlined_not_linked(self):
        text = self.index.read_text(encoding="utf-8")
        self.assertIn("<style>", text)
        self.assertNotIn('rel="stylesheet"', text)
        self.assertIn("--ground", text, "token stylesheet was not inlined")

    def test_fonts_are_embedded_as_data_uris(self):
        text = self.index.read_text(encoding="utf-8")
        self.assertIn("data:font/woff2;base64,", text)
        self.assertNotIn("fonts.googleapis.com", text)

    def test_svg_ids_are_suffixed_per_screen(self):
        # Two screens may each define a gradient called "fade"; unsuffixed they
        # collide in one document and the second silently renders the first.
        problems = [p for p in qa.check_index(self.index) if "duplicate id" in p]
        self.assertEqual(problems, [])

    def test_built_index_passes_qa(self):
        self.assertEqual(qa.check_index(self.index), [])


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd docs/ux/mockups && python3 -m unittest discover -s tests -p 'test_build.py' -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'build'`.

- [ ] **Step 4: Write `build.py`**

Create `docs/ux/mockups/build.py`:

```python
#!/usr/bin/env python3
"""Assemble the mockup fragments into two self-contained files.

index.html is a document for a browser. artifact.html is the same content
without the document wrapper, because the Artifact tool supplies its own
<!doctype>/<head>/<body> and a nested one is invalid.
"""
import base64
import re
from pathlib import Path

HERE = Path(__file__).resolve().parent
FRAGMENTS = HERE / "fragments"
FONTS = HERE / "fonts"

# An id defined inside one screen's SVG is global to the assembled document.
# Two screens that both call a gradient "fade" would leave the second
# rendering the first one's paint. Suffixing per screen makes that impossible.
SVG_ID = re.compile(r'(\bid="|\burl\(#|\bxlink:href="#|\bhref="#)([A-Za-z][\w-]*)')
SCREEN_ID = re.compile(r'<section[^>]*\bid="([^"]+)"')


def font_face_block() -> str:
    faces = {
        "ibm-plex-sans-400.woff2": ("IBM Plex Sans", 400, "normal"),
        "ibm-plex-sans-600.woff2": ("IBM Plex Sans", 600, "normal"),
        "ibm-plex-mono-400.woff2": ("IBM Plex Mono", 400, "normal"),
        "ibm-plex-mono-500.woff2": ("IBM Plex Mono", 500, "normal"),
    }
    out = []
    for name, (family, weight, style) in faces.items():
        path = FONTS / name
        if not path.exists():
            raise SystemExit(f"build: missing font {path}")
        b64 = base64.b64encode(path.read_bytes()).decode("ascii")
        out.append(
            f"@font-face{{font-family:'{family}';font-weight:{weight};"
            f"font-style:{style};font-display:swap;"
            f"src:url(data:font/woff2;base64,{b64}) format('woff2')}}"
        )
    return "\n".join(out)


def suffix_svg_ids(fragment: str, screen_id: str) -> str:
    return SVG_ID.sub(lambda m: f"{m.group(1)}{m.group(2)}--{screen_id}", fragment)


def collect() -> list[tuple[str, str, str]]:
    """Return (screen_id, title, html) per fragment, in filename order."""
    out = []
    for path in sorted(FRAGMENTS.glob("*.html")):
        text = path.read_text(encoding="utf-8")
        m = SCREEN_ID.search(text)
        if not m:
            raise SystemExit(f"build: {path.name} has no <section id=…>")
        screen_id = m.group(1)
        title = re.search(r'data-screen-title="([^"]*)"', text)
        out.append((screen_id, title.group(1) if title else screen_id,
                    suffix_svg_ids(text, screen_id)))
    return out


def toc(screens: list[tuple[str, str, str]]) -> str:
    items = "".join(
        f'<li><a href="#{sid}"><span class="toc-n">{i:02d}</span>{title}</a></li>'
        for i, (sid, title, _) in enumerate(screens)
    )
    return f'<nav class="toc"><ol>{items}</ol></nav>'


def build() -> tuple[Path, Path]:
    screens = collect()
    css = (HERE / "darkrouter-ui.css").read_text(encoding="utf-8")
    shell = (HERE / "_shell.html").read_text(encoding="utf-8")

    content = toc(screens) + "\n" + "\n".join(html for _, _, html in screens)
    style = f"<style>\n{font_face_block()}\n{css}\n</style>"

    document = (
        shell.replace("<!--STYLE-->", style)
             .replace("<!--CONTENT-->", content)
    )

    index = HERE / "index.html"
    index.write_text(document, encoding="utf-8", newline="\n")

    # The Artifact tool wraps what it is given, so hand it content only.
    inner = re.search(r"<body[^>]*>(.*)</body>", document, re.S)
    if not inner:
        raise SystemExit("build: shell has no <body>")
    artifact = HERE / "artifact.html"
    artifact.write_text(style + "\n" + inner.group(1).strip(),
                        encoding="utf-8", newline="\n")

    print(f"build: {len(screens)} screen(s) -> {index.name} "
          f"({index.stat().st_size // 1024} KB), {artifact.name} "
          f"({artifact.stat().st_size // 1024} KB)")
    return index, artifact


if __name__ == "__main__":
    build()
```

- [ ] **Step 5: Write `_shell.html`**

Create `docs/ux/mockups/_shell.html`. The two toggles live here and nowhere else, so every screen inherits them:

```html
<!doctype html>
<html lang="en" data-mode="dark">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Darkrouter — operator console mockups</title>
    <!--STYLE-->
  </head>
  <body>
    <header class="doc-head">
      <h1 class="doc-title">darkrouter / operator console</h1>
      <p class="doc-keys">
        <kbd>A</kbd> annotations &middot; <kbd>T</kbd> light mode
      </p>
    </header>
    <!--CONTENT-->
    <script>
      document.addEventListener("keydown", function (e) {
        if (e.metaKey || e.ctrlKey || e.altKey) return;
        var t = e.target;
        if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA")) return;
        var k = e.key.toLowerCase();
        if (k === "a") document.body.classList.toggle("pins-on");
        if (k === "t") {
          var light = document.body.classList.toggle("theme-light");
          document.documentElement.setAttribute("data-mode", light ? "light" : "dark");
        }
      });
    </script>
  </body>
</html>
```

- [ ] **Step 6: Write `darkrouter-ui.css` and `_chrome.html`**

`darkrouter-ui.css` declares every token from Global Constraints under `:root` (dark) and `body.theme-light` (light), then the shared classes listed in this task's Interfaces block. Two rules matter more than the rest:

```css
/* The well sits at the extreme end of the value range while the panel sits
   mid-range. Dark: the well is darker than the panel. Light: brighter. This
   inversion is why light and dark are structurally different screens rather
   than a palette swap, and it is what keeps the readout at the highest
   contrast in the frame in both modes. */
:root      { --panel: #191C1F; --well: #090A0C; }
body.theme-light { --panel: #F9FAFB; --well: #FFFFFF; }

/* Pins are inert until A is pressed, so a screenshot of the design reads as
   the design and a screenshot with pins reads as the specification. */
.pin { display: none; }
body.pins-on .pin { display: inline-flex; }
```

`_chrome.html` is the 200px rail with the three nav groups (Operate: Overview, Requests, Usage / Configure: Providers, Models, Routing / Use: Playground, Connect) plus Settings pinned at the bottom, and the 44px top bar carrying the route-style page title and the ⌘K affordance. It is copied verbatim into each console screen; the page title and the active rail item are the only things that differ.

- [ ] **Step 7: Write the smoke fragment and build**

Create `docs/ux/mockups/fragments/99-smoke.html`:

```html
<section class="screen" id="s-99-smoke" data-screen-title="smoke">
  <p class="legend">Smoke fragment — proves the pipeline, deleted in this task.</p>
  <b class="pin" data-pin="1">1</b>
  <div class="panel"><span class="mono">groq/openai-gpt-oss-120b</span></div>
</section>
```

Run: `cd docs/ux/mockups && python3 build.py && python3 qa.py`
Expected: `build: 1 screen(s) -> index.html (…), artifact.html (…)` then `qa: PASS — 1 fragment(s) clean`.

- [ ] **Step 8: Run the tests to verify they pass**

Run: `cd docs/ux/mockups && python3 -m unittest discover -s tests -v`
Expected: OK, 14 tests (8 qa + 6 build).

- [ ] **Step 9: Delete the smoke fragment, rebuild, commit**

```bash
cd docs/ux/mockups
rm fragments/99-smoke.html
# index.html and artifact.html still hold the smoke screen that was just
# deleted. build.py does not refuse an empty fragment set — it would write an
# index with no screens — so remove the built files rather than rebuilding.
# The first real index arrives in Task 3.
rm -f index.html artifact.html
cd /root/repositories/darkrouter
git add -A docs/ux/mockups THIRD_PARTY_NOTICES.md
git commit -m "build(ux): assemble mockup fragments into a self-contained page"
```

The committed tree therefore has `build.py`, `qa.py`, `_shell.html`, `_chrome.html`, `darkrouter-ui.css`, `fonts/` and `tests/`, and no fragments and no built output. That is the intended state at the end of this task.

---

## Task 3: The design language screen, and the screenshot harness

**Files:**
- Create: `docs/ux/mockups/check.py`
- Create: `docs/ux/mockups/fragments/00-design-language.html`
- Modify: `docs/ux/mockups/darkrouter-ui.css` (add whatever the screen proves is missing)

**Interfaces:**
- Consumes: `build.build`, `qa.check_fragment`.
- Produces: `check.py`, invoked as `python3 check.py <fragment-stem> [--light] [--no-pins]`, which writes `docs/ux/mockups/.check/<stem>.png`. Every later screen task calls it. Also produces the visual vocabulary — the classes and their rendered appearance — that all sixteen remaining screens copy.

- [ ] **Step 1: Write `check.py`**

Headless Chrome's `--screenshot` renders the top of the document and does **not** scroll to a `#anchor`, so screenshotting the assembled `index.html` always yields the first screen. Wrapping the single fragment in a minimal document is what makes per-screen verification possible.

Create `docs/ux/mockups/check.py`:

```python
#!/usr/bin/env python3
"""Screenshot one fragment on its own.

Chrome's --screenshot does not honour a #fragment anchor, so a per-screen
image has to come from a per-screen document rather than from index.html.
"""
import argparse
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import build

HERE = Path(__file__).resolve().parent
OUT = HERE / ".check"
CHROME = "/usr/bin/google-chrome"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("stem", help="fragment filename without .html, e.g. 02-overview")
    ap.add_argument("--light", action="store_true")
    ap.add_argument("--no-pins", action="store_true")
    ap.add_argument("--width", type=int, default=1440)
    ap.add_argument("--height", type=int, default=1100)
    args = ap.parse_args()

    fragment = HERE / "fragments" / f"{args.stem}.html"
    if not fragment.exists():
        print(f"check: no such fragment {fragment}", file=sys.stderr)
        return 1

    OUT.mkdir(exist_ok=True)
    body_classes = []
    if not args.no_pins:
        body_classes.append("pins-on")
    if args.light:
        body_classes.append("theme-light")

    css = (HERE / "darkrouter-ui.css").read_text(encoding="utf-8")
    style = f"<style>\n{build.font_face_block()}\n{css}\n</style>"
    doc = (
        f'<!doctype html><html lang="en" '
        f'data-mode="{"light" if args.light else "dark"}"><head>'
        f'<meta charset="UTF-8">{style}</head>'
        f'<body class="{" ".join(body_classes)}">'
        f'{fragment.read_text(encoding="utf-8")}</body></html>'
    )
    tmp = OUT / f"{args.stem}.check.html"
    tmp.write_text(doc, encoding="utf-8", newline="\n")

    suffix = ("-light" if args.light else "") + ("-nopins" if args.no_pins else "")
    png = OUT / f"{args.stem}{suffix}.png"
    cmd = [
        CHROME, "--headless", "--disable-gpu", "--no-sandbox",
        f"--window-size={args.width},{args.height}",
        f"--screenshot={png}", f"file://{tmp}",
    ]
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=90)
    if r.returncode != 0 or not png.exists():
        print(r.stderr[-2000:], file=sys.stderr)
        return 1
    print(f"check: {png} ({png.stat().st_size // 1024} KB)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
```

Add `docs/ux/mockups/.check/` to `.gitignore` — the PNGs are verification scratch, not deliverables.

- [ ] **Step 2: Write the design language screen**

Create `docs/ux/mockups/fragments/00-design-language.html`. This screen is the reference every other screen is checked against, so it must render each token and rule as an actual specimen rather than describing it. Required regions, each with its own pin:

1. **Palette** — one swatch row per token from Global Constraints, each swatch showing the token name in mono and the measured contrast ratio against its intended partner. The dark and light hexes are in the CSS, not in this fragment, so the swatches change under `T` and the ratios are labelled per mode.
2. **The health/decision split** — two rows side by side. Left: three provider identity pips in `--state-healthy`, `--state-cooling`, `--state-failed` labelled "what the provider is". Right: a served mark and a skipped mark in `--trace` and `--bezel` labelled "what the router decided". A caption stating that a healthy provider can be skipped and a degraded one can serve.
3. **Type scale** — all eight roles from Global Constraints as live specimens with their spec printed beside each in `--legend`. Include a tabular-numeral proof: two stacked figures `1,481.20` and `9,003.75` showing the decimals aligning.
4. **The unit rule** — `142ms`, `1.2k`, `$0.0021`, `26.4k/1.9k` rendered with the unit at 10px in `--legend` and no space, beside the same values set naively, so the difference is visible.
5. **Surface** — panel on ground with its 1px seam; a well cut into a panel showing the inset engraving; the single permitted overlay shadow; and a 2px radius specimen beside 0px and 4px so the choice is legible.
6. **Density** — a 30px table row, a 30px control, and the 1px panel seam measured with a rule.
7. **Mono versus sans** — the semantic test stated as a rule, with four examples classified: `claude-opus-4-20250514` (mono, off the wire), `01JG7X…` (mono), "The previous configuration is still serving." (sans, a person telling you something), "Cooling until 14:22" (sans prose plus mono value).
8. **Motion** — the three permitted animations, each as a hover-triggered specimen, with a note that nothing else animates.

Required pins on this screen, one per region, each stating component, tokens, and rule:

- `1 · Palette — tokens declared in darkrouter-ui.css :root and body.theme-light. Fragments reference var(--token) only; qa.py rejects raw hex.`
- `2 · Health/decision split — state colours are provider identity, --trace is router verdict. Spec §3.1. Amber appears only as a health pip and a cooling skip mark.`
- `3 · Type scale — IBM Plex Sans/Mono, self-hosted. 30px ceiling enforced by qa.py.`
- `4 · Unit rule — 10px --legend, no space, so the value keeps its optical weight.`
- `5 · Surface — separation is value step + hairline. --shadow-card: none. One shadow, on --overlay only.`
- `6 · Density — compact: 30px rows, 30px controls, 44px topbar, 200px rail, 1px panel seams.`
- `7 · Type lanes — mono means this came off the wire, sans means a person is telling you something.`
- `8 · Motion — 120ms hover, 160ms disclosure, 90ms value swap. Nothing else moves.`

The single `.legend` on this screen explains the pin layer itself: what a pin contains and that `A` toggles it.

- [ ] **Step 3: Build, gate, and look at it**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 00-design-language
python3 check.py 00-design-language --light
python3 check.py 00-design-language --no-pins
```

Expected: `qa: PASS — 1 fragment(s) clean`, and three PNGs written. Open all three. The dark and light images must differ in **well polarity** — the well darker than the panel in dark, brighter in light — not merely in overall lightness. If they differ only in lightness, the CSS is a palette swap and the structural rule is not implemented; fix `darkrouter-ui.css` before continuing.

- [ ] **Step 4: Verify the contrast claims are true, not asserted**

The palette region prints ratios. Compute them rather than trusting the spec table:

```bash
cd docs/ux/mockups && python3 - <<'PY'
def lum(h):
    h = h.lstrip('#')
    c = [int(h[i:i+2], 16) / 255 for i in (0, 2, 4)]
    c = [x / 12.92 if x <= 0.04045 else ((x + 0.055) / 1.055) ** 2.4 for x in c]
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]

def ratio(a, b):
    la, lb = sorted((lum(a), lum(b)), reverse=True)
    return (la + 0.05) / (lb + 0.05)

pairs = [
    ("ink/panel dark", "#EBEDF0", "#191C1F", 4.5),
    ("ink/well dark", "#EBEDF0", "#090A0C", 4.5),
    ("muted/panel dark", "#98A1A9", "#191C1F", 4.5),
    ("legend/panel dark", "#808A93", "#191C1F", 4.5),
    ("trace/panel dark", "#3ABFF8", "#191C1F", 3.0),
    ("bezel/panel dark", "#6A737C", "#191C1F", 3.0),
    ("ink/panel light", "#1A2128", "#F9FAFB", 4.5),
    ("ink/ground light", "#1A2128", "#E5E8EB", 4.5),
    ("muted/ground light", "#5A636D", "#E5E8EB", 4.5),
    ("legend/panel light", "#68737D", "#F9FAFB", 4.5),
    ("accent-ink/panel light", "#0369A0", "#F9FAFB", 4.5),
    ("trace/panel light", "#0284C5", "#F9FAFB", 3.0),
    ("bezel/panel light", "#818C98", "#F9FAFB", 3.0),
    ("focus/ground light", "#0284C5", "#E5E8EB", 3.0),
]
bad = 0
for name, fg, bg, need in pairs:
    r = ratio(fg, bg)
    ok = r >= need
    bad += not ok
    print(f"{'PASS' if ok else 'FAIL'} {name:26s} {r:5.2f}:1 (needs {need})")
raise SystemExit(1 if bad else 0)
PY
```

Expected: every line PASS, exit 0. A FAIL means the spec's palette is wrong and §3.1 needs correcting before sixteen screens inherit the error.

- [ ] **Step 5: Commit**

```bash
git add docs/ux/mockups .gitignore
git commit -m "feat(ux): establish the graticule bench design language"
```

---

## Task 4: The ladder specimen

**Files:**
- Create: `docs/ux/mockups/fragments/01-ladder-specimen.html`
- Modify: `docs/ux/mockups/darkrouter-ui.css` (the `.ladder*` classes)

**Interfaces:**
- Consumes: every class from Task 3.
- Produces: the `.ladder` markup pattern that Tasks 7, 12 and 13 copy verbatim. Its exact class names and DOM shape are the contract: `.ladder > .ladder-row > (.rank, .mark, .stub, .target, .reason, .latency-bar)`.

This is the spine of the whole console (spec §4) and gets its own screen because three later screens embed it and must not each invent their own.

- [ ] **Step 1: Build the specimen**

The screen shows the same eight-candidate ladder three times — the three modes — plus a state gallery.

**Mode 1, retrospective** (as a request trace shows it). Eight candidates, marks **filled** because the attempts happened:

| Rank | Target | State | Reason / result |
|---|---|---|---|
| 01 | `groq/openai-gpt-oss-120b` | attempted, failed | `http · 429 rate limited` — 412ms |
| 02 | `cerebras/gpt-oss-120b` | attempted, failed | `timeout · 30000ms elapsed before first byte` — 30000ms |
| 03 | `together/openai-gpt-oss-120b` | served | 1,847ms, ttft 284ms |
| 04 | `deepinfra/gpt-oss-120b` | below the served row | — |
| 05 | `openrouter/openai/gpt-oss-120b` | below the served row | — |
| 06 | `anthropic/claude-opus-4-20250514` | below the served row | — |
| 07 | `bedrock/us.anthropic.claude-opus-4` | below the served row | — |
| 08 | `vertex/gemini-2.5-pro` | below the served row | — |

Rows 04–08 demonstrate **the termination rule**: the spine drops from `--hairline` to `--hairline-subtle`, marks go hollow at 5px in `--hairline-subtle`, text at 45% opacity. The ladder must visibly run out of ink.

**Mode 2, predictive** (the Routing dry-run). The same eight candidates with marks **outlined** rather than filled, no latencies, no served row — because nothing has been sent. Two rows carry skip reasons that would apply right now:

- `02 cerebras/gpt-oss-120b` — `cooling · 4m 12s remaining`, hollow square stroked in `--state-cooling`, with a live countdown in the reason chip
- `06 anthropic/claude-opus-4-20250514` — `no_capability · vision demanded, model has none`

**Mode 3, compressed** (the Models catalog). The same candidates as a single scannable column: rank, a 9px mark, and the provider id only — no reason column, no latency bar. Show twelve rows so the "barcode of sigils" reading is evident at a glance.

**State gallery.** The four marks isolated at 4× with their construction labelled:

| State | Mark | Stub | Row |
|---|---|---|---|
| Skipped | hollow 7px square, spine passes through, 1px `--bezel` | none | target `--ink-muted`, no background |
| Skipped, cooling | hollow 7px square stroked `--state-cooling` | none | reason chip carries a countdown |
| Attempted, failed | filled 7px square `--state-failed` | 12px **dashed**, 3px on 3px, `--state-failed` | latency is time-to-failure |
| Served | filled 7px square `--trace` | 12px **solid** `--trace` | 1px `--trace` left border, served wash |

Also required on this screen:

- **Rank gutter detail** — two-digit zero-padded ordinals, 11px mono, `--legend`, right-aligned; every fifth rank draws a 5px tick into the spine in `--bezel`, others 3px.
- **Reason format** — six examples proving the fixed `machine_code · plain sentence` rhythm: `model_not_offered · target does not serve claude-sonnet-4-6`, `cooling · 4m 12s remaining`, `no_capability · vision demanded, model has none`, `context · 210k requested, 200k available`, `no_credential · no key configured for this provider`, `adapter_surface · darkrouter's bedrock adapter cannot speak rerank`.
- **Latency micro-bar** — a 2px bar in the row's right margin scaled across the ladder's own maximum, turning the ladder into a waterfall with no second axis.
- **Greyscale proof** — the retrospective ladder repeated inside a `filter: grayscale(1)` container, proving all four states remain distinguishable with colour stripped. This is the screen's most important panel: it is the evidence for the spec's claim that silhouette carries meaning and colour is a second channel.

Pins:

- `1 · Ladder geometry — 28px rank gutter, 1px spine, 12px stub lane, then target/reason/latency. Identical in all three modes.`
- `2 · Termination rule — below the served row the spine drops to --hairline-subtle and marks go hollow-5px at 45% opacity. Spec §4.`
- `3 · Fill vs outline — filled means it happened (trace), outlined means it would happen (dry-run). No legend needed.`
- `4 · Solid vs dashed stub — solid served, dashed attempted-and-failed. Legible at 50% zoom.`
- `5 · Reason format — machine_code · plain sentence. The code is what you grep for, the sentence is what you read.`
- `6 · Greyscale proof — four states distinguishable with colour removed. Colour is the second channel, never the only one.`
- `7 · Rank gutter — zero-padded so the column stays optically stable past nine candidates; every fifth rank ticks 5px.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 01-ladder-specimen
python3 check.py 01-ladder-specimen --light
```

Expected: qa PASS; both PNGs written.

- [ ] **Step 3: Verify the greyscale claim by measurement, not by eye**

```bash
cd docs/ux/mockups && python3 - <<'PY'
# The four marks must differ in shape, not only in hue. Convert the state
# gallery region to greyscale and confirm the four cells are not identical.
import subprocess, sys
print("Open .check/01-ladder-specimen.png and confirm, in the greyscale panel:")
for line in [
    "  skipped        hollow square, spine passes through, NO stub",
    "  skipped/cool   hollow square, NO stub, countdown chip present",
    "  failed         FILLED square, DASHED stub",
    "  served         FILLED square, SOLID stub, left border on the row",
]:
    print(line)
print("\nIf any two are indistinguishable in the greyscale panel, the mark set")
print("has failed spec §4 and must be redrawn before Tasks 7, 12 and 13.")
PY
```

Expected: all four visually distinct in the greyscale panel. If not, redraw before continuing — three later screens depend on this vocabulary.

- [ ] **Step 4: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): specify the routing ladder in all three modes"
```

---

## Wave 1 — Operate

Tasks 5 to 8 are independent of one another and can be built in parallel. All four consume the chrome from Task 2 and the vocabulary from Tasks 3 and 4, and none consumes anything from the others.

## Task 5: Overview

**Files:**
- Create: `docs/ux/mockups/fragments/02-overview.html`

**Interfaces:**
- Consumes: `_chrome.html`, all classes from Tasks 3 and 4.
- Produces: `.sparkline`, `.health-tile`, `.ops-footer` — reused by Task 9.

Spec §6.1. Page title `operate/overview`, rail item Overview active.

- [ ] **Step 1: Build the screen**

Regions:

1. **Config-invalid banner**, shown in its alarmed state so the design is proven rather than assumed: the error text in mono from `config.error`, and beneath it in sans "The previous configuration is still serving." from `config.serving`. Full-bleed above the live strip, `--state-failed` 1px rule, no fill wash heavier than 8% — an alarm that shouts is one an operator learns to dismiss.
2. **Live strip** — four readouts, each 30px mono over a 40px sparkline in a well: `requests_per_min` `14.2`, `error_rate` `2.4%`, latency `p50 890ms / p95 4.1s`, `today_spend` `$3.47`. Each carries its window in `--legend`: "over the last 5 min" from `window_sec`.
3. **Provider health grid** — eight tiles from `overview.providers[]`. Use `groq` healthy 2 credentials, `cerebras` degraded 1 cooling, `anthropic` healthy 1, `openrouter` healthy 3, `together` healthy 1, `deepinfra` disabled, `bedrock` unconfigured 0 credentials, `vertex` healthy needs_reauth. Each tile: name, a state pip in the state colour, credential count, cooling count when non-zero. The `vertex` tile shows the "needs reconnection" callout, because it is the one state only the operator can fix.
4. **Recent failovers strip** — five rows from requests where `attempts > 1`: timestamp, `alias → provider/final_model`, an `attempts` chip, `total_ms`. Each row links to its trace. This is the region that does not exist today.
5. **Ops footer** — a single hairline-separated line in `--legend`: `v0.9.3 · up 6d 14h · 1,204,881 log records written · 0 dropped`, from `/healthz`. The dropped counter is rendered in `--state-cooling` when non-zero, and this screen shows the zero case with a tooltip explaining that a non-zero value means usage figures are a lower bound.
6. **Loading treatment** — one readout shown mid-load with the 2px determinate bar at the top of its well, proving the spec's replacement for the current blank-screen-on-load behaviour.

Pins:

- `1 · Config banner — GET /api/config {valid,error,serving}. Shown only when valid=false. Error in mono, reassurance in sans.`
- `2 · Live strip — GET /api/overview {requests_per_min,error_rate,window_sec,today_spend{micros,priced}} + §8.2 percentile and series extensions. 30px readout is the type ceiling.`
- `3 · Health grid — GET /api/overview providers[]{id,name,state,cooling,credentials,needs_reauth}. State pip is provider identity, never --trace.`
- `4 · Recent failovers — GET /api/requests?limit=5 filtered attempts>1. New region; a fleet error rate hides one provider degrading.`
- `5 · Ops footer — GET /healthz {version,uptime,log_records_written,log_records_dropped}. Dropped>0 turns amber: usage is then a lower bound.`
- `6 · Loading — 2px determinate bar in --trace at the top of the well. Replaces today's "if (!data) return null" blank screen.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 02-overview && python3 check.py 02-overview --light
python3 check.py 02-overview --no-pins
```

Expected: qa PASS; three PNGs. Confirm in the `--no-pins` image that no pin marks are visible — the design must read as a design, not a specification.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design the overview screen"
```

---

## Task 6: Requests

**Files:**
- Create: `docs/ux/mockups/fragments/03-requests.html`

**Interfaces:**
- Consumes: `_chrome.html`, Task 3 vocabulary.
- Produces: `.filter-bar`, `.combo`, `.table`, `.chip-failover`, `.chip-path`, `.newer-pill` — reused by Tasks 9 and 12.

Spec §6.2. Page title `operate/requests`.

- [ ] **Step 1: Build the screen**

Regions:

1. **Filter bar** — six controls, all real controls rather than the free-text boxes the current screen uses: provider combobox (open, showing `groq`, `cerebras`, `anthropic`, `openrouter`, `together` with counts), model combobox, alias combobox, surface segmented control (`llm`, `embedding`, `image`, `stt`, `tts`, `rerank`, `moderation`), status segmented control (`success`, `error`), and a time-range picker showing `last 24h` with a custom range open. A "Reset" ghost button appears because filters are active.
2. **Saved views** — three chips above the filter bar: `failovers only`, `errors today`, `passthrough misses`, the first one active.
3. **The table** — 14 rows, 30px each. Columns: time, surface, model (`alias → model` where an alias resolved), provider, status, attempts, tokens `in/out`, latency. Row chips make routing legible without opening anything: a `failover ×3` chip in `--state-cooling` on two rows, a `passthrough` chip and a `translated` chip in `--legend` on others, an estimated-token marker on one count-tokens row.
4. **The "newer" pill** — a floating `3 newer` pill at the top of the table, proving that polling queues new rows rather than shifting the scroll position.
5. **Column visibility menu** — open, showing checkboxes over the eight columns, and a CSV export item.
6. **Keyset paging** — a "Load more" affordance at the foot with the cursor's meaning explained in the legend: pages accumulate, they do not replace.
7. **Empty state** — a second, smaller panel showing what the table looks like under filters that match nothing: a legend explaining what the well would show, not a bare "no results".

Data: request ids are 26-character ULIDs (`01JG7XQ2M4K8VBNR3TDYW5EZ9C`). Models are real: `openai/gpt-oss-120b`, `claude-opus-4-20250514`, `gemini-2.5-pro`, `text-embedding-3-large`, `bge-reranker-v2-m3`.

Pins:

- `1 · Filter bar — GET /api/requests?provider=&model=&alias=&status=&surface=&since_ms=&until_ms=. alias/since_ms/until_ms are accepted today and unreachable from the current UI.`
- `2 · Saved views — localStorage only. Not server state; no endpoint.`
- `3 · Table — darkraise-ui/data-table. requests[]{id,ts_ms,dialect,surface,model,alias,provider,status,tokens_in,tokens_out,total_ms,attempts}.`
- `4 · Newer pill — the 3s poll queues rows rather than moving the reader's scroll position.`
- `5 · Columns + CSV — DataTable ships both. Faceted filters need the 6.5.0 extension (spec §7).`
- `6 · Paging — keyset on (ts,id) desc, cursor rejected when filters change. Phase 7 §4.2, unchanged.`
- `7 · Empty state — every empty well says what it would show. Spec §6.11.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 03-requests && python3 check.py 03-requests --light
```

Expected: qa PASS; both PNGs. Confirm the 30px row density holds — 14 rows plus header should occupy about 450px.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design the requests log"
```

---

## Task 7: Request trace

**Files:**
- Create: `docs/ux/mockups/fragments/04-request-trace.html`

**Interfaces:**
- Consumes: the `.ladder` contract from Task 4 — copy its markup shape exactly.
- Produces: `.waterfall`, `.warning-list` — no later consumer; this is a leaf.

Spec §6.3. This screen is the payoff for the whole design; it is where the ladder earns the spine claim.

- [ ] **Step 1: Build the screen**

Render the trace of the eight-candidate failover from Task 4's retrospective mode, so the two screens agree.

Regions:

1. **Header** — status badge, the ULID in mono with a copy affordance, then `dialect · surface · alias → provider/final_model`: `openai · llm · fast-coder → together/openai-gpt-oss-120b`.
2. **The ladder, retrospective** — all eight candidates, three attempted, one served, four below the termination rule. Copy Task 4's markup exactly; this must not be a second implementation.
3. **Waterfall** — three bars, one per attempt, showing connect / time-to-first-byte / total, aligned on a shared time axis with the graticule as its gridline. Attempt 2's bar runs to the 30s timeout and is capped with a `--state-failed` end mark.
4. **Readouts** — `tokens_in 8,412`, `tokens_out 1,904`, `cost_micros $0.0213`, `ttft_ms 284ms`, `total_ms 32,259ms`. Cost is shown priced here; a second inline example shows the unpriced case rendering an em-dash with "pricing unavailable for this model" in `--legend`, because an honest gap beats a confident zero.
5. **Warnings** — two entries from `warnings[]`, phase 4 dropped-field warnings, e.g. `openai→anthropic: a request parameter was dropped in translation`.
6. **Surface detail** — `surface_meta` as a JSON tree, collapsed to two levels.
7. **Bodies** — the panel in its permanent state: "Not captured." plus a sentence in sans explaining that body capture has no writer, so this is off rather than broken. Spec §2 and §6.3.
8. **Open in playground** — the button, positioned so it reads as the end of the investigation.

Pins:

- `1 · Header — GET /api/requests/{id} {id,dialect,surface,alias,provider,final_model,status,error_code}.`
- `2 · Ladder — candidates[] and skips[] ("target:reason") and attempts[]{seq,provider,key_label,model,outcome,status_code,latency_ms,error,path}. seq is 0-indexed at source and displayed 1-based.`
- `3 · Waterfall — attempts[].latency_ms plus ttft_ms. Graticule doubles as the gridline; no second axis.`
- `4 · Readouts — tokens_in/out, cost_micros (null renders an em-dash, never $0.00), ttft_ms, total_ms. Cost needs spec §8.3.`
- `5 · Warnings — warnings[]. Phase 4 dropped-field warnings; this is where a vanished cache_control marker becomes visible.`
- `6 · Surface detail — surface_meta, rendered with darkraise-ui JsonTreeView.`
- `7 · Bodies — bodies[] is always empty: capture.bodies has a retention sweep and no writer. Saying so beats an empty panel that reads as a bug.`
- `8 · Open in playground — seeds the playground with this request's model and params. Spec §6.3.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 04-request-trace && python3 check.py 04-request-trace --light
```

Expected: qa PASS. Then diff the ladder region against Task 4 by eye: the marks, spine, stubs and termination rule must be identical. Any divergence means the ladder has been reimplemented and the two will drift.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design the request trace around the ladder"
```

---

## Task 8: Usage

**Files:**
- Create: `docs/ux/mockups/fragments/05-usage.html`

**Interfaces:**
- Consumes: Task 3 vocabulary, `.well` in particular — every chart sits in a well and the graticule is its gridline.
- Produces: `.chart-scope`, `.rank-table` — `.chart-scope` overrides the colliding chart ramp and Task 19 re-renders it in light. Declare it in darkrouter-ui.css, not inline, so Task 19 does not redefine it.

Spec §6.4. Page title `operate/usage`. This screen does not exist today.

- [ ] **Step 1: Build the screen**

Regions:

1. **Range picker** — `7d / 30d / 90d / 365d` as a segmented control, `30d` active.
2. **Requests over time** — a stacked area chart by provider, 30 daily buckets, in a well with the graticule as its gridline.
3. **Tokens over time** — input and output as two series.
4. **Cost over time** — a bar per day, with a running total readout at 30px.
5. **Ranked by provider** — a table: provider, requests, tokens, cost, share as a bar. Rows are clickable and the pin says where they go.
6. **Ranked by model** — the same shape, keyed by model.
7. **The honesty footnote** — a sentence in sans, in `--legend`, at the foot: "Tokens spent on attempts that failed before commit are not counted here, so these figures understate spend exactly when failover fires." Spec §6.4 and §8.3.

**The chart ramp override is mandatory and this screen is where it is proven.** `darkraise-ui`'s generated ramp with a sky accent emits `--chart-4` as orange-400 and `--chart-5` as lime-400, which read as the reserved cooling amber and healthy green. Declare a `.chart-scope` class that redefines `--chart-1` through `--chart-5` as a monochrome accent ramp differentiated by fill — solid, 60% tint, 45° hatch, dot, outline — and put it on an ancestor of every chart. Custom properties inherit, so the class beats `:root`.

Pins:

- `1 · Range — GET /api/usage?days=30. Serves up to 365 days today with no screen calling it.`
- `2-4 · Charts — GET /api/usage daily series + the §8.2 group_by=provider|model extension. usage_daily already carries the detail; the endpoint currently aggregates it away.`
- `5-6 · Ranked tables — clicking a row navigates to /requests pre-filtered to that provider or model. The interaction that turns a chart into an investigation.`
- `7 · Footnote — request_attempts carries no usage columns, so pre-commit failures never reach usage_daily. Spec §8.3 fixes it; until then the footnote is the truth.`
- `8 · .chart-scope — overrides --chart-1..5. The engine's sky ramp emits orange-400 and lime-400 at positions 4 and 5, colliding with the reserved cooling and healthy colours.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 05-usage && python3 check.py 05-usage --light
```

Expected: qa PASS. Inspect both PNGs for any orange or lime in the charts. If either appears, `.chart-scope` is not applied to that chart's ancestor and the collision is live.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design the usage screen"
```

---

## Wave 2 — Configure

Tasks 9 to 12 are independent of one another. Task 10 and Task 11 are reached *from* Task 9's screen, but each fragment stands alone, so all four can be built in parallel.

## Task 9: Providers

**Files:**
- Create: `docs/ux/mockups/fragments/06-providers.html`

**Interfaces:**
- Consumes: `.health-tile` from Task 5, `.table` from Task 6.
- Produces: `.priority-handle`, `.discovery-status` — reused by Task 10.

Spec §6.5. Page title `configure/providers`.

- [ ] **Step 1: Build the screen**

Regions:

1. **Provider list** — eight rows, each: drag handle, name, `kind`, `priority`, an enabled switch, credential count, a state pip, and a discovery status dot. Show `groq` priority 100, `cerebras` 90 with one credential cooling, `anthropic` 80, `openrouter` 70, `together` 60, `deepinfra` 50 disabled via the switch rather than deleted, `bedrock` 40 with zero credentials, `vertex` 30 with a needs-reconnection flag.
2. **Reordering** — one row mid-drag, showing the drop indicator and the priority values renumbering. The pin names the endpoint that has never been called.
3. **Discovery health column** — three states side by side: healthy (`last success 3m ago`), stale (`last success 6h ago, 14 consecutive failures`) in `--state-cooling`, and never-run. The stale case is the one that is invisible today.
4. **Add provider** — a primary button opening Task 11's browser, plus a secondary "Add without a preset" link opening the raw form.
5. **Raw provider form** — the escape hatch, shown open: `id`, `kind` select (`openaicompat`, `anthropic`, `gemini`, `bedrock`, `vertex`), `base_url`, `auth_style`, `priority`, `enabled`, `region`, `project`, `location`. The API accepts all of these and today's two-field form can express none.
6. **Bulk state summary** — a one-line count in `--legend`: `8 providers · 6 enabled · 1 cooling · 1 needs reconnection · 197 presets available`.

Pins:

- `1 · List — GET /api/providers providers[]{id,name,preset,kind,base_url,priority,enabled,auth_style,credentials[]}.`
- `2 · Reorder/enable — PATCH /api/providers/{id} {name,base_url,priority,enabled,region,project}. Implemented, wired, and called by nothing today.`
- `3 · Discovery health — provider_discovery{consecutive_failures,last_attempt_at,last_success_at,last_error} via the §8.2 extension. DB.DiscoveryStates is never called by the admin package today.`
- `4-5 · Create — POST /api/providers {id,name,preset,kind,base_url,auth_style,priority,enabled,region,project,location}. location is create-only, not patchable.`
- `6 · Counts — derived client-side from the list; no endpoint.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 06-providers && python3 check.py 06-providers --light
```

Expected: qa PASS; both PNGs.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design the providers screen"
```

---

## Task 10: Provider detail

**Files:**
- Create: `docs/ux/mockups/fragments/07-provider-detail.html`

**Interfaces:**
- Consumes: `.discovery-status` from Task 9, `.ladder` marks from Task 4 for the cooling triples list.
- Produces: `.credential-row`, `.breaker-panel` — no later consumer; this is a leaf.

Spec §6.5. Shown for `cerebras`, because it is the provider with something wrong, and a detail screen that shows a healthy provider proves nothing.

- [ ] **Step 1: Build the screen**

Regions:

1. **Identity** — editable `name`, `kind` (read-only, with a note that kind is create-only), `base_url`, `priority`, enabled switch, and the preset it came from with a link to that preset's website.
2. **Credentials** — three rows. Each: label, `masked` suffix (`…a4f2`), an enabled switch, a cooling badge where applicable, last-used time, and Remove. One row is mid-replace, showing the replace-secret field — replacing means adding and deleting today, and there is no route for either enable/disable or replace.
3. **OAuth account** — a second panel for the `anthropic-oauth` case: account identifier, token expiry with a countdown, last refresh time, the background refresh worker's next scheduled run, and Reconnect. Today only "needs reconnection" is ever shown, and the account identifier is returned once on connect and discarded.
4. **Breaker panel** — the state that exists in memory and in the database and is exposed nowhere: `cooling_until 14:22:07 (4m 12s)`, `backoff_level 3 of 5`, `consecutive_failures 7`, and a list of the specific model triples cooling, each rendered with Task 4's cooling mark. A **Reset breaker** button that is not a probe.
5. **Probe** — the Test button with a result panel showing `ok`, `probe: listing`, `model_count: 41`, `latency_ms: 284`, and a note that a successful probe resets the ladder for the probed credential. Show the failed variant beneath it with an `error` string.
6. **Discovery** — last attempt, last success, consecutive failures, the last error verbatim in mono, and a **Run discovery now** button.
7. **Danger zone** — Delete, with the dangling-alias warning naming the aliases that would be stranded: `fast-coder`, `cheap-embed`. Master design §7 makes a dangling alias a warning rather than an error precisely so this delete cannot brick the next reload.

Pins:

- `1 · Identity — PATCH /api/providers/{id}. kind, preset and auth_style are not patchable; location is create-only.`
- `2 · Credentials — GET /api/providers credentials[]{id,label,masked,enabled,cooling}. Never plaintext, never ciphertext, no endpoint reveals one. Phase 7 §4.1. Enable/disable/replace need the new PATCH /api/providers/{id}/keys/{keyId}.`
- `3 · OAuth — POST /api/providers/{id}/oauth/start|complete. Expiry, last refresh and account id need the §8.2 extension.`
- `4 · Breaker — GET /api/health/providers, POST /api/providers/{id}/breaker/reset. Both new. Today clearing a cooldown is only a side effect of Test.`
- `5 · Probe — POST /api/providers/{id}/test {ok,probe,latency_ms,model_count,error}. Bypasses the breaker by design; a rejected key is a 200 with ok:false, not a 5xx.`
- `6 · Discovery — POST /api/providers/{id}/discover. New. Discoverer.Trigger fires today only as a side effect of a successful test.`
- `7 · Delete — DELETE /api/providers/{id} returns dangling_aliases[]. A dangling alias is a warning, not a validation error.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 07-provider-detail && python3 check.py 07-provider-detail --light
```

Expected: qa PASS. Confirm no credential secret, plaintext or ciphertext, appears anywhere in the fragment — grep it:

```bash
cd docs/ux/mockups && grep -nEi 'sk-[a-z0-9]{8,}|gsk_[a-z0-9]{8,}|BEGIN (RSA )?PRIVATE KEY' fragments/07-provider-detail.html && echo "LEAK" || echo "clean"
```

Expected: `clean`.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design provider detail with breaker and discovery health"
```

---

## Task 11: Preset browser

**Files:**
- Create: `docs/ux/mockups/fragments/08-preset-browser.html`

Spec §6.5. An overlay, so this is the one screen that uses the single permitted shadow.

- [ ] **Step 1: Build the screen**

The current UI reduces 197 presets to `name (id)` in one flat dropdown. This replaces it.

Regions:

1. **Search** — focused, with `cere` typed and three matches.
2. **Facets** — surface (`llm`, `embedding`, `image`, `stt`, `tts`, `rerank`, `moderation`), auth kind (`bearer`, `x-api-key`, `api-key`, `query-param`, `none`, `sigv4`, `gcp-sa`, `oauth`), and a free-tier toggle. Each facet shows its count: bearer 178, x-api-key 6, api-key 4, none 5.
3. **Results grid** — twelve preset cards: name, id in mono, `kind`, `base_url` truncated from the middle, surface chips, a free-tier badge where applicable, and a website link. Use real presets: `groq`, `cerebras`, `deepinfra`, `together`, `openrouter`, `anthropic`, `anthropic-oauth`, `bedrock`, `vertex`, `vertex-anthropic`, `agentrouter`, `agnes`.
4. **Selected state** — one card selected, revealing the create form inline: `id` prefilled from the preset id and editable, plus a note naming what the preset supplies (kind, base_url, auth style, surfaces, quirks) so the operator understands what they are not having to type.
5. **Counts line** — `197 presets · 186 openaicompat · 7 anthropic · 2 vertex · 1 gemini · 1 bedrock`.

Pins:

- `1-3 · GET /api/presets presets[]{id,name,kind,base_url,surfaces,auth_kind,website,free_tier}. Every one of these fields is served today and the current dropdown drops all but name and id.`
- `4 · POST /api/providers {id,preset}. The preset supplies kind, base_url, auth_style, declared surfaces, quirks, model traits and the models.dev join key.`
- `5 · Counts — derived from the preset list.`
- `6 · Overlay — the one place in the product with a shadow: 0 8px 24px -8px. Spec §3.3.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 08-preset-browser && python3 check.py 08-preset-browser --light
```

Expected: qa PASS. `qa.py` permits the `<a href="https://groq.com">` website links because they are content, not loaded resources — confirm it does not flag them.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design the preset browser over all 197 presets"
```

---

## Task 12: Models

**Files:**
- Create: `docs/ux/mockups/fragments/09-models.html`

**Interfaces:**
- Consumes: `.ladder` compressed mode from Task 4, `.table` from Task 6.

Spec §6.6. Page title `configure/models`.

- [ ] **Step 1: Build the screen**

Regions:

1. **Facets** — surface, capability (`tools`, `vision`, `reasoning`), context window as a range, price band, provider, and lifecycle state (`live`, `stale`, `removed_upstream`). The API already returns `state` and the current table never renders it.
2. **Table** — sixteen rows with columns the current screen lacks: model, providers serving it, surfaces, context window, max output tokens, input price per MTok, output price per MTok, publisher, capability chips, source, and state. Source values: `models_dev`, `discovered`, `inferred`, `override` — the provenance of every number.
3. **Removed-upstream rows** — two rows in the `removed_upstream` state, struck through, with a filter toggle labelled "show retired models" that is off by default. `Snapshot.Filter.IncludeRemoved` exists for exactly this and nothing calls it.
4. **Model detail sheet** — open for `openai/gpt-oss-120b`, containing: every provider serving it in route order as a **compressed ladder** copied from Task 4, per-provider pricing, traits, merge provenance per field, and an **override editor** with fields for context window, max output, capabilities and surfaces, plus a "revert to detected" control per field.
5. **Aliases panel** — a read-only summary linking to Routing, where aliases are actually edited.

Pins:

- `1-2 · GET /api/models?q=&surface=&min_context=&tools=true. min_context and tools are accepted today and unreachable from the current screen.`
- `3 · state is live|stale|removed_upstream. Filter.IncludeRemoved exists and nothing calls it.`
- `4 · Detail — providers[] in route order via POST /api/route/preview. Override editor writes GET/PUT/DELETE /api/models/{provider}/{model}/override — the model_overrides table sits at the top of the merge precedence and has never had a writer.`
- `5 · Aliases — edited on configure/routing, not here.`
- `6 · inferred — an inferred model routes with a warning, so a guessed row must be visibly a guess.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 09-models && python3 check.py 09-models --light
```

Expected: qa PASS. Confirm the compressed ladder in the detail sheet uses the same mark shapes as Task 4.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design the model catalog with provenance and overrides"
```

---

## Wave 3 — Routing and Use

Tasks 13 to 16 are independent of one another.

## Task 13: Routing

**Files:**
- Create: `docs/ux/mockups/fragments/10-routing.html`

**Interfaces:**
- Consumes: the `.ladder` contract from Task 4, predictive mode specifically.

Spec §6.7. Page title `configure/routing`. This is the screen that earns the spine.

- [ ] **Step 1: Build the screen**

Regions:

1. **Route preview** — the centrepiece. An input holding `fast-coder` with the resolved ladder beneath it in **predictive** mode: outlined marks, no latencies, no served row, two skip reasons that apply right now (`cooling · 4m 12s remaining` on `cerebras`, `no_capability · vision demanded, model has none` on `anthropic`). Above the ladder, a line naming what resolution rule fired: `alias → 8 targets`. Show a second collapsed example for a bare model name resolving through provider priority order, and a third for `provider/model` resolving to exactly one target.
2. **Resolution rules** — the three rules in order as reference: exact alias name, then `provider/model` split on the first slash only when the prefix names a configured provider, then bare model name expanded across providers in priority order. State that alias targets resolve one level only — nested aliases are not followed.
3. **Alias editor** — four aliases as ordered chains with drag handles: `fast-coder` → 8 targets, `cheap-embed` → 3, `vision` → 2, `long-context` → 4. One chain is mid-drag. Each target row shows whether it currently resolves, with an unresolvable target flagged in `--state-cooling` rather than red, because a dangling alias is a warning and not an error.
4. **Alias validation** — an inline warning on `cheap-embed` naming the embedding hazard: the same embedding model served by two different providers can return differently-scaled vectors, so a fallback chain across providers silently changes what is stored.
5. **Policy** — retry `max_attempts 4`, cooldown `trip_after 3` and `max 15m`, timeouts `connect 10s`, `first_byte 60s`, `total 10m`, `idle 120s`. Each field carries a **hot-reloadable** or **restart required** marker: `connect` and `first_byte` configure the one shared transport built at startup and are restart-only; `total` and `idle` are read per request and are live. That distinction is real and currently invisible.
6. **Where this lives** — a note stating that after first run, aliases and policy live in SQLite and editing them in `darkrouter.yaml` has no effect, exactly as with `providers:`. Spec §8.1.

Pins:

- `1 · Route preview — POST /api/route/preview {model}. New. The resolver is already a pure function of a frozen snapshot, so this is a thin endpoint over existing machinery.`
- `2 · Resolution — first match wins: alias, then provider/model (split on the FIRST slash, only when the prefix names a configured provider, so meta-llama/Llama-3.3-70B survives), then bare name in priority order.`
- `3-4 · Aliases — GET/PUT /api/aliases. New. Chains are yaml-only today and rendered as a JSON dump.`
- `5 · Policy — GET/PUT /api/policy. GET /api/config already returns the whole policy block and the SPA's Config type does not declare it.`
- `6 · Storage — spec §8.1: imported once on first run, SQLite authoritative after. Same rule as providers:.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 10-routing && python3 check.py 10-routing --light
```

Expected: qa PASS. Confirm the preview ladder's marks are **outlined**, not filled — filled here would claim attempts happened that did not.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design routing with the route-preview dry run"
```

---

## Task 14: Playground

**Files:**
- Create: `docs/ux/mockups/fragments/11-playground.html`

Spec §6.8. Page title `use/playground`.

- [ ] **Step 1: Build the screen**

Today this screen sends one line of text to one model. Regions:

1. **Model picker** — a combobox holding `fast-coder` with the resolved target shown beneath in `--legend`: `→ together/openai-gpt-oss-120b`.
2. **Dialect selector** — `openai`, `anthropic`, `gemini` as a segmented control. The gateway serves all three and none is testable from the dashboard today.
3. **Surface selector** — `chat`, `embeddings`, `rerank`, `moderation`, `images`, `speech`, `transcription`, `count tokens`. Chat active.
4. **Conversation** — three turns: a system message, a user message, an assistant reply mid-stream with a cursor. Multi-turn, which the current single-input screen cannot express.
5. **Parameters** — temperature, max tokens, top-p, a stream toggle (on), and a tools panel holding one JSON tool definition in a well.
6. **Auxiliary surface panels** — three collapsed previews proving the other surfaces are designed, not deferred: embeddings showing a 3,072-dimension vector preview with an encoding selector and dimension override; transcription showing a file drop zone with a duration readout; speech showing a voice selector, format selector, and an audio player.
7. **Token counting** — a count readout with the `X-Darkrouter-Estimated: true` marker explained: a native upstream count when the candidate's kind matches the inbound dialect, otherwise a local BPE estimate.
8. **Result footer** — request id, provider that actually served, attempts, latency, and a link to the trace.

Pins:

- `1-2 · POST /api/playground {model,prompt,system,stream}. The API already accepts system and stream:false; the current screen exposes neither.`
- `3 · Surfaces — the gateway serves llm, embedding, moderation, rerank, image, stt and tts. None of the six auxiliary surfaces can be exercised from the dashboard today.`
- `4-5 · Multi-turn and parameters need the playground endpoint extended to accept a message array and sampling params.`
- `6 · Embeddings — POST /v1/embeddings {input,encoding_format,dimensions}. Transcription is multipart; speech returns binary or SSE and is never stored.`
- `7 · Counting — POST /v1/messages/count_tokens and /v1beta/models/{model}:countTokens. X-Darkrouter-Estimated marks a local estimate.`
- `8 · Trace link — X-Darkrouter-Request arrives with the response headers, before the body this is rendering.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 11-playground && python3 check.py 11-playground --light
```

Expected: qa PASS.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design the playground across every surface"
```

---

## Task 15: Playground compare

**Files:**
- Create: `docs/ux/mockups/fragments/12-playground-compare.html`

Spec §6.8. The reference survey identifies compare-two-models as the most-wanted feature in a router playground, because choosing between providers is the operator's recurring decision.

- [ ] **Step 1: Build the screen**

Regions:

1. **Shared prompt** — one input feeding both panes, with the system message shared.
2. **Two panes** — left `together/openai-gpt-oss-120b`, right `anthropic/claude-opus-4-20250514`. Each pane holds its own model picker, its own parameters, and its own streaming output. One pane is mid-stream, the other complete, so both states are proven.
3. **Comparison footer** — per pane: time to first token, total latency, tokens in and out, and cost. The cheaper and the faster are marked, and the marks are in `--trace` rather than `--state-healthy`, because "faster" is a reading and not a health state.
4. **Divergence affordance** — a control to sync or unlock the two panes' parameters, so a comparison is either same-params-different-model or a deliberate two-variable run.
5. **Trace links** — each pane links to its own trace.

Pins:

- `1-2 · Two independent POST /api/playground calls with a shared prompt. Each pane carries its own model, params and stream.`
- `3 · Comparison — ttft_ms, total_ms, tokens_in/out, cost_micros per pane. Winner marks use --trace: a reading, not a health state.`
- `4 · Sync toggle — locked means one variable (the model), unlocked means the operator accepts two.`
- `5 · Each pane's X-Darkrouter-Request links to its own trace.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 12-playground-compare && python3 check.py 12-playground-compare --light
```

Expected: qa PASS. Confirm no state colour is used to mark a winner.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design side-by-side model comparison"
```

---

## Task 16: Connect

**Files:**
- Create: `docs/ux/mockups/fragments/13-connect.html`

Spec §6.9. Page title `use/connect`. This is the screen a stranger needs first.

- [ ] **Step 1: Build the screen**

Regions:

1. **Endpoints** — one row per dialect with a copy control: OpenAI `http://192.168.0.10:8080/v1`, Anthropic `http://192.168.0.10:8080/v1`, Gemini `http://192.168.0.10:8080/v1beta`. Each names the routes it serves.
2. **Surfaces live** — a grid of the seven surfaces with their paths, each marked available or not based on what the configured providers declare.
3. **Proxy tokens** — a table of per-client tokens: label, masked value, created, last used, Revoke. Show three: `claude-code`, `cursor`, `nightly-script`. Plus a Create control. Today this is one shared secret in a file with no rotation, so one leaked token cannot be revoked without reconfiguring every client.
4. **Client snippets** — tabs for Claude Code, Codex, Cursor, the OpenAI SDK, the Anthropic SDK, and curl. Each shows the exact config with the base URL and a token placeholder filled in, and a copy control. The Claude Code tab shows the `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN` environment form.
5. **Response headers** — the four headers a client sees, so routing is visible from a terminal without the dashboard: `X-Darkrouter-Provider`, `X-Darkrouter-Model`, `X-Darkrouter-Attempts`, `X-Darkrouter-Request`.

Pins:

- `1-2 · Static, derived from server.proxy_listen and the configured providers' declared surfaces.`
- `3 · GET/POST/DELETE /api/proxy-tokens. New. server.proxy_token today is a single shared secret compared constant-time after SHA-256, accepted in all three dialects' auth headers.`
- `4 · Snippets are generated from the endpoint plus the selected token. No endpoint.`
- `5 · Headers — set by the proxy on every response; the terminal-visible half of the trace.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 13-connect && python3 check.py 13-connect --light
```

Expected: qa PASS. Confirm no real-looking secret appears; token values are masked to a last-four suffix.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design the connect screen"
```

---

## Wave 4 — Settings and entry

## Task 17: Settings

**Files:**
- Create: `docs/ux/mockups/fragments/14-settings.html`

Spec §6.10. Page title `settings`. Grouped by concern so one long form does not become unnavigable.

- [ ] **Step 1: Build the screen**

Six grouped sections, each a panel:

1. **Server** — `proxy_listen :8080`, `admin_listen :8081`, `max_body_bytes 33554432`, `shutdown_grace 30s`, `sse.max_line_bytes 1048576`, `sse.max_precommit_bytes 1048576`. Every one marked restart-required.
2. **Log and capture** — `log.retention 720h` (live: read by the retention worker), `capture.bodies` off, `capture.max_bytes 256000`, `capture.retention 72h`. The capture group carries a note that body capture has no writer, so these three settings are inert today.
3. **Catalog** — `models_dev_url`, `sync_interval 12h`, `sync_timeout 30s`, and a **Sync now** button. Then discovery: an `enabled` master switch, `interval 15m`, `timeout 15s`, `concurrency 8`. The master switch carries a sentence explaining it governs outbound traffic the gateway initiates on the operator's behalf, which today requires a file edit and a restart.
4. **Account** — change password, and a session list: three sessions with created time, last seen, user agent, and Revoke, with the current session marked.
5. **Appearance** — mode (`system`, `light`, `dark`) and density (`compact`, `cozy`). Two axes, not seventeen.
6. **Configuration file** — the parsed config rendered read-only with a validation badge, the warnings list, a Reload button, and the §8.1 note stating which blocks the file still governs and which now live in SQLite.

Pins:

- `1-3 · GET /api/config returns the whole server, log, capture and catalog blocks today and the SPA's Config type declares none of them. Writes need GET/PUT endpoints per §8.2.`
- `2 · capture.bodies is inert: nothing writes request_bodies, though the retention sweep dutifully prunes the table.`
- `3 · Sync now → POST /api/catalog/sync. Discovery enabled is a pointer in the schema so an explicit false is distinguishable from absent.`
- `4 · POST /api/auth/password, GET/DELETE /api/sessions. Both new. Sessions have a sliding 30-day TTL and there is no way to list or revoke one today.`
- `5 · Two axes. The seventeen-axis switcher is removed: an ops console reading live health must not let a gradient picker change what amber means.`
- `6 · GET /api/config, POST /api/config/reload. The source marker per value is the §8.2 extension.`

- [ ] **Step 2: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 14-settings && python3 check.py 14-settings --light
```

Expected: qa PASS.

- [ ] **Step 3: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design settings grouped by concern"
```

---

## Task 18: Login and first-run

**Files:**
- Create: `docs/ux/mockups/fragments/15-login.html`
- Create: `docs/ux/mockups/fragments/16-first-run.html`

Spec §6.11. Two fragments in one task because neither is large and they are the same moment in the product.

- [ ] **Step 1: Build the login fragment**

Three states side by side on one screen, because a login screen with one state proves nothing:

1. **Resting** — the identity mark from spec §3.5 above a single password field and a submit button. Centred, on `--ground`, no chrome.
2. **Rejected** — the same, with one message: the server returns the same string for a wrong password and an unconfigured hash, deliberately, because an operator reading "no password is set" learns the port is open.
3. **No password configured** — the state the product currently fails at. Today an unset `DARKROUTER_ADMIN_PASSWORD_HASH` closes the dashboard silently with only a `/healthz` warning, so every password is refused with no explanation. This state instead says what happened and prints the exact remedy: `darkrouter hash-password`, then set `DARKROUTER_ADMIN_PASSWORD_HASH`, then restart. Note the compose gotcha: a bcrypt hash contains `$`, which compose reads as a variable, so every one must be doubled.

- [ ] **Step 2: Build the first-run fragment**

The console with zero providers and zero requests. This is the worst-looking case in the whole set — a fresh install renders as flat rectangles with faint grids, which is indistinguishable from broken equipment — so it is designed rather than left to fall out.

1. **Overview, empty** — each well carries a legend naming what it will show and a dimmed example: the health grid shows one ghost tile, the live strip shows dashed axes with "no requests yet".
2. **The one call to action** — Add your first provider, opening the preset browser.
3. **A three-step path** — connect a provider, test its credential, send a request from the playground. Rendered as a Steps control with the first step active, not as prose.
4. **Empty-state pattern** — the reusable shape stated once: a legend sentence in sans, a dimmed specimen of the real content, and at most one action.

Pins on both fragments:

- `1 · Login — POST /api/auth/login {password}. One message for a wrong password and an unconfigured hash, matching the server.`
- `2 · No-hash state — DARKROUTER_ADMIN_PASSWORD_HASH unset does not stop the gateway; it closes the dashboard and adds a /healthz warning. Today the UI never says so.`
- `3 · First run — GET /api/overview with providers[] empty. Every empty well says what it would show.`
- `4 · Steps — connect, test, send. POST /api/providers, POST /api/providers/{id}/test, POST /api/playground.`

- [ ] **Step 3: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 15-login && python3 check.py 15-login --light
python3 check.py 16-first-run && python3 check.py 16-first-run --light
```

Expected: qa PASS; four PNGs.

- [ ] **Step 4: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): design login and the first-run experience"
```

---

## Wave 5 — Proof and publication

## Task 19: The light proof

**Files:**
- Create: `docs/ux/mockups/fragments/17-light-proof.html`
- Modify: `docs/ux/mockups/darkrouter-ui.css` (any light-mode corrections this task finds)

Spec §10. Light mode is first-class, so this screen is a proof rather than a gesture. It exists because light and dark are structurally different screens — the well polarity inverts — and a single wrong token is invisible until the two are set side by side.

- [ ] **Step 1: Build the screen**

This fragment renders in light regardless of the `T` toggle, by scoping its own contents under a forced-light container, so the assembled document can show dark and light adjacent.

Regions:

1. **Well polarity, side by side** — the same panel-containing-a-well in both modes. Dark: well darker than panel. Light: well brighter than panel. Labelled with the measured luminance step in each direction. If these two images are the same shape with different lightness, the CSS is a palette swap and the structural rule is not implemented.
2. **The ladder in light** — the retrospective ladder from Task 4, in light. The four marks and the termination rule must remain as legible as in dark; the termination rule is the one most at risk, because `--hairline-subtle` in light is a pale grey against a near-white well.
3. **The five repaired tokens** — the ones `darkraise-ui` ships broken in light, each shown with its shipped value and its repaired value, with both ratios printed: `--primary` 2.74:1 → 3.93:1, `--focus-ring` 1.28:1 → 3.34:1, `--success` 2.48:1 → 3.70:1, `--warning` 2.03:1 → 3.02:1, `--destructive` failing → 4.59:1. Spec §7.
4. **Input focus in light** — a focused text field, because `dist/styles.css:10749` documents that form controls take their indicator from `--primary` and not `--focus-ring`, so this is the specific control the library defect breaks.
5. **The cooling mark's headroom** — `--state-cooling` in light measures 3.02:1 against the panel, which clears the 3:1 non-text floor by 0.02. Render it at its actual size beside the same mark at 4× so the thinness of the margin is visible and the decision to accept it is made with eyes open.
6. **Charts in light** — the `.chart-scope` ramp in light, confirming the monochrome-plus-fill differentiation still separates five series without reaching for orange or lime.

Pins:

- `1 · Well polarity — :root {--well darker than --panel}; body.theme-light {--well brighter}. Spec §3.3. This is why light is not a palette swap.`
- `2 · Ladder in light — the termination rule is the at-risk mark: --hairline-subtle on a near-white well.`
- `3 · Repaired tokens — five shipped darkraise-ui light-mode failures. Fixed upstream in 6.5.0 per spec §7; mocked here as repaired.`
- `4 · Input focus — styles.css:10749: inputs use --primary, not --focus-ring. Fixing --focus-ring alone does not fix form controls.`
- `5 · Cooling headroom — 3.02:1, clears the floor by 0.02. Accepted deliberately.`
- `6 · Charts in light — .chart-scope, monochrome accent plus fill patterns.`

- [ ] **Step 2: Verify the well polarity by measuring pixels, not by looking**

```bash
cd docs/ux/mockups
python3 check.py 02-overview --no-pins
python3 check.py 02-overview --no-pins --light
python3 - <<'PY'
# A palette swap keeps the panel/well ordering; the structural rule inverts it.
# Read the two PNGs without any third-party library.
import struct, zlib, sys
from pathlib import Path

def read_png_gray(path):
    data = Path(path).read_bytes()
    pos, idat, w, h, depth, ctype = 8, b"", 0, 0, 0, 0
    while pos < len(data):
        ln = struct.unpack(">I", data[pos:pos+4])[0]
        typ = data[pos+4:pos+8]
        body = data[pos+8:pos+8+ln]
        if typ == b"IHDR":
            w, h, depth, ctype = (*struct.unpack(">II", body[:8]), body[8], body[9])
        elif typ == b"IDAT":
            idat += body
        elif typ == b"IEND":
            break
        pos += 12 + ln
    if depth != 8 or ctype not in (2, 6):
        raise SystemExit(f"{path}: unexpected PNG format depth={depth} ctype={ctype}")
    bpp = 3 if ctype == 2 else 4
    raw = zlib.decompress(idat)
    stride = w * bpp
    out, prev = [], bytearray(stride)
    i = 0
    for _ in range(h):
        f = raw[i]; i += 1
        line = bytearray(raw[i:i+stride]); i += stride
        for x in range(stride):
            a = line[x-bpp] if x >= bpp else 0
            b = prev[x]
            c = prev[x-bpp] if x >= bpp else 0
            if f == 1: line[x] = (line[x] + a) & 255
            elif f == 2: line[x] = (line[x] + b) & 255
            elif f == 3: line[x] = (line[x] + (a+b)//2) & 255
            elif f == 4:
                p = a + b - c
                pa, pb, pc = abs(p-a), abs(p-b), abs(p-c)
                pr = a if (pa <= pb and pa <= pc) else (b if pb <= pc else c)
                line[x] = (line[x] + pr) & 255
        out.append(bytes(line)); prev = line
    return w, h, bpp, out

def luma_at(path, x, y):
    w, h, bpp, rows = read_png_gray(path)
    px = rows[y][x*bpp:x*bpp+3]
    return 0.2126*px[0] + 0.7152*px[1] + 0.0722*px[2]

# Sample coordinates must land on a panel region and a well region of the
# overview screen. Adjust to the actual layout, then keep them fixed.
PANEL_XY, WELL_XY = (700, 300), (700, 360)
# check.py builds its suffix as ("-light" if light) + ("-nopins" if no_pins),
# so --no-pins --light writes "-light-nopins", in that order.
d_panel = luma_at(".check/02-overview-nopins.png", *PANEL_XY)
d_well  = luma_at(".check/02-overview-nopins.png", *WELL_XY)
l_panel = luma_at(".check/02-overview-light-nopins.png", *PANEL_XY)
l_well  = luma_at(".check/02-overview-light-nopins.png", *WELL_XY)
print(f"dark : panel {d_panel:6.1f}  well {d_well:6.1f}  -> well is {'darker' if d_well < d_panel else 'BRIGHTER'}")
print(f"light: panel {l_panel:6.1f}  well {l_well:6.1f}  -> well is {'brighter' if l_well > l_panel else 'DARKER'}")
ok = d_well < d_panel and l_well > l_panel
print("PASS: polarity inverts" if ok else "FAIL: polarity does not invert — this is a palette swap")
raise SystemExit(0 if ok else 1)
PY
```

Expected: `PASS: polarity inverts`, exit 0. A FAIL means the CSS implements light as a lightness change rather than a structural inversion, and every screen inherits the error.

- [ ] **Step 3: Build, gate, screenshot**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py
python3 check.py 17-light-proof
```

Expected: qa PASS — and this is the first build with all eighteen fragments present, so expect `qa: PASS — 18 fragment(s) clean`.

- [ ] **Step 4: Commit**

```bash
git add docs/ux/mockups
git commit -m "feat(ux): prove light mode inverts rather than swaps"
```

---

## Task 20: Whole-set review and publication

**Files:**
- Modify: whatever the review finds
- Modify: `docs/ux/mockups/index.html`, `docs/ux/mockups/artifact.html` (rebuilt)
- Modify: `docs/PROGRESS.md`

- [ ] **Step 1: Full gate**

```bash
cd docs/ux/mockups
python3 build.py && python3 qa.py && python3 -m unittest discover -s tests -v
```

Expected: 18 fragments, qa PASS, 14 tests OK. Record `index.html`'s size — it should land in the 200–350 KB range; materially larger means an asset was embedded that should not have been.

- [ ] **Step 2: Screenshot every screen in both modes**

```bash
cd docs/ux/mockups
for f in fragments/*.html; do
  s=$(basename "$f" .html)
  python3 check.py "$s" && python3 check.py "$s" --light
done
ls -la .check/*.png | wc -l
```

Expected: 36 PNGs, no failures.

- [ ] **Step 3: Dispatch one whole-set review**

Dispatch a single subagent with `model: fable` and read-only tools over `docs/ux/mockups/` and the spec. Its brief:

> Review all eighteen mockup screens as a set against `docs/superpowers/specs/2026-08-24-darkrouter-phase10-operator-console.md`. Report every finding, classified Important or Minor, with the fragment and line. Check specifically: (1) does any screen contradict the design language screen — a colour used for a purpose §3.1 reserves for another, a type role off the §3.2 scale, a shadow anywhere but `--overlay`, a radius other than 2px; (2) is the ladder markup byte-comparable across fragments 01, 04, 09 and 10, or has it been reimplemented; (3) does any pin claim an endpoint or field that does not exist in the admin API; (4) is any state colour used for a router decision or `--trace` used for a provider state; (5) does every screen's empty state say what it would show; (6) is any value implausible, generic, or a placeholder — `foo`, `lorem`, `Example Provider`, a non-ULID request id; (7) does any screen exceed the 30px type ceiling or animate outside the three permitted animations.

Fix every Important finding. Fix Minor findings unless fixing one would contradict the spec, in which case record it in the commit body.

- [ ] **Step 4: Rebuild, re-gate, commit the fixes**

```bash
cd docs/ux/mockups && python3 build.py && python3 qa.py && python3 -m unittest discover -s tests -v
cd /root/repositories/darkrouter
git add docs/ux/mockups
git commit -m "fix(ux): resolve whole-set review findings"
```

- [ ] **Step 5: Publish the artifact**

Publish `docs/ux/mockups/artifact.html` with the Artifact tool. Title: `Darkrouter Console`. Favicon: `🎛️`. Description: one sentence naming it as the eighteen-screen operator console design set.

Two things to get right. The Artifact tool wraps what it is given in its own `<!doctype>`, `<head>` and `<body>`, which is exactly why `build.py` emits `artifact.html` without a document wrapper — publishing `index.html` instead produces nested `<html>` and is invalid. And republishing later means calling Artifact with the **same file path**, which redeploys to the same URL; a different path claims a new one.

Record the returned URL in `docs/PROGRESS.md` so a future session can find and update it rather than publishing a second copy.

- [ ] **Step 6: Verify the published artifact**

Open the returned URL. Confirm: both keyboard toggles work (`A` reveals pins, `T` swaps to light), the table of contents links resolve to all eighteen screens, no font falls back to a system face, and nothing fails to load. The page is self-contained and the artifact CSP blocks external hosts, so a missing font would prove `build.py` linked rather than embedded it.

- [ ] **Step 7: Record the milestone**

Append to `docs/PROGRESS.md`: the eighteen screens, the artifact URL, the qa and test results, and the two decisions a future reader will need — that light mode inverts well polarity rather than swapping a palette, and that the ladder markup is defined once in fragment 01 and copied, not reimplemented.

```bash
git add docs/PROGRESS.md
git commit -m "docs: record the mockup phase and its artifact"
```

- [ ] **Step 8: Confirm nothing is left running**

```bash
pgrep -af 'google-chrome|chrome-headless' || echo "no chrome processes"
```

Expected: `no chrome processes`. `check.py` runs Chrome synchronously with a 90-second timeout, so a leftover means a run hung and its tree needs killing with `pkill -P <pid>` then `kill <pid>`.

---

## Execution notes

**Parallelism.** Tasks 1 to 4 are strictly sequential — the gate, then the assembler, then the design language, then the ladder. After that, Wave 1 (Tasks 5–8), Wave 2 (Tasks 9–12), Wave 3 (Tasks 13–16) and Wave 4 (Tasks 17–18) each hold independent tasks that can run four at a time. Waves 5 (Tasks 19–20) is sequential again.

The house pattern for these waves is fragment-only agents: each agent writes exactly one fragment and screenshots it, and the orchestrator runs `build.py`, `qa.py` and the commit for the whole wave. Agents do not run `build.py`, because four agents building the same `index.html` concurrently will interleave writes.

**Shared CSS is the contention point.** Tasks 5 to 18 may all need to add classes to `darkrouter-ui.css`. Within a wave, have each agent append its classes to a clearly-commented per-screen block at the end of the file rather than editing shared rules, and have the orchestrator consolidate between waves. A wave agent that needs to change an existing shared rule should report it rather than making the change.

**The gotcha that will cost an hour if forgotten.** Headless Chrome's `--screenshot` renders the top of the document and ignores a `#fragment` anchor, so screenshotting `index.html` always yields screen 00. That is the entire reason `check.py` exists; use it rather than pointing Chrome at the assembled page.
