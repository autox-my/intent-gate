# S-10: audit events, `verify --replay` and `intentgate stats`

**Base commit:** the commit that adds this spec   **Branch:** feature/s-10-audit-and-stats
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

Phase 0's audit trail: one `intentgate.audit/1` event per `verify`, `lock` and `amend`; a replay that proves a past decision reproduces; and the footprint-amendment rate derived from git history.

## Criteria (claims this slice must satisfy)

- REQ-0042: every audit event lists each lock's `ratification_method`.
- REQ-0041: `intentgate stats` reports the footprint-amendment rate from the locks in git history.

## Read first

- `docs/design/audit-metrics.md` (all; updated in f86b2af)
- `docs/design/core-contracts.md` §1 (replay), §3
- `cmd/intentgate` (`verify`, `lock`, `amend`, `Deps`), `internal/gitx`, `internal/lock`, `internal/report`

## Choices fixed here

- `internal/audit` (PURE): `Event` with exactly the design's fields plus `base_branch`, `author`, `author_type`, `verify_sha256`; `locks[]` = `{id, revision, hash, ratification_method, review_duration_ms}` sorted by id; `JSON(e) []byte` with fixed key order and a trailing newline. `NewVerifyEvent(…)`, `NewLockEvent(event string, l lock.Lock, …)` take every value as an argument (including `at`, `version`, `repo`, `pr`); no clock inside. Events never contain code, diffs, criterion text, footprints or root intent text. Add `schema/intentgate.audit.v1.json` (draft 2020-12) matching it.
- `verify`:
  - New optional flags `--repo <owner/name>` and `--pr <n>` (recorded only; they change no decision and no verify output).
  - With `--out-dir`, also writes `audit.json`. The event's `at` is the wall clock (UTC, seconds), the only place `verify` reads it; `verify.json` and the other outputs stay byte-identical across runs (REQ-0040 must still pass).
  - When `$CI` is not `true`, also appends the event to `<state>/audit.jsonl` (state dir rules from S-04; file 0600, dir 0700; one line per event; append with `O_APPEND`). A failure to append is a warning on stderr, never a change of exit code.
  - `--replay <event.json>`: read the event, re-run verify with its `base_sha`, `head_sha`, `base_branch`, `author`, `author_type` in the current repo, and compare the SHA-256 of the fresh `verify.json` with `verify_sha256`. Same → exit 0 "replay: identical"; different → exit 1 "replay: differs" and the fresh `verify.json` on stdout; a ref missing → exit 3. `--replay` excludes the other input flags (exit 2 if combined) and writes no audit event.
- `lock` (all three paths, incl. the hook) and `amend`: after a successful write, append an event (`event`: `lock` or `amend`, the one lock, `outcome: "written"`, `repo` from the origin remote if it parses, else empty) to `<state>/audit.jsonl`. Never in CI-only paths; a failure is a warning, never a change of exit code (the hook keeps its single JSON object on stdout).
- `intentgate stats [--since <Nd>] [--format text|json]` (default 30d, text):
  - Window end = `HEAD`'s committer time; start = end − N days.
  - Locks = `.intent/<id>.yml` files present at `HEAD` whose adding commit (first-parent history of `HEAD`, `git log --first-parent --diff-filter=A --format=%H%x00%ct -z -- .intent/`) has a committer time inside the window. Each is read at `HEAD` with `lock.Parse` (invalid ones counted separately as `invalid`, not in the rate).
  - Footprint-amendment rate = locks with ≥ 1 `add-glob` amendment / valid locks, as a fraction with 4 decimal places and as `k/n`; `n = 0` → rate reported as `n/a`, not 0.
  - Also: count by `ratification.method`, median `review_duration_ms` (lower middle for even counts; `n/a` when none).
  - JSON `intentgate.stats/1` with fixed key order; deterministic for the same `HEAD`.
  - `gitx` additions: `LockAdditions(dir, rev)`, using S-08's scrubbed environment and pinned config.
