# IntentGate v2 — intent locks and calibrated merge gates

**Status:** revised after external review
**Supersedes:** `intent-gate-external-review.md`
**Reviewer:** senior DevOps engineer (2026-11-17)
**Date:** November 2026

> **Vocabulary (2026-11-18).** IntentGate's terms were renamed so they no longer clash with req's: **claim → criterion**, **scope → footprint**, **the gate → the PR check**, **plan diff → plan review**, **model_pin → model_version**. This document keeps the reviewed wording; `docs/glossary.md` is authoritative, and the requirements in `claims/` use the new terms.

---

## 0. What changed after review

The review found four issues that block a required check. All four are fixed here, and the phasing is re-scoped around them.

| Review issue | Fix | Section |
|---|---|---|
| **Intent provenance.** The agent writes the PR description, so the gate checks the agent against itself. | Intent lock at human ratification; original prompt kept as root intent; plan-vs-prompt diff at lock time; no lock → `unverifiable`. | §3 |
| **`neutral` satisfies GitHub required checks.** Fail-open as written means fail-open to merge. | Explicit mode × outcome conclusion table; `action_required` or `failure` for needs-human on blocking paths; active reviewer/label routing. | §8 |
| **Validation cannot support the calibration claim.** PR-level badness labels are not claim-level intent violations; 100 PRs is too small. | Claim-level labels, seeded mutations, inter-rater check, 300 labels per family before public calibration claims, Brier/ECE. | §9.4 |
| **"Hidden" conflicts with "auditable."** | Hide from agents, not humans; access-controlled audit store; variant rotation and per-variant calibration. | §9.3 |
| Idempotency key misses base, lock, policy, model. | Key = `(repo, base_sha, head_sha, claim_lock_hash, policy_hash, qlib_version, model_pin)`; `merge_group` support; incremental re-verification. | §8 |
| Phase plan assumes semantic verification first. | Re-scoped: intent lock + deterministic scope drift first; semantic verification second. | §7 |

**Thesis after review:** lock intent before code, route every claim to the cheapest sufficient oracle, and reserve calibrated model judgment for the residue. The first product is a **lockfile for intent** with a deterministic scope gate — not a semantic reviewer.

### 0.1 Where I still push back on the review

1. **Plan-approval hooks are more fragmented than the review implies.** Claude Code, Codex, Cursor, Kiro, and Spec Kit each expose a different "plan approved" moment, and agents replan continuously. Phase 0 must ship a manual `intentgate lock` plus an issue-based lock; agent hooks are Phase 2, not the opening.
2. **Scope-creep-first is a wedge, not a moat.** "These 3 files are outside every locked claim's scope" is cheap and useful, but it is path globs plus a manifest. The moat is the policy layer and the override/calibration data, not the scope check.
3. **300 labels per family is a public-claim bar, not an internal Phase 1 bar.** Start with ~100 seeded-mutation labels for the families that will block, and expand to 300 human labels before claiming calibration publicly.
4. **Fail-closed needs an availability caveat.** On `auth/**` or `infra/prod/**`, "fail-closed" should mean *requires human approval from CODEOWNERS/security*, not *no merge possible*. Otherwise a gate outage blocks security fixes. Break-glass must be available to on-call.

---

## 1. Summary

Agent-written code is outpacing human review. A pull request shows *what* changed, not whether the change still does *what was asked*. Existing AI reviewers produce plausible comments, but they are noisy, overconfident, and easy to game; they are optimized for prose, not merge policy.

IntentGate is a merge-gate layer, not another reviewer. It captures intent as a **locked, human-ratified set of claims** before implementation, verifies each claim against the diff with calibrated atomic judgments, and converts those probabilities into an explicit, auditable **merge policy**. It uses TypeSafe's Jev for judgment and a conventional LLM reviewer only as an evidence source for escalated claims.

**Thesis:** review should be a set of calibrated claim checks governed by policy, not a stream of opinions.

---

## 2. Problem

