# IntentGate: review report

**Reviewing:** `docs/intent-gate-external-review.md` (November 2026 draft)
**Reviewer perspective:** a senior DevOps engineer who has owned branch protection, merge queues and CI reliability for large orgs, and who also looks for developer tools people would adopt without being made to.
**Date:** 2026-11-17

---

## 0. Verdict in one paragraph

The positioning is right. "The semantic layer between tests and human review" is a gap that really exists, and routing each claim to the cheapest sufficient oracle (§3.4) is the most defensible idea in the document. The plan is not yet something I would make a required check, and the main reasons are not about the model:

1. **Intent provenance is unsolved.** If the agent writes the PR description, the gate is checking the agent against itself.
2. **The failure semantics have a real bug on GitHub.** A `neutral` conclusion satisfies a required status check, so "fail-open to human review" as written actually means "fail-open to merge".
3. **The validation plan cannot support the calibration claims.** 100 PRs labeled at the PR level cannot validate per-claim probabilities at a 0.9 threshold.
4. **"Hidden" and "auditable" are in conflict**, and the plan doesn't say who the hidden part is hidden from.

Fix those four and I would run it in advisory mode on day one. It could earn required status on a few high-risk paths within a quarter.

**Recommendation:** Go, after re-scoping. Lead with **claim-locked scope drift**, which is cheap, mostly deterministic and easy to explain. Treat calibrated semantic verification as the second act, not the opening.

---

## 1. What is strong (keep it)

| Element | Why it works operationally |
|---|---|
| Oracle routing (§3.4) | This is what moves you from "AI reviewer" to "control plane". Every claim satisfied by a test or policy is a model call you didn't make and a verdict you don't have to defend. |
| Tri-state verdicts, never rounding unknown to pass | This is correct, and rare. Most tools quietly turn "couldn't check" into a green check. |
| Version pinning plus a replay command | This is what makes a probabilistic gate debuggable. Without it, nobody keeps it after the first disputed block. |
| Shadow → advisory → blocking, per path | This is the only rollout I would accept. |
| One check run and one comment, with no dashboard | Correct. Each new dashboard is an adoption tax. |
| Bypass rate as a first-class metric | Correct, and as §6 below argues, it is also the data moat. |
| Explicit kill criteria (§11) | Rare and valuable. Keep them, but fix the statistics (§4). |

---

## 2. Critical issues (must fix before any team runs this)

### 2.1 Intent provenance: who wrote the claims?

§4.1 extracts claims "from the PR description, issue, or session transcript". In an agent-heavy workflow, **the coding agent usually writes the PR description**, and the transcript is the agent's own narrative. A claim set derived from either one describes what the agent did, not what was asked. The gate then confirms that the agent did what the agent said it did.

That is exactly the Goodhart loop §3.3 claims to prevent, entered through the intent side instead of the reviewer side.

**What provenance actually means in agent workflows.** Requiring human-*authored* claims doesn't match how teams work now. The usual flow is: a human writes a prompt or discusses the idea with the agent, the agent drafts a plan or spec, and the human **ratifies** it. The requirement should therefore be **human ratification at a fixed point before implementation**, not human authorship. What matters is *when* the claims are fixed and *who signed off*, not who typed them.

**Fix: introduce an intent lock at the ratification point.**

