import json
import math
import pathlib
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer

EVAL_ROOT = pathlib.Path(__file__).resolve().parent.parent
if str(EVAL_ROOT) not in sys.path:
    sys.path.insert(0, str(EVAL_ROOT))

from intentgate_eval import cli, dedupe, items, metrics, runner  # noqa: E402


class TestItemsParity(unittest.TestCase):
    def test_bullets_numbers_sentences_dedupe_caps(self):
        text = "\n".join([
            "- Add SARIF output here.",
            "* Keep reports compact always.",
            "+ Ignore.",
            "1. Validate user inputs thoroughly.",
            "2) Reject unknown configuration keys.",
            "First sentence is valid. Second valid sentence! Too short. Third question ends?",
            "- Duplicate item loses earlier position.",
            "Some middle requirement item here.",
            "- Duplicate item loses earlier position.",
        ])
        got = items.items(text, 0)
        want = [
            "Add SARIF output here.",
            "Keep reports compact always.",
            "Validate user inputs thoroughly.",
            "Reject unknown configuration keys.",
            "First sentence is valid.",
            "Second valid sentence!",
            "Third question ends?",
            "Some middle requirement item here.",
            "Duplicate item loses earlier position.",
        ]
        self.assertEqual(got, want)

        many = "\n".join(f"- Requirement line number {i} here." for i in range(25))
        last_20 = items.items(many, 20)
        self.assertEqual(len(last_20), 20)
        self.assertEqual(last_20[0], "Requirement line number 5 here.")
        self.assertEqual(last_20[-1], "Requirement line number 24 here.")

    def test_head_and_tail_utf8_boundaries(self):
        # '€' is 3 UTF-8 bytes; byte cut must align cleanly to UTF-8 rune boundaries.
        s = "ab" + "€" * 4
        h, htrunc = items.head_bytes(s, 7)
        self.assertEqual(h, "ab€")
        self.assertTrue(htrunc)

        t, ttrunc = items.tail_bytes(s, 7)
        self.assertEqual(t, "€€")
        self.assertTrue(ttrunc)

        full, full_trunc = items.head_bytes(s, len(s.encode("utf-8")))
        self.assertEqual(full, s)
        self.assertFalse(full_trunc)

    def test_build_jev_request_shape_and_prompt_isolation(self):
        root = "- Add SARIF output to verify.\n- Never block a merge ever."
        pl = "1. Add SARIF writer module.\n2. Rewrite project in Rust."
        req = items.build_jev_request(root, pl, model="jev-test")
        self.assertEqual(req["model"], "jev-test")
        st = req["state"]
        self.assertEqual(st["root_intent"], root)
        self.assertEqual(st["plan"], pl)
        self.assertFalse(st["root_intent_truncated"])
        self.assertFalse(st["plan_truncated"])
        self.assertEqual(st["items"]["r0"], "Add SARIF output to verify.")
        self.assertEqual(st["items"]["r1"], "Never block a merge ever.")
        self.assertEqual(st["items"]["p0"], "Add SARIF writer module.")
        self.assertEqual(st["items"]["p1"], "Rewrite project in Rust.")
        qs = req["questions"]
        self.assertEqual(set(qs.keys()), {"omit_0", "omit_1", "weak_0", "weak_1", "add_0", "add_1"})
        for qid, q in qs.items():
            self.assertEqual(q["type"], "noul")
            ins = q["instructions"]
            for txt in st["items"].values():
                self.assertNotIn(txt, ins, msg=f"{qid} leaks item text")
            self.assertIn("data", ins.lower())
            self.assertIn("instructions", ins.lower())


