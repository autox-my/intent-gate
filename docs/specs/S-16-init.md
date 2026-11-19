# S-16: `intentgate init`

**Base commit:** the commit that adds this spec   **Branch:** feature/s-16-init
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

Set a repository up for IntentGate in one command, so sessions start in plan mode and agents write a well-formed `intentgate` block. Closes the gaps the 2026-11-19 hand check found: a session outside plan mode never locks, and a model wrote the block with `footprint:` at the wrong level. Design: ratification.md §1.1 ("`intentgate init` prints it; later slice") and §2.

## Criteria

No requirement changes: this is the setup step the design already names. REQ-0002 (hook) and REQ-0004 (lock from a plan file) are what it serves; their links stay.

## Choices fixed here

- `intentgate init [--agent claude-code|generic] [--dry-run]` (default `claude-code`), run in a git repo (the top level is resolved with `gitx.TopLevel`; outside a repo → exit 2). It never needs a terminal and never writes a lock.
- `--agent claude-code` writes or merges `.claude/settings.json`:
  - adds the two hooks exactly as `hook claude-code print-config` prints them, and sets `permissions.defaultMode` to `"plan"`;
  - merges into an existing file: keeps every other key and every other hook; an existing `ExitPlanMode` entry that already runs `intentgate hook claude-code pre|post` is left as is (no duplicate); an existing `permissions.defaultMode` other than `plan` is kept and reported ("left defaultMode=… as is; set it to plan to start sessions in plan mode");
  - JSON written with 2-space indent and a trailing newline; a file that isn't valid JSON is never overwritten (exit 3, naming the file).
- Agent instructions, for both agents: a marked section between `<!-- intentgate:begin -->` and `<!-- intentgate:end -->` in `AGENTS.md` if it exists, else `CLAUDE.md` if it exists, else a new `AGENTS.md`. Running again replaces the section in place (idempotent). Content:
  - one paragraph: plans end with exactly one ` ```intentgate ` block; criteria are checkable outcomes; footprints are repo-relative globs;
  - the exact template (2-space indents, `footprint` inside each criterion) as a fenced example;
  - `claude-code`: "Start work in plan mode; approving the plan writes the intent lock." `generic`: "Write the plan to a file, then ask the human to run `intentgate lock --from-plan <file>` before you change code."
- `.intentgate.yml`: created with `version: 1` only if absent; never modified.
- Output: one line per file (`created`, `updated`, `unchanged`, `kept`), then the next step. `--dry-run` prints the same lines and writes nothing. Exit 0 on success, 2 usage, 3 on an unreadable or invalid existing file.
- The top-level usage text lists every command: `hook`, `lock`, `amend`, `verify`, `publish`, `stats`, `init` (today `stats` and `publish` are missing).
- Process map: add `init` to `docs/process/sdlc-map.yml` (the BUILD "Plan mode" entry's `commands:`), and `stats` / `publish` where the map names them in prose; re-render `docs/process/sdlc-map.md`.
- `README.md`: a "Set up a repository" section with `intentgate init`.
- Review 1 amendments (binding):
  - Markers inside a fenced code block in the instructions file are text, not markers: the scan skips fenced regions (backtick and tilde fences).
  - All-or-nothing writes: every new file content is first written to a temp file beside its target; only when all temps exist are they renamed into place. Any error before the renames removes the temps and leaves every file as it was (the README's "exit 3 means nothing was written" stays true).
  - A target that is a symlink is written through only if it resolves inside the repository's top level; a symlink resolving outside it, or a dangling one, is exit 3 naming the file.
  - An existing `PreToolUse`/`PostToolUse` entry with any matcher that already runs `intentgate hook claude-code pre|post` counts as present (no duplicate).
  - The instructions section uses the file's line ending (CRLF if the file's first line ends in CRLF).
  - New hook entries use the same key order as `print-config` (`matcher` before `hooks`).
- Dependencies: none new.

## Tests first (committed red)

In `cmd/intentgate`, temp git repos:

- `TestInit_FreshRepo`: creates `.claude/settings.json` (hooks + `defaultMode: plan`, parses as JSON), `AGENTS.md` with one marked section containing the template, `.intentgate.yml`; exit 0.
- `TestInit_MergesExistingSettings`: other keys and other hooks kept; running twice gives byte-identical files and `unchanged` lines; an existing `defaultMode: acceptEdits` is kept and reported.
- `TestInit_InvalidSettings`: a broken `.claude/settings.json` → exit 3, file untouched.
- `TestInit_InstructionsFile`: prefers `AGENTS.md`, then `CLAUDE.md`; replaces its own section in place and leaves the rest of the file byte-identical.
- `TestInit_Generic`: no `.claude/` written; the section names `intentgate lock --from-plan`.
- `TestInit_DryRun`: nothing written; same lines printed.
- `TestInit_TemplateDrafts`: the template in the written section, filled with a sample criterion, passes `ratify.DraftCriteria` (so the block agents copy is valid).
- `TestUsage_ListsAllCommands`: the usage text names all seven commands.

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one assertion failure in the report.

## Verify

```bash
scripts/check.sh   # from the worktree root; GREEN, incl. "process map" and req check
```

## Footprint (allowed paths)

- `cmd/intentgate/**`
- `docs/process/sdlc-map.yml`, `docs/process/sdlc-map.md`
- `README.md`

## Forbidden

Everything else, including `.claude/**` of this repo, `.intent/**`, `claims/**`, `decisions/**`, `baselines/**`, `.req/**`, `scripts/**`. If a forbidden file must change, stop and report.

```intentgate
criteria:
  - text: "intentgate init sets up a repo: Claude Code hooks and plan mode in .claude/settings.json, an intentgate block template in the agent instructions, and .intentgate.yml"
    footprint: ["cmd/intentgate/**"]
  - text: "The usage text lists every command, and the process map and README name init"
    footprint: ["cmd/intentgate/**", "docs/process/sdlc-map.yml", "docs/process/sdlc-map.md", "README.md"]
```
