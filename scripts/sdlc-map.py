#!/usr/bin/env python3
"""Render the IntentGate SDLC map from docs/process/sdlc-map.yml, and check it against the repo.

  python3 -I scripts/sdlc-map.py            write docs/process/sdlc-map.md (GitHub, mermaid) and
                                            req-views/sdlc-map.html (git-ignored, the reference page's look)
  python3 -I scripts/sdlc-map.py --check    write nothing; exit 1 when the committed Markdown differs from the
                                            YAML's render, a `paths:` entry matches nothing in the repo, or a
                                            `commands:` entry is not an `intentgate` subcommand; exit 3 when
                                            that cannot be decided (the intentgate binary will not build)

Options: --yml FILE, --md FILE, --html FILE override the three files (the tests use them). Python 3, standard
library only. The YAML is read by a small subset parser (see parse_yaml), so the format stays this file's own.
"""
import argparse
import html
import json
import os
import re
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
YML = os.path.join(ROOT, "docs", "process", "sdlc-map.yml")
MD = os.path.join(ROOT, "docs", "process", "sdlc-map.md")
HTML = os.path.join(ROOT, "req-views", "sdlc-map.html")
TMPL = os.path.join(ROOT, "scripts", "sdlc-map.tmpl.html")
STATES = ("covered", "partial", "gap", "overlap")


def shown(path):
    """A repo-relative name for messages when the file is inside the repo."""
    return os.path.relpath(path, ROOT) if path.startswith(ROOT + os.sep) else path


# ---------------------------------------------------------------------------------------------------------
# YAML subset: block mappings and sequences, flow lists [a, "b"] and flow mappings {k: v} of scalars,
# double- or single-quoted strings, plain strings, true/false, integers, `#` comments. No anchors, no
# multi-line scalars, no tabs, no nested flow collections. Anything else is a ValueError naming the line.
# ---------------------------------------------------------------------------------------------------------
KEY = re.compile(r"^([A-Za-z_][\w-]*):(?:\s+(.*))?$")


def _strip_comment(line):
    quote = None
    i = 0
    while i < len(line):
        c = line[i]
        if quote == '"' and c == "\\":
            i += 2
            continue
        if quote:
            if c == quote:
                if quote == "'" and line[i + 1:i + 2] == "'":
                    i += 2
                    continue
                quote = None
        elif c in "\"'" and (i == 0 or line[i - 1] in " [{,:"):
            quote = c
        elif c == "#" and (i == 0 or line[i - 1] in " \t"):
            return line[:i].rstrip()
        i += 1
    return line.rstrip()


def _unquote(tok, n):
    if len(tok) < 2 or tok[-1] != tok[0]:
        raise ValueError(f"line {n}: unterminated string {tok!r}")
    if tok[0] == "'":
        return tok[1:-1].replace("''", "'")
    esc = {"n": "\n", "t": "\t", '"': '"', "\\": "\\", "/": "/"}
    body, out, i = tok[1:-1], [], 0
    while i < len(body):
        if body[i] == "\\":
            i += 1
            if i >= len(body) or body[i] not in esc:
                raise ValueError(f"line {n}: unsupported escape in {tok!r}")
            out.append(esc[body[i]])
        else:
            out.append(body[i])
        i += 1
    return "".join(out)


def _split_flow(body, n):
    """Split a flow collection's body at the commas outside quotes."""
    parts, cur, quote, i = [], "", None, 0
    while i < len(body):
        c = body[i]
        if quote:
            cur += c
            if quote == '"' and c == "\\" and i + 1 < len(body):
                i += 1
                cur += body[i]
            elif c == quote:
                quote = None
        elif c in "\"'" and (cur.strip() == "" or cur.rstrip().endswith(":")):
            quote = c
            cur += c
        elif c == ",":
            parts.append(cur.strip())
            cur = ""
        elif c in "[{":
            raise ValueError(f"line {n}: nested flow collections are not supported")
        else:
            cur += c
        i += 1
    if quote:
        raise ValueError(f"line {n}: unterminated string in a flow collection")
    if cur.strip():
        parts.append(cur.strip())
    return parts


