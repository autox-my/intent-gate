# S-14: hook messages match what Claude Code shows

**Base commit:** the commit that adds this spec   **Branch:** feature/s-14-hook-messages
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** none (wording only; the orchestrator checks it)

The 2026-11-19 hand check (ratification.md §2) found that Claude Code shows the `pre` hook's message only after the human answers the plan prompt. Its wording, "IntentGate will lock these criteria if you approve", is then wrong. Make both messages true for the order the human actually sees them in.

## Criteria

No requirement changes; REQ-0002, REQ-0016–0020 links stay as they are.

## Choices fixed here

- `pre` message starts `IntentGate criteria for this plan:` (then the criteria and `Display(res)` as today). A drafting error keeps `IntentGate: no lock: …`.
- `post` message: `IntentGate: intent lock written: .intent/<id>.yml (plan review: <status>; <o> omissions, <a> additions, <w> weakenings)` when the lock has a `plan_review`, else exactly as today.
- Update the tests that assert the old wording; add one assertion for the `post` suffix. No other behaviour changes.

## Verify

`scripts/check.sh` from the worktree root ends `verdict: GREEN`.

## Footprint (allowed paths)

- `cmd/intentgate/hook.go`, `cmd/intentgate/*_test.go`

## Forbidden

Everything else. If a forbidden file must change, stop and report.
