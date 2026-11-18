// Package github is the first piece of the GitHub adapter: a small client on net/http and
// encoding/json for the three GETs `intentgate lock --issue` needs.
package github

import (
	"context"
	"errors"
	"net/http"
)

// Client talks to the GitHub REST API at BaseURL with Token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Issue is an issue, or a pull request seen through the issues API.
type Issue struct {
	Number        int
	Title, Body   string
	Author        string
	IsPullRequest bool
}

// Comment is one issue comment.
type Comment struct{ Author, Body string }

var errNotImplemented = errors.New("github: not implemented")

// Issue gets GET /repos/{owner}/{repo}/issues/{n}.
func (c *Client) Issue(ctx context.Context, owner, repo string, n int) (Issue, error) {
	return Issue{}, errNotImplemented
}

// Comments gets every comment of the issue, in API order.
func (c *Client) Comments(ctx context.Context, owner, repo string, n int) ([]Comment, error) {
	return nil, errNotImplemented
}

// Viewer gets the login of the token's user: GET /user.
func (c *Client) Viewer(ctx context.Context) (string, error) {
	return "", errNotImplemented
}
