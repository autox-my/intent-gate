# S-21: release pipeline and a checksummed binary for the Action

**Base commit:** the commit that adds this spec   **Branch:** feature/s-21-release-pipeline
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/gemini-4-argon

ADR 0005 says the Action downloads a pinned, checksummed release of `intentgate`. Since S-09 it builds from source instead, so a repo using it needs Go in its CI and our source. This slice adds the release workflow and makes the Action download and verify the pinned binary, keeping the source build as an explicit choice (this repo's dogfood run uses it to test the PR's own code).

## Criteria

No requirement changes. ADR 0005 is the decision; this slice implements its "checksummed release download" consequence.

## Read first

- `docs/adr/0005-phase0-cli-and-action-app-later.md`
- `action/action.yml`, `action/action_test.go`, `action/README.md`
- `.github/workflows/check.yml` (how `req` is installed with a pinned SHA-256: the same pattern, reversed), `.github/workflows/intentgate.yml`

## Choices fixed here

- **Release workflow** `.github/workflows/release.yml`:
  - Trigger: `push` of a tag matching `v*` only. Permissions: `contents: write` at job level, nothing else. Actions pinned to full commit SHAs with a version comment, as in the other workflows.
  - Go from `go.mod` (`go-version-file`), `CGO_ENABLED=0`, `go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=<tag>"`, for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`.
  - Assets: raw binaries named `intentgate_<tag>_<os>_<arch>` and one `SHA256SUMS` in `sha256sum` format (`<hex>  <asset>`, sorted by asset name).
  - Publish with `gh release create <tag> --verify-tag` (the `GH_TOKEN` from `github.token` through `env:`), the assets and `SHA256SUMS` attached. The tag value reaches `run:` only through `env:`, and the workflow refuses a tag that does not match `^v[0-9]+\.[0-9]+\.[0-9]+$`.
  - Pushing a tag is the human's act; no workflow or script here creates tags.
- **Pinned release** `action/release.txt`: lines `version <tag>` then one `<hex>  intentgate_<tag>_<os>_<arch>` per Linux asset (`linux_amd64`, `linux_arm64`). Comments start with `#`. This slice commits it with **no** `version` line (no release exists yet), plus a header comment saying how to fill it from a release's `SHA256SUMS`. Pinning the Action's commit therefore pins the binary's hash.
- **Fetch script** `action/fetch.sh` (bash, `set -euo pipefail`), env in, nothing else:
  - `RELEASE_FILE`, `BASE_URL` (default `https://github.com/autox-my/intent-gate/releases/download`), `RUNNER_OS`, `RUNNER_ARCH`, `DEST` (output path).
  - Maps `Linux`/`X64` → `linux_amd64`, `Linux`/`ARM64` → `linux_arm64`; any other platform: exit 4 with a one-line message.
  - No `version` line: exit 5 ("no pinned release"). Downloads `<BASE_URL>/<tag>/<asset>` with `curl -fsSL --proto '=https,file'` to a temp file, checks its SHA-256 against `release.txt` (`sha256sum -c` or equivalent), and only then moves it to `DEST` with mode 0755. Download failure: exit 6; checksum mismatch: exit 7, the temp file removed, nothing at `DEST`.
- **Action** `action/action.yml`:
  - New input `source`: `release` (default) or `build`. Any other value: a warning and the job does nothing more (still never fails).
  - `source: release` runs `fetch.sh`; exit 5 (no pinned release yet) falls back to the source build with a `::notice`; any other non-zero exit is a `::warning` and the check is skipped (advisory: never build silently after a checksum failure).
  - `source: build`: today's `setup-go` + `go build` path, unchanged. `setup-go` runs only on the build path.
  - Every new step keeps the existing rules: `continue-on-error: true`, guarded by `github.event_name == 'pull_request'`, event values only through `env:`.
- `.github/workflows/intentgate.yml` (dogfood) passes `source: build`.
- `action/README.md`: the input table, how the release pin works, and that a new release means updating `release.txt` in a reviewed commit and re-pinning the Action's SHA.
- Process map: name `.github/workflows/release.yml` and `action/fetch.sh`; re-render.

## Tests first (committed red)

- `TestFetch` (`action/`, runs `fetch.sh` with `BASE_URL=file://<tmp>`): a correct asset → exit 0, file at `DEST`, mode 0755, bytes equal; a tampered asset → exit 7 and nothing at `DEST`; a missing asset → exit 6; no `version` line → exit 5; `RUNNER_OS=macOS` → exit 4; `--proto` refuses `http://` (exit 6).
- `TestAction_Source`: `source` input with default `release`; the fetch step runs only for `release`; `setup-go` and `build` run only for `build` or the exit-5 fallback; no `${{ }}` inside any `run:`; every step `continue-on-error: true` and event-guarded (extend the existing static checks, don't weaken them).
- `TestReleaseWorkflow` (static, parses `.github/workflows/release.yml`): tag-only trigger, `contents: write` only, every `uses:` pinned to a 40-hex SHA, the four targets, `SHA256SUMS`, `--verify-tag`, the tag through `env:`.
- `TestReleaseFile_NoVersionYet`: `action/release.txt` parses and has no `version` line in this slice.

The red commit may contain stubs that compile (and an empty `fetch.sh` that exits 1), so the tests fail on assertions. Quote one failure in the report.

## Verify

```bash
scripts/check.sh   # must end "verdict: GREEN"
```
The PR's own IntentGate check must still run (dogfood on `source: build`).

## Footprint (allowed paths)

- `action/**`
- `.github/workflows/release.yml`, `.github/workflows/intentgate.yml`
- `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`

## Forbidden

Everything else, including `claims/**`, `cmd/**`, `internal/**`, `.github/workflows/check.yml` and `docs/adr/**`. If a forbidden file must change, stop and report.

## Out of scope

- Creating the first tag and filling `release.txt` (human tags; a follow-up commit pins it).
- Reproducible-build verification, signing the binaries (cosign/minisign), macOS runners for the Action.

```intentgate
criteria:
  - text: "A v* tag builds and publishes checksummed intentgate binaries, and the Action downloads the pinned release and verifies its SHA-256 before running it, with the source build kept as an explicit option"
    footprint: ["action/**", ".github/workflows/release.yml", ".github/workflows/intentgate.yml", "docs/process/sdlc-map.yml", "docs/process/sdlc-map.md"]
```
