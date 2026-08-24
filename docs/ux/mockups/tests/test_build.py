import re
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import build
import qa

HERE = Path(__file__).resolve().parents[1]
FRAGMENTS = HERE / "fragments"

# Two fragments that deliberately define the SAME gradient id. Unsuffixed they
# collide in the assembled document and the second paints with the first one's
# gradient, which is the failure suffix_svg_ids exists to prevent. The id must
# not look like a hex colour or the gate flags it.
_FIXTURE = """<section class="screen" id="s-9{n}-{slug}" data-screen-title="{slug}">
  <p class="legend">{slug}</p>
  <b class="pin" data-pin="1">1</b>
  <svg width="10" height="10" aria-hidden="true">
    <defs><linearGradient id="graticule"><stop offset="0"/></linearGradient></defs>
    <rect width="10" height="10" fill="url(#graticule)"/>
  </svg>
</section>
"""


class TestBuild(unittest.TestCase):
    def setUp(self):
        FRAGMENTS.mkdir(exist_ok=True)
        self.written = []
        for n, slug in ((0, "alpha"), (1, "beta")):
            path = FRAGMENTS / f"9{n}-{slug}.html"
            self.assertFalse(path.exists(), f"{path.name} would clobber a real fragment")
            path.write_text(_FIXTURE.format(n=n, slug=slug), encoding="utf-8")
            self.written.append(path)
        self.index, self.artifact = build.build()

    def tearDown(self):
        for path in self.written:
            path.unlink(missing_ok=True)
        # Leave the built files matching the real fragment set rather than
        # carrying this test's scratch screens into a committed index.
        if any(FRAGMENTS.glob("*.html")):
            build.build()
        else:
            (HERE / "index.html").unlink(missing_ok=True)
            (HERE / "artifact.html").unlink(missing_ok=True)

    def test_build_produces_both_outputs(self):
        self.assertTrue(self.index.exists())
        self.assertTrue(self.artifact.exists())

    def test_index_is_a_document_and_artifact_is_not(self):
        head = self.index.read_text(encoding="utf-8").lstrip().lower()
        self.assertTrue(head.startswith("<!doctype html>"))
        body = self.artifact.read_text(encoding="utf-8").lower()
        # \b keeps <header> out of this: "head" followed by "e" is not a word
        # boundary, so only a real <head> tag matches.
        for wrapper in (r"<!doctype", r"<html\b", r"<head\b", r"<body\b"):
            self.assertIsNone(
                re.search(wrapper, body),
                f"artifact.html must not contain a {wrapper} tag",
            )

    def test_header_element_is_not_mistaken_for_document_head(self):
        # The shell's page banner is a <header>. A substring check for "<head"
        # matches it and would fail the build for no reason.
        body = self.artifact.read_text(encoding="utf-8").lower()
        self.assertIn("<header", body, "shell should carry a <header> banner")
        self.assertIsNone(re.search(r"<head\b", body))

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
        text = self.index.read_text(encoding="utf-8")
        self.assertIn('id="graticule--s-90-alpha"', text)
        self.assertIn('id="graticule--s-91-beta"', text)
        self.assertIn("url(#graticule--s-90-alpha)", text)
        self.assertIn("url(#graticule--s-91-beta)", text)
        self.assertEqual(
            [], [p for p in qa.check_index(self.index) if "duplicate id" in p]
        )

    def test_every_fragment_reaches_the_built_index(self):
        # Counted dynamically, not hardcoded to the 2 fixtures this test
        # writes: real fragments now live alongside them, and a fixed count
        # would break every time a later task adds one.
        text = self.index.read_text(encoding="utf-8")
        expected = len(list(FRAGMENTS.glob("*.html")))
        self.assertEqual(expected, text.count('class="screen"'))
        self.assertIn("alpha", text)
        self.assertIn("beta", text)

    def test_built_index_passes_qa(self):
        self.assertEqual(qa.check_index(self.index), [])


if __name__ == "__main__":
    unittest.main()
