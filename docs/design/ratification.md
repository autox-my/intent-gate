# Design: ratification (creating a lock)

<!-- req-satisfies: REQ-0002 -->
<!-- req-satisfies: REQ-0003 -->
<!-- req-satisfies: REQ-0004 -->
<!-- req-satisfies: REQ-0005 -->
<!-- req-satisfies: REQ-0012 -->
<!-- req-satisfies: REQ-0015 -->

A lock is created in exactly one of three ways. All three go through the same pipeline in `internal/ratify`, and all three record which way it happened (`ratification.method`, ADR 0003).

## 1. The shared pipeline

1. **Collect** the root intent and the plan.
2. **Secret-scan and mask** the root intent (§5).
3. **Draft criteria and footprints** from the plan, capped at 8. If the plan has more, stop and ask for the plan to be split rather than truncating.
4. **Run the plan review** (plan-review.md). This is advisory and never blocks.
5. **Show** the criteria, footprints and plan-review findings to the human.
6. **Confirm**: the human approves, edits or rejects.
7. **Write** `.intent/<id>.yml` and the local root intent record (intent-lock.md §1).

Nothing is written before step 6. A rejection leaves no files.

## 2. Plan-approval hook (REQ-0002), method `hook`

Phase 0 ships one hook, for Claude Code.

| Hook event | Matcher | What it does |
|---|---|---|
| `PreToolUse` | `ExitPlanMode` | Steps 1–5: reads the plan from the tool input and the human turns from the session transcript, runs the plan review, and returns the findings as a message the human sees beside the approval prompt. Records the start time. |
| `PostToolUse` | `ExitPlanMode` | Steps 6–7: only runs if the human approved. Writes the lock. |

The human's click on Claude Code's own approval prompt **is** the confirmation. The agent can't click it, which is why this path counts as human-ratified without any extra step (v2 review §0).

**To verify before implementation:** the exact hook input fields (plan text, transcript path), whether `PreToolUse` output is displayed to the human before the approval prompt, and what happens on rejection. If `PreToolUse` can't show findings in time, the fallback is to run the plan review in `PostToolUse` and append the findings to the lock as `plan_review:` for the PR reviewer. That's weaker, because it comes after approval, but it still meets REQ-0016–0020.

Hooks for Codex, Cursor, Kiro and Spec Kit are Phase 2.

## 3. CLI paths, method `cli-confirm` or `issue`

| Command | Root intent | Plan | Method |
|---|---|---|---|
| `intentgate lock --issue 123` (REQ-0003) | the issue body and the issue author's comments | the same text: for an issue, prompt and plan are the same document | `issue` |
| `intentgate lock --from-plan plan.md` (REQ-0004) | `--prompt <file>`, or the human pastes it at the prompt | `plan.md` | `cli-confirm` |

**Interactive confirmation (REQ-0005):** both commands require a terminal. If stdin or stdout isn't a TTY, the command exits 2 with "intentgate lock needs an interactive terminal; ratification is a human act". The confirmation shows the criteria and footprints and asks the human to type `yes`. There is no `--yes` flag.

This stops a helpful agent from creating locks by accident. It doesn't stop a determined one (an agent can allocate a pseudo-terminal), and Phase 0 doesn't claim it does (ADR 0003).

## 4. Review duration (REQ-0012)

`review_duration_ms` is the time from when the criteria were first shown (step 5) to the confirmation (step 6):

- **Hook:** `PostToolUse` time minus `PreToolUse` time.
- **CLI:** time from printing the criteria to reading `yes`.

It's recorded, not judged. A very short review is a signal for the audit trail, not a reason to refuse the lock.

## 5. Secret scan (REQ-0015)

Before the root intent is written anywhere or sent to the plan-review model:

- Scan it with **gitleaks' detection library** (Go, importable; one of ADR 0001's reasons).
- Replace each finding with `[REDACTED:<rule-id>]`, and record the count in the lock as `root_intent.redactions: <n>` (counted in the hash).
- If the scanner itself fails, stop with exit 3. Don't write an unscanned root intent.

The masked text is what gets stored and hashed, so the hash in the committed lock refers to the redacted version.
