# S-29: Offline `eval/` Jev plan-review calibration harness and threshold tuning

**Base commit:** the commit that adds this spec   **Branch:** feature/s-29-eval-jev-calibration
**Orchestrator / Executor:** antigravity / gemini-pro-orchestrator-neon (Neon planner, Hydrogen executor)   **Reviewer:** antigravity / claude-sonnet-5-5 (cross-family)

Phase 1 tuning, third piece (ADR 0001, proposal v2 §9.4): stand up the offline Python (`uv`-managed, stdlib-only, never shipped in the Go binary) evaluation and calibration harness under `eval/`, score Jev's plan-review questions against a labelled ~100-item seeded-mutation corpus (incorporating S-27's cross-kind dedupe), commit the reproducible score snapshot and calibration metrics (precision, recall, F1, FPR, Brier score, ECE), and set empirically calibrated per-kind thresholds (`omitMax`, `weakMin`, `addMax`) in `internal/planreview/jev.go`. Design: `docs/design/plan-review.md` §1–§2 (on `main`).

## Criteria

No new claims and no Baseline changes. Existing linked tests on `REQ-0016`, `REQ-0017`, `REQ-0018`, `REQ-0019`, `REQ-0020` (`TestRun_ReportsEachKind`, `TestDisplay_EachKind`, `TestJev_MapsAnswersToFindings`, `TestRun_Budget`, etc.) must stay green (preserving exact test names so `baselines/phase0-5.json` remains valid); do not edit `claims/**`.

## Read first

- `docs/adr/0001-go-product-python-evaluation.md`
- `docs/design/plan-review.md` (§1, §2)
- `docs/design/README.md` (§2: `eval/` is offline Python managed with `uv`, never shipped)
- `internal/planreview/planreview.go` (`normalizeQuote`, `kindRank`, `outranks`, `dedupeCrossKind`)
- `internal/planreview/jev.go` (`Items`, `head`, `tail`, `omitAsk`, `weakAsk`, `addAsk`, `lowNoul`, `highNoul`, `Review`)
- `internal/planreview/jev_test.go` (`TestJev_MapsAnswersToFindings`, `TestJev_Live`)
- `scripts/check.sh`, `docs/process/sdlc-map.yml`

## Choices fixed here

