# Architecture decision records

One decision per file, numbered, never renumbered. A superseded ADR stays, with its status set to `Superseded by NNNN`.

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-go-product-python-evaluation.md) | Go for the product, Python for offline evaluation | Proposed |
| [0002](0002-lock-committed-in-pr-branch.md) | Commit the lock in the PR branch; keep the root intent local | Proposed |
| [0003](0003-record-ratification-method-not-enforce.md) | Record how a lock was ratified; enforce nothing in Phase 0 | Proposed |
| [0004](0004-req-through-cli-and-json.md) | Integrate with req through its CLI and JSON only | Proposed |
| [0005](0005-phase0-cli-and-action-app-later.md) | Phase 0 ships a CLI and a GitHub Action; the App comes in Phase 1.5 | Proposed |
| [0006](0006-committed-intent-as-root-intent.md) | A committed, owner-approved `intent.md` may be a lock's root intent (Phase 1, opt-in) | Proposed |
