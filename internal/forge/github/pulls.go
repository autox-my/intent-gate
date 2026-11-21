package github

import (
	"context"
	"fmt"
	"net/url"
)

// Pull is a pull request that contains a commit.
type Pull struct {
	Number int
	Merged bool
}

// Review is one review of a pull request.
type Review struct {
	Login string // "" for a deleted account
	State string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED or PENDING
}

// CommitPulls lists the pull requests that contain commit sha, in API order:
// GET /repos/{o}/{r}/commits/{sha}/pulls?per_page=100, following Link: rel="next". A pull request
// is merged when it has a merged_at.
func (c *Client) CommitPulls(ctx context.Context, owner, repo, sha string) ([]Pull, error) {
	var all []Pull
	next := fmt.Sprintf("/repos/%s/%s/commits/%s/pulls?per_page=100", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(sha))
	for pages := 0; next != ""; pages++ {
		if pages >= maxPages {
			return nil, fmt.Errorf("github: more than %d pages of pull requests", maxPages)
		}
		var raw []struct {
			Number   int     `json:"number"`
			MergedAt *string `json:"merged_at"`
		}
		h, err := c.get(ctx, next, &raw)
		if err != nil {
			return nil, err
		}
		for _, r := range raw {
			all = append(all, Pull{Number: r.Number, Merged: r.MergedAt != nil})
		}
		next = nextLink(h.Get("Link"))
	}
	return all, nil
}

// PullReviews lists the reviews of pull request n, oldest first (the API's order):
// GET /repos/{o}/{r}/pulls/{n}/reviews?per_page=100, following Link: rel="next".
func (c *Client) PullReviews(ctx context.Context, owner, repo string, n int) ([]Review, error) {
	var all []Review
	next := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=100", url.PathEscape(owner), url.PathEscape(repo), n)
	for pages := 0; next != ""; pages++ {
		if pages >= maxPages {
			return nil, fmt.Errorf("github: more than %d pages of reviews", maxPages)
		}
		var raw []struct {
			User  *user  `json:"user"`
			State string `json:"state"`
		}
		h, err := c.get(ctx, next, &raw)
		if err != nil {
			return nil, err
		}
		for _, r := range raw {
			rv := Review{State: r.State}
			if r.User != nil {
				rv.Login = r.User.Login
			}
			all = append(all, rv)
		}
		next = nextLink(h.Get("Link"))
	}
	return all, nil
}
