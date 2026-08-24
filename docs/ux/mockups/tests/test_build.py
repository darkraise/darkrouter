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
