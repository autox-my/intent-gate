# How req runs its own development: a study for IntentGate

Studied: `requirements-management-tool` (req, by autox-my) at `main` = `15ff956` (2026-11-18), read-only.
All paths below are relative to that repository unless they start with `intent-gate/`.

## Summary

req is built by one human owner and AI agents. The process is small but strict, and almost every rule
has exactly one written home. The parts that matter most:

1. **One gate script, run everywhere.** `scripts/check.sh` is the only definition of "green". CI
   (`.github/workflows/ci.yml`) calls the same script and holds no check logic. A check that could not
   run prints `SKIP`, and CI fails the job if any `SKIP` appears: "a skipped check is never a pass".
2. **A fixed change flow, visible in git history.** For each unit of work: a committed spec
   (`docs(specs): ...`), then a commit of failing tests (`test(...): ... (red at the base)`), then the
   code (`feat(...)`), then fix commits named `(review N of PR #M)`, then a human merge. Larger
   changes start with an ADR that is merged first ("ratified by the merge of PR #N").
3. **Orchestrator / worker pattern for agents.** A small orchestrator thread writes specs; worker
   agents run in their own git worktree, get a short pointer-only brief, and must return a JSON
   "task envelope" that a script validates. A reviewer agent runs read-only on a *different model
   family*. The orchestrator checks the branch's changed files against the spec's allowed paths.
4. **TDD is enforced by evidence, not trust.** A fix must name the test proven red at the base commit
   (the actual assertion failure, not a compile error) and a list of mutations that turn a test red.
5. **req manages req.** Its own requirements are 40 claims in `claims/`, each with `tests:` links to
   real test names. The gate runs `req check --test-report target/nextest/ci/junit.xml`, so a claim
   whose linked test is missing, skipped or failing blocks the merge.
6. **Humans merge, agents never push `main`.** Branch + PR always; branch protection requires the one
   `gate` check; a human reads every PR. An earlier automated LLM review in CI was removed.
7. **Three-state outcomes and a fixed exit-code contract** (0 pass, 1 fail, 2 usage error,
   3 unverifiable) across the tool, the gate and the scripts.

Much of the rest (signed baselines, reproducible builds, SHA-pinned CI downloads, mutation testing,
benchmark corpora) is driven by req being a security- and audit-oriented tool. It is not needed by a
small Go project in Phase 0.

---

## 1. Agent / AI workflow

