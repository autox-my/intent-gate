# S-05: `intentgate lock --from-plan` and `intentgate amend`

**Base commit:** the commit that adds this spec   **Branch:** feature/s-05-lock-and-amend
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The CLI ways to ratify: create a lock from a plan file after an interactive confirmation, and amend a lock (add a footprint glob, or re-ratify an expired lock). It also gives `amendments:` a typed shape, which closes the REQ-0014 gap left open in S-01.

## Criteria (claims this slice must satisfy)

- REQ-0004: `intentgate lock --from-plan plan.md` creates a lock from the plan's `intentgate` block, with method `cli-confirm`, source `plan-file`.
- REQ-0005: `lock` and `amend` write only after an interactive confirmation: stdin and stdout must be terminals, and the human must type `yes`.
- REQ-0030 (extend its links): `intentgate amend <id> --criterion C2 --add '<glob>'`, the exact command a footprint finding prints, adds the glob in one confirmed step.
- REQ-0014 (extend its links): an amendment entry is typed and unknown keys are rejected, so root intent text can't ride in `amendments:`.

## Read first

- `docs/design/ratification.md` §1, §1.1, §3, §4, §5
- `docs/design/footprint-check.md` §5 (amend)
- `docs/design/intent-lock.md` §2, §5 (validity, `--reratify`)
- `docs/design/core-contracts.md` §3 (exit codes)
- `cmd/intentgate` (S-04: `Run`, `Deps`), `internal/ratify`, `internal/lock`

## Choices fixed here

- `intentgate lock --from-plan <plan.md> [--prompt <file>]`:
  - Plan: the file's text, `ratify.DraftCriteria`. Root intent: `--prompt` file's text, else read from stdin after printing "Paste the root intent (the request the plan answers), then a line with only `.`:", up to that line (excluded).
  - Then print the criteria and footprints and "Type yes to lock these criteria:"; read one line; only `yes` (after trimming spaces and the line ending) confirms. `review_duration_ms` = time from printing that prompt to reading the line.
  - Lock: `ratify.MaskAndBuild` with `ids.NewULID`, `gitx.BaseRef`, source `plan-file`, ratifier `git:<user.email>`, method `cli-confirm`, ratified-at now; `ratify.Store.Write`; print "intent lock written: .intent/<id>.yml".
- `intentgate amend <id> --criterion <Cn> --add <glob>` and `intentgate amend <id> --reratify`:
  - Reads `<toplevel>/.intent/<id>.yml` (`<id>` must pass the ULID rule; no prefixes). `lock.Parse`, `lock.Validate`, `lock.VerifyHash` must pass, else exit 3.
  - `--add`: the glob must pass the footprint rules and not already be in that criterion's footprint; append it. `--reratify`: replace `ratification.ratifier`, `ratified_at`, `method` (`cli-confirm`), `review_duration_ms` with the new confirmation's; drop `drafted_by`. Exactly one of `--add` (with `--criterion`) or `--reratify`.
  - Both: show the change, the same `yes` confirmation, `revision` +1, append an amendment entry, re-hash, then replace the lock file atomically (temp + rename) via a new `ratify.Store.Amend(old, new lock.Lock) error` that refuses unless the id and `root_intent` are unchanged, `new.Revision == old.Revision+1`, the file on disk still hashes to `old.Hash`, and `new` validates and verifies. The root intent record is never touched.
- Typed amendment in `internal/lock` (replaces `map[string]any`):
  `{revision: int ≥ 2, kind: add-glob | reratify, criterion: Cn (add-glob only), glob: string (add-glob only), by: string, at: RFC 3339 UTC with Z, method: hook | cli-confirm | issue}`. Unknown keys rejected. Validation: revisions are 2, 3, … in order and the last equals the lock's `revision` (or there are none and `revision` is 1); `criterion` exists; `glob` passes the footprint rules and is in that criterion's footprint. Update `schema/intentgate.lock.v1.json` to match, and keep the S-01 golden hash (no amendments → unchanged).
- Confirmation gate: a `Deps.IsTerminal(fd)` check on stdin and stdout (real one: `os.File.Stat` and `ModeCharDevice`, no new dependency). Not both terminals → exit 2 with "intentgate lock needs an interactive terminal; ratification is a human act" (`amend` says "intentgate amend …"). There is no `--yes` flag, and no environment variable that skips it.
- Exit codes (core-contracts.md §3): 0 written; 1 not confirmed, or a drafting error ("no lock: …"); 2 usage or no terminal; 3 the scan failed, git can't resolve the base, or the lock to amend is invalid or fails its hash. The hook keeps its own exit-0 rule (S-04).
- Dependencies: none new.

## Tests first (committed red)

In `cmd/intentgate` with temp repo, temp `INTENTGATE_HOME`, fake clock, fake terminal check and scripted stdin:

- `TestLock_FromPlan`: `--prompt` file and pasted root intent both produce a valid, hash-verified lock with method `cli-confirm`, source `plan-file`, the right criteria and `review_duration_ms`; root intent record written. (REQ-0004)
- `TestLock_RequiresTerminalAndYes`: stdin not a terminal, stdout not a terminal → exit 2, nothing written; answers `no`, `y`, `YES `, empty, EOF → exit 1, nothing written; `yes` → written. (REQ-0005)
- `TestAmend_AddGlob`: the exact fix command from a footprint finding (build it with `footprint` for a path, then run it) adds the glob; revision 2; one `add-glob` amendment; hash verifies; root intent record untouched; a duplicate glob, an unknown criterion, a bad glob → error, file unchanged. (REQ-0030)
- `TestAmend_Reratify`: new `ratified_at` and ratifier; `lock.ValidAt` true again after 15 days; revision +1; `reratify` amendment. 
- `TestAmend_RequiresTerminalAndYes`: as for lock. (REQ-0005)
- `TestAmend_RefusesTamperedLock`: a lock file edited on disk (hash fails) → exit 3, unchanged.
- In `internal/lock`: `TestParse_RejectsUnknownAmendmentKeys` (an amendment carrying `root_intent_text` fails to parse) (REQ-0014) and `TestValidate_Amendments` (revision sequence, kinds, criterion, glob).

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add to `tests:` (ids exactly as in `build/junit.xml`): REQ-0004 `TestLock_FromPlan`; REQ-0005 `TestLock_RequiresTerminalAndYes` and `TestAmend_RequiresTerminalAndYes`; REQ-0030 also `TestAmend_AddGlob`; REQ-0014 also `TestParse_RejectsUnknownAmendmentKeys`. Keep the existing links.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `cmd/intentgate/**`
- `internal/lock/**`, `internal/ratify/**` (only `Store.Amend` and its tests in ratify)
- `schema/intentgate.lock.v1.json`
- `claims/REQ-0004.yml`, `claims/REQ-0005.yml`, `claims/REQ-0014.yml`, `claims/REQ-0030.yml`: the `tests:` key only.

## Forbidden

Everything else, including `internal/footprint/**`, `internal/hooks/**`, `docs/**`, `.req/**` and `scripts/**`. If a forbidden file must change, stop and report.

## Out of scope

- `intentgate lock --issue` (S-06, needs the GitHub adapter).
- `intentgate init` and the agent-instructions snippet.
- The PR check (`intentgate verify`).
