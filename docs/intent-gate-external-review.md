# IntentGate — calibrated merge gates for agent-written code

**Working title:** IntentGate
**Status:** idea for external review
**Date:** November 2026

> **Correction (2026-11-18).** The `req` row in §7 overstates the adoption cost and understates what `req` does for type 2. See `intent-gate-proposal-v2.md` §5.1 and the `req` team's clarification. This draft is kept as the reviewed version.

> **Reviewer's brief (senior DevOps engineer).** I am looking for a critique of the operational model, the CI integration, the failure semantics, and the technical bet — not a code review. The specific questions I want pressure-tested are in §13.

---

## 1. Summary

Agent-written code is outpacing human review. A pull request shows *what* changed, not whether the change still does *what was asked*. Existing AI reviewers generate plausible comments, but they are noisy, overconfident, and easy to game; they are optimized for human-readable prose, not for merge policy.

IntentGate is a merge-gate layer, not another reviewer. It captures the intent behind a change as a small set of stable **claims**, verifies each claim against the diff with calibrated atomic judgments, and converts those probabilities into an explicit, auditable **merge policy**. It uses TypeSafe's Jev for the judgment layer and a conventional LLM reviewer for explanation and discovery.

**Thesis:** review should be a set of calibrated claim checks governed by policy, not a stream of opinions.

---

## 2. Problem

- Coding agents produce larger and more frequent changes. The session that produced the change holds the intent, but reviewers only see the diff.
- Reviewers cannot easily distinguish:
  1. a correct implementation,
  2. an implementation that misses the intent,
  3. scope creep,
  4. a requirement that itself moved.
- LLM reviewers (CodeRabbit, Greptile, PR-Agent, open-code-review) improve coverage but produce false positives and no calibrated risk. Teams either ignore them or disable them.
- A single 0–100 "review score" is unactionable and gameable. Agents optimize the score, not the intent.
- Tests, policy-as-code (OPA/Conftest), and architecture fitness functions already verify codified behavior on every push. The gap is the **semantic residue**: requirements that are not expressible as tests or policy, where the link between prose intent and code behavior is judgmental.
- Regulated teams have traceability tools (Jama, Polarion, DOORS), but those track human-authored links, not per-push conformance of agent-written code.

---

## 3. Insight

### 3.1 Review is four drift questions over one claim graph

| # | Drift | Question | Priority |
|---|---|---|---|
| 2 | Intent | Does the change implement claim C? | Core |
| 3 | Scope | Does the change touch anything outside C? | Core |
| 1 | Requirement | Did C's meaning change (loosen / tighten / reshape)? | Later |
| 4 | Session goal | Does the agent's plan still serve the original goal? | Later |

All four need the same primitive: a **claim** with a stable ID, a source, a status (`satisfied` / `violated` / `unverifiable` / `suspect`), and a lifecycle event when its text or evidence changes. Build the claim graph once; the four checks are queries over it.

### 3.2 Calibrated probabilities make probabilistic merge policy viable

Instead of "the bot said fail," the gate can say: block if `P(violation) > 0.9`, escalate if `0.5–0.9`, pass below `0.3`. That is a risk budget, and it is auditable. It also lets a team tighten or loosen the gate without changing prompts.

### 3.3 Independence beats a bigger model

The LLM reviewer and the gate should be separate systems with separate question sets. The reviewer finds and explains; the gate verifies. If the coding agent fixes against the visible reviewer, the hidden gate still checks whether the fix addressed the claim. This is a direct counter to Goodharting.

### 3.4 Route each claim to the cheapest sufficient oracle

Not every claim deserves a model call. The gate classifies the claim and uses the strongest deterministic check available before spending a probabilistic one:

| Claim type | Cheapest sufficient oracle | Determinism |
|---|---|---|
| Codified behavior | unit / integration / property test | deterministic |
| Codified rule | OPA / Conftest / policy-as-code | deterministic |
| Structural invariant | ArchUnit / dependency-cruiser / import-linter | deterministic |
| Traceable but not testable | OpenFastTrace / Doorstop / `req` link + meaning check | deterministic bookkeeping |
| Semantic, natural-language | Jev per-claim verification | probabilistic, calibrated |
| Irreducibly judgmental | human review, routed by risk | human |