- Coding agents produce larger and more frequent changes. The session that produced the change holds the intent, but reviewers only see the diff.
- Reviewers cannot easily distinguish a correct implementation, an implementation that misses the intent, scope creep, and a requirement that itself moved.
- **Intent provenance is broken by default.** The agent often writes the PR description and the session transcript is its own narrative. A gate that reads either one confirms that the agent did what the agent said it did.
- LLM reviewers (CodeRabbit, Greptile, PR-Agent, open-code-review) improve coverage but produce false positives and no calibrated risk. Teams ignore or disable them.
- A single 0–100 "review score" is unactionable and gameable. Agents optimize the score, not the intent.
- Tests, policy-as-code (OPA/Conftest), and architecture fitness functions already verify codified behavior on every push. The gap is the **semantic residue**: requirements that cannot be expressed as tests or policy.
- Regulated teams have traceability tools (Jama, Polarion, DOORS), but those track human-authored links, not per-push conformance of agent-written code.

---

## 3. The intent lock

### 3.1 What it is

A small, hashed artifact created at the moment a human ratifies the agent's plan, **before implementation begins**. It is the agent's brief and the gate's spec at the same time.

| Field | Contents |
|---|---|
| `root_intent` | The human's original prompt or discussion turns, verbatim. The only text with fully human provenance. |
| `claims` | At most 8 atomic, falsifiable claims with acceptance criteria in the human's words. |
| `scopes` | Per-claim path globs, including negations (`billing/**`, `!billing/legacy/**`). |
| `ratification` | Who approved, when, how long the review took, which agent/model drafted the plan. |
| `hash` | Hash over the above, stored outside the PR's control (check-run artifact, `.intent/<id>.lock` commit, or server-side). |

### 3.2 When it is created

- **Phase 0:** `intentgate lock #1234` from a human-written issue, or `intentgate lock --from-plan plan.md`. This is the reliable path across all agents.
- **Phase 2:** an agent hook on plan approval (leaving plan mode, approving a spec in Kiro or Spec Kit, merging a design doc).
- If no lock exists, the result is `unverifiable: no-locked-intent`. Never infer claims from the PR body. Bots such as Dependabot and Renovate are skipped by policy.

### 3.3 Why it fixes provenance

The PR description is agent-authored; the lock is human-ratified and precedes implementation. The gate checks code against the lock, not against the agent's own narrative. A claim set derived from the PR body is a description of what the agent did; a claim set derived from a ratified plan is a statement of what was asked.

### 3.4 Plan-vs-prompt diff at ratification

Before the human approves, show three things:

- **omissions** — what the prompt asked for that the plan drops;
- **additions** — what the plan adds that the prompt did not ask for;
- **weakenings** — where the plan loosens an obligation.

This is arguably Jev's best fit in the whole design: small state, atomic judgments, and a human in the loop to act on the result. It catches drift #1 and #4 at the cheapest possible moment, before code exists. It also gives the lock a reason to exist beyond the gate: a sharper plan review.

### 3.5 Amendments and lock rot

- A mid-implementation change requires an **amendment**: the agent proposes, the human ratifies, the lock is re-hashed. That amendment is an auditable requirement-drift event.
- Locks expire or require re-ratification on base-branch change, issue edit, or a configurable age. A stale lock becomes `suspect`; it is not silently trusted.
- A 2-second approval of 15 claims is recorded as a signal in the audit log. Ratification only means something if someone read it.

---

## 4. Four drift checks over the claim graph

| # | Drift | Question | With the lock | Priority |
|---|---|---|---|---|
| 2 | Intent | Does the change implement claim C? | C is a ratified claim | Core |
| 3 | Scope | Does the change touch anything outside C? | C has declared globs; deterministic first | Core |
| 1 | Requirement | Did C's meaning change? | Lock diff between revisions | Phase 3 |
| 4 | Session goal | Does the plan still serve the original goal? | Lock vs current plan | Phase 2 |

All four use one primitive: a claim with a stable ID, a source, a status (`satisfied` / `violated` / `unverifiable` / `suspect`), and a lifecycle event when its text or evidence changes.

---

## 5. Route each claim to the cheapest sufficient oracle

