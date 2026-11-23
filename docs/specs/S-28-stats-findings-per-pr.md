# S-28: `intentgate stats` findings per PR from CI artifacts

**Base commit:** the commit that adds this spec   **Branch:** feature/s-28-stats-findings-per-pr
**Orchestrator / Executor:** antigravity / gemini-pro-orchestrator-neon (Neon planner, Hydrogen executor)   **Reviewer:** antigravity / claude-sonnet-5-5 (cross-family)

Phase 1 tuning, second signal: compute findings per PR from downloaded GitHub Actions `intentgate` artifacts (`audit.json` + sibling `verify.json`) via `intentgate stats --artifacts <dir>`, completely offline and deterministic. Design: `docs/design/audit-metrics.md` §2 "Findings per PR (CI artifacts)" (on `main`).

## Criteria

No new claims and no Baseline changes. Existing linked tests on `REQ-0040`, `REQ-0041`, `REQ-0042` (`TestAuditEvent_RatificationMethod`, `TestVerify_AuditEvent`, `TestStats_AmendmentRate`, `TestStats_Deterministic`) and `TestStats_MergedPRCoverage` must stay green unchanged; do not edit `claims/**` or `schema/intentgate.audit.v1.json`.

## Read first

- `docs/design/audit-metrics.md` §1, §2
- `docs/design/README.md` §2 (`internal/audit` is **PURE**, checked by `internal/arch/arch_test.go`)
- `internal/audit/audit.go`, `internal/audit/audit_test.go`
- `cmd/intentgate/stats.go`, `cmd/intentgate/stats_test.go`, `cmd/intentgate/audit.go`, `cmd/intentgate/verify.go`
- `docs/process/sdlc-map.yml`

## Choices fixed here

- **Pure aggregation in `internal/audit/stats.go` (`internal/audit` stays PURE — no `os`, `io/fs`, `net`, or clock calls):**
  - `ArtifactRun`:
    - `Event Event`
    - `At time.Time`
    - `VerifyOutcome string`
    - `FootprintCount int`
    - `SuspectLocks int`
    - `Source string` (slash-separated relative path, final deterministic tie-breaker)
  - `ArtifactSummary` (JSON field order is key order):
    - `Runs int` (`json:"runs"`)
    - `PRs int` (`json:"prs"`)
    - `PRsWithLock int` (`json:"prs_with_lock"`)
    - `PRsWithFindings int` (`json:"prs_with_findings"`)
    - `FindingsPRRate string` (`json:"findings_pr_rate"`)
    - `FindingsPRRatio string` (`json:"findings_pr_ratio"`)
    - `TotalFindings int` (`json:"total_findings"`)
    - `FindingsPerPR string` (`json:"findings_per_pr"`)
    - `MedianFindingsPerPR *int` (`json:"median_findings_per_pr"`)
    - `PRsWithSuspectLock int` (`json:"prs_with_suspect_lock"`)
  - `SummarizeArtifacts(runs []ArtifactRun, start, end time.Time) ArtifactSummary`:
    - Keep only runs with `Event.Schema == SchemaID`, `Event.Event == EventVerify`, `Event.PR > 0`, `!At.Before(start) && !At.After(end)`, and `VerifyOutcome != "skipped"`.
    - `Runs` counts every kept run.
    - Collapse multiple runs for the same `(Event.Repo, Event.PR)` to the single latest run using strict deterministic order: `At` later → `HeadSHA` greater → `VerifySHA256` greater → `Source` greater. Result is independent of input slice order.
    - `PRs` = count of distinct `(repo, pr)` keys.
    - A PR whose latest run has `VerifyOutcome == "no-lock"` counts in `PRs` only (not `PRsWithLock`). Every other kept outcome (`clean`, `findings`, `unverifiable`) increments `PRsWithLock`.
    - Among `PRsWithLock`:
      - `PRsWithFindings`: count where `VerifyOutcome == "findings"`.
      - `PRsWithSuspectLock`: count where `SuspectLocks > 0`.
      - `TotalFindings`: sum of `FootprintCount`.
      - `FindingsPRRatio`: `fmt.Sprintf("%d/%d", PRsWithFindings, PRsWithLock)`.
      - When `PRsWithLock == 0`: `FindingsPRRate = "n/a"`, `FindingsPerPR = "n/a"`, `MedianFindingsPerPR = nil`.
      - When `PRsWithLock > 0`: `FindingsPRRate = fmt.Sprintf("%.4f", float64(PRsWithFindings)/float64(PRsWithLock))`, `FindingsPerPR = fmt.Sprintf("%.4f", float64(TotalFindings)/float64(PRsWithLock))`, `MedianFindingsPerPR` = lower middle of sorted `FootprintCount` slice (`counts[(len(counts)-1)/2]`).
    - Keep cyclomatic complexity of every function ≤ 10.
