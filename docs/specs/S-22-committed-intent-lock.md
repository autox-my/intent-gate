# S-22: a lock whose root intent is a committed intent file

**Base commit:** the commit that adds this spec   **Branch:** feature/s-22-committed-intent-lock
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/gemini-4-argon

First slice of ADR 0006: a plan may name `intents/<slug>.md` as its root intent, and the lock records the file's path and blob instead of keeping a private record. Acceptance by a code owner (REQ-0045) is the next slice.

## Criteria

- REQ-0043: The lock command shall accept a committed intent file as the root intent of an intent lock.
- REQ-0044: The intent lock shall record the path and blob hash of its committed intent file.

Both claims were ratified by the human on 2026-11-20 and are **new in this slice**: the orchestrator commits `claims/REQ-0043.yml` and `claims/REQ-0044.yml` on the branch base (after the lock commit), with `tests: []`. The worker fills `tests:` only.

## Read first

- `docs/adr/0006-committed-intent-as-root-intent.md`
- `docs/design/intent-lock.md` §1–§3 and **§6** (the contract for this slice; on `main`)
- `docs/design/ratification.md` (masking, the root intent record), `internal/ratify` (`draft.go`, `mask.go`, `store.go`), `internal/lock`, `cmd/intentgate/lock.go`, `internal/hooks/claudecode`

## Choices fixed here

- Everything in intent-lock.md §6 is binding: the `intents/<slug>.md` path rule, the optional top-level `intent:` key in the plan's `intentgate` block (same parser for hook, ratify card and `lock --from-plan`), `--prompt` + `intent:` is exit 2, tracked-and-clean at `HEAD` or exit 3, the `root_intent` fields (`source: committed-intent`, `sha256` of the bytes, `redactions: 0`, `path`, `blob`), no `root-intent.md` written, a secret-scan hit is exit 3 with the §6 message, the plan review reads the intent file's text.
- `blob` is `git rev-parse HEAD:<path>` through `internal/gitx` (scrubbed environment and pinned config as the other gitx calls); the bytes compared are the working-tree file against `git cat-file blob <blob>`.
- An `intent:` value that breaks the path rule (absolute, `..`, outside `intents/`, wrong slug, not `.md`) is a drafting error: no lock, exit 2 from the CLI, and the hook's usual "no lock" message with the reason.
- `schema/intentgate.lock.v1.json`: `committed-intent` added to the source enum, optional `path` and `blob` with the §6 patterns, and the "both iff committed-intent" rule (`if`/`then`/`else`). Schema id unchanged.
- `docs/design/intent-lock.md`: add only the two lines `<!-- req-satisfies: REQ-0043 -->` and `<!-- req-satisfies: REQ-0044 -->` beside the others; regenerate `.req/satisfied` with req's own command if `req satisfy` asks for it.
- The amend path keeps a committed-intent lock's `root_intent` unchanged (amendments don't touch it).

## Tests first (committed red)

- `TestLock_CommittedIntentFields` (internal/lock, REQ-0044): valid lock with `committed-intent`, `path`, `blob` passes; `path`/`blob` missing with that source, or present with another source, fails; bad path patterns and non-hex/short blobs fail; changing `path` or `blob` changes the hash.
- `TestLockFromPlan_CommittedIntent` (cmd, REQ-0043): temp repo with a committed `intents/checkout-v2.md`; a plan whose block has `intent: intents/checkout-v2.md` → after `yes`, the lock has `source: committed-intent`, `path`, `blob` = `git rev-parse HEAD:intents/checkout-v2.md`, `sha256` of the bytes, `redactions: 0`, and no `root-intent.md` under the state dir; the file edited but not committed → exit 3, nothing written; untracked → exit 3; `intent: ../x.md` → exit 2; `--prompt` + `intent:` → exit 2; a file holding a fake GitHub token → exit 3 and the §6 message.
- `TestHook_CommittedIntent` (cmd, REQ-0043): `pre` + `post` for an approved `ExitPlanMode` plan whose block names a committed intent → lock with `source: committed-intent`; the same through the ratify card (`AskUserQuestion`) → same.
- `TestDraft_IntentKey` (internal/ratify): the `intent:` key parses; an unknown top-level key is still rejected.

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one failure in the report.

## Claim links

After green: REQ-0043 `tests:` = `TestLockFromPlan_CommittedIntent`, `TestHook_CommittedIntent`; REQ-0044 `tests:` = `TestLock_CommittedIntentFields`; ids exactly as in `build/junit.xml`.

## Verify

```bash
scripts/check.sh
```
Expected on the branch until the human signs the next Baseline: every step PASS except `req check` and `req baseline`, and their only complaints are that REQ-0043 and REQ-0044 are in no signed Baseline. Quote those lines in the report. Any other failure is the worker's to fix. Then the human signs `phase0-3` on the branch (`scripts/sign-baseline.sh phase0-3`), the orchestrator points `scripts/check.sh`, `docs/process/sdlc-map.yml` (re-rendered) and `docs/glossary.md` at it, and `scripts/check.sh` must end `verdict: GREEN`.

## Footprint (allowed paths)

- `cmd/intentgate/**`, `internal/lock/**`, `internal/ratify/**`, `internal/hooks/claudecode/**`, `internal/gitx/**`
- `schema/intentgate.lock.v1.json`
- `claims/REQ-0043.yml`, `claims/REQ-0044.yml` (`tests:` only, for the worker)
- `docs/design/intent-lock.md` (the two `req-satisfies` lines only), `.req/satisfied`
- For the Baseline step (human and orchestrator only): `baselines/**`, `decisions/**`, `scripts/check.sh`, `docs/glossary.md`, `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`

## Forbidden

Everything else, including every other claim. The worker never touches the Baseline-step paths. If a forbidden file must change, stop and report.

## Out of scope

- REQ-0045 (the PR check reports code-owner approval) and the "intent accepted" result: S-23.
- An `intent:` for the issue path; `stats` counts of locks with a committed intent.

```intentgate
criteria:
  - text: "REQ-0043: The lock command shall accept a committed intent file as the root intent of an intent lock."
    footprint: ["cmd/intentgate/**", "internal/lock/**", "internal/ratify/**", "internal/hooks/claudecode/**", "internal/gitx/**", "schema/intentgate.lock.v1.json", "claims/REQ-0043.yml", "docs/design/intent-lock.md", ".req/satisfied"]
  - text: "REQ-0044: The intent lock shall record the path and blob hash of its committed intent file."
    footprint: ["internal/lock/**", "schema/intentgate.lock.v1.json", "claims/REQ-0044.yml", "docs/design/intent-lock.md", ".req/satisfied"]
  - text: "The next Baseline (phase0-3) attests REQ-0043 and REQ-0044, and scripts/check.sh verifies it"
    footprint: ["baselines/**", "decisions/**", "scripts/check.sh", "docs/glossary.md", "docs/process/sdlc-map.yml", "docs/process/sdlc-map.md"]
```