def _scalar(tok, n):
    tok = tok.strip()
    if tok == "":
        return None
    if tok[0] in "\"'":
        return _unquote(tok, n)
    if tok[0] == "[":
        if tok[-1] != "]":
            raise ValueError(f"line {n}: unterminated flow list")
        return [_scalar(p, n) for p in _split_flow(tok[1:-1], n)]
    if tok[0] == "{":
        if tok[-1] != "}":
            raise ValueError(f"line {n}: unterminated flow mapping")
        out = {}
        for p in _split_flow(tok[1:-1], n):
            m = KEY.match(p)
            if not m:
                raise ValueError(f"line {n}: bad flow mapping entry {p!r}")
            if m.group(1) in out:
                raise ValueError(f"line {n}: duplicate key {m.group(1)!r}")
            out[m.group(1)] = _scalar(m.group(2) or "", n)
        return out
    if tok in ("true", "false"):
        return tok == "true"
    if re.fullmatch(r"-?\d+", tok):
        return int(tok)
    if tok[0] in "&*!|>%@`":
        raise ValueError(f"line {n}: unsupported YAML syntax {tok!r}")
    return tok


def _is_item(content):
    return content == "-" or content.startswith("- ")


def parse_yaml(text):
    lines = []  # [line number, indent, content]
    for n, raw in enumerate(text.split("\n"), 1):
        if "\t" in raw[:len(raw) - len(raw.lstrip())]:
            raise ValueError(f"line {n}: tabs are not allowed in indentation")
        body = _strip_comment(raw)
        if body.strip():
            lines.append([n, len(body) - len(body.lstrip(" ")), body.strip()])
    if not lines:
        return {}
    pos = [0]

    def block(indent):
        n, ind, content = lines[pos[0]]
        if ind != indent:
            raise ValueError(f"line {n}: bad indentation")
        return seq(indent) if _is_item(content) else mapping(indent)

    def nested(indent):
        """The value of a `key:` with nothing after it: a deeper block (a sequence may sit level with its key)."""
        if pos[0] < len(lines):
            _, ind, content = lines[pos[0]]
            if ind > indent or (ind == indent and _is_item(content)):
                return block(ind)
        return None

    def mapping(indent):
        out = {}
        while pos[0] < len(lines):
            n, ind, content = lines[pos[0]]
            if ind < indent or _is_item(content):
                break
            if ind > indent:
                raise ValueError(f"line {n}: bad indentation")
            m = KEY.match(content)
            if not m:
                raise ValueError(f"line {n}: expected `key: value`, got {content!r}")
            key, rest = m.group(1), m.group(2)
            if key in out:
                raise ValueError(f"line {n}: duplicate key {key!r}")
            pos[0] += 1
            out[key] = _scalar(rest, n) if rest else nested(indent)
        return out

    def seq(indent):
        out = []
        while pos[0] < len(lines):
            n, ind, content = lines[pos[0]]
            if ind < indent or not _is_item(content):
                break
            if ind > indent:
                raise ValueError(f"line {n}: bad indentation")
            after = content[1:].lstrip(" ")
            if after == "":
                pos[0] += 1
                out.append(nested(indent))
            elif KEY.match(after):
                # a mapping item: its first key sits after "- ", and the rest align to that column
                col = ind + len(content) - len(after)
                lines[pos[0]] = [n, col, after]
                out.append(mapping(col))
            else:
                pos[0] += 1
                out.append(_scalar(after, n))
        return out

    result = block(lines[0][1])
    if pos[0] != len(lines):
        raise ValueError(f"line {lines[pos[0]][0]}: unexpected content")
    return result


# ---------------------------------------------------------------------------------------------------------
# The map's data, validated
# ---------------------------------------------------------------------------------------------------------
def load(path):
    with open(path, encoding="utf-8") as f:
        data = parse_yaml(f.read())
    validate(data, shown(path))
    return data


