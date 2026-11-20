package github

import "context"

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

// CommitPulls lists the pull requests that contain commit sha.
func (c *Client) CommitPulls(ctx context.Context, owner, repo, sha string) ([]Pull, error) {
	return nil, nil
}

// PullReviews lists the reviews of pull request n, oldest first.
func (c *Client) PullReviews(ctx context.Context, owner, repo string, n int) ([]Review, error) {
	return nil, nil
}

// StatusError is a non-2xx answer from the API.
type StatusError struct {
	Status int
	Msg    string
}

func (e *StatusError) Error() string { return e.Msg }
