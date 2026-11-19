# S-15: process map in the repo, kept true by the gate; artifact tidy-up

**Base commit:** the commit that adds this spec   **Branch:** feature/s-15-process-map
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The SDLC map (stages, artifact map, lifecycle sequence) lives only as a private web page today. Move it into the repo as data, render it for GitHub and for a local page, and make `scripts/check.sh` fail when it drifts from the repo. Also clear up the overlaps the map exposed.

The current page is the reference for content and look: `scripts/sdlc-map.tmpl.html` starts from it (the orchestrator puts a copy at `docs/process/reference/sdlc-map-v2.html` in the spec commit).

## Criteria

No requirement changes. This is process tooling and documentation; no claim moves.

## Choices fixed here

- **Source of truth:** `docs/process/sdlc-map.yml`, hand-edited, holding:
  - `entries`: the start-anywhere boxes (`kind` PLAN/BUILD/TEST/DEPLOY, `title`, `detail`, `feeds`: a stage id);
  - `stages`: the 8 pipeline stages in order (`id`, `kind`, `title`, `question`, `detail`);
  - `maintain`: the loop line;
  - `artifacts`: rows of the artifact map (`playbook`, `build`, `product`, `state`: covered | partial | gap | overlap);
  - `lifecycle`: `lanes` (id, name, sub) and `steps` (`from`, `to`, `text`, optional `reply: true`, `optional: true`).
  - Any item may carry `paths:` (repo-relative paths or globs) and `commands:` (`intentgate` subcommands, e.g. `lock --from-plan`, `verify`).
  Content: exactly what the reference page shows.
- **Renderer:** `scripts/sdlc-map.py` (Python 3, standard library only, plus a small YAML subset parser written for this file; run as `python3 -I`):
  - writes `docs/process/sdlc-map.md`: a header saying it is generated from the YAML and must not be edited, then a mermaid `flowchart LR` for the stages (entries in a subgraph above, pipeline in one row, no HTML in labels; use `\n`-free short labels so GitHub renders them), the artifact table in Markdown, and a mermaid `sequenceDiagram` for the lifecycle (participant aliases without `~`, `/` or parentheses in ids);
  - writes `req-views/sdlc-map.html` (git-ignored): the reference page's design, filled from the YAML by `scripts/sdlc-map.tmpl.html` (hand-drawn stage grid and SVG sequence, no mermaid);
  - `--check`: regenerates the Markdown in memory and fails (exit 1) when `docs/process/sdlc-map.md` differs, naming the first differing line; fails when any `paths:` entry matches nothing in `git ls-files` plus the working tree; fails when any `commands:` entry isn't a subcommand `intentgate` accepts (build `./cmd/intentgate` into a temp dir and check each with `--help` or the usage text; a usage error for an unknown command counts as missing). Exit 0 only when all three hold.
- `scripts/check.sh`: add `step "process map" python3 -I scripts/sdlc-map.py --check` (after the Go steps). CI already has Python 3 on `ubuntu-latest`.
- `AGENTS.md`, "How work flows": one line saying a slice that adds, renames or removes a stage artifact, a path or a command named in `docs/process/sdlc-map.yml` updates the YAML in the same slice, and the gate enforces it.
- **Tidy-up:**
  - `docs/requirements.md`: a header block saying it is the frozen adoption source, superseded by `claims/`, kept for history; nothing else in the file changes.
  - `docs/glossary.md`: rows for intent (project proposal / root intent / playbook `intent.md`), claim, design element, ADR, slice spec, plan, intent lock, Baseline, each saying where it lives and which stage writes it.
  - `docs/specs/README.md`: the template gains a closing ` ```intentgate ` block (criteria: one per claim id with its one-line text; footprint: the allowed paths), with a sentence saying a human may lock the slice with `intentgate lock --from-plan docs/specs/S-NN-….md` so the PR check enforces the footprint. Add that block to S-15 itself as the first example.

## Tests

- `scripts/sdlc_map_test.py` (stdlib `unittest`, run by `check.sh` in the same step or before it): YAML subset parsing of the real file; `--check` passes on the committed tree; a temp copy with one stale Markdown line, one missing path and one unknown command each fails with the right message.

## Verify

```bash
scripts/check.sh                                  # GREEN, with the new "process map" step
python3 -I scripts/sdlc-map.py && git status --short docs/process   # no diff: committed Markdown is current
```

## Footprint (allowed paths)

- `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`
- `scripts/sdlc-map.py`, `scripts/sdlc-map.tmpl.html`, `scripts/sdlc_map_test.py`, `scripts/check.sh` (one step only)
- `AGENTS.md` (one line), `docs/requirements.md` (header only), `docs/glossary.md`, `docs/specs/README.md`, `docs/specs/S-15-process-map.md` (the closing block only)

## Forbidden

Everything else, including `claims/**`, `.req/**`, `baselines/**`, `decisions/**`, Go code. If a forbidden file must change, stop and report.

```intentgate
criteria:
  - text: "The process map is generated from docs/process/sdlc-map.yml and scripts/check.sh fails when it drifts from the repo"
    footprint: ["docs/process/**", "scripts/sdlc-map.py", "scripts/sdlc-map.tmpl.html", "scripts/sdlc_map_test.py", "scripts/check.sh"]
  - text: "The artifact overlaps are resolved in the docs (requirements.md archived, glossary rows, spec template block)"
    footprint: ["AGENTS.md", "docs/requirements.md", "docs/glossary.md", "docs/specs/README.md", "docs/specs/S-15-process-map.md"]
```
