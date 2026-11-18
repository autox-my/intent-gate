# IntentGate GitHub Action

A composite Action that checks a pull request against the intent locks it adds (`.intent/*.yml`)
and publishes the result:

- one check run named `IntentGate`, updated in place when the check runs again for the same commit;
- at most one pull request comment, found by its hidden marker and edited in place;
- the footprint findings as SARIF, uploaded with `github/codeql-action/upload-sarif` (category
  `intentgate`), so they show in code scanning and as annotations.

Phase 0 is advisory. The Action never fails the job and the check run never concludes `failure`,
so nothing here can block a merge.

## Use

```yaml
name: IntentGate
on:
  pull_request:

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
- The Action builds `intentgate` from the checked-out Action repository with Go (`go-version`,
  default `stable`); it does not download a binary.

| Input | Default | Meaning |
|---|---|---|
| `go-version` | `stable` | Go version for the build |
| `token` | `${{ github.token }}` | token for the Checks and Issues APIs |

Output: `exit-code`, the exit code of `intentgate verify` (0 clean, 1 findings, 3 could not decide).

## Pull requests from forks

On a `pull_request` event from a fork, GitHub gives the workflow a **read-only** token. The check
run and the comment cannot be written, and the SARIF upload is refused. The Action then shows a
workflow warning and still prints the full result in the job log; the job does not fail.

Running on `pull_request_target` gives a token that can write, but that event runs with the
base repository's secrets, and a workflow that checks out and runs a pull request's code under it
is a well-known way to leak them. The Action only reads the pull request through git and never runs
its code, but the safe use of `pull_request_target` is not supported in Phase 0: prefer
`pull_request` and accept the warning on fork pull requests.
