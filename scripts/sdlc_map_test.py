#!/usr/bin/env python3
"""Tests for scripts/sdlc-map.py (S-15). Run: python3 -I scripts/sdlc_map_test.py

The renderer's file name has a hyphen, so it is loaded by path. -I drops the script directory from
sys.path, so nothing here imports a sibling by name.
"""
import importlib.util
import os
import re
import subprocess
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCRIPT = os.path.join(ROOT, "scripts", "sdlc-map.py")
YML = os.path.join(ROOT, "docs", "process", "sdlc-map.yml")
MD = os.path.join(ROOT, "docs", "process", "sdlc-map.md")
STATES = {"covered", "partial", "gap", "overlap"}


def load():
    spec = importlib.util.spec_from_file_location("sdlc_map", SCRIPT)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def read(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


def run(*args):
    return subprocess.run([sys.executable, "-I", SCRIPT, *args], cwd=ROOT, capture_output=True, text=True)


class ParseSubset(unittest.TestCase):
    """The YAML subset parser, on small inputs."""

    def test_mapping_sequences_scalars_and_comments(self):
        text = (
            "# a comment\n"
            "name: \"has: colon # and hash\"   # trailing comment\n"
            "plain: some words\n"
            "flag: true\n"
            "count: 3\n"
            "single: 'it''s'\n"
            "paths: [\"a/**\", 'b.md', c]\n"
            "list:\n"
            "  - one\n"
            "  - id: x\n"
            "    reply: true\n"
            "    nested:\n"
            "      - \"q\"\n"
            "  - id: y\n"
        )
        got = load().parse_yaml(text)
        self.assertEqual(got.get("name"), "has: colon # and hash")
        self.assertEqual(got.get("plain"), "some words")
        self.assertIs(got.get("flag"), True)
        self.assertEqual(got.get("count"), 3)
        self.assertEqual(got.get("single"), "it's")
        self.assertEqual(got.get("paths"), ["a/**", "b.md", "c"])
        self.assertEqual(got.get("list"), ["one", {"id": "x", "reply": True, "nested": ["q"]}, {"id": "y"}])

    def test_bad_indentation_is_an_error(self):
        with self.assertRaises(ValueError):
            load().parse_yaml("a:\n    b: 1\n  c: 2\n")


class RealFile(unittest.TestCase):
    """docs/process/sdlc-map.yml has the shape the spec fixes."""

    @classmethod
    def setUpClass(cls):
        assert_exists = os.path.exists(YML)
        cls.data = load().parse_yaml(read(YML)) if assert_exists else {}

    def test_file_exists(self):
        self.assertTrue(os.path.exists(YML), "docs/process/sdlc-map.yml is missing")

    def test_stages_in_order(self):
        ids = [s.get("id") for s in self.data.get("stages", [])]
        self.assertEqual(ids, ["intent", "claims", "design", "spec", "test", "code", "review", "merge"])
        for s in self.data["stages"]:
            for k in ("id", "kind", "title", "question", "detail"):
                self.assertTrue(s.get(k), f"stage {s.get('id')} lacks {k}")

    def test_entries_feed_stages(self):
        entries = self.data.get("entries", [])
        self.assertEqual([e.get("kind") for e in entries], ["PLAN", "TEST", "BUILD", "DEPLOY"])
        stage_ids = {s["id"] for s in self.data.get("stages", [])}
        for e in entries:
            self.assertIn(e.get("feeds"), stage_ids)
            self.assertTrue(e.get("title") and e.get("detail"))

    def test_artifacts(self):
        rows = self.data.get("artifacts", [])
        self.assertEqual(len(rows), 7)
        for r in rows:
            self.assertIn(r.get("state"), STATES)
            for k in ("playbook", "build", "product"):
                self.assertTrue(r.get(k), f"artifact row lacks {k}")

    def test_lifecycle(self):
        life = self.data.get("lifecycle", {})
        lanes = [l.get("id") for l in life.get("lanes", [])]
        self.assertEqual(len(lanes), 8)
        self.assertEqual(len(set(lanes)), 8)
        for l in lanes:
            self.assertRegex(l, r"^[A-Za-z][A-Za-z0-9]*$", "lane ids become mermaid aliases")
        steps = life.get("steps", [])
        self.assertEqual(len(steps), 21)
        for s in steps:
            self.assertIn(s.get("from"), lanes)
            self.assertIn(s.get("to"), lanes)
            self.assertTrue(s.get("text"))

    def test_maintain_line(self):
        self.assertTrue(self.data.get("maintain"))


class Check(unittest.TestCase):
    """--check passes on the committed tree and fails, with the right message, on each drift."""

    def test_passes_on_the_committed_tree(self):
        r = run("--check")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_committed_markdown_is_the_render(self):
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "map.md")
            r = run("--md", out, "--html", os.path.join(d, "map.html"))
            self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
            self.assertEqual(read(out), read(MD))
            html = read(os.path.join(d, "map.html"))
            self.assertIn("IntentGate SDLC map", html)
            self.assertNotIn("__", re.sub(r"__proto__", "", html), "an unfilled template placeholder")

    def committed(self):
        self.assertTrue(os.path.exists(YML) and os.path.exists(MD), "the map's YAML and Markdown are not committed")

    def check_with(self, yml_text=None, md_text=None):
        with tempfile.TemporaryDirectory() as d:
            y, m = os.path.join(d, "sdlc-map.yml"), os.path.join(d, "sdlc-map.md")
            with open(y, "w", encoding="utf-8") as f:
                f.write(yml_text if yml_text is not None else read(YML))
            with open(m, "w", encoding="utf-8") as f:
                f.write(md_text if md_text is not None else read(MD))
            return run("--check", "--yml", y, "--md", m)

    def test_stale_markdown_fails_naming_the_line(self):
        self.committed()
        lines = read(MD).split("\n")
        lines[10] += " (edited by hand)"
        r = self.check_with(md_text="\n".join(lines))
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("stale", r.stderr)
        self.assertIn("line 11", r.stderr)

    def test_missing_markdown_fails(self):
        with tempfile.TemporaryDirectory() as d:
            r = run("--check", "--md", os.path.join(d, "absent.md"))
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("stale", r.stderr)

    def test_missing_path_fails(self):
        self.committed()
        text, n = re.subn(r'(paths:\s*\[)"[^"]+"', r'\1"docs/no-such-dir/**"', read(YML), count=1)
        self.assertEqual(n, 1, "the real file names no paths")
        r = self.check_with(yml_text=text)
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("docs/no-such-dir/**", r.stderr)
        self.assertIn("matches nothing", r.stderr)

    def test_unknown_command_fails(self):
        self.committed()
        text, n = re.subn(r'(commands:\s*\[)"[^"]+"', r'\1"frobnicate"', read(YML), count=1)
        self.assertEqual(n, 1, "the real file names no commands")
        r = self.check_with(yml_text=text)
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("frobnicate", r.stderr)
        self.assertIn("not an intentgate command", r.stderr)


if __name__ == "__main__":
    unittest.main()
