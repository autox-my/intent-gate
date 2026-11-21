#!/usr/bin/env python3
"""Check ADR statuses (docs/adr/README.md, "Status"). Run: python3 -I scripts/adr_status.py [adr_dir]

Every docs/adr/NNNN-*.md has one `**Status:**` line whose value is `Proposed`, `Accepted` or
`Superseded by NNNN` (a parenthetical note may follow), a superseding ADR exists, and the README's
index has a row for every ADR with the same status. Exit 0 clean, 1 with the problems listed.
"""
import pathlib
import re
import sys

STATUS = re.compile(r"^\*\*Status:\*\*\s*(Proposed|Accepted|Superseded by (\d{4}))(\s+\(.*\))?\s*$")
ROW = re.compile(r"^\|\s*\[(\d{4})\]\((\d{4}-[^)]+\.md)\)\s*\|.*\|\s*([^|]+?)\s*\|\s*$")


def check(adr_dir: pathlib.Path) -> list[str]:
    problems = []
    files = sorted(p for p in adr_dir.glob("[0-9][0-9][0-9][0-9]-*.md"))
    numbers = {p.name[:4] for p in files}
    status = {}
    for p in files:
        lines = [l for l in p.read_text(encoding="utf-8").splitlines() if l.startswith("**Status:**")]
        if len(lines) != 1:
            problems.append(f"{p.name}: want exactly one **Status:** line, found {len(lines)}")
            continue
        m = STATUS.match(lines[0])
        if not m:
            problems.append(f"{p.name}: status must be Proposed, Accepted or Superseded by NNNN: {lines[0]!r}")
            continue
        if m.group(2) and m.group(2) not in numbers:
            problems.append(f"{p.name}: superseded by {m.group(2)}, which does not exist")
        status[p.name] = m.group(1)
    readme = adr_dir / "README.md"
    rows = {}
    for line in readme.read_text(encoding="utf-8").splitlines():
        m = ROW.match(line)
        if m:
            rows[m.group(2)] = m.group(3)
    for name, st in status.items():
        if name not in rows:
            problems.append(f"README.md: no index row for {name}")
        elif rows[name] != st:
            problems.append(f"README.md: {name} is {rows[name]!r} in the index but {st!r} in the file")
    for name in rows:
        if not (adr_dir / name).exists():
            problems.append(f"README.md: index row for {name}, which does not exist")
    return problems


def main() -> int:
    adr_dir = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else "docs/adr")
    problems = check(adr_dir)
    for p in problems:
        print(p)
    if problems:
        print(f"adr-status: {len(problems)} problem(s)")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
