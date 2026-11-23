import argparse
import json
import os
import pathlib
import sys

from .metrics import evaluate_scores, sweep_thresholds
from .runner import DEFAULT_BASE, DEFAULT_MODEL, run_dataset


def _read_jsonl(path: str) -> list[dict]:
    lines = pathlib.Path(path).read_text(encoding="utf-8").splitlines()
    return [json.loads(line) for line in lines if line.strip()]


def _write_jsonl(path: str, rows: list[dict]) -> None:
    out = pathlib.Path(path)
    out.parent.mkdir(parents=True, exist_ok=True)
    body = "".join(json.dumps(row, sort_keys=True) + "\n" for row in rows)
    out.write_text(body, encoding="utf-8")


def _format_score_text(doc: dict) -> str:
    th = doc["thresholds"]
    lines = [
        f"cases: {doc['cases']}  thresholds: omit<{th['omit_max']:.2f} weak>{th['weak_min']:.2f} add<{th['add_max']:.2f}",
        f"macro: f1={doc['macro']['f1']:.4f}  precision={doc['macro']['precision']:.4f}",
    ]
    for k, m in doc["families"].items():
        p = f"{m['precision']:.4f}" if m["precision"] is not None else "n/a"
        r = f"{m['recall']:.4f}" if m["recall"] is not None else "n/a"
        b = f"{m['brier']:.4f}" if m["brier"] is not None else "n/a"
        e = f"{m['ece']:.4f}" if m["ece"] is not None else "n/a"
        lines.append(
            f"  {k:<10} tp={m['tp']:2d} fp={m['fp']:2d} fn={m['fn']:2d} tn={m['tn']:2d} "
            f"prec={p} rec={r} f1={m['f1']:.4f} brier={b} ece={e}"
        )
    return "\n".join(lines) + "\n"


def _format_sweep_text(doc: dict) -> str:
    lines = [
        f"grid: {doc['grid_size']}  min_precision={doc['min_precision']:.2f}  meets_target={doc['meets_precision_target']}",
        "-- baseline --",
        _format_score_text(doc["baseline"]).rstrip("\n"),
        "-- best --",
        _format_score_text(doc["best"]).rstrip("\n"),
    ]
    return "\n".join(lines) + "\n"


def _cmd_run(ns: argparse.Namespace) -> int:
    key = os.environ.get("TYPESAFE_API_KEY", "")
    if not key:
        print("TYPESAFE_API_KEY must be set in environment", file=sys.stderr)
        return 2
    base = ns.base_url or os.environ.get("TYPESAFE_BASE_URL") or DEFAULT_BASE
    model = ns.model or os.environ.get("TYPESAFE_MODEL") or DEFAULT_MODEL
    rows = run_dataset(_read_jsonl(ns.dataset), base, key, model=model, timeout_s=ns.timeout)
    _write_jsonl(ns.out, rows)
    print(f"wrote {len(rows)} score rows to {ns.out}")
    return 0


def _cmd_score(ns: argparse.Namespace) -> int:
    doc = evaluate_scores(
        _read_jsonl(ns.dataset),
        _read_jsonl(ns.scores),
        omit_max=ns.omit_max,
        weak_min=ns.weak_min,
        add_max=ns.add_max,
    )
    out = json.dumps(doc, indent=2, sort_keys=True) + "\n" if ns.format == "json" else _format_score_text(doc)
    sys.stdout.write(out)
    return 0


def _cmd_sweep(ns: argparse.Namespace) -> int:
    doc = sweep_thresholds(_read_jsonl(ns.dataset), _read_jsonl(ns.scores), min_precision=ns.min_precision)
    out = json.dumps(doc, indent=2, sort_keys=True) + "\n" if ns.format == "json" else _format_sweep_text(doc)
    sys.stdout.write(out)
    return 0


def _build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="intentgate-eval")
    sub = p.add_subparsers(dest="cmd", required=True)

    r = sub.add_parser("run")
    r.add_argument("--dataset", required=True)
    r.add_argument("--out", required=True)
    r.add_argument("--base-url", default="")
    r.add_argument("--model", default="")
    r.add_argument("--timeout", type=float, default=10.0)

    s = sub.add_parser("score")
    s.add_argument("--dataset", required=True)
    s.add_argument("--scores", required=True)
    s.add_argument("--omit-max", type=float, default=0.30)
    s.add_argument("--weak-min", type=float, default=0.70)
    s.add_argument("--add-max", type=float, default=0.30)
    s.add_argument("--format", choices=("text", "json"), default="text")

    w = sub.add_parser("sweep")
    w.add_argument("--dataset", required=True)
    w.add_argument("--scores", required=True)
    w.add_argument("--min-precision", type=float, default=0.90)
    w.add_argument("--format", choices=("text", "json"), default="text")
    return p


def main(argv: list[str] | None = None) -> int:
    ns = _build_parser().parse_args(argv)
    if ns.cmd == "run":
        return _cmd_run(ns)
    if ns.cmd == "score":
        return _cmd_score(ns)
    return _cmd_sweep(ns)
