package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/autox-my/intent-gate/internal/forge/github"
)

// `intentgate lock --issue <n>` (ratification.md §3): the issue is both the root intent and the
// plan. It reuses confirmAndWrite, so it asks for the same interactive `yes` as --from-plan.
//
// Exit codes: 0 written; 1 not confirmed or a drafting error; 2 usage, no terminal, no token, not an
// issue, no repo; 3 any API or network error, scan failure, git failure.

const (
	defaultAPIURL = "https://api.github.com"
	partSep       = "\n\n---\n\n"
	noTokenMsg    = "intentgate lock --issue needs a GitHub token (GITHUB_TOKEN, GH_TOKEN or gh auth login)"
)

var (
	repoArg = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	// git@github.com:owner/name(.git)
	scpRemote = regexp.MustCompile(`(?i)^(?:[\w.-]+@)?github\.com:([^/\s]+)/([^/\s]+?)(?:\.git)?/?$`)
	// https://github.com/owner/name(.git), ssh://git@github.com/owner/name(.git)
	urlRemote = regexp.MustCompile(`(?i)^(?:https?|ssh|git)://(?:[^@/\s]+@)?github\.com(?::\d+)?/([^/\s]+)/([^/\s]+?)(?:\.git)?/?$`)
)

func validRepo(s string) bool { return repoArg.MatchString(s) }

// repoFromRemote parses a git remote URL on github.com into owner and name.
func repoFromRemote(url string) (owner, name string, ok bool) {
	url = strings.TrimSpace(url)
	for _, re := range []*regexp.Regexp{scpRemote, urlRemote} {
		if m := re.FindStringSubmatch(url); m != nil && m[1] != "" && m[2] != "" {
			return m[1], m[2], true
		}
	}
	return "", "", false
}

// gitRemoteURL is `git remote get-url <name>` in dir.
func gitRemoteURL(dir, name string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", name)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git remote get-url %s: %w", name, err)
	}
	return strings.TrimSpace(out.String()), nil
}

// ghAuthToken is `gh auth token`, if gh is on PATH. Its output is the token and is never shown; a
// failure says only that it failed.
func ghAuthToken() (string, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return "", err
	}
	out, err := exec.Command(path, "auth", "token").Output()
	if err != nil {
		return "", errors.New("gh auth token failed")
	}
	return strings.TrimSpace(string(out)), nil
}

// githubToken is $GITHUB_TOKEN, else $GH_TOKEN, else `gh auth token`; "" if there is none.
func githubToken(deps Deps) string {
	for _, v := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if tok := strings.TrimSpace(os.Getenv(v)); tok != "" {
			return tok
		}
	}
	if deps.GHAuthToken != nil {
		if tok, err := deps.GHAuthToken(); err == nil {
			return strings.TrimSpace(tok)
		}
	}
	return ""
}

// sameAuthor reports whether a comment's author is the issue's author: both non-empty and equal
// ignoring case. A deleted account (no user, or GitHub's placeholder "ghost") is nobody in
// particular, so it never matches, not even another deleted account.
func sameAuthor(comment, issue string) bool {
	known := func(s string) bool { return s != "" && !strings.EqualFold(s, "ghost") }
	return known(comment) && known(issue) && strings.EqualFold(comment, issue)
}

// issueText is the issue body, then each comment by the issue's author in API order, joined with
// a rule; each part ends in a newline and an empty part is left out. It is both the root intent and
// the plan. included counts the comment parts that are in the text.
func issueText(is github.Issue, comments []github.Comment) (text string, included int) {
	var parts []string
	add := func(s string) bool {
		if strings.TrimSpace(s) == "" {
			return false
		}
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		parts = append(parts, s)
		return true
	}
	add(is.Body)
	for _, c := range comments {
		if sameAuthor(c.Author, is.Author) && add(c.Body) {
			included++
		}
	}
	return strings.Join(parts, partSep), included
}

func runLockIssue(t *term, n int, repoFlag string) int {
	deps := t.deps
	if !t.requireTerminal() {
		return exitUsage
	}

	owner, name, ok := splitRepo(repoFlag)
	if !ok {
		if url, err := deps.Git.RemoteURL(deps.Dir, "origin"); err == nil {
			owner, name, ok = repoFromRemote(url)
		}
	}
	if !ok {
		return t.fail(exitUsage, "intentgate lock: cannot tell the GitHub repository: pass --repo owner/name")
	}
	token := githubToken(deps)
	if token == "" {
		return t.fail(exitUsage, "%s", noTokenMsg)
	}
	api := os.Getenv("GITHUB_API_URL")
	if api == "" {
		api = defaultAPIURL
	}
	client := &github.Client{BaseURL: api, Token: token}
	ctx := context.Background()

	issue, err := client.Issue(ctx, owner, name, n)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate lock: %v", err)
	}
	if issue.IsPullRequest {
		return t.fail(exitUsage, "intentgate lock: #%d is a pull request, not an issue", n)
	}
	comments, err := client.Comments(ctx, owner, name, n)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate lock: %v", err)
	}
	viewer, err := client.Viewer(ctx)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate lock: %v", err)
	}

	text, included := issueText(issue, comments)
	drafted, _, err := draftPlanMasked(text)
	if err != nil {
		return t.fail(draftCode(err), "%v", err)
	}
	if drafted.Intent != "" { // the issue is its own root intent (intent-lock.md §6)
		return t.fail(exitUsage, "no lock: an issue is its own root intent; remove intent: from its intentgate block")
	}
	criteria := drafted.Criteria
	top, err := deps.Git.TopLevel(deps.Dir)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate lock: %v", err)
	}
	baseRef, err := deps.Git.BaseRef(deps.Dir)
	if err != nil {
		return t.fail(exitUnverifiable, "intentgate lock: %v", err)
	}
	return t.confirmAndWrite(pending{
		Top: top, BaseRef: baseRef, Criteria: criteria, RootIntent: text, Plan: criteriaAsPlan(criteria), // no plan file: the drafted criteria are what the review weighs against the issue
		Source: "issue", Method: "issue", Ratifier: "github:" + viewer,
		Intro: fmt.Sprintf("Issue #%d: %s (by @%s, %d of %d comments included)", n, issue.Title, shownAuthor(issue.Author), included, len(comments)),
	})
}

func splitRepo(s string) (owner, name string, ok bool) {
	if s == "" {
		return "", "", false
	}
	owner, name, ok = strings.Cut(s, "/")
	return owner, name, ok
}

// shownAuthor is the author as printed: a deleted account has no login, so it shows as ghost.
func shownAuthor(login string) string {
	if login == "" {
		return "ghost"
	}
	return login
}
