# S-20: `cmd/intentgate` tests ignore the developer's plan-review environment

**Base commit:** the commit that adds this spec   **Branch:** feature/s-20-hermetic-tests
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/gemini-4-argon

A shell that exports `TYPESAFE_API_KEY` turns `scripts/check.sh` red: `TestHook_ApprovedPlanWritesLock` reaches the live Jev provider through `planreview.Select(os.Getenv)` (`cmd/intentgate/planreview.go`) and sees `plan review: complete` instead of `unavailable`. Because that test is linked to REQ-0002, `req check` fails with it. Found while signing Baseline `phase0-2` on 2026-11-20. Tests only; no product change.

## Criteria

No requirement changes. REQ-0002's linked tests must pass whatever the developer's shell exports.

## Read first

- `cmd/intentgate/planreview.go` (`Deps.reviewer`), `internal/planreview/planreview.go` (`Select`)
- `cmd/intentgate/hook_test.go` around `TestHook_ApprovedPlanWritesLock`

## Choices fixed here

- A `TestMain` in package `main` of `cmd/intentgate` unsets `TYPESAFE_API_KEY`, `TYPESAFE_BASE_URL`, `TYPESAFE_MODEL` and `INTENTGATE_PLAN_REVIEW` before `m.Run()`, so every test there (and every subprocess it starts, which inherits the environment) sees no provider unless the test injects `Deps.PlanReviewer` or sets the variables itself with `t.Setenv`.
- No product code changes: `planreview.go`, `internal/**` and `Select` stay as they are. The live check stays in `internal/planreview` (`TestJev_Live`, opt-in through `INTENTGATE_LIVE_JEV=1`).
- If an existing test in `cmd/intentgate` relied on the inherited variables, it sets them itself with `t.Setenv`; never weaken an assertion.
- Review 1 amendments (binding):
  - The child run passes `-test.v`, and `TestHermetic_PlanReviewEnv` requires its output to contain `--- PASS: TestHook_ApprovedPlanWritesLock`, so a renamed or missing test cannot pass vacuously (exit 0 with "no tests to run").
  - The child environment sets all four variables (`TYPESAFE_API_KEY=hermetic-test-key`, `TYPESAFE_BASE_URL=http://127.0.0.1:9`, `TYPESAFE_MODEL=hermetic-test-model`, `INTENTGATE_PLAN_REVIEW=on`) and also runs a new `TestHermetic_ProviderEnvCleared`, which asserts `os.LookupEnv` reports each of the four as absent; the output must contain `--- PASS: TestHermetic_ProviderEnvCleared` too. Dropping any name from `TestMain`'s list then fails the test.

## Tests first (committed red)

- `TestHermetic_PlanReviewEnv`: re-runs the test binary (`os.Args[0]`, `-test.run=^TestHook_ApprovedPlanWritesLock$`, `-test.count=1`) as a subprocess whose environment adds `TYPESAFE_API_KEY=hermetic-test-key` and `TYPESAFE_BASE_URL=http://127.0.0.1:9` (a closed port, so nothing leaves the machine), and asserts it exits 0. Guard against recursion (for example an env marker the child checks to skip this test). Red at the base: the child sees the key and the review is not `unavailable`. Quote the failure.

## Verify

```bash
scripts/check.sh                                         # must end "verdict: GREEN"
TYPESAFE_API_KEY=x TYPESAFE_BASE_URL=http://127.0.0.1:9 scripts/check.sh   # also GREEN
```

## Footprint (allowed paths)

- `cmd/intentgate/*_test.go`

## Forbidden

Everything else, including `claims/**` and every non-test file. If a forbidden file must change, stop and report.

## Out of scope

- Hermeticity of other packages (none reads the provider environment today).

```intentgate
criteria:
  - text: "cmd/intentgate tests pass whether or not the shell exports the plan-review provider variables (TYPESAFE_API_KEY, TYPESAFE_BASE_URL, TYPESAFE_MODEL, INTENTGATE_PLAN_REVIEW)"
    footprint: ["cmd/intentgate/*_test.go"]
```
