# IntentGate v2: review report

**Reviewing:** `docs/intent-gate-proposal-v2.md`
**Previous review:** `docs/intent-gate-review-report.md` (2026-11-17)
**Reviewer perspective:** senior DevOps engineer and developer-tool builder
**Date:** 2026-11-18

---

## 0. Revision (2026-11-18): adoption first

The first version of this review graded every issue against "would I make this a required check?" Phase 0 is **advisory**, and its job is to get people to use locks. Several fixes below are correct for a blocking or compliance deployment and wrong for Phase 0, because friction at ratification produces exactly the rubber-stamping or abandonment the lock is meant to prevent. The triage below replaces the severity labels in the rest of this report wherever they conflict.

**The reframe that matters most: the agent is not the adversary.** Coding agents don't scheme to ratify their own plans. The realistic failure is a *helpful* agent running `intentgate lock` as part of its workflow, skipping the human by accident. The cheapest fix is also the most adoptable one: **the agent harness's own plan-approval UI is already a human-presence check.** When a user clicks "approve" on a plan in Claude Code, Kiro or Spec Kit, the agent can't click it for them. A hook on that moment gives you a lock that is zero-friction **and** human-ratified. That's a stronger reason to ship one hook in Phase 0 (§1, pushback 1) than the adoption argument alone.

| Issue | Phase 0 (advisory): the minimum | Before a path blocks | Compliance tier only |
|---|---|---|---|
| Agent self-ratifies (§2.1) | Lock created by the harness plan-approval hook, or by a CLI that asks for an interactive confirm. Record the method (`hook`, `cli-confirm`, `issue`). Enforce nothing. | Policy option: blocking paths only accept locks from `hook` or the web UI. | Passkey/WebAuthn ratification, signed records. |
| Lock tampered in branch (§2.2) | The gate already knows the lock hash from creation. Flag `suspect` if `.intent/**` changes in the PR diff. Tens of lines, no crypto. | Same, plus CODEOWNERS on `.intent/**`. | Signed locks (sigstore/passkey). |
| App down blocks merges (§2.3) | **Not applicable.** An advisory check isn't required, so an outage blocks nothing. | Watchdog deadline that posts the fail-open result; approval rewrites the check; ruleset bypass actors as the documented break-glass; pin the check source. | Separate highly available gatekeeper service with its own SLO. |
| Lock rot (§3.1) | Re-ratify only on retarget and lock age. That's simpler than v2, not more work. | Add re-ratification on a conflict in a locked scope. | — |
| Scope false positives (§3.2) | **Required.** Implicit scopes for tests, fixtures, lockfiles and generated files, plus a one-tap scope amendment. This is the main adoption risk. | — | — |
| Seeded labels inflate precision (§3.3) | No engineering cost; just don't quote seeded precision. | Precision from adjudicated real shadow blocks. | — |
| Agents game actioned-block rate (§3.4) | — | Split human- and agent-actioned blocks. | — |
| Human-only PRs (§3.5) | Missing lock is silent. That's the simplest possible default. | `require_lock` policy per path. | — |
| `root_intent` privacy (§3.6) | Don't commit `root_intent`, only its hash. Reuse the packet secret scanner. | — | Turn selection at ratification, retention policy. |

**What Phase 0 actually needs from this report:** the harness hook as the main lock path, a `suspect` flag on `.intent/**` edits, implicit scopes, and not committing `root_intent`. Everything else waits until a path is about to block.

**The one thing I'd keep regardless of phase:** don't claim "the agent can't game it" in marketing or to security reviewers until ratification is enforced. Record the ratification method from day one so the claim can be made truthfully later, but don't make it early.

---

## 0.1 Original verdict

v2 is a real revision, not a cosmetic one. All four critical issues from the first review are addressed, the conclusion table is correct about `neutral`, and the phasing now leads with something people can adopt. Your four pushbacks in §0.1 are fair, and I accept all of them (see §1).

**Phase 0: go.** I would run Phase 0 as written, once the three new critical issues below are fixed. All three come from the intent lock itself. The lock is now the trust anchor of the whole system, so it gets attacked and it fails in new ways:

1. **The agent can ratify its own plan.** `intentgate lock --from-plan plan.md` is a CLI, and coding agents have a shell and usually the human's GitHub token. Unless ratification requires something the agent can't do, the circular-verification problem from v1 comes back through the lock.
2. **A lock committed to the branch is not outside the PR's control.** §3.1 lists "`.intent/<id>.lock` commit" as an option for storing it outside the PR's control. It isn't: the agent can edit and re-hash it in the same PR.
3. **If the App is down, every blocking path is hard-blocked, not failed open.** A required check that is never posted stays "Expected — waiting for status". The conclusion table only covers the case where the gate is up enough to post a conclusion. And "fail-closed means human approval" needs the gate to be up to flip the check after that approval.

There are also four significant issues: lock rot on base-branch change, scope false positives from files nobody could predict, seeded labels inflating precision, and agents gaming the actioned-block rate.

---

## 1. Response to your pushbacks (§0.1)

| # | Your pushback | My position |
|---|---|---|
| 1 | Plan-approval hooks are fragmented; ship a manual lock first | **Agree**, with one change. Ship **one** hook in Phase 0 for whichever agent your design partners use most. Claude Code, for example, already has hooks that fire around plan-mode exit, so this is days of work, not a phase. The manual path stays the universal fallback. Without at least one zero-friction path, the ">50% of agent PRs locked" exit criterion is unlikely. |
| 2 | Scope-creep first is a wedge, not a moat | **Agree.** The plan-vs-prompt diff is also a wedge. An agent vendor can add it to plan mode in a sprint. The moat is the per-org override and calibration data plus the policy layer, as you say. |
| 3 | 300 labels is a public bar, not an internal Phase 1 bar | **Agree**, with a caveat on what seeded labels can prove (§3.3). |
| 4 | Fail-closed should mean human approval, not no merge | **Agree.** It does need a mechanism that works while the gate is down (§2.3). |

---

## 2. New critical issues

### 2.1 Ratification must be something the agent cannot do

The lock moves the trust problem from "who wrote the claims" to "who clicked ratify". In current agent setups:

- The agent runs in a shell, so it can run `intentgate lock` itself.
- The agent often has `gh` authenticated as the human, so it can post `/intentgate ratify` as the human.
- `ratification.who` will then record the human, truthfully, from the API's point of view.

**Fix:** ratification has to require **user presence** through a channel the agent's credentials can't reach.