- **Python package under `eval/` (ADR 0001):**
  - `eval/pyproject.toml`: managed with `uv`, `requires-python = ">=3.11"`, `dependencies = []` (Python standard library only, so unit tests and offline scoring run air-gapped with both `uv run` and `python3 -I`).
  - `eval/intentgate_eval/items.py`: deterministic port of Go's `Items(text, max)`, `head(s, max_bytes)`, `tail(s, max_bytes)` (UTF-8 byte boundaries), and `build_jev_request(root_intent, plan, model)` using the exact question templates and truncation flags from `internal/planreview/jev.go`.
  - `eval/intentgate_eval/dedupe.py`: exact port of `normalizeQuote`, `kindRank`, `outranks`, `dedupeCrossKind` from `internal/planreview/planreview.go`, plus `map_answers(root_items, plan_items, answers, omit_max, weak_min, add_max)` applying the thresholds and cross-kind dedupe identically to `Jev.Review`.
  - `eval/intentgate_eval/metrics.py`:
    - Pure offline metrics per finding family (`omission`, `weakening`, `addition`):
      - Raw per-question calibration: `brier_score` and 10-bin `ece` (Expected Calibration Error), using `P(omission) = 1 - noul`, `P(weakening) = noul`, `P(addition) = 1 - noul`.
      - Post-dedupe classification metrics at `(omit_max, weak_min, add_max)`: `tp`, `fp`, `fn`, `tn`, `precision`, `recall`, `f1`, `fpr` (matching each predicted finding against item-level ground-truth labels on normalized quote + kind).
    - Deterministic threshold sweep (`sweep_thresholds`): grid over `omit_max` ∈ `[0.15, 0.20, ..., 0.55]`, `weak_min` ∈ `[0.45, 0.50, ..., 0.85]`, `add_max` ∈ `[0.15, 0.20, ..., 0.55]` (9 values per axis, 729 tuples). Selects the best `(omit_max, weak_min, add_max)` that achieves per-family precision ≥ `min_precision` (default `0.90`) on every family with positives, maximizing macro-F1 (tie-break: higher macro-precision, then smallest L1 distance to baseline `(0.30, 0.70, 0.30)`, then lexicographical). If no tuple meets `min_precision` on all three families, falls back to maximizing per-family feasible precision then macro-F1 and flags `meets_precision_target: false`.
  - `eval/intentgate_eval/runner.py`:
    - Live TypeSafe Jev client (`POST <base>/v1/systemone`, `Authorization: Bearer <key>`).
    - Same transport safety as `internal/planreview/jev.go`: refuses non-HTTPS URLs except loopback (`127.0.0.1`, `::1`, `localhost`), refuses URL userinfo/query/fragment, refuses redirects, caps response body at 1 MiB, scrubs API key from all errors, never reads `.env` itself (caller exports `TYPESAFE_API_KEY`).
    - Reads dataset JSONL, queries Jev per case, writes deterministic score JSONL (`case_id`, `model`, `latency_ms`, `root_items`, `plan_items`, `answers` with numeric `noul` values only — no secrets).
  - `eval/intentgate_eval/cli.py` + `eval/intentgate_eval/__main__.py`:
    - Subcommands: `run`, `score`, `sweep` (`--format text|json`, deterministic output).
  - Keep cyclomatic complexity of every Python and Go function ≤ 10.
- **Labelled dataset (`eval/datasets/plan_review.jsonl`):**
  - 28 realistic cases (~100+ item-level ground-truth labels) covering:
    - Clean plans (verbatim & paraphrased)
    - Implied work negative controls (unit tests, docs, lockfile updates, migrations → NOT additions)
    - Later human override negative controls (later turn drops/replaces earlier request → NOT omissions)
    - Seeded omissions (dropped flags, dropped security checks, dropped constraints)
    - Seeded weakenings (`must` → optional, tightened → loosened numeric limits, 401 → warning log, universal → narrow scope)
    - Seeded additions (unasked rewrites, telemetry/analytics, UI, caching, unrelated refactors)
    - Mixed multi-defect plans (e.g. omission + addition, weakening + implied tests)
  - Every case carries explicit item-level expected findings (`expected_omissions`, `expected_weakenings`, `expected_additions` matching exact `Items()` outputs so dataset validation proves every expected quote actually appears in `root_items` or `plan_items`!).
- **Empirical live calibration & Go threshold tuning (`internal/planreview/jev.go`):**
  - Run `eval` live once against TypeSafe Jev (`set -a; . /Users/fd/Projects/intent-gate/.env; set +a; ...`, never printing the key) and commit the resulting numeric score snapshot to `eval/results/plan-review-jev.jsonl`.
  - Run `eval` `score` (baseline `0.30 / 0.70 / 0.30`) and `sweep` (`--min-precision 0.90`) on `eval/results/plan-review-jev.jsonl`.
  - In `internal/planreview/jev.go`, replace the shared `lowNoul` / `highNoul` constants with explicit per-kind constants `omitMax`, `weakMin`, `addMax` set to the sweep-selected thresholds (keeping `(0.30, 0.70, 0.30)` for any axis where the sweep does not strictly improve macro-F1/precision).
  - In `internal/planreview/jev_test.go`, update `TestJev_MapsAnswersToFindings` boundary cases to reference `omitMax`, `weakMin`, `addMax` (preserving exact test name and testing strictly-below / at-boundary / strictly-above behavior for all three kinds!).
  - Document the exact calibration numbers (model version, dataset counts, Brier, ECE, baseline vs tuned precision/recall/F1, and selected thresholds) in `eval/README.md`.
