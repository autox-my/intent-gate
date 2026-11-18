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
