// Package github is the GitHub adapter: a small client on net/http and encoding/json for the three
// GETs `intentgate lock --issue` needs and the check run and comment writes `intentgate publish`
// makes (checks.go).
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	apiVersion = "2022-11-28"
	userAgent  = "intentgate"
	timeout    = 30 * time.Second
	maxBody    = 16 << 20 // a bound on what one response may hold
	maxPages   = 1000     // a bound on Link: rel="next" chains, so a bad server can't loop us
)

// Client talks to the GitHub REST API at BaseURL with Token. BaseURL must be https (http only for a
// loopback host) and no redirect is followed. A nil HTTP means a client with a 30 s request
// timeout. The token is only ever sent as the Authorization header of a request to BaseURL's host,
// and never appears in an error.
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

type user struct {
	Login string `json:"login"`
}

// Issue gets GET /repos/{owner}/{repo}/issues/{n}.
func (c *Client) Issue(ctx context.Context, owner, repo string, n int) (Issue, error) {
	var raw struct {
		Number      int              `json:"number"`
		Title       string           `json:"title"`
		Body        *string          `json:"body"`
		User        user             `json:"user"`
		PullRequest *json.RawMessage `json:"pull_request"`
	}
	if _, err := c.get(ctx, c.repoPath(owner, repo, n, ""), &raw); err != nil {
		return Issue{}, err
	}
	is := Issue{Number: raw.Number, Title: raw.Title, Author: raw.User.Login, IsPullRequest: raw.PullRequest != nil}
	if raw.Body != nil {
		is.Body = *raw.Body
	}
	return is, nil
}

// Comments gets GET /repos/{owner}/{repo}/issues/{n}/comments?per_page=100, following Link:
// rel="next" until done, and returns the comments in API order.
func (c *Client) Comments(ctx context.Context, owner, repo string, n int) ([]Comment, error) {
	var all []Comment
	next := c.repoPath(owner, repo, n, "/comments") + "?per_page=100"
	for pages := 0; next != ""; pages++ {
		if pages >= maxPages {
			return nil, fmt.Errorf("github: more than %d pages of comments", maxPages)
		}
		var raw []struct {
			Body *string `json:"body"`
			User user    `json:"user"`
		}
		h, err := c.get(ctx, next, &raw)
		if err != nil {
			return nil, err
		}
		for _, r := range raw {
			cm := Comment{Author: r.User.Login}
			if r.Body != nil {
				cm.Body = *r.Body
			}
			all = append(all, cm)
		}
		next = nextLink(h.Get("Link"))
	}
	return all, nil
}

// Viewer gets the login of the token's user: GET /user.
func (c *Client) Viewer(ctx context.Context) (string, error) {
	var u user
	if _, err := c.get(ctx, "/user", &u); err != nil {
		return "", err
	}
	if u.Login == "" {
		return "", fmt.Errorf("github: GET /user: no login in the response")
	}
	return u.Login, nil
}

func (c *Client) repoPath(owner, repo string, n int, suffix string) string {
	return fmt.Sprintf("/repos/%s/%s/issues/%d%s", url.PathEscape(owner), url.PathEscape(repo), n, suffix)
}

// apiBase parses BaseURL and refuses any that would send the token in the clear: it must be https,
// except a loopback host (127.0.0.1, ::1, localhost) for tests, and carry no credentials.
func apiBase(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil {
		return nil, fmt.Errorf("github: bad API URL %q", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return nil, fmt.Errorf("github: refusing API URL %q: the token only travels over https (http is allowed only for 127.0.0.1, ::1 and localhost)", raw)
		}
	default:
		return nil, fmt.Errorf("github: bad API URL %q: want an https URL", raw)
	}
	return u, nil
}

func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// noRedirect is CheckRedirect: a redirect is an error naming its status and target host, never the
// token. The Authorization header is never re-sent to wherever a server points us.
func noRedirect(req *http.Request, via []*http.Request) error {
	status := 0
	if len(via) > 0 && req.Response != nil {
		status = req.Response.StatusCode
	}
	return fmt.Errorf("refusing to follow a %d redirect to host %q", status, req.URL.Host)
}

// get fetches target (a path with an optional query, or an absolute URL from a Link header) and
// decodes the JSON body into out. A non-2xx is an error with the status and GitHub's message.
func (c *Client) get(ctx context.Context, target string, out any) (http.Header, error) {
	return c.do(ctx, http.MethodGet, target, nil, out)
}

