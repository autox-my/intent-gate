package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"intentgate/internal/report"
)

// Nothing here reaches the real GitHub API or reads a real token: every test points GITHUB_API_URL
// at a fake on loopback and uses a synthetic token.

const (
	pubToken = "ghp_FAKE_TOKEN_publish_tests_0123456789"
	pubSHA   = "0123456789abcdef0123456789abcdef01234567"
	pubBot   = "github-actions[bot]"
)

type fakeRun struct {
	ID                                    int64
	Name, HeadSHA, Conclusion, Title, Sum string
}

type fakeComment struct {
	ID          int64
	Body, Login string
}

type call struct{ Method, Path string }

// pubAPI is a fake GitHub for repository octo/repo: stateful check runs and comments of PR 7.
type pubAPI struct {
	srv      *httptest.Server
	mu       sync.Mutex
	calls    []call
	runs     []fakeRun
	comments []fakeComment
	nextID   int64
	pageSize int    // comments per page; 0 means all on one page
	status   int    // when not 0, every call answers this status
	message  string // the error message served with status

	postAs      string // the login a created comment gets: the token's owner
	race        bool   // the next comment POST is preceded by a racing run's own comment
	emptyWrites bool   // PATCH answers 200 with no body
}

func newPubAPI(t *testing.T) *pubAPI {
	t.Helper()
	f := &pubAPI{nextID: 100, pageSize: 2, postAs: pubBot}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	t.Setenv("GITHUB_API_URL", f.srv.URL)
	t.Setenv("GITHUB_TOKEN", pubToken)
	return f
}