- **Gate & process map:**
  - `scripts/check.sh`: add `step "eval tests" python3 -I -m unittest discover -s eval/tests -t eval` inside the `if need python3;` block (hermetic, offline, < 1 s).
  - `docs/process/sdlc-map.yml`: include `eval/pyproject.toml` and `eval/datasets/plan_review.jsonl` in the `closing the loop` (or `TEST` entry) `paths:` and mention the `eval/` calibration harness in its detail/build text, then re-render `docs/process/sdlc-map.md` with `python3 -I scripts/sdlc-map.py`.

## Tests first (committed red)

Commit red tests first (failing on assertions, with only minimal stubs to import/compile):

- `eval/tests/test_eval.py`:
  - `TestItemsParity`: bullets (`-`, `*`, `+`), numbered (`1.`, `1)`), sentence splits, `<3` word drop, last-wins dedupe, caps (last 20 root, first 20 plan), UTF-8 multi-byte `head`/`tail` truncation boundaries, `build_jev_request` shape & prompt-injection guard (item text never in `instructions`).
  - `TestDedupeParity`: confidence wins, ties (`Omission > Weakening > Addition`), case/whitespace quote normalization, empty quotes unmerged, `NaN` loses to finite, same-kind preservation, idempotence, exact `map_answers` threshold boundary behavior (`< omit_max`, `> weak_min`, `< add_max`).
  - `TestMetricsAndSweep`: known-answer Brier score, 10-bin ECE, TP/FP/FN/TN, precision/recall/F1/FPR, and deterministic `sweep_thresholds` selecting the precision-constrained macro-F1 optimum (with baseline tie-breaking and fallback flag when no tuple reaches `min_precision`).
  - `TestRunnerTransport`: loopback `http.server` fake Jev server verifying headers/payload/JSONL output, refusing non-loopback `http://`, refusing userinfo/query/fragment, refusing redirects, refusing >1 MiB body, scrubbing API key from error text.
  - `TestDatasetIntegrity`: validates every row in `eval/datasets/plan_review.jsonl` (unique `id`, non-empty `root_items` and `plan_items`, every `expected_omissions`/`expected_weakenings` quote exists in `root_items`, every `expected_additions` quote exists in `plan_items`, no quote listed in both `expected_omissions` and `expected_weakenings`, total labelled item-judgments ≥ 90).
- `internal/planreview/jev_test.go`:
  - `TestJev_PerKindThresholds`: verifies `omitMax`, `weakMin`, `addMax` are distinct per-kind constants in `(0, 1)` and that values strictly past each threshold trigger a finding while values exactly at each threshold do not.

## Verify

```bash
scripts/check.sh   # must end "verdict: GREEN" (includes Go checks, eval tests, process map, req baseline)
```

## Footprint (allowed paths)

- `eval/**`
- `internal/planreview/**`
- `scripts/check.sh`
- `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`

## Forbidden

Everything else, including `claims/**`, `cmd/**`, `schema/**`, `docs/design/**`, `docs/adr/**`, `.req/**`, `baselines/**`, `.env`. If a forbidden file must change, stop and report.

## Out of scope

- CI-time Jev semantic claim verification against git diffs (Phase 1 later slice).
- Prompt wording rewrites (kept fixed to measure and calibrate the shipped prompts first).
- Human multi-rater labeling of 300+ PRs (public calibration bar, proposal v2 §9.4).

```intentgate
criteria:
  - text: "Offline uv-managed Python eval/ harness scores and sweeps Jev plan-review thresholds (precision, recall, F1, Brier, ECE with Go-parity item splitting and cross-kind dedupe) on a labelled seeded-mutation corpus, and internal/planreview/jev.go uses the empirically calibrated per-kind thresholds"
    footprint: ["eval/**", "internal/planreview/**", "scripts/check.sh", "docs/process/sdlc-map.yml", "docs/process/sdlc-map.md"]
```
