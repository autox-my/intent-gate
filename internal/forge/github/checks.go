package github

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	maxSummary      = 65535 // GitHub's bound on a check run's output.summary, in bytes
	truncatedSuffix = "\n… (truncated)"
)

// CheckRun is the content of one check run: the Checks API fields IntentGate writes. Its status
// is always completed.
type CheckRun struct {
	Name       string
	HeadSHA    string // sent on create only
	Conclusion string
	Title      string
	Summary    string // cut to 65 535 bytes on a UTF-8 boundary, ending "… (truncated)"
}

// FindCheckRun looks for the latest check run called name on commit sha:
// GET /repos/{o}/{r}/commits/{sha}/check-runs?check_name=<name>&filter=latest&per_page=100,
// following Link: rel="next". It returns the first whose name is exactly name.
func (c *Client) FindCheckRun(ctx context.Context, owner, repo, sha, name string) (id int64, found bool, err error) {
	q := url.Values{"check_name": {name}, "filter": {"latest"}, "per_page": {"100"}}
	next := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?%s", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(sha), q.Encode())
	for pages := 0; next != ""; pages++ {
		if pages >= maxPages {
			return 0, false, fmt.Errorf("github: more than %d pages of check runs", maxPages)
		}
		var raw struct {
			CheckRuns []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"check_runs"`
		}
		h, err := c.get(ctx, next, &raw)
		if err != nil {
			return 0, false, err
		}
		for _, r := range raw.CheckRuns {
			if r.Name == name {
				return r.ID, true, nil
			}
		}
		next = nextLink(h.Get("Link"))
	}
	return 0, false, nil
}

type checkOutput struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type checkRunBody struct {
	Name       string      `json:"name"`
	HeadSHA    string      `json:"head_sha,omitempty"`
	Status     string      `json:"status"`
	Conclusion string      `json:"conclusion"`
	Output     checkOutput `json:"output"`
}

func (cr CheckRun) body(withSHA bool) checkRunBody {
	b := checkRunBody{Name: cr.Name, Status: "completed", Conclusion: cr.Conclusion,
		Output: checkOutput{Title: cr.Title, Summary: truncateSummary(cr.Summary)}}
	if withSHA {
		b.HeadSHA = cr.HeadSHA
	}
	return b
}

// CreateCheckRun is POST /repos/{o}/{r}/check-runs and returns the new check run's id.
func (c *Client) CreateCheckRun(ctx context.Context, owner, repo string, cr CheckRun) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	target := fmt.Sprintf("/repos/%s/%s/check-runs", url.PathEscape(owner), url.PathEscape(repo))
	if err := c.send(ctx, "POST", target, cr.body(true), &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// UpdateCheckRun is PATCH /repos/{o}/{r}/check-runs/{id}; it does not send the head SHA.
func (c *Client) UpdateCheckRun(ctx context.Context, owner, repo string, id int64, cr CheckRun) error {
	target := fmt.Sprintf("/repos/%s/%s/check-runs/%d", url.PathEscape(owner), url.PathEscape(repo), id)
	return c.send(ctx, "PATCH", target, cr.body(false), &struct{}{})
}

// truncateSummary keeps s whole when it fits and otherwise cuts it so that, with the final line
// "… (truncated)", it is at most 65 535 bytes, without splitting a UTF-8 sequence.
func truncateSummary(s string) string {
	if len(s) <= maxSummary {
		return s
	}
	cut := maxSummary - len(truncatedSuffix)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncatedSuffix
}

// FindComment returns the id of the first comment on pull request pr whose body starts with marker
// and whose author's login equals author, ignoring case: GET /repos/{o}/{r}/issues/{pr}/comments
// ?per_page=100, following Link: rel="next". A comment by anyone else is never a match, whatever
// it contains.
func (c *Client) FindComment(ctx context.Context, owner, repo string, pr int, marker, author string) (id int64, found bool, err error) {
	if author == "" || marker == "" {
		return 0, false, nil
	}
	next := c.repoPath(owner, repo, pr, "/comments") + "?per_page=100"
	for pages := 0; next != ""; pages++ {
		if pages >= maxPages {
			return 0, false, fmt.Errorf("github: more than %d pages of comments", maxPages)
		}
		var raw []struct {
			ID   int64   `json:"id"`
			Body *string `json:"body"`
			User *user   `json:"user"`
		}
		h, err := c.get(ctx, next, &raw)
		if err != nil {
			return 0, false, err
		}
		for _, r := range raw {
			if r.Body != nil && r.User != nil && strings.HasPrefix(*r.Body, marker) && strings.EqualFold(r.User.Login, author) {
				return r.ID, true, nil
			}
		}
		next = nextLink(h.Get("Link"))
	}
	return 0, false, nil
}

// CreateComment is POST /repos/{o}/{r}/issues/{pr}/comments and returns the new comment's id.
func (c *Client) CreateComment(ctx context.Context, owner, repo string, pr int, body string) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.send(ctx, "POST", c.repoPath(owner, repo, pr, "/comments"), map[string]string{"body": body}, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// UpdateComment is PATCH /repos/{o}/{r}/issues/comments/{id}.
func (c *Client) UpdateComment(ctx context.Context, owner, repo string, id int64, body string) error {
	target := fmt.Sprintf("/repos/%s/%s/issues/comments/%d", url.PathEscape(owner), url.PathEscape(repo), id)
	return c.send(ctx, "PATCH", target, map[string]string{"body": body}, &struct{}{})
}

// FindComments is FindComment returning every match, oldest id first. (stub)
func (c *Client) FindComments(ctx context.Context, owner, repo string, pr int, marker, author string) ([]int64, error) {
	return nil, errors.New("not implemented")
}

// DeleteComment is DELETE /repos/{o}/{r}/issues/comments/{id}. (stub)
func (c *Client) DeleteComment(ctx context.Context, owner, repo string, id int64) error {
	return errors.New("not implemented")
}