This is the positioning that survives a DevOps review: **the semantic layer between tests and human review.** Not a replacement for tests, policy, or traceability — the layer that covers what they cannot express.

---

## 4. What it is

A CI-native service that:

1. Captures intent as claims — from the PR description, issue, or session transcript, with optional import from a formal spec.
2. Builds small, relevant state packets per claim (claim + changed hunks + related tests/callers).
3. Asks Jev a versioned library of atomic questions per claim.
4. Computes a per-claim verdict and a composite risk vector in code.
5. Applies per-repo policy to produce `pass` / `review` / `block`, with an audit log.
6. Optionally calls an LLM reviewer for findings, locations, and suggested fixes on anything escalated.

It is **not** a code generator, a chat reviewer, a replacement for tests or SAST, or a single "quality score."

---

## 5. Architecture

```
intent sources ──► claim extractor ──► claim graph
                                         │
diff + repo context ──► context builder ─┼──► Jev question library ──► probabilities
                                         │                              │
                                         │                              ▼
                                         │                        policy engine ──► gate status
                                         │                              │
                                         └──► LLM review layer ──► findings ─┘
                                                                        │
                                                                        ▼
                                                              audit + coverage ledger
```

**Components**

- **Claim extractor** — LLM with structured output; produces atomic, falsifiable claims with scope and acceptance criteria. This is System 2 work.
- **Context builder** — file selection, symbol extraction, diff slicing, test/caller lookup. Deterministic. Reuse ideas from open-code-review's deterministic front-end.
- **Question library** — versioned YAML of Noul / Choice / Score questions, each with criteria, risk weight, threshold, and model pin.
- **Jev verification** — batched, parallel, per-claim state packets; probabilities and confidence.
- **Policy engine** — per-repo thresholds, risk budget, confidence routing, bypass rules.
- **LLM review layer** — open-code-review or pr-agent for findings and rationale; anchor-in-diff validation.
- **Coverage ledger + audit log** — what was checked, what wasn't, which model/question versions, who bypassed.

---

## 6. Why Jev

- **Calibrated confidence** per answer: the gate can distinguish "confident violation" from "no idea," which LLM reviewers cannot.
- **Speed and cost**: roughly 70–500 ms per call, $0.042 per million input tokens, free output. Dense checks on every push become affordable.
- **Type-safe outputs**: no parse failures or schema drift in CI.
- **Consistency**: same input yields similar answers; a gate needs stability.
- **Independence**: a different model family and question set from the reviewer.

**Limits we design around:** 64k per-request / 32k per-question context by default (262k long-context is slower and less accurate), context rot, literal reading, weak numeric reasoning, and susceptibility to adversarial content. Hence: small state packets, arithmetic in code, comment stripping, and per-question calibration.

---

## 7. Why not just X

Tests, OPA/Conftest, and fitness functions already verify codified behavior per push. They are the first three layers, not alternatives. The table below covers the tools that compete for the same review surface.

| Alternative | What it gives | Why it isn't enough |
|---|---|---|
| Entire Trails | session capture, review agents, gates | agents are LLM-scored and overconfident; no calibrated policy; agents cost tokens |
| CodeRabbit / Greptile / Graphite / Qodo | review comments, findings | prose-optimized, noisy, no probability, no claim-level conformance |
| open-code-review / pr-agent | strong findings layer | reviewers, not gates; no intent claims or calibrated policy |
| Cloudflare security-audit-skill | deep security audit | periodic and expensive, not per-push |
| Jama / Polarion / DOORS / Codebeamer | formal traceability: versioned requirements, baselines, suspect links, audit chain | detects *that* a requirement changed, not *how*; human-linked; not per-push agent code |
| StrictDoc / Doorstop / OpenFastTrace / Sphinx-Needs | git-native requirements and link/coverage traceability | a claim source and coverage check; no semantic conformance or direction classification |
| `req` (autox-my) | requirement meaning drift with direction (`looser` / `tighter` / `scope` / `polarity`); linked JUnit tests and `SUSPECT` design links give a narrow type-2 trigger | deterministic; the full workflow wants a claim corpus, but `req diff` alone does not. Not a semantic code verifier. Corrected in v2 §5.1. |
| Spec Kit / Kiro / Tessl | spec capture | upstream of review; no merge gate |

