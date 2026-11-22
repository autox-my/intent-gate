# IntentGate: agent instructions

## Project

IntentGate locks the intent behind agent-written changes and checks pull requests against it.
Design history lives in `docs/` (proposal v1, v2 and both external reviews). Phase 0 is advisory
only: nothing may block a merge. Vocabulary is in `docs/glossary.md`: in this repo, "claim",
"scope", "gate", "pin" and "diff" always mean req's terms; IntentGate's own are criterion,
footprint, PR check and plan review.

## Requirements are managed with req

All requirements live in `claims/`, one obligation per file, managed with
[req](https://github.com/autox-my/req). The corpus is the source of truth.
`docs/requirements.md` was the adoption source; do not edit it to change a requirement.

- Add a requirement: `req next-id`, write `claims/REQ-NNNN.yml` (copy an existing claim's shape),
  then run `req canon` and read the derived parse.
- Change a requirement: edit its claim's `statement`, run `req canon`. Changing meaning moves its
  address; that is intended.
- Remove a requirement: delete its claim file, deliberately. Never empty `claims/`.
- On a pull request: `req diff --base main --fail-on weakened,removed` shows which requirements
  moved and in which direction. A human ratifies every direction change.
- Every code change should name the claim(s) it implements (for example `Implements: REQ-0024`
  in the commit message).

### req rules (from req's `examples/agents-snippet.md`)

- Use the `req` on `PATH` only after `req --version` shows `claim_schema req.claim/4` and `rules_v 9`. Anything else is an older or newer build: stop and report it; do not work around it.
- To find a command, run `req --help`; `req <command> --help` is that command's contract, with its `exit:` line. Act on the exit code and the named line, not on prose.
- The claim is the unit: one obligation per file under `claims/`, one file per claim.
- A claim is authored: its parse, canonical form and address are derived from `statement` at load and stored nowhere. Never add `address`, `canonical` or a parse slot to a claim file.
- `pins:` states what the parse must be where it is load-bearing; a pin agrees or is a flag. Never edit a pin to match a parse the statement was not meant to have: re-word the statement, or leave the flag for a human.
- `req canon` shows the derived parse and each pin's verdict and does not fail on a flag: it is diagnostics, never a pass.
- Until every claim has a linked test, the gate cannot be green; use `req canon --fail-on-flag` meanwhile and move to `req check --test-report <junit.xml>` when the last claim is linked.
- A committed projection is checked, never edited: `req render --check docs/spec.md`, regenerated with `req render --out docs/spec.md`.
- An exit 3 is not a pass: report what could not be decided and ask a human, and never relax a check to land a change.
- Never auto-merge, and never sign a Baseline: ratification is the human's act.

## How work flows (SDLC)

Adapted from req's own process (`docs/process/req-sdlc-study.md`).

### The artifacts, and which one answers which question

| Artifact | Answers | Lives in | Changes how |
|---|---|---|---|
| Requirement (req claim) | *What* must hold | `claims/REQ-NNNN.yml` | Edit the claim; `req diff` shows the direction; a human ratifies |
| ADR | *Why* a hard-to-reverse choice was made | `docs/adr/NNNN-*.md` | New ADR supersedes old; merged before the code that depends on it |
| Design element | *How* components meet the claims | `docs/design/*.md` (satisfaction links) | Edit; `req satisfy` flags links whose claim moved |
| **Spec** | *This slice of work*: which claims, which paths, what "done" means | `docs/specs/S-NN-*.md` | Written by the orchestrator, committed **before** a worker starts |
| Test | *Proof* a claim holds | `*_test.go`, linked from claims via `tests:` | Written first, committed red |
| Code | the implementation | `cmd/`, `internal/` | Only after its test is red |

Specs of merged slices are history, not current documentation: for what holds now, read `claims/`, `docs/adr/` (Accepted ADRs bind; Proposed ones don't) and `docs/design/`. A spec adds no new requirements and restates none. It points at claims and design sections and adds only what a worker needs for one slice: the allowed paths, choices fixed for the slice, the red-first tests and the verify command. **A spec is this project's intent lock, written by hand:** its criteria are claim ids, its footprint is the allowed paths. Once IntentGate's footprint check exists, it runs on our own branches against the spec.

### Roles

Follow the setup that matches the active harness:

- **Antigravity (native Orchestrator):** planner and executor share one conversation (`antigravity` / `gemini-pro-orchestrator-neon`: Gemini 4 Neon plans and reviews handbacks; Gemini 4 Hydrogen executes inside the slice's worktree). Read-only review uses an available Antigravity model — prefer a different family from Hydrogen (`antigravity` / `claude-sonnet-5-5` or `claude-opus-5-5`), or Neon reviewing Hydrogen's work directly.
- **Non-Antigravity (previous setup):** separate orchestrator, delegated worktree worker, and cross-family reviewer.

| Role | Who | Antigravity (native) | Non-Antigravity |
|---|---|---|---|
| Orchestrator | writes specs, plans, checks branches, keeps the summary | `antigravity` / `gemini-pro-orchestrator-neon` (Neon planner) | Claude Opus 5.5 |
| Worker / Executor | writes tests (red first) then code in the slice's worktree | Hydrogen executor (same conversation, in slice worktree) | `claudeAgent` / `claude-sonnet-5-5` (dedicated worktree) |
| Reviewer | read-only review before PR | `antigravity` / `claude-sonnet-5-5` (or `claude-opus-5-5`, or Neon reviewing Hydrogen) | `opencode` / `opencode-go/gemini-4-argon` (fallback: `opencode-go/deepseek-v4.1-flash`) |
| Human | ratifies claims and links, reviews, merges | — | — |

### The flow for one slice

1. Orchestrator commits the spec (`docs(specs): S-NN …`).
   The orchestrator then presents the spec as a plan: approving it in plan mode writes the intent lock (the hook installed by `intentgate init`). Where the harness captures plan approval (T3 Code), the orchestrator writes the spec, runs `intentgate hook claude-code ratify-question docs/specs/S-NN-*.md` and asks that exact card; the human's `Ratify` writes the lock. Where the hook can't run, `scripts/lock-slice.sh <S-NN>` does the same.
2. Worker, on branch `feature/s-NN-<slug>` in its own worktree:
   1. `test(<pkg>): … (red at the base)`: the tests, plus only the stubs needed to compile. They must fail on **assertions**, not compile errors. Quote the failure in the report.
   2. `feat(<pkg>): …`: the code that turns them green, plus `tests:` links on the spec's claims.
   3. `scripts/check.sh` must end `verdict: GREEN`.
3. Orchestrator checks `git diff <base>...<branch> --name-only` against the spec's allowed paths. A path outside them rejects the branch.
4. Reviewer reviews the branch read-only; fixes are commits named `fix(<pkg>): … (review N of S-NN)`.
5. Human merges. Agents never commit to `main` and never change the git identity.

A slice that adds, renames or removes a stage artifact, a path or a command named in `docs/process/sdlc-map.yml` updates that YAML in the same slice; `scripts/check.sh` fails when the map and the repo disagree (`python3 -I scripts/sdlc-map.py` re-renders `docs/process/sdlc-map.md`).

### Rules for every worker

- Read the spec and the files it points to; don't restate them.
- Don't enter plan mode or call `ExitPlanMode`: the slice was ratified before you started, and a plan approval in your session would write a second lock.
- Touch only the spec's allowed paths. If a forbidden file must change, stop and report.
- Never weaken a test, a check or a claim to get green. An exit 3 is not a pass.
- Report back in at most 10 lines: branch, base commit, commits, files changed, red evidence, `check.sh` verdict, open questions.

<!-- intentgate:begin -->
## IntentGate

End every plan with exactly one fenced `intentgate` block. Each criterion is a checkable outcome, and its footprint lists the repo-relative globs it may touch. Use this shape exactly (2-space indents, `footprint` inside each criterion):

```intentgate
criteria:
  - text: "<a checkable outcome>"
    footprint: ["<repo-relative glob>"]
```

Start work in plan mode; approving the plan writes the intent lock.
<!-- intentgate:end -->
