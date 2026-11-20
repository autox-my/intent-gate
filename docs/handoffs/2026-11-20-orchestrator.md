# Handoff: orchestrator thread, 2026-11-20

Replaces the 2026-11-19 handoff. Everything else is in git.

## Where things stand

- Private repo `github.com/autox-my/intent-gate`, `main` at the S-18 merge. CI runs `scripts/check.sh`: Go checks, tests, the process-map check, `req check` and verification of the signed Baseline `baselines/phase0.json`.
- Slices since the last handoff: S-13 publish readiness (module path, LICENSE, CI); S-14 hook messages; S-15 process map in the repo (`docs/process/sdlc-map.yml` → `.md`, checked by the gate); S-16 `intentgate init`; S-17 `init` run on this repo, root intent scoped to the turns since the previous approved plan, `scripts/lock-slice.sh`; S-18 ratify card.
- This repo runs IntentGate on itself: `.claude/settings.json` has the hooks (`ExitPlanMode` and `AskUserQuestion`) and `defaultMode: plan`. S-16 was the first locked slice; its PR check concluded `success`.
- Hand-check results (ratification.md §2, §2.1): in the Claude Code terminal app, approving a plan writes the lock, and its messages show only after approval. In T3 Code, plan approval is captured by the client (and plan mode is gone), so `post` never fires there; the ratify card (S-18) is the T3 path.

## Locking a slice (human cost: one click + merge)

1. Orchestrator writes `docs/specs/S-NN-*.md` ending with an `intentgate` block and pushes it to `main` (specs and their amendments always go to `main`, never the slice branch: they are outside the footprint).
2. In T3: run `intentgate hook claude-code ratify-question docs/specs/S-NN-….md` and ask **exactly** that card with `AskUserQuestion`. The human clicks **Ratify**; the hook writes `.intent/<id>.yml`. In a Claude Code terminal session, presenting the spec as a plan in plan mode does the same. Fallback anywhere: `scripts/lock-slice.sh S-NN` (one command + `yes`).
3. Orchestrator commits the lock locally on top of `main` (don't push `main`), starts the worker from there, opens the PR, watches checks; human merges.

**First live card test still to do (new thread):** the hooks load at session start, so the S-18 card path has never run live. In the new thread, lock the next slice with the card; check `.intent/` for a new lock with `method: hook`. If none: inspect `~/.intentgate/pending/` (did `pre` run?) and the transcript's `AskUserQuestion` result shape.

## Waiting for the next Baseline signature (human)

Adding a `tests:` link to a claim changes what the Baseline attests, so links are batched for the next re-sign (`scripts/sign-baseline.sh`, new name):
- REQ-0002: `github.com/autox-my/intent-gate/cmd/intentgate::TestHook_RatifyCardWritesLock`

## Working rules (also in the orchestrator's memory)

- Decide worker and reviewer findings yourself; escalate only claim changes, Baseline signing, merges and real ambiguity. Keep summaries short.
- One worktree at a time; remove worktree and branches (local and remote) right after each merge. Disk is tight.
- Review findings become a binding "Review N amendments" bullet in the spec, pushed to `main`, then a fix worker on the rebased branch.
- Workers don't enter plan mode (AGENTS.md). Never auto-merge; never sign a Baseline.
- Use `watch_pull_request` for PR checks, not polling loops.

## Open items

1. Live ratify-card test (above).
2. Phase 1 ADR: a committed, product-owner-accepted `intent.md` as a lock source (from the AI-native SDLC playbook comparison; see `docs/process/sdlc-map.md`).
3. REQ-0043 default-mode nudge: only if plan mode + `init` + the card prove not enough.
4. Release pipeline (ADR 0005: pinned, checksummed binary download for the Action).
5. Phase 1 tuning: Jev thresholds, cross-kind dedupe, `stats` locks-per-PR and findings-per-PR.
6. Upstream req issues autox-my/req#116, #117, #118.
7. Jev key is not in T3's environment, so plan review shows "unavailable" in T3 sessions; export `TYPESAFE_API_KEY` in the environment T3 starts from if the review should run there.
