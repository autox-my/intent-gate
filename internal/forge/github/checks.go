package github

import (
	"context"
	"errors"
)

// CheckRun is the content of one check run: the Checks API fields IntentGate writes.
type CheckRun struct {
	Name       string
	HeadSHA    string // create only
	Conclusion string
	Title      string
	Summary    string
}

var errNotImplemented = errors.New("github: not implemented")

func (c *Client) FindCheckRun(ctx context.Context, owner, repo, sha, name string) (int64, bool, error) {
	return 0, false, errNotImplemented
}

func (c *Client) CreateCheckRun(ctx context.Context, owner, repo string, cr CheckRun) (int64, error) {
	return 0, errNotImplemented
}

func (c *Client) UpdateCheckRun(ctx context.Context, owner, repo string, id int64, cr CheckRun) error {
	return errNotImplemented
}

func (c *Client) FindComment(ctx context.Context, owner, repo string, pr int, marker, author string) (int64, bool, error) {
	return 0, false, errNotImplemented
}

func (c *Client) CreateComment(ctx context.Context, owner, repo string, pr int, body string) (int64, error) {
	return 0, errNotImplemented
}

func (c *Client) UpdateComment(ctx context.Context, owner, repo string, id int64, body string) error {
	return errNotImplemented
}
