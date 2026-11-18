# S-07: PR check core (verify, policy, report)

**Base commit:** the commit that adds this spec   **Branch:** feature/s-07-verify-core
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The pure decision logic of the PR check: from the PR's inputs (as values), decide skip / no lock / lifecycle / footprint / outcome, map it to a conclusion and comment decision, and render the `intentgate.verify/1` JSON, SARIF and Markdown. No git, no network, no clock. The CLI (`intentgate verify`) and the Action are later slices.

## Criteria (claims this slice must satisfy)

- REQ-0021: a lock whose `base_ref` differs from the PR's current base branch is suspect (`retargeted`).
- REQ-0023: a lock whose hash doesn't verify has all its criteria suspect (`hash-mismatch`).
- REQ-0032: the core's input has no field for the PR title, description or commit messages, so criteria can't come from them.
- REQ-0033: no lock → no comment.
- REQ-0034: a PR by a bot (author type `Bot`, or a login in the skip list) is skipped: nothing published.
- REQ-0036: footprint findings render as SARIF 2.1.0.
- REQ-0038: a clean result posts no new comment.
- REQ-0039: every outcome concludes `success` or `neutral`.
- REQ-0040: identical inputs give byte-identical JSON, SARIF and Markdown.

## Read first

- `docs/design/pr-check.md` (all), `docs/design/core-contracts.md` §1, §3, §4
- `docs/design/footprint-check.md` §1, §4 (now with `skip_authors`), §6
- `docs/design/intent-lock.md` §5
- public APIs of `internal/lock` and `internal/footprint`

## Choices fixed here

- Packages, all PURE (no I/O, clock, randomness, network; output lists sorted by stable keys; no map iteration for order):
  - `internal/verify`: `Run(in Input) Result`.
  - `internal/policy`: `Decide(r verify.Result) Decision`.
  - `internal/report`: `JSON(r) []byte`, `SARIF(r) []byte`, `Text(r) string`, `CheckSummary(r, d) string`, `Comment(r, d) string`.
- `verify.Input` (exactly these fields; nothing about title, body, description, commits or messages):
  `BaseBranch, Author, AuthorType string; EvalTime time.Time; Changed []footprint.ChangedFile; LockFiles []LockFile{Path string; Status footprint.Status; OldPath string; Data []byte}; Config, GitAttributes []byte`. `LockFiles` are the changed paths under `.intent/` ending in `.yml`, with their content at head (nil when deleted).
- Order in `Run`:
  1. Skip: `AuthorType == "Bot"`, or `Author` in `dependabot[bot]`, `renovate[bot]` or the config's `skip_authors` → `Outcome: skipped`, nothing else computed. (`AuthorType` is checked before the config is parsed.)
  2. Config: `footprint.ParseConfig` + `footprint.NewImplicit`; an error → outcome `unverifiable` with the reason.
  3. Discovery (pr-check.md §2): `added` lock files are this PR's locks. `modified` or `renamed` ones are an existing lock being changed: reason `modifies-existing-lock`, the lock (at head) is suspect. `deleted` ones are ignored. No lock files left → `Outcome: no-lock`.
  4. Each lock: `lock.Parse` + `lock.Validate`, and the file name must be `.intent/<id>.yml`. A failure makes that lock `unverifiable` (its problems listed), and it contributes no criteria. Otherwise suspect reasons, sorted: `expired` (`!lock.ValidAt(l, EvalTime)`), `hash-mismatch` (`!lock.VerifyHash`), `modifies-existing-lock`, `retargeted` (`base_ref != BaseBranch`). Suspect criteria are still footprint-checked.
  5. Footprint: `footprint.Check(Changed, criteria of all usable locks (lock order: by path), implicit)`.
  6. Outcome, by precedence: `unverifiable` (any lock or the config) > `findings` (any finding or any suspect criterion) > `clean`. Exit code (core-contracts §3): skipped 0, no-lock 0, clean 0, findings 1, unverifiable 3.
- `policy.Decision{Publish bool; Conclusion string; Comment CommentMode; Title string}`, `CommentMode` one of `none`, `upsert`, `update-if-exists`:
  skipped → `Publish: false`; no-lock → neutral, `none`, title "No intent lock in this pull request"; clean → success, `update-if-exists` (the comment, if one already exists, becomes "All criteria inside their footprints"); findings → neutral, `upsert`; unverifiable → neutral, `upsert`. `Conclusion` is never anything but `success` or `neutral`.
