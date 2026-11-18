# S-01: intent lock core (parse, validate, hash, validity)

**Base commit:** the commit that adds this spec   **Branch:** feature/s-01-lock-core
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The first slice: the pure `internal/lock` package. Everything else (ratification, the footprint check, the PR check) reads locks through it.

## Criteria (claims this slice must satisfy)

- REQ-0007: validation rejects a lock with 0 or more than 8 criteria.
- REQ-0008: validation rejects a criterion with a missing or empty footprint, or a glob that doesn't parse.
- REQ-0009, REQ-0010, REQ-0011: validation requires `ratifier`, `ratified_at` (RFC 3339, UTC) and `method` (one of `hook`, `cli-confirm`, `issue`).
- REQ-0012: the hash covers the root intent hash, criteria, footprints and the ratification record, and is computed from the canonical form, so reformatting the YAML doesn't change it but any content change does.
- REQ-0014: the lock schema has no field for root intent text; unknown keys are rejected, so `root_intent.text` can't be committed.
- REQ-0022: a lock is valid for at most 14 days from `ratified_at`, judged against an evaluation time passed in by the caller.

## Read first

- `docs/design/intent-lock.md` (all of it: schema §2, canonical form §3, identity §4, validity §5)
- `docs/design/core-contracts.md` §1–§2 (purity, evaluation time)
- `docs/design/README.md` §2 (the PURE package rule)

## Choices fixed here

- Package `intentgate/internal/lock`. Pure: no file I/O, no clock, no network, no randomness. Callers pass bytes in and an evaluation time.
- API (names may be adjusted, shape may not):
  - `Parse(data []byte) (Lock, error)`: YAML to struct, rejecting unknown keys (`gopkg.in/yaml.v3`, `KnownFields(true)`).
  - `Validate(l Lock) []Problem`: every problem, not just the first, sorted.
  - `Hash(l Lock) (string, error)`: `"sha256:" + hex`, over the canonical JSON of the lock with `hash` removed.
  - `VerifyHash(l Lock) bool`.
  - `ValidAt(l Lock, at time.Time) bool`: false when `at` is more than 14 days after `ratified_at`.
- Canonical JSON: implement in-house, the RFC 8785 subset the lock needs (objects with keys sorted by UTF-16 code units, arrays, strings with RFC 8785 escaping, integers). Reject floats. No dependency for this.
- Globs: validate with `github.com/bmatcuk/doublestar/v4` (`doublestar.ValidatePattern`), after stripping a leading `!`.
- `id`: validate as a 26-character Crockford base32 ULID string. Generating ids is not this slice.
- Dependencies: `gopkg.in/yaml.v3` and `github.com/bmatcuk/doublestar/v4` only, pinned in `go.mod`/`go.sum`.
- A JSON Schema `schema/intentgate.lock.v1.json` matching the Go validation (draft 2020-12). It's the public contract; the Go code is the enforcement.

## Tests first (committed red)

Table-driven tests in `internal/lock`, with fixtures under `internal/lock/testdata/`. At minimum:

- `TestValidate_CriteriaCount`: 0 and 9 criteria are problems; 1 and 8 are fine. (REQ-0007)
- `TestValidate_Footprint`: missing, empty, and unparseable globs are problems; `!`-prefixed globs are allowed. (REQ-0008)
- `TestValidate_Ratification`: missing ratifier, missing or non-UTC `ratified_at`, unknown method are problems. (REQ-0009, 0010, 0011)
- `TestHash_StableUnderReformatting`: the same lock with keys reordered and different whitespace hashes identically. (REQ-0012)
- `TestHash_ChangesWithContent`: changing any one of root intent hash, a criterion text, a footprint glob, the ratifier, or the method changes the hash. (REQ-0012)
- `TestVerifyHash`: a correct hash verifies; an edited lock doesn't.
- `TestParse_RejectsUnknownKeys`: a lock carrying `root_intent.text` fails to parse. (REQ-0014)
- `TestValidAt`: exactly 14 days is valid, 14 days and 1 second is not. (REQ-0022)
- `TestCanonicalJSON`: known-answer cases for key order (including non-ASCII keys), string escaping, and float rejection.

The red commit may contain stubs that compile and return zero values, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add `tests:` to REQ-0007, 0008, 0009, 0010, 0011, 0012, 0014 and 0022, using the ids exactly as they appear in `build/junit.xml` (`classname::name`). Link a claim only to the test that proves it. Leave a claim unlinked rather than link it weakly, and say so in the report.

## Verify

```bash
scripts/check.sh   # must end "verdict: GREEN"
```

`req gate` is report-only for now; the linked claims should no longer appear as "no linked test" in `build/check/req-gate.txt`.

## Footprint (allowed paths)

- `go.mod`, `go.sum`
- `internal/lock/**`
- `schema/intentgate.lock.v1.json`
- `claims/REQ-0007.yml`, `claims/REQ-0008.yml`, `claims/REQ-0009.yml`, `claims/REQ-0010.yml`, `claims/REQ-0011.yml`, `claims/REQ-0012.yml`, `claims/REQ-0014.yml`, `claims/REQ-0022.yml`: the `tests:` key only. Never touch `statement`, `pins` or anything else.

## Forbidden

Everything else, including `docs/**`, `.req/**` and `scripts/**`. If a forbidden file must change (for example, the design is wrong), stop and report.

## Out of scope

- Writing locks to disk, the local root-intent store, ULID generation, secret scanning (S-03, ratification).
- Amendments (`amendments:` may be parsed as a list but needs no validation beyond its shape).
- The footprint matching itself (S-02).
