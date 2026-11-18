# S-11: plan review core (provider interface, budget, wiring)

**Base commit:** the commit that adds this spec   **Branch:** feature/s-11-plan-review-core
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The plan review without a real model: the provider interface, the 5-second budget, the display rules, the `plan_review` record in the lock, and its wiring into the hook and `intentgate lock`. The only providers here are `unavailable` and test fakes; the Jev provider is S-12.

## Criteria (claims this slice must satisfy)

- REQ-0016, REQ-0017, REQ-0018: the review reports omissions, additions and weakenings that the provider finds, to the human, at ratification. (The pipeline is proven here with a fake provider; the quality of the judgments is the Jev provider's, S-12.)
- REQ-0019: the review returns within 5 s, whatever the provider does.
- REQ-0020: no review status or provider behaviour stops or delays the lock beyond the budget.

## Read first

- `docs/design/plan-review.md` (all; §4–§5 updated in 2c5f0ef), `docs/design/ratification.md` §1, §2, §3
- `cmd/intentgate/hook.go`, `cmd/intentgate/lock.go`, `internal/ratify`, `internal/lock`

## Choices fixed here

- `internal/planreview`:
  - `type Kind string` (`omission`, `addition`, `weakening`); `Finding{Kind Kind; Quote, Text string; Confidence float64}`; `Status` `complete | partial | unavailable | error`.
  - `type Reviewer interface { Review(ctx context.Context, rootIntent, plan string) ([]Finding, error) }`. Contract: on `ctx` done, return the findings gathered so far with `ctx.Err()`.
  - `Unavailable{}`: no findings, status `unavailable`.
  - `Run(ctx, r Reviewer, rootIntent, plan string, budget time.Duration) Result` (`budget` = 5 s in production): calls `r.Review` in a goroutine with a context deadline of `budget − 250 ms`, and returns no later than `budget` after the call even if the provider ignores its context (the goroutine is abandoned; its late result is discarded). Status: no error → `complete`; `ctx` deadline with ≥ 1 finding → `partial`, with none → `unavailable`; another error → `error` (findings kept if any); provider `Unavailable` → `unavailable`; a provider panic is recovered → `error`. `Result{Status; Findings []Finding; Counts per kind}`.
  - `Display(res Result) string`: per kind at most 5 findings ordered by confidence (ties by quote, then text), each with its quote; "+N more" for the rest; a status line ("Plan review: complete" / "partial: the 5 s budget ran out" / "unavailable" / "error: …"). Pure.
  - `Select(getenv func(string) string) Reviewer`: `INTENTGATE_PLAN_REVIEW=off` → `Unavailable`; otherwise, in this slice, `Unavailable` (S-12 adds Jev). Tests inject reviewers directly.
- Lock: optional `plan_review: {status, omissions, additions, weakenings}` (status enum, integers ≥ 0, all four required when present, unknown keys rejected, counted in the hash, schema updated). No finding text ever goes into the lock, the pending record, the root-intent record or an audit event.
- `ratify.BuildInput` gains an optional plan-review record that `Build` copies into the lock.
- Wiring (both review the **masked** root intent and the **masked** plan):
  - Hook `pre`: now reads the transcript too, masks it, runs the review, and puts `Display(res)` in the `systemMessage` after the criteria. The pending record also stores `{status, omissions, additions, weakenings}` (no text). A transcript or review failure never stops the criteria message: it shows as review status `error` or `unavailable`.
  - Hook `post`: copies the pending record's plan-review record into the lock (none if the pending record predates this slice).
  - `intentgate lock` (`--from-plan`, `--issue`): runs the review after drafting and before the `yes` prompt, prints `Display(res)`, and records it in the lock. `review_duration_ms` still starts at the prompt, after the review.
  - `amend` doesn't run a review and keeps the lock's existing `plan_review` unchanged.
- Dependencies: none new.

## Tests first (committed red)

- `TestRun_ReportsEachKind` (planreview, fake provider returning one finding of each kind): result has all three, counts right; `TestDisplay_EachKind`: the display shows each kind with its quote. (REQ-0016, REQ-0017, REQ-0018)
- `TestRun_Budget` (REQ-0019): a provider that ignores its context and sleeps 30 s → `Run` returns within budget + 100 ms (use a 500 ms test budget and also one run with the real 5 s budget), status `unavailable`; one that returns 2 findings then honours the deadline → `partial` with both.
- `TestRun_Statuses`: complete, error (findings kept), panic → error, `Unavailable` → unavailable.
- `TestDisplay_Caps`: 8 omissions → 5 shown in confidence order + "+3 more".
- `TestHook_PlanReviewNeverBlocks` (cmd, REQ-0020): for each provider behaviour (complete with findings, partial, hang past the budget, error, panic, unavailable), `pre` then `post` writes the lock, the `pre` message contains the criteria, `pre` returns within budget + 1 s, and the lock's `plan_review.status` matches.
- `TestLock_PlanReviewShownBeforePrompt`: `lock --from-plan` prints the findings before "Type yes"; the lock records status and counts; no finding text in the lock file, the root intent record or the audit log.
- `TestLock_PlanReviewSchema` (internal/lock): `plan_review` validation (bad status, negative count, unknown key, partial record), hash changes with it, S-01 golden unchanged without it.

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add `tests:` (ids exactly as in `build/junit.xml`): REQ-0016, REQ-0017, REQ-0018 each `TestRun_ReportsEachKind` and `TestDisplay_EachKind`; REQ-0019 `TestRun_Budget`; REQ-0020 `TestHook_PlanReviewNeverBlocks`.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN"
```

`req gate` should now find every claim linked; if it still exits 3, report why from `build/check/req-gate.txt`.

## Footprint (allowed paths)

- `internal/planreview/**`, `cmd/intentgate/**`
- `internal/lock/**`, `internal/ratify/**` (only the `plan_review` record and its tests)
- `schema/intentgate.lock.v1.json`
- `claims/REQ-0016.yml` … `claims/REQ-0020.yml`: the `tests:` key only.

## Forbidden

Everything else, including `internal/verify/**`, `internal/report/**`, `internal/audit/**`, `scripts/**`, `docs/**`, `.req/**`. If a forbidden file must change, stop and report.

## Out of scope

- The Jev provider and its credentials (S-12).
- Switching `scripts/check.sh` from report-only `req gate` to the real gate (orchestrator, after this merges).
