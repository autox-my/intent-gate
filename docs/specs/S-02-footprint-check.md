# S-02: footprint check (matching, implicit footprints, findings)

**Base commit:** the commit that adds this spec   **Branch:** feature/s-02-footprint-check
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The pure `internal/footprint` package: given the changed files, the criteria and the implicit footprints, report each changed file that is outside all of them, with a one-step amendment command. Reading git, `.intentgate.yml` and `.gitattributes` from disk is the adapters' job (a later slice); this package takes bytes and values.

## Criteria (claims this slice must satisfy)

- REQ-0024: a changed path inside no criterion's footprint and no implicit footprint is reported; findings are sorted by path.
- REQ-0025: a test file paired with a subject inside a criterion's footprint is inside that footprint.
- REQ-0026: fixtures and snapshots are inside the footprint (default globs).
- REQ-0027: lockfiles are inside the footprint (default names, any depth).
- REQ-0028: generated and vendored files are inside the footprint (default globs plus `.gitattributes` linguist markers).
- REQ-0029: implicit footprints are adjusted by `.intentgate.yml` (`add`, `remove`, `test_pairing`); a malformed file is an error, never "use the defaults".
- REQ-0030: every finding carries the exact `intentgate amend` command that would bring the path into a footprint.
- REQ-0031: the package makes no model call (its imports are an allowlist with no network package).

## Read first

- `docs/design/footprint-check.md` (all of it)
- `docs/design/README.md` §2 (the PURE package rule)
- `docs/design/core-contracts.md` §1 (determinism)

## Choices fixed here

- Package `intentgate/internal/footprint`. Pure: no file I/O, no clock, no network, no randomness. It does not import `internal/lock`; callers convert.
- API (names may be adjusted, shape may not):
  - `type ChangedFile struct { Path, OldPath string; Status Status }`: `Status` is one of added, modified, deleted, renamed; `OldPath` is set only for renamed.
  - `type Criterion struct { LockID, ID string; Footprint []string }`: criteria from every lock, in input order.
  - `ParseConfig(data []byte) (Config, error)`: `.intentgate.yml`. `nil` or empty data means "no file" and yields the defaults. Unknown keys are errors (`KnownFields(true)`), `version` must be `1`, every `add` glob must pass `doublestar.ValidatePattern`, and a `remove` entry that isn't exactly one of the default globs (as written in design §3, `**/`-prefixed for lockfiles) is an error (it catches typos); a duplicate `remove` entry is an error saying so. `test_pairing` defaults to true, and so does a zero-value `Config` passed to `NewImplicit`: the zero value means "the defaults". A file with no `version` (including a comment-only file) is an error naming the missing key.
  - `NewImplicit(cfg Config, gitattributes []byte) (Implicit, error)`: defaults (design §3) adjusted by the config, plus the linguist paths.
  - `Check(changed []ChangedFile, criteria []Criterion, implicit Implicit) []Finding`.
  - `type Finding struct { Path string; Status Status; NearestCriterion, LockID, Fix string }`, JSON field names as in design §6 (`lock_id` added).