| Claim type | Cheapest sufficient oracle | Determinism |
|---|---|---|
| Codified behavior | unit / integration / property test | deterministic |
| Codified rule | OPA / Conftest / policy-as-code | deterministic |
| Structural invariant | ArchUnit / dependency-cruiser / import-linter | deterministic |
| Traceable but not testable | OpenFastTrace / Doorstop / `req` link + meaning check + linked JUnit tests | deterministic bookkeeping; `req` also triggers targeted semantic re-checks |
| Semantic, natural-language | Jev per-claim verification | probabilistic, calibrated |
| Irreducibly judgmental | human review, routed by risk | human |

**Position:** the semantic layer between tests and human review. Tests, policy, fitness functions, and traceability are complements, not competitors: the gate routes a claim to them first and only spends a model call on the residue.

### 5.1 Deterministic drift narrows the semantic surface

The most useful correction from the `req` team: `req` is not a competitor to intent-drift tools — it is their **trigger**.

- `req diff` detects that a requirement's meaning changed and classifies the direction (`same` / `looser` / `tighter` / `scope` / `polarity`).
- `req satisfy` marks the design elements linked to a changed claim as `SUSPECT`.
- `req gate` can require linked JUnit tests to pass — a narrow, deterministic type-2 check where tests exist.

The expensive layer therefore does not need to re-judge every claim on every push. It re-judges **only the links a deterministic trigger marked suspect**. This is the screen-first pattern applied to review: cheap deterministic drift detection decides *where* to spend model judgment.

The same pattern works without `req`: an amendment or re-lock is a lock diff, and a changed claim marks its linked artifacts suspect. `req` is an optional adapter that brings a stronger, deterministic trigger to teams that already maintain requirements.

**Integration contract:** read `req diff` / `req satisfy` output; map `SUSPECT` links to claims and artifacts; run Jev only on those packets; write the verdict back as evidence. Never treat `req`'s `HOLDS` as proof of satisfaction — it means the link is current, not correct.

---

## 6. Architecture

```
intent sources ──► intent lock ──► claim graph
  (prompt, issue,                  │
   ratified plan)                  │
                                   ├──► deterministic checks (tests, OPA, scope globs, SARIF)
diff + repo context ──► packet ────┤
                                   ├──► Jev question library ──► probabilities
                                   │                              │
                                   │                              ▼
                                   │                        policy engine ──► gate status
                                   │                              │
                                   └──► evidence layer ────────► findings
                                       (open-code-review,          │
                                        pr-agent, SARIF)           ▼
                                                          audit + coverage ledger
```

- **Intent lock** — ratification artifact; source of claims.
- **Packet builder** — file selection, symbol extraction, diff slicing, test/caller lookup; secret scan before egress.
- **Question library** — versioned YAML of Noul / Choice / Score questions with criteria, weight, threshold, model pin.
- **Policy engine** — per-repo thresholds, PR-level aggregation, confidence routing, bypass rules.
- **Evidence layer** — consumes test results, OPA output, SARIF, and optionally open-code-review/pr-agent findings. It does not post a parallel review.
- **Audit + coverage ledger** — checked vs skipped, model/question versions, overrides, retention.

---

## 7. Phasing

| Phase | Ship | Gate mode | Exit criterion |
|---|---|---|---|
| **0 (4–6 weeks)** | CLI, `intent.lock` from issues or plan files, plan-vs-prompt diff at ratification, deterministic scope-creep check, SARIF and check-run output | Advisory only | 3 design-partner repos use locks on >50% of agent PRs |
| **1** | Jev claim verification on locked claims, replay, override flow, merge-group support, claim-level labeling harness with seeded mutations | Shadow, then advisory | PR-level false-block ≤2%, actioned-block rate ≥70%, unverifiable ≤30% on target paths |
| **1.5** | GitHub App UX, policy dry-run, per-path `fail_closed`, question-library CI, secret scan before egress | Blocking on opt-in paths | One partner keeps it required on ≥1 path for 30 days without demotion |
| **2** | Agent hooks for plan approval, agent-loop pre-flight (visible set), session goal drift from standard agent logs | Warn-only | Measurable reduction in CI-time blocks as agents self-correct |
| **3** | Requirement drift as lock diff, `req` adapter (diff → SUSPECT links → targeted Jev re-check), traceability adapters (StrictDoc, Doorstop, Jama), self-hosted model path | Per policy | First regulated customer passes security review; one traceability integration in production |

---

## 8. GitHub and CI semantics

