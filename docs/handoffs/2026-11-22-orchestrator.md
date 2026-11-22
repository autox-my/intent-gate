# Handoff: orchestrator thread, 2026-11-22

Replaces the 2026-11-20 handoff. Everything else is in git. The next orchestrator is **Gemini 4 Neon** (Google Antigravity), first through T3 Code, then possibly directly in Antigravity. It does not have the previous orchestrator's private memory, so the working rules are written out in full below.

## Where things stand

- **Repo:** `github.com/autox-my/intent-gate` is **public** since 2026-11-20. `main` is at the S-25 merge (1ff142c), clean, with no open PRs, extra branches or worktrees.
- **CI:** `scripts/check.sh` must end `verdict: GREEN`. Its steps are gofmt, go vet, staticcheck, tests, process map, `adr status`, `req canon`, `req satisfy`, `req check` and `req baseline`. It verifies Baseline `baselines/phase0-5.json`.
- **Requirements:** REQ-0002 to REQ-0047 (46 claims), every one with linked tests, all in signed Baseline `phase0-5`.
- **ADRs:** 0001 to 0006, all **Accepted** (2026-11-21). The status rules are in `docs/adr/README.md`. Only Accepted ADRs bind, and only the human accepts.
- **Releases:** `v0.1.0` and `v0.2.0`. The Action pins **v0.2.0** in `action/release.txt` and downloads and checks it. v0.2.0 contains S-19 to S-24. **S-25 is not in a release yet.**

### Slices since the last handoff

| Slice | What | Claims |
|---|---|---|
| S-19 | `stats`: merged PRs, PRs that brought a lock, lock coverage | — |
| S-20 | `cmd/intentgate` tests ignore the shell's Jev variables | — |
| S-21 | Release workflow (a `vX.Y.Z` tag builds 4 binaries and `SHA256SUMS`); the Action downloads the pinned binary and checks it (`action/fetch.sh`, `action/release.txt`) | — |
| S-22 | A plan may name `intent: intents/<slug>.md`; the lock records `source: committed-intent`, path, blob, sha256 (ADR 0006) | REQ-0043, 0044 |
| S-23 | `intentgate acceptance`: the PR check reports whether the committed intent was approved by a CODEOWNERS owner (accepted, not-accepted or unknown; report only) | REQ-0045 |
| S-24 | `internal/arch` test: packages marked PURE in `docs/design/README.md` import no I/O, network, randomness or adapter package, and read no clock | — |
| S-25 | A footprint glob that matches every path (`**`, …): warning when the lock is written, suspect `repo-wide-footprint` in the PR check | REQ-0046, 0047 |

Also on `main`, outside slices:
- the ADR status rules and `scripts/adr_status.py`;
- "merged specs are history" in `docs/specs/README.md` and AGENTS.md;
- a refreshed `docs/design/README.md` layout.

These came from reviewing the 2026-11-21 ADR-vs-codebase research. The human accepted gaps 1, 2, 3 and 5 and rejected gap 4 as a bulk change: new claims name their ADR in `rationale.source`, but the old claims are not backfilled. The research file itself was deleted at the human's request, because its package names and claim ids were wrong.

## How a slice runs (human cost: one click or command, one signature when claims change, one merge)

1. **Spec:** the orchestrator writes `docs/specs/S-NN-*.md`, ending with one `intentgate` block, and pushes it to `main` directly. Specs, their "Review N amendments", design sections and ADRs always go to `main`, never to the slice branch, because they are outside the slice's footprint.
2. **Lock:** see "Locking from a non-Claude harness" below. Commit `.intent/<id>.yml` locally on top of `main` and don't push `main`. New claims the human has ratified are committed the same way, right after the lock, with `tests: []`.
3. **Worker:** one per slice, in its own worktree, on `feature/s-NN-<slug>`. It commits the tests failing on assertions first, then the code, then the `tests:` links. It touches only the spec's allowed paths and pushes the branch, but opens no PR.
4. **Path check:** `git diff origin/main...<branch> --name-only` must stay inside the allowed paths, plus the lock.
5. **Review:** read-only, by a different model family from the worker. Valid findings become a binding "Review N amendments" bullet in the spec on `main`, then a fix commit `fix(<pkg>): … (review N of S-NN)` on the rebased branch. Small fixes the orchestrator may make itself.
   - Past reviews returned "no findings" too easily. Ask for adversarial checks with concrete cases, and spot-check the risky part yourself.
