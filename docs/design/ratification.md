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

Nothing is written to the repo or the root intent store before step 6. A rejection leaves no lock and no root intent record (the hook's pending record is temporary, §2).

### 1.1 Drafting criteria (step 3)

The plan review is Phase 0's only model call (plan-review.md), so drafting is deterministic: the plan declares its criteria in one fenced block, and IntentGate parses it.

````markdown
```intentgate
criteria:
  - text: "Lock validation rejects more than 8 criteria"
    footprint: ["internal/lock/**"]
  - text: "Footprint findings carry an amend command"
    footprint: ["internal/footprint/**", "!internal/footprint/testdata/**"]
```
````

- Exactly one ` ```intentgate ` block; none, or more than one, is a drafting error. YAML inside, unknown keys rejected; each criterion has `text` and `footprint` only. Ids `C1`, `C2`, … are assigned in order.
- More than 8 criteria is a drafting error asking for the plan to be split (REQ-0007); it is never truncated.
- A drafting error writes nothing. The hook reports it as a `systemMessage` ("no lock: …"); the CLI exits 1 with the same message.
- Agents learn the format from a snippet in the repo's agent instructions (`intentgate init` prints it; later slice).


## 2. Plan-approval hook (REQ-0002), method `hook`

Phase 0 ships one hook, for Claude Code.

| Hook event | Matcher | What it does |
|---|---|---|
| `PreToolUse` | `ExitPlanMode` | Steps 1–5: reads the plan from `tool_input.plan` and the human turns from `transcript_path`, masks secrets, drafts the criteria (§1.1), runs the plan review, and shows the criteria and findings to the human as `systemMessage`. Saves a pending record keyed by `session_id` + `tool_use_id` with its finish time. Never denies: on any error it fails open with a `systemMessage`. |
| `PostToolUse` | `ExitPlanMode` | Steps 6–7: fires only when the human approved. Reads the approved plan from `tool_response.plan` (it wins over the pending copy if they differ, and is re-drafted), writes the lock and the root intent record, and deletes the pending record. |

The human's click on Claude Code's own approval prompt **is** the confirmation. The agent can't click it, which is why this path counts as human-ratified without any extra step (v2 review §0).

What `method: hook` attests is that Claude Code's hook runner called `post` after a `pre` for the same tool call; `post` refuses to write a lock without that `pre` record. It does not prove a click: any local process can run both commands with forged input. That is ADR 0003's position (ratification is recorded, not enforced in Phase 0), and the PR check must not present `method: hook` as stronger evidence than that.

**Root intent scope:** the root intent is the human turns **since the previous approved plan** in the same session, not the whole session. A session that approves several plans (an orchestrator thread working slice after slice) gives each lock only what the human asked for that slice. An `ExitPlanMode` that was rejected doesn't reset the scope, so the feedback that led to the revised plan stays in. With no earlier approved plan, it is every human turn, as before.

**Verified 2026-11-18** (Claude Code hooks reference, code.claude.com/docs/en/hooks, and an experiment on Claude Code 2.1.327):

- Input: `tool_input.plan` and `tool_input.planFilePath` in `PreToolUse`; `tool_response.plan` and `tool_response.filePath` in `PostToolUse`; both carry `session_id`, `transcript_path`, `cwd`, `tool_use_id`.
- Rejection: a denied `ExitPlanMode` fires `PreToolUse` but neither `PostToolUse` nor `PostToolUseFailure`. So `PostToolUse` firing means approval, and a rejection leaves only a pending record. Pending records older than 24 hours are deleted on the next hook run, so rejected plans leave nothing behind for long.
- Display, checked by hand on 2026-11-19 (Claude Code 2.1.328): a `PreToolUse` `systemMessage` is shown in the transcript only **after** the human answers the plan-approval prompt, never beside it; a `PreToolUse` `permissionDecision: "ask"` reason is not shown at all for `ExitPlanMode` (no extra prompt appears). A `PostToolUse` `systemMessage` is shown right after approval. So the plan-review fallback below is the Phase 0 behaviour: the findings are recorded in the lock and shown right after approval, not before it. (Denying once to surface findings was ruled out: it would delay ratification, against REQ-0020.)
- Timeouts: command hooks default to 600 s, and a timed-out `PreToolUse` doesn't block the tool call. The `PreToolUse` hook is installed with a 60 s timeout, and the plan review inside it has its own shorter one (plan-review.md).

**Fallback for the plan review only:** if `PreToolUse` can't show findings in time, run the plan review in `PostToolUse` and record its status and counts in the lock's `plan_review:` (plan-review.md §4), showing the findings in the `PostToolUse` message. That's weaker, because it comes after approval, but it still meets REQ-0016–0020.

### 2.1 Ratify card (harnesses without plan approval)

Some harnesses take plan approval over themselves (T3 Code captures `ExitPlanMode` and, since November 2026, has no plan mode), so `PostToolUse` on `ExitPlanMode` never fires there (checked 2026-11-20). Their question card still completes normally: the human's selection comes back as the tool result of `AskUserQuestion`. The agent can write the question but not the answer, which only the harness produces from the human's click, the same evidence as a plan-approval click. So the same hook also ratifies through a card:

- The agent writes the plan to a file and asks one `AskUserQuestion` with header `IntentGate`, the question `Ratify the plan in <path> (sha256 <hex>)?` and the options `Ratify` and `Not yet`. `intentgate hook claude-code ratify-question <path>` prints that question exactly.
- `PreToolUse` on that question drafts the criteria and runs the plan review, like `pre` for a plan; `PostToolUse` writes the lock only when the answer is `Ratify` and the file still hashes to `<hex>` (the agent can't swap the plan after asking). Any other question, header or answer writes nothing.
- The lock records `method: hook`, source `claude-code-session`, and the root intent scoped as below, where a ratified card counts as an approved plan.

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

Before the root intent is written anywhere or sent to the plan-review model (and, the same way, the plan text before criteria are drafted from it, since criterion text is committed in the lock):

- Scan it with **gitleaks' detection library** (Go, importable; one of ADR 0001's reasons).
- Replace each finding with `[REDACTED:<rule-id>]`, and record the count in the lock as `root_intent.redactions: <n>` (counted in the hash).
- If the scanner itself fails, stop with exit 3. Don't write an unscanned root intent.

The masked text is what gets stored and hashed, so the hash in the committed lock refers to the redacted version.
