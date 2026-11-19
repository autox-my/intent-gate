# S-18: ratify a plan through a question card

**Base commit:** the commit that adds this spec   **Branch:** feature/s-18-ratify-card
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

One-click locking in harnesses that capture plan approval (T3 Code). Design: ratification.md §2.1 "Ratify card" (on `main`). This slice isn't locked (it builds the path that will lock the next ones).

## Criteria

No requirement changes: REQ-0002 ("create an intent lock when a human approves an agent plan") is what the card implements; add the new end-to-end test to its `tests:`.

## Choices fixed here

- `intentgate hook claude-code ratify-question <plan path>`: prints the JSON `{"header":"IntentGate","question":"Ratify the plan in <path> (sha256 <hex>)?","options":[{"label":"Ratify","description":"Write the intent lock for this plan"},{"label":"Not yet","description":"No lock"}],"multiSelect":false}` with `<path>` repo-relative (or absolute outside the repo) and `<hex>` the file's SHA-256. Exit 2 if the file is missing.
- `pre`/`post` also accept `tool_name: AskUserQuestion`:
  - only a single question whose `header` is exactly `IntentGate` and whose text matches `^Ratify the plan in (.+) \(sha256 ([0-9a-f]{64})\)\?$`; anything else prints `{}` and exits 0 (never interferes with ordinary questions);
  - the plan path resolves inside the repository top level or `~/.claude/plans/`; anything else is "no lock: plan path outside the repo and the plans folder";
  - `pre`: read the file, check the hash, draft (masked) and run the plan review as for `ExitPlanMode`, write the pending record; a hash mismatch is "no lock: the plan changed since the question";
  - `post`: the selected answer for that question must be exactly `Ratify` (find how the harness reports answers in `tool_response`, from Claude Code's hooks docs and the structure of a local transcript's `AskUserQuestion` result; inspect keys only, commit synthetic fixtures); re-read the file and re-check the hash; then the same lock path as an approved plan. `Not yet` or any other answer: "IntentGate: no lock (not ratified)".
- Root-intent scope: a ratified card is a boundary exactly like an approved `ExitPlanMode` (the `Ratify` answer is its approval).
- `intentgate init` also registers `PreToolUse` and `PostToolUse` entries with matcher `AskUserQuestion` (same commands, same timeouts); dedupe rules as today. Re-run it on this repo and commit the result unedited.
- `AGENTS.md` step 1: where the harness captures plans, the orchestrator writes the spec, runs `ratify-question`, and asks that exact card; the human's `Ratify` writes the lock.
- Process map: name `ratify-question`; re-render.

## Tests first (committed red)

- `TestRatifyQuestion`: printed JSON for a fixture plan; missing file → 2.
- `TestHook_RatifyCardWritesLock` (REQ-0002): `pre` + `post` for a marked question with answer `Ratify` → lock written, `method: hook`, criteria from the plan; `Not yet` → nothing; a plan edited between `pre` and `post` → nothing and the "changed" message; an unmarked question → `{}`, nothing; a path outside the repo and plans folder → nothing.
- `TestRootIntent_RatifiedCardIsBoundary`.
- `TestInit_RegistersAskUserQuestion`: init adds both entries; re-run unchanged.

## Verify

`scripts/check.sh` GREEN (incl. process map and req check).

## Footprint (allowed paths)

- `cmd/intentgate/**`, `internal/hooks/claudecode/**`
- `.claude/settings.json` (init output only), `AGENTS.md` (the step-1 line and the init section), `claims/REQ-0002.yml` (`tests:` only)
- `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`

## Forbidden

Everything else. If a forbidden file must change, stop and report.
