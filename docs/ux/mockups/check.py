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