6. **PR:** the orchestrator opens it and watches the checks. **The human merges.**
7. **When a slice adds claims:**
   - The branch stays RED, but only on `req satisfy`, `req check` and `req baseline`, and only about the new claims.
   - The human runs `req satisfy --record <design file> REQ-NNNN` for each claim, then `scripts/sign-baseline.sh phase0-N`, on the branch.
   - The orchestrator then points `scripts/check.sh`, `docs/process/sdlc-map.yml` (re-rendered with `python3 -I scripts/sdlc-map.py`) and `docs/glossary.md` at `phase0-N`. That must end GREEN.
   - The next Baseline is `phase0-6`. A signed manifest is never overwritten.
8. **After merge:** remove the worktree and delete the branches, local and remote. Disk is tight on this Mac.

**Release:** tag only after `main`'s CI is green. Then download the release, run `shasum -a 256 -c SHA256SUMS`, run `action/fetch.sh` against the real URL, and pin the two Linux lines in `action/release.txt` through a reviewed PR. The human asked the orchestrator to push the first two tags; ask again for the next one.

## Locking from a non-Claude harness (read this first)

IntentGate's plan-approval hook and ratify card are **Claude Code specific**:
- **What's installed:** `.claude/settings.json` has `PreToolUse` and `PostToolUse` on `ExitPlanMode` and `AskUserQuestion`, running `intentgate hook claude-code pre|post`.
- **What they parse:** Claude Code's hook JSON (`tool_name`, `tool_input`, `tool_response`, the transcript).
- **Consequence:** in a session driven by Gemini or Antigravity, **those hooks will not fire**. Asking the ratify card there will **not** write a lock.

What Antigravity's docs say (read 2026-11-22; check them again, they are new):
- **Instruction files:** it reads `AGENTS.md` (or `GEMINI.md`, or `.agents/rules/`) from the workspace folder up to the repo root. Each file is cut off past 24 KB; AGENTS.md is about 7.6 KB.
- **Hooks:** `.agents/hooks.json` (and `~/.gemini/config/hooks.json`), with the events `SessionStart`, `PreToolUse`, `PostToolUse`, `PreInvocation`, `PostInvocation` and `Stop`.
  - The stdin payload is camelCase: `conversationId`, `workspacePaths`, `transcriptPath`, `toolCall {name, args}`.
  - A `PreToolUse` hook can answer `allow`, `ask` or `deny`. `PostToolUse` returns `{}`.
- **Questions:** it has an `ask_question` tool (multiple choice: `questions[].question`, `options`, `is_multi_select`).
- **Plans:** approval of an implementation plan ("Proceed") is **not** a documented hook event or tool call, and no payload field carries the plan or the user's decision.
- **`PostToolUse` carries no tool result:** its fields are `toolCall {name, args}`, `stepIdx`, `error` and the common fields. The option the user picked in `ask_question` is therefore not in the hook payload. It may be in the transcript at `transcriptPath`, which is undocumented.
- **In T3 Code with an Antigravity thread,** Claude Code's hooks don't run, and nothing in the repo is in `.agents/hooks.json`. A ratify card shown there writes **no lock**. (T3's `antigravity` provider also showed as disabled on 2026-11-21; it must be enabled in T3's settings.)

So, in order of preference:
1. **Fallback that works everywhere:** the human runs `scripts/lock-slice.sh S-NN` in a terminal and types `yes`. The orchestrator then commits the staged lock. The lock's `method` is `cli-confirm`, not `hook`; that's expected.
2. **Through T3 Code with a Claude worker:** `delegate_task` to `claudeAgent` / `claude-sonnet-5-5` still works from any orchestrator thread. The worker runs Claude Code, but the lock was already written before it starts.
3. **An Antigravity adapter is a candidate slice**, and the human wants to see how IntentGate behaves in this harness:
   - `intentgate hook antigravity pre|post` would parse Antigravity's payload and match the `ask_question` card the same way `ratify-question` does today.
   - `intentgate init --agent antigravity` would write `.agents/hooks.json`.
   - **Open question:** whether the transcript at `transcriptPath` records the user's selected option after `ask_question`. The `PostToolUse` payload doesn't. Check it by hand first: a `PostToolUse` hook on `ask_question` that copies its stdin and the transcript to a temp file. This is the same check S-18 did for Claude Code.
   - **Later, beyond Claude Code:** `PreToolUse` can answer `ask` or `deny` before `write_to_file` or `run_command`. That could warn *before* an edit outside the lock's footprint, not only at PR time. That is a Phase 1 design question, and must stay advisory (`ask`, never `deny`) while Phase 0 rules hold.

