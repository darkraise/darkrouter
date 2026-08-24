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
# A raw colour hex is any # literal that is not an in-page reference. Excluding
# url(#…) and href="#…" keeps hex-letter SVG ids and anchors — fade, beef,
# cafe — out, without exempting fill="#FF0000", which is the real sin.
HEX = re.compile(r"""(?<!url\()(?<!href=")(?<!href=')#[0-9a-fA-F]{3,8}\b""")

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
    """Match one class among several.

    The delimiter must exclude the hyphen as well as word characters: a plain
    \b sits at a hyphen, so "legend" would match "legend-caps" and a screen
    with three group labels reads as having four legends.
    """
    return re.compile(
        rf'class\s*=\s*["\'][^"\']*(?<![\w-]){name}(?![\w-])[^"\']*["\']'
    )

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

    for forbidden in ("html", "head", "body", "style"):
        if re.search(rf"<{forbidden}\b", text, re.IGNORECASE):
            problems.append(f"{path.name}: fragment contains <{forbidden}> — fragments are sections only")

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