### 8.1 Conclusion table

GitHub treats `success`, `skipped`, and `neutral` as satisfying a required check. `neutral` must therefore never mean "needs human" on a blocking path.

| Mode | pass | review | block | infra error |
|---|---|---|---|---|
| shadow | neutral | neutral | neutral | neutral |
| advisory | success | neutral | neutral | neutral |
| blocking | success | `action_required` | `failure` | `action_required`; `failure` only on `fail_closed: true` paths |

Side effects, not just colors: request CODEOWNERS reviewers or apply a `needs-human-review` label that a ruleset requires to be cleared. "Route to human review" needs a mechanism.

### 8.2 Merge queue and idempotency

- Run on `merge_group` (and GitLab merge trains) or the gate never satisfies a required check in a queue.
- Cache answers when the tree-relevant packets are unchanged; target ≤20 s for a cached queue re-run.
- Idempotency key: `(repo, base_sha, head_sha, claim_lock_hash, policy_hash, qlib_version, model_pin)`. The same head against a new base is a different diff.
- Re-verify incrementally: only claims whose packet hash changed.

---

## 9. Quality and calibration

### 9.1 Determinism, not consistency

Replay reads the **stored distributions**; it does not re-query the model. Re-querying is a separate drift probe whose differences are tracked as a model-stability metric. The first time a re-run turns a block into a pass, trust is gone.

### 9.2 PR-level aggregation

- Thresholds are per claim; the merge decision is per PR. Ten claims at a 3% false-block rate produce roughly a 26% PR-level false-block rate.
- Policy: any claim with `P(violation) > θ_block` on a blocking path blocks; otherwise apply a weighted risk budget. Report **PR-level** false-block rate, not just per-claim precision.
- Define the missing band: `0.3–0.5` escalates; it does not pass.
- Cap claims per PR at 8. A long claim list is a signal that the PR should be split.

### 9.3 Hidden from agents, visible to humans

- Check output shows the claim, verdict, evidence anchors, and threshold — **not** the question text.
- Question text and full distributions live in an access-controlled audit store (repo-admin or gate-auditor role); replay authenticates.
- Rotate phrasings from a pool and record the variant. Calibrate variants, not just questions.
- Treat the hidden set as a rate-limiter on overfitting, not a secrecy guarantee. Agents can infer it over many runs; monitor convergence.

### 9.4 Validation

- **Labels at the claim level**, not the PR level. Take merged PRs with a linked human issue, lock claims, and have two engineers label each claim `satisfied` / `violated` / `unverifiable`.
- Measure inter-rater agreement first. Below ~85%, the target is ill-defined and no model will calibrate against it.
- **Seeded violations:** mutate correct PRs (drop a guard, widen a scope, remove a check, add an unrelated file). Cheap, unambiguous positives at volume.
- Start with ~100 seeded labels for blocking families; target ≥300 human labels per family before claiming public calibration.
- Report **Brier score and ECE**, not just a curve picture.
- Keep revert/incident PRs as a separate **escape benchmark**: "would this have caught real escapes?"
- Baselines: tests + OPA/Conftest alone, open-code-review alone, LLM reviewer alone, Jev gate alone.
- Kill criteria: precision <90% at a usable threshold, recall <50%, calibration far from diagonal, or `unverifiable` >30% on blocking paths.

### 9.5 Unverifiable is a product metric

Wide-context claims ("does not break existing callers", "keeps tenant isolation") will often be `unverifiable` under small packets. Track `unverifiable rate by claim family` as an SLO and a kill criterion. If it exceeds ~30% on blocking paths, the product is a router to humans and should say so.

---

## 10. DevOps operations