def validate(d, name):
    def need(cond, msg):
        if not cond:
            raise ValueError(f"{name}: {msg}")

    need(isinstance(d, dict), "the top level must be a mapping")
    for k in ("page", "entries", "stages", "maintain", "notes", "artifacts", "lifecycle"):
        need(k in d, f"missing top-level key {k!r}")
    for k in ("eyebrow", "title", "lede", "stages_title", "stages_text", "artifacts_title", "artifacts_text",
              "lifecycle_title", "lifecycle_text", "lifecycle_note"):
        need(isinstance(d["page"].get(k), str), f"page.{k} must be a string")
    need(isinstance(d["maintain"], str), "maintain must be a string")
    ids = [s.get("id") for s in d["stages"]]
    need(ids and len(set(ids)) == len(ids), "stages need unique ids")
    for i, s in enumerate(d["stages"]):
        for k in ("id", "kind", "title", "question", "detail"):
            need(isinstance(s.get(k), str) and s[k], f"stages[{i}] lacks {k}")
        need(re.fullmatch(r"[a-z][a-z0-9]*", s["id"]), f"stages[{i}].id {s['id']!r} must be lower-case letters and digits")
    for i, e in enumerate(d["entries"]):
        for k in ("kind", "title", "detail", "feeds"):
            need(isinstance(e.get(k), str) and e[k], f"entries[{i}] lacks {k}")
        need(e["feeds"] in ids, f"entries[{i}].feeds {e['feeds']!r} is not a stage id")
    need(len({e["feeds"] for e in d["entries"]}) == len(d["entries"]), "two entries feed the same stage")
    for i, n in enumerate(d["notes"]):
        need(n.get("section") in ("stages", "artifacts"), f"notes[{i}].section must be stages or artifacts")
        need(isinstance(n.get("title"), str) and isinstance(n.get("items"), list), f"notes[{i}] needs title and items")
    for i, r in enumerate(d["artifacts"]):
        for k in ("playbook", "build", "product"):
            need(isinstance(r.get(k), str) and r[k], f"artifacts[{i}] lacks {k}")
        need(r.get("state") in STATES, f"artifacts[{i}].state must be one of {', '.join(STATES)}")
    lanes = d["lifecycle"].get("lanes") or []
    lane_ids = [l.get("id") for l in lanes]
    need(lane_ids and len(set(lane_ids)) == len(lane_ids), "lifecycle.lanes need unique ids")
    for i, l in enumerate(lanes):
        need(re.fullmatch(r"[A-Za-z][A-Za-z0-9]*", str(l.get("id"))), f"lifecycle.lanes[{i}].id must be letters and digits (it is a mermaid alias)")
        need(isinstance(l.get("name"), str), f"lifecycle.lanes[{i}] lacks name")
    for i, s in enumerate(d["lifecycle"].get("steps") or []):
        need(s.get("from") in lane_ids and s.get("to") in lane_ids, f"lifecycle.steps[{i}] names an unknown lane")
        need(isinstance(s.get("text"), str) and s["text"], f"lifecycle.steps[{i}] lacks text")
    for where, item in items_with_refs(d):
        for k in ("paths", "commands"):
            v = item.get(k)
            need(v is None or (isinstance(v, list) and all(isinstance(x, str) and x for x in v)), f"{where}.{k} must be a list of strings")


def items_with_refs(d):
    """Every item that may carry paths: or commands:, with a name for messages."""
    for sec in ("entries", "stages", "artifacts"):
        for i, it in enumerate(d[sec]):
            yield f"{sec}[{i}] ({it.get('id') or it.get('kind') or it.get('playbook')})", it
    for i, l in enumerate(d["lifecycle"]["lanes"]):
        yield f"lifecycle.lanes[{i}] ({l['id']})", l
    for i, s in enumerate(d["lifecycle"]["steps"]):
        yield f"lifecycle.steps[{i}] (step {i + 1})", s


# ---------------------------------------------------------------------------------------------------------
# Markdown (GitHub renders the mermaid blocks)
# ---------------------------------------------------------------------------------------------------------
def mm(text):
    """Text safe inside a double-quoted mermaid label or a sequence message."""
    return text.replace('"', "'").replace(";", ",").replace("#", "no.").replace("\n", " ")


def cell(text):
    return text.replace("|", "\\|")


def notes_md(d, section):
    out = []
    for n in d["notes"]:
        if n["section"] == section:
            out += [f"**{n['title']}**", ""] + [f"- {it}" for it in n["items"]] + [""]
    return out


