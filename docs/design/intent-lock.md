# Design: the intent lock

<!-- req-satisfies: REQ-0006 -->
<!-- req-satisfies: REQ-0007 -->
<!-- req-satisfies: REQ-0008 -->
<!-- req-satisfies: REQ-0009 -->
<!-- req-satisfies: REQ-0010 -->
<!-- req-satisfies: REQ-0011 -->
<!-- req-satisfies: REQ-0012 -->
<!-- req-satisfies: REQ-0014 -->
<!-- req-satisfies: REQ-0022 -->

The intent lock is IntentGate's public contract. Agent hooks, the CLI, the PR check and later integrations all read and write it, so its schema is versioned, and published as `schema/intentgate.lock.v1.json`.

## 1. Two artifacts, one hash

REQ-0006 says the lock stores the root intent verbatim, and REQ-0014 says the committed file must not contain it. The design meets both by splitting the lock:

| Artifact | Where | Contains |
|---|---|---|
| **Committed lock** | `.intent/<id>.yml` in the PR branch | everything except the root intent text, plus `root_intent.sha256` |
| **Root intent record** | `~/.intentgate/locks/<id>/root-intent.md` on the ratifier's machine | the root intent verbatim (after secret masking, see ratification.md) |

The lock's hash covers the root intent's *hash*, so the committed lock is bound to the exact text without revealing it (REQ-0012). Anyone holding the root intent record can verify it against the committed lock.

## 2. Schema (`intentgate.lock/1`)

```yaml
schema: intentgate.lock/1
id: 01JD8Z6Q4M7R2T9X3B5N8K1C6F       # ULID, stable across amendments
revision: 1                          # +1 per amendment
base_ref: main                       # PR base branch at ratification (REQ-0021)
root_intent:
  sha256: 9f2c…                      # hash of the verbatim root intent (REQ-0006, REQ-0014)
  source: claude-code-session        # claude-code-session | issue | plan-file
criteria:                            # 1..8 items (REQ-0007)
  - id: C1
    text: "Footprint findings are published as SARIF."
    footprint:                       # path globs, required, non-empty (REQ-0008)
      - "internal/report/**"
      - "!internal/report/legacy/**"
ratification:
  ratifier: "github:octocat"         # identity (REQ-0009)
  ratified_at: 2026-11-18T09:14:03Z  # RFC 3339 UTC (REQ-0010)
  method: hook                       # hook | cli-confirm | issue (REQ-0011, ADR 0003)
  review_duration_ms: 48210          # REQ-0013, see ratification.md §4
  drafted_by: "claude-code/claude-opus-5-5"
amendments: []                       # see footprint-check.md §5
hash: sha256:3b7a…                   # over everything above (REQ-0012)
```

**Validation** (`internal/lock`, pure): unknown keys are errors; `criteria` must have 1–8 entries; every criterion needs a non-empty `footprint`; footprint globs must parse; `method` must be one of the enum; `ratified_at` must be UTC. A lock that fails validation is reported as UNVERIFIABLE with the reason. It's never silently skipped.

## 3. Canonical form and hash

- Parse the YAML, drop the `hash` key, and convert to JSON.
- Canonicalize with **RFC 8785 (JCS)**: sorted keys, no insignificant whitespace, fixed number formatting.
- `hash = "sha256:" + hex(sha256(canonical_bytes))`.

YAML is the human-facing format; the canonical JSON is what gets hashed, so reformatting the YAML or reordering keys doesn't change the hash, while any change of content does. Criterion order is significant (C1, C2, … are referenced by id).

## 4. Identity and file naming

- `id` is a ULID generated at creation. It is stable across amendments, so findings and audit events can refer to a lock over its lifetime. The hash is not used as the id because it changes with every amendment.
- One file per lock: `.intent/<id>.yml`. A PR may add several locks; the PR check uses the union of their criteria.

## 5. Validity (REQ-0022)

A lock is valid for at most **14 days** from `ratification.ratified_at`, measured against the **evaluation time** (core-contracts.md §2), not the wall clock. An expired lock makes its criteria **suspect**. It isn't deleted and doesn't block. The fix is `intentgate amend <id> --reratify`, which creates a new revision with a new `ratified_at`.

The 14-day limit is a default in Phase 0. If it becomes configurable, REQ-0022 must change first, because its pin is `LE 14 days`.
