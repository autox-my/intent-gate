package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

// All of this runs against a fake API on loopback with a synthetic token (REQ-0035, REQ-0037).

type seen struct {
	method, path string
	query        url.Values
	body         map[string]any
}

// capture serves status and body for path and records what it was sent.
func capture(r *recorder, path string, status int, resp string, out *[]seen) {
	r.routes[path] = func(w http.ResponseWriter, req *http.Request) {
		s := seen{method: req.Method, path: req.URL.Path, query: req.URL.Query()}
		if raw, _ := io.ReadAll(req.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &s.body)
		}
		*out = append(*out, s)
		w.WriteHeader(status)
		fmt.Fprint(w, resp)
	}
}

func TestClient_ChecksAndComments(t *testing.T) {
	ctx := context.Background()

	t.Run("FindCheckRun asks for the latest run of that name on that sha and pages", func(t *testing.T) {
		r := newRecorder(t)
		var got []seen
		r.routes["/repos/o/r/commits/abc123/check-runs"] = func(w http.ResponseWriter, req *http.Request) {
			got = append(got, seen{method: req.Method, path: req.URL.Path, query: req.URL.Query()})
			if req.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `{"total_count":3,"check_runs":[{"id":42,"name":"IntentGate"}]}`)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/commits/abc123/check-runs?check_name=IntentGate&filter=latest&per_page=100&page=2>; rel="next"`, r.srv.URL))
			fmt.Fprint(w, `{"total_count":3,"check_runs":[{"id":1,"name":"Other"},{"id":2,"name":"Another"}]}`)
		}
		id, found, err := r.client().FindCheckRun(ctx, "o", "r", "abc123", "IntentGate")
		if err != nil || !found || id != 42 {
			t.Fatalf("FindCheckRun = %d, %v, %v; want 42, true, nil", id, found, err)
		}
		if len(got) != 2 {
			t.Fatalf("%d requests, want 2 (a page, then its Link next)", len(got))
		}
		q := got[0].query
		if got[0].method != "GET" || q.Get("check_name") != "IntentGate" || q.Get("filter") != "latest" || q.Get("per_page") != "100" {
			t.Errorf("first request = %s ?%s", got[0].method, q.Encode())
		}
	})

	t.Run("FindCheckRun reports not found, and ignores a run of another name", func(t *testing.T) {
		r := newRecorder(t)
		r.routes["/repos/o/r/commits/abc123/check-runs"] = func(w http.ResponseWriter, req *http.Request) {
			fmt.Fprint(w, `{"total_count":1,"check_runs":[{"id":9,"name":"Intentgate-lookalike"}]}`)
		}
		id, found, err := r.client().FindCheckRun(ctx, "o", "r", "abc123", "IntentGate")
		if err != nil || found || id != 0 {
			t.Fatalf("FindCheckRun = %d, %v, %v; want 0, false, nil", id, found, err)
		}
	})

	t.Run("CreateCheckRun posts name, head_sha, completed, conclusion and output", func(t *testing.T) {
		r := newRecorder(t)
		var got []seen
		capture(r, "/repos/o/r/check-runs", 201, `{"id":77}`, &got)
		id, err := r.client().CreateCheckRun(ctx, "o", "r", CheckRun{Name: "IntentGate", HeadSHA: "abc123", Conclusion: "neutral", Title: "T", Summary: "S"})
		if err != nil || id != 77 {
			t.Fatalf("CreateCheckRun = %d, %v; want 77, nil", id, err)
		}
		if len(got) != 1 || got[0].method != "POST" {
			t.Fatalf("requests = %+v, want one POST", got)
		}
		b := got[0].body
		out, _ := b["output"].(map[string]any)
		if b["name"] != "IntentGate" || b["head_sha"] != "abc123" || b["status"] != "completed" || b["conclusion"] != "neutral" ||
			out["title"] != "T" || out["summary"] != "S" {
			t.Errorf("body = %v", b)
		}
	})

	t.Run("UpdateCheckRun patches by id and sends no head_sha", func(t *testing.T) {
		r := newRecorder(t)
		var got []seen
		capture(r, "/repos/o/r/check-runs/77", 200, `{"id":77}`, &got)
		if err := r.client().UpdateCheckRun(ctx, "o", "r", 77, CheckRun{Name: "IntentGate", HeadSHA: "abc123", Conclusion: "success", Title: "T", Summary: "S"}); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].method != "PATCH" {
			t.Fatalf("requests = %+v, want one PATCH", got)
		}
		b := got[0].body
		out, _ := b["output"].(map[string]any)
		if _, has := b["head_sha"]; has || b["name"] != "IntentGate" || b["status"] != "completed" || b["conclusion"] != "success" || out["summary"] != "S" {
			t.Errorf("body = %v", b)
		}
	})

	t.Run("a long summary is cut to 65535 bytes on a UTF-8 boundary with a final truncation line", func(t *testing.T) {
		const limit = 65535
		const tail = "\n… (truncated)"
		cases := map[string]string{
			"two-byte runes (the cut falls inside one)": strings.Repeat("é", 40000),
			"three-byte runes":                          strings.Repeat("€", 30000),
			"ascii":                                     strings.Repeat("a", limit+1),
		}
		for name, summary := range cases {
			r := newRecorder(t)
			var got []seen
			capture(r, "/repos/o/r/check-runs", 201, `{"id":1}`, &got)
			if _, err := r.client().CreateCheckRun(ctx, "o", "r", CheckRun{Name: "IntentGate", HeadSHA: "s", Conclusion: "neutral", Title: "T", Summary: summary}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			sent, _ := got[0].body["output"].(map[string]any)["summary"].(string)
			if len(sent) > limit || !utf8.ValidString(sent) || !strings.HasSuffix(sent, tail) {
				t.Errorf("%s: sent %d bytes, valid UTF-8 %v, suffix %v", name, len(sent), utf8.ValidString(sent), strings.HasSuffix(sent, tail))
			}
			if len(sent) < limit-3-len(tail) {
				t.Errorf("%s: cut too much: %d bytes", name, len(sent))
			}
		}
		r := newRecorder(t)
		var got []seen
		capture(r, "/repos/o/r/check-runs", 201, `{"id":1}`, &got)
		exact := strings.Repeat("a", limit)
		if _, err := r.client().CreateCheckRun(ctx, "o", "r", CheckRun{Name: "IntentGate", HeadSHA: "s", Conclusion: "neutral", Title: "T", Summary: exact}); err != nil {
			t.Fatal(err)
		}
		if sent, _ := got[0].body["output"].(map[string]any)["summary"].(string); sent != exact {
			t.Errorf("a summary of exactly %d bytes was changed (%d bytes sent)", limit, len(sent))
		}
	})

	const marker = "<!-- intentgate:pr-comment -->"

	t.Run("FindComment matches the marker at the start and the author, case-insensitively, across pages", func(t *testing.T) {
		r := newRecorder(t)
		var queries []url.Values
		r.routes["/repos/o/r/issues/5/comments"] = func(w http.ResponseWriter, req *http.Request) {
			queries = append(queries, req.URL.Query())
			next := func(p int) {
				w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/issues/5/comments?per_page=100&page=%d>; rel="next"`, r.srv.URL, p))
			}
			switch req.URL.Query().Get("page") {
			case "":
				next(2)
				fmt.Fprintf(w, `[{"id":1,"body":%q,"user":{"login":"mallory"}},{"id":2,"body":%q,"user":{"login":"github-actions[bot]"}}]`,
					marker+"\nforged", "see "+marker)
			case "2":
				next(3)
				fmt.Fprint(w, `[{"id":3,"body":null,"user":null}]`)
			default:
				fmt.Fprintf(w, `[{"id":4,"body":%q,"user":{"login":"GitHub-Actions[bot]"}}]`, marker+"\nreal")
			}
		}
		id, found, err := r.client().FindComment(ctx, "o", "r", 5, marker, "github-actions[bot]")
		if err != nil || !found || id != 4 {
			t.Fatalf("FindComment = %d, %v, %v; want 4, true, nil", id, found, err)
		}
		if len(queries) != 3 || queries[0].Get("per_page") != "100" {
			t.Errorf("queries = %v, want three pages starting with per_page=100", queries)
		}
	})

	t.Run("FindComment finds nothing when only others' comments carry the marker", func(t *testing.T) {
		r := newRecorder(t)
		r.routes["/repos/o/r/issues/5/comments"] = func(w http.ResponseWriter, req *http.Request) {
			fmt.Fprintf(w, `[{"id":1,"body":%q,"user":{"login":"mallory"}}]`, marker+"\nforged")
		}
		id, found, err := r.client().FindComment(ctx, "o", "r", 5, marker, "github-actions[bot]")
		if err != nil || found || id != 0 {
			t.Fatalf("FindComment = %d, %v, %v; want 0, false, nil", id, found, err)
		}
	})

	t.Run("CreateComment and UpdateComment send the body", func(t *testing.T) {
		r := newRecorder(t)
		var got []seen
		capture(r, "/repos/o/r/issues/5/comments", 201, `{"id":11}`, &got)
		capture(r, "/repos/o/r/issues/comments/11", 200, `{"id":11}`, &got)
		c := r.client()
		id, err := c.CreateComment(ctx, "o", "r", 5, "hello")
		if err != nil || id != 11 {
			t.Fatalf("CreateComment = %d, %v; want 11, nil", id, err)
		}
		if err := c.UpdateComment(ctx, "o", "r", 11, "again"); err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].method != "POST" || got[0].body["body"] != "hello" ||
			got[1].method != "PATCH" || got[1].body["body"] != "again" {
			t.Errorf("requests = %+v", got)
		}
	})

	t.Run("a write that fails names the status and GitHub's message, never the token", func(t *testing.T) {
		r := newRecorder(t)
		r.routes["/repos/o/r/check-runs"] = func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"message":"Resource not accessible by integration `+req.Header.Get("Authorization")+`"}`)
		}
		_, err := r.client().CreateCheckRun(ctx, "o", "r", CheckRun{Name: "IntentGate", HeadSHA: "s", Conclusion: "neutral", Title: "T", Summary: "S"})
		if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "Resource not accessible") || strings.Contains(err.Error(), secretToken) {
			t.Fatalf("err = %v, want 403 and the message without the token", err)
		}
	})

	t.Run("a write is never redirected", func(t *testing.T) {
		other := newRecorder(t)
		other.routes["/stolen"] = func(w http.ResponseWriter, req *http.Request) { fmt.Fprint(w, `{"id":1}`) }
		r := newRecorder(t)
		r.routes["/repos/o/r/issues/5/comments"] = func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, other.srv.URL+"/stolen", http.StatusTemporaryRedirect)
		}
		if _, err := r.client().CreateComment(ctx, "o", "r", 5, "x"); err == nil || strings.Contains(err.Error(), secretToken) {
			t.Fatalf("err = %v, want a redirect refusal without the token", err)
		}
		if len(other.reqs) != 0 {
			t.Errorf("the redirect target was contacted %d times", len(other.reqs))
		}
	})

	t.Run("a plain-http non-loopback API URL is refused for writes too", func(t *testing.T) {
		c := &Client{BaseURL: "http://example.com", Token: secretToken}
		if err := c.UpdateComment(ctx, "o", "r", 1, "x"); err == nil || strings.Contains(err.Error(), secretToken) {
			t.Fatalf("err = %v, want a refusal", err)
		}
	})
}