## Roles: Antigravity's Orchestrator mode (the human's choice, 2026-11-22)

The human runs Antigravity's built-in **Orchestrator**: **Gemini 4 Neon as planner**, **Gemini 4 Hydrogen as executor**. Per the docs, the Orchestrator "keeps a single agent in your conversation and hands off between planning and execution within each turn". The planner explores and writes the plan; the executor "searches, edits files, runs builds and tests".

| Role | Who |
|---|---|
| Orchestrator (planner) | Gemini 4 Neon |
| Worker (executor) | Gemini 4 Hydrogen, in the same conversation; not a separate T3 `delegate_task` child |
| Reviewer | must be a **different family from the worker**, so **not Gemini**: `opencode` / `opencode-go/deepseek-v4.1-flash` (or GLM, or a Claude model). Gemini 4 Argon no longer qualifies while Hydrogen is the worker. |
| Human | ratifies claims, locks, signs, merges |

Update the AGENTS.md roles table in the first slice if this setup is kept.

What this changes in the flow:
- **Worktree per slice:** the executor works where the conversation works. Create the slice's worktree and branch yourself (`git worktree add … -b feature/s-NN-<slug> <base>`) and run the executor there, or the slice's commits land in the main checkout. Remove the worktree after merge.
- **Red first is prompt discipline only:** nothing in the harness enforces "tests fail on assertions before code". Check the red commit yourself before accepting the branch.
- **Lock before the executor starts:**
  - Set Settings > General > **Plan Review Policy = "Request review"** ("Always Ask"). The planner then pauses on its plan and notifies the human.
  - At that pause, the human runs `scripts/lock-slice.sh S-NN` and types `yes`. Commit the lock, then click **Proceed**.
  - With "Always Proceed", the executor starts at once and the lock would come after the code.

## Working rules (were in the previous orchestrator's private memory)

- **Decide worker and reviewer questions yourself,** from the spec, the design and the claims. Escalate only claim wording, Baseline signing, ADR acceptance, merges, tags and real ambiguity. The human's attention is scarce, so state decisions with a one-line reason instead of asking.
- **Never** auto-merge, sign a Baseline, record a satisfaction link (`req satisfy --record` is the human's act), set an ADR to Accepted, or push a tag without asking.
- **Claims are ratified by the human** before they enter a slice: show the wording and the `req canon` result, and ask.
- **Don't leave drafts untracked in `claims/`** when switching branches: they follow you onto `main` and turn the check red. Keep drafts outside the repo until the slice base commit.
- **Worktree and branch:** a worker's branch stays checked out in its worktree. Remove that worktree before switching the main checkout to the branch, for example when the human signs a Baseline.
- **One live worktree at a time.** Clean up right after every merge.
- **Keep summaries short.**

## Waiting on the human

Nothing. Every Baseline is signed and every PR is merged.

## Open items

1. **Locking from Antigravity:** try the lock paths above. If the human wants it, investigate, then build an Antigravity hook adapter (claims, design section in ratification.md, slice).
2. **v0.3.0 release** so the Action carries S-25's repo-wide warning.
3. **Phase 1 tuning:** findings per PR (from the Action's `audit.json` artifacts), the Jev thresholds, merging duplicate findings of different kinds.
4. **Upstream req issues:** autox-my/req#116, #117 and #118.
5. **The Jev key isn't in T3's environment,** so the plan review shows "unavailable" (every recent lock has `plan_review: unavailable`). Export `TYPESAFE_API_KEY` where T3 starts if the review should run.
6. **Default-mode nudge** (called "REQ-0043" in older handoffs; that id now belongs to the committed-intent claim): still not needed.