- **Lock when the plan is approved.** The natural hook is the moment the human approves the agent's plan: leaving plan mode, approving a spec in Kiro or Spec Kit, or merging a design doc. Claims are extracted from the **ratified plan**, and the lock is hashed and stored before the first line of implementation exists, outside the PR's control (as a check-run artifact, as a commit to `.intent/<id>.lock`, or server-side).
- **Store two layers, not one.** Keep the human's **original prompt or discussion turns verbatim** as the *root intent*, and the ratified plan as the *working claims*. The prompt is the only text with fully human provenance. The plan is the agent's interpretation of it, and the human has endorsed that interpretation.
- **Check the plan against the prompt at ratification time.** An agent-drafted plan can quietly narrow, widen or reshape what was asked, and an approving human may not notice. This is drift #1 and #4 from §3.1, moved to the cheapest possible moment. Before the human clicks approve, show "the plan omits X from your prompt", "the plan adds Y you didn't ask for", and "the plan weakens Z". This one check is arguably the best fit for Jev in the whole design: small state, atomic judgments, and a human right there to act on the result.
- **Treat rubber-stamping as the real risk.** Ratification only means something if people read what they approve. Keep the claim list short (≤ 8), show it as a diff against the prompt rather than as a wall of prose, and record what was ratified and how long the review took. A 2-second approval of 15 claims is a signal worth surfacing in the audit log.
- **Changes after the lock need a re-lock.** Editing the PR body, or the agent revising its plan mid-implementation, **does not change the claims**. If the agent discovers mid-task that the plan must change, it proposes an amendment and the human ratifies it. That amendment is an auditable requirement-drift event, which gives you Phase 3 almost for free.
- If no ratified intent exists, the result is `unverifiable: no-locked-intent`, never inference from the PR body. Bots such as Dependabot and Renovate are skipped by policy. Human-written issues still work as a source; they are simply the case where prompt and plan are the same text.

This is also the developer-loved primitive the plan is missing (see §6): a **lockfile for intent**, written at the moment the human says "yes, build that".

### 2.2 `neutral` passes required checks on GitHub

§9 says that on infrastructure failure the check returns `neutral` / `unverifiable` and routes to human review. On GitHub, a required status check whose conclusion is `neutral` or `skipped` **counts as satisfied for branch protection**. Unless a human approval is independently required, fail-open with `neutral` is an unguarded merge.

**Fix:**

- Use `action_required` or `failure` for "needs human" on paths where the policy says to block, plus a bypass mechanism (§2.5). Reserve `neutral` for advisory-mode repos only.
- Alternatively, have the gate actively add the routing: request CODEOWNERS reviewers or apply a `needs-human-review` label that a ruleset requires to be cleared. "Route to human review" needs a concrete mechanism, not just a status color.
- Document the semantics per mode in a table: `{shadow, advisory, blocking} × {pass, review, block, infra-error}` → check conclusion, plus side effects. This table belongs in the plan.

### 2.3 Merge queues, re-runs and the idempotency key

- **Merge queue:** the check must also run on the `merge_group` event (and on GitLab merge trains), or it will never satisfy a required check in a queue. In the queue, the 90 s p95 multiplies across the batch, so the gate must answer from cache when the tree-relevant state packets are unchanged.
- **Idempotency key:** `(repo, commit, question-library-version)` is not sufficient. The verdict also depends on the **claim-set hash**, the **policy version**, the **model version** and the **base commit** (the same head against a new base can change the diff). The key should be `(repo, base_sha, head_sha, claim_lock_hash, policy_hash, qlib_version, model_pin)`.
- **Incremental re-verification:** on each push, re-ask only for claims whose state-packet hash changed. This is the biggest cost and latency lever and isn't mentioned.

### 2.4 "Hidden question set" vs "auditable"

§8 wants a hidden or held-out question set and randomized phrasing. §9 wants every verdict explainable from the artifact. Coding agents read CI logs and check summaries, so anything in the check output leaks to the next iteration of the agent.

**Fix:** define the threat model explicitly. Hide from the **agent**, not from **humans**.

- The check output shows the claim, the verdict, the evidence anchors and the threshold, but **not** the question text.
- Question text and full distributions go into an access-controlled audit store, readable by humans with repo-admin or a "gate auditor" role. The replay command authenticates.
- Rotate phrasings from a pool per question and record which variant ran. Calibrate each variant, not just each question.

### 2.5 The block UX and the bypass model are underspecified

A block that doesn't tell the developer **what would make it pass** gets bypassed every time. Every `block` or `review` comment needs:

- the claim (in the human's words, from the lock);
- the evidence anchors (file:line in the diff), or "no evidence found for…";
- a one-line **satisfying condition** ("add or modify code under `auth/` that enforces X", or "re-lock the claim if the requirement changed");
- three buttons: **Fix** (no-op, just re-run), **Override with reason**, **Claim is wrong → re-lock**.

The bypass model I would accept:

- An override is a slash command or button restricted to CODEOWNERS of the blocked path. A reason is required, from an enum plus free text: `false-positive`, `claim-outdated`, `accepted-risk`, `emergency`.
- **Break-glass:** an `emergency` override is allowed for any maintainer, auto-opens a follow-up issue and pages nobody. A 2 a.m. hotfix must never wait on the gate.
- Every override becomes a **labeled example** in the calibration set (see §6).

---

## 3. Significant issues (fix before "required")

### 3.1 The threshold policy has a hole and ignores multiplicity

- §3.2: block above 0.9, escalate at 0.5–0.9, pass below 0.3. **The 0.3–0.5 band is undefined.** Pick one: escalate, or pass with advisory.
- Thresholds are per claim, but the merge decision is per PR. With 10 claims each carrying a 3% false-block rate, the PR-level false-block rate is about 26%. The policy engine needs a **PR-level aggregation rule** (for example: any claim with `P(violation) > θ_block` on a blocking path, or a weighted risk budget) and validation must report **PR-level** false-block rate, not just per-claim precision.
- Cap claims per PR (for example 8). A long claim list is a sign of a PR that should be split, which is itself a useful signal to surface.

### 3.2 The validation plan cannot support the claims being made

- **Label mismatch.** Positives are "PRs that caused a revert, hotfix, incident, or security finding". Most such failures are not violations of the stated intent: they are performance, config, infrastructure or concurrency bugs. The system predicts **claim-level intent violation**, but the labels are **PR-level badness**. Recall measured this way will look terrible for reasons unrelated to the product, or good by coincidence.
- **Sample size.** 100 PRs with maybe 15–30 positives gives a 95% confidence interval on precision of about ±15–20 points. "Precision ≥ 90%" is not testable at that size, and a calibration curve needs hundreds of claim-level labels per question family.
- **Fix:**
  - Label at the **claim** level. Take merged PRs with a linked human issue, lock claims from the issue, and have two engineers label each claim satisfied, violated or unverifiable. Measure inter-rater agreement first: if humans agree less than about 85% of the time, the target is ill-defined and no model will be calibrated against it.
  - **Seeded violations:** take correct PRs and mutate them (drop a guard, widen a scope, remove a check, add an unrelated file). This gives cheap, unambiguous positives at volume. It is standard mutation-testing practice applied to intent.
  - Target **at least 300 claim-level labels per question family** before claiming calibration. Report Brier score and ECE, not just a curve picture.
  - Keep the revert/incident set as a separate **"escape" benchmark**, framed honestly as "would this have caught real escapes?"

### 3.3 The unverifiable rate is the hidden product metric

Small state packets (a 32 k per-question limit plus comment stripping) are right for accuracy. But the claims that matter most ("does not break existing callers of X", "keeps tenant isolation") need wide context. Expect a high `unverifiable` rate on exactly the high-value claims. If more than about 30% of claims on blocking paths come back unverifiable, the product is mostly a router to humans and should say so.

Add `unverifiable rate by claim family` to the SLOs, and to the kill criteria.

### 3.4 Auto-demote is a security control, so treat it like one

§9: "Alert on false-block spikes or override spikes; auto-demote to advisory." An attacker, or a frustrated team, can trigger demotion by mass-overriding. Auto-demote should **open an incident and require a human ack** within N hours, not silently weaken branch protection. Log the demotion as a policy change in the audit trail.

### 3.5 Policy and the question library are code, so give them CI

- Per-repo policy lives in the repo (`.intentgate.yml`), protected by CODEOWNERS (platform/security team), and changes to it appear in the gate's own check.
- The question library needs **its own regression suite**: a golden set of state packets with expected distributions, run on every library change and on every model pin bump. "Re-validate on upgrade" (§8) needs a concrete gate: no model pin bump ships if Brier score regresses by more than X on the golden set.
- Policy changes should be dry-runnable: "if this policy had been active last week, it would have blocked N PRs (list)." This is the feature that gets platform teams to tighten the policy themselves.

### 3.6 Consistency is not determinism

§6 says that the "same input yields similar answers". For a gate, a re-run on the same commit must give the **same** answer. Replay must **read the stored distributions**, not re-query. Re-querying is a separate "drift probe" whose differences are tracked as a model-stability metric. Otherwise the first time someone clicks "re-run" and a block turns into a pass, trust is gone.

### 3.7 Egress: minimize, scan and offer a bring-your-own model

The answer to Q3 is in §5.3 below, but the plan needs these engineering items regardless:

- Run a **secret scanner (gitleaks or trufflehog) over every state packet before egress**. Drop or mask hits and record the hits in the coverage ledger.
- Use **packet minimization by default**: changed hunks plus N lines of context plus symbol signatures, not whole files.
- Provide a **model provider interface** with at least one self-hostable fallback, even if it is uncalibrated and therefore advisory only. Without this, every regulated prospect stops at procurement.

### 3.8 Prompt-injection surface is larger than comments

Stripping comments does not neutralize string literals, identifiers, test fixture data, Markdown docs, i18n files or commit messages. Specific measures:

- Pass artifact content in a clearly typed data field (not concatenated into instructions), if Jev supports that.
- Run a dual-pass: once with and once without non-code text. A large divergence means `suspect`, which escalates.
- Add a small adversarial suite (known injection patterns embedded in diffs) to the question-library CI.

---

## 4. Smaller issues and gaps

- **Stacked PRs and squash merges** (Graphite, ghstack): which PR owns which claim? Support claims that span multiple PRs and are satisfied across the stack.
- **PRs linked to several issues**, and issues with several PRs: the claim graph needs `partially-satisfied-by` edges.
- **The path-scope check (#3) is too coarse as described.** Scope should be declared per claim (`scope: ["billing/**", "!billing/legacy/**"]`), suggested by the extractor and fixed at lock time. Out-of-scope touches are then deterministic, and the model is only asked "is this out-of-scope change incidental (formatting, imports) or substantive?"
- **GitLab and Bitbucket:** enterprise buyers will ask on the first call. Keep the core engine forge-agnostic from day one, even if only GitHub ships.
- **Audit retention:** define retention, export (to S3 or a SIEM) and tamper-evidence (hash-chained log) for the audit trail. Compliance buyers need this before they will call it a "control".
- **Auditor framing:** auditors may reject a "probabilistic merge control". Frame it as a **risk-routing control**: the gate decides who must look, and accountable humans approve. This wording matters in SOC 2 and ISO conversations.
- **Cost model:** Jev is cheap, so the real costs are claim extraction (the LLM) and the findings layer. Extraction happens once per lock, not per push, which is another reason to adopt the intent lock.
- **The Entire dependency in Phase 2** ties a feature to a third party's free tier. Read standard agent logs (Claude Code, Codex and Cursor transcripts) directly and treat Entire as one adapter.

---

## 5. Answers to the eight questions (§13)

### Q1. Required check, or another gate you'd bypass? What earns "required"?

I would bypass it as currently specified. What would earn required status:

1. Claims come from a locked, human-ratified plan, with the original prompt kept as root intent (§2.1).
2. Measured on **my** repo in shadow mode for at least 4 weeks: PR-level false-block rate ≤ 2% on blocking paths, and actioned-block rate ≥ 70% (see Q8).
3. Every block is actionable (§2.5) and replayable (§3.6).
4. Required only on paths I choose (`auth/**`, `infra/**`, `migrations/**`, `billing/**`). Advisory everywhere else, indefinitely if necessary.

### Q2. Acceptable p95 latency and failure mode?

- **Latency:** p95 ≤ 60 s from push to check conclusion for the gate track, and ≤ 20 s for a cached re-run in the merge queue. 90 s is tolerable only for advisory. The findings track is asynchronous, and it must **never** delay the conclusion.
- **Failure mode:** fail-open everywhere **except** paths marked `fail_closed: true` in policy (I would mark `auth/**` and `infra/prod/**`). Fail-open must be implemented as "require human review", not `neutral` (§2.2). Fail-closed paths must have the break-glass override.
- Publish an **SLO for the gate itself**: availability ≥ 99.5% monthly, with error budget burn alerts. If you blow the budget, the gate auto-shifts to advisory with an incident (§3.4).

### Q3. Sending diffs to TypeSafe plus an LLM provider?

- **Startups and most mid-market:** acceptable with a DPA, zero data retention, region pinning, secret-scanning before egress and minimized packets.
- **Enterprise or regulated:** it is a blocker unless there is a VPC or self-hosted deployment of the gate **and** at least the option of a private model endpoint (Bedrock, Vertex or Azure in their tenant, or a self-hosted model). They will accept "self-hosted model = advisory-only, uncalibrated" as a starting point.
- **What I would need on paper:** a data-flow diagram, a subprocessor list, retention guarantees, a pen-test report, SOC 2 Type II (or a credible date), and per-repo controls for which paths may never leave the network (`never_egress: ["secrets/**", "customer-data/**"]`).

### Q4. Preventing false-block and alert fatigue; an acceptable override model

- Start in shadow mode, then advisory, then blocking per path, with promotion gated on measured metrics, never on a date.
- Post **one** comment per PR, edited in place, never a new one per push. No comment at all when everything passes; the check run is enough.
- Overrides use CODEOWNERS plus a reason enum. Break-glass is always available. Overrides feed calibration.
- A per-repo **weekly digest** goes to the platform team, not to every developer: blocks, overrides, top noisy questions. Any question with override rate above 30% is automatically quarantined to advisory, with a human ack.

### Q5. GitHub App, reusable Action, CLI or service?

**All three, sharing one engine. Build the CLI first, the App second and the Action third:**

- **Core:** a single static binary or container (`intentgate verify --base … --head … --lock …`). This is what runs everywhere: locally, in any CI, in air-gapped environments, and in the replay command.
- **GitHub App:** for the hosted path. It provides check runs, merge-group support, comment UX, override buttons and claim locking on issues. The App is what makes the UX good.
- **Reusable Action, GitLab CI component, and so on:** thin wrappers over the binary for teams that won't install an App.
- **Why:** platform teams trust things they can run and debug in their own pipeline. The CLI is also what turns this from a gate into a tool developers *want* (§6).

### Q6. What must it integrate with, and what must it not duplicate?

- **Must integrate with:** branch protection and rulesets, merge queue, CODEOWNERS; test results (JUnit XML) and coverage, so a claim can be "satisfied by test T"; OPA/Conftest results; SARIF ingestion (SAST/SCA findings as evidence, not re-derived); issue trackers (GitHub Issues, Jira, Linear) as the intent source; agent transcripts (Claude Code, Codex, Cursor) as an optional source.
- **Must output SARIF as well**, so findings show up in the existing code-scanning UI. That is free distribution and no new dashboard.
- **Must not duplicate:** SAST/SCA rules, test execution, linting, or style review. In particular, the findings layer must not compete with CodeRabbit-style review. Consume those tools' output instead.

### Q7. What would make you turn it off after a week?

In order of likelihood:

1. A false block on a hotfix with no fast override.
2. The same commit giving different verdicts on re-run.
3. Comment spam or edits on every push.
4. Adding noticeable time to the merge queue.
5. Blocks I can't understand ("violation, 0.93" with no evidence and no path to green).
6. Blocking on claims inferred from the agent's own PR text, which defeats the purpose.
7. A security review finding source code in third-party logs.

### Q8. The single metric

**Actioned-block rate**: of all `block` or `review` outcomes, the fraction that led to a **code change or a claim re-lock** rather than an override or an unchanged re-run.

It is a live precision proxy that needs no labeling, it is hard to fake, and it measures the thing that matters, which is whether the gate's opinion changed what got merged. The guardrail metric next to it is the **escaped-violation rate**: post-merge reverts or incidents traced to a locked claim the gate passed.

---

## 6. The inventor's angle: making it a tool developers want, not just tolerate

Gates are bought by platform teams and resented by developers. To get pull rather than push, give developers something that saves them time **before** the gate.

1. **`intent.lock`, a lockfile for intent.** It is written automatically when the human approves the agent's plan (an agent hook on plan approval), or from an issue with `intentgate lock #1234`. The lock holds the original prompt, the ratified claims and their scopes. At approval time it also shows a "plan vs your prompt" diff, so the human ratifies with eyes open. The lock becomes the agent's brief **and** the gate's spec. Developers get a sharper plan review for free; the gate gets human-ratified claims for free. This one primitive fixes §2.1, makes Phase 3 (requirement drift = lock diff) trivial, and creates a visible artifact that spreads from team to team.
2. **Claim coverage as a familiar number.** "4/5 claims satisfied, 1 unverifiable" sits next to test coverage in the PR. Developers already understand coverage; don't make them learn calibration.
3. **A local pre-flight with a visible question set.** `intentgate check` runs in the agent loop (as a Claude Code, Codex or Cursor hook) using a **visible** question set, so agents self-correct before pushing. The **hidden** set runs only in CI. This removes the reason to game the visible set (it's meant to be optimized against) while keeping the independent check. It also turns the "Goodharting" risk into a feature: two tiers, by design.
4. **Scope-creep diff view.** "These 3 files are outside every locked claim's scope" is the single most useful comment for reviewing agent PRs. It is cheap, almost deterministic and rarely wrong. **Lead the product with this.**
5. **Overrides are the data flywheel, and the moat.** Each override with a reason is a labeled, repo-specific calibration example. After six months, per-org calibration on its own code is something a forge's "bring your own scorer" cannot copy from day one. This is the answer to "feature vs company" in §12, and it should be stated as the strategy, not only listed as a risk.

---

## 7. Proposed re-scope of the phasing

| Phase | Ship | Gate mode | Exit criterion |
|---|---|---|---|
| **0 (4–6 weeks)** | CLI, `intent.lock` written on plan approval (agent hook) or from issues, plan-vs-prompt diff at ratification, deterministic scope-creep check, SARIF and check-run output | Advisory only | 3 design-partner repos use locks on more than 50% of agent PRs |
| **1** | Jev claim verification on locked claims, replay, override flow, merge-group support, claim-level labeling harness with seeded mutations | Shadow, then advisory | PR-level false-block rate ≤ 2%, actioned-block rate ≥ 70%, unverifiable rate ≤ 30% on target paths |
| **1.5** | GitHub App UX, policy dry-run, per-path `fail_closed`, question-library CI, secret-scan before egress | Blocking on opt-in paths | One partner makes it required on at least one path for 30 days without demotion |
| **2** | Agent-loop pre-flight (visible set), session goal drift from standard agent logs | Warn-only | Measurable reduction in blocks at CI time (agents self-correcting) |
| **3** | Requirement drift as lock diff, traceability adapters (StrictDoc, Doorstop, Jama), self-hosted model path | Per policy | First regulated customer passes a security review |

---

## 8. Additions to the plan's risk register

| Risk | Severity | Mitigation |
|---|---|---|
| Unratified, agent-authored intent (circular verification) | **Critical** | Intent lock at plan approval, original prompt kept as root intent (§2.1) |
| Rubber-stamp ratification of agent plans | High | Plan-vs-prompt diff at approval, ≤ 8 claims, review time recorded in the audit log (§2.1) |
| `neutral` satisfies required checks | **Critical** | Mode × outcome conclusion table; active reviewer routing (§2.2) |
| Validation labels don't match the prediction target | High | Claim-level labels, seeded mutations, inter-rater check (§3.2) |
| Check output leaks the hidden set to agents | High | Hide from agents, not humans; access-controlled audit store (§2.4) |
| PR-level false-block compounding across claims | High | PR-level aggregation and reporting (§3.1) |
| Non-reproducible re-runs | High | Replay reads stored distributions (§3.6) |
| High unverifiable rate on high-value claims | Medium | Track as an SLO and a kill criterion (§3.3) |
| Auto-demote abuse | Medium | Incident plus human ack (§3.4) |
| Secrets in egressed packets | Medium | Pre-egress secret scan, `never_egress` paths (§3.7) |

---

## 9. Bottom line

The architecture is sound, and the oracle-routing framing is what makes it credible to a DevOps audience. The weak points are where the claims come from, how GitHub interprets the check's conclusions, and whether the evidence can justify the word "calibrated". All three can be fixed, and the fixes (intent lock, an explicit conclusion table, claim-level validation) also make the product more appealing to developers, not just more correct.

Ship scope-creep detection on locked intent first. Earn trust in advisory mode. Let overrides build the calibration moat. Then ask for "required" on the paths where it has proven itself.
