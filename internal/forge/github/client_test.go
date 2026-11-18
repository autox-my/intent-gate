package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const secretToken = "ghp_NOT_A_REAL_TOKEN_for_tests_only"

// recorder is a fake GitHub: it records every request and serves routes by path.
type recorder struct {
	mu     sync.Mutex
	reqs   []*http.Request
	routes map[string]http.HandlerFunc
	srv    *httptest.Server
}

func newRecorder(t *testing.T) *recorder {
	r := &recorder{routes: map[string]http.HandlerFunc{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.reqs = append(r.reqs, req)
		r.mu.Unlock()
		h, ok := r.routes[req.URL.Path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		h(w, req)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) client() *Client { return &Client{BaseURL: r.srv.URL, Token: secretToken} }

func TestClient(t *testing.T) {
	ctx := context.Background()

	t.Run("headers on every request", func(t *testing.T) {
		r := newRecorder(t)
		r.routes["/repos/o/r/issues/7"] = func(w http.ResponseWriter, req *http.Request) { fmt.Fprint(w, `{"number":7}`) }
		r.routes["/repos/o/r/issues/7/comments"] = func(w http.ResponseWriter, req *http.Request) { fmt.Fprint(w, `[]`) }
		r.routes["/user"] = func(w http.ResponseWriter, req *http.Request) { fmt.Fprint(w, `{"login":"x"}`) }
		c := r.client()
		if _, err := c.Issue(ctx, "o", "r", 7); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Comments(ctx, "o", "r", 7); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Viewer(ctx); err != nil {
			t.Fatal(err)
		}
		if len(r.reqs) != 3 {
			t.Fatalf("%d requests, want 3", len(r.reqs))
		}
		for _, req := range r.reqs {
			want := map[string]string{
				"Authorization":        "Bearer " + secretToken,
				"Accept":               "application/vnd.github+json",
				"X-GitHub-Api-Version": "2022-11-28",
				"User-Agent":           "intentgate",
			}
			for k, v := range want {
				if got := req.Header.Get(k); got != v {
					t.Errorf("%s %s: %s = %q, want %q", req.Method, req.URL.Path, k, got, v)
				}
			}
			if req.Method != http.MethodGet {
				t.Errorf("method %s, want GET", req.Method)
			}
		}
	})

	t.Run("issue decode", func(t *testing.T) {
		r := newRecorder(t)
		r.routes["/repos/o/r/issues/7"] = func(w http.ResponseWriter, req *http.Request) {
			fmt.Fprint(w, `{"number":7,"title":"Add SARIF","body":"Please.\n","user":{"login":"alice"}}`)
		}
		r.routes["/repos/o/r/issues/8"] = func(w http.ResponseWriter, req *http.Request) {
			fmt.Fprint(w, `{"number":8,"title":"A PR","body":null,"user":{"login":"bob"},"pull_request":{"url":"https://x"}}`)
		}
		got, err := r.client().Issue(ctx, "o", "r", 7)
		if want := (Issue{Number: 7, Title: "Add SARIF", Body: "Please.\n", Author: "alice"}); err != nil || got != want {
			t.Errorf("issue = %+v, %v; want %+v", got, err, want)
		}
		got, err = r.client().Issue(ctx, "o", "r", 8)
		if want := (Issue{Number: 8, Title: "A PR", Author: "bob", IsPullRequest: true}); err != nil || got != want {
			t.Errorf("pull request = %+v, %v; want %+v", got, err, want)
		}
	})

	t.Run("comments follow Link rel=next over 3 pages, in API order", func(t *testing.T) {
		r := newRecorder(t)
		page := func(next string, comments ...string) http.HandlerFunc {
			return func(w http.ResponseWriter, req *http.Request) {
				if next != "" {
					w.Header().Set("Link", fmt.Sprintf(`<%s%s>; rel="next", <%s/last>; rel="last"`, r.srv.URL, next, r.srv.URL))
				}
				items := make([]string, len(comments))
				for i, c := range comments {
					items[i] = fmt.Sprintf(`{"body":%q,"user":{"login":"u%d"}}`, c, i)
				}
				fmt.Fprint(w, "["+strings.Join(items, ",")+"]")
			}
		}
		r.routes["/repos/o/r/issues/7/comments"] = func(w http.ResponseWriter, req *http.Request) {
			switch req.URL.Query().Get("page") {
			case "":
				if req.URL.Query().Get("per_page") != "100" {
					t.Errorf("first page query %q, want per_page=100", req.URL.RawQuery)
				}
				page("/repos/o/r/issues/7/comments?per_page=100&page=2", "one", "two")(w, req)
			case "2":
				page("/repos/o/r/issues/7/comments?per_page=100&page=3", "three")(w, req)
			default:
				page("", "four")(w, req)
			}
		}
		got, err := r.client().Comments(ctx, "o", "r", 7)
		want := []Comment{{"u0", "one"}, {"u1", "two"}, {"u0", "three"}, {"u0", "four"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("comments = %+v, %v; want %+v", got, err, want)
		}
		if len(r.reqs) != 3 {
			t.Errorf("%d requests, want 3", len(r.reqs))
		}
	})

	t.Run("viewer", func(t *testing.T) {
		r := newRecorder(t)
		r.routes["/user"] = func(w http.ResponseWriter, req *http.Request) { fmt.Fprint(w, `{"login":"carol"}`) }
		if got, err := r.client().Viewer(ctx); err != nil || got != "carol" {
			t.Errorf("viewer = %q, %v; want carol", got, err)
		}
	})

	t.Run("a non-2xx is an error with the status and GitHub's message, never the token", func(t *testing.T) {
		r := newRecorder(t)
		r.routes["/repos/o/r/issues/404"] = func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		}
		r.routes["/repos/o/r/issues/500"] = func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"Server Error Today"}`)
		}
		r.routes["/user"] = func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"Bad credentials"}`)
		}
		c := r.client()
		_, e404 := c.Issue(ctx, "o", "r", 404)
		_, e500 := c.Issue(ctx, "o", "r", 500)
		_, e500c := c.Comments(ctx, "o", "r", 500)
		_, e401 := c.Viewer(ctx)
		for _, tc := range []struct {
			err         error
			status, msg string
		}{{e404, "404", "Not Found"}, {e500, "500", "Server Error Today"}, {e500c, "404", "Not Found"}, {e401, "401", "Bad credentials"}} {
			if tc.err == nil || !strings.Contains(tc.err.Error(), tc.status) || !strings.Contains(tc.err.Error(), tc.msg) {
				t.Errorf("error %v, want it to carry %q and %q", tc.err, tc.status, tc.msg)
			}
			if tc.err != nil && strings.Contains(tc.err.Error(), secretToken) {
				t.Errorf("the error carries the token: %v", tc.err)
			}
		}
	})

	t.Run("a network error does not carry the token", func(t *testing.T) {
		r := newRecorder(t)
		c := r.client()
		r.srv.Close()
		_, err := c.Issue(ctx, "o", "r", 1)
		if err == nil || strings.Contains(err.Error(), secretToken) {
			t.Errorf("error %v: want an error without the token", err)
		}
	})
}
