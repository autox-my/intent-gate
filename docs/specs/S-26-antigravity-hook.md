# S-26: Antigravity hook adapter and init support

**Base commit:** the commit that adds this spec   **Branch:** feature/s-26-antigravity-hook
**Orchestrator / Executor:** antigravity / gemini-pro-orchestrator-neon (Neon planner, Hydrogen executor)   **Reviewer:** Neon review of Hydrogen, or antigravity / gemini-4-argon

Adds native `method: hook` intent locking for Google Antigravity (CLI, App, IDE, and T3 Code Antigravity ACP) via `.agents/hooks.json` (`PreToolUse` on `ask_question` + `PostInvocation`). Design: `docs/design/ratification.md` §2.2 and `docs/design/intent-lock.md` §2 (on `main`).

## Criteria

No new claims and no Baseline changes: REQ-0002 ("The plan-approval hook shall create an intent lock when a human approves an agent plan.") is what the Antigravity hook implements; add its end-to-end test to `claims/REQ-0002.yml` `tests:`.

## Read first

- `docs/design/ratification.md` §1, §2.1, **§2.2**
- `docs/design/intent-lock.md` §2, §6
- `docs/design/README.md` §2 (`internal/arch/arch_test.go` PURE package guard)
- `internal/hooks/claudecode/claudecode.go`, `cmd/intentgate/hook.go`, `cmd/intentgate/ratifycard.go`, `cmd/intentgate/init.go`, `cmd/intentgate/planreview.go`
- `internal/lock/lock.go`, `schema/intentgate.lock.v1.json`, `docs/process/sdlc-map.yml`

## Choices fixed here

- **Lock source (`internal/lock`, `schema/intentgate.lock.v1.json`):**
  - Add `"antigravity-session"` to `sources` in `internal/lock/lock.go` and `root_intent.source` enum in `schema/intentgate.lock.v1.json`. Same validation rules as `"claude-code-session"` (`path` and `blob` refused). `internal/lock` stays PURE.
- **Pure-style Antigravity parser package (`internal/hooks/antigravity`):**
  - No file I/O, no clock, no network — bytes in, structs/bytes out.
  - `ParseInput(data []byte) (Input, error)`: parses camelCase Antigravity hook JSON (`hookEventName`, `conversationId`, `transcriptPath`, `artifactDirectoryPath`, `cwd`, `modelName`, `stepIdx` as `*int`, `invocationNum` as `*int`, `initialNumSteps` as `*int`, `error`, `toolCall.{id,name,args}`). For `toolCall.name == "ask_question"`, parses `toolCall.args.questions` whether encoded as a JSON array or a JSON string.
  - Ratify card:
    - `RatifyQuestionText(path, sha256hex)` = `"IntentGate: Ratify the plan in " + path + " (sha256 " + sha256hex + ")?"`.
    - `RenderCard(path, sha256hex)` prints `{"question":"IntentGate: Ratify the plan in <path> (sha256 <hex>)?","options":["Ratify","Not yet"],"is_multi_select":false}\n` (no HTML escaping).
    - `ParseCard(qs []Question) (Card, bool)`: accepts ONLY `len(qs) == 1`, `MultiSelect == false`, `Options` strictly `["Ratify", "Not yet"]` in that exact order, and `Question` matching `^IntentGate: Ratify the plan in (.+) \(sha256 ([0-9a-f]{64})\)\?$`.
  - Answer check:
    - `StepAnswer(transcript []byte, stepIdx int) (answer string, found bool, done bool)`: finds the JSONL line with `step_index == stepIdx`; `done` is `status == "DONE"`; extracts the trimmed text after `"A1: "` from `content`.
    - `Ratified(answer string) bool`: true iff `answer == "Ratify" || strings.HasPrefix(answer, "Ratify, ")`.
  - Root-intent scoping:
    - `RootIntentScope(transcript []byte, currentStepIdx int) (Scope, error)`: scans JSONL lines in order (malformed JSON line is an error). Keeps `type == "USER_INPUT"` with `source == "USER_EXPLICIT"` and `step_index < currentStepIdx`, stripping T3 Code wrapper tags (`<user_request>...</user_request>` body if present; otherwise removing `<t3_code_instructions>...</t3_code_instructions>`, `<runtime_info>...</runtime_info>`, `<ADDITIONAL_METADATA>...</ADDITIONAL_METADATA>`, `<USER_SETTINGS_CHANGE>...</USER_SETTINGS_CHANGE>`, `<USER_REQUEST>`, `</USER_REQUEST>`), trimming and skipping empty turns.
    - Tracks `draftedBy = "antigravity/" + model` from the last `PLANNER_RESPONSE` with non-empty `model` at `step_index < currentStepIdx`.
    - Boundaries: earlier `PLANNER_RESPONSE` ratify-card `ask_question` calls (paired with the corresponding subsequent `GENERIC` step before the next `PLANNER_RESPONSE`/`USER_INPUT`). The card paired with `currentStepIdx` (or an unanswered card on `step_index == currentStepIdx - 1`, i.e., the card currently being asked in `PreToolUse`) is the current card and never a prior boundary. A prior card whose paired `GENERIC` step is present and not `DONE`+`Ratified` is rejected (not a boundary); a ratified or truncated prior card is a boundary. Supports `ScopeWhole`, `ScopeSince`, `ScopeReused` and `Describe()` matching `claudecode`.
  - Outputs:
    - `PreOutput`: always renders `{"decision":"allow"}\n`.
    - `PostInvocationOutput`: empty `Message` renders `{}\n`; non-empty renders `{"injectSteps":[{"systemMessage":{"systemMessage":"<msg>","metadata":{"user_markdown":"<msg>"}}}]}\n`.
