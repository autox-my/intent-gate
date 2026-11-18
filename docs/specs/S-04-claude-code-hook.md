# S-04: Claude Code plan-approval hook

**Base commit:** the commit that adds this spec   **Branch:** feature/s-04-claude-code-hook
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The first way to create a lock: when the human approves a Claude Code plan, write the intent lock. It wires `internal/ratify` (S-03) to Claude Code's `PreToolUse`/`PostToolUse` hooks on `ExitPlanMode`, and adds the first `intentgate` binary.

## Criteria (claims this slice must satisfy)

- REQ-0002: approving a plan that carries an `intentgate` block writes `.intent/<id>.yml` and the root intent record; rejecting it (only `PreToolUse` fires) writes neither.

Supporting (already linked, don't re-link): REQ-0012 (`review_duration_ms`), REQ-0015 (masking via `ratify.MaskAndBuild`).

## Read first

- `docs/design/ratification.md` §1, §1.1, §2 (incl. "Verified 2026-11-18"), §4
- `docs/design/intent-lock.md` §1–§2 (ratifier format, `drafted_by`)
- `internal/ratify` and `internal/lock` public APIs (S-01, S-03)

## Choices fixed here

- Packages:
  - `internal/hooks/claudecode` (pure): `ParseInput([]byte) (Input, error)` for both events (fields per ratification.md §2: `hook_event_name`, `session_id`, `transcript_path`, `cwd`, `tool_name`, `tool_use_id`, `tool_input.plan`, `tool_response.plan`; unknown fields ignored, this is Claude Code's format, not ours); `RootIntent(transcript []byte) (text, draftedBy string, err error)`; `Output{SystemMessage string}` rendered as `{"systemMessage": "..."}`.
  - `internal/gitx` (adapter, git CLI): `TopLevel(dir)`, `UserEmail(dir)`, `BaseRef(dir)`.
  - `internal/ids` : `NewULID(t time.Time, r io.Reader) (string, error)`, in-house (no dependency), Crockford base32, 48-bit ms time + 80 random bits; production passes `crypto/rand.Reader`.
  - `cmd/intentgate`: `intentgate hook claude-code pre|post|print-config`. All logic sits in a testable `Run(args, stdin, stdout, deps)` with injected clock, random source, state dir and git; `main` only wires real ones.
- Root intent from the transcript (JSONL): the human turns are entries with `"type": "user"` whose `message.content` is a string, or an array whose `text` blocks are joined; skip entries with `isMeta: true` or `isSidechain: true`, and any entry containing a `tool_result` block. Turns are joined with `"\n\n---\n\n"`, in file order. A malformed line is an error. `draftedBy` is `claude-code/<model>` from the last `"type": "assistant"` entry's `message.model`, or empty. Build fixtures from the structure of a real local transcript (inspect keys only; commit synthetic text, never real content).
- `pre` (`PreToolUse`, `tool_name` `ExitPlanMode`): `ratify.DraftCriteria(tool_input.plan)`. On success, write a pending record `<state>/pending/<session_id>/<tool_use_id>.json` = `{"finished_at": RFC3339Nano UTC, "plan_sha256": hex}` (dirs 0700, file 0600) and print a `systemMessage` listing the criteria and footprints ("IntentGate will lock these criteria if you approve: …"). On a drafting error, print `systemMessage` "IntentGate: no lock: …" and write nothing. The pending record holds no root intent text.
- `post` (`PostToolUse`, `ExitPlanMode`): fires only on approval. Read `tool_response.plan` (re-draft it; it wins over whatever `pre` saw), the transcript, the pending record; `ratify.MaskAndBuild` with: `ids.NewULID(now)`, `gitx.BaseRef`, source `claude-code-session`, ratifier `git:<user.email>`, ratified-at `now`, method `hook`, `review_duration_ms` = now − `finished_at` in ms (0 if negative), drafted-by from the transcript. Then `ratify.Store{RepoRoot: gitx.TopLevel(cwd), StateDir: state}.Write`, delete the pending record, and print `systemMessage` "IntentGate: intent lock written: .intent/<id>.yml".
- Every run of `pre` or `post` first deletes pending records older than 24 h (by `finished_at`, not file mtime) and empty session dirs.
- Fail open, always: both events exit 0 and print exactly one JSON object on stdout. Any error becomes `systemMessage` "IntentGate: no lock: <reason>". Never exit 2, never `permissionDecision`, never write to stderr on success. A `tool_name` other than `ExitPlanMode` prints `{}` and exits 0.
- Review 1 amendments (binding):
  - `post` without a pending record for the same `session_id` + `tool_use_id` writes no lock ("no lock: no pending record; was the plan shown by the PreToolUse hook?"). `review_duration_ms` is then always now − `finished_at` (0 only if negative).
  - Root intent also skips entries with `isCompactSummary: true` or `isVisibleInTranscriptOnly: true`, entries whose text is exactly `[Request interrupted by user]` or `[Request interrupted by user for tool use]`, and entries whose text starts with `<command-name>`, `<command-message>`, `<command-args>`, `<local-command-stdout>`, `<local-command-stderr>`, `<bash-stdout>` or `<bash-stderr>`. `<system-reminder>…</system-reminder>` spans are removed from human text; a turn left empty is dropped.
  - Exit codes: `pre`/`post` always exit 0 with one JSON object, including a bad hook command line (`hook claude-code pre extra`), which prints a "no lock:" `systemMessage`. Every other usage error (unknown command, `hook` without a valid agent/event, `print-config` with extra args) exits 2 with usage on stderr (core-contracts.md §3).
  - Pending records are written temp file + rename. Pruning deletes a record only when `|now − finished_at| > 24 h` or when it is unparseable *and* its mtime is older than 24 h.
  - One test runs the built binary (`os/exec`, `go build` into a temp dir) for `pre` and `post` and asserts stdout is exactly one JSON object and stderr is empty.
- State dir: `$INTENTGATE_HOME` if set, else `~/.intentgate`.
- `BaseRef`: `$INTENTGATE_BASE_REF` if set; else the short name of `refs/remotes/origin/HEAD` without `origin/`; else `main` if a local branch `main` exists, else `master` if it exists; else an error (no lock).
- `UserEmail`: `git config user.email`; empty is an error (no lock), since the ratifier is required.
- `print-config` prints the `.claude/settings.json` `hooks` fragment: `PreToolUse` and `PostToolUse`, matcher `ExitPlanMode`, command `intentgate hook claude-code pre|post`, `PreToolUse` timeout 60, `PostToolUse` timeout 30. It writes no file.
- Dependencies: none new.

## Tests first (committed red)

- `TestHook_ApprovedPlanWritesLock` (cmd/intentgate, temp git repo + temp state dir, fake clock and random source): `pre` then `post` with fixture inputs → `.intent/<id>.yml` exists, `lock.Parse` + `lock.Validate` empty + `lock.VerifyHash` true, method `hook`, source `claude-code-session`, ratifier `git:<email>`, `review_duration_ms` = the fake-clock gap, root intent record = the masked transcript text; pending record gone; stdout is one JSON object. (REQ-0002)
- `TestHook_RejectedPlanWritesNothing`: `pre` only → no `.intent/`, no root intent record; an old pending record (25 h) is deleted on the next run, a fresh one is kept. (REQ-0002)
- `TestHook_FailsOpen`: plan without a block, missing transcript, empty user.email, unparseable stdin → exit 0, one JSON object with a "no lock:" `systemMessage`, nothing written.
- `TestHook_ApprovedPlanDiffersFromPre`: `tool_response.plan` differs from the `pre` plan → the lock has the approved plan's criteria.
- `TestRootIntent` (claudecode): string and block content, `isMeta`/`isSidechain`/`tool_result` skipped, join order, malformed line → error, `draftedBy` from the last assistant entry.
- `TestParseInput`: both events from fixtures; unknown fields ignored.
- `TestNewULID`: known-answer for a fixed time and fixed bytes; 26 chars; passes `lock.Validate`'s ULID rule; time-ordered.
- `TestBaseRef` (gitx, temp repos): env override, origin/HEAD, `main`, `master`, none → error.

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add `tests:` to REQ-0002: `TestHook_ApprovedPlanWritesLock` and `TestHook_RejectedPlanWritesNothing`, ids exactly as in `build/junit.xml`.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `cmd/intentgate/**`
- `internal/hooks/claudecode/**`, `internal/gitx/**`, `internal/ids/**`
- `go.mod`, `go.sum` (only if `go mod tidy` changes them)
- `claims/REQ-0002.yml`: the `tests:` key only.

## Forbidden

Everything else, including `internal/ratify/**`, `internal/lock/**`, `docs/**`, `.req/**`, `.claude/**` and `scripts/**`. If a forbidden file must change (for example `ratify` lacks something), stop and report.

## Out of scope

- The plan review and its `systemMessage` findings (plan-review slice), and the hand check of whether `systemMessage` renders before the approval prompt.
- `intentgate lock`, `amend`, `init` (S-05+).
- Installing the hook into `.claude/settings.json` (`print-config` only prints it).
