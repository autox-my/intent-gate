# S-09: GitHub Action and publishing

**Base commit:** the commit that adds this spec   **Branch:** feature/s-09-github-action
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The PR check in CI: a composite Action that runs `intentgate verify` and publishes its result as one check run, at most one comment, and SARIF. Publishing is a Go command (`intentgate publish`) so it can be tested against a fake API; the Action only wires the event to it.

## Criteria (claims this slice must satisfy)

- REQ-0035: the result is one check run named `IntentGate` per head SHA; a re-run updates it instead of creating a second.
- REQ-0037: at most one comment per PR: found by its marker and edited in place, otherwise created.

Supporting (already linked in the core, don't re-link): REQ-0033/0038 (comment mode from `decision.json`), REQ-0036 (SARIF uploaded), REQ-0039.

## Read first

- `docs/design/pr-check.md` §1, §4, §5; ADR 0005; `docs/design/core-contracts.md` §3
- `cmd/intentgate/verify.go` (`--out-dir` files, `decision.json`), `internal/forge/github/client.go` (S-06: TLS-only, no redirects, Link rules)

## Choices fixed here

- `internal/forge/github` additions, same safety rules as S-06 (https except loopback, no redirects, token scrubbed from errors):
  - `FindCheckRun(ctx, owner, repo, sha, name string) (id int64, found bool, err error)`: `GET /repos/{o}/{r}/commits/{sha}/check-runs?check_name=<name>&filter=latest&per_page=100`, paginated.
  - `CreateCheckRun` / `UpdateCheckRun`: `POST …/check-runs`, `PATCH …/check-runs/{id}` with `name`, `head_sha` (create only), `status: completed`, `conclusion`, `output: {title, summary}`. Summary truncated to 65 535 bytes on a UTF-8 boundary with a final line "… (truncated)".
  - `FindComment(ctx, owner, repo string, pr int, marker, author string) (id int64, found bool, err error)`: `GET …/issues/{pr}/comments?per_page=100`, paginated; the first comment whose body **starts with** the marker **and** whose author login equals `author` (case-insensitive). Comments by anyone else are never edited, whatever they contain.
  - `CreateComment` / `UpdateComment`: `POST …/issues/{pr}/comments`, `PATCH …/issues/comments/{id}`.
- `intentgate publish --out-dir <dir> --repo <owner/name> --pr <n> --head-sha <sha> [--bot-login <login>]`:
  - Reads `decision.json`, `summary.md`, `comment.md` from `--out-dir`. Token from `GITHUB_TOKEN` (exit 2 if absent); API base from `GITHUB_API_URL` (default `https://api.github.com`). `--bot-login` defaults to `github-actions[bot]`.
  - `publish: false` → does nothing, exit 0.
  - Check run: find by name `IntentGate` and head SHA; update if found, else create. Exactly one write.
  - Comment, by `decision.comment`: `none` → no comment call; `upsert` → update the found comment, else create; `update-if-exists` → update the found comment, else nothing. The body is `comment.md` as written.
  - Exit: 0 when published; 3 on any API error (status and GitHub's message on stderr, never the token). It never exits 1: findings were already reported by `verify`.
- `action/action.yml` (composite, `name: IntentGate`), inputs `go-version` (default `stable`) and `token` (default `${{ github.token }}`); runs on `pull_request` and `pull_request_target`:
  1. `actions/setup-go` pinned by full commit SHA; build `intentgate` from `${{ github.action_path }}/..` into `$RUNNER_TEMP`, with `-ldflags "-X main.version=<action ref or commit>"`.
  2. `intentgate verify --base "$BASE_SHA" --head "$HEAD_SHA" --base-branch "$BASE_REF" --author "$AUTHOR" --author-type "$AUTHOR_TYPE" --out-dir "$RUNNER_TEMP/intentgate"`, all values passed through `env:` from `github.event.pull_request.{base.sha, head.sha, base.ref, user.login, user.type}`. Its exit code is recorded as an output and never fails the step.
  3. `intentgate publish …` with `--repo "$GITHUB_REPOSITORY" --pr "$PR_NUMBER" --head-sha "$HEAD_SHA"`, `GITHUB_TOKEN` from the `token` input. A failure (for example the read-only token on a fork PR) is a workflow warning, not a failed step.
  4. `github/codeql-action/upload-sarif` pinned by full commit SHA, with `category: intentgate`, only if `results.sarif` exists and the decision says `publish`; `continue-on-error: true`.
  - No `${{ … }}` expression appears inside any `run:` script; every value reaches the script through `env:`. The job never fails because of IntentGate (Phase 0 is advisory).
  - `action/README.md`: the workflow snippet (`on: pull_request`, `fetch-depth: 0` checkout, permissions `contents: read`, `checks: write`, `pull-requests: write`, `security-events: write`), and the fork-PR caveat.
- Review 1 amendments (binding):
  - `pull_request` only. `pull_request_target` is not supported (ADR 0005): every step after setup is guarded by `if: github.event_name == 'pull_request'`, and any other event gets a single warning step "IntentGate supports pull_request only" and does nothing else. This removes the code-from-PR-with-a-write-token path and the pull_request + pull_request_target double run.
  - Input `bot-login` (default `github-actions[bot]`) passed as `--bot-login`; README says to set it when `token` is a PAT or another App's token.
  - Fork PRs and every run: `summary.md` is appended to `$GITHUB_STEP_SUMMARY`, and the out dir (`verify.json`, `results.sarif`, `summary.md`, `comment.md`, `decision.json`) is uploaded with `actions/upload-artifact` pinned by full SHA (name `intentgate`, `if-no-files-found: ignore`, `continue-on-error: true`), as ADR 0005 requires.
  - README's workflow snippet sets `concurrency: { group: intentgate-${{ github.event.pull_request.number }}, cancel-in-progress: true }`; the README says one check run and one comment are guaranteed only with it. After creating a comment, `publish` lists again and, if more than one bot comment starts with the marker, keeps the oldest (lowest id), updates it, and deletes the others (`DELETE …/issues/comments/{id}`), so a race converges to one comment.
  - The comment body is cut to 65 000 bytes on a UTF-8 boundary with a final line "… (truncated; the check run has the full summary)".
  - `decision.json`: `conclusion` must be `success` or `neutral` when `publish` is true, else exit 3.
  - A 2xx with an empty body is success for PATCH and DELETE; POST still needs the created id.
- Dependencies: none new.

## Tests first (committed red)

- `TestPublish_OneCheckRun` (cmd/intentgate, fake API on loopback): no existing check run → one POST; existing one for the SHA → one PATCH, no POST; two consecutive runs → exactly one check run in the fake's state. (REQ-0035)
- `TestPublish_OneComment` (REQ-0037): `upsert` with none → POST; with an existing marker comment by the bot → PATCH that id; a marker comment by another user is ignored (a new one is created); a comment containing but not starting with the marker is ignored; `update-if-exists` with none → no call; `none` → no comment call; comments on page 3 are found; two consecutive runs → exactly one bot comment.
- `TestPublish_NotPublished`: `publish: false` → no API call at all.
- `TestPublish_Errors`: missing token → 2; API 403 → 3 with status on stderr and the token absent from all output.
- `TestClient_ChecksAndComments` (forge/github): request paths, methods, JSON bodies, truncation on a UTF-8 boundary, pagination.
- `TestAction_Static` (in `action/`, a tiny Go test package): parses `action.yml`; every `uses:` is pinned to a 40-hex SHA; no `run:` contains `${{`; the verify and publish steps can't fail the job; and `actionlint` passes on a sample workflow using the Action if `actionlint` is on `PATH` (skip otherwise, never fail for its absence).

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add `tests:`: REQ-0035 `TestPublish_OneCheckRun`; REQ-0037 `TestPublish_OneComment`, ids exactly as in `build/junit.xml`.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `cmd/intentgate/**`
- `internal/forge/github/**`
- `action/**`
- `claims/REQ-0035.yml`, `claims/REQ-0037.yml`: the `tests:` key only.

## Forbidden

Everything else, including `internal/verify/**`, `internal/report/**`, `internal/policy/**`, `internal/gitx/**`, `.github/**`, `docs/**`, `.req/**` and `scripts/**`. If a forbidden file must change, stop and report.

## Out of scope

- Running the Action on this repository (no remote yet) and a release pipeline for prebuilt binaries.
- The audit event and `--replay` (REQ-0041/0042).
- Blocking mode (Phase 1.5).
