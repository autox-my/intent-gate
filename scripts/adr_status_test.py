#!/usr/bin/env python3
"""Tests for adr_status.py, including a canary: a broken corpus must fail. Run: python3 -I scripts/adr_status_test.py"""
import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import adr_status  # noqa: E402

README = "| ADR | Decision | Status |\n|---|---|---|\n{rows}"


def corpus(files: dict[str, str], rows: list[tuple[str, str]]) -> pathlib.Path:
    d = pathlib.Path(tempfile.mkdtemp())
    for name, status in files.items():
        (d / name).write_text(f"# ADR\n\n**Status:** {status}\n\n## Context\n", encoding="utf-8")
    body = "".join(f"| [{n[:4]}]({n}) | something | {s} |\n" for n, s in rows)
    (d / "README.md").write_text(README.format(rows=body), encoding="utf-8")
    return d


class Check(unittest.TestCase):
    def test_clean(self):
        d = corpus({"0001-a.md": "Accepted", "0002-b.md": "Superseded by 0003", "0003-c.md": "Proposed (agreed 2026-11-18)"},
                   [("0001-a.md", "Accepted"), ("0002-b.md", "Superseded by 0003"), ("0003-c.md", "Proposed")])
        self.assertEqual(adr_status.check(d), [])

    def test_canary_bad_status(self):
        d = corpus({"0001-a.md": "Done"}, [("0001-a.md", "Done")])
        self.assertTrue(any("must be Proposed" in p for p in adr_status.check(d)))

    def test_index_disagrees(self):
        d = corpus({"0001-a.md": "Accepted"}, [("0001-a.md", "Proposed")])
        self.assertTrue(any("in the index" in p for p in adr_status.check(d)))

    def test_missing_row_and_dangling_supersede(self):
        d = corpus({"0001-a.md": "Superseded by 0009", "0002-b.md": "Accepted"}, [("0001-a.md", "Superseded by 0009")])
        got = adr_status.check(d)
        self.assertTrue(any("does not exist" in p for p in got))
        self.assertTrue(any("no index row for 0002-b.md" in p for p in got))

    def test_repo_is_clean(self):
        repo = pathlib.Path(__file__).resolve().parent.parent / "docs" / "adr"
        self.assertEqual(adr_status.check(repo), [])


if __name__ == "__main__":
    unittest.main()
