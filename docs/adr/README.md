# Architecture decision records

One decision per file, numbered, never renumbered.

## Status

Every ADR has one `**Status:**` line with one of three values (a note in parentheses may follow):

- **Proposed**: on `main` for discussion, not yet ratified. Code, designs and specs must not depend on it.
- **Accepted**: ratified by the human (the product owner). Binding: code, designs and specs may depend on it. An agent never sets this; it asks.
- **Superseded by NNNN**: replaced by a later ADR, which must exist. The file stays, unchanged except for this line.

The index below must agree with each file's status. `scripts/check.sh` checks both (`scripts/adr_status.py`).

## Index

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-go-product-python-evaluation.md) | Go for the product, Python for offline evaluation | Accepted |
| [0002](0002-lock-committed-in-pr-branch.md) | Commit the lock in the PR branch; keep the root intent local | Accepted |
| [0003](0003-record-ratification-method-not-enforce.md) | Record how a lock was ratified; enforce nothing in Phase 0 | Accepted |
| [0004](0004-req-through-cli-and-json.md) | Integrate with req through its CLI and JSON only | Accepted |
| [0005](0005-phase0-cli-and-action-app-later.md) | Phase 0 ships a CLI and a GitHub Action; the App comes in Phase 1.5 | Accepted |
| [0006](0006-committed-intent-as-root-intent.md) | A committed, owner-approved `intent.md` may be a lock's root intent (Phase 1, opt-in) | Accepted |
