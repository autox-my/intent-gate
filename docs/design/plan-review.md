# Design: plan review (plan vs prompt)

<!-- req-satisfies: REQ-0016 -->
<!-- req-satisfies: REQ-0017 -->
<!-- req-satisfies: REQ-0018 -->
<!-- req-satisfies: REQ-0019 -->
<!-- req-satisfies: REQ-0020 -->

The plan review compares the drafted plan with the root intent while the human is deciding whether to approve. It's the only model call in Phase 0, and it's advisory.

## 1. Output

Three lists of findings, each finding quoting the text it is about:

| Kind | Question | Claim |
|---|---|---|
| `omission` | What does the root intent ask for that the plan drops? | REQ-0016 |
| `addition` | What does the plan add that the root intent didn't ask for? | REQ-0017 |
| `weakening` | Where does the plan loosen an obligation from the root intent? | REQ-0018 |

**Rules that keep the noise down:**
- **Later human turns override earlier ones.** If the human changed their mind during the discussion, the earlier request isn't an omission.
- **Implied work is not an addition:** tests, docs, migrations and lockfile updates that follow from the plan.
- Show at most 5 findings per kind, ordered by the model's confidence; the rest are counted ("+3 more").

## 2. Provider interface

```go
type PlanReviewer interface {
    Review(ctx context.Context, rootIntent, plan string) (Findings, error)
}
```

- **Jev provider:** one atomic question per (root-intent item, plan) pair for omissions and weakenings, and per (plan item, root intent) pair for additions. Batched and run in parallel.
- **Unavailable provider:** used when no credentials are configured. It returns no findings and the status `unavailable`.

The root intent sent to the provider is the secret-masked text (ratification.md §5).

## 3. Time budget (REQ-0019)

The whole review runs under a **5-second context deadline**. When the deadline passes, the review returns whatever findings it has with the status `partial` (or `unavailable` if none arrived). It never makes the human wait longer than 5 s.

## 4. Never blocking (REQ-0020)

Every status (`complete`, `partial`, `unavailable`, `error`) lets ratification proceed. The findings are shown to the human, and the status is recorded in the lock as `plan_review.status`, but no outcome refuses or delays the lock.
