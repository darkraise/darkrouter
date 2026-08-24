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

    def test_svg_url_reference_is_not_raw_hex(self):
        # A gradient/mask/filter id made of hex letters (fade, beef, cab...)
        # is ordinary, not a colour that escaped the token system.
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "svg_url.html"
            f.write_text(
                '<section class="screen" id="s-1-su" data-screen-title="su">'
                '<p class="legend">svg url</p>'
                '<b class="pin" data-pin="1">1</b>'
                '<svg><rect fill="url(#fade)"/></svg></section>',
                encoding="utf-8",
            )
            self.assertEqual(qa.check_fragment(f), [])

    def test_hex_looking_anchor_is_not_raw_hex(self):
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "hex_anchor.html"
            f.write_text(
                '<section class="screen" id="s-1-ha" data-screen-title="ha">'
                '<p class="legend">hex anchor</p>'
                '<b class="pin" data-pin="1">1</b>'
                '<a href="#dead">x</a></section>',
                encoding="utf-8",
            )
            self.assertEqual(qa.check_fragment(f), [])

    def test_hex_in_svg_fill_is_rejected(self):
        # fill="#FF0000" is the canonical way a raw hex enters an SVG-heavy
        # fragment; the url(#...)/href="#..." exemptions must not swallow it.
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "svg_fill.html"
            f.write_text(
                '<section class="screen" id="s-1-sf" data-screen-title="sf">'
                '<p class="legend">svg fill</p>'
                '<b class="pin" data-pin="1">1</b>'
                '<svg><rect fill="#FF0000"/></svg></section>',
                encoding="utf-8",
            )
            problems = qa.check_fragment(f)
            self.assertTrue(any("raw hex" in p for p in problems), problems)

    def test_legend_caps_is_not_a_legend(self):
        # A plain \b word boundary sits at a hyphen, so "legend" would match
        # "legend-caps" and three nav-group labels would read as three extra
        # legends.
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "legend_caps.html"
            f.write_text(
                '<section class="screen" id="s-1-lc" data-screen-title="lc">'
                '<p class="legend">only legend</p>'
                '<b class="pin" data-pin="1">1</b>'
                '<p class="legend-caps">Operate</p>'
                '<p class="legend-caps">Configure</p>'
                '<p class="legend-caps">Use</p></section>',
                encoding="utf-8",
            )
            self.assertEqual(qa.check_fragment(f), [])

    def test_header_element_is_allowed_in_a_fragment(self):
        # <header> is a legitimate content element (a page/section banner),
        # not the document <head>; a substring check on "<head" would
        # falsely flag it.
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "header_elem.html"
            f.write_text(
                '<section class="screen" id="s-1-he" data-screen-title="he">'
                '<p class="legend">header</p>'
                '<b class="pin" data-pin="1">1</b>'
                '<header class="topbar">x</header></section>',
                encoding="utf-8",
            )
            self.assertEqual(qa.check_fragment(f), [])

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
