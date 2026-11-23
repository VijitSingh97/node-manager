import json
import re
import unittest
import xml.etree.ElementTree as ET
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PAGES = ROOT / "packaging" / "pages"
SITE = "https://vijitsingh97.github.io/node-manager/"
REPO = "https://github.com/VijitSingh97/node-manager"
FILE_PREFIXES = (REPO + "/blob/main/", "https://raw.githubusercontent.com/VijitSingh97/node-manager/main/")


def page_texts():
    return (PAGES / "index.html").read_text() + (ROOT / "llms.txt").read_text()


class ProjectPageTests(unittest.TestCase):
    def test_repository_file_links_resolve(self):
        text = page_texts()
        paths = [path for prefix in FILE_PREFIXES for path in re.findall(re.escape(prefix) + r"([^\s\"'<>)#]+)", text)]
        self.assertGreater(len(paths), 10)
        for path in paths:
            self.assertTrue((ROOT / path).is_file(), path)

    def test_readme_anchors_exist(self):
        headings = re.findall(r"^#+ (.+)$", (ROOT / "README.md").read_text(), re.M)
        slugs = {re.sub(r"[^\w\- ]", "", heading.lower()).replace(" ", "-") for heading in headings}
        anchors = re.findall(re.escape(REPO) + r"#([\w-]+)", page_texts())
        self.assertTrue(anchors)
        for anchor in anchors:
            self.assertIn(anchor, slugs)

    def test_metadata_points_at_the_feed_address(self):
        html = (PAGES / "index.html").read_text()
        self.assertIn(f'<link rel="canonical" href="{SITE}">', html)
        self.assertIn(f'<meta property="og:url" content="{SITE}">', html)
        block = re.search(r'<script type="application/ld\+json">(.*?)</script>', html, re.S)
        data = json.loads(block.group(1))
        self.assertEqual(data["url"], SITE)
        self.assertEqual(data["codeRepository"], REPO)
        namespace = "{http://www.sitemaps.org/schemas/sitemap/0.9}"
        locations = [loc.text for loc in ET.parse(PAGES / "sitemap.xml").getroot().iter(namespace + "loc")]
        self.assertEqual(locations, [SITE])
        self.assertIn(f"Sitemap: {SITE}sitemap.xml", (PAGES / "robots.txt").read_text())
        self.assertIn(SITE, (ROOT / "llms.txt").read_text())

    def test_llms_txt_follows_the_llmstxt_layout(self):
        lines = (ROOT / "llms.txt").read_text().splitlines()
        self.assertTrue(lines[0].startswith("# "))
        self.assertTrue(lines[2].startswith("> "))
        sections = [line for line in lines if line.startswith("## ")]
        self.assertTrue(sections)
        if "## Optional" in sections:
            self.assertEqual(sections[-1], "## Optional")
        for line in lines:
            if line.startswith("- ["):
                self.assertRegex(line, r"^- \[[^\]]+\]\(https://[^)]+\): \S")


if __name__ == "__main__":
    unittest.main()