def render_markdown(d):
    p, stages, steps = d["page"], d["stages"], d["lifecycle"]["steps"]
    out = [
        "<!-- Generated by scripts/sdlc-map.py from docs/process/sdlc-map.yml. Do not edit this file:",
        "     edit the YAML and run `python3 -I scripts/sdlc-map.py`. scripts/check.sh fails when they differ. -->",
        "",
        f"# {p['title']}",
        "",
        p["eyebrow"],
        "",
        p["lede"],
        "",
        f"## {p['stages_title']}",
        "",
        p["stages_text"],
        "",
        "```mermaid",
        "flowchart LR",
        '  subgraph entries["Start anywhere"]',
    ]
    for i, e in enumerate(d["entries"]):
        out.append(f'    e{i + 1}["{mm(e["kind"])}: {mm(e["title"])}"]')
    out.append("  end")
    for i, s in enumerate(stages):
        out.append(f'  {s["id"]}["{i + 1} {mm(s["kind"])}: {mm(s["question"])}"]')
    out.append("  " + " --> ".join(s["id"] for s in stages))
    for i, e in enumerate(d["entries"]):
        out.append(f"  e{i + 1} --> {e['feeds']}")
    out.append(f'  maintain["MAINTAIN: {mm(d["maintain"])}"]')
    out.append(f"  {stages[-1]['id']} -.-> maintain")
    out.append(f"  maintain -.-> {stages[0]['id']}")
    out += ["```", "", "| # | Stage | Question | Where |", "|---|---|---|---|"]
    for i, s in enumerate(stages):
        out.append(f"| {i + 1} | {cell(s['kind'])} | {cell(s['question'])} | {cell(s['detail'])} |")
    out += ["", f"**↺ MAINTAIN** {d['maintain']}", ""]
    out += notes_md(d, "stages")
    out += [f"## {p['artifacts_title']}", "", p["artifacts_text"], "",
            "| Playbook | Build loop (this repo) | Product loop (a team using IntentGate) | State |", "|---|---|---|---|"]
    for r in d["artifacts"]:
        pb = cell(r["playbook"]) + (f"<br>*{cell(r['note'])}*" if r.get("note") else "")
        out.append(f"| {pb} | {cell(r['build'])} | {cell(r['product'])} | {r['state']} |")
    out.append("")
    out += notes_md(d, "artifacts")
    out += [f"## {p['lifecycle_title']}", "", p["lifecycle_text"], "", "```mermaid", "sequenceDiagram"]
    for l in d["lifecycle"]["lanes"]:
        label = l["name"] + (f", {l['sub']}" if l.get("sub") else "")
        out.append(f"  participant {l['id']} as {mm(label)}")
    inopt = False
    for i, s in enumerate(steps):
        if s.get("optional") and not inopt:
            out.append("  opt optional")
            inopt = True
        arrow = "-->>" if s.get("reply") else "->>"
        out.append(f"  {'  ' if inopt else ''}{s['from']}{arrow}{s['to']}: {i + 1}. {mm(s['text'])}")
        if inopt and not (steps[i + 1] if i + 1 < len(steps) else {}).get("optional"):
            out.append("  end")
            inopt = False
    out += ["```", "", p["lifecycle_note"], ""]
    return "\n".join(out)


# ---------------------------------------------------------------------------------------------------------
# The local page: the reference page's look, filled from the YAML by scripts/sdlc-map.tmpl.html
# ---------------------------------------------------------------------------------------------------------
def esc(text):
    return html.escape(text, quote=True)


def inline(text):
    """`code`, **bold**, *italic* to HTML; everything else escaped."""
    out = []
    for i, seg in enumerate(text.split("`")):
        if i % 2:
            out.append(f"<code>{esc(seg)}</code>")
            continue
        seg = esc(seg)
        seg = re.sub(r"\*\*(.+?)\*\*", r"<strong>\1</strong>", seg)
        seg = re.sub(r"(?<![\w*])\*(?!\s)(.+?)(?<!\s)\*(?![\w*])", r"<em>\1</em>", seg)
        out.append(seg)
    return "".join(out)