class TestDedupeParity(unittest.TestCase):
    def test_cross_kind_precedence_normalization_nan_and_idempotence(self):
        q = "Add a --quiet flag."
        nan = float("nan")
        cases = [
            (
                "omission beats weakening",
                [dedupe.Finding("omission", q, "o", 0.95), dedupe.Finding("weakening", q, "w", 0.85)],
                ["o"],
            ),
            (
                "weakening beats omission",
                [dedupe.Finding("weakening", q, "w", 0.99), dedupe.Finding("omission", q, "o", 0.91)],
                ["w"],
            ),
            (
                "tie omission over weakening",
                [dedupe.Finding("weakening", q, "w", 0.9), dedupe.Finding("omission", q, "o", 0.9)],
                ["o"],
            ),
            (
                "tie weakening over addition",
                [dedupe.Finding("addition", q, "a", 0.8), dedupe.Finding("weakening", q, "w", 0.8)],
                ["w"],
            ),
            (
                "whitespace and case normalization",
                [
                    dedupe.Finding("omission", "  Add  a --quiet flag. ", "o", 0.7),
                    dedupe.Finding("weakening", "add a --quiet flag.", "w", 0.9),
                ],
                ["w"],
            ),
            (
                "same kind preserved",
                [
                    dedupe.Finding("weakening", q, "w1", 0.99),
                    dedupe.Finding("omission", q, "o", 0.90),
                    dedupe.Finding("weakening", q, "w2", 0.70),
                ],
                ["w1", "w2"],
            ),
            (
                "empty quotes never merged",
                [
                    dedupe.Finding("omission", "", "o", 0.9),
                    dedupe.Finding("weakening", "   ", "w", 0.5),
                    dedupe.Finding("addition", "\t", "a", 0.4),
                ],
                ["o", "w", "a"],
            ),
            (
                "nan loses to finite zero",
                [dedupe.Finding("omission", q, "o", nan), dedupe.Finding("weakening", q, "w", 0.0)],
                ["w"],
            ),
            (
                "two nans break by kind",
                [
                    dedupe.Finding("addition", q, "a", nan),
                    dedupe.Finding("weakening", q, "w", nan),
                    dedupe.Finding("omission", q, "o", nan),
                ],
                ["o"],
            ),
        ]
        for name, inp, want in cases:
            with self.subTest(name=name):
                out = dedupe.dedupe_cross_kind(inp)
                self.assertEqual([f.text for f in out], want)
                self.assertEqual(dedupe.dedupe_cross_kind(out), out)

    def test_map_answers_boundaries(self):
        roots = ["Root requirement one here.", "Root requirement two here."]
        plans = ["Plan item one here.", "Plan item two here."]
        ans = {
            "omit_0": 0.24,  # < 0.25 -> triggers
            "omit_1": 0.25,  # exact -> does NOT trigger
            "weak_0": 0.75,  # exact -> does NOT trigger
            "weak_1": 0.76,  # > 0.75 -> triggers
            "add_0": 0.29,   # < 0.30 -> triggers
            "add_1": 0.30,   # exact -> does NOT trigger
        }
        got = dedupe.map_answers(roots, plans, ans, omit_max=0.25, weak_min=0.75, add_max=0.30)
        self.assertEqual(
            [(f.kind, f.quote) for f in got],
            [
                ("omission", "Root requirement one here."),
                ("weakening", "Root requirement two here."),
                ("addition", "Plan item one here."),
            ],
        )


class TestMetricsAndSweep(unittest.TestCase):
    def test_brier_and_ece_known_values(self):
        brier = metrics.brier_score([0.9, 0.2], [1, 0])
        self.assertIsNotNone(brier)
        self.assertAlmostEqual(brier, 0.025, places=6)

        ece = metrics.ece_score([0.95, 0.95], [1, 0], bins=10)
        self.assertIsNotNone(ece)
        self.assertAlmostEqual(ece, 0.45, places=6)

    def test_evaluate_and_sweep(self):
        ds = [
            {
                "id": "c1",
                "category": "omission",
                "root_intent": "- Keep strict mode.\n- Add JSON output.",
                "plan": "1. Add JSON output.",
                "expected_omissions": ["Keep strict mode."],
                "expected_weakenings": [],
                "expected_additions": [],
            },
            {
                "id": "c2",
                "category": "weakening",
                "root_intent": "- Require auth everywhere.",
                "plan": "1. Make auth optional.\n2. Rewrite CLI in Rust.",
                "expected_omissions": [],
                "expected_weakenings": ["Require auth everywhere."],
                "expected_additions": ["Rewrite CLI in Rust."],
            },
        ]
        scores = [
            {
                "case_id": "c1",
                "model": "jev-test",
                "latency_ms": 100,
                "root_items": ["Keep strict mode.", "Add JSON output."],
                "plan_items": ["Add JSON output."],
                "answers": {
                    "omit_0": 0.10, "weak_0": 0.60,
                    "omit_1": 0.80, "weak_1": 0.10,
                    "add_0": 0.85,
                },
            },
            {
                "case_id": "c2",
                "model": "jev-test",
                "latency_ms": 100,
                "root_items": ["Require auth everywhere."],
                "plan_items": ["Make auth optional.", "Rewrite CLI in Rust."],
                "answers": {
                    "omit_0": 0.40, "weak_0": 0.88,
                    "add_0": 0.22,  # noisy weakening plan item scored 0.22 -> FP at 0.30, avoided at 0.20
                    "add_1": 0.05,
                },
            },
        ]
        base = metrics.evaluate_scores(ds, scores, omit_max=0.30, weak_min=0.70, add_max=0.30)
        self.assertEqual(base["families"]["omission"]["tp"], 1)
        self.assertEqual(base["families"]["weakening"]["tp"], 1)
        self.assertEqual(base["families"]["addition"]["tp"], 1)
        self.assertEqual(base["families"]["addition"]["fp"], 1)
        self.assertAlmostEqual(base["families"]["addition"]["precision"], 0.5, places=6)

        sw = metrics.sweep_thresholds(ds, scores, min_precision=0.90)
        self.assertTrue(sw["meets_precision_target"])
        best = sw["best"]["thresholds"]
        self.assertLessEqual(best["add_max"], 0.20)
        self.assertEqual(best["omit_max"], 0.30)  # tie-break prefers baseline 0.30
        self.assertEqual(best["weak_min"], 0.70)


