# Design: the intent lock

<!-- req-satisfies: REQ-0006 -->
<!-- req-satisfies: REQ-0007 -->
<!-- req-satisfies: REQ-0008 -->
<!-- req-satisfies: REQ-0009 -->
<!-- req-satisfies: REQ-0010 -->
<!-- req-satisfies: REQ-0011 -->
<!-- req-satisfies: REQ-0013 -->
<!-- req-satisfies: REQ-0014 -->
<!-- req-satisfies: REQ-0022 -->

The intent lock is IntentGate's public contract. Agent hooks, the CLI, the PR check and later integrations all read and write it, so its schema is versioned, and published as `schema/intentgate.lock.v1.json`.

## 1. Two artifacts, one hash

REQ-0006 says the lock stores the root intent verbatim, and REQ-0014 says the committed file must not contain it. The design meets both by splitting the lock:

| Artifact | Where | Contains |
|---|---|---|
| **Committed lock** | `.intent/<id>.yml` in the PR branch | everything except the root intent text, plus `root_intent.sha256` |
| **Root intent record** | `~/.intentgate/locks/<id>/root-intent.md` on the ratifier's machine | the root intent verbatim (after secret masking, see ratification.md) |

The lock's hash covers the root intent's *hash*, so the committed lock is bound to the exact text without revealing it (REQ-0013). Anyone holding the root intent record can verify it against the committed lock.

## 2. Schema (`intentgate.lock/1`)

```yaml
schema: intentgate.lock/1
id: 01JD8Z6Q4M7R2T9X3B5N8K1C6F       # ULID, stable across amendments
revision: 1                          # +1 per amendment
base_ref: main                       # PR base branch at ratification (REQ-0021)
root_intent:
  sha256: 9f2c…                      # hash of the verbatim root intent (REQ-0006, REQ-0014)
  source: claude-code-session        # claude-code-session | issue | plan-file
  redactions: 0                      # secrets masked before hashing (REQ-0015, ratification.md §5)
criteria:                            # 1..8 items (REQ-0007)
  - id: C1
    text: "Footprint findings are published as SARIF."
    footprint:                       # path globs, required, non-empty (REQ-0008)
      - "internal/report/**"
      - "!internal/report/legacy/**"
ratification:
  ratifier: "github:octocat"         # identity (REQ-0009): github:<login> (issue path) or git:<user.email> (local)
  ratified_at: 2026-11-18T09:14:03Z  # RFC 3339 UTC (REQ-0010)
  method: hook                       # hook | cli-confirm | issue (REQ-0011, ADR 0003)
  review_duration_ms: 48210          # REQ-0012, see ratification.md §4
  drafted_by: "claude-code/claude-opus-5-5"
amendments: []                       # see footprint-check.md §5
hash: sha256:3b7a…                   # over everything above (REQ-0013)
```

**Validation** (`internal/lock`, pure): unknown keys are errors; `criteria` must have 1–8 entries; every criterion needs a non-empty `footprint`; footprint globs must parse; `method` must be one of the enum; `ratified_at` must be UTC. A lock that fails validation is reported as UNVERIFIABLE with the reason. It's never silently skipped.

## 3. Canonical form and hash

- Parse the YAML, drop the `hash` key, and convert to JSON.
- Canonicalize with **RFC 8785 (JCS)**: sorted keys, no insignificant whitespace, fixed number formatting.
- `hash = "sha256:" + hex(sha256(canonical_bytes))`.

YAML is the human-facing format; the canonical JSON is what gets hashed, so reformatting the YAML or reordering keys doesn't change the hash, while any change of content does. A scalar key that is present (even with a zero value such as `0` or `""`) is content and hashes differently from the same key absent; an absent list and an empty list hash the same. Criterion order is significant (C1, C2, … are referenced by id).

## 4. Identity and file naming

- `id` is a ULID generated at creation. It is stable across amendments, so findings and audit events can refer to a lock over its lifetime. The hash is not used as the id because it changes with every amendment.
- One file per lock: `.intent/<id>.yml`. A PR may add several locks; the PR check uses the union of their criteria.

## 5. Validity (REQ-0022)

A lock is valid for at most **14 days** from `ratification.ratified_at`, measured against the **evaluation time** (core-contracts.md §2), not the wall clock. The window is closed at both ends: valid when `ratified_at ≤ t ≤ ratified_at + 14 days`. An evaluation time before `ratified_at` is not valid, so a future-dated lock cannot extend its life past 14 days. There is no clock-skew allowance, because one would let a lock live longer than the pin allows. An expired lock makes its criteria **suspect**. It isn't deleted and doesn't block. The fix is `intentgate amend <id> --reratify`, which creates a new revision with a new `ratified_at`.

The 14-day limit is a default in Phase 0. If it becomes configurable, REQ-0022 must change first, because its pin is `LE 14 days`.

## 6. Committed intent as the root intent (ADR 0006, REQ-0043, REQ-0044)

A team may make a lock's root intent a committed, reviewed file instead of the private prompt. It is opt-in per lock; every other lock keeps §1's split.

- **Where.** `intents/<slug>.md` at the repository top level: `<slug>` is `[a-z0-9][a-z0-9-]*`, one file per piece of work, outside `.intent/` (which holds only locks). Several locks may name the same intent.
- **How a plan names it.** The plan's `intentgate` block gets one optional top-level key, `intent: intents/<slug>.md`, next to `criteria:`. The same key works for every path that reads the block: the plan-approval hook, the ratify card and `lock --from-plan`. `--prompt` together with `intent:` is a usage error (two root intents). The issue path has no `intent:` (the issue is its root intent).
- **What must hold at lock time.** The file is tracked, and its bytes in the working tree equal the blob at `HEAD` (no uncommitted edits), so the lock binds to text anyone can read from git. Otherwise: no lock, exit 3, with the reason. Whether the intent was *accepted* (merged through a code-owner-approved PR, ADR 0006) is the PR check's job (REQ-0045, a later slice), not the lock command's.
- **What the lock records.**

  ```yaml
  root_intent:
    sha256: 5be1…                     # SHA-256 of the file's bytes, as for any root intent
    source: committed-intent          # new value
    redactions: 0                     # always 0: the text is public by the team's choice
    path: intents/checkout-v2.md      # new, required iff source is committed-intent
    blob: 3f9a…                       # new, the git blob id at HEAD (40 or 64 hex), required iff committed-intent
  ```

  `path` and `blob` are in the hashed content (§3). Validation: both present iff `source: committed-intent`, absent otherwise; `path` matches `^intents/[a-z0-9][a-z0-9-]*\.md$`; `blob` is 40 or 64 lowercase hex. The schema stays `intentgate.lock/1` with the two optional keys; an older `intentgate` reports such a lock as UNVERIFIABLE (unknown keys), which only affects teams that opted in.
- **No local record.** No `root-intent.md` is written for a committed intent: git is the verbatim store (REQ-0006), and the committed lock still holds no intent text (REQ-0014), only its path and hashes.
- **Secrets.** The secret scan still runs on the file before the lock is written (REQ-0015). A hit is no lock, exit 3, "the committed intent contains what looks like a secret": the file is already public, so masking it in the lock would hide nothing; the fix is in the file and its history.
- **Plan review.** The review compares the plan with the intent file's text, as it does with a prompt.