def render_html(d, template):
    p, stages = d["page"], d["stages"]
    col = {s["id"]: i + 1 for i, s in enumerate(stages)}
    rows = []
    for e in d["entries"]:
        rows.append(f'      <div class="st-entry" style="grid-column:{col[e["feeds"]]}"><b>{esc(e["kind"])}</b>{esc(e["title"])}<span>{esc(e["detail"])}</span></div>')
    rows += ["", "      " + "".join(f'<div class="st-down" style="grid-column:{col[e["feeds"]]}">↓</div>' for e in d["entries"]), ""]
    for i, s in enumerate(stages):
        last = " last" if i == len(stages) - 1 else ""
        rows.append(f'      <div class="st{last}" style="grid-column:{i + 1}"><i>{i + 1}</i><b>{esc(s["kind"])}</b>{esc(s["question"])}<span>{esc(s["detail"])}</span></div>')
    rows += ["", f'      <div class="st-loop" style="grid-column:1 / span {len(stages)}"><span>↺ MAINTAIN</span> {esc(d["maintain"])}</div>']

    def cards(section):
        return "\n".join(
            f'      <div class="card"><h3>{esc(n["title"])}</h3><ul>\n' + "\n".join(f"        <li>{inline(it)}</li>" for it in n["items"]) + "\n      </ul></div>"
            for n in d["notes"] if n["section"] == section)

    arows = []
    for r in d["artifacts"]:
        pb = inline(r["playbook"]) + (f'<br><span class="note">{inline(r["note"])}</span>' if r.get("note") else "")
        pill = "ok" if r["state"] == "covered" else "gap"
        arows.append(f'        <tr><td>{pb}</td><td>{inline(r["build"])}</td><td>{inline(r["product"])}</td><td><span class="pill {pill}">{r["state"]}</span></td></tr>')

    lanes = [[l["id"], l["name"]] + ([l["sub"]] if l.get("sub") else []) for l in d["lifecycle"]["lanes"]]
    steps = []
    for s in d["lifecycle"]["steps"]:
        row = [s["from"], s["to"], s["text"], 1 if s.get("reply") else 0]
        if s.get("optional"):
            row.append("opt")
        steps.append(row)

    def js(v):
        return json.dumps(v, ensure_ascii=False, separators=(",", ":")).replace("</", "<\\/")

    fill = {
        "TITLE": esc(p["title"]), "EYEBROW": esc(p["eyebrow"]), "LEDE": inline(p["lede"]),
        "STAGES_TITLE": esc(p["stages_title"]), "STAGES_TEXT": inline(p["stages_text"]),
        "ARTIFACTS_TITLE": esc(p["artifacts_title"]), "ARTIFACTS_TEXT": inline(p["artifacts_text"]),
        "LIFECYCLE_TITLE": esc(p["lifecycle_title"]), "LIFECYCLE_TEXT": inline(p["lifecycle_text"]),
        "LIFECYCLE_NOTE": inline(p["lifecycle_note"]),
        "NSTAGES": str(len(stages)), "MINWIDTH": str(max(900, 112 * len(stages))),
        "STAGEMAP": "\n".join(rows), "NOTES_STAGES": cards("stages"), "NOTES_ARTIFACTS": cards("artifacts"),
        "ARTIFACT_ROWS": "\n".join(arows), "LANES": js(lanes), "STEPS": js(steps),
    }
    return re.sub(r"__([A-Z_]+)__", lambda m: fill.get(m.group(1), m.group(0)), template)


# ---------------------------------------------------------------------------------------------------------
# --check
# ---------------------------------------------------------------------------------------------------------
def repo_files():
    out = subprocess.run(["git", "ls-files", "--cached", "--others", "--exclude-standard"], cwd=ROOT,
                         capture_output=True, text=True)
    if out.returncode == 0 and out.stdout.strip():
        return [f for f in out.stdout.split("\n") if f]
    found = []  # not a git checkout: walk the tree
    for base, dirs, files in os.walk(ROOT):
        dirs[:] = [x for x in dirs if x != ".git"]
        found += [os.path.relpath(os.path.join(base, f), ROOT) for f in files]
    return found


def glob_re(pattern):
    """`*` and `?` stay inside one path segment, `**` crosses them; a bare directory matches what is under it."""
    pattern = pattern.rstrip("/")
    out, i = [], 0
    while i < len(pattern):
        c = pattern[i]
        if pattern[i:i + 3] == "**/":
            out.append("(?:.*/)?")
            i += 3
            continue
        if pattern[i:i + 2] == "**":
            out.append(".*")
            i += 2
            continue
        out.append("[^/]*" if c == "*" else "[^/]" if c == "?" else re.escape(c))
        i += 1
    return re.compile("".join(out) + "(?:/.*)?$")


def first_difference(want, have):
    a, b = want.split("\n"), have.split("\n")
    for i in range(max(len(a), len(b))):
        x = a[i] if i < len(a) else "<end of file>"
        y = b[i] if i < len(b) else "<end of file>"
        if x != y:
            return i + 1, x, y
    return None


