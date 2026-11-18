# S-03: ratification core (drafting, secret masking, lock building, root intent store)

**Base commit:** the commit that adds this spec   **Branch:** feature/s-03-ratify-core
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The shared pipeline every ratification path uses (ratification.md §1), without any path's I/O shell: parse the plan's criteria block, mask secrets in the root intent, build and hash the lock, and write the lock plus the root intent record. The Claude Code hook (S-04) and the `lock`/`amend` commands (S-05) call this.

## Criteria (claims this slice must satisfy)

- REQ-0006: the root intent is stored verbatim (after masking) in the root intent record, and the lock's `root_intent.sha256` is the SHA-256 of exactly those bytes.
- REQ-0015: the root intent is secret-scanned and masked before anything is written; a scanner failure writes nothing.

Supporting (already linked elsewhere, don't re-link): the `intentgate` block's 8-criterion cap (REQ-0007), `review_duration_ms` set by the builder (REQ-0012).

## Read first

- `docs/design/ratification.md` §1 (incl. §1.1 drafting), §4, §5
- `docs/design/intent-lock.md` §1, §2 (note the new `root_intent.redactions`)
- `docs/design/README.md` §2 (PURE rule; `internal/ratify` is not PURE, but keep the pure parts free of I/O)

## Choices fixed here

- Package `intentgate/internal/ratify`. Pure functions take values; the store is the only I/O and takes its directories as arguments (no `os.UserHomeDir`, no clock, no randomness inside the package; ids and times are passed in).
- API (names may be adjusted, shape may not):
  - `DraftCriteria(plan string) ([]lock.Criterion, error)`: ratification.md §1.1 exactly. Exactly one fenced block whose info string is `intentgate`; YAML with `KnownFields(true)`; each item has `text` and `footprint` only; ids `C1…Cn` in order; 0 or more than 8 criteria, a missing block, two blocks, or invalid YAML is an error whose message starts `no lock: `. Footprints are validated with the same rules as `lock.Validate` (reuse it; don't copy them).
  - `Mask(text string) (masked string, redactions int, err error)`: gitleaks' detection library (`github.com/zricethezav/gitleaks/v8`, its `detect` package with the default config), each finding's secret replaced by `[REDACTED:<rule-id>]`. Deterministic: same input, same output. If gitleaks can't be imported as a library at a pinned version, stop and report; don't substitute another scanner.
  - `Build(in BuildInput) (lock.Lock, error)`: from id, base ref, source, masked root intent, redactions, criteria, ratifier, ratified-at (`time.Time`, converted to UTC, truncated to seconds, formatted with `Z`), method, review duration, optional drafted-by. Revision 1, empty amendments, `root_intent.sha256` = lower-hex SHA-256 of the masked text's UTF-8 bytes, hash set via `lock.Hash`. Returns an error if `lock.Validate` reports any problem.
  - `Store{RepoRoot, StateDir string}` with `Write(l lock.Lock, maskedRootIntent string) error`: writes `<StateDir>/locks/<id>/root-intent.md` (mode 0600, dirs 0700) first, then `<RepoRoot>/.intent/<id>.yml`; each by temp file + rename in the same directory; refuses to overwrite an existing lock file with the same id. Before writing, it re-checks that the text's SHA-256 matches `root_intent.sha256` and refuses otherwise.
  - `MaskAndBuild` (or equivalent) is the only way the CLI/hook will get a Lock: scanning happens inside it, before `Build`, so an unmasked root intent can't reach the store. A `Mask` error aborts with nothing written.
- Lock schema change in `internal/lock`: `RootIntent.Redactions int` (`redactions`), required, integer ≥ 0, counted in the hash; add it to `schema/intentgate.lock.v1.json` and to the S-01 fixtures (`internal/lock/testdata/*.yml`), recompute the golden hash independently (outside the Go code) and say how.
- YAML output of a lock (`lock.Marshal(l Lock) ([]byte, error)` in `internal/lock`): deterministic field order as in intent-lock.md §2, and `Parse(Marshal(l))` round-trips to an equal Lock with the same hash.
- ULID generation, git identity, TTY confirmation, transcript parsing and the hook JSON are not this slice.

## Tests first (committed red)

- `TestDraftCriteria`: one block → criteria with ids C1…; missing block, two blocks, 0 and 9 criteria, unknown key, invalid footprint glob, invalid YAML → error starting `no lock: `; a ```` ```yaml ```` block is not an `intentgate` block. (REQ-0007 support; don't link)
- `TestMask_RedactsSecrets`: a text containing a well-known fake credential (e.g. an AWS access key id `AKIAIOSFODNN7EXAMPLE` with a secret, or a GitHub token pattern) comes back with `[REDACTED:<rule-id>]` and the right count; a clean text comes back unchanged with 0; same input twice → same output. (REQ-0015)
- `TestMaskAndBuild_ScanBeforeWrite`: with a scanner stub that fails, nothing is returned and a `Store` in a temp dir stays empty; with secrets, the stored root intent and the hashed bytes are the masked text, and `root_intent.redactions` matches. (REQ-0015)
- `TestStore_RootIntentVerbatim`: the root-intent record's bytes equal the masked text exactly (including trailing newlines and non-ASCII), `root_intent.sha256` is its SHA-256, file mode 0600; a mismatched hash is refused; an existing lock id is refused. (REQ-0006)
- `TestBuild`: fields set as above, `ratified_at` in UTC with `Z`, `lock.Validate` empty, `lock.VerifyHash` true; an invalid input (e.g. 0 criteria) is an error.
- `TestMarshal_RoundTrip` (in `internal/lock`): `Parse(Marshal(l))` equals `l` and hashes the same.
- Extend the S-01 tests for `redactions` (missing and -1 are problems).

The red commit may contain stubs that compile and return zero values, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add `tests:` to REQ-0006 and REQ-0015 using the ids exactly as they appear in `build/junit.xml`. Link a claim only to the test that proves it.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `internal/ratify/**`
- `internal/lock/**` (only the `redactions` field, `Marshal`, their tests and fixtures)
- `schema/intentgate.lock.v1.json`
- `go.mod`, `go.sum`
- `claims/REQ-0006.yml`, `claims/REQ-0015.yml`: the `tests:` key only.

## Forbidden

Everything else, including `internal/footprint/**`, `docs/**`, `.req/**` and `scripts/**`. If a forbidden file must change, stop and report.

## Out of scope

- The Claude Code hook adapter, pending records, transcript parsing (S-04, REQ-0002).
- `intentgate lock --from-plan`, `--issue`, `amend`, the TTY confirmation, ULIDs, git identity (S-05, REQ-0003/0004/0005).
- The plan review (plan-review slice).
