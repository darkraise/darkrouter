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


def css_partials() -> str:
    """Concatenate every css/*.css partial in filename order.

    Parallel screen waves each get their own stylesheet file instead of all
    appending to darkrouter-ui.css, so two screens built at once cannot
    clobber each other's rules. Sorted filename order keeps the concatenated
    result deterministic regardless of build order.
    """
    return "\n".join(
        path.read_text(encoding="utf-8") for path in sorted((HERE / "css").glob("*.css"))
    )


def style_block() -> str:
    """Build the single <style> block shared by index.html and check.py.

    A per-fragment screenshot must see exactly what the assembled page sees,
    so both call this instead of each reading darkrouter-ui.css on its own.
    """
    css = (HERE / "darkrouter-ui.css").read_text(encoding="utf-8")
    partials = css_partials()
    sheet = "\n".join(part for part in (css, partials) if part)
    return f"<style>\n{font_face_block()}\n{sheet}\n</style>"


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
    shell = (HERE / "_shell.html").read_text(encoding="utf-8")

    content = toc(screens) + "\n" + "\n".join(html for _, _, html in screens)
    style = style_block()

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
