# ADR 0005: Phase 0 ships a CLI and a GitHub Action; the App comes in Phase 1.5

**Status:** Proposed

## Context

v2 plans a GitHub App for the hosted path. An App needs hosting, a webhook endpoint, secrets management and an availability story (v2 review §2.3), none of which an advisory Phase 0 needs.

## Decision

- Phase 0 distributes the `intentgate` binary (CLI and agent hook) and a **composite GitHub Action** that downloads a pinned, checksummed release of the binary, runs `intentgate verify`, creates the check run through the Checks API with `GITHUB_TOKEN`, uploads SARIF, and maintains the single PR comment.
- The GitHub App is built in Phase 1.5, when blocking mode needs the `action_required` conclusion and approval-driven re-conclusions, which only an App can provide.

## Consequences

- Nothing to host or keep available in Phase 0. An outage of IntentGate can't block a merge, because the check is advisory and not required.
- The Action needs `checks: write`, `pull-requests: write` and `security-events: write` permissions in the workflow.
- Workflows triggered from forks get a read-only `GITHUB_TOKEN`. For fork PRs, Phase 0 writes the result to the job summary and the SARIF artifact only. Supporting `pull_request_target` safely is deferred.
- GitLab and other forges work through the CLI, with no forge-specific output, until demand justifies an adapter.