- `report`:
  - JSON `intentgate.verify/1`: `{schema, outcome, exit_code, locks: [{path, id, revision, suspect: [..], problems: [..], criteria: [{id, text, footprint}]}], findings: [footprint finding shape]}`; keys in a fixed order, 2-space indent, trailing newline; no time, host or duration. Add `schema/intentgate.verify.v1.json` (draft 2020-12) matching it.
  - SARIF 2.1.0: one run, tool `IntentGate` with the version passed in by the caller (an argument, not a build-time global), rule `intentgate/outside-footprint`; one result per finding with `level: "warning"`, `locations[0].physicalLocation.artifactLocation.uri` = the path, no region, `message.text` naming the nearest criterion and the fix command. Results in path order.
  - Comment: starts with `<!-- intentgate:pr-comment -->\n`; lists suspect locks with reasons, unverifiable problems, and findings with their fix commands. `CheckSummary`: criteria with suspect markers, findings, and for unverifiable the reasons. `Text` renders the JSON document's content, not a separate decision path.
- `skip_authors`: add to `footprint.ParseConfig` as a top-level list of non-empty strings (unknown keys still rejected); footprint itself ignores it.
- Dependencies: none new.

## Tests first (committed red)

Table-driven, fixtures under `internal/verify/testdata/`:

- `TestRun_Retargeted` (REQ-0021), `TestRun_HashMismatch` (REQ-0023; the lock's criteria all suspect, still footprint-checked), `TestRun_Expired`, `TestRun_ModifiesExistingLock`, `TestRun_UnverifiableLock` (bad YAML, validation problems, file name ≠ id), `TestRun_UnverifiableConfig`.
- `TestRun_SkipsBots` (REQ-0034): type `Bot`; `dependabot[bot]`; a `skip_authors` login; a human → not skipped.
- `TestInput_HasNoDescriptionFields` (REQ-0032): reflect over `verify.Input` (recursively) and fail on any field whose name contains `Title`, `Body`, `Description`, `Commit` or `Message`.
- `TestDecide_NoLockNoComment` (REQ-0033), `TestDecide_CleanNoNewComment` (REQ-0038), `TestDecide_AdvisoryConclusions` (REQ-0039: every outcome → `success` or `neutral`; skipped → not published).
- `TestSARIF` (REQ-0036): known-answer SARIF for two findings; valid against the SARIF 2.1.0 shape you assert (version, `$schema`, runs, rule id, uri, no region).
- `TestGolden_Deterministic` (REQ-0040): for ≥ 4 fixture cases (clean, findings + suspect, no lock, unverifiable), JSON, SARIF, check summary and comment equal golden files under `internal/verify/testdata/golden/` byte for byte; running twice and with `Changed`/`LockFiles` shuffled gives identical bytes. `-update` flag regenerates goldens (never in CI).
- `TestJSONSchema`: the JSON output for each golden case has exactly the keys the schema declares.
- `TestParseConfig_SkipAuthors` in `internal/footprint`.

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add `tests:` (ids exactly as in `build/junit.xml`): REQ-0021 `TestRun_Retargeted`; REQ-0023 `TestRun_HashMismatch`; REQ-0032 `TestInput_HasNoDescriptionFields`; REQ-0033 `TestDecide_NoLockNoComment`; REQ-0034 `TestRun_SkipsBots`; REQ-0036 `TestSARIF`; REQ-0038 `TestDecide_CleanNoNewComment`; REQ-0039 `TestDecide_AdvisoryConclusions`; REQ-0040 `TestGolden_Deterministic`.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `internal/verify/**`, `internal/policy/**`, `internal/report/**`
- `schema/intentgate.verify.v1.json`
- `internal/footprint/config.go`, `internal/footprint/footprint_test.go` (only `skip_authors`)
- `claims/REQ-0021.yml`, `REQ-0023.yml`, `REQ-0032.yml`, `REQ-0033.yml`, `REQ-0034.yml`, `REQ-0036.yml`, `REQ-0038.yml`, `REQ-0039.yml`, `REQ-0040.yml`: the `tests:` key only.

## Forbidden

Everything else, including `cmd/**`, `internal/lock/**`, other `internal/footprint/**` files, `docs/**`, `.req/**` and `scripts/**`. If a forbidden file must change, stop and report.

## Out of scope

- `intentgate verify` (git diff, lock discovery from git, flags, `--replay`, evaluation time from the head commit) and the GitHub Action, check run, comment upsert, SARIF upload (next slices; REQ-0035, REQ-0037).
- The audit event (audit-metrics.md, REQ-0041/0042).
