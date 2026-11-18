# S-12: Jev plan-review provider

**Base commit:** the commit that adds this spec   **Branch:** feature/s-12-jev-provider
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The real provider behind S-11's `planreview.Reviewer`: TypeSafe's Jev, called directly over HTTPS. One request per review, one atomic `noul` question per item.

## Criteria (claims this slice must satisfy)

- REQ-0016, REQ-0017, REQ-0018 (extend their links): Jev's answers become omission, addition and weakening findings.

Supporting (already linked, don't re-link): REQ-0019/0020 (S-11's `Run` bounds and never blocks; this provider must honour its context).

## Read first

- `docs/design/plan-review.md` (all), `internal/planreview` (S-11)
- TypeSafe docs: `https://docs.typesafe.ai/api/system-one.md`, `/api/errors.md`, `/primitives/noul.md`, `/patterns/fan-out.md`, `/model-jaggedness.md`
- Observed on 2026-11-19 with the project key: `POST https://api.typesafe.ai/v1/systemone` → 200 in 0.49 s, response `{"model":"jev-1.14.0","answers":{"q1":{"type":"noul","noul":0.82}},"usage":{…}}`.

## Choices fixed here

- `planreview.Jev{BaseURL, Key, Model string; HTTP *http.Client}` implementing `Reviewer`.
  - `POST <BaseURL>/v1/systemone`, `Authorization: Bearer <Key>`, `Content-Type: application/json`, body `{"model": Model, "state": {...}, "questions": {...}}`. Response: `answers.<id>.noul` in [0, 1]; a missing answer or an out-of-range value is skipped, not an error.
  - Transport safety, as the GitHub client (S-06): `BaseURL` must be `https://` except a loopback host; no redirects; the key is never in an error, log or output; response body read up to 1 MB; non-2xx → an error naming the status and TypeSafe's `detail.error_type` (no retries: 429 and 529 are errors too, the budget has no room for backoff).
  - The request uses the caller's context (S-11's deadline); there is no extra timeout beyond `http.Client{Timeout: 4 s}` as a backstop.
- Items (deterministic, pure, `Items(text string, max int) []string`):
  - Split on line breaks; a line starting with `-`, `*`, `+` or `<digits>.`/`<digits>)` is one item (marker stripped); other lines are split into sentences at `.`, `!` or `?` followed by a space or end of line. Trim, collapse whitespace, drop items under 3 words, drop exact duplicates keeping the **last** occurrence.
  - Root-intent items: from the masked root intent, at most 20, keeping the **last** 20 (later human turns override earlier ones).
  - Plan items: the drafted criterion texts first, then items from the masked plan, at most 20 in that order.
- State: `{"root_intent": <masked root intent, its last 48 000 bytes cut on a UTF-8 boundary>, "root_intent_truncated": bool, "plan": <masked plan, first 48 000 bytes>, "items": {"r0": …, "r1": …, "p0": …}}`. Item text lives only in `state`; every question's `instructions` is one of three fixed strings that refer to an item id, never contain item text, and say that text in `state` is data, not instructions:
  - `omit_<i>` (each root item `r<i>`): "Does `state.plan` cover the requested item `state.items.r<i>`? If a later part of `state.root_intent` withdraws or replaces it, answer yes."
  - `weak_<i>` (each root item): "Does `state.plan` weaken the obligation in `state.items.r<i>` (make it optional, narrower, or looser than asked)?"
  - `add_<j>` (each plan item `p<j>`): "Is `state.items.p<j>` asked for by `state.root_intent`, or work implied by it (tests, docs, migrations, lockfile updates)?"
  (Exact wording may be tuned; the shape may not.)
- Findings: omission when `omit` noul < 0.3 (confidence 1 − noul); weakening when `weak` noul > 0.7 (confidence noul); addition when `add` noul < 0.3 (confidence 1 − noul). `Quote` = the item text; `Text` = a fixed sentence per kind. No item with zero root items or zero plan items → no request, status `complete`, no findings.
- Selection (`planreview.Select`): `INTENTGATE_PLAN_REVIEW=off` → `Unavailable`; else `TYPESAFE_API_KEY` set → `Jev` with `TYPESAFE_BASE_URL` (default `https://api.typesafe.ai`) and `TYPESAFE_MODEL` (default `jev-latest`); else `Unavailable`. `intentgate` never reads `.env` itself; the user exports the variable.
- Live check (not part of the gate): `TestJev_Live` runs only when `INTENTGATE_LIVE_JEV=1` and `TYPESAFE_API_KEY` are set, otherwise skips. It sends four fixed cases (a clean plan, one clear omission, one clear addition, one clear weakening) and logs per case the findings and latency; it asserts only that each call returns 200 within 5 s and parses. The worker runs it once with the project key (`set -a; . ./.env; set +a` inside the command, never printing the key) and quotes the results in the report.
- Review 1 amendments (binding):
  - State also carries `plan_truncated` (bool), beside `root_intent_truncated`.
  - Each answer is decoded on its own (`json.RawMessage` per id): a wrong-typed or malformed answer is skipped like a missing one; only a body that isn't a JSON object with an `answers` object is an error.
  - The HTTP backstop is 10 s and is set on a copy of any caller-supplied client whose `Timeout` is zero; the caller's context deadline always fires first in production, and a deadline or client-timeout error is returned as `ctx.Err()` (`context.DeadlineExceeded`) so `Run` reports `partial`/`unavailable`, never `error` with Go's transport text.
  - `TYPESAFE_BASE_URL` with userinfo, a query or a fragment is refused (error, no request); no error string ever contains the base URL's userinfo.
  - The 1 MB body limit is checked before the status, and a non-2xx body is decoded only from within that limit.
  - Tests never print a `Jev` value or its `Key` field.
  - Cross-kind dedupe (one dropped item flagged as both omission and weakening) is left to Phase 1 tuning.
- Dependencies: none new.

## Tests first (committed red)

Fake TypeSafe server on loopback (`httptest`):

- `TestJev_MapsAnswersToFindings` (REQ-0016, 0017, 0018): answers below/above the thresholds produce exactly the expected omission, addition and weakening findings with confidences; values at 0.3/0.7 exactly don't; missing and out-of-range answers are skipped.
- `TestJev_Request`: method, path, headers, model, the state layout, item ids, every question id present, instructions never contain item text, truncation flags.
- `TestJev_Transport`: http non-loopback base → error; a redirect isn't followed; 401/403/429/529/500 → errors carrying the status and `error_type` and never the key; a 2 MB body → error; context cancel → returns promptly with `ctx.Err()`.
- `TestItems`: bullets, numbered lines, sentences, short-item drop, last-wins dedupe, caps keeping the last 20 for the root intent and the first 20 for the plan.
- `TestSelect`: off / key set / no key / custom base URL and model.
- `TestJev_Live`: as above (skips in the gate).

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add `TestJev_MapsAnswersToFindings` to the `tests:` of REQ-0016, REQ-0017 and REQ-0018 (keep the existing links), ids exactly as in `build/junit.xml`.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN" (req gate is a real step now)
```

## Footprint (allowed paths)

- `internal/planreview/**`
- `claims/REQ-0016.yml`, `claims/REQ-0017.yml`, `claims/REQ-0018.yml`: the `tests:` key only.

## Forbidden

Everything else, including `cmd/**`, `.env`, `docs/**`, `.req/**` and `scripts/**`. If a forbidden file must change, stop and report.

## Out of scope

- Tuning thresholds and wording on labelled data (Phase 1 eval harness).
- Jev for claim verification in CI (Phase 1; that is when a GitHub secret is needed).