- Matching (design §2): a path is inside a criterion when it matches at least one positive glob and none of its `!` globs (`doublestar.Match`, case-sensitive, `/` separators). Implicit globs have no negation.
- Changed files: paths under `.intent/` are skipped. A rename checks both `OldPath` and `Path`; each one outside is its own finding with status renamed. Deleted files are checked like any other.
- Paired tests (design §3 table, exactly): Go `d/x_test.go` ↔ `d/x.go`; JS/TS (extensions `ts tsx js jsx mjs cjs`, same extension on both sides) `d/x.test.E`, `d/x.spec.E`, `d/__tests__/x.E` ↔ `d/x.E`; Python `d/test_x.py`, `d/x_test.py`, `d/tests/test_x.py` ↔ `d/x.py`; Rust `R/tests/x.rs` ↔ `R/src/x.rs`. A test is inside a criterion's footprint when its subject path is (glob match only; the subject need not be in the diff or exist on disk). `test_pairing: false` turns this off.
- `.gitattributes`: one rule per line; blank lines, `#` comments and `[attr]` macro lines are skipped. Attributes `linguist-generated`, `linguist-generated=true`, `linguist-vendored`, `linguist-vendored=true` mark a pattern; `-linguist-…`, `!linguist-…` and any value other than `true` (`=false`, `=no`, …) unmark it; for a path, the last matching line wins per attribute (git's rule). A pattern ending in `/` matches nothing (gitattributes(5): directory patterns don't match paths inside; use `dir/**`). A negative pattern (`!…`) is an error, as git forbids it. A pattern with no `/` except a trailing one matches at any depth (`**/` + pattern); otherwise it is anchored at the repo root (a leading `/` is stripped). A pattern that fails `doublestar.ValidatePattern`, or a quoted pattern, is an error (UNVERIFIABLE upstream), never silently skipped.
- Nearest criterion (design §6): for each positive glob take its static prefix (the text before the first `*`, `?`, `[` or `{`, cut back to the last `/`); the criterion whose glob prefix shares the most leading path segments with the finding's directory wins; ties go to the earliest criterion in input order. With no criteria, `NearestCriterion`, `LockID` and `Fix` are empty.
- Fix command: `intentgate amend <lock-id> --criterion <id> --add '<glob>'`, where the glob is the finding's directory + `/**` (for a root-level file, the path itself), with doublestar metacharacters (`*?[]{}\`) in the path backslash-escaped, then single-quoted for POSIX shells (`'` becomes `'\''`). `amend` itself is S-03; this slice only writes the string.
- REQ-0031 test: parse the package's non-test `.go` files with `go/parser` and assert every import is in an allowlist: the standard library packages it actually needs minus `net`, `net/*`, `os`, `os/exec`, `io/fs`, `syscall`, plus `github.com/bmatcuk/doublestar/v4` and `gopkg.in/yaml.v3`.
- Dependencies: none new (both are already in `go.mod`).

## Tests first (committed red)

Table-driven tests in `internal/footprint`, fixtures under `internal/footprint/testdata/` if needed. At minimum:

- `TestCheck_OutsideEveryFootprint`: a path matching no criterion is reported; one matching any criterion is not; a `!` glob excludes; `**` crosses directories; matching is case-sensitive; findings are sorted by path; `.intent/` paths are skipped; renames check both paths; deletes are checked. (REQ-0024)
- `TestCheck_PairedTests`: every pairing in the table, with the subject inside a footprint → not reported; subject outside → reported; `test_pairing: false` → reported. (REQ-0025)
- `TestImplicit_Fixtures`: each fixture/snapshot default at some depth → not reported. (REQ-0026)
- `TestImplicit_Lockfiles`: each lockfile name at the root and nested → not reported; `go.mod` is not a lockfile → reported. (REQ-0027)
- `TestImplicit_GeneratedVendored`: each default glob, plus `.gitattributes` markers (set, `=true`, unset with `-`, `=false`, last-line-wins, anchored vs any-depth patterns, `[attr]` skipped, invalid pattern → error). (REQ-0028)
- `TestParseConfig`: no file → defaults; `add` extends; `remove` of a default drops it; `remove` of a non-default → error; unknown key, `version: 2`, invalid glob, malformed YAML → error. (REQ-0029)
- `TestFinding_FixCommand`: known-answer strings for nearest-criterion choice, tie-break, root-level file, metacharacter escaping and a path containing `'`; no criteria → empty fix. (REQ-0030)
- `TestNoModelCall_Imports`: the import allowlist above. (REQ-0031)

The red commit may contain stubs that compile and return zero values, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add `tests:` to REQ-0024 … REQ-0031 using the ids exactly as they appear in `build/junit.xml` (`classname::name`). Link a claim only to the test that proves it; leave it unlinked rather than link it weakly, and say so.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `internal/footprint/**`
- `go.mod`, `go.sum` (only if `go mod tidy` changes them)
- `claims/REQ-0024.yml` … `claims/REQ-0031.yml`: the `tests:` key only. Never touch `statement`, `pins` or anything else.

## Forbidden

Everything else, including `internal/lock/**`, `docs/**`, `.req/**` and `scripts/**`. If a forbidden file must change, stop and report.

## Out of scope

- The `gitx` adapter (running `git diff`), reading `.intentgate.yml` / `.gitattributes` from disk, lock discovery and the union across locks (PR check slice).
- The `intentgate amend` command (S-03).
- A JSON Schema for `.intentgate.yml`.
