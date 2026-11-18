# Specs

One spec per slice of work, committed before a worker starts (a worktree only sees committed files). See `AGENTS.md`, "How work flows", for what a spec is and isn't.

## Template

```markdown
# S-NN: <title>

**Base commit:** <sha>   **Branch:** feature/s-NN-<slug>
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

## Criteria (claims this slice must satisfy)
- REQ-NNNN: <one line on what part of it this slice covers>

## Read first
- docs/design/<file>.md §<n>

## Choices fixed here
- <decisions the worker must not re-open>

## Tests first (committed red)
- <test name>: <what it asserts>

## Verify
scripts/check.sh   # must end "verdict: GREEN"

## Footprint (allowed paths)
- <glob>

## Forbidden
Everything else. If a forbidden file must change, stop and report.

## Out of scope
- <what the next slices do>
```
