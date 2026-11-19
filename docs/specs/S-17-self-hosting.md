# S-17: IntentGate on its own repo, one-click locks

**Base commit:** the commit that adds this spec   **Branch:** feature/s-17-self-hosting
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

Cut the human's part of locking a slice from about six terminal actions to one approve click: install IntentGate in this repository with `intentgate init`, scope the hook's root intent to one plan, and add a one-command fallback for harnesses where the hook can't run. This slice itself is not locked (it installs the thing that locks); the PR check will say "no intent lock".

## Criteria

No requirement changes. REQ-0002 (the hook writes the lock on approval) and REQ-0006 (root intent stored verbatim) are what the scoping serves; their links stay. Design: ratification.md §2, "Root intent scope" (on `main`).

## Choices fixed here

- **Root intent scope** (`internal/hooks/claudecode`): `RootIntent(transcript, currentToolUseID)` keeps only human turns after the last `ExitPlanMode` tool use that came **before** the current one and was approved (its `tool_result` is not an error). Rejected ones don't reset the scope. No earlier approved one: all human turns (today's behaviour). The existing filters (meta, sidechain, tool results, command tags, system reminders, compaction summaries, interrupts) still apply. `pre` and `post` both pass the hook input's `tool_use_id`.
- **Review 1 amendments (binding):**
  - Only `ExitPlanMode` tool uses that appear **before** the current one in transcript order are boundary candidates (one in the same assistant message counts only if it comes earlier in that message's content). Uses after the current one are ignored.
  - A candidate is approved when it has a result that is not an error **and** whose text doesn't contain a denial marker (`The user doesn't want to proceed`, `User rejected`, `rejected the plan`; matched case-insensitively). A candidate with **no** result before the current use counts as a boundary too (a truncated transcript must not leak earlier slices' asks).
  - Empty scope (no human turn since the boundary, e.g. one message that asked for two slices): reuse the scope the previous approved plan had, i.e. the human turns between the boundary before it and it.
  - The `pre` message and the `post` message say which scope was used: `root intent: N human turn(s) since the previous approved plan` / `… reused from the previous plan (no new turns)` / `… whole session (no earlier approved plan)`.
- **Install in this repo:** run the built `intentgate init` (claude-code agent) at the repo root and commit what it writes: `.claude/settings.json` (hooks + `permissions.defaultMode: "plan"`), the marked section in `AGENTS.md`, `.intentgate.yml`. Don't hand-edit them. Also add `.claude/settings.local.json` to `.gitignore` if it isn't there.
- **Fallback:** `scripts/lock-slice.sh <S-NN>`: finds `docs/specs/<S-NN>-*.md` (exactly one, else exit 2), builds `intentgate` from the checkout into a temp dir, loads `.env` if present (never prints it), writes the root intent `Implement <S-NN> as specified in <spec path>.` to a temp file, runs `intentgate lock --from-plan <spec> --prompt <tmp>` (the human still types `yes`), and on success runs `git add .intent/<new id>.yml` and prints the commit command for the human or orchestrator. It refuses to run without a terminal, exactly like `lock`.
- **Process map:** name `scripts/lock-slice.sh` and `intentgate init` where the map describes locking a slice; re-render.
- `AGENTS.md`, "The flow for one slice", step 1: one line saying the orchestrator presents the spec as a plan (approve = lock); the fallback is `scripts/lock-slice.sh`.

## Tests first (committed red)

- `TestRootIntent_ScopedToCurrentPlan` (claudecode): a fixture transcript with turns, an approved `ExitPlanMode`, more turns, a rejected one, more turns, then the current one → only the turns after the approved one, including those around the rejection; with no earlier approved plan → all turns; the current tool use id is never a boundary.
- `TestHook_RootIntentScoped` (cmd): `pre` + `post` with that transcript → the root-intent record holds only the scoped turns.
- `scripts/lock_slice_test.sh` or a Go test that runs the script with a fake `intentgate`: unknown or ambiguous slice → exit 2; no terminal → refuses; the generated root intent line.

## Verify

```bash
scripts/check.sh   # GREEN, incl. process map and req check
git diff origin/main...HEAD --name-only   # only the footprint below
```

## Footprint (allowed paths)

- `internal/hooks/claudecode/**`, `cmd/intentgate/**`
- `.claude/settings.json`, `.intentgate.yml`, `AGENTS.md` (the init section and the one step-1 line), `.gitignore` (one line)
- `scripts/lock-slice.sh`, `scripts/lock_slice_test.sh`
- `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`

## Forbidden

Everything else, including `claims/**`, `decisions/**`, `baselines/**`, `.req/**`, `docs/design/**`, `.intent/**`. If a forbidden file must change, stop and report.
