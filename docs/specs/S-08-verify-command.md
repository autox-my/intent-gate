# S-08: `intentgate verify`

**Base commit:** the commit that adds this spec   **Branch:** feature/s-08-verify-command
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The command that runs the PR check locally or in CI: read the inputs from git, run the S-07 core, print or write its outputs, and exit with its code. The GitHub Action (S-09) is a thin wrapper around this.

## Criteria (claims this slice must satisfy)

- REQ-0040 (extend its links): `intentgate verify` run twice on the same repo and refs gives byte-identical outputs.

Supporting, end to end (already linked in the core, don't re-link): REQ-0021/0022/0023 lifecycle, REQ-0024 footprint, REQ-0032 (no PR text is read: the command has no flag for it).

## Read first

- `docs/design/core-contracts.md` §1–§4, `docs/design/pr-check.md` §1–§2
- `internal/verify`, `internal/policy`, `internal/report` (S-07), `cmd/intentgate` (`Run`, `Deps`), `internal/gitx`

## Choices fixed here

- `intentgate verify --base <rev> --head <rev> --base-branch <name> [--author <login>] [--author-type <type>] [--format text|json|sarif|summary|comment] [--out-dir <dir>]`:
  - `--base`, `--head`, `--base-branch` are required. No flag reads the PR title, body or commits.
  - Inputs, all read at `head` through `internal/gitx` (git CLI, `-z` output, no shell):
    - Changed files: `git diff --name-status -z --find-renames <base>...<head>`. Map `A`→added, `M`/`T`→modified, `D`→deleted, `R*`→renamed (old and new path), `C*`→ added (new path only) for `Changed`; for `LockFiles` (paths under `.intent/` ending `.yml`) `C*` keeps status `copied` so the core rejects it; any other letter → status as the raw letter, which the core rejects for locks and the command reports as an error for other files (exit 3).
    - Lock file content: `git show <head>:<path>` (nil for deleted).
    - `.intentgate.yml` and `.gitattributes` at the repo root: `git show <head>:<file>`; absent → nil (a missing file is not an error, any other git failure is).
    - Evaluation time: the head commit's committer time, `git show -s --format=%ct <head>`, as UTC.
  - `--format` (default `text`) selects what goes to stdout: `report.Text`, `report.JSON`, `report.SARIF`, `report.CheckSummary`, `report.Comment` (with `policy.Decide`).
  - `--out-dir <dir>` also writes all of them: `verify.json`, `results.sarif`, `summary.md`, `comment.md`, `decision.json` (`{"publish", "conclusion", "comment", "title"}` from `policy.Decision`, fixed key order). It creates the dir (0755) and overwrites these five files only.
  - SARIF tool version: `version` set with `-ldflags "-X main.version=…"`, default `dev`; `intentgate --version` prints it.
  - Exit code: the core's (0, 1 or 3); 2 for usage (missing or unknown flags, a bad `--format`); 3 when git fails (bad rev, not a repo) or an output file can't be written, with the reason on stderr. stdout carries only the selected format.
- `gitx` additions: `ChangedFiles(dir, base, head)`, `ShowFile(dir, rev, path) ([]byte, found bool, err error)`, `CommitTime(dir, rev)`. Each runs `git` with `-C dir`, a fixed argument list, `--no-pager`, `LC_ALL=C`, and no user config that changes output (`-c core.quotepath=false`, `-c diff.renames=true` explicitly, `-c color.ui=false`).
- Dependencies: none new.

## Tests first (committed red)

In temp git repos built by the test (local `user.email`/`user.name`, fixed `GIT_COMMITTER_DATE`/`GIT_AUTHOR_DATE`):

- `TestVerify_CleanAndFindings`: a branch adding a valid lock (built with `ratify`, ratified within 14 days of the fixed commit date) plus files inside and outside its footprint → exit 1, the outside file in `verify.json` findings with its fix command; a branch with only inside files → exit 0.
- `TestVerify_NoLock`: no `.intent/` change → exit 0, outcome `no-lock`, `decision.json` comment `none`.
- `TestVerify_Lifecycle`: retargeted (`--base-branch` ≠ `base_ref`), hand-edited lock (hash mismatch), lock older than 14 days at the head commit time, a modified existing lock → the right suspect reasons, exit 1.
- `TestVerify_Renames`: a renamed file checks both paths; a copied lock → unverifiable, exit 3.
- `TestVerify_Deterministic` (REQ-0040): two runs → identical bytes for all five `--out-dir` files and every `--format`; a run with a different `TZ` and `LC_ALL` → identical bytes.
- `TestVerify_Usage`: missing `--base`/`--head`/`--base-branch`, unknown flag, bad `--format` → exit 2; a bad rev → exit 3 with stderr, empty stdout.
- `TestVerify_BotSkipped`: `--author-type Bot` → exit 0, `decision.json` publish false.
- `TestGitx_ChangedFiles`: statuses and `-z` parsing incl. paths with spaces, newlines and non-ASCII; `ShowFile` found/absent; `CommitTime` from a fixed committer date.

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add `TestVerify_Deterministic` to REQ-0040's `tests:` (keep the existing link), id exactly as in `build/junit.xml`.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `cmd/intentgate/**`
- `internal/gitx/**`
- `claims/REQ-0040.yml`: the `tests:` key only.

## Forbidden

Everything else, including `internal/verify/**`, `internal/report/**`, `internal/policy/**`, `docs/**`, `.req/**` and `scripts/**`. If a forbidden file must change (for example the core lacks something), stop and report.

## Out of scope

- The GitHub Action, check run, comment upsert, SARIF upload (S-09; REQ-0035, REQ-0037).
- `--replay` and the audit event (audit slice; REQ-0041/0042).
