# S-19: `intentgate stats` counts merged PRs and lock coverage

**Base commit:** the commit that adds this spec   **Branch:** feature/s-19-stats-merged-prs
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

Phase 1 tuning, first signal (handoff open item 5): how many merged PRs the window holds and how many of them brought an intent lock. Git history answers it for repositories that merge PRs with merge commits (this one does), so no forge call is needed. Design: audit-metrics.md §2 "Merged PRs and lock coverage" (on `main`).

## Criteria

No requirement changes. REQ-0041's `intentgate stats` gains two counts and a ratio; its existing test must stay green unchanged.

## Read first

- `docs/design/audit-metrics.md` §2
- `cmd/intentgate/stats.go`, `cmd/intentgate/stats_test.go`, `internal/gitx/gitx.go` (`LockAdditions`, `CommitTime`, the scrubbed `run`)

## Choices fixed here

- `gitx.FirstParentMerges(dir, rev string) ([]Merge, error)`, `Merge{Commit string; Time time.Time}`: the commits on the first-parent history of `rev` with two or more parents (`git log --first-parent --merges --format=%H%x00%ct -z <rev>`), newest first; same scrubbed environment, pinned config and `checkRev` as `LockAdditions`.
- `stats`:
  - `merged_prs`: merges from `FirstParentMerges(HEAD)` whose committer time is inside the window (same bounds as locks: not before start, not after end).
  - `merged_prs_with_lock`: those of them that are the adding commit (`LockAddition.Commit`) of at least one lock counted in `locks` (valid, present at `HEAD`, inside the window). A lock committed straight to the first-parent line (not via a merge) counts in `locks` but in neither PR field.
  - `lock_coverage`: `merged_prs_with_lock / merged_prs` with 4 decimal places, `n/a` when `merged_prs` is 0; `lock_coverage_ratio`: `k/n`.
  - JSON keys, in this order, right after `amendment_ratio`: `merged_prs`, `merged_prs_with_lock`, `lock_coverage`, `lock_coverage_ratio`. Schema stays `intentgate.stats/1` (additive; no schema file exists).
  - Text: one line after the amendment-rate line: `merged PRs: <n>, with a lock: <k> (coverage <rate>)`, `<rate>` being `n/a` when `n` is 0.
  - Still no clock, still byte-identical for the same `HEAD`.
- Process map: the `stats` product line names the merged-PR coverage; re-render.

## Tests first (committed red)

- `TestGitx_FirstParentMerges`: two merges on main (one bringing a lock, one not), one merge on a side branch that is itself merged → the side branch's inner merge is not listed; a fast-forward-only history → empty; times are committer times.
- `TestStats_MergedPRCoverage` (temp repo with fixed commit dates): 3 merges in the window (2 bring a valid lock, 1 brings none), 1 merge before the window bringing a lock, 1 lock committed directly on main → `merged_prs` 3, `merged_prs_with_lock` 2, `lock_coverage` `0.6667`, ratio `2/3`, `locks` counts the direct one too; a merge whose only lock is invalid is not "with a lock"; `--since 1d` with no merges → `n/a`; the JSON key order and the text line exactly as above.
- `TestStats_Deterministic` and `TestStats_AmendmentRate` stay green without edits.

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one assertion failure in the report.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `cmd/intentgate/**`, `internal/gitx/**`
- `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`

## Forbidden

Everything else, including `claims/**` (no new `tests:` links: they wait for the next Baseline). If a forbidden file must change, stop and report.

## Out of scope

- Findings per PR (needs the CI artifacts).
- Squash- or rebase-merged PRs (need the forge).
- Jev thresholds and cross-kind dedupe.

```intentgate
criteria:
  - text: "intentgate stats reports merged PRs in the window, those that brought a valid lock, and the lock coverage ratio, from git history alone"
    footprint: ["cmd/intentgate/**", "internal/gitx/**", "docs/process/sdlc-map.yml", "docs/process/sdlc-map.md"]
```
