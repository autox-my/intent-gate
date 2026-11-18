# ADR 0001: Go for the product, Python for offline evaluation

**Status:** Proposed (language agreed 2026-11-18)

## Context

IntentGate ships as a CLI that runs in three places: inside agent hooks on every plan approval, in CI on every push, and on air-gapped runners. It also needs a GitHub integration, and in Phase 1 an evaluation harness for labeling and calibration. req's test-link workflow only needs a JUnit report, so it doesn't constrain the language.

## Decision

- **Go** for everything that ships: the `intentgate` binary, the GitHub Action's logic, and later the GitHub App.
- **Python** (managed with `uv`) under `eval/` for the Phase 1 labeling, seeded-mutation and calibration harness. It never runs in the product's path.

## Reasons

- One static binary, cross-compiled trivially: fast hook start-up, simple CI and air-gapped installs.
- Mature libraries for what Phase 0–1.5 needs: `go-github` and `ghinstallation` for GitHub, `doublestar` for globs, gitleaks' detection library for the secret scan (REQ-0015).
- open-code-review is written in Go, which may let Phase 1 reuse its deterministic packet-building code. **Check its license, and whether that code is importable (not under `internal/`), before relying on it.**
- Python has the best tools for calibration maths and labeling work, which is offline research tooling.

## Consequences

- Tree-sitter (for Phase 1 symbol extraction) needs cgo in Go, which complicates static cross-compilation. Revisit when Phase 1 is designed; options are cgo with musl builds, a WASM build of tree-sitter, or running symbol extraction as a separate step.
- Tests produce JUnit XML through `gotestsum --junitfile` so `req gate --test-report` can read them.

## Alternatives considered

- **Rust:** comparable binary story and native tree-sitter, slower iteration, and the req alignment turned out not to matter (we integrate through the CLI). A reasonable second choice.
- **Python for everything:** fastest to prototype, but slow hook start-up, harder distribution as a CLI, and weaker static guarantees for a component sold on determinism.
