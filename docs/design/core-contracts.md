# Design: core contracts

<!-- req-satisfies: REQ-0040 -->

## 1. Determinism (REQ-0040)

`intentgate verify` must produce byte-identical output for identical inputs. Its inputs are exactly:

| Input | Source |
|---|---|
| Lock files added in the PR | `git show head:<path>` for each discovered lock |
| Changed paths and statuses | `git diff --name-status --find-renames base...head` |
| `.intentgate.yml` and `.gitattributes` | read at `head` |
| PR base branch name and author | the CI event payload (passed as flags) |
| Evaluation time | §2 |

Rules that make this hold:

- The pure core (`lock`, `footprint`, `policy`, `report`) does no I/O, reads no clock, uses no randomness and makes no network calls.
- Every list in the output is sorted by a stable key. Go map iteration is never used for output order.
- The output contains no run timestamp, hostname or duration. Those go in the audit event, which isn't part of the verify output.
- `intentgate verify --replay <event.json>` re-runs a past decision from the inputs recorded in its audit event and must reproduce the same output.

A golden-file test suite pins the output for a set of fixture repos. It's the linked test for REQ-0040.

## 2. Evaluation time

Anything time-dependent (lock validity, REQ-0022) uses the **head commit's committer timestamp**, not the wall clock. The same commit therefore gets the same verdict on every re-run, and replay works without faking the clock. A lock that expires while a PR sits idle is reported as expired on the next push. That's acceptable: a PR that isn't moving isn't merging.

## 3. Exit codes (req's contract)

| Code | Meaning | Example |
|---|---|---|
| 0 | settled affirmatively | locks valid, no findings |
| 1 | a definite finding | files outside every footprint; suspect criteria |
| 3 | UNVERIFIABLE | lock fails validation; `.intentgate.yml` malformed; git can't resolve the base |
| 2 | usage | unknown flag; `lock` run without a terminal |

UNVERIFIABLE is never folded into 0. The CI Action maps every code to an advisory conclusion (pr-check.md §4), so a non-zero exit never fails the workflow in Phase 0.

## 4. Machine-readable output

`--format json` prints an `intentgate.verify/1` document (schema in `schema/intentgate.verify.v1.json`): the locks found, each criterion with its suspect reasons, the findings, the outcome and the exit code. The text output is a rendering of the same document, never a separate code path.