def build_usage():
    """Build ./cmd/intentgate into a temp dir and return the usage text it prints (no arguments, and --help)."""
    with tempfile.TemporaryDirectory() as d:
        exe = os.path.join(d, "intentgate")
        try:
            b = subprocess.run(["go", "build", "-o", exe, "./cmd/intentgate"], cwd=ROOT, capture_output=True, text=True)
        except FileNotFoundError:
            raise RuntimeError("go is not installed, so ./cmd/intentgate cannot be built")
        if b.returncode != 0:
            raise RuntimeError("go build ./cmd/intentgate failed: " + b.stderr.strip())
        text = ""
        for args in ([], ["--help"]):
            r = subprocess.run([exe, *args], cwd=d, capture_output=True, text=True, stdin=subprocess.DEVNULL)
            text += r.stdout + r.stderr + "\n"
        return text


def usage_forms(usage):
    """One (subcommand, token set) per usage form: `usage: intentgate verify ...` plus its continuation lines."""
    forms, cur = [], None
    for line in usage.split("\n"):
        m = re.match(r"^\s*(?:usage:\s*)?intentgate\s+(\S.*)$", line)
        if m:
            cur = [m.group(1)]
            forms.append(cur)
        elif cur is not None and line.startswith(" ") and line.strip():
            cur.append(line.strip())
        else:
            cur = None
    return [(f[0].split()[0], set(re.split(r"[\s|()\[\]<>]+", " ".join(f)))) for f in forms]


def command_ok(command, forms):
    """`lock --from-plan` is accepted when some usage form starts with `lock` and mentions `--from-plan`."""
    words = command.split()
    return any(first == words[0] and all(w in toks for w in words[1:]) for first, toks in forms)


def check(yml, md, say):
    """Report each problem through say() and return how many there were. RuntimeError: cannot decide."""
    data = load(yml)
    problems = 0
    want = render_markdown(data)
    have = open(md, encoding="utf-8").read() if os.path.exists(md) else None
    if have is None:
        say(f"{shown(md)} is stale: the file is missing; run python3 -I scripts/sdlc-map.py")
        problems += 1
    elif have != want:
        n, x, y = first_difference(want, have)
        say(f"{shown(md)} is stale at line {n}: the YAML renders {x!r} but the file has {y!r}; run python3 -I scripts/sdlc-map.py")
        problems += 1
    files = repo_files()
    for where, item in items_with_refs(data):
        for pat in item.get("paths") or []:
            rx = glob_re(pat)
            if not any(rx.match(f) for f in files):
                say(f"{shown(yml)}: {where}: path {pat!r} matches nothing in the repo (git ls-files plus the working tree)")
                problems += 1
    wanted = [(w, c) for w, it in items_with_refs(data) for c in it.get("commands") or []]
    if wanted:
        forms = usage_forms(build_usage())
        if not forms:
            raise RuntimeError("the intentgate usage text has no `intentgate <command>` lines to check against")
        for where, c in wanted:
            if not command_ok(c, forms):
                say(f"{shown(yml)}: {where}: command {c!r} is not an intentgate command (no usage line `intentgate {c}`)")
                problems += 1
    return problems


def main(argv=None):
    ap = argparse.ArgumentParser(description="Render and check docs/process/sdlc-map.yml")
    ap.add_argument("--check", action="store_true", help="write nothing; exit 1 on drift from the repo")
    ap.add_argument("--yml", default=YML)
    ap.add_argument("--md", default=MD)
    ap.add_argument("--html", default=HTML)
    args = ap.parse_args(argv)
    yml, md, page = (os.path.abspath(x) for x in (args.yml, args.md, args.html))

    def err(text):
        print(text, file=sys.stderr)

    try:
        if args.check:
            n = check(yml, md, err)
            if n:
                err(f"sdlc-map: {n} problem(s)")
                return 1
            print("sdlc-map: the map matches the repo")
            return 0
        data = load(yml)
        outputs = ((md, render_markdown(data)), (page, render_html(data, open(TMPL, encoding="utf-8").read())))
    except (ValueError, OSError) as e:
        err(f"sdlc-map: {e}")
        return 1
    except RuntimeError as e:
        err(f"sdlc-map: cannot decide: {e}")
        return 3
    for path, text in outputs:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        print(f"wrote {shown(path)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