**Where the instructions live.** There is no `.claude/` directory and no `CLAUDE.md` in the repo
(only inside vendored crates). Everything is in `AGENTS.md` (~150 lines), with `CONTEXT.md` as the
glossary and `CONTRIBUTING.md` for humans. `AGENTS.md` was deliberately cut from 771 to 150 lines
("one home per rule", merge commit of PR #88, spec `docs/specs/M2-3-one-home-per-rule.md`):

> "**Each rule has one home**: a command's contract is its `req <cmd> --help` page, the adopter's loop
> is `docs/adopting.md`; this file restates neither." (`AGENTS.md`)

Key agent rules (`AGENTS.md`, "Agent rules"):

> "You are an autonomous workflow assistant: execute the SDLC below step by step ... if unsure, ask
> one targeted question."
> "**Vocabulary comes from `CONTEXT.md`** ... add a missing term there in the same commit."
> "**Never change the git identity** ... commits are the human's."
> "**Do not overclaim.** A seed-corpus pass is not the external-corpora falsification."
> "**Docs are claims too**: when `docs/` asserts a rule the code contradicts, fix one of them in the
> commit that exposed it."

**Hooks and skills live outside the repo.** `AGENTS.md` refers to session guards "EDH001" (refuses
writes outside the session's worktree), "EDH003" (an agent never pushes `main`) and an `edh-tdd` skill.
None of these are in the repository; they belong to the owner's agent harness. Model choice per role
is in `p3-models.md` (cheap model for features/bug fixes, Claude Sonnet for "judgment and prose" and
"hardest tasks"; a unit touching the core hashing path "runs as `hardest tasks`, never `bug-fix`").

**Orchestrator / worker pattern** (`AGENTS.md`, "Orchestration (delegated work)"):

> "One small **orchestrator** thread; the bulk goes to delegated children. **Specs are committed**
> under `docs/specs/<milestone>-*.md` before a child starts (a worktree child sees only committed
> files). Out: a pointer-only brief ... never a restated spec. Back: only the JSON envelope ...
> Writers are `delegate_task` children with `isolation: "worktree"`, one per worktree; they push a
> feature branch and open a PR, never `main`. A reviewer runs on a different model family, read-only
> ... The orchestrator checks `git diff <branch-point>...<branch> --name-only` against the spec's
> allowed paths (a stray path rejects the branch), never reads a transcript."

The pieces:

- **Spec** (`docs/specs/*.md`, ~50 of them, e.g. `docs/specs/C-1-canon-json.md`). Each has: task id,
  base commit, writer and reviewer model, the contracts it must respect, "Choices fixed here",
  "Tests first (envelope `red`)", "Mutation checks", "Verify", "Docs, one home each", and
  **"Allowed paths" / "Forbidden"** with "If a forbidden file must change, stop and report."
- **Brief** (`docs/specs/brief-template.md`): eight lines, `TASK / SPEC / BRANCH / REPORT / RED /
  VERIFY / FORBIDDEN / NOTE`. "Do not restate goal, scope, acceptance or context from the spec: that is
  duplicate tokens and a second source of truth." `RED` "is never blank".
- **Envelope** (`docs/specs/task-envelope.schema.json`, checked by
  `tools/orchestration/validate_envelope.py`, which itself is self-checked in the gate). Required:
  `task_id, status (done|blocked|failed), branch, base_commit, files_changed, tests, diff_stat,
  committed, blockers, summary`. `summary` max 10 lines, "No transcript". `blocked` must carry exactly
  one question. A `-fix` task must carry `red` and `mutations`. A `-review-` task makes no edits and
  lists findings "severity first".
- **Handoff** (`docs/handoffs/TEMPLATE.md`, example `docs/handoffs/M4-close.md`): at the end of a
  milestone, write what shipped, evidence, decisions, open items, entry conditions for the next
  milestone. "The next orchestrator thread starts from this file, not from the previous thread's
  transcript."

## 2. TDD

**Rule** (`AGENTS.md`, "SDLC"; repeated in `CONTRIBUTING.md`):

> "**TDD is mandatory for code** ...: agree the seam ..., write the failing test first, then only
> enough code; tests use public interfaces and independent oracles."
> "**Test expectations are independent**: corpus E/N labels come from the spec, never regenerated from
> the implementation."

**Evidence in history.** Recent units commit the tests separately before the code, e.g.:

```
87e61c2 docs(specs): C-1 req canon --format json ...
bf74351 test(canon): req canon --format json ... (red at the base)
d536656 feat(canon): req canon --format json ...
15ff956 Merge pull request #114 from autox-my/feature/c-1-canon-json
```

The test commit body says exactly which tests are red and why: "Every test is red at 87e61c2, where
`--format` is a usage error, except the malformed-kind guard ...". The feat commit body ends with the
gate verdict: "Full scripts/check.sh: exit 0, no SKIP."

**"Red at the base" / "probed red".** Specs say "Prove each red at the base" (`C-1-canon-json.md`).
The envelope's `red` field requires the exact test id, the base commit and "The assertion the test
produced at base, not a compile error and not \"no tests found\"." This stops the common fake: a test
that "fails" only because the function doesn't exist yet.

**Mutation checks.** Each spec lists named mutations ("Drop the lints from the document: the
text-agreement test fails"). A full mutation run (`scripts/mutants.sh`, cargo-mutants, ~2 hours) is
kept *out* of the gate: "It is evidence about the test suite, not a gate."

**Tests linked to claims** (ADR 0019 `docs/adr/0019-claims-carry-authored-test-links.md`). Each claim
lists its tests by full name, e.g. `claims/REQ-0028.yml`:

```yaml
tests:
  - "req::baseline::an_edited_claim_fails_verification"
evidence: machine
```

"a test rename must never move a claim's address, while the evidence must ride the signed Baseline so
an agent cannot quietly unlink what it cannot prove."

**JUnit via nextest.** `.config/nextest.toml`, profile `ci`, writes `target/nextest/ci/junit.xml`.
nextest was "adopted for its **JUnit artefact**, not for speed". `fail-fast = false` so "the artefact
must name every failure". Ignored tests are reported as skipped, because "absence reads as pass" is
the failure to avoid. The gate deletes any old report before the run "so a *stale* report can never
answer for a run that failed to produce one". Then `req check --test-report <junit.xml>` judges every
variant line in `.req/variants`; a machine-evidence claim with no passing linked test is UNVERIFIABLE
(exit 3), and a skipped linked test is not a pass (`src/gate.rs` lines 38-40).

**Code-to-claim links.** Source modules carry `//! req-satisfies: REQ-0024 — ...` markers; `.req/design`
names the roots and `.req/satisfied` records the human-accepted links, judged by `req satisfy` (ADR 0040).

## 3. Requirements dogfooding and the change flow

Yes: req manages req. `claims/` (40 claims), `aliases/subjects.yml`, `decisions/DEC-*.yml` (a
hash-chained decision log), `baselines/*.json` + `.minisig` (signed by the owner), `.req/variants`,
`.req/projections`, `.req/completeness`. `docs/spec.md` and `docs/trace.md` are generated projections,
"never hand-edited", and the gate fails if they are stale.

**Sources of truth, in order** (`AGENTS.md`): `docs/design.md` → `docs/adr/` → hashing spec → claim
format → `CONTEXT.md` (glossary only).

**The flow, as seen in `git log` for ADR 0056 (PRs #102-#109):**

1. Research/experiment spec and report (`docs(specs): X-1 ...`, `docs(research): X-1 ...`), PR #102.
2. ADR drafted as *proposed* (`docs(adr): 0056 ... (proposed)`), reviewed in rounds
   ("review 1 of PR #103"), merged = ratified.
3. A tiny follow-up PR flips status: "0056 is accepted (ratified by the merge of PR #103), not yet
   implemented" (PR #105).
4. Implementation split into units F-1, F-2, F-3, each its own spec → red tests → feat → review fixes →
   merge (PRs #106, #107, #108). Review fixes are their own commits: `fix(claim): ... (review 1 of PR #106)`.
5. Release PR (`chore(release): version 0.6.0`), then a docs PR recording the release.

**ADRs** (`docs/adr/0001-...md` to `0056-...md`): four-digit number, slug title stated as the decision
("0040 a design satisfies a claim at a meaning"). Header line `**Status: accepted** — ratified by the
owner's merge of PR #103 (2026-11-18); implemented by PRs #106, #107 and #108.` Statuses seen:
proposed, accepted, "accepted, not yet implemented", superseded (older ADRs use YAML front matter
`status: ...`). Sections: Context, Decision (numbered decisions, each with "Why not ..."), then
consequences / "Not decided here". Settled ADRs are never edited in passing:

> "The six tensions ... and **ADRs 0001–0009** are settled; changing one needs a superseding ADR, not an
> edit in passing (ADR 0020 is the precedent)." (`AGENTS.md`)

The spec says when no ADR is needed: "No new ADR is needed. This adds one more document of the same
family" (`C-1-canon-json.md`).

## 4. Branching, PRs, review, CI, releases

- **Branches**: `feature/<task>` (most), `docs/<topic>`, `fix/<topic>`, `chore/<topic>`, `research/<topic>`
  (from merge commits). Agents work in `worktrees/<name>`: `git worktree add worktrees/<name> -b feature/<name>`.
- **Commits**: Conventional Commits, `feat: fix: refactor: test: docs: chore:` with optional scope;
  history counts ~234 fix, 167 docs, 125 feat, 34 test. Subjects are full sentences describing the
  behaviour. Bodies state the gate result and any SKIP/UNVERIFIABLE. `Co-Authored-By:` names the model.
- **PRs**: one branch per change, merged with a merge commit (GitHub "Merge pull request #N from
  autox-my/feature/..."). There is **no PR template**; `CONTRIBUTING.md` asks: "Say what you changed,
  which tests prove it, and what you could not verify." A fix PR uses `Fixes #N` and `fix:` so release
  notes list it (ADR 0031). Issue form: `.github/ISSUE_TEMPLATE/tool-defect.yml`.
- **Review**: "**There is no automated review workflow in this repository: a human reads every pull
  request**" (`AGENTS.md`, ADR 0029 §7). History: on Forgejo, an LLM review (OpenCodeReview, `tools/ocr`,
  rules in `review/rule.json`) blocked merges; a local self-review loop was dropped as duplicate cost
  (`docs/specs/M2-1-drop-local-review-loop.md`), then the CI review was dropped on the move to GitHub
  because it "would hold an LLM credential, which does not belong in a workflow that runs on pull
  requests from strangers". The tool stays for manual use, and its output is "**unverified** ... a clean
  run means *the reviewer reported nothing*, not that nothing was there". A bake-off to choose a future
  review tool on req's own past PRs is specced in `docs/specs/review-bakeoff.md`. During agent work, the
  cross-family reviewer agent fills this role.
- **CI** (`.github/workflows/`):
  - `ci.yml`: on PR and push to main; `permissions: contents: read`, no secrets, no
    `pull_request_target`; installs pinned tools (SHA-256 checked), then
    `bash scripts/check.sh 2>&1 | tee` and fails on any `^SKIP` line. Concurrency cancels superseded PR runs.
  - `audit.yml`: weekly scheduled dependency-advisory scan, never on PRs (so an outside advisory never
    turns an unrelated PR red).
  - `release.yml`: `workflow_dispatch` only; build job (read-only) and publish job (the only one with
    write rights); never overwrites an existing asset.
  - Branch protection requires the `gate` check only: "there is no second required check, and a human
    reads the diff."
- **Releases** (`docs/releasing.md`, ADRs 0025, 0031, 0055): the owner dispatches; tag `v<VERSION>`
  from `Cargo.toml`; release notes generated from git history (`scripts/release-notes.sh`); version
  bump checked against whether identity moved (`scripts/release-version-check.sh`). Each release is
  recorded in `docs/releasing.md` afterwards.
- After merge: `git fetch origin && git pull --ff-only`.

## 5. Quality gates and conventions

- **`scripts/check.sh`** (~700 lines, heavily commented). Order: fmt → clippy `-D warnings` → nextest
  (JUnit) → `req canon` → JSON output parse checks → `req bench` (FMR = 0 on every corpus) →
  `req check --test-report` (all variants, projections, completeness) → Baseline and decision-log
  verification (no SKIP allowed) → `cargo deny` → SBOM check → `--self-check` of every Python tool →
  timed quickstart rehearsal (`tools/quickstart.sh`, fails at 300 s) → reproducible build.
  - `--fast` (~20 s) for iteration; banner says `GATE: PASS (FAST ...)` so it is never mistaken for a
    merge verdict. "**the full run is required before a commit**".
  - `plan` prints what would run and exits 3 ("a plan is not a pass").
  - "**Never pipe a check through `tail`/`grep`**: the pipeline's status is not the command's."
- **Exit-code contract**: 0 pass, 1 fail (a definite finding), 2 usage error, 3 UNVERIFIABLE (could
  not decide). Every `--help` page has an `exit:` line; `--format json` never changes an exit code; on
  exit 3 nothing is printed on stdout.
- **Three-state outcomes**: "An unreadable corpus, a degraded registry or a skipped check is reported
  as such, never folded into PASS."
- **Lint config as rule enforcement**: six targeted clippy lints in `Cargo.toml` enforce the
  determinism rules "on every path, not only on the ones reviewed" (no floats, no unwrap, no silent
  casts, every `allow` needs a reason).
- **`req selftest`** (ADR 0034): known-answer checks compiled into the binary so an installed copy can
  prove it derives what the reference build derives.
- **Bench / FMR gate**: `req bench` must report zero false merges on the seed and external corpora;
  "FAIL or UNVERIFIABLE blocks; never relax it to land a change." Results are append-only in
  `bench/results/`.
- **Version pins are provenance**: toolchain, lock file, CI tools (version + SHA-256); "never bump
  opportunistically".
- **Docs conventions**: one home per rule; `--help` is the command reference; generated docs are
  checked, never edited; "Report which corpus produced which number"; stage files by name
  (`git add <paths>`), never `git add -A`.
- **Adopter path tested**: `tools/quickstart.sh` rehearses `docs/adopting.md` offline in the gate;
  `tests/ci_template.rs` pins the CI snippet in `examples/` so the docs cannot drift from reality.

## 6. Other distinctive things

Worth copying:

- **Specs with allowed/forbidden paths** turn "scope creep" into a mechanical check on the diff.
- **Self-checking tools**: every helper script has `--self-check`, run by the gate.
- **Tests that pin process files**: `tests/release_workflow.rs` fails if `release.yml` gains a trigger
  other than manual dispatch; `tests/audit_workflow.rs` fails if two pinned versions disagree.
- **"Accepted risk, stated rather than hidden"** (M2-1 spec): when something is dropped, write down what
  is no longer detected.
- **Commit message states the gate verdict and any skip.**
- **Measure before deciding** (e.g. nextest adopted after timing it; the review bake-off).

Not worth copying for IntentGate Phase 0:

- Signed baselines, minisign, trust anchor from `origin/main`, decision hash chain.
- Reproducible builds, SBOM, dependency-licence gate, SHA-pinned tool downloads with API fallbacks.
- 2-hour mutation runs, multiple benchmark corpora, append-only bench results.
- Very long, defensive comments in every script; ~700-line gate script.
- Multi-model routing table (`p3-models.md`) beyond "writer and reviewer are different model families".
- The `prompts/` folder of one-off packs and reports (historic clutter, not process).
- An LLM reviewer in CI (req itself removed it).

---

## Recommended for IntentGate

IntentGate already has `claims/`, `.req/design`, `.req/satisfied`, `docs/adr/0001-0005` and an
`AGENTS.md` with req's agent snippet. Below, "red" means a test that fails for the right reason
before the code exists.

### Adopt now (Phase 0)

1. **One gate script: `scripts/check.sh`**, used locally and in CI unchanged. Steps: `gofmt -l` (fail
   if any output), `go vet ./...`, `staticcheck` or `golangci-lint` (pick one, pin its version),
   `gotestsum --junitfile out/junit.xml -- ./...`, `req canon --fail-on-flag`, then
   `req check --test-report out/junit.xml` once all claims have tests. Print `SKIP` for missing tools;
   CI fails on any `SKIP`.
2. **Exit-code contract 0/1/2/3** for the IntentGate CLI and the gate script, stated in each command's
   `--help`. Fits Phase 0 "advisory" well: "could not decide" (3) is never shown as pass.
3. **Tests first, with red evidence.** Commit tests separately as `test(scope): ... (red at the base)`
   with the failing assertion in the body, then `feat(scope): ...`. Red must be a real assertion
   failure, not a compile error (in Go: add a stub that compiles and returns the wrong answer).
4. **Link every claim to Go tests** via `tests:` using the names gotestsum writes into JUnit
   (classname = package path, name = `TestXxx` or `TestXxx/subcase`; check one real report first and
   pin the format with a test). Add `// req-satisfies: REQ-NNNN` markers in package docs for
   `.req/satisfied`.
5. **Branch + PR, humans merge.** `feature/`, `fix/`, `docs/`, `chore/` branches; Conventional Commits;
   agents never push `main`; branch protection requires one `gate` check. Commit body states the gate
   result and any skip.
6. **ADR flow**: number `NNNN-decision-as-sentence.md`, `**Status: proposed | accepted (ratified by
   merge of PR #N) | superseded by NNNN**`, Context / Decision / Why not / Not decided here. Merge the
   ADR PR before the implementation PRs. Settled ADRs change only by a superseding ADR.
7. **"One home per rule" in `AGENTS.md`**: keep it short; put the glossary in `docs/glossary.md`
   (already done), command contracts in `--help`, and do not repeat rules across files.
8. **CI hygiene from day one**: `permissions: contents: read`, no secrets, no `pull_request_target`,
   cancel superseded PR runs, pin the Go version (`go.mod` `toolchain` line) and tool versions.

### Adopt later (when agent delegation or the team grows)

9. **Specs under `docs/specs/<id>-*.md`** with "Tests first", "Mutation checks" (a few named ones, done
   by hand), "Verify", and **Allowed / Forbidden paths**; check the branch diff against them.
10. **Pointer-only brief + JSON task envelope** for worker agents (copy `brief-template.md` and the
    schema; a small Go or Python validator). Worker in its own git worktree; reviewer agent read-only
    on a different model family.
11. **Milestone handoff file** (`docs/handoffs/<milestone>-close.md`) so a new agent thread starts from
    a file, not a transcript.
12. **`--fast` gate mode** once the full gate exceeds about a minute; the banner must say FAST.
13. **Committed projections** (`req render --out docs/spec.md`, `req trace --out docs/trace.md`) listed
    in `.req/projections` once stakeholders read them.
14. **Tests that pin process files** (e.g. a Go test that the release workflow is dispatch-only) when
    there is a release workflow.
15. **Release by manual dispatch** with notes generated from Conventional Commit history (e.g.
    goreleaser or a small script), when there is something to ship.
16. **Occasional mutation testing** (e.g. `go-mutesting`/`gremlins`) outside the gate, as evidence.

### Skip (for now)

17. Signed baselines, decision hash chain, trust-anchor tricks, reproducible-build proof, SBOM and
    licence gate.
18. Benchmark corpora and false-merge-style hard gates (no equivalent in Phase 0).
19. An LLM review bot in CI (req removed theirs for credential and noise reasons); a human reads PRs.
20. Per-role model routing tables and the `prompts/` pack style.