class TestRunnerTransport(unittest.TestCase):
    def test_url_validation_and_error_scrubbing(self):
        for bad in [
            "http://example.com",
            "https://user:pass@api.typesafe.ai",
            "https://api.typesafe.ai?foo=bar",
            "https://api.typesafe.ai#frag",
            "ftp://api.typesafe.ai",
        ]:
            with self.assertRaises(ValueError):
                runner.validate_base_url(bad)
        self.assertEqual(runner.validate_base_url("http://127.0.0.1:8080/"), "http://127.0.0.1:8080")
        self.assertEqual(runner.validate_base_url("https://api.typesafe.ai/"), "https://api.typesafe.ai")

    def test_loopback_roundtrip_and_redirect_rejection(self):
        seen_headers = {}

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                return

            def do_POST(self):
                if self.path == "/redirect/v1/systemone":
                    self.send_response(302)
                    self.send_header("Location", "https://evil.example/")
                    self.end_headers()
                    return
                seen_headers["auth"] = self.headers.get("Authorization")
                length = int(self.headers.get("Content-Length", "0"))
                req = json.loads(self.rfile.read(length))
                ans = {qid: {"type": "noul", "noul": 0.85} for qid in req["questions"]}
                body = json.dumps({"model": "jev-loopback", "answers": ans}).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        srv = HTTPServer(("127.0.0.1", 0), Handler)
        th = threading.Thread(target=srv.serve_forever, daemon=True)
        th.start()
        try:
            port = srv.server_address[1]
            rows = [{
                "id": "case-1",
                "category": "clean",
                "root_intent": "- Add a --json flag.",
                "plan": "1. Add a --json flag.",
                "expected_omissions": [],
                "expected_weakenings": [],
                "expected_additions": [],
            }]
            res = runner.run_dataset(rows, f"http://127.0.0.1:{port}", "secret-key-999", model="jev-test")
            self.assertEqual(len(res), 1)
            self.assertEqual(seen_headers.get("auth"), "Bearer secret-key-999")
            self.assertEqual(res[0]["case_id"], "case-1")
            self.assertEqual(res[0]["model"], "jev-loopback")
            self.assertIn("omit_0", res[0]["answers"])

            with self.assertRaises(RuntimeError) as ctx:
                runner.ask_jev(f"http://127.0.0.1:{port}/redirect", "secret-key-999", {"model": "m", "state": {}, "questions": {}})
            self.assertNotIn("secret-key-999", str(ctx.exception))
        finally:
            srv.shutdown()
            srv.server_close()


class TestDatasetIntegrity(unittest.TestCase):
    def test_dataset_items_and_label_count(self):
        path = EVAL_ROOT / "datasets" / "plan_review.jsonl"
        lines = [line for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
        self.assertGreaterEqual(len(lines), 25)
        ids = set()
        total_item_labels = 0
        for idx, line in enumerate(lines, 1):
            row = json.loads(line)
            self.assertNotIn(row["id"], ids, f"duplicate id on line {idx}")
            ids.add(row["id"])
            ritems = items.items(row["root_intent"], 20)
            pitems = items.items(row["plan"], 0)[:20]
            self.assertGreater(len(ritems), 0, f"{row['id']} has empty root_items")
            self.assertGreater(len(pitems), 0, f"{row['id']} has empty plan_items")
            total_item_labels += len(ritems) + len(pitems)
            for q in row["expected_omissions"] + row["expected_weakenings"]:
                self.assertIn(q, ritems, f"{row['id']}: root label {q!r} not in {ritems!r}")
            for q in row["expected_additions"]:
                self.assertIn(q, pitems, f"{row['id']}: plan label {q!r} not in {pitems!r}")
            overlap = set(row["expected_omissions"]) & set(row["expected_weakenings"])
            self.assertFalse(overlap, f"{row['id']}: quote in both omission and weakening: {overlap}")
        self.assertGreaterEqual(total_item_labels, 90)


if __name__ == "__main__":
    unittest.main()
