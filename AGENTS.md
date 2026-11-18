# IntentGate: agent instructions

## Project

IntentGate locks the intent behind agent-written changes and checks pull requests against it.
Design history lives in `docs/` (proposal v1, v2 and both external reviews). Phase 0 is advisory
only: nothing may block a merge. Vocabulary is in `docs/glossary.md`: in this repo, "claim",
"scope", "gate", "pin" and "diff" always mean req's terms; IntentGate's own are criterion,
footprint, PR check and plan review.

## Requirements are managed with req

All requirements live in `claims/`, one obligation per file, managed with
[req](https://github.com/autox-my/req). The corpus is the source of truth.
`docs/requirements.md` was the adoption source; do not edit it to change a requirement.

- Add a requirement: `req next-id`, write `claims/REQ-NNNN.yml` (copy an existing claim's shape),
  then run `req canon` and read the derived parse.
- Change a requirement: edit its claim's `statement`, run `req canon`. Changing meaning moves its
  address; that is intended.
- Remove a requirement: delete its claim file, deliberately. Never empty `claims/`.
- On a pull request: `req diff --base main --fail-on weakened,removed` shows which requirements
  moved and in which direction. A human ratifies every direction change.
- Every code change should name the claim(s) it implements (for example `Implements: REQ-0024`
  in the commit message).

### req rules (from req's `examples/agents-snippet.md`)

- Use the `req` on `PATH` only after `req --version` shows `claim_schema req.claim/4` and `rules_v 9`. Anything else is an older or newer build: stop and report it; do not work around it.
- To find a command, run `req --help`; `req <command> --help` is that command's contract, with its `exit:` line. Act on the exit code and the named line, not on prose.
- The claim is the unit: one obligation per file under `claims/`, one file per claim.
- A claim is authored: its parse, canonical form and address are derived from `statement` at load and stored nowhere. Never add `address`, `canonical` or a parse slot to a claim file.
- `pins:` states what the parse must be where it is load-bearing; a pin agrees or is a flag. Never edit a pin to match a parse the statement was not meant to have: re-word the statement, or leave the flag for a human.
- `req canon` shows the derived parse and each pin's verdict and does not fail on a flag: it is diagnostics, never a pass.
- Until every claim has a linked test, the gate cannot be green; use `req canon --fail-on-flag` meanwhile and move to `req check --test-report <junit.xml>` when the last claim is linked.
- A committed projection is checked, never edited: `req render --check docs/spec.md`, regenerated with `req render --out docs/spec.md`.
- An exit 3 is not a pass: report what could not be decided and ask a human, and never relax a check to land a change.
- Never auto-merge, and never sign a Baseline: ratification is the human's act.