**Position:** the semantic layer between tests and human review. The deterministic layers — tests, policy-as-code, fitness functions, traceability — are complements, not competitors: the gate routes a claim to them first and only spends a calibrated model call on the residue.

---

## 8. Quality model

The thin first slice is narrow in scope but held to the same bar:

- **Dual-track independence** — reviewer and gate are separate systems.
- **Evidence binding** — every finding must anchor to a changed file/line (anchor-in-diff).
- **Tri-state verdicts** — `satisfied` / `violated` / `unverifiable`; never round unknown to pass.
- **Coverage ledger** — explicit record of checked vs skipped claims and files.
- **Held-out calibration** — measure precision, recall, and calibration on historical PRs before blocking.
- **Version pinning** — model and question-library versions in the audit log; re-validate on upgrade.
- **Warn-only ratchet** — shadow → advisory → blocking, per repo.
- **Goodhart mitigations** — hidden/held-out question set, randomized phrasing, human spot audits, grade the artifact not the score.
- **Human re-record** — a claim whose meaning tightens becomes `suspect` until a person re-checks and re-records.

---

## 9. DevOps integration and operations

### Deployment surface

- GitHub App plus a required status check; optional reusable Action; self-hosted runner for air-gapped or enterprise installs.
- No new dashboard by default. Output: one check run and one compact PR comment; full detail in the check summary or a build artifact.
- Integrates with existing branch protection rather than creating a parallel approval system.

### Failure semantics

- **Fail-open with explicit `unverifiable`.** If Jev is down, rate-limited, or over budget, the check returns `neutral` / `unverifiable` and routes to human review. Never silently pass; never hard-block on infrastructure failure.
- Idempotency key per `(repo, commit, question-library-version)`; re-runs are safe.
- Latency budget: p95 under ~90s for the Jev track. The LLM findings track runs asynchronously and must not block merge.
- Retries with backoff, a circuit breaker, and per-repo concurrency caps.

### Determinism and debuggability

- Pin the model version and question-library version per run; write both into the check output.
- Store the state-packet hash and per-question probabilities so any verdict can be replayed.
- A wrong verdict must be explainable from the artifact: claim, state packet, question, distribution, threshold, policy.
- Provide a replay command that re-runs a past decision against the same versions.

### Security and data handling

- Diffs and code leave the network to TypeSafe and the LLM provider. For many organizations this is the first blocker. Needs DPA, a ZDR option, region choice, and a self-hosted or on-prem path for enterprise.
- Treat all artifact content as untrusted: comments, strings, commit messages, PR body. Strip or neutralize before Jev; keep the security question set separate from artifact text where possible.
- No code content in logs; redact; scoped tokens; least-privilege GitHub App permissions.
- Sandbox the LLM review layer: read-only checkout, no write access, no network beyond the model endpoint.

### Cost and rate limits

- Per-repo monthly budget and per-push cap; batch questions; cache identical state packets carefully (staleness matters).
- Jev is cheap enough that the LLM findings layer dominates cost. Run findings only on escalated claims, not on every push.
- Backpressure: degrade to gate-only when over budget.

### Observability and SLOs

- Metrics: checks per run, escalation rate, block rate, override rate, p95 latency, Jev error rate, cost per run.
- Weekly calibration report: sampled human agreement, false-block rate, missed-bad rate.
- Alert on false-block spikes or override spikes; auto-demote to advisory.

### Rollout

- Shadow (no status) → advisory (neutral check) → blocking (required check), per repo and per path.
- Bypass requires a reason and is recorded; bypass rate is a first-class metric.
- Per-path policy: e.g. block on `auth/**`, `infra/**`, `migrations/**`; advisory elsewhere.

