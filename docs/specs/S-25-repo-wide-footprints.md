# S-25: flag footprints that match every path

**Base commit:** the commit that adds this spec   **Branch:** feature/s-25-repo-wide-footprints
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/gemini-4-argon

A footprint glob like `**` makes the footprint check vacuous. This slice flags it at both ends: a warning when the lock is written, and a suspect reason in the PR check. Advisory only. From the review of the 2026-11-21 ADR-vs-codebase research (gap 3).

## Criteria

- REQ-0046: The PR check shall mark an intent lock suspect when one of its footprint globs matches every path.
- REQ-0047: The lock command shall warn the ratifier when a footprint glob matches every path.

Both ratified by the human on 2026-11-21 and **new in this slice**: the orchestrator commits `claims/REQ-0046.yml` and `claims/REQ-0047.yml` on the branch base (after the lock commit) with `tests: []`. The worker fills `tests:` only.

## Read first

- `docs/design/footprint-check.md` **§7** (the contract; on `main`), §2
- `docs/design/pr-check.md` §3–§4; `docs/design/ratification.md` §1–§3
- `internal/footprint`, `internal/verify` (`checkLock`, suspect reasons), `internal/report`, `cmd/intentgate/lock.go` (`lock`, `amend`), `internal/hooks/claudecode`, `schema/intentgate.verify.v1.json`

## Choices fixed here

- Everything in footprint-check.md §7 is binding: the repo-wide definition and its exact probe set, judging each positive glob alone, `footprint.RepoWide` as the single pure function both ends call, the suspect reason `repo-wide-footprint` counting amendment-added globs, the warning's exact text and where it appears (CLI before the confirmation prompt; the hook's message), and that the lock is still written.
- `internal/verify`: a new constant `ReasonRepoWide = "repo-wide-footprint"`, appended after the existing reasons in the same order rules; `schema/intentgate.verify.v1.json` adds it to the suspect enum. The comment and summary name the criterion and the glob (in `internal/report`).
- Existing golden or known-answer outputs change only where a fixture now has a repo-wide glob; if any existing fixture uses `**` as a footprint, keep the fixture and update its expected output to include the new reason (say which in the report). Don't change a fixture to avoid the reason.
- `internal/footprint` stays PURE and keeps `TestNoModelCall_Imports` passing unchanged (doublestar is already allowed).

## Tests first (committed red)

- `TestRepoWide` (internal/footprint): `**`, `**/*`, `**/**`, `{**,x}` → true; `*`, `**/*.go`, `src/**`, `!**`, `.*`, `a/**` → false.
- `TestVerify_RepoWideFootprint` (internal/verify, REQ-0046): a lock with `**` in C2 → suspect `repo-wide-footprint`, outcome `findings`, exit 1; with `**` only via an `add-glob` amendment → same; a lock with `**` and `!vendor/**` → same; a lock with `src/**` → no such reason. Output stays byte-identical across two runs.
- `TestLock_RepoWideWarning` (cmd, REQ-0047): `lock --from-plan` with a `**` footprint prints the §7 warning on stderr before the prompt and still writes the lock after `yes`; `amend --add "**"` warns too; a narrow footprint prints no warning.
- `TestHook_RepoWideWarning` (cmd, REQ-0047): the hook's `post` message for an approved plan with `**` contains the warning, and the lock is written.

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one failure in the report.

## Claim links

After green: REQ-0046 `tests:` = `TestVerify_RepoWideFootprint`; REQ-0047 `tests:` = `TestLock_RepoWideWarning`, `TestHook_RepoWideWarning`; ids exactly as in `build/junit.xml`.

## Verify

```bash
scripts/check.sh
```
Expected on the branch until the human signs: every step PASS except `req satisfy`, `req check` and `req baseline`, whose only complaints concern REQ-0046 and REQ-0047 (link not recorded, no signed Baseline). Quote them. Then the human records the two links (`req satisfy --record docs/design/footprint-check.md REQ-0046` and `… REQ-0047`) and signs `phase0-5`; the orchestrator points `scripts/check.sh`, `docs/process/sdlc-map.yml` (re-rendered) and `docs/glossary.md` at it, and `scripts/check.sh` must end `verdict: GREEN`.

## Footprint (allowed paths)

- `internal/footprint/**`, `internal/verify/**`, `internal/report/**`, `internal/ratify/**`, `internal/hooks/claudecode/**`, `cmd/intentgate/**`
- `schema/intentgate.verify.v1.json`
- `claims/REQ-0046.yml`, `claims/REQ-0047.yml` (`tests:` only, for the worker)
- `docs/design/footprint-check.md` (the two `req-satisfies` lines only, for the worker)
- For the Baseline step (human and orchestrator only): `.req/satisfied`, `baselines/**`, `decisions/**`, `scripts/check.sh`, `docs/glossary.md`, `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`

## Forbidden

Everything else, including every other claim and `internal/lock/**`. The worker never touches the Baseline-step paths and does not run `req satisfy --record`. If a forbidden file must change, stop and report.

## Out of scope

- Governance-path warnings (rejected in §7).
- Footprints that are wide but not repo-wide (`src/**` in a repo where everything is under `src/`).

```intentgate
criteria:
  - text: "REQ-0046: The PR check shall mark an intent lock suspect when one of its footprint globs matches every path."
    footprint: ["internal/footprint/**", "internal/verify/**", "internal/report/**", "schema/intentgate.verify.v1.json", "claims/REQ-0046.yml", "docs/design/footprint-check.md", "cmd/intentgate/**"]
  - text: "REQ-0047: The lock command shall warn the ratifier when a footprint glob matches every path."
    footprint: ["internal/footprint/**", "internal/ratify/**", "internal/hooks/claudecode/**", "cmd/intentgate/**", "claims/REQ-0047.yml", "docs/design/footprint-check.md"]
  - text: "The next Baseline (phase0-5) attests REQ-0046, REQ-0047 and their satisfaction links, and scripts/check.sh verifies it"
    footprint: [".req/satisfied", "baselines/**", "decisions/**", "scripts/check.sh", "docs/glossary.md", "docs/process/sdlc-map.yml", "docs/process/sdlc-map.md"]
```
