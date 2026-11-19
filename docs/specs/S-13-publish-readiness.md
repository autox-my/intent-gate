# S-13: publish readiness (module path, license, CI)

**Base commit:** the commit that adds this spec   **Branch:** feature/s-13-publish-readiness
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

Make the repository ready for its first push to `github.com/autox-my/intent-gate` (private). Decided by the human on 2026-11-19: private repo on the `autox-my` account, Apache-2.0, history kept as is, CI runs the full gate including req.

This lands **before** the first Baseline is signed: the module rename changes every test id in `claims/*.yml`, and a Baseline attests those links.

## Criteria

No requirement changes. Every claim keeps its statement, pins and meaning; only `tests:` ids are renamed to the new module path. `req gate` must stay green with all 41 claims linked.

## Choices fixed here

- Module path `intentgate` → `github.com/autox-my/intent-gate`: `go.mod`, every import, and every `tests:` id in `claims/*.yml` (`intentgate/…::Test…` → `github.com/autox-my/intent-gate/…::Test…`, exactly as they appear in the new `build/junit.xml`). Also `decisions/DEC-*.yml` notes and `scripts/coverage-explorer.*` if they show the old prefix (the decision drafts are unsealed, so editing them now is fine).
- `LICENSE`: the Apache License 2.0 text, verbatim, with `Copyright 2026 autox-my` in the appendix notice; a one-line SPDX header is not required in source files.
- `README.md`: rewrite as the project's front page: what IntentGate is (3–4 sentences from `docs/`), status (Phase 0, advisory only), install (`go install github.com/autox-my/intent-gate/cmd/intentgate@<ref>`), the three ways to lock, `intentgate verify`, the Action (link `action/README.md`), how requirements are managed (req, `claims/`, `scripts/check.sh`), license. Keep any existing content that is still true.
- `action/README.md`: `uses: autox-my/intent-gate/action@<full commit sha>` with a note to pin a SHA; keep everything else.
- `.github/workflows/check.yml`, on `push` to `main` and `pull_request`:
  - `actions/checkout` and `actions/setup-go` pinned by full SHA (reuse S-09's setup-go pin), `fetch-depth: 0`, `permissions: contents: read`.
  - Install req 0.6.0 from the public `autox-my/req` release `v0.6.0`: download `req-161c336-x86_64-unknown-linux-gnu.tar.gz` and its `.sha256`, verify with `sha256sum -c`, put `req` on `PATH`, and check `req --version` shows `claim_schema req.claim/4` and `rules_v 9` (fail otherwise). Pin the expected SHA-256 in the workflow too (read it from the release's `.sha256` asset now), so a replaced asset fails.
  - Run `scripts/check.sh`.
  - On `pull_request` only: `req diff --base origin/${{ github.base_ref }} --fail-on weakened,removed,reshaped` (values through `env:`, never `${{ }}` inside `run:`).
- `.github/workflows/intentgate.yml`: dogfood the Action on this repo's own PRs, `on: pull_request`, `uses: ./action`, the permissions and `concurrency` group from `action/README.md`. (Under `pull_request` the token is read-only for forks and the Action refuses other events, so building from the PR checkout is safe.)
- `action/action_test.go`'s actionlint check, or a new test, runs `actionlint` over both new workflows when `actionlint` is on `PATH` (skip otherwise).
- No secrets anywhere; `.env` stays ignored. Do not push, add a remote or create the GitHub repo: the human does that.

## Verify

```bash
scripts/check.sh                                   # GREEN, req gate included
grep -rn '"intentgate/' --include=*.go . | wc -l   # 0
grep -rn 'intentgate/' claims/ | grep -v 'github.com/autox-my/intent-gate/' | wc -l   # 0
actionlint .github/workflows/*.yml                 # clean
```

## Footprint (allowed paths)

- `go.mod`, `go.sum`, every `*.go` file (import paths only, plus test changes the rename forces)
- `claims/*.yml` (`tests:` ids only), `decisions/*.yml` (only the old module prefix in notes)
- `LICENSE`, `README.md`, `action/README.md`, `action/action_test.go`
- `.github/workflows/check.yml`, `.github/workflows/intentgate.yml`
- `scripts/coverage-explorer.py`, `scripts/coverage-explorer.tmpl.html` (old prefix only)

## Forbidden

Everything else, including claim statements and pins, `docs/**`, `.req/**`, `scripts/check.sh`, `scripts/sign-baseline.sh`, `baselines/**`. If a forbidden file must change, stop and report.
