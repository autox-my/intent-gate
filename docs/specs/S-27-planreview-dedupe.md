# S-27: Plan-review cross-kind deduplication

**Base commit:** the commit that adds this spec   **Branch:** feature/s-27-planreview-dedupe
**Worker:** antigravity / claude-sonnet-5-5   **Reviewer:** antigravity / gemini-4-neon (cross-family)

Eliminates duplicate cross-kind findings in `internal/planreview` (Phase 1 tuning, deferred from S-12). In live Jev runs, a dropped requirement triggers both `omission` (e.g. 0.95) and `weakening` (e.g. 0.85), while a weakened requirement triggers both `weakening` (e.g. 0.99) and `omission` (e.g. 0.91). Keeping the highest-confidence kind per normalized quote resolves both cases accurately, keeps `Result.Counts` and `Display` clean, and works uniformly for Jev and any future `Reviewer`. Design: `docs/design/plan-review.md` §1 (on `main`).

## Criteria

No new claims and no Baseline changes. Existing linked tests on `REQ-0016`, `REQ-0017`, `REQ-0018` (`TestRun_ReportsEachKind`, `TestDisplay_EachKind`, `TestJev_MapsAnswersToFindings`) must stay green unchanged; do not edit `claims/**` (adding `tests:` links waits for Baseline `phase0-6`).

## Read first

- `docs/design/plan-review.md` §1, §2, §4
- `internal/planreview/planreview.go`, `internal/planreview/planreview_test.go`
- `internal/planreview/jev.go`, `internal/planreview/jev_test.go`

## Choices fixed here

- **Quote normalization (`normalizeQuote(q string) string` in `internal/planreview/planreview.go`):**
  - `strings.ToLower(strings.Join(strings.Fields(q), " "))`.
  - Empty normalized quotes (`""`, including whitespace-only quotes) are never merged with each other.
- **Cross-kind deduplication (`dedupeCrossKind(in []Finding) []Finding` in `internal/planreview/planreview.go`):**
  - Pure, deterministic, idempotent, and order-independent when selecting the winning kind.
  - For each distinct non-empty normalized quote `k`, select the single winning `Kind` across all findings with normalized quote `k` using a strict weak ordering on `(Confidence, Kind)`:
    1. Finite `Confidence` always beats `NaN`.
    2. Higher finite `Confidence` wins.
    3. On equal `Confidence` (or both `NaN`), kind precedence breaks ties: `Omission` (0) beats `Weakening` (1), which beats `Addition` (2).
  - Keep every finding whose normalized quote is `""` OR whose `Kind` equals the winning `Kind` for its normalized quote, preserving original input order. (Multiple distinct findings of the same winning kind on the same quote are preserved.)
  - Keep cyclomatic complexity of every function ≤ 10.
- **Where it runs:**
  - `classify` in `internal/planreview/planreview.go` runs `dedupeCrossKind(valid(o.findings))` before `count(res.Findings)` so `Run` deduplicates findings and `Counts` for every `Reviewer` across `Complete`, `Partial`, and `ErrorStatus` (with findings). Note: `valid` runs first so an unknown kind can never beat a valid kind.
  - `Jev.Review` in `internal/planreview/jev.go` also runs `dedupeCrossKind(out)` before returning so direct callers (including `TestJev_Live`) receive deduplicated findings without needing `Run`.
- **Unchanged:**
  - `Display` remains a pure renderer of `Result` (`TestDisplay_TiesAreOrderedByQuoteThenText` must keep passing unchanged).
  - `lowNoul = 0.3` and `highNoul = 0.7` thresholds in `internal/planreview/jev.go` stay unchanged in this slice (threshold calibration waits for labelled data).
  - All existing exported types, functions, and method signatures stay unchanged.
  - No new dependencies.

## Tests first (committed red)

Commit red tests first (failing on assertions, not compile errors):

- `internal/planreview/planreview_test.go`:
  - `TestRun_DedupeCrossKind`:
    - Omission (0.95) vs Weakening (0.85) on same quote → keeps Omission only, `Counts{Omissions: 1}` (tested in both input orders).
    - Weakening (0.99) vs Omission (0.91) on same quote → keeps Weakening only, `Counts{Weakenings: 1}` (tested in both input orders).
    - Exact confidence ties: Omission vs Weakening → Omission; Weakening vs Addition → Weakening; Omission vs Addition → Omission (tested in both input orders).
    - Whitespace and case normalization: `"  Add  a --quiet flag. "` vs `"add a --quiet flag."` collapse to the winning kind.
    - Same-kind multiple findings on the same quote are preserved when their kind wins (e.g., two `Weakening` at 0.99 and 0.70 + one `Omission` at 0.90 → both `Weakening` kept, `Omission` dropped, `Counts{Weakenings: 2}`).
    - Empty and whitespace-only quotes (`""`, `"   "`) are never merged across kinds.
    - `NaN` confidence loses to finite confidence (including `0.0`); two `NaN`s break by kind precedence (`Omission` > `Weakening` > `Addition`).
    - `Partial` (deadline with findings) and `ErrorStatus` (error with findings) also deduplicate before computing `Counts`.
    - Invalid/unknown kind with high confidence (1.0) is filtered out before dedupe and never suppresses a valid kind with lower confidence (0.5).
- `internal/planreview/jev_test.go`:
  - `TestJev_DedupeCrossKind`: fake loopback Jev server returning:
    - `r0`: `omit_0: 0.05` (omission 0.95) + `weak_0: 0.85` (weakening 0.85) → only `Omission` (0.95) for `r0`.
    - `r1`: `omit_1: 0.09` (omission 0.91) + `weak_1: 0.99` (weakening 0.99) → only `Weakening` (0.99) for `r1`.
    - `r2`: `omit_2: 0.10` (omission 0.90) + `weak_2: 0.90` (weakening 0.90) → tie breaks to `Omission` (0.90) for `r2`.
    - `p1`: `add_1: 0.05` (addition 0.95) on a distinct plan item → `Addition` (0.95) preserved.
    - Verify both direct `f.jev().Review(...)` and `Run(..., f.jev(), ...)` return the exact 4 findings with `Counts{Omissions: 2, Additions: 1, Weakenings: 1}`.

(The live `TestJev_Live` spot-check uses the gitignored `.env` on the main workspace and is run by the orchestrator; it skips automatically in worktrees without `INTENTGATE_LIVE_JEV=1`.)

## Verify

```bash
scripts/check.sh   # must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `internal/planreview/**`

## Forbidden

Everything else, including `cmd/**`, `claims/**`, `docs/**`, `.req/**`, `baselines/**`, `schema/**`, `scripts/**`, `.env`. If a forbidden file must change, stop and report.

## Out of scope

- Tuning Jev thresholds (`lowNoul`, `highNoul`) or question wording (waits for `eval/` labelled data).
- `intentgate stats` findings per PR from CI artifacts.
- Linking new tests to `claims/REQ-0016..0018.yml` (waits for Baseline `phase0-6`).

```intentgate
criteria:
  - text: "Plan review merges cross-kind duplicate findings on the same normalized quote by keeping the highest-confidence kind (omission > weakening > addition on ties) in both Run and Jev.Review before computing counts"
    footprint: ["internal/planreview/**"]
```
