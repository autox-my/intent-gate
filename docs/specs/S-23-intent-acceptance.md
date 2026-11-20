# S-23: the PR check reports whether a committed intent was accepted

**Base commit:** the commit that adds this spec   **Branch:** feature/s-23-intent-acceptance
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/gemini-4-argon

Second and last slice of ADR 0006: for each lock whose root intent is a committed intent, the PR check says whether that intent reached the base branch through a PR approved by a code owner. Report only; nothing blocks.

## Criteria

- REQ-0045: The PR check shall report whether each committed intent file was approved by a code owner.

Ratified by the human on 2026-11-20 with REQ-0043/0044 and **new in this slice**: the orchestrator commits `claims/REQ-0045.yml` on the branch base (after the lock commit) with `tests: []`. The worker fills `tests:` only.

## Read first

- `docs/design/pr-check.md` **§7** (the contract; on `main`), §5; `docs/design/core-contracts.md` §1
- `docs/design/intent-lock.md` §6; `docs/adr/0006-committed-intent-as-root-intent.md`
- `internal/forge/github` (client rules: https only, no redirects, token never in errors, pagination), `cmd/intentgate/publish.go`, `cmd/intentgate/verify.go`, `internal/gitx`, `action/action.yml`, `action/action_test.go`

## Choices fixed here

- Everything in pr-check.md §7 is binding: the command, its inputs, the six rules in order with their statuses and reasons, `acceptance.json` (`intentgate.acceptance/1`) shape and order, publishing rules, and that `verify` is untouched.
- Command: `intentgate acceptance --out-dir <dir> --repo <owner/name> --base <sha> --head <sha>`; token from `GITHUB_TOKEN`. Exit 0 when `acceptance.json` was written (whatever the statuses), 2 usage, 3 when it cannot write it (no `verify.json`, git cannot resolve base/head). Phase 0 advisory: no status is an error exit.
- Forge: add `CommitPulls(ctx, owner, repo, sha)` and `PullReviews(ctx, owner, repo, n)` to `internal/forge/github`, with the package's existing safety rules and `Link` pagination.
- CODEOWNERS: a pure parser and matcher in a new `internal/codeowners` package (no I/O): GitHub's file order and "last match wins", gitignore-style patterns (`*`, `**`, leading `/`, trailing `/`), comments and blank lines, owners as `@user`, `@org/team` or an email. A line with a pattern and no owners un-owns the path.
- git: the base blob and the last changing commit through `internal/gitx` (scrubbed environment and pinned config).
- `schema/intentgate.acceptance.v1.json` (draft 2020-12) matching the output.
- Action: a step `acceptance` after `verify` and before `summary`/`publish`, guarded like the others, `continue-on-error: true`, values only through `env:`, run only when `verify` succeeded. Its failure is a `::warning` and changes nothing else. `acceptance.json` rides in the existing `intentgate` artifact. Extend `action_test.go`'s static checks; weaken none.
- `publish`: the "Committed intents" section as §7 says; existing publish tests stay green unchanged.

## Tests first (committed red)

- `TestCodeowners` (internal/codeowners): file order (`.github/` before root before `docs/`); last match wins; `*.md`, `/intents/`, `intents/**`, `docs/` patterns; an owner-less line un-owns; teams and emails kept apart from users; comments ignored.
- `TestAcceptance` (cmd, REQ-0045; temp repo + a fake GitHub server on loopback): one case per §7 outcome — accepted (owner approved), not-accepted (blob not on base), not-accepted (no PR for the commit), not-accepted (approved only by non-owners, and an owner who approved then requested changes), unknown (no CODEOWNERS / no matching line), unknown (team-owned), unknown (API 403). `acceptance.json` bytes known-answer for a two-lock case, sorted by lock id; a PR with no committed-intent lock → empty list; `verify.json` byte-identical before and after.
- `TestForge_CommitPullsAndReviews` (internal/forge/github): request paths, pagination, no token in errors.
- `TestPublish_CommittedIntents` (cmd): the summary gains the section when `acceptance.json` lists intents; no new comment is created only because of it; conclusion unchanged.
- `TestAction_Acceptance` (action): the new step's guard, order, `continue-on-error`, `env:`-only values.

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one failure in the report.

## Claim links

After green: REQ-0045 `tests:` = `TestAcceptance`; id exactly as in `build/junit.xml`.

## Verify

```bash
scripts/check.sh
```
Expected on the branch until the human signs the next Baseline: every step PASS except `req check` and `req baseline`, whose only complaint is that REQ-0045 is in no signed Baseline. Quote those lines. Then the human signs `phase0-4` on the branch and records the satisfaction link (`req satisfy --record docs/design/pr-check.md REQ-0045`), the orchestrator points `scripts/check.sh`, `docs/process/sdlc-map.yml` (re-rendered) and `docs/glossary.md` at it, and `scripts/check.sh` must end `verdict: GREEN`.

## Footprint (allowed paths)

- `cmd/intentgate/**`, `internal/codeowners/**`, `internal/forge/github/**`, `internal/gitx/**`
- `action/**`, `schema/intentgate.acceptance.v1.json`
- `claims/REQ-0045.yml` (`tests:` only, for the worker)
- `docs/design/pr-check.md` (the one `req-satisfies: REQ-0045` line only, for the worker)
- For the Baseline step (human and orchestrator only): `.req/satisfied`, `baselines/**`, `decisions/**`, `scripts/check.sh`, `docs/glossary.md`, `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`

## Forbidden

Everything else, including `internal/verify/**`, `internal/report/**`, `internal/lock/**` and every other claim. The worker never touches the Baseline-step paths and does not run `req satisfy --record` (the human does). If a forbidden file must change, stop and report.

## Out of scope

- Team membership lookups (need a token with `read:org`).
- `stats` counts of locks with an accepted intent.

```intentgate
criteria:
  - text: "REQ-0045: The PR check shall report whether each committed intent file was approved by a code owner."
    footprint: ["cmd/intentgate/**", "internal/codeowners/**", "internal/forge/github/**", "internal/gitx/**", "action/**", "schema/intentgate.acceptance.v1.json", "claims/REQ-0045.yml", "docs/design/pr-check.md"]
  - text: "The next Baseline (phase0-4) attests REQ-0045 and its satisfaction link, and scripts/check.sh verifies it"
    footprint: [".req/satisfied", "baselines/**", "decisions/**", "scripts/check.sh", "docs/glossary.md", "docs/process/sdlc-map.yml", "docs/process/sdlc-map.md"]
```
