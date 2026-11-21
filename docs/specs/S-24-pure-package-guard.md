# S-24: a test that keeps the PURE packages pure

**Base commit:** the commit that adds this spec   **Branch:** feature/s-24-pure-package-guard
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/gemini-4-argon

`docs/design/README.md` §2 marks packages PURE: "They do no I/O, read no clock and make no network calls." That rule carries `verify`'s determinism (REQ-0040), but today only `internal/footprint` has a test for it (its import allowlist, REQ-0031). This slice turns the rule into one test that reads the PURE list from the design overview itself, so the prose and the check cannot drift apart. From the review of the 2026-11-21 ADR-vs-codebase research (gap 2). Tests only.

## Criteria

No requirement changes and no claim links: this guards a design rule (design README §2), not a claim.

## Read first

- `docs/design/README.md` §2 (the layout block and "the rule that carries the design")
- `internal/footprint/footprint_test.go` `TestNoModelCall_Imports` (the existing pattern: `go/parser` with `ImportsOnly`)

## Choices fixed here

- New test-only package `internal/arch`: `doc.go` (package comment only) and `arch_test.go`. No production code changes anywhere.
- **The PURE set comes from the design overview:** the test reads `docs/design/README.md` (relative to the repo root found from the test's directory), takes the lines of the fenced `text` layout block whose second column is `PURE`, and gets the package paths (`internal/lock/`, …). Fewer than one, or a listed directory that does not exist: the test fails naming it.
- **The rule, per PURE package, over its non-test `.go` files** (parsed with `go/parser`, not `go list`, so no subprocess):
  - No import of: `os`, `os/exec`, `os/signal`, `io/fs`, `io/ioutil`, `syscall`, `net` or anything under `net/`, `crypto/rand`, `math/rand`, `math/rand/v2`, `runtime/debug`, `plugin`, `unsafe`, and no import of the module's adapter packages: `internal/gitx`, `internal/forge/...`, `internal/hooks/...`, `internal/ratify`, `internal/planreview`.
  - No call or reference to `time.Now`, `time.Since`, `time.Until`, `time.Tick`, `time.After`, `time.NewTimer`, `time.NewTicker` (selector expressions on the imported `time` package, whatever its local name).
  - Each problem names the file, the line and the import or call.
- **Canary:** the checker is a function the test also runs on a synthetic package written to `t.TempDir()` that imports `os` and calls `time.Now()` under an alias (`import t2 "time"`); it must report both. A second canary: a README layout block with a PURE line naming a missing directory must fail.
- The existing `TestNoModelCall_Imports` in `internal/footprint` stays as it is.

## Tests first (committed red)

- `TestPurePackages` (internal/arch): passes on the current tree once implemented. Red first: commit the test calling a stub checker that returns no problems, so the canaries fail on their assertions. Quote one.
- `TestPurePackages_Canary` and `TestPurePackages_DesignListMissingDir`: as above.

## Verify

```bash
scripts/check.sh   # must end "verdict: GREEN"
```
If a PURE package fails the rule on the current tree, stop and report it (do not change that package; that is a separate slice).

## Footprint (allowed paths)

- `internal/arch/**`

## Forbidden

Everything else, including every other package and `docs/design/README.md`. If a forbidden file must change, stop and report.

## Out of scope

- A purity check for `internal/ids` (its clock and entropy are injected, but it is not marked PURE).
- Layering rules between PURE packages.

```intentgate
criteria:
  - text: "A test reads the PURE package list from docs/design/README.md and fails if any of them imports an I/O, network, randomness or adapter package or reads the clock, with canaries proving it catches violations"
    footprint: ["internal/arch/**"]
```
