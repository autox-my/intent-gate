# S-06: `intentgate lock --issue`

**Base commit:** the commit that adds this spec   **Branch:** feature/s-06-lock-from-issue
**Worker:** claudeAgent / claude-sonnet-5-5   **Reviewer:** opencode / opencode-go/deepseek-v4.1-flash

The third way to ratify: a lock from a GitHub issue, where the issue is both the root intent and the plan (ratification.md §3). Adds the first piece of the GitHub adapter.

## Criteria (claims this slice must satisfy)

- REQ-0003: `intentgate lock --issue <n>` creates a lock from the issue, with method `issue`, source `issue`, after the same interactive `yes` as `--from-plan` (REQ-0005, already linked).

## Read first

- `docs/design/ratification.md` §1, §1.1, §3, §5
- `docs/design/intent-lock.md` §2 (ratifier `github:<login>`)
- `docs/design/core-contracts.md` §3
- `cmd/intentgate/lock.go` (S-05: reuse its confirmation, build and store path; don't fork it)

## Choices fixed here

- Package `intentgate/internal/forge/github`: a small client on `net/http` + `encoding/json` (no go-github yet; one dependency less for three GETs). `Client{BaseURL, Token string; HTTP *http.Client}` with:
  - `Issue(ctx, owner, repo string, n int) (Issue, error)`: `GET /repos/{owner}/{repo}/issues/{n}`; `Issue{Number int; Title, Body, Author string; IsPullRequest bool}` (`pull_request` key present → `IsPullRequest`).
  - `Comments(ctx, owner, repo string, n int) ([]Comment, error)`: `GET …/issues/{n}/comments?per_page=100`, following `Link: rel="next"` until done, in API order; `Comment{Author, Body string}`.
  - `Viewer(ctx) (login string, err error)`: `GET /user`.
  - Headers: `Authorization: Bearer <token>`, `Accept: application/vnd.github+json`, `X-GitHub-Api-Version: 2022-11-28`, a `User-Agent` of `intentgate`. Non-2xx → an error carrying the status and GitHub's `message`. A 30 s request timeout.
- `intentgate lock --issue <n> [--repo <owner/name>]`:
  - Repo: `--repo`, else parsed from `git remote get-url origin` (`github.com:owner/name(.git)`, `https://github.com/owner/name(.git)`, `ssh://git@github.com/owner/name(.git)`); otherwise exit 2 "pass --repo owner/name".
  - API base: `$GITHUB_API_URL` if set (GitHub Enterprise), else `https://api.github.com`. Token: `$GITHUB_TOKEN`, else `$GH_TOKEN`, else `gh auth token` if `gh` is on `PATH`; none → exit 2 "intentgate lock --issue needs a GitHub token (GITHUB_TOKEN, GH_TOKEN or gh auth login)". The token is never printed, logged or written.
  - Text: the issue body, then each comment **by the issue's author** in API order, joined with `"\n\n---\n\n"`, each part ending in `\n`. Other people's comments are excluded. That text is both the root intent (masked by `ratify.MaskAndBuild`) and the plan for `ratify.DraftCriteria` (exactly one `intentgate` block across the whole text).
  - A pull request number → exit 2 "#<n> is a pull request, not an issue".
  - Before the `yes` prompt, print `Issue #<n>: <title> (by @<author>, <k> of <m> comments included)` and the criteria.
  - Lock: source `issue`, method `issue`, ratifier `github:<Viewer login>` (the person confirming, not necessarily the author), base ref `gitx.BaseRef`, `review_duration_ms` from the prompt to the answer.
  - Exit codes: 0 written; 1 not confirmed or drafting error; 2 usage, no terminal, no token, not an issue, no repo; 3 any API or network error, scan failure, git failure.
- Review 1 amendments (binding):
  - The token only travels over TLS to `BaseURL`'s host: `BaseURL` must be `https://`, except a loopback host (`127.0.0.1`, `::1`, `localhost`) for tests; the client follows no redirects (`CheckRedirect` returns an error naming the status and target host, never the token); a `Link` next URL must match `BaseURL`'s scheme and host exactly.
  - `Link` parsing follows RFC 8288: `rel` may be quoted or not and may hold several space-separated relation types; any containing `next` is followed.
  - Plans are masked too, on every path (`--issue`, `--from-plan`, and the S-04 hook): the plan text goes through `ratify.Mask` before `ratify.DraftCriteria`, so criterion text and footprints in the committed lock, and everything printed, never contain an unmasked secret. Tests: a secret inside the `intentgate` block's `text` appears in none of stdout, the lock file, or the root intent record, for all three paths.
  - Comment selection: a comment is included only if both its author and the issue's author are non-empty and equal ignoring case (ghost/null users never match); "<k> of <m> comments included" counts only parts actually included.
  - Remote host matching is case-insensitive (`GitHub.com`).
  - Tests use `t.Setenv`, and one test exercises the `gh auth token` fallback through an injected command.
- Fetch before confirming, write only after `yes`; a failure at any step writes nothing.
- Dependencies: none new.

## Tests first (committed red)

- `TestClient` (forge/github, `httptest.Server`): headers sent (token as Bearer, API version), issue decode incl. `pull_request` → `IsPullRequest`, comments pagination over 3 pages via `Link`, `/user`, a 404 and a 500 → errors carrying status and message, the token never appears in an error string.
- `TestLock_FromIssue` (cmd/intentgate, fake API via `GITHUB_API_URL`, temp repo, fake terminal and clock): body + two author comments + one other-user comment → lock with method `issue`, source `issue`, ratifier `github:<viewer>`, root intent record = body and the author's comments only, criteria from the block; hash verifies. (REQ-0003)
- `TestLock_FromIssueRefusals`: a PR number, no token, no repo/remote, an API 500, no `intentgate` block, not confirmed → the right exit code and nothing written.
- `TestRepoFromRemote`: the three URL forms, with and without `.git`; a non-GitHub remote → no repo.
- `TestLock_FromIssueTokenNotLeaked`: across all refusal paths, the token string appears in neither stdout nor stderr.

The red commit may contain stubs that compile, so the tests fail on assertions. Quote one assertion failure in the report.

## Claim links

After green, add `tests:` to REQ-0003: `TestLock_FromIssue`, id exactly as in `build/junit.xml`.

## Verify

```bash
scripts/check.sh   # run from the worktree root; must end "verdict: GREEN"
```

## Footprint (allowed paths)

- `internal/forge/github/**`
- `cmd/intentgate/**`
- `claims/REQ-0003.yml`: the `tests:` key only.

## Forbidden

Everything else, including `internal/ratify/**`, `internal/lock/**`, `go.mod`, `go.sum`, `docs/**`, `.req/**` and `scripts/**`. If a forbidden file must change, stop and report.

## Out of scope

- Check runs, PR comments, PR metadata (the PR check slices).
- GitHub App authentication.
