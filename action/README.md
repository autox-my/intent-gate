# IntentGate GitHub Action

A composite Action that checks a pull request against the intent locks it adds (`.intent/*.yml`)
and publishes the result:

- one check run named `IntentGate`, updated in place when the check runs again for the same commit;
- at most one pull request comment, found by its hidden marker and edited in place;
- the result in the job summary and, with `verify.json`, `results.sarif`, `summary.md`,
  `comment.md`, `decision.json` and `audit.json` (the run's `intentgate.audit/1` audit event), as
  the `intentgate` workflow artifact, on every run;
- the footprint findings as SARIF, uploaded with `github/codeql-action/upload-sarif` (category
  `intentgate`), so they show in code scanning and as annotations.

Phase 0 is advisory. The Action never fails the job and the check run never concludes `failure`,
so nothing here can block a merge.

## Use

```yaml
name: IntentGate
on:
  pull_request:

concurrency:
  group: intentgate-${{ github.event.pull_request.number }}
  cancel-in-progress: true

permissions:
  contents: read
  checks: write
  pull-requests: write
  security-events: write

jobs:
  intentgate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: OWNER/intent-gate/action@COMMIT_SHA
```

- `fetch-depth: 0` is needed: the check reads the base and the head of the pull request from git.
- Pin the Action to a full commit SHA, as the Action itself does with the actions it uses.
- Keep the `concurrency:` block. One check run and one comment per pull request are guaranteed
  only with it: two runs that overlap can each create a comment. The Action also deletes duplicate
  comments of its own after it creates one, but it does not rely on that alone.
- Set `bot-login` when `token` is a personal access token or another App's token: the Action finds
  its comment by its marker **and** its author, and a comment made with another identity is not
  `github-actions[bot]`'s. A mismatch makes every run create a new comment.
- `pull_request` is the only supported event. Any other event, including `pull_request_target`,
  gets one workflow warning and no check.
- The Action builds `intentgate` from the checked-out Action repository with Go (`go-version`,
  default `stable`); it does not download a binary.

| Input | Default | Meaning |
|---|---|---|
| `go-version` | `stable` | Go version for the build |
| `token` | `${{ github.token }}` | token for the Checks and Issues APIs |
| `bot-login` | `github-actions[bot]` | login whose comments are the Action's own |

Output: `exit-code`, the exit code of `intentgate verify` (0 clean, 1 findings, 3 could not decide).

## Pull requests from forks

On a `pull_request` event from a fork, GitHub gives the workflow a **read-only** token. The check
run and the comment cannot be written, and the SARIF upload is refused. The Action then shows a
workflow warning and still shows the full result in the job summary and the `intentgate`
artifact; the job does not fail.

`pull_request_target` gives a token that can write, but that event runs with the base repository's
secrets, and a workflow that checks out and runs a pull request's code under it is a well-known way
to leak them. It is **not supported** in Phase 0: the Action refuses it with a warning and does
nothing else. Use `pull_request` and accept the warning on fork pull requests.