- **CLI wiring (`cmd/intentgate`):**
  - `intentgate hook antigravity pre`: fail open (always exit 0, always print `{"decision":"allow"}\n`, even on panic or malformed input). Ignores non-`ask_question` tools and non-ratify-card questions (writes no pending record). Validates `hookEventName == "PreToolUse"`, safe `conversationId`, non-negative `stepIdx`, and non-empty `StateDir`. Checks plan path is inside `deps.Git.TopLevel(in.CWD)` or `in.ArtifactDirectoryPath` (when non-empty, absolute, and not filesystem root) **before** touching the filesystem; verifies SHA-256; drafts criteria (masked); runs plan review under `deps.reviewBudget()` (using committed intent when `drafted.Intent != ""`, else `RootIntentScope`); writes atomic 0600 pending record to `<StateDir>/pending/agy-<conversationId>/step-<stepIdx>.json` (pruned automatically after 24 h by `prunePending`). If a recognized ratify card hits an error during `pre`, saves `error` in the pending record so `post-invocation` reports `IntentGate: no lock: <reason>` if ratified.
  - `intentgate hook antigravity post-invocation`: fail open (always exit 0, always print one valid JSON object). Validates `hookEventName == "PostInvocation"` and safe `conversationId`. Processes all `step-*.json` records in `<StateDir>/pending/agy-<conversationId>/` sorted by `step_idx` ascending:
    - If `step_index == rec.StepIdx` is present in `transcriptPath`: remove the pending file; if `!done || !Ratified(answer)` → `IntentGate: no lock (not ratified)`; else if `rec.Error != ""` → `IntentGate: no lock: <rec.Error>`; else re-check path scope and SHA-256, build and write `.intent/<id>.yml` (`source: "antigravity-session"` or `"committed-intent"`, `method: "hook"`, `review_duration_ms`, `drafted_by` falling back to `"antigravity/" + in.ModelName` if transcript had none, `plan_review`), write local root intent (unless committed intent), emit audit event, and report `IntentGate: intent lock written: .intent/<id>.yml...` (with plan review counts, scope line, and repo-wide warnings).
    - If `step_index == rec.StepIdx` is absent and `in.InitialNumSteps != nil && rec.StepIdx < *in.InitialNumSteps`: remove the stale pending file and emit nothing.
    - Otherwise leave pending and emit nothing.
  - `intentgate hook antigravity ratify-question <plan path>`: prints `antigravity.RenderCard` with repo-relative path (or absolute outside repo) and file SHA-256. Exit 2 if missing/unreadable.
  - `intentgate hook antigravity print-config`: prints the `.agents/hooks.json` fragment under `"intentgate"` (`enabled: true`, `PreToolUse` matcher `"ask_question"` running `intentgate hook antigravity pre` timeout 60, flat `PostInvocation` running `intentgate hook antigravity post-invocation` timeout 30).
  - Update `usage` in `cmd/intentgate/hook.go` to list both `hook claude-code ...` and `hook antigravity ...` forms.
- **`intentgate init --agent antigravity` (`cmd/intentgate/init.go`):**
  - Accept `--agent claude-code|antigravity|generic`.
  - For `antigravity`: plan `.agents/hooks.json` (merge top-level `"intentgate"` entry idempotently, preserving other named hooks; exit 3 and leave untouched on invalid JSON or wrong types), agent instructions (`AGENTS.md` else `CLAUDE.md` else new `AGENTS.md`), and `.intentgate.yml`. Update `nextStep("antigravity")` and `instructionsSection("antigravity")`.
- **Process map (`docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`):**
  - Add `hook antigravity pre`, `hook antigravity post-invocation`, `hook antigravity ratify-question`, `hook antigravity print-config` to the `commands:` list of the hooks entry and re-render with `python3 -I scripts/sdlc-map.py`.