- Review 1 amendments (binding):
  - A verify event lists only locks that pass `lock.Parse` and `lock.Validate` and sit at `.intent/<id>.yml`; other changed lock files are counted in a new integer field `unverifiable_locks`, with no text from them. Schema updated.
  - `--replay` requires `base_sha` and `head_sha` to be full 40- or 64-hex object names that resolve to themselves as commits; anything else (a branch name, a short sha) exits 3.
  - `stats` follows renames: `LockAdditions` uses `--diff-filter=AR --find-renames` on first-parent history, oldest first, and a lock's add time is the time of the original `A`, carried through each `R` to the path it has at `HEAD`. A renamed lock is never "newly added".
  - `audit.jsonl`: opened with `O_APPEND|O_CREATE|O_WRONLY|O_NOFOLLOW`, must be a regular file (else a warning, nothing written), and set to 0600 with `fchmod` after opening, so an existing looser file is tightened.
  - `config_sha256` is `""` when `.intentgate.yml` is absent, and the SHA-256 of its bytes otherwise (an empty file hashes to `e3b0c442…`).
  - REQ-0042 also links `TestVerify_AuditEvent`, which proves the PR check itself writes the method of each lock it evaluated.
- Action: pass `--repo "$GITHUB_REPOSITORY" --pr "$PR_NUMBER"` to `verify` through `env:`; `audit.json` rides in the existing `intentgate` artifact.
- Dependencies: none new.

## Tests first (committed red)

- `TestAuditEvent_RatificationMethod` (internal/audit): an event for two locks (`hook`, `cli-confirm`) lists each lock's method; key order and JSON bytes known-answer; no field carries criterion text, footprints or root intent text (reflect over `Event`). (REQ-0042)
- `TestVerify_AuditEvent` (cmd): `--out-dir` writes `audit.json` with each lock's method, refs, `verify_sha256` = SHA-256 of `verify.json`; `CI=true` → no `audit.jsonl`; `CI` unset → one line appended; `verify.json` identical across two runs while `audit.json`'s `at` differs (fake clock).
- `TestVerify_Replay`: replaying a recorded event → exit 0; a tampered `verify_sha256` → exit 1; an event naming a missing sha → exit 3; `--replay` with `--base` → exit 2.
- `TestLockAndAmend_AuditLog`: `lock --from-plan`, the hook's `post` and `amend` each append exactly one event with the lock's method; an unwritable state dir → warning, exit code unchanged, hook stdout still one JSON object.
- `TestStats_AmendmentRate` (cmd, temp repo with fixed commit dates): 4 locks in the window, 1 with an `add-glob` amendment, 1 re-ratified only, 1 outside the window, 1 invalid → rate `1/3` (0.3333), invalid 1, methods counted, median duration; `--since 1d` with no locks → `n/a`. (REQ-0041)
- `TestStats_Deterministic`: two runs → identical bytes; a different `TZ`/`LC_ALL` → identical.
- `TestGitx_LockAdditions`: first-parent only, so a lock added on a merged side branch counts at the merge commit's time; `-z` parsing.

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add `tests:`: REQ-0042 `TestAuditEvent_RatificationMethod` and `TestVerify_AuditEvent`; REQ-0041 `TestStats_AmendmentRate`, ids exactly as in `build/junit.xml`.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `internal/audit/**`, `internal/gitx/**`, `cmd/intentgate/**`, `action/**`
- `schema/intentgate.audit.v1.json`
- `claims/REQ-0041.yml`, `claims/REQ-0042.yml`: the `tests:` key only.

## Forbidden

Everything else, including `internal/verify/**`, `internal/report/**`, `internal/policy/**`, `internal/lock/**`, `internal/ratify/**`, `docs/**`, `.req/**` and `scripts/**`. If a forbidden file must change, stop and report.

## Out of scope

- Locks per merged PR and findings per PR (need the forge or CI artifacts).
- A summary of the audit event in the check run.
- The plan review (REQ-0016–0020).