### Monorepos and large diffs

- File selection and exclusions for generated, vendored, minified, and lock files.
- Chunk by claim, not by file; cap state-packet size; emit `unverifiable` rather than truncating silently.
- Per-question-family path filters.

### Coexistence

- Runs alongside SAST, SCA, and tests; does not replace them.
- Can consume open-code-review or pr-agent findings as one input, but the gate remains independent.
- Can read claims from StrictDoc, Doorstop, OpenFastTrace, or Jama via an adapter; the gate does not own the requirements corpus.

---

## 10. Scope and phasing

**Phase 1 — core (#2 + #3).** Claims from PR/issue; Jev per-claim verification; deterministic path-scope check; advisory gate. No formal corpus, no session capture.

**Phase 2 — session goal drift (#4).** Use Entire's free session capture (or local agent logs). Compare original goal vs plan vs recent actions every N turns; warn-only. Small state, atomic judgment — arguably Jev's best fit in the whole design.

**Phase 3 — requirement drift (#1).** Lightweight claim table plus a Jev direction classifier (`same` / `looser` / `tighter` / `scope change` / `polarity flip`). If compliance-grade determinism is required, integrate a real traceability tool (StrictDoc, Doorstop, OpenFastTrace, or Jama/Polarion) as the claim source rather than rebuilding canonicalization.

---

## 11. Validation plan

- 100 historical PRs from 1–2 repos. Positives: PRs that caused a revert, hotfix, incident, or security finding. Negatives: clean merges.
- 15–25 atomic questions, frozen before labeling.
- Shadow mode; no blocking.
- Metrics: precision at block threshold, recall of known-bad, false-block rate, calibration curve, escalation rate, human agreement.
- Baselines: tests + OPA/Conftest alone, open-code-review alone, LLM reviewer alone, Jev gate alone.
- Claim routing audit: classify each claim as test / policy / structural / traceable / semantic; report the incremental lift over the deterministic layers, not just over LLM reviewers.
- **Kill criteria:** precision below ~90% at a usable threshold, or recall below ~50%, or calibration far from the diagonal → narrow to one claim family (security or intent) before expanding.

---

## 12. Risks and open questions

- **Feature vs company.** Entire or a forge could add "bring your own scorer." Counter: forge-agnostic, own the calibration, policy, and claim graph.
- **Intent quality.** Garbage intent produces garbage claims. Need a strong capture UX and explicit `unverifiable` handling.
- **Claim-extraction cost and latency.** LLM extraction per PR may dominate the pipeline. Cache, template, and reuse issue text.
- **Adversarial code and comments.** Prompt injection into Jev. Strip or neutralize comment text, dual-pass, treat all artifact content as data.
- **Goodharting.** Agents optimize visible scores. Hidden checks, audits, artifact grading.
- **Calibration transfer.** Jev's confidence is calibrated on its distribution, not your repo. Validate every question.
- **Vendor risk.** Early-access model. Abstract behind an interface, keep an LLM fallback, pin versions.
- **Adoption.** Another gate is friction. Start advisory, integrate with existing checks, never add a second dashboard.

---

## 13. What I want from the reviewer

1. Would you make this a required check in your repos, or is it another gate you'd bypass? What would earn "required" status?
2. What is your acceptable p95 latency and failure mode for a review gate? Is fail-open-with-`unverifiable` acceptable, or do you need fail-closed on specific paths?
3. Sending diffs to TypeSafe and an LLM provider: blocker, or acceptable with DPA / ZDR / region choice? What would you need for enterprise?
4. How do you prevent false-block fatigue and alert fatigue? What override and bypass model would you actually accept?
5. Would you rather have this as a GitHub App, a reusable Action, a CLI in your existing pipeline, or a service? Why?
6. What existing tool (tests, OPA/Conftest, fitness functions, SAST/SCA, CodeRabbit, Entire, traceability) must this integrate with, and which must it not duplicate?
7. What would make you turn it off after a week?
8. What single metric would you watch to decide whether it is working?

I am optimizing for something a senior DevOps team would actually run as a required check, not something that only demos well.