- Review 1 amendments (binding):
  - Keep `claims/REQ-0002.yml` untouched in S-26 (linking `TestAntigravityHook_RatifyCardWritesLock` to `REQ-0002` changes `tests_hash` in signed `baselines/phase0-5.json`; defer the claim link to the next Baseline `phase0-6`, matching S-18 / `6b916e8`).
  - `RootIntentScope` ignores all transcript entries with `step_index >= currentStepIdx` before updating pending/in-flight state (so later steps cannot close an in-flight card and turn it into a prior boundary).
  - `agyCheckPlan` resolves symlinks on `artifactDirectoryPath` (`filepath.EvalSymlinks`) before enforcing the non-filesystem-root check.
  - `agyPostInvocation` returns `{}` without pruning pending records when reading `transcriptPath` fails.

## Tests first (committed red)

- `internal/lock`: extend `lock_test.go` so `source: antigravity-session` validates (and rejects `path`/`blob`), and `schema/intentgate.lock.v1.json` enum test includes `"antigravity-session"`.
- `internal/hooks/antigravity`:
  - `TestParseCard_AndRender`: exact round-trip; rejects wrong options, extra options, reordered options, `is_multi_select: true`, missing `IntentGate: ` prefix, bad sha256, multiple questions.
  - `TestParseInput`: PreToolUse (`ask_question` with array and string-encoded `questions`) and PostInvocation camelCase fields including `stepIdx: 0`.
  - `TestStepAnswer_AndRatified`: `A1: Ratify` and `A1: Ratify, yes` → true; `A1: Not yet`, `A1: Ratify later`, non-DONE status, missing step → false.
  - `TestRootIntentScope`: whole session, since previous ratified card, reused when no new turns, rejected card does not reset scope, current card at `currentStepIdx - 1` is not treated as an earlier boundary, T3 wrapper stripping, `draftedBy` from `PLANNER_RESPONSE`.
- `cmd/intentgate`:
  - `TestAntigravityRatifyQuestion`: prints expected JSON; missing file → exit 2.
  - `TestAntigravityHook_RatifyCardWritesLock` (**REQ-0002**): `pre` + `post-invocation` with `A1: Ratify` (and `A1: Ratify, yes`) → writes `.intent/<id>.yml` (`method: hook`, `source: antigravity-session`, `drafted_by`, `review_duration_ms`, criteria from plan) + local root intent + `injectSteps` output; `Not yet` → no lock + `(not ratified)`; plan edited between `pre` and `post-invocation` → no lock + changed message; ordinary question → `{"decision":"allow"}` and `{}`; out-of-scope path (not in repo or `artifactDirectoryPath`) → checked before stat/read, no lock; committed intent (`intent: intents/...`) works end-to-end; `pre` always exits 0 with `{"decision":"allow"}` even on bad input.
  - `TestAntigravityHook_PostInvocationStalePending`: pending `stepIdx < initialNumSteps` absent from transcript is pruned quietly; `stepIdx >= initialNumSteps` absent from transcript is kept.
  - `TestInit_Antigravity`: creates `.agents/hooks.json`, `AGENTS.md`, `.intentgate.yml`; re-run is unchanged; merges into existing `.agents/hooks.json` preserving other named hooks; invalid `.agents/hooks.json` exits 3 leaving all files untouched.

## Claim links

After green: add `"github.com/autox-my/intent-gate/cmd/intentgate::TestAntigravityHook_RatifyCardWritesLock"` to `claims/REQ-0002.yml` `tests:` (exact id as in `build/junit.xml`).

## Verify

```bash
scripts/check.sh   # must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `internal/hooks/antigravity/**`
- `internal/lock/**`
- `schema/intentgate.lock.v1.json`
- `cmd/intentgate/**`
- `claims/REQ-0002.yml` (`tests:` only)
- `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`

## Forbidden

Everything else, including `.agents/hooks.json`, `AGENTS.md`, `internal/ratify/**`, `internal/hooks/claudecode/**`, and all other claims. If a forbidden file must change, stop and report.

## Out of scope

- Rebuilding/installing `/opt/homebrew/bin/intentgate` on `PATH` and running `intentgate init --agent antigravity` on this repo (do that right after S-26 merges so `.agents/hooks.json` never runs against an older binary).
- Locking on `exit_plan_mode` (agent-to-agent handoff in Orchestrator mode, not a human ratification click).
- Phase 1 `PreToolUse` footprint warnings before file edits.

```intentgate
criteria:
  - text: "REQ-0002: Antigravity hook (PreToolUse ask_question + PostInvocation) writes an intent lock on Ratify, scopes root intent across turns, and records source antigravity-session"
    footprint: ["internal/hooks/antigravity/**", "internal/lock/**", "schema/intentgate.lock.v1.json", "cmd/intentgate/**", "claims/REQ-0002.yml"]
  - text: "intentgate init --agent antigravity configures .agents/hooks.json idempotently and the SDLC process map stays green"
    footprint: ["cmd/intentgate/**", "docs/process/sdlc-map.yml", "docs/process/sdlc-map.md"]
```
