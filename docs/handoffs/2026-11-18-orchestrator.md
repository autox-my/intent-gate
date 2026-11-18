# Handoff: orchestrator thread, 2026-11-18

The previous orchestrator thread ran in the wrong T3 project (sys-one), so worker worktrees were impossible. This file carries its state; everything else is in git.

## Where things stand

- Requirements: 41 Phase 0 claims in `claims/` (req 0.6.0), bounds pinned on REQ-0007, 0019, 0022, 0037. `req canon --fail-on-flag` → 0.
- Design: `docs/design/` (7 elements), every claim linked once, all 41 links recorded by the human (`req satisfy` → PASS).
- ADRs 0001–0005 in `docs/adr/` (status Proposed): Go product + Python eval, lock committed in PR branch, ratification recorded not enforced, req via CLI/JSON, CLI + Action before App.
- Vocabulary: `docs/glossary.md` (criterion, footprint, PR check, plan review; "claim/scope/gate/pin/diff" are req's words).
- Process: `AGENTS.md` "How work flows (SDLC)"; req's own process studied in `docs/process/req-sdlc-study.md`.
- Gate: `scripts/check.sh` (gofmt, go vet, staticcheck, gotestsum JUnit, req canon/satisfy; req gate report-only). Currently RED only because there is no Go code yet ("0 tests").
- Next slice: `docs/specs/S-01-lock-core.md`, committed, **not started**.

## Roles (resolved from orchestrator_capabilities)

- Worker: `delegate_task`, `isolation: "worktree"`, provider `claudeAgent`, model `claude-sonnet-5-5`.
- Reviewer: `delegate_task`, read-only, provider `opencode`, model `opencode-go/deepseek-v4.1-flash`.
- The orchestrator keeps the main thread small: specs, branch checks (`git diff <base>...<branch> --name-only` against the spec's footprint), summaries. The human merges.

## Open items

1. Start the S-01 worker, then check its branch's paths, then run the reviewer, then hand to the human for merge.
2. Verify the Claude Code hook mechanics before S-03 (ratification.md §2).
3. Check open-code-review's license and importability (ADR 0001).
4. Upstream req issues filed while dogfooding: autox-my/req#116, #117, #118. File new ones when found.
5. No remote yet; CI (`scripts/check.sh` + `req diff --fail-on weakened,removed,reshaped`) waits for one.
