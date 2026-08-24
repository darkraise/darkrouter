import re
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
        # Two screens may each define a gradient called "fade"; unsuffixed they
        # collide in one document and the second silently renders the first.
        problems = [p for p in qa.check_index(self.index) if "duplicate id" in p]
        self.assertEqual(problems, [])

    def test_built_index_passes_qa(self):
        self.assertEqual(qa.check_index(self.index), [])


if __name__ == "__main__":
    unittest.main()
