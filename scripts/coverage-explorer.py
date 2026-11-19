#!/usr/bin/env python3
"""Build the requirements coverage explorer: every claim, its design element, the slices that
name it, and its linked tests with their results.

Inputs: `req trace --format json`, `req satisfy --format json`, build/junit.xml (run
scripts/check.sh first) and docs/specs/S-*.md. Output: req-views/coverage.html (git-ignored),
a single self-contained page. Usage: python3 -I scripts/coverage-explorer.py [--out <file>]
"""
import argparse
import glob
import json
import os
import re
import subprocess
import sys
import xml.etree.ElementTree as ET

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def req_json(*args):
    out = subprocess.run(["req", *args, "--format", "json"], cwd=ROOT, capture_output=True, text=True)
    if not out.stdout.strip():
        sys.exit(f"req {' '.join(args)} printed nothing (exit {out.returncode}): {out.stderr.strip()}")
    return json.loads(out.stdout)


def junit_results(path):
    if not os.path.exists(path):
        sys.exit(f"{path} is missing: run scripts/check.sh first")
    results, total = {}, 0
    for tc in ET.parse(path).getroot().iter("testcase"):
        status = "pass"
        if tc.find("failure") is not None or tc.find("error") is not None:
            status = "fail"
        elif tc.find("skipped") is not None:
            status = "skip"
        results[f"{tc.get('classname')}::{tc.get('name')}"] = (status, float(tc.get("time") or 0))
        total += 1
    return results, total


def spec_criteria():
    """Claim ids each slice's spec names in its Criteria section (ranges and lists expanded)."""
    specs = {}
    for path in sorted(glob.glob(os.path.join(ROOT, "docs/specs/S-*.md"))):
        text = open(path, encoding="utf-8").read()
        title = text.splitlines()[0].lstrip("# ").strip()
        m = re.search(r"## Criteria.*?\n(.*?)\n## ", text, re.S)
        section = m.group(1) if m else ""
        ids = set()
        for a, b in re.findall(r"REQ-(\d{4})(?:\s*(?:…|\.\.\.|to)\s*REQ-(\d{4}))?", section):
            ids.update(f"REQ-{i:04d}" for i in range(int(a), int(b or a) + 1))
        for run in re.findall(r"REQ-\d{4}(?:[/,]\s*\d{4})+", section):
            ids.update(f"REQ-{n}" for n in re.findall(r"\d{4}", run))
        specs[os.path.basename(path)[:4]] = {"title": title, "ids": ids}
    return specs


def pins_of(claim_id):
    text = open(os.path.join(ROOT, "claims", f"{claim_id}.yml"), encoding="utf-8").read()
    m = re.search(r"^pins:\n((?:  .*\n)+)", text, re.M)
    return dict(re.findall(r'^\s+(\w+):\s*"?([^"\n]*)"?', m.group(1), re.M)) if m else None


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--out", default=os.path.join(ROOT, "req-views", "coverage.html"))
    args = ap.parse_args()

    trace, satisfy = req_json("trace"), req_json("satisfy")
    results, total = junit_results(os.path.join(ROOT, "build", "junit.xml"))
    specs = spec_criteria()
    links = {}
    for link in satisfy["links"]:
        links.setdefault(link["claim_id"], []).append({"element": link["element"], "outcome": link["outcome"]})
    gate = subprocess.run(["req", "gate", "--test-report", "build/junit.xml"], cwd=ROOT, capture_output=True)

    claims = []
    for node in trace["nodes"]:
        cid = node["claim_id"]
        claims.append({
            "id": cid, "statement": node["statement"], "address": node["address"], "source": node["source"],
            "tests": [{"id": t, "status": results.get(t, ("missing", 0))[0], "time": results.get(t, (None, 0))[1]}
                      for t in node["tests"]],
            "design": links.get(cid, []),
            "slices": sorted(k for k, v in specs.items() if cid in v["ids"]),
            "pins": pins_of(cid),
        })
    commit, when = subprocess.check_output(["git", "log", "-1", "--format=%h %cI"], cwd=ROOT, text=True).split()
    data = {"generated": when, "commit": commit, "junit_total": total, "claims": claims,
            "specs": {k: {"title": v["title"]} for k, v in specs.items()},
            "gate": "GREEN" if gate.returncode == 0 else f"exit {gate.returncode}"}

    template = open(os.path.join(ROOT, "scripts", "coverage-explorer.tmpl.html"), encoding="utf-8").read()
    page = template.replace("__DATA__", json.dumps(data, separators=(",", ":")).replace("</", "<\\/"))
    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w", encoding="utf-8") as f:
        f.write(page)
    print(f"wrote {os.path.relpath(args.out, ROOT)}: {len(claims)} claims, {sum(len(c['tests']) for c in claims)} test links, req gate {data['gate']}")


if __name__ == "__main__":
    main()
