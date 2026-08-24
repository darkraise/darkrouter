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
