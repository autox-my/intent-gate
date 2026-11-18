# IntentGate

Lock the intent behind agent-written code before implementation, then check pull requests
against it: scope drift first (deterministic), calibrated semantic verification later.

- Design: [`docs/intent-gate-proposal-v2.md`](docs/intent-gate-proposal-v2.md)
- Reviews: [`docs/intent-gate-review-report.md`](docs/intent-gate-review-report.md),
  [`docs/intent-gate-v2-review-report.md`](docs/intent-gate-v2-review-report.md)
- Requirements: `claims/` (managed with [req](https://github.com/autox-my/req));
  `req canon` lists them with their derived parse.

Status: Phase 0, pre-implementation.
