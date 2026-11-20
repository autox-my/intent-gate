# Glossary

IntentGate's requirements are managed with req, and the two tools share some words. Where a
word means the same thing in both, IntentGate uses req's word. Where the meanings differ,
IntentGate uses its own word, so a sentence like "each claim declares its scope" can't be read
two ways. Agreed 2026-11-18.

## IntentGate terms

| Term | Meaning | Replaces (v1/v2 docs) |
|---|---|---|
| **intent lock** | The hashed artifact created when a human ratifies an agent's plan, before implementation. Holds the root intent, the criteria, their footprints and the ratification record. | — |
| **root intent** | The human's original prompt or discussion turns, verbatim. | — |
| **criterion** (plural **criteria**) | One atomic, checkable item in an intent lock. Field name `criteria:`. | "claim" |
| **footprint** | The path globs a criterion's change may touch. | "scope" |
| **footprint check** | The deterministic check that reports changed files outside every criterion's footprint. | "scope check" |
| **footprint amendment** | A one-step, ratified addition of a path to a footprint. | "scope amendment" |
| **plan review** | The plan-vs-prompt comparison shown at ratification: omissions, additions, weakenings. | "plan diff", "plan-vs-prompt diff" |
| **PR check** | IntentGate's check run on a pull request. The product is still called IntentGate. | "the gate" |
| **model_version** | The pinned model version recorded with a verdict. | "model_pin" |

## Shared with req (same meaning)

| Term | Meaning in both |
|---|---|
| **suspect** | Needs a human re-check because something it depends on changed. A req `SUSPECT` link maps onto an IntentGate suspect criterion. |
| **UNVERIFIABLE** | Cannot be decided. Never folded into a pass. The `intentgate` CLI follows req's exit contract: 0 settled affirmatively, 1 a definite finding, 3 UNVERIFIABLE, 2 usage. |
| **ratify / ratification** | A human's act of acceptance. Never done by an agent. |

## req's terms (used only in req's sense in this repo)

| Term | req meaning |
|---|---|
| **claim** | One requirement, one file under `claims/`. |
| **scope** | Where a requirement applies (`applicability`); `scope_divergence` is a `req diff` axis. |
| **gate** | `req gate`, the release gate over claims and their linked tests. |
| **pin** | A `pins:` assertion of what a claim's statement must parse to. |
| **diff** | `req diff`, the meaning change between two revisions. |

## Artifacts: where each lives and which stage writes it

Stages are those of `docs/process/sdlc-map.md`. "Intent" and "spec" each mean more than one thing, so
each meaning has its own row.

| Term | Lives in | Written at stage |
|---|---|---|
| **intent** (project proposal) | `docs/intent-gate-proposal-v2.md` and the two reviews: once, for the whole project. | 1 INTENT |
| **intent** (root intent of a change) | The prompt, issue or pasted request. Kept masked in `~/.intentgate` on the ratifier's machine; only its hash is committed. | Product loop: the human's request, before plan approval |
| **intent** (playbook `intent.md`) | Not used here: there is no per-change `intent.md`. The nearest things are the slice spec's criteria and, in the product, the root intent. | none (Phase 1 may add it) |
| **claim** | `claims/REQ-NNNN.yml`, one requirement per file (req's sense). | 2 CLAIMS |
| **design element** | `docs/design/*.md`, linked to claims by `req satisfy`. | 3 DESIGN |
| **ADR** | `docs/adr/NNNN-*.md`: why a hard-to-reverse choice was made. | 3 DESIGN |
| **slice spec** | `docs/specs/S-NN-*.md`: the claim ids, allowed paths and red-first tests for one slice. Closest to the playbook's `plan.md`. | 4 SPEC |
| **plan** | The agent's plan, approved in plan mode, ending in an `intentgate` block. In this repo the slice spec plays that part. | Product loop: plan mode, before code |
| **intent lock** | `.intent/<id>.yml` on the PR branch: criteria, footprints, hashes, ratification record. A slice spec's `intentgate` block can be locked with `intentgate lock --from-plan`. | Product loop: written by the hook or `lock`, at human ratification |
| **Baseline** | `baselines/phase0-2.json` and its signature: the signed set of claim addresses a release is checked against. Signing is the human's act. | 8 MERGE (release) |