// send writes with method POST or PATCH: body is marshalled as JSON and the response decoded into out.
func (c *Client) send(ctx context.Context, method, target string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", method, target, err)
	}
	_, err = c.do(ctx, method, target, raw, out)
	return err
}

// do is one request with the safety rules of this package: an https base (or loopback http), no
// redirect, a bounded body, and the token only in the Authorization header and never in an error.
func (c *Client) do(ctx context.Context, method, target string, body []byte, out any) (http.Header, error) {
	base, err := apiBase(c.BaseURL)
	if err != nil {
		return nil, err
	}
	full := base.String() + target
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		u, err := url.Parse(target)
		if err != nil || u.User != nil || u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) {
			return nil, fmt.Errorf("github: refusing to follow a link that is not on %s://%s: %q", base.Scheme, base.Host, target)
		}
		full = target
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, rd)
	if err != nil {
		return nil, fmt.Errorf("github: %s %s: %w", method, target, scrub(err, c.Token))
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	hc := &http.Client{Timeout: timeout}
	if c.HTTP != nil {
		cp := *c.HTTP
		hc = &cp
	}
	hc.CheckRedirect = noRedirect
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %s %s: %w", method, target, scrub(err, c.Token))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("github: %s %s: %w", method, target, scrub(err, c.Token))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &m)
		msg := m.Message
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("github: %s %s: %d %s", method, target, resp.StatusCode, scrubString(msg, c.Token))
	}
	// An empty 2xx is success for PATCH and DELETE, which need nothing from the answer; POST still
	// needs the created id, and GET the data, so an empty body stays a decode error for them.
	if len(bytes.TrimSpace(data)) == 0 && (method == http.MethodPatch || method == http.MethodDelete) {
		return resp.Header, nil
	}
	if out == nil {
		return resp.Header, nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, fmt.Errorf("github: %s %s: bad JSON: %w", method, target, err)
	}
	return resp.Header, nil
}

// scrub returns an error whose text lacks the token, so no path can print it.
func scrub(err error, token string) error {
	if token == "" || !strings.Contains(err.Error(), token) {
		return err
	}
	return fmt.Errorf("%s", scrubString(err.Error(), token))
}

func scrubString(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[REDACTED]")
}

// nextLink returns the target of the first link in a Link header (RFC 8288) whose rel names the
// relation type next, or "". rel may be a token or a quoted string and may hold several
// space-separated relation types; only a link's first rel parameter counts (§3.3). A header that
// can't be parsed from some point on yields the links before that point.
func nextLink(header string) string {
	s := header
	for {
		s = strings.TrimLeft(s, " \t,")
		if !strings.HasPrefix(s, "<") {
			return ""
		}
		end := strings.IndexByte(s, '>')
		if end < 0 {
			return ""
		}
		target := strings.TrimSpace(s[1:end])
		s = s[end+1:]
		var rel string
		var haveRel bool
		for {
			s = strings.TrimLeft(s, " \t")
			if !strings.HasPrefix(s, ";") {
				break
			}
			var name, value string
			var ok bool
			name, value, s, ok = linkParam(s[1:])
			if !ok {
				return ""
			}
			if strings.EqualFold(name, "rel") && !haveRel {
				rel, haveRel = value, true
			}
		}
		for _, r := range strings.Fields(rel) {
			if strings.EqualFold(r, "next") && target != "" {
				return target
			}
		}
	}
}

// linkParam reads one `name[=value]` link parameter from the front of s (after its `;`): a token
// name, then optionally `=` and a token or a quoted string with backslash escapes. It returns the
// rest of s.
func linkParam(s string) (name, value, rest string, ok bool) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, "=;, \t")
	if i <= 0 {
		return "", "", s, false
	}
	name, s = s[:i], strings.TrimLeft(s[i:], " \t")
	if !strings.HasPrefix(s, "=") {
		return name, "", s, true
	}
	s = strings.TrimLeft(s[1:], " \t")
	if !strings.HasPrefix(s, `"`) {
		j := strings.IndexAny(s, ";, \t")
		if j < 0 {
			j = len(s)
		}
		return name, s[:j], s[j:], true
	}
	var b strings.Builder
	for j := 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			if j++; j < len(s) {
				b.WriteByte(s[j])
			}
		case '"':
			return name, b.String(), s[j+1:], true
		default:
			b.WriteByte(s[j])
		}
	}
	return "", "", "", false // an unterminated quoted string
}
