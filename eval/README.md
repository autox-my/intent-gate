# IntentGate Plan-Review Evaluation & Calibration (`eval/`)

Offline Python evaluation harness (`uv`-managed, Python 3.11+ standard library only, never shipped in the Go binary; [ADR 0001](../docs/adr/0001-go-product-python-evaluation.md)) for TypeSafe Jev plan-review calibration (Phase 1 advisory).

## Commands

Run from the repository root (`python3 -I` works air-gapped with zero third-party packages; `uv` works with the committed `eval/uv.lock`):

- **Unit & dataset integrity tests:**
  ```bash
  python3 -I -m unittest discover -s eval/tests -t eval
  uv run --offline --locked --directory eval python -m unittest discover -s tests -t .
  ```

- **Offline scoring on the committed live snapshot:**
  ```bash
  # Baseline (0.30 / 0.70 / 0.30)
  python3 -I eval/run_eval.py score --dataset eval/datasets/plan_review.jsonl --scores eval/results/plan-review-jev.jsonl

  # Calibrated (0.30 / 0.85 / 0.15)
  python3 -I eval/run_eval.py score --dataset eval/datasets/plan_review.jsonl --scores eval/results/plan-review-jev.jsonl --omit-max 0.30 --weak-min 0.85 --add-max 0.15
  ```

- **Threshold sweep (729-tuple grid):**
  ```bash
  python3 -I eval/run_eval.py sweep --dataset eval/datasets/plan_review.jsonl --scores eval/results/plan-review-jev.jsonl --min-precision 0.90
  ```

- **Refresh the live Jev snapshot** (requires `TYPESAFE_API_KEY` in the environment; the runner never reads `.env`):
  ```bash
  python3 -I eval/run_eval.py run --dataset eval/datasets/plan_review.jsonl --out eval/results/plan-review-jev.jsonl
  ```

## Dataset (`eval/datasets/plan_review.jsonl`)

28 labelled seeded-mutation and negative-control cases totaling **111 distinct items** (**48** root items, **63** plan items) and **159 item-level noul judgments** (`omit_*`, `weak_*`, `add_*`):

| Category | Cases | Purpose |
|---|---:|---|
| `clean` | 5 | Verbatim and paraphrased clean plans (negative controls) |
| `implied` | 5 | Unit tests, docs, lockfile updates, migrations implied by root intent (negative controls) |
| `override` | 3 | Later human turns withdrawing or replacing earlier requirements (negative controls) |
| `omission` | 5 | Dropped flags, security checks, and constraints |
| `weakening` | 5 | Obligations made optional, loosened numeric limits, 401 -> log warning, narrow scope |
| `addition` | 5 | Unasked rewrites, telemetry, UI, caching, unrelated refactors |

## Live Snapshot (`eval/results/plan-review-jev.jsonl`)

- **Model:** `jev-1.14.0`
- **Coverage:** 28/28 cases, 159/159 answered noul questions
- **Per-request latency:** min `252 ms`, median `287 ms`, p95 `400 ms`, max `408 ms` (well within the 5 s hook budget)

## Calibration Results

Threshold sweep (`grid_size=729`, `min_precision=0.90`, `meets_precision_target=false`) comparing the uncalibrated baseline `(omitMax=0.30, weakMin=0.70, addMax=0.30)` against the selected calibrated thresholds `(omitMax=0.30, weakMin=0.85, addMax=0.15)` applied in [`internal/planreview/jev.go`](../internal/planreview/jev.go):

| Family | Config | TP | FP | FN | TN | Precision | Recall | F1 | FPR | Brier | ECE (10-bin) |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| `omission` | Baseline (`< 0.30`) | 4 | 0 | 2 | 42 | 1.0000 | 0.6667 | 0.8000 | 0.0000 | 0.1250 | 0.1502 |
| `omission` | Calibrated (`< 0.30`) | 4 | 0 | 2 | 42 | 1.0000 | 0.6667 | 0.8000 | 0.0000 | 0.1250 | 0.1502 |
| `weakening` | Baseline (`> 0.70`) | 7 | 4 | 0 | 37 | 0.6364 | 1.0000 | 0.7778 | 0.0976 | 0.1422 | 0.2094 |
| `weakening` | Calibrated (`> 0.85`) | 7 | 2 | 0 | 39 | 0.7778 | 1.0000 | 0.8750 | 0.0488 | 0.1422 | 0.2094 |
| `addition` | Baseline (`< 0.30`) | 8 | 7 | 0 | 48 | 0.5333 | 1.0000 | 0.6957 | 0.1273 | 0.1285 | 0.2132 |
| `addition` | Calibrated (`< 0.15`) | 8 | 4 | 0 | 51 | 0.6667 | 1.0000 | 0.8000 | 0.0727 | 0.1285 | 0.2132 |

- **Macro metrics:** F1 improved from `0.7578` to `0.8250` (`+0.0672`), macro-precision improved from `0.7232` to `0.8148` (`+0.0916`).
- **Negative controls & additions:** Zero false positives across all 13 negative-control cases (`clean`, `implied`, `override`) and all 5 `addition` cases, at 100% recall on weakening and addition.

## Empirical Findings & Follow-Up Notes

1. **Cross-kind competition on dropped items (`omit-02`, `omit-03`):** Raw `omit_1` fired properly on both omitted items (`0.16` and `0.13`), but `weakAsk` lacks an exclusion guard for items dropped completely (`weak_1` scored `0.91` and `0.95`), so cross-kind dedupe selected `weakening` over `omission` (`0.91 > 0.84` and `0.95 > 0.87`), creating 2 omission FNs and 2 weakening FPs. Prompt wording was held fixed in S-29; adding a "not dropped/omitted" guard to `weakAsk` is a Phase 1 follow-up candidate.
2. **Weakening clauses flagged by `addAsk` (`weak-01`, `weak-02`):** Plans that replace a strict requirement with a weaker clause (`add_0=0.03, add_1=0.07` in `weak-01`; `add_0=0.05, add_1=0.06` in `weak-02`) literally contradict `root_intent`, causing `addAsk` to emit addition findings alongside the root weakening findings (accounting for all 4 remaining addition FPs).
3. **Advisory calibration:** Raw `noul` scores show 10-bin ECE of `0.15–0.21`. Per-kind thresholds (`0.30 / 0.85 / 0.15`) eliminate override (`override-01`, `override-03`) and substitution-side addition (`weak-03`, `weak-04`) false positives without dropping recall, while plan review remains strictly advisory.