- **CLI & artifact loader (`cmd/intentgate/stats.go`):**
  - Usage: `usage: intentgate stats [--since <N>d] [--format text|json] [--artifacts <dir>]\n`.
  - If `--artifacts` is explicitly passed as `""`: exit 2 (usage error).
  - When `--artifacts` is omitted: `statsDoc.Artifacts` is `nil` (`json:"artifacts,omitempty"`), so both JSON and text output are byte-identical to today.
  - When `--artifacts <dir>` is passed:
    - Resolve against `deps.Dir`; verify it exists and is a directory (else exit 3).
    - Walk `<dir>` recursively with `filepath.WalkDir` (never following symlinked dirs).
    - For every entry named `audit.json`:
      - Must be a regular file (symlink/FIFO/non-regular → exit 3). Read bounded (max 10 MiB, opened with `os.O_RDONLY|auditOpenFlags` + `f.Stat()` regular-file check).
      - Decode `audit.Event`; invalid JSON or `Schema != audit.SchemaID` → exit 3.
      - If `Event != audit.EventVerify` → skip (not a verify run).
      - Parse `ev.At` with `time.RFC3339`; invalid timestamp → exit 3.
      - If `ev.PR <= 0` or `at.Before(start) || at.After(headTime)` → skip (outside window or not a PR run).
      - Read sibling `verify.json` in the same directory using the same bounded regular-file open (missing, symlink, non-regular, or >10 MiB → exit 3).
      - If `ev.VerifySHA256 != ""` and `sha256Hex(verifyBytes) != ev.VerifySHA256` → exit 3.
      - Parse `verify.json`: require `schema == "intentgate.verify/1"` and `outcome` in `{"skipped", "no-lock", "clean", "findings", "unverifiable"}` and (if `ev.Outcome != ""`) `ev.Outcome == v.Outcome`; else exit 3.
      - Count `suspectLocks` = locks in `verify.json` with `len(suspect) > 0`; `footprintCount = len(v.Findings)`.
      - Append `audit.ArtifactRun` with `Source: filepath.ToSlash(relPath)`.
    - Summarize with `audit.SummarizeArtifacts(runs, start, headTime)` and attach to `statsDoc.Artifacts`.
    - Still reads no wall clock; byte-identical for the same `HEAD` and artifact directory across `TZ` and `LC_ALL`.
  - Text output when `d.Artifacts != nil` (three lines appended after `median review duration:`):
    - `artifact PRs: <prs> (<prs_with_lock> with a lock, <runs> runs)`
    - `PRs with findings: <findings_pr_rate> (<findings_pr_ratio>, <prs_with_suspect_lock> with suspect lock)`
    - `findings per PR: <findings_per_pr> (total <total_findings>, median <median>)` (where `<median>` is `n/a` if nil, else `%d`).
- **Process map (`docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`):**
  - Update the `closing the loop` product description to mention findings per PR from CI artifacts, and re-render with `python3 -I scripts/sdlc-map.py`.
- **Dependencies:** none new.
- **Review 1 amendments (binding):**
  - Split `cmd/intentgate/stats.go` helpers (`parseStatsFlags`, `readAuditEvent`, `loadVerifyArtifact`, `loadArtifactPair`) and invoke `readArtifactSummary` outside `computeStats` so `runStats` and every S-28 helper stays at cyclomatic complexity ≤ 10 (and `computeStats` gains no new branch).
  - Check `--artifacts` root with `os.Lstat`: a symlinked root exits 3 (never silently returns 0 PRs).
  - Require non-empty `verify_sha256` and `outcome` on in-window `verify` events (matching `verify.json` SHA-256 and `outcome`, consistent with `verify --replay`); empty values exit 3.
  - Distinguish JSON parse errors on `audit.json` from schema mismatch errors (`"is not an intentgate.audit/1 event"`).

