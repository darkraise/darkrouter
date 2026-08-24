#!/usr/bin/env python3
"""Screenshot one fragment on its own.

Chrome's --screenshot does not honour a #fragment anchor, so a per-screen
image has to come from a per-screen document rather than from index.html.
"""
import argparse
import struct
import subprocess
import sys
import zlib
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import build

HERE = Path(__file__).resolve().parent
OUT = HERE / ".check"
CHROME = "/usr/bin/google-chrome"


# A PNG scanline identical to the one above encodes as filter 2 (Up) with
# all-zero deltas, which is precisely what trailing page background looks
# like. Finding the last non-zero row therefore needs no unfiltering, and a
# full pixel decode of this image size takes tens of seconds in pure Python.
def content_extent(png: Path) -> tuple[int, int]:
    """Return (last row carrying content, image height)."""
    data = png.read_bytes()
    pos, idat, width, height, ctype = 8, b"", 0, 0, 0
    while pos < len(data):
        length = struct.unpack(">I", data[pos:pos + 4])[0]
        kind = data[pos + 4:pos + 8]
        if kind == b"IHDR":
            width, height = struct.unpack(">II", data[pos + 8:pos + 16])
            ctype = data[pos + 17]
        elif kind == b"IDAT":
            idat += data[pos + 8:pos + 8 + length]
        elif kind == b"IEND":
            break
        pos += 12 + length
    raw = zlib.decompress(idat)
    stride = width * (4 if ctype == 6 else 3)
    blank = bytes(stride)
    for y in range(height - 1, -1, -1):
        off = y * (stride + 1)
        if raw[off] != 2 or raw[off + 1:off + 1 + stride] != blank:
            return y + 1, height
    return 0, height


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("stem", help="fragment filename without .html, e.g. 02-overview")
    ap.add_argument("--light", action="store_true")
    ap.add_argument("--no-pins", action="store_true")
    ap.add_argument("--width", type=int, default=1440)
    ap.add_argument("--height", type=int, default=6000)
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

    style = build.style_block()
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

    content, height = content_extent(png)
    # A clip landing inside a band of flat colour reads as "content ended
    # here", so allow a small margin before trusting the reading.
    if content >= height - 8:
        print(
            f"check: CLIPPED — content reaches the bottom edge of {png}. "
            f"Re-run with --height {height * 2} and look at the whole screen.",
            file=sys.stderr,
        )
        return 1
    print(
        f"check: {png} ({png.stat().st_size // 1024} KB), "
        f"content to {content}px of {height}px"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
