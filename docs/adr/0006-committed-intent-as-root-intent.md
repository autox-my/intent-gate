# ADR 0006: A committed, owner-approved `intent.md` may be a lock's root intent (Phase 1, opt-in)

**Status:** Proposed

## Context

A lock today starts from an agent's plan or a GitHub issue. Its root intent (the prompt or request behind it) is masked and kept on the ratifier's machine; only its SHA-256 is committed (ADR 0002). The AI-native SDLC playbook puts a reviewed `intent.md` (what, why, constraints, accepted by a product owner) at the head of the chain, and our process map marks its absence as a gap (`docs/process/sdlc-map.md`). A chat prompt is an unreviewed root intent: nobody but the ratifier has seen it, and it covers one agent session, not work that spans several pull requests or is written by people.

## Decision

- **Opt-in, next to the existing sources.** A lock may name a committed intent file as its root intent. Plans and issues stay; a team that never uses intent files sees no change.
- **One intent per piece of work.** An intent file lives at `intents/<slug>.md` (outside `.intent/`, which holds only locks) and describes one larger piece of work. Several locks may point to the same intent; an intent is not written per change, because the plan already is.
- **Accepted means merged with an owner's approval.** An intent is accepted when the commit that last changed it reached the default branch through a pull request approved by a code owner of that file (GitHub `CODEOWNERS`). This reuses the forge's review instead of a new trust mechanism.
- **The lock records it; nothing enforces it.** The lock's root intent gets a new source, `committed-intent`, with the file's path and blob hash at the default branch. The lock's own ratification is unchanged: criteria and footprint are still approved per lock through `hook`, `cli-confirm` or `issue` (ADR 0003), because an intent does not say which paths one change may touch. Whether the intent was accepted is recorded and reported by the PR check, never a reason to block, in line with Phase 0/1's advisory stance and ADR 0003.
- **ADR 0002 stays the default.** Choosing a committed intent is choosing to make that intent public to everyone with repository access. The root intent of every other lock stays local and masked.

## Consequences

- The lock schema (`intentgate.lock/1`) gains `committed-intent` as a root-intent source and fields for the path and blob hash; the PR check gains an "intent accepted" result (yes, no, or could not tell without the forge). This needs claims, a design section and slices before any code.
- Checking acceptance needs the forge's review API, so the CLI can only record the intent's path and hash; the Action, which has a token, reports acceptance. Squash and rebase merges are fine here: acceptance is read from the pull request, not from merge commits.
- The intent text is public by the team's choice, so it is not masked; secret masking still applies to plan text and to the root intent of other lock sources.
- The lock's `ratification.method` keeps meaning "how this lock was approved". Intent acceptance is a separate fact about the root intent, so the two can be reported apart (for example in `stats`: locks with an accepted intent).

## Alternatives considered

- **A new ratification method `committed-intent` that writes the lock from the intent file alone:** rejected. An intent covers a piece of work, not one change's footprint; letting it stand in for the per-lock approval would lock paths nobody approved.
- **Any commit on the default branch counts as accepted:** too weak; a direct push or an agent's commit would make an intent "accepted".
- **A signature on the intent file:** stronger, but a new key and trust mechanism for every product owner; the forge's code-owner review already exists.
- **One `intent.md` per change:** duplicates the plan, which is already reviewed per change.
- **Make every root intent public:** breaks ADR 0002's privacy default for teams that never opted in.
