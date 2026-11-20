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

`ratification_method` is copied from each lock into every event, which is what REQ-0042 asks for. A `verify` event also records `base_branch`, `author`, `author_type` and `verify_sha256` (the SHA-256 of the `verify.json` bytes), so `verify --replay <event.json>` can re-run the same refs in a clone that has them and confirm it reproduces the same bytes (core-contracts.md §1).

| Event source | Where it goes |
|---|---|
| `verify` in CI | `audit.json` in the out dir, uploaded with the Action's `intentgate` artifact |
| `lock`, `amend`, `verify` run locally | appended to `~/.intentgate/audit.jsonl` |

Events contain no code, diff content or root intent text. They hold ids, hashes and counts only.

## 2. Footprint-amendment rate (REQ-0041)

The rate is **derived, not collected**. Every amendment is written into the lock itself (`amendments:`, footprint-check.md §5), and locks are committed, so `intentgate stats` computes it from git history:

```text
footprint-amendment rate = locks with ≥ 1 footprint amendment / locks merged, per 30 days
```

`intentgate stats --since 30d` prints it with the other Phase 0 signals that git history alone can answer: lock count, ratification methods and median review duration. The window ends at the `HEAD` commit's committer time, not the wall clock, so the same commit gives the same numbers. Findings per PR need the CI artifacts and come later. The v2 review's noise threshold applies: above about 40%, the footprint suggestions at ratification are wrong and need fixing before anything else.

### Merged PRs and lock coverage

For repositories that merge PRs with merge commits, `stats` also counts the merges on `HEAD`'s first-parent history inside the window (`merged_prs`), those that are the adding commit of at least one valid lock (`merged_prs_with_lock`), and their ratio (`lock_coverage`, `n/a` with no merges). It is the share of merged work that went through an intent lock. Squash- and rebase-merged PRs leave no merge commit and are invisible here; counting them needs the forge.
