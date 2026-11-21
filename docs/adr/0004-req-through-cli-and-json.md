# ADR 0004: Integrate with req through its CLI and JSON only

**Status:** Accepted (2026-11-21)

## Context

req plays two roles here. It manages this project's requirements now (`claims/`, test links, satisfaction links), and v2 §5.1 plans a product integration where req's deterministic drift detection (`req diff`, `req satisfy`) tells IntentGate which criteria to re-judge.

## Decision

- IntentGate calls req as an external command and reads its `--format json` documents (`req.diff/1`, `req.satisfy/1`, `req.gate/2`). It never links req as a library.
- req's exit contract (0 / 1 / 3 / 2) is adopted for the `intentgate` CLI as well (`docs/design/core-contracts.md` §3).
- For this repository: every requirement is a req claim, every design element declares `req-satisfies:` links, and core tests are linked through `tests:` so `req gate` can go green.

## Consequences

- No language or version coupling: IntentGate can pin a req release and treat its JSON schemas as the contract.
- The product integration (Phase 3) is an adapter that maps req's `SUSPECT` links onto suspect criteria. "Suspect" means the same in both tools (`docs/glossary.md`).
- Bugs found in req while dogfooding are reported upstream (so far #116, #117, #118).
