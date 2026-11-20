package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// REQ-0045
func TestForge_CommitPullsAndReviews(t *testing.T) {
	ctx := context.Background()

	t.Run("CommitPulls asks /commits/{sha}/pulls with per_page=100 and follows Link", func(t *testing.T) {
		r := newRecorder(t)
		r.routes["/repos/o/r/commits/abc123/pulls"] = func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"number":9,"merged_at":"2026-11-19T10:00:00Z"}]`)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/commits/abc123/pulls?per_page=100&page=2>; rel="next"`, r.srv.URL))
			fmt.Fprint(w, `[{"number":5,"merged_at":null,"state":"open"},{"number":7,"merged_at":"2026-11-18T10:00:00Z"}]`)
		}
		got, err := r.client().CommitPulls(ctx, "o", "r", "abc123")
		if err != nil {
			t.Fatal(err)
		}
		want := []Pull{{5, false}, {7, true}, {9, true}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("pulls = %+v, want %+v", got, want)
		}
		if len(r.reqs) != 2 || r.reqs[0].Method != "GET" || r.reqs[0].URL.Query().Get("per_page") != "100" {
			t.Errorf("requests = %v, want two GETs, the first with per_page=100", r.reqs)
		}
	})

	t.Run("PullReviews asks /pulls/{n}/reviews with per_page=100, follows Link and keeps API order", func(t *testing.T) {
		r := newRecorder(t)
		r.routes["/repos/o/r/pulls/12/reviews"] = func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"user":{"login":"carol"},"state":"DISMISSED"}]`)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/pulls/12/reviews?per_page=100&page=2>; rel="next"`, r.srv.URL))
			fmt.Fprint(w, `[{"user":{"login":"Alice"},"state":"APPROVED"},{"user":null,"state":"COMMENTED"}]`)
		}
		got, err := r.client().PullReviews(ctx, "o", "r", 12)
		if err != nil {
			t.Fatal(err)
		}
		want := []Review{{"Alice", "APPROVED"}, {"", "COMMENTED"}, {"carol", "DISMISSED"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("reviews = %+v, want %+v", got, want)
		}
		if len(r.reqs) != 2 || r.reqs[0].URL.Query().Get("per_page") != "100" {
			t.Errorf("requests = %v", r.reqs)
		}
	})

	t.Run("the token is a bearer header, and a server that echoes it gets it scrubbed from the error", func(t *testing.T) {
		r := newRecorder(t)
		echo := func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"message":%q}`, "denied "+req.Header.Get("Authorization"))
		}
		r.routes["/repos/o/r/commits/abc123/pulls"] = echo
		r.routes["/repos/o/r/pulls/12/reviews"] = echo
		c := r.client()
		_, err1 := c.CommitPulls(ctx, "o", "r", "abc123")
		_, err2 := c.PullReviews(ctx, "o", "r", 12)
		for _, err := range []error{err1, err2} {
			if err == nil || !strings.Contains(err.Error(), "403") {
				t.Errorf("err = %v, want one naming status 403", err)
			}
			if err != nil && strings.Contains(err.Error(), secretToken) {
				t.Errorf("the token is in the error: %v", err)
			}
		}
		for _, req := range r.reqs {
			if req.Header.Get("Authorization") != "Bearer "+secretToken {
				t.Errorf("Authorization = %q", req.Header.Get("Authorization"))
			}
		}
	})

	t.Run("a redirect is refused and a link to another host is not followed", func(t *testing.T) {
		r := newRecorder(t)
		r.routes["/repos/o/r/pulls/12/reviews"] = func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Link", `<https://evil.example/steal>; rel="next"`)
			fmt.Fprint(w, `[]`)
		}
		r.routes["/repos/o/r/commits/abc123/pulls"] = func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, "https://evil.example/x", http.StatusFound)
		}
		c := r.client()
		if _, err := c.PullReviews(ctx, "o", "r", 12); err == nil || strings.Contains(err.Error(), secretToken) {
			t.Errorf("PullReviews followed a foreign link, err = %v", err)
		}
		if _, err := c.CommitPulls(ctx, "o", "r", "abc123"); err == nil || strings.Contains(err.Error(), secretToken) {
			t.Errorf("CommitPulls followed a redirect, err = %v", err)
		}
	})

	t.Run("a status error can be told from other errors", func(t *testing.T) {
		r := newRecorder(t)
		r.routes["/repos/o/r/pulls/12/reviews"] = func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		}
		_, err := r.client().PullReviews(ctx, "o", "r", 12)
		var se *StatusError
		if !errors.As(err, &se) || se.Status != 404 {
			t.Errorf("err = %v, want a *StatusError with status 404", err)
		}
	})
}