- **Recommended:** a ratify page served by the gate (or a local confirm UI) that requires a **passkey/WebAuthn tap**. It's one touch, and agents can't do it. This also gives you a cryptographically signed ratification record for free, which compliance buyers will value.
- **Acceptable for Phase 0:** ratification through the GitHub web UI only (a button on the App's check or issue comment), with the App rejecting ratify events whose request came from a token or OAuth app rather than a web session. This depends on what GitHub exposes; verify it before relying on it.
- **Not acceptable:** a CLI command or slash comment with no presence check. If you keep `--from-plan` (and you should), it should *propose* a lock, which a human then ratifies through the presence-checked path.
- Record **how** ratification happened (`passkey`, `web-session`, `cli-unverified`). Policy can then say "blocking paths require `passkey`-ratified locks".

### 2.2 The lock needs tamper evidence, not just a hash

A hash proves nothing if the attacker can recompute it. Options, in order of preference:

1. **Server-side canonical copy.** The gate stores the lock at ratification and only trusts its own copy. Any `.intent/` file in the repo is a convenience mirror.
2. **Signed lock in the repo.** Sign it at ratification (sigstore/gitsign, or the passkey signature from §2.1), and have the gate verify the signature against the ratifier's identity.
3. Either way, **any change to `.intent/**` inside a PR diff marks the claims `suspect`** unless it matches a ratified amendment. Put `.intent/**` under CODEOWNERS.

Remove "`.intent/<id>.lock` commit" from the "outside the PR's control" list in §3.1, or qualify it with "signed".

### 2.3 Availability: the App is the single point of failure

The conclusion table assumes the gate posts *something*. Three cases it doesn't cover:

| Failure | What actually happens on GitHub | What v2 implies |
|---|---|---|
| Verification backend (Jev, the LLM) down, App up | App posts `action_required` | Matches the table ✔ |
| App fully down | Required check never posted. PR shows "Expected — waiting for status". **Every PR touching a blocking path is hard-blocked**, not just `fail_closed` ones | Fail-open to human review ✘ |
| `action_required` on a fail-closed path, CODEOWNERS approves | The check stays `action_required`. Approving a review doesn't change a check's conclusion; only the App can rewrite it. If the App is down, there is no path to merge | "Fail-closed means human approval" ✘ |

**Fix:**

- **Split the App into a small, highly available "gatekeeper" and the verification backend.** The gatekeeper only receives webhooks, posts the check, applies the policy fallback and listens for approvals. It has no model dependencies and should have at least 99.9% availability. A **watchdog deadline** (for example 5 minutes) makes it post the fail-open conclusion if the backend hasn't answered.
- **Define the approval-to-conclusion mechanism explicitly.** The gatekeeper subscribes to `pull_request_review` and, when a qualifying CODEOWNERS approval arrives, rewrites the check to `success` with `override: human-approval` recorded.
- **Break-glass for full outages uses GitHub's own ruleset bypass list**, not the gate. Put on-call in the ruleset's bypass actors. GitHub's audit log records the bypass, and the gate records it on recovery. This is the only break-glass that works while the gate is down. Document it as the runbook step.
- Pin the required check to the App as its source (rulesets support this). Otherwise anything with `checks:write` can spoof a green check, including an agent's workflow.
- Note for distribution: the `action_required` conclusion is only available through the Checks API from an App. A reusable Action or GitLab CI job can only pass or fail. **Blocking mode therefore needs the App**, and the Action and CLI are advisory-only, or "review" maps to `failure` there. Say this in §8.

---

## 3. Significant issues

### 3.1 Lock rot on "base-branch change" will churn constantly

§3.5 says locks require re-ratification on base-branch change. If that means new commits on `main`, then in a busy repo every lock goes stale within hours, and people will either re-ratify blindly (rubber-stamping, which §3.5 is meant to prevent) or stop using locks.

**Fix:** trigger re-ratification on a **retarget** (base branch name changes), on an **edit to the source issue**, on a **lock age** limit (for example 14 days), and on a **conflict in a locked scope** (someone else's merge touched files in this lock's scopes). Commits on base that don't touch locked scopes should not affect the lock.

### 3.2 Deterministic scope checks will flag files no plan could predict

Scopes are written before any code exists. The first implementation will touch files nobody could have listed: tests for touched modules, fixtures, snapshots, generated code, lockfiles, migrations, docs, a shared utility that needed one more parameter. v1 suggested a model question ("is this out-of-scope change incidental or substantive?"). v2 makes Phase 0 purely deterministic, so it needs another way to control noise:

- **Implicit scopes by default:** tests and fixtures paired with any in-scope module, generated and vendored files, lockfiles, `CHANGELOG`/docs. All of these are configurable in `.intentgate.yml`.
- **A scope amendment fast path:** a "this file is fine, add it to the scope" button. It is a ratified amendment, but it takes one tap. The **scope-amendment rate** becomes the Phase 0 noise metric. If more than about 40% of locks need a scope amendment, the scope suggestions are wrong.
- Rank flags by distance: a new top-level directory or a second service is worth flagging loudly; a sibling file in the same package is worth a quiet note.

### 3.3 Seeded labels inflate precision; measure at real prevalence

Seeded mutations are good for **recall** and for regression tests. They overstate **precision**, for two reasons:

1. Seeded violations are easier to spot than real ones.
2. Precision depends on the base rate. Seeded sets are often 50% positive, while real agent PRs may violate a locked claim 2–5% of the time. A question with 95% sensitivity and 95% specificity has about 95% precision at 50% prevalence, but only about **28–50% precision at 2–5% prevalence**.

**Fix:** the 90% precision kill criterion and the "make it blocking" decision must use **real shadow-mode blocks, adjudicated by humans**, not seeded data. Seeded sets measure recall and catch regressions; shadow traffic measures precision. Say this in §9.4.

### 3.4 Agents can game the actioned-block rate

The actioned-block rate was my proposed north-star metric, and in an agent workflow it is easy to inflate. Agents respond to *every* block by changing code, because changing code is cheap for them. A false block followed by a needless agent edit counts as "actioned".

**Fix:** split the metric:

- **Human-actioned block rate:** a human changed code, re-locked or explicitly accepted the block.
- **Agent-actioned blocks:** report these separately and sample them for audit ("was this change necessary?").
- Add **block-then-revert**, where an agent's "fix" for a block is later reverted, as a false-block signal.

### 3.5 Human-authored PRs and agent detection

v2 is silent on PRs with no agent involved. If a missing lock means `unverifiable`, and `unverifiable` on a blocking path means `action_required`, then every human PR to `auth/**` needs an extra approval. That's friction the gate didn't earn.

- Make it a policy setting: `require_lock: agent-authored | all | none`, per path.
- Agent detection (commit trailers, bot identities, session metadata) is unreliable and easy to strip, so don't rely on it for security. A reasonable default: on blocking paths, a missing lock routes to the CODEOWNERS approval that is usually required anyway, with no *additional* gate friction; on advisory paths, a missing lock is silent.

### 3.6 Root intent is sensitive data

`root_intent` stores discussion turns verbatim. Those can contain customer names, credentials pasted for debugging, or incident details. It is stored, possibly committed to `.intent/`, and egressed for the plan-vs-prompt diff.

- Secret-scan the lock at creation, and apply the same `never_egress` and retention rules as packets.
- Decide **which turns** count as the root intent. If the agent picks them, that is an agent act inside the trust anchor. Default to **all human turns in the session**, with later turns overriding earlier ones, and let the human deselect turns at ratification.
- Don't commit `root_intent` to the repo by default. Keep it in the server-side store and commit only its hash.

---

## 4. Smaller issues

- **The plan-vs-prompt diff will flag legitimate refinements.** When a human changes their mind during the discussion, the diff marks the earlier ask as an "omission". Treating later human turns as overriding earlier ones (§3.6) fixes most of this. The diff should also suppress implied work (tests, migrations, docs) from "additions". It is advisory at ratification, so noise costs less here, but it still erodes trust.
- **Ratification latency:** the diff runs while a human waits. Budget p95 ≤ 5 s. Jev's 70–500 ms per call makes this feasible; the claim-extraction LLM call is the risk.
- **The claim caps are inconsistent:** §3.1 caps claims per lock at 8; §9.2 caps them per PR at 8. A lock can span several PRs. State both explicitly, and say that features bigger than 8 claims are split into several locks.
- **"Acceptance criteria in the human's words"** (§3.1) contradicts the plan being agent-drafted. Say "in the ratified plan's words".
- **The visible pre-flight set** (§11) is "disjoint from, or a rotated sample of, the CI set". Choose **disjoint**. Any rotated sample leaks the hidden set over enough runs, which §9.3 itself predicts.
- **§0 says "all four are fixed"**, but the table has six rows. Say "four critical, two significant".
- **The `0.3–0.5` band now escalates**, so the escalation band is 0.3–0.9. That's correct, but watch escalation volume. If more than about 15% of PRs escalate, reviewers will start ignoring escalations.
- **Phase 0 exit criterion:** ">50% of agent PRs locked" measures adoption, not value. Add "≥ 30% of scope flags or plan-diff findings led to a change, a split or an amendment". Otherwise you could graduate a feature people use but ignore.
- **Parallel agents and worktrees:** one lock may drive several agents in several branches at once. The claim graph handles this if claims have global IDs and `partially-satisfied-by` edges. Make sure the lock ID is not tied to a branch.

---

## 5. Answers to your open questions (§13)

**1. Is a manual lock enough for Phase 0?**
It's enough for design partners, provided it is ratified through a presence-checked path (§2.1). Ship one agent hook in Phase 0 for your partners' main agent, or locking will feel like homework, and the >50% exit criterion will measure diligence rather than value.

**2. Is the plan-vs-prompt diff the real wedge, with PR gating as the second act?**
Yes. It runs before code exists, can't block a merge, gives value in the first session and needs no calibration evidence to be useful. It's the cleanest wedge you have. But it's a wedge for *usage*, not for *revenue*. Platform teams pay for the gate, the audit trail and the policy layer, so keep the gate on the roadmap as the paid layer.

**3. Is "scope-creep + lock" enough for advisory adoption, or does the semantic check need to be in Phase 1?**
Scope plus lock is enough for advisory adoption **if** implicit scopes keep the noise down (§3.2). Running the semantic check in shadow during Phase 1, as planned, is right: it collects the real-prevalence data you need anyway (§3.3) at no cost to user trust.

**4. Minimum calibration evidence before a path becomes blocking?**
All of the following, per repo and per path family:

- ≥ 4 weeks in shadow mode on **that** repo.
- ≥ 50 real (not seeded) shadow-mode blocks, adjudicated by humans, with ≤ 2 false. That puts the lower bound of a 95% Wilson interval on precision at about 86%. 30 blocks with 1 false only gets you to about 83%.
- PR-level false-block rate ≤ 2%.
- ECE ≤ 0.05 for the question families that can block.
- `unverifiable` ≤ 30% on that path.
- 100% replay determinism on sampled verdicts.
- Inter-rater agreement ≥ 85% on the labeling set.

**5. Does fail-closed-as-human-approval resolve the availability concern?**
Only if approval can turn the check green without the verification backend, and break-glass works without the gate at all (§2.3). With a highly available gatekeeper, a watchdog deadline and ruleset bypass actors, yes. I would not keep a hard block anywhere.

**6. How important are cross-repo claims for the first design partners?**
Not important. Pick partners whose agent work is mostly single-repo, and design the data model so it can be added later (global claim IDs, lock ID not tied to a repo). Cross-repo claims are a Phase 3 enterprise feature.

**7. What would make me turn it off after a week?** (updated for v2)

1. Learning in a security review that the agent can ratify its own lock.
2. A gate outage blocking every PR on blocking paths, with no documented break-glass.
3. Re-ratifying locks every time `main` moves.
4. Scope flags on every test, fixture and lockfile.
5. Human-only PRs needing an extra approval because they have no lock.
6. Approving a fail-closed PR and still not being able to merge it until someone re-runs the check.
7. Anything from the v1 list that comes back: non-reproducible verdicts, comment spam, slow merge queue.

---

## 6. Additions to the v2 risk register

| Risk | Severity | Mitigation |
|---|---|---|
| Agent self-ratifies its own plan | Low in Phase 0; **Critical** for compliance claims | Phase 0: harness plan-approval hook as the main path, record the method. Later: web-session or passkey ratification required on blocking paths (§0, §2.1) |
| Lock tampered in the PR branch | Medium in Phase 0; **Critical** before blocking | Phase 0: edits to `.intent/**` mark claims `suspect`. Later: server-side canonical copy or signed lock (§0, §2.2) |
| App fully down hard-blocks blocking paths | None in Phase 0 (advisory); **Critical** before blocking | Watchdog deadline, ruleset bypass actors as break-glass; separate highly available gatekeeper only at enterprise scale (§0, §2.3) |
| Spoofed green check | High | Pin the required check to the App as its source |
| Lock churn on base-branch movement | High | Re-ratify only on retarget, issue edit, age, or a conflict in a locked scope (§3.1) |
| Scope false positives | High | Implicit scopes, one-tap scope amendments, scope-amendment rate as a metric (§3.2) |
| Precision overstated by seeded data | High | Precision only from adjudicated real shadow blocks (§3.3) |
| Agents inflating the actioned-block rate | Medium | Split human- and agent-actioned rates; track block-then-revert (§3.4) |
| Friction on human-only PRs | Medium | `require_lock` policy per path (§3.5) |
| Sensitive data in `root_intent` | Medium | Secret scan, server-side storage, human-selected turns (§3.6) |

---

## 7. Bottom line

v1's problems were about **what the gate believes**. v2 fixes those, and its problems are now about **what the gate trusts**: who ratified the lock, where the lock is stored, and what happens when the gate itself is down. These are normal problems for a system that is becoming a real control, and all three have standard fixes: user presence, signatures or a server-side source of truth, and a small highly available component with platform-level break-glass.

For Phase 0, follow the triage in §0: lock from the harness hook, flag `.intent/**` edits, ship implicit scopes, keep `root_intent` out of the repo. The full fixes in §2.1–§2.3 are the price of the **blocking** tier, paid when a design partner asks for it, not before.
