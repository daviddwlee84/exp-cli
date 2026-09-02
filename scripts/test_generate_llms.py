from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from generate_llms import GenerationError, generate  # noqa: E402


class GenerateLLMSTest(unittest.TestCase):
    def make_project(self) -> tuple[Path, Path, Path]:
        root = Path(tempfile.mkdtemp(prefix="exp-llms-test-"))
        docs = root / "docs"
        docs.mkdir()
        config = root / "mkdocs.yml"
        config.write_text(
            "\n".join(
                [
                    "site_name: Test Site",
                    "site_description: Test documentation.",
                    "site_url: https://example.test/project/",
                    "docs_dir: docs",
                    "nav:",
                    "  - Home: index.md",
                    "  - Guide: guide.md",
                    "",
                ]
            ),
            encoding="utf-8",
        )
        (docs / "index.md").write_text("# Home\n\nEnglish home summary.\n", encoding="utf-8")
        (docs / "guide.md").write_text("# Guide\n\nEnglish guide summary.\n", encoding="utf-8")
        (docs / "index.zh-TW.md").write_text("# 首頁\n\n繁體中文首頁摘要。\n", encoding="utf-8")
        (docs / "guide.zh-TW.md").write_text("# 指南\n\n繁體中文指南摘要。\n", encoding="utf-8")
        return root, config, root / "site"

    def test_generates_four_outputs_in_nav_order(self) -> None:
        root, config, site = self.make_project()
        self.addCleanup(lambda: __import__("shutil").rmtree(root))

        self.assertEqual(generate(config, site), 2)

        english = (site / "llms.txt").read_text(encoding="utf-8")
        chinese = (site / "zh-TW" / "llms.txt").read_text(encoding="utf-8")
        self.assertLess(english.index("[Home]"), english.index("[Guide]"))
        self.assertIn("https://example.test/project/guide/", english)
        self.assertIn("https://example.test/project/zh-TW/guide/", chinese)
        self.assertIn("English guide summary.", (site / "llms-full.txt").read_text(encoding="utf-8"))
        self.assertIn("繁體中文指南摘要。", (site / "zh-TW" / "llms-full.txt").read_text(encoding="utf-8"))

    def test_missing_translation_fails(self) -> None:
        root, config, site = self.make_project()
        self.addCleanup(lambda: __import__("shutil").rmtree(root))
        (root / "docs" / "guide.zh-TW.md").unlink()

        with self.assertRaisesRegex(GenerationError, "missing zh-TW source"):
            generate(config, site)

    def test_pending_translation_fails(self) -> None:
        root, config, site = self.make_project()
        self.addCleanup(lambda: __import__("shutil").rmtree(root))
        (root / "docs" / "guide.zh-TW.md").write_text(
            "# 指南\n\n!!! warning \"Translation pending\"\n",
            encoding="utf-8",
        )

        with self.assertRaisesRegex(GenerationError, "unfinished translation marker"):
            generate(config, site)


if __name__ == "__main__":
    unittest.main()
