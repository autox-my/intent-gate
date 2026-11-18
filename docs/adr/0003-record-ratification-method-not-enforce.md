# ADR 0003: Record how a lock was ratified; enforce nothing in Phase 0

**Status:** Proposed

## Context

The v2 review found that an agent with shell access could create its own lock. The adoption-first triage concluded that the realistic risk is a *helpful* agent doing it by accident, not a malicious one, and that friction at ratification causes rubber-stamping or abandonment.

## Decision

- Every lock records `ratification.method`: `hook` (the agent harness's own approval prompt), `cli-confirm` (interactive terminal confirmation) or `issue`.
- `intentgate lock` and `amend` refuse to run without a terminal and have no `--yes` flag. This prevents accidental agent-created locks.
- Phase 0 enforces no minimum method. The method is copied into every audit event (REQ-0042).

## Consequences

- A determined agent can still create a lock (for example through a pseudo-terminal). **IntentGate must not claim otherwise** to users or security reviewers until a later phase enforces stronger ratification.
- Because the method is recorded from day one, a later policy (for example "blocking paths accept only `hook` or web-session ratification") can be applied to existing data and reported on before it's enforced.

## Alternatives considered

- **Passkey or web-session ratification now:** the right control for the blocking and compliance tiers, too much friction for advisory adoption.
