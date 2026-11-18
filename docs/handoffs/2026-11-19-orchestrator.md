# Handoff: orchestrator thread, 2026-11-19

Phase 0 is built. Everything is in git; this file carries what git doesn't say.

## Where things stand

- All 12 slices (S-01 to S-12, `docs/specs/`) merged. Each went red-first test, green, path check, cross-family review, review fixes, human merge.
- `scripts/check.sh` is GREEN on `main` with `req gate` as a real step: 41 of 41 claims linked, every linked test passing.
- Coverage explorer (claims → design → slices → tests, as of 0fa5d6a): https://claude.ai/artifact/PmWLAnDgBSCF23P8sonEVg (private to the owner). Regenerate from `req trace --format json`, `req satisfy --format json` and `build/junit.xml` after a check run.
- Binary `intentgate`: `hook claude-code pre|post|print-config`, `lock --from-plan|--issue`, `amend`, `verify` (+ `--replay`), `publish`, `stats`. Action in `action/` (pull_request only).
- Plan review: Jev provider (`TYPESAFE_API_KEY`, direct `https://api.typesafe.ai/v1/systemone`), 5 s budget over the whole step, never blocks. Live check: `set -a; . ./.env; set +a; INTENTGATE_LIVE_JEV=1 go test ./internal/planreview -run TestJev_Live -v`. Jev isn't offered through opencode-go.

## Working rules learned (also in the orchestrator's memory)

- The orchestrator decides worker questions and review findings itself; it escalates only claim changes, Baseline signing, merges and genuine ambiguity.
- Disk is tight: remove each slice's worktree and merged branches (`feature/*`, `t3/*`) right after the merge; one worktree at a time.
- Review findings become a binding "Review 1 amendments" bullet in the spec, committed by the orchestrator on the branch, then a fix worker.

## Open items

1. **Hand check (human):** does the hook's `PreToolUse` `systemMessage` render before Claude Code's plan-approval prompt in the terminal UI? If not, use the fallback in ratification.md §2.
2. **Baseline (human only):** `req check` stays UNVERIFIABLE until a signed Baseline exists: `req baseline --at <ts> --out baselines/<name>.json`, then `minisign -Sm`. Then switch `scripts/check.sh` from `req gate` to `req check --test-report build/junit.xml`. Also consider relations / `.req/completeness` roots (every claim is "unrelated" today).
3. **Remote:** no GitHub remote yet, so the Action has never run for real. With a remote: CI running `scripts/check.sh` plus `req diff --base main --fail-on weakened,removed,reshaped`, and a first real PR through the Action. No GitHub secret is needed in Phase 0 (the plan review runs on the ratifier's machine).
4. **Release pipeline:** ADR 0005 wants a pinned, checksummed binary download; the Action builds from source until then.
5. **`intentgate init`:** prints the `intentgate` criteria-block snippet for agent instructions (ratification.md §1.1).
6. **Phase 1 tuning:** Jev plan-review thresholds and cross-kind dedupe (one dropped item flags omission and weakening); locks per merged PR and findings per PR in `stats`.
7. Upstream req issues autox-my/req#116, #117, #118 (from the first handoff) are still open.