## Tests first (committed red)

Commit red tests first (failing on assertions, with only minimal compile stubs if needed):

- `internal/audit/stats_test.go`:
  - `TestSummarizeArtifacts`:
    - Window filtering (closed bounds `[start, end]`, before-start and after-end ignored), `pr == 0` ignored, non-verify events ignored, `skipped` bot runs ignored.
    - Latest-run deduplication per `(repo, pr)` (by `At`, then `HeadSHA`, then `VerifySHA256`, then `Source`), independent of input order; distinct repos with the same PR number stay separate.
    - `no-lock` PR counted in `PRs` but excluded from `PRsWithLock` denominator.
    - Suspect-lock-only finding (`VerifyOutcome == "findings"`, `FootprintCount == 0`, `SuspectLocks == 1`) increments `PRsWithFindings` and `PRsWithSuspectLock`, contributes 0 to `TotalFindings`.
    - Mean (`%.4f`), ratio (`k/n`), lower-middle median for even counts, and `n/a`/`nil` when `PRsWithLock == 0`.
- `cmd/intentgate/stats_test.go`:
  - `TestStats_ArtifactsFindingsPerPR`:
    - Real `intentgate verify --out-dir` runs (or matching `audit.json` + `verify.json` with valid `verify_sha256`) across nested artifact subdirectories:
      - PR #1 (2 runs in window: earlier findings=3, later clean=0 → latest wins: 0 findings, clean).
      - PR #2 (in window: 2 footprint findings, 0 suspect locks).
      - PR #3 (in window: 0 footprint findings, 1 suspect lock, outcome=findings).
      - PR #4 (in window: 4 footprint findings, 1 suspect lock).
      - PR #5 (in window: `no-lock` → counted in `prs`, excluded from `prs_with_lock`).
      - PR #6 (in window: `skipped` bot → excluded completely).
      - PR #7 (outside window → excluded).
      - Local `verify` (`pr: 0`) and `lock` event in dir → ignored.
    - Assert exact JSON fields, key order, text lines, determinism across runs / `TZ` / `LC_ALL`, and zero wall-clock reads.
    - Empty artifacts dir (or `--since 1d` with no in-window PRs) → `n/a` rates and `null` median.
    - Error handling (exit 3): missing artifacts dir, path is a file not dir, corrupt `audit.json`, missing sibling `verify.json`, `verify_sha256` mismatch, outcome mismatch, symlinked `audit.json`, symlinked `verify.json`.
    - Usage error (exit 2): `--artifacts ""`.
    - Existing `TestStats_AmendmentRate`, `TestStats_Deterministic`, `TestStats_MergedPRCoverage` remain green unchanged.

## Verify

```bash
scripts/check.sh   # must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `internal/audit/**`
- `cmd/intentgate/**`
- `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`

## Forbidden

Everything else, including `claims/**`, `schema/**`, `docs/design/**`, `docs/adr/**`, `internal/verify/**`, `internal/report/**`, `internal/gitx/**`, `.req/**`, `baselines/**`, `scripts/**`. If a forbidden file must change, stop and report.

## Out of scope

- Fetching GitHub Actions artifacts over the network (remains offline; users download artifacts with `gh run download` or CI).
- Modifying `schema/intentgate.audit.v1.json` (kept backward-compatible with `v0.1.0`/`v0.2.0` artifacts).
- `eval/` Jev calibration harness and threshold tuning (next slice, S-29).

```intentgate
criteria:
  - text: "intentgate stats --artifacts <dir> computes offline, deterministic per-PR findings metrics from downloaded audit.json + verify.json artifacts (verifying verify_sha256, collapsing to latest run per PR, excluding skipped/no-lock from locked-PR rates) with pure aggregation in internal/audit"
    footprint: ["internal/audit/**", "cmd/intentgate/**", "docs/process/sdlc-map.yml", "docs/process/sdlc-map.md"]
```
