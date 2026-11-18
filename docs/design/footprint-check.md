# Design: the footprint check

<!-- req-satisfies: REQ-0024 -->
<!-- req-satisfies: REQ-0025 -->
<!-- req-satisfies: REQ-0026 -->
<!-- req-satisfies: REQ-0027 -->
<!-- req-satisfies: REQ-0028 -->
<!-- req-satisfies: REQ-0029 -->
<!-- req-satisfies: REQ-0030 -->
<!-- req-satisfies: REQ-0031 -->

The footprint check is Phase 0's main product: "these files are outside every criterion's footprint." It lives in `internal/footprint` as a pure function and makes no model call or network request (REQ-0031).

```go
func Check(changed []ChangedFile, criteria []Criterion, implicit Implicit) []Finding
```

## 1. Inputs

- **Changed files:** from `git diff --name-status --find-renames base...head`, done by the `gitx` adapter. Each file has a status (added, modified, deleted, renamed). For a rename, both old and new paths are checked. Paths under `.intent/` are excluded (lock files are checked by the PR check, not here).
- **Criteria:** the union across every lock found in the PR (pr-check.md §2).
- **Implicit footprints:** the defaults (§3) merged with `.intentgate.yml` (§4).

## 2. Matching rule (REQ-0024)

A path is **inside a criterion's footprint** when it matches at least one positive glob of that criterion and none of its `!` globs. Globs use `doublestar` semantics (`**` crosses directories), are matched against repo-relative paths with `/` separators, and are case-sensitive.

A changed path is **reported** when it is inside no criterion's footprint and inside no implicit footprint. Findings are sorted by path, so the output is deterministic.

## 3. Implicit footprints (defaults)

| Category | Claim | Default rule |
|---|---|---|
| Paired tests | REQ-0025 | A test file is in the footprint of the criterion containing its subject. Pairing rules: Go `x_test.go` ↔ `x.go` (same dir); JS/TS `x.test.ts`, `x.spec.ts`, `__tests__/x.ts` ↔ `x.ts`; Python `test_x.py`, `x_test.py`, `tests/test_x.py` ↔ `x.py`; Rust `tests/x.rs` ↔ `src/x.rs`. The subject only needs to exist in the footprint, not in the diff. |
| Fixtures and snapshots | REQ-0026 | `**/testdata/**`, `**/fixtures/**`, `**/__fixtures__/**`, `**/__snapshots__/**`, `**/*.snap`, `**/*.golden` |
| Lockfiles | REQ-0027 | `go.sum`, `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, `bun.lockb`, `Cargo.lock`, `poetry.lock`, `uv.lock`, `Pipfile.lock`, `Gemfile.lock`, `composer.lock` (at any depth) |
| Generated and vendored | REQ-0028 | `vendor/**`, `third_party/**`, `**/node_modules/**`, `**/*.pb.go`, `**/*_gen.go`, `**/*.generated.*`, `**/dist/**`, and any path marked `linguist-generated` or `linguist-vendored` in `.gitattributes` |

Using GitHub's `linguist-generated` / `linguist-vendored` markers means repos that already hide generated files from PR diffs get the same treatment for free.

## 4. Configuration (REQ-0029)

```yaml
# .intentgate.yml
version: 1
implicit_footprints:
  add:
    - "docs/**"            # this repo treats docs as always in scope
  remove:
    - "**/dist/**"         # this repo commits dist/ deliberately and wants it checked
  test_pairing: true       # false disables the paired-test rule
```

`add` and `remove` adjust the default globs; there is no way to replace the defaults wholesale in Phase 0. A malformed file is UNVERIFIABLE (exit 3), never "use the defaults".

## 5. Footprint amendment (REQ-0030)

Every finding carries the exact command that fixes it if the change was intended:

```text
intentgate amend 01JD8Z6Q… --criterion C2 --add "internal/gitx/**"
```

`amend` uses the same interactive confirmation as `lock` (ratification.md §3): one prompt, one `yes`. It adds the glob, appends an entry to `amendments:` (who, when, which criterion, which glob, method), increments `revision` and re-hashes. This is "one step" from the developer's point of view, and it's still a ratified change. The count of amendments feeds the footprint-amendment rate (audit-metrics.md).

## 6. Finding shape

```json
{ "path": "internal/gitx/diff.go", "status": "modified",
  "nearest_criterion": "C2", "fix": "intentgate amend 01JD… --criterion C2 --add \"internal/gitx/**\"" }
```

`nearest_criterion` is the criterion whose footprint shares the longest directory prefix with the path. It's a suggestion for the amendment command, not a judgment.
