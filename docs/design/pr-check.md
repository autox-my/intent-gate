# Design: the PR check

<!-- req-satisfies: REQ-0021 -->
<!-- req-satisfies: REQ-0023 -->
<!-- req-satisfies: REQ-0032 -->
<!-- req-satisfies: REQ-0033 -->
<!-- req-satisfies: REQ-0034 -->
<!-- req-satisfies: REQ-0035 -->
<!-- req-satisfies: REQ-0036 -->
<!-- req-satisfies: REQ-0037 -->
<!-- req-satisfies: REQ-0038 -->
<!-- req-satisfies: REQ-0039 -->

In Phase 0 the PR check is a composite GitHub Action (ADR 0005) that runs `intentgate verify` and publishes the result. The decision logic is the pure `verify` core; the Action is only an adapter.

## 1. Order of steps

1. **Skip bots (REQ-0034).** If the PR author's type is `Bot`, or the login is in the skip list (default `dependabot[bot]`, `renovate[bot]`; configurable in `.intentgate.yml`), exit 0 without a check run.
2. **Discover locks** (§2).
3. **No lock:** publish the check run with conclusion `neutral` and the summary "No intent lock in this pull request". Post no comment (REQ-0033). Criteria are **never** derived from the PR title, body or commits (REQ-0032): there is no code path that reads them for that.
4. **Lifecycle checks** (§3) on each lock.
5. **Footprint check** (footprint-check.md).
6. **Policy:** map the outcome to a conclusion (§4).
7. **Publish:** one check run, SARIF, at most one comment, an audit event (§5–§6).

## 2. Lock discovery

A lock belongs to this PR when its file under `.intent/` is **added** in `base...head`. Locks already on the base branch belong to earlier work and are ignored. If a PR modifies a lock that exists on base, that's reported as a finding ("modifies an existing lock from another change") and the lock is treated as suspect.

## 3. Lifecycle checks (marking criteria suspect)

| Check | Rule | Claim |
|---|---|---|
| Hash | Recompute the hash (intent-lock.md §3). A mismatch means the file was edited outside `intentgate amend`: all criteria in that lock are suspect. | REQ-0023 |
| Retarget | The lock's `base_ref` differs from the PR's current base branch: the lock is suspect. Stateless: no event history needed. | REQ-0021 |
| Validity | Older than 14 days at the evaluation time: the lock is suspect. | REQ-0022 (intent-lock.md §5) |

**Why a hash check, not a diff check:** locks are committed in the PR branch (ADR 0002), so every PR with a lock adds a file under `.intent/`. Looking at the diff can't separate a new lock from an edited one. A hash mismatch does: it means the file was changed outside `intentgate amend`, which is the case REQ-0023 is meant to catch. (REQ-0023 was reworded to this on 2026-11-18.)

A suspect criterion is still footprint-checked. Being suspect changes how much the result can be trusted, not whether the check runs.

## 4. Conclusion (advisory mode, REQ-0039)

| Outcome | Conclusion | Comment |
|---|---|---|
| Locks valid, no findings | `success` | none (REQ-0038) |
| Footprint findings, or any suspect criterion | `neutral` | one, edited in place |
| No lock | `neutral` | none (REQ-0033) |
| Lock invalid or unreadable (UNVERIFIABLE) | `neutral` | one, naming the reason |
| IntentGate itself errors | `neutral` | none; the error goes in the check summary |

Phase 0 never concludes `failure` or `action_required`. The blocking-mode table from v2 §8.1 is implemented in Phase 1.5 behind `mode: blocking`, together with the availability work from the v2 review §2.3.

## 5. Outputs

- **One check run (REQ-0035)** named `IntentGate`, created through the Checks API with the Action's `GITHUB_TOKEN` (`checks: write`). The summary lists criteria, suspect markers and findings. A re-run updates the same check run for the same head SHA; it doesn't create a second one.
- **SARIF (REQ-0036):** each footprint finding becomes a SARIF result with rule `intentgate/outside-footprint`, location at the file (no line), and the fix command in the message. The Action uploads it with `github/codeql-action/upload-sarif`, so findings appear in code scanning and as PR annotations without a new dashboard.
- **At most one comment (REQ-0037):** the comment body starts with a hidden marker `<!-- intentgate:pr-comment -->`. The Action finds an existing comment with that marker and edits it; otherwise it creates one. If a later run has nothing to say, the existing comment is edited to a one-line "All criteria inside their footprints" rather than deleted, so the history stays readable.

## 6. Audit event

Every run writes one `intentgate.audit/1` JSON event (audit-metrics.md) to the check-run summary's details and as a workflow artifact.
