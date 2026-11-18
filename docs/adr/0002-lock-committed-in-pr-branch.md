# ADR 0002: Commit the lock in the PR branch; keep the root intent local

**Status:** Proposed

## Context

The PR check needs the criteria and footprints. REQ-0006 needs the root intent stored verbatim, and REQ-0014 forbids the committed lock from containing it. Phase 0 has no server. The v2 review discussed server-side storage and signed locks for the blocking tier.

## Decision

- The lock is committed as `.intent/<id>.yml` **in the PR branch**, normally in the first commit after ratification.
- The root intent is stored **on the ratifier's machine** (`~/.intentgate/locks/<id>/root-intent.md`). The committed lock carries only its SHA-256, which the lock's hash covers.
- Tamper detection in Phase 0 is the **hash self-check**: a lock edited without `intentgate amend` doesn't match its hash and is marked suspect.

## Consequences

- Locks are visible in code review and travel with the branch. No infrastructure is needed.
- An agent that edits a lock *and* recomputes the hash isn't caught. That's accepted for the advisory tier (v2 review §0: agents aren't adversaries). The blocking tier adds a server-side canonical copy or signed locks.
- Reviewers other than the ratifier can't read the root intent in Phase 0.
- REQ-0023 originally marked criteria suspect whenever the PR diff touched `.intent/`, which would have flagged every lock. It was reworded on 2026-11-18 to the hash check.

## Alternatives considered

- **A separate git ref (`refs/intentgate/*`):** keeps locks out of the diff, but needs push and fetch configuration on every machine and in CI, and is invisible in review.
- **Issue comments as storage:** ties Phase 0 to GitHub and makes CLI-only use awkward.
- **Server-side store:** right for the blocking tier, too much for Phase 0.