- **Failure semantics:** fail-open everywhere except `fail_closed: true` paths. Fail-open means *require human review*, implemented via `action_required` or reviewer routing — not `neutral`. Fail-closed means *requires CODEOWNERS/security approval*, not *no merge possible*.
- **Latency:** p95 ≤60 s from push to conclusion for the gate track; ≤20 s for a cached merge-queue re-run. The findings track is asynchronous and must never delay the conclusion.
- **SLO:** gate availability ≥99.5% monthly, with error-budget burn alerts.
- **Egress:** gitleaks/trufflehog over every packet before sending; drop or mask hits and record them. Minimal packets (changed hunks + N lines + symbol signatures). `never_egress: ["secrets/**", "customer-data/**"]`. Provider interface with a self-hostable fallback (advisory-only if uncalibrated).
- **Audit:** define retention, export to S3/SIEM, and a hash-chained log. A probabilistic control becomes a risk-routing control in SOC 2 / ISO language: the gate decides who must look; accountable humans approve.
- **Auto-demote:** open an incident and require a human ack; never silently weaken branch protection. Log the demotion as a policy change.
- **Policy as code:** `.intentgate.yml` protected by CODEOWNERS; dry-run ("if this policy had been active last week, it would have blocked N PRs").
- **Question-library CI:** golden state packets with expected distributions; no model-pin bump if Brier/ECE regresses beyond a threshold.
- **Metrics:** actioned-block rate, override rate and reasons, escaped-violation rate, unverifiable rate, p95, cost per run.

---

## 11. Developer adoption

- **`intent.lock`** — the agent's brief and the gate's spec. Written when the human says "yes, build that."
- **Claim coverage** — "4/5 claims satisfied, 1 unverifiable" next to test coverage. Developers already understand coverage.
- **Local pre-flight** — `intentgate check` in the agent loop against a *visible* question set that is disjoint from, or a rotated sample of, the CI set. Agents self-correct before pushing; the hidden set stays independent.
- **Scope-creep diff** — "these 3 files are outside every locked claim's scope." The single most useful comment for agent PRs.
- **Overrides are the flywheel** — every override with a reason is a repo-specific calibration example. After six months, per-org calibration is what a forge's "bring your own scorer" cannot copy on day one.

**Who the lock is for.** Generic product teams with prose in Jira/Notion/Linear should use the lock as the entry path — no formal corpus, no traceability tool. Teams already under traceability rules (automotive, safety, medical) already have a corpus in `req`, StrictDoc, Doorstop, or Jama; there the product is an adapter, not a replacement. The maturity objection matters for the first segment only if we ask them to adopt `req`; it does not apply if the lock is the primary path.

---

## 12. Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Unratified, agent-authored intent | Critical | Intent lock at ratification; root prompt kept verbatim |
| Rubber-stamp ratification | High | Plan-vs-prompt diff, ≤8 claims, review time recorded |
| `neutral` satisfies required checks | Critical | Conclusion table; `action_required` / `failure`; reviewer routing |
| Validation labels don't match target | High | Claim-level labels, seeded mutations, inter-rater check |
| Check output leaks the hidden set | High | Hide from agents, not humans; audit store; variant rotation |
| PR-level false-block compounding | High | PR-level aggregation, claim cap, report PR-level rate |
| Non-reproducible re-runs | High | Replay stored distributions |
| High unverifiable rate on high-value claims | Medium | SLO and kill criterion |
| Auto-demote abuse | Medium | Incident + human ack |
| Secrets in egressed packets | Medium | Pre-egress secret scan; `never_egress` |
| Lock rot | Medium | Expiry and re-ratification on base/issue/age |
| Cross-repo claims | Medium | Claim → multiple artifacts/PRs; `partially-satisfied-by` edges |
| Gate outage on fail-closed paths | Medium | Fail-closed = human approval; break-glass for on-call |
| `req` dependency maturity / format churn | Medium | Optional adapter behind an interface; pin the format version; wait for the F-3 freeze; keep the lock as the primary path |

---

## 13. Open questions for the reviewer

1. Is the manual lock (`intentgate lock #1234` / `--from-plan`) enough for Phase 0, or is an agent hook required for adoption?
2. Is the plan-vs-prompt diff at ratification the real wedge, with PR gating as the second act?
3. Is "scope-creep + lock" enough to earn advisory adoption, or does the semantic check need to be in Phase 1?
4. What is the minimum calibration evidence you would accept before a path becomes blocking?
5. Does fail-closed-as-human-approval resolve the availability concern, or would you keep fail-closed as a hard block?
6. How important are cross-repo claims in the first design-partner set?
7. Should `req` be a Phase 3 adapter, or is the lock diff sufficient for the first design partners?
8. What would make you turn it off after a week?
