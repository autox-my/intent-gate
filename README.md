# IntentGate

IntentGate locks the intent behind agent-written changes and checks pull requests against it.
Before an agent implements anything, a human ratifies its plan as an **intent lock**: the root
intent verbatim, a few atomic criteria, and the path globs (the footprint) each criterion may
touch. The pull request check then compares the diff to the lock. The first check is
deterministic: it reports every changed file that falls outside every criterion's footprint.
Calibrated semantic verification comes later.

## Status

Phase 0, **advisory only**. Nothing IntentGate does can block a merge: the PR check never
concludes `failure`, and a pull request without a lock gets a `neutral` check, never a guess
derived from its title or description.

## Install

```sh
go install github.com/autox-my/intent-gate/cmd/intentgate@<ref>
```

Use a tag or a full commit SHA for `<ref>`. Go 1.23 or later is required.

## Set up a repository

```sh
intentgate init [--agent claude-code|generic] [--dry-run]
```

Run it once at the top of a git repository. For `claude-code` (the default) it adds the two hooks
and `permissions.defaultMode: plan` to `.claude/settings.json`, merging into what is there; for
both agents it writes a marked section into `AGENTS.md` (else `CLAUDE.md`) with the `intentgate`
block template, and creates `.intentgate.yml` if it is missing. It is safe to run again, never
writes a lock, and `--dry-run` prints what it would do without writing. Exit 3 means an existing
file is invalid and nothing was written.

## Lock intent

A lock is written only after a human confirms it: by typing `yes` at a terminal (there is no
flag that skips it), or, for the hook, by approving the plan in Claude Code. It is committed as `.intent/<id>.yml` in the pull request
branch.

1. **From a plan:** `intentgate lock --from-plan <plan.md> [--prompt <file>]`
2. **From an issue:** `intentgate lock --issue <n> [--repo <owner/name>]`
3. **From an agent hook:** with the Claude Code hooks in place (`intentgate hook claude-code
   print-config` prints them), approving a plan in Claude Code writes the lock; your click on
   Claude Code's own approval prompt is the confirmation.

A plan declares its criteria in one fenced `intentgate` block, with a `text` and a `footprint`
(path globs) for each, at most 8 in all.

To widen a criterion's footprint afterwards, or to renew a lock (a lock is valid for 14 days),
use `intentgate amend <id> (--criterion <Cn> --add <glob> | --reratify)`.

## Verify

```sh
intentgate verify --base <rev> --head <rev> --base-branch <name> [--format text|json|sarif|summary|comment]
```

Exit status: 0 clean, 1 findings, 3 could not decide (an undecidable result is never a pass),
2 usage. `intentgate stats` summarises the local audit events.

## GitHub Action

[`action/`](action/README.md) is a composite Action that runs `intentgate verify` on a pull
request and publishes one check run, at most one comment, SARIF for code scanning, and the job
summary. Pin it to a full commit SHA; the Action's README has the workflow to copy.

## Requirements

Requirements are managed with [req](https://github.com/autox-my/req), not in prose.

- `claims/` holds one obligation per file, `REQ-NNNN.yml`. `req canon` lists them with their
  derived parse; `req diff --base main` shows which moved and in which direction. A human
  ratifies every direction change.
- `scripts/check.sh` is the one gate: gofmt, `go vet`, staticcheck, the tests, then `req canon`,
  `req satisfy` and `req gate` against the test report. It must end `verdict: GREEN`. CI runs
  exactly this script.
- Design history is in [`docs/`](docs/): the [v2 proposal](docs/intent-gate-proposal-v2.md), the
  two external reviews, the [glossary](docs/glossary.md), ADRs, and the specs that scoped each
  slice of work.
- Agents working in this repository follow [`AGENTS.md`](AGENTS.md).

## License

[Apache License 2.0](LICENSE).