func (f *pubAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{r.Method, r.URL.Path})
	if f.status != 0 {
		w.WriteHeader(f.status)
		msg := f.message
		if msg == "" {
			msg = "boom"
		}
		// A hostile server echoing the credentials must not get them printed.
		fmt.Fprintf(w, `{"message":%q}`, msg+" "+r.Header.Get("Authorization"))
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Name       string `json:"name"`
		HeadSHA    string `json:"head_sha"`
		Conclusion string `json:"conclusion"`
		Output     struct{ Title, Summary string }
		Body       string `json:"body"`
	}
	_ = json.Unmarshal(raw, &body)
	const pre = "/repos/octo/repo"
	p := r.URL.Path
	switch {
	case r.Method == "GET" && p == pre+"/commits/"+pubSHA+"/check-runs":
		var hits []string
		for _, c := range f.runs {
			if c.HeadSHA == pubSHA && c.Name == r.URL.Query().Get("check_name") {
				hits = append(hits, fmt.Sprintf(`{"id":%d,"name":%q}`, c.ID, c.Name))
			}
		}
		fmt.Fprintf(w, `{"total_count":%d,"check_runs":[%s]}`, len(hits), strings.Join(hits, ","))
	case r.Method == "POST" && p == pre+"/check-runs":
		f.nextID++
		f.runs = append(f.runs, fakeRun{f.nextID, body.Name, body.HeadSHA, body.Conclusion, body.Output.Title, body.Output.Summary})
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"id":%d}`, f.nextID)
	case r.Method == "PATCH" && strings.HasPrefix(p, pre+"/check-runs/"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(p, pre+"/check-runs/"), 10, 64)
		for i := range f.runs {
			if f.runs[i].ID == id {
				f.runs[i].Conclusion, f.runs[i].Title, f.runs[i].Sum = body.Conclusion, body.Output.Title, body.Output.Summary
				if !f.emptyWrites {
					fmt.Fprintf(w, `{"id":%d}`, id)
				}
				return
			}
		}
		http.NotFound(w, r)
	case r.Method == "GET" && p == pre+"/issues/7/comments":
		size := f.pageSize
		if size <= 0 {
			size = len(f.comments) + 1
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		lo, hi := (page-1)*size, page*size
		if lo > len(f.comments) {
			lo = len(f.comments)
		}
		if hi > len(f.comments) {
			hi = len(f.comments)
		}
		if hi < len(f.comments) {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?per_page=100&page=%d>; rel="next"`, f.srv.URL, p, page+1))
		}
		var items []string
		for _, c := range f.comments[lo:hi] {
			items = append(items, fmt.Sprintf(`{"id":%d,"body":%q,"user":{"login":%q}}`, c.ID, c.Body, c.Login))
		}
		fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
	case r.Method == "POST" && p == pre+"/issues/7/comments":
		if f.race { // another run created its comment between our listing and our POST
			f.race = false
			f.nextID++
			f.comments = append(f.comments, fakeComment{f.nextID, report.CommentMarker + "\nthe racing run's comment", f.postAs})
		}
		f.nextID++
		f.comments = append(f.comments, fakeComment{f.nextID, body.Body, f.postAs})
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"id":%d}`, f.nextID)
	case r.Method == "PATCH" && strings.HasPrefix(p, pre+"/issues/comments/"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(p, pre+"/issues/comments/"), 10, 64)
		for i := range f.comments {
			if f.comments[i].ID == id {
				f.comments[i].Body = body.Body
				if !f.emptyWrites {
					fmt.Fprintf(w, `{"id":%d}`, id)
				}
				return
			}
		}
		http.NotFound(w, r)
	case r.Method == "DELETE" && strings.HasPrefix(p, pre+"/issues/comments/"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(p, pre+"/issues/comments/"), 10, 64)
		for i := range f.comments {
			if f.comments[i].ID == id {
				f.comments = append(f.comments[:i], f.comments[i+1:]...)
				w.WriteHeader(204)
				return
			}
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *pubAPI) called() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

func (f *pubAPI) count(method string) (n int) {
	for _, c := range f.called() {
		if c.Method == method {
			n++
		}
	}
	return n
}

func (f *pubAPI) botComments() (n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.comments {
		if c.Login == f.postAs && strings.HasPrefix(c.Body, report.CommentMarker) {
			n++
		}
	}
	return n
}

func (f *pubAPI) addComment(login, body string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.comments = append(f.comments, fakeComment{f.nextID, body, login})
	return f.nextID
}

func (f *pubAPI) hasComment(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.comments {
		if c.ID == id {
			return true
		}
	}
	return false
}

func (f *pubAPI) commentBody(id int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.comments {
		if c.ID == id {
			return c.Body
		}
	}
	return ""
}

// outDir writes what `verify --out-dir` would: decision.json, summary.md and comment.md.
func outDir(t *testing.T, publish bool, conclusion, comment, title string) string {
	t.Helper()
	dir := t.TempDir()
	dec, _ := json.MarshalIndent(map[string]any{"publish": publish, "conclusion": conclusion, "comment": comment, "title": title}, "", "  ")
	for name, data := range map[string]string{
		"decision.json": string(dec) + "\n",
		"summary.md":    "## Summary\nfor " + title + "\n",
		"comment.md":    report.CommentMarker + "\nThe comment for " + title + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func publishRun(t *testing.T, dir string, extra ...string) (code int, stdout, stderr string) {
	t.Helper()
	args := append([]string{"publish", "--out-dir", dir, "--repo", "octo/repo", "--pr", "7", "--head-sha", pubSHA}, extra...)
	var out, errb bytes.Buffer
	code = Run(args, strings.NewReader(""), &out, Deps{Stderr: &errb, Dir: t.TempDir()})
	return code, out.String(), errb.String()
}

func mustPublish(t *testing.T, dir string, extra ...string) {
	t.Helper()
	if code, out, errs := publishRun(t, dir, extra...); code != 0 {
		t.Fatalf("publish exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
}

func TestPublish_OneCheckRun(t *testing.T) {
	t.Run("none yet: exactly one POST and no PATCH", func(t *testing.T) {
		api := newPubAPI(t)
		mustPublish(t, outDir(t, true, "neutral", "none", "Findings"))
		if api.count("POST") != 1 || api.count("PATCH") != 0 {
			t.Fatalf("calls = %v, want one POST and no PATCH", api.called())
		}
		if len(api.runs) != 1 || api.runs[0].Name != "IntentGate" || api.runs[0].HeadSHA != pubSHA ||
			api.runs[0].Conclusion != "neutral" || api.runs[0].Title != "Findings" || !strings.Contains(api.runs[0].Sum, "## Summary") {
			t.Errorf("check run = %+v", api.runs)
		}
	})

	t.Run("one for the SHA already: exactly one PATCH and no POST", func(t *testing.T) {
		api := newPubAPI(t)
		api.runs = []fakeRun{{ID: 5, Name: "IntentGate", HeadSHA: pubSHA, Conclusion: "neutral", Title: "old"}}
		mustPublish(t, outDir(t, true, "success", "none", "Clean"))
		if api.count("POST") != 0 || api.count("PATCH") != 1 {
			t.Fatalf("calls = %v, want one PATCH and no POST", api.called())
		}
		if len(api.runs) != 1 || api.runs[0].Conclusion != "success" || api.runs[0].Title != "Clean" {
			t.Errorf("check run = %+v", api.runs)
		}
	})

	t.Run("a check run of another name on the same SHA is left alone", func(t *testing.T) {
		api := newPubAPI(t)
		api.runs = []fakeRun{{ID: 5, Name: "build", HeadSHA: pubSHA}}
		mustPublish(t, outDir(t, true, "neutral", "none", "T"))
		if api.count("POST") != 1 || len(api.runs) != 2 || api.runs[0].Conclusion != "" {
			t.Errorf("calls = %v, runs = %+v", api.called(), api.runs)
		}
	})

	t.Run("two consecutive runs leave one check run", func(t *testing.T) {
		api := newPubAPI(t)
		dir := outDir(t, true, "neutral", "none", "Findings")
		mustPublish(t, dir)
		mustPublish(t, dir)
		if len(api.runs) != 1 || api.count("POST") != 1 || api.count("PATCH") != 1 {
			t.Fatalf("runs = %+v, calls = %v; want one run, one POST then one PATCH", api.runs, api.called())
		}
	})
}

func TestPublish_OneComment(t *testing.T) {
	marked := func(s string) string { return report.CommentMarker + "\n" + s }
	const path = "/repos/octo/repo/issues/7/comments"

	t.Run("upsert with none: one POST of comment.md as written", func(t *testing.T) {
		api := newPubAPI(t)
		dir := outDir(t, true, "neutral", "upsert", "Findings")
		mustPublish(t, dir)
		want, _ := os.ReadFile(filepath.Join(dir, "comment.md"))
		if api.botComments() != 1 || api.comments[0].Body != string(want) {
			t.Fatalf("comments = %+v, want one with comment.md as the body", api.comments)
		}
		var posts int
		for _, c := range api.called() {
			if c.Method == "POST" && c.Path == path {
				posts++
			}
		}
		if posts != 1 {
			t.Errorf("%d comment POSTs, want 1", posts)
		}
	})

	t.Run("upsert with the bot's marker comment: that id is patched", func(t *testing.T) {
		api := newPubAPI(t)
		api.addComment("alice", "unrelated")
		id := api.addComment(pubBot, marked("old"))
		mustPublish(t, outDir(t, true, "neutral", "upsert", "Findings"))
		if got := api.commentBody(id); !strings.Contains(got, "The comment for Findings") {
			t.Errorf("comment %d = %q, want it edited in place", id, got)
		}
		if len(api.comments) != 2 {
			t.Errorf("%d comments, want 2 (none created)", len(api.comments))
		}
		patched := false
		for _, c := range api.called() {
			if c.Method == "PATCH" && c.Path == fmt.Sprintf("/repos/octo/repo/issues/comments/%d", id) {
				patched = true
			}
		}
		if !patched {
			t.Errorf("calls = %v, want a PATCH of comment %d", api.called(), id)
		}
	})

	t.Run("a marker comment by another user is ignored: a new one is created", func(t *testing.T) {
		api := newPubAPI(t)
		other := api.addComment("mallory", marked("forged"))
		mustPublish(t, outDir(t, true, "neutral", "upsert", "Findings"))
		if api.commentBody(other) != marked("forged") {
			t.Errorf("another user's comment was edited: %q", api.commentBody(other))
		}
		if api.botComments() != 1 || len(api.comments) != 2 {
			t.Errorf("comments = %+v, want the forged one plus one by the bot", api.comments)
		}
	})

	t.Run("--bot-login chooses whose comment counts", func(t *testing.T) {
		api := newPubAPI(t)
		id := api.addComment("IntentGate-Bot", marked("old"))
		mustPublish(t, outDir(t, true, "neutral", "upsert", "Findings"), "--bot-login", "intentgate-bot")
		if !strings.Contains(api.commentBody(id), "The comment for Findings") || len(api.comments) != 1 {
			t.Errorf("comments = %+v, want comment %d edited and none created", api.comments, id)
		}
	})

	t.Run("a bot-login that is not the default still gives one comment over two runs", func(t *testing.T) {
		api := newPubAPI(t)
		api.postAs = "intentgate-app[bot]" // the token belongs to another App
		dir := outDir(t, true, "neutral", "upsert", "Findings")
		mustPublish(t, dir, "--bot-login", "intentgate-app[bot]")
		mustPublish(t, dir, "--bot-login", "intentgate-app[bot]")
		if api.botComments() != 1 || len(api.comments) != 1 {
			t.Fatalf("comments = %+v, want exactly one", api.comments)
		}
		var posts int
		for _, c := range api.called() {
			if c.Method == "POST" && c.Path == path {
				posts++
			}
		}
		if posts != 1 {
			t.Errorf("%d comment POSTs over two runs, want 1", posts)
		}
	})

	t.Run("a race that created two comments converges: the oldest is kept and updated, the other deleted", func(t *testing.T) {
		api := newPubAPI(t)
		api.race = true
		alice := api.addComment("alice", "chatter")
		mallory := api.addComment("mallory", marked("forged"))
		mustPublish(t, outDir(t, true, "neutral", "upsert", "Findings"))
		if api.botComments() != 1 {
			t.Fatalf("comments = %+v, want exactly one bot comment", api.comments)
		}
		if !api.hasComment(alice) || !api.hasComment(mallory) || api.commentBody(mallory) != marked("forged") {
			t.Errorf("another user's comment was touched: %+v", api.comments)
		}
		var kept fakeComment
		for _, c := range api.comments {
			if c.Login == pubBot {
				kept = c
			}
		}
		// The racing run's comment was created just before ours, so it has the lower id: it is kept.
		var deleted int64
		for _, c := range api.called() {
			if c.Method == "DELETE" {
				deleted, _ = strconv.ParseInt(strings.TrimPrefix(c.Path, "/repos/octo/repo/issues/comments/"), 10, 64)
			}
		}
		if deleted == 0 || kept.ID != deleted-1 || !strings.Contains(kept.Body, "The comment for Findings") {
			t.Errorf("kept comment = %+v, deleted id %d; want the lower id kept with the new body", kept, deleted)
		}
		if api.count("DELETE") != 1 {
			t.Errorf("calls = %v, want exactly one DELETE", api.called())
		}
	})

	t.Run("two bot comments already there: the oldest is updated and the other deleted, nobody else's", func(t *testing.T) {
		api := newPubAPI(t)
		first := api.addComment(pubBot, marked("old a"))
		other := api.addComment("mallory", marked("forged"))
		second := api.addComment(pubBot, marked("old b"))
		mustPublish(t, outDir(t, true, "neutral", "upsert", "Findings"))
		if !strings.Contains(api.commentBody(first), "The comment for Findings") || api.hasComment(second) || !api.hasComment(other) {
			t.Errorf("comments = %+v, want %d updated, %d deleted, %d untouched", api.comments, first, second, other)
		}
	})

	t.Run("an empty 2xx answer to a PATCH is success", func(t *testing.T) {
		api := newPubAPI(t)
		api.emptyWrites = true
		api.runs = []fakeRun{{ID: 5, Name: "IntentGate", HeadSHA: pubSHA}}
		id := api.addComment(pubBot, marked("old"))
		mustPublish(t, outDir(t, true, "neutral", "upsert", "Findings"))
		if !strings.Contains(api.commentBody(id), "The comment for Findings") || api.runs[0].Title != "Findings" {
			t.Errorf("comments = %+v, runs = %+v", api.comments, api.runs)
		}
	})

	t.Run("a comment over 65000 bytes is cut on a UTF-8 boundary with the final line", func(t *testing.T) {
		api := newPubAPI(t)
		dir := outDir(t, true, "neutral", "upsert", "Findings")
		big := report.CommentMarker + "\n" + strings.Repeat("é€😀", 20000)
		if err := os.WriteFile(filepath.Join(dir, "comment.md"), []byte(big), 0o644); err != nil {
			t.Fatal(err)
		}
		mustPublish(t, dir)
		got := api.comments[0].Body
		const tail = "\n… (truncated; the check run has the full summary)"
		if len(got) > 65000 || !utf8.ValidString(got) || !strings.HasSuffix(got, tail) || !strings.HasPrefix(got, report.CommentMarker) {
			t.Errorf("posted %d bytes, valid UTF-8 %v, final line %v, marker first %v", len(got), utf8.ValidString(got), strings.HasSuffix(got, tail), strings.HasPrefix(got, report.CommentMarker))
		}
	})

	t.Run("a comment that contains but does not start with the marker is ignored", func(t *testing.T) {
		api := newPubAPI(t)
		api.addComment(pubBot, "quoting "+report.CommentMarker+" in the middle")
		mustPublish(t, outDir(t, true, "neutral", "upsert", "Findings"))
		if len(api.comments) != 2 {
			t.Errorf("comments = %+v, want a new one created", api.comments)
		}
	})

	t.Run("update-if-exists with none: it looks, and writes no comment", func(t *testing.T) {
		api := newPubAPI(t)
		mustPublish(t, outDir(t, true, "success", "update-if-exists", "Clean"))
		for _, c := range api.called() {
			if strings.Contains(c.Path, "/issues/") && c.Method != "GET" {
				t.Errorf("comment write %v", c)
			}
		}
		if len(api.comments) != 0 {
			t.Errorf("comments = %+v", api.comments)
		}
	})

	t.Run("update-if-exists with one: it is edited", func(t *testing.T) {
		api := newPubAPI(t)
		id := api.addComment(pubBot, marked("old findings"))
		mustPublish(t, outDir(t, true, "success", "update-if-exists", "Clean"))
		if !strings.Contains(api.commentBody(id), "The comment for Clean") || len(api.comments) != 1 {
			t.Errorf("comments = %+v", api.comments)
		}
	})

	t.Run("none: no comment call at all, even with a marker comment present", func(t *testing.T) {
		api := newPubAPI(t)
		id := api.addComment(pubBot, marked("old"))
		mustPublish(t, outDir(t, true, "neutral", "none", "No lock"))
		for _, c := range api.called() {
			if strings.Contains(c.Path, "/issues/") {
				t.Errorf("comment call %v", c)
			}
		}
		if api.commentBody(id) != marked("old") {
			t.Errorf("the comment was edited: %q", api.commentBody(id))
		}
	})

	t.Run("a comment on page 3 is found", func(t *testing.T) {
		api := newPubAPI(t) // two comments per page
		for i := 0; i < 4; i++ {
			api.addComment("alice", fmt.Sprintf("chatter %d", i))
		}
		id := api.addComment(pubBot, marked("old")) // the fifth: page 3
		mustPublish(t, outDir(t, true, "neutral", "upsert", "Findings"))
		if !strings.Contains(api.commentBody(id), "The comment for Findings") || len(api.comments) != 5 {
			t.Errorf("%d comments, comment %d = %q; want it edited and none created", len(api.comments), id, api.commentBody(id))
		}
	})

	t.Run("two consecutive runs leave exactly one bot comment", func(t *testing.T) {
		api := newPubAPI(t)
		dir := outDir(t, true, "neutral", "upsert", "Findings")
		mustPublish(t, dir)
		mustPublish(t, dir)
		if api.botComments() != 1 || len(api.comments) != 1 {
			t.Fatalf("comments = %+v, want exactly one", api.comments)
		}
	})
}

func TestPublish_NotPublished(t *testing.T) {
	api := newPubAPI(t)
	api.addComment(pubBot, report.CommentMarker+"\nold")
	code, out, errs := publishRun(t, outDir(t, false, "neutral", "none", ""))
	if code != 0 || errs != "" {
		t.Errorf("exit %d, stdout %q, stderr %q; want 0 and no error output", code, out, errs)
	}
	if calls := api.called(); len(calls) != 0 {
		t.Errorf("publish: false made API calls: %v", calls)
	}
}

func TestPublish_Errors(t *testing.T) {
	t.Run("no token is a usage error", func(t *testing.T) {
		api := newPubAPI(t)
		t.Setenv("GITHUB_TOKEN", "")
		code, _, errs := publishRun(t, outDir(t, true, "neutral", "none", "T"))
		if code != 2 || !strings.Contains(errs, "GITHUB_TOKEN") {
			t.Errorf("exit %d, stderr %q; want 2 naming GITHUB_TOKEN", code, errs)
		}
		if len(api.called()) != 0 {
			t.Errorf("calls = %v", api.called())
		}
	})

	t.Run("bad usage is 2", func(t *testing.T) {
		newPubAPI(t)
		dir := outDir(t, true, "neutral", "none", "T")
		for name, args := range map[string][]string{
			"no flags":     {"publish"},
			"bad repo":     {"publish", "--out-dir", dir, "--repo", "octo", "--pr", "7", "--head-sha", pubSHA},
			"bad pr":       {"publish", "--out-dir", dir, "--repo", "octo/repo", "--pr", "0", "--head-sha", pubSHA},
			"bad sha":      {"publish", "--out-dir", dir, "--repo", "octo/repo", "--pr", "7", "--head-sha", "../x"},
			"extra arg":    {"publish", "--out-dir", dir, "--repo", "octo/repo", "--pr", "7", "--head-sha", pubSHA, "x"},
			"empty outdir": {"publish", "--out-dir", "", "--repo", "octo/repo", "--pr", "7", "--head-sha", pubSHA},
		} {
			var out, errb bytes.Buffer
			if code := Run(args, strings.NewReader(""), &out, Deps{Stderr: &errb, Dir: t.TempDir()}); code != 2 {
				t.Errorf("%s: exit %d, want 2 (stderr %q)", name, code, errb.String())
			}
		}
	})

	t.Run("API 403 is exit 3 with the status and message, and never the token", func(t *testing.T) {
		api := newPubAPI(t)
		api.status, api.message = 403, "Resource not accessible by integration"
		code, out, errs := publishRun(t, outDir(t, true, "neutral", "upsert", "T"))
		if code != 3 || !strings.Contains(errs, "403") || !strings.Contains(errs, "Resource not accessible by integration") {
			t.Errorf("exit %d, stderr %q; want 3 with the status and message", code, errs)
		}
		if strings.Contains(out, pubToken) || strings.Contains(errs, pubToken) {
			t.Errorf("the token leaked: stdout %q stderr %q", out, errs)
		}
	})

	t.Run("an unreadable or invalid decision is exit 3 and no call", func(t *testing.T) {
		api := newPubAPI(t)
		empty := t.TempDir()
		if code, _, errs := publishRun(t, empty); code != 3 || !strings.Contains(errs, "decision.json") {
			t.Errorf("missing files: exit %d, stderr %q; want 3 naming decision.json", code, errs)
		}
		bad := outDir(t, true, "neutral", "sometimes", "T")
		if code, _, errs := publishRun(t, bad); code != 3 || !strings.Contains(errs, "sometimes") {
			t.Errorf("bad comment mode: exit %d, stderr %q; want 3 naming the mode", code, errs)
		}
		if len(api.called()) != 0 {
			t.Errorf("calls = %v", api.called())
		}
	})

	t.Run("a conclusion other than success or neutral is exit 3 and no call", func(t *testing.T) {
		api := newPubAPI(t)
		for _, c := range []string{"failure", "action_required", "cancelled", "timed_out", "skipped", "stale", "bogus", ""} {
			code, _, errs := publishRun(t, outDir(t, true, c, "none", "T"))
			if code != 3 || !strings.Contains(errs, "conclusion") {
				t.Errorf("conclusion %q: exit %d, stderr %q; want 3 naming the conclusion", c, code, errs)
			}
		}
		if len(api.called()) != 0 {
			t.Errorf("calls = %v", api.called())
		}
		// not publishing: the conclusion is not looked at
		if code, _, _ := publishRun(t, outDir(t, false, "failure", "none", "")); code != 0 {
			t.Errorf("publish false with any conclusion: exit %d, want 0", code)
		}
	})

	t.Run("it never exits 1", func(t *testing.T) {
		api := newPubAPI(t)
		api.status = 500
		if code, _, _ := publishRun(t, outDir(t, true, "neutral", "none", "T")); code == 1 {
			t.Errorf("exit 1")
		}
	})
}
