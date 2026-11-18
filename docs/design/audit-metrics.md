# Design: audit events and metrics

<!-- req-satisfies: REQ-0041 -->
<!-- req-satisfies: REQ-0042 -->

Phase 0 has no server, so the audit trail is made of things that already persist: git history, CI artifacts and a local log.

## 1. Audit events (REQ-0042)

Every `verify` run and every `lock` or `amend` writes one JSON event, schema `intentgate.audit/1`:

```json
{
  "schema": "intentgate.audit/1",
  "event": "verify",
  "at": "2026-11-18T09:20:11Z",
  "repo": "owner/name", "pr": 42, "base_sha": "…", "head_sha": "…",
  "intentgate_version": "0.1.0", "config_sha256": "…",
  "locks": [ { "id": "01JD8Z6Q…", "revision": 2, "hash": "sha256:…",
               "ratification_method": "hook", "review_duration_ms": 48210 } ],
  "outcome": "findings", "exit_code": 1, "conclusion": "neutral"
}
```

`ratification_method` is copied from each lock into every event, which is what REQ-0042 asks for. It also holds every input needed for `verify --replay` (core-contracts.md §1).

| Event source | Where it goes |
|---|---|
| `verify` in CI | uploaded as the `intentgate-audit` workflow artifact, and summarised in the check run |
| `lock`, `amend`, `verify` run locally | appended to `~/.intentgate/audit.jsonl` |

Events contain no code, diff content or root intent text. They hold ids, hashes and counts only.

## 2. Footprint-amendment rate (REQ-0041)

The rate is **derived, not collected**. Every amendment is written into the lock itself (`amendments:`, footprint-check.md §5), and locks are committed, so `intentgate stats` computes it from git history:

```text
footprint-amendment rate = locks with ≥ 1 footprint amendment / locks merged, per 30 days
```

`intentgate stats --since 30d` prints it with the other Phase 0 signals: locks per merged PR, ratification methods, median review duration and findings per PR. The v2 review's noise threshold applies: above about 40%, the footprint suggestions at ratification are wrong and need fixing before anything else.
