# IntentGate requirements: adoption source

This document is the **adoption source** for the req corpus in `claims/`. Each bullet under
*Phase 0 requirements* was drafted into one claim with `tools/adopt.py`. After adoption, **the
corpus is authoritative**: change a requirement by editing its claim, not this file.

Scope: Phase 0 of `docs/intent-gate-proposal-v2.md` §7, cut by the adoption-first triage in
`docs/intent-gate-v2-review-report.md` §0. Vocabulary: `docs/glossary.md`. Phase 0 is advisory: nothing here may block a merge.

## Phase 0 requirements

### Intent lock: creation

- The plan-approval hook shall create an intent lock when a human approves an agent plan.
- The lock command shall create an intent lock from an issue.
- The lock command shall create an intent lock from a plan file.
- The lock command shall create an intent lock only after an interactive human confirmation.

### Intent lock: contents

- The intent lock shall store the root intent verbatim.
- The intent lock shall contain no more than 8 criteria.
- Each criterion shall declare its footprint as path globs.
- The intent lock shall record the ratifier identity.
- The intent lock shall record the ratification time.
- The intent lock shall record the ratification method.
- The intent lock shall record the review duration of the ratification.
- The intent lock shall carry a hash over its root intent, criteria, footprints and ratification record.
- The committed lock file shall not contain the root intent.
- The lock command shall scan the root intent for secrets before it writes the intent lock.

### Plan review (plan vs prompt)

- The plan review shall report what the root intent asks for that the plan omits.
- The plan review shall report what the plan adds that the root intent does not ask for.
- The plan review shall report where the plan weakens an obligation of the root intent.
- The plan review shall complete within 5 s.
- The plan review shall not block ratification.

### Intent lock: lifecycle

- The PR check shall mark an intent lock suspect when the pull request base branch is retargeted.
- The PR check shall treat an intent lock as valid for no more than 14 days.
- The PR check shall mark the criteria suspect when the pull request diff modifies a file under `.intent/`.

### Footprint check

- The footprint check shall report each changed file that lies outside the footprint of every criterion.
- The footprint check shall treat a test file paired with a module inside a footprint as inside that footprint.
- The footprint check shall treat test fixtures and snapshots as inside the footprint.
- The footprint check shall treat lockfiles as inside the footprint.
- The footprint check shall treat generated and vendored files as inside the footprint.
- The footprint check shall read its implicit footprints from `.intentgate.yml`.
- The footprint check shall offer a one-step footprint amendment for each flagged file.
- The footprint check shall not call a model.

### PR check behaviour

- The PR check shall not derive criteria from the pull request description.
- The PR check shall post no comment on a pull request that has no intent lock.
- The PR check shall skip pull requests authored by dependency-update bots.
- The PR check shall publish its result as one check run.
- The PR check shall publish its footprint findings as SARIF.
- The PR check shall post no more than 1 comment per pull request.
- The PR check shall post no comment when every check passes.
- The PR check shall conclude its check run as success or neutral in advisory mode.
- The verify command shall produce identical output for identical inputs.

### Metrics

- The PR check shall record the footprint-amendment rate.
- The PR check shall record the ratification method of each intent lock in the audit log.
