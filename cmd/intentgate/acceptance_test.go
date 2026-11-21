package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/autox-my/intent-gate/internal/lock"
	"github.com/autox-my/intent-gate/internal/ratify"
)

// S-23 (pr-check.md §7, REQ-0045): `intentgate acceptance`. The repositories are temporary, the
// GitHub server is a fake on loopback, the token is synthetic, and the intents are synthetic.

const (
	accToken      = "ghp_FAKE_TOKEN_acceptance_tests_0123456789"
	accIntent     = "intents/checkout-v2.md"
	accIntentText = "# Checkout v2\n\nWhat: one-page checkout.\n"
)

type reply struct {
	status int
	body   string
}

// accAPI is a fake GitHub for octo/repo: pulls by commit sha, reviews by pull request number.
type accAPI struct {
	mu      sync.Mutex
	paths   []string
	pulls   map[string]reply
	reviews map[int]reply
}

func newAccAPI(t *testing.T) *accAPI {
	t.Helper()
	f := &accAPI{pulls: map[string]reply{}, reviews: map[int]reply{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.paths = append(f.paths, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+accToken {
			http.Error(w, `{"message":"bad credentials"}`, http.StatusUnauthorized)
			return
		}
		const pre = "/repos/octo/repo/"
		var rep reply
		var ok bool
		switch {
		case strings.HasPrefix(r.URL.Path, pre+"commits/") && strings.HasSuffix(r.URL.Path, "/pulls"):
			sha := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, pre+"commits/"), "/pulls")
			rep, ok = f.pulls[sha]
		case strings.HasPrefix(r.URL.Path, pre+"pulls/") && strings.HasSuffix(r.URL.Path, "/reviews"):
			var n int
			fmt.Sscanf(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, pre+"pulls/"), "/reviews"), "%d", &n)
			rep, ok = f.reviews[n]
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		if rep.status != 0 {
			w.WriteHeader(rep.status)
			// a hostile server echoing the credentials must not get them printed
			fmt.Fprintf(w, `{"message":%q}`, "denied "+r.Header.Get("Authorization"))
			return
		}
		fmt.Fprint(w, rep.body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", accToken)
	return f
}

func (f *accAPI) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func mergedPull(n int) string {
	return fmt.Sprintf(`{"number":%d,"merged_at":"2026-11-18T10:00:00Z"}`, n)
}
func openPull(n int) string { return fmt.Sprintf(`{"number":%d,"merged_at":null}`, n) }

// reviewsOf builds a reviews body from login:state pairs.
func reviewsOf(pairs ...string) string {
	var items []string
	for _, p := range pairs {
		login, state, _ := strings.Cut(p, ":")
		items = append(items, fmt.Sprintf(`{"user":{"login":%q},"state":%q}`, login, state))
	}
	return "[" + strings.Join(items, ",") + "]"
}

// committedLock builds the lock of a committed intent at path, bound to blob.
func committedLock(t *testing.T, id, path, blob string) []byte {
	t.Helper()
	l, _, err := ratify.MaskAndBuild(noScan, accIntentText, ratify.BuildInput{
		ID: id, BaseRef: "main", Committed: &ratify.CommittedIntent{Path: path, Blob: blob},
		Criteria: []lock.Criterion{crit("C1", "It works", "src/**")},
		Ratifier: "github:octocat", RatifiedAt: ratifiedOK, Method: "hook", ReviewDurationMS: 48210,
	})
	if err != nil {
		t.Fatal(err)
	}
	return marshalLock(t, l)
}

// accScenario is a repository whose main holds the intent (and what the case adds), and whose
// feature branch adds a lock on it. It returns the repository and the sha of the commit that last
// changed the intent on main.
type accScenario struct {
	codeowners map[string]string // CODEOWNERS files on main, by path
	onFeature  bool              // the intent is added on the feature branch, not on main
}

func (s accScenario) build(t *testing.T) (r *vrepo, intentCommit string) {
	t.Helper()
	r = newVRepo(t)
	if !s.onFeature {
		r.write(accIntent, accIntentText)
		intentCommit = r.commit("add the intent", baseDate)
	}
	for p, c := range s.codeowners {
		r.write(p, c)
	}
	r.write("docs/unrelated.md", "later\n")
	r.commit("later commit, not touching the intent", baseDate)
	r.feature()
	if s.onFeature {
		r.write(accIntent, accIntentText)
		r.commit("add the intent in the PR", headDate)
	}
	blob := r.git("rev-parse", "HEAD:"+accIntent)
	r.write(lockPath(lockA), string(committedLock(t, lockA, accIntent, blob)))
	r.write("src/a.go", "package src\n")
	r.commit("the work", headDate)
	return r, intentCommit
}

// acceptance runs `intentgate acceptance` for the scenario's PR: verify first (it writes
// verify.json), then acceptance into the same out dir.
func (r *vrepo) acceptance(t *testing.T, extra ...string) (code int, outDir string, stdout, stderr string) {
	t.Helper()
	_, outDir, _, _ = r.verify()
	args := append([]string{"acceptance", "--out-dir", outDir, "--repo", "octo/repo", "--base", "main", "--head", "feature"}, extra...)
	var out, errb bytes.Buffer
	code = Run(args, strings.NewReader(""), &out, Deps{Dir: r.dir, Stderr: &errb})
	return code, outDir, out.String(), errb.String()
}

type accDoc struct {
	Schema  string `json:"schema"`
	Intents []struct {
		LockID string `json:"lock_id"`
		Path   string `json:"path"`
		Blob   string `json:"blob"`
		Status string `json:"status"`
		Reason string `json:"reason"`
		PR     int    `json:"pr"`
	} `json:"intents"`
}

func readAcc(t *testing.T, outDir string) accDoc {
	t.Helper()
	var d accDoc
	if err := json.Unmarshal(readOut(t, outDir, "acceptance.json"), &d); err != nil {
		t.Fatalf("acceptance.json: %v", err)
	}
	return d
}

// REQ-0045
func TestAcceptance(t *testing.T) {
	const nopr = "reached the base branch without a pull request"
	const noteam = "owned by a team or an email; checking membership needs more than the Action's token"
	const noneApproved = "approved by none of its code owners"

	cases := []struct {
		name       string
		scenario   func(f *accAPI, intentCommit string) accScenario // also arms the API
		wantStatus string
		wantReason string // exact, or a fragment when it starts with "~"
		wantPR     int
		wantNoAPI  bool
	}{
		{
			name: "accepted: a code owner approved the PR that merged the intent (login case ignored)",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("Alice:APPROVED")}
				return accScenario{codeowners: map[string]string{".github/CODEOWNERS": "* @bob\n/intents/ @alice\n"}}
			},
			wantStatus: "accepted", wantReason: "approved in #12 by its code owner @alice", wantPR: 12,
		},
		{
			name: "accepted: an owner who approved and then only commented still approves",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("alice:APPROVED", "alice:COMMENTED")}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "/intents/ @alice\n"}}
			},
			wantStatus: "accepted", wantReason: "approved in #12 by its code owner @alice", wantPR: 12,
		},
		{
			name: "accepted: of several merged PRs the lowest-numbered counts",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(20) + "," + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("alice:APPROVED")}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "/intents/ @alice\n"}}
			},
			wantStatus: "accepted", wantReason: "approved in #12 by its code owner @alice", wantPR: 12,
		},
		{
			name: "accepted: a user among team owners is enough",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("alice:APPROVED")}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "/intents/ @acme/reviewers @alice\n"}}
			},
			wantStatus: "accepted", wantReason: "approved in #12 by its code owner @alice", wantPR: 12,
		},
		{
			name: "accepted: the first CODEOWNERS file that exists wins (.github/ over the root)",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("alice:APPROVED")}
				return accScenario{codeowners: map[string]string{".github/CODEOWNERS": "* @alice\n", "CODEOWNERS": "* @bob\n"}}
			},
			wantStatus: "accepted", wantReason: "approved in #12 by its code owner @alice", wantPR: 12,
		},
		{
			name: "not-accepted: the intent is added in this PR, so its blob is not on the base",
			scenario: func(f *accAPI, c string) accScenario {
				return accScenario{onFeature: true, codeowners: map[string]string{"CODEOWNERS": "* @alice\n"}}
			},
			wantStatus: "not-accepted", wantReason: "this version of the intent is not on the base branch", wantNoAPI: true,
		},
		{
			name: "not-accepted: no PR for the commit",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[]"}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "* @alice\n"}}
			},
			wantStatus: "not-accepted", wantReason: nopr,
		},
		{
			name: "not-accepted: the only PR was never merged",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + openPull(12) + "]"}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "* @alice\n"}}
			},
			wantStatus: "not-accepted", wantReason: nopr,
		},
		{
			name: "not-accepted: approved only by people who are not code owners",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("bob:APPROVED", "carol:APPROVED")}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "/intents/ @alice\n"}}
			},
			wantStatus: "not-accepted", wantReason: noneApproved, wantPR: 12,
		},
		{
			name: "not-accepted: an owner approved and then requested changes",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("alice:APPROVED", "alice:CHANGES_REQUESTED")}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "/intents/ @alice\n"}}
			},
			wantStatus: "not-accepted", wantReason: noneApproved, wantPR: 12,
		},
		{
			name: "not-accepted: an owner's approval was dismissed",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("alice:APPROVED", "alice:DISMISSED")}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "/intents/ @alice\n"}}
			},
			wantStatus: "not-accepted", wantReason: noneApproved, wantPR: 12,
		},
		{
			name: "unknown: no CODEOWNERS file at the base",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("alice:APPROVED")}
				return accScenario{}
			},
			wantStatus: "unknown", wantReason: "no code owner for " + accIntent, wantPR: 12,
		},
		{
			name: "unknown: no CODEOWNERS line matches the intent",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("alice:APPROVED")}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "/src/ @alice\n"}}
			},
			wantStatus: "unknown", wantReason: "no code owner for " + accIntent, wantPR: 12,
		},
		{
			name: "unknown: an owner-less line un-owns the intent",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("alice:APPROVED")}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "* @alice\n/intents/\n"}}
			},
			wantStatus: "unknown", wantReason: "no code owner for " + accIntent, wantPR: 12,
		},
		{
			name: "a line with an invalid owner is skipped, so the earlier line's owners decide",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("alice:APPROVED")}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "/intents/ @alice\n/intents/ not-an-owner\n/intents/ @alice#comment\n"}}
			},
			wantStatus: "accepted", wantReason: "approved in #12 by its code owner @alice", wantPR: 12,
		},
		{
			name: "unknown: the owner is a team",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("bob:APPROVED")}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "/intents/ @acme/reviewers\n"}}
			},
			wantStatus: "unknown", wantReason: noteam, wantPR: 12,
		},
		{
			name: "unknown: the owner is an email",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{body: reviewsOf("bob:APPROVED")}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "/intents/ lead@example.com\n"}}
			},
			wantStatus: "unknown", wantReason: noteam, wantPR: 12,
		},
		{
			name: "unknown: the API answers 403 for the pulls of the commit",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{status: 403}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "* @alice\n"}}
			},
			wantStatus: "unknown", wantReason: "~403",
		},
		{
			name: "unknown: the API answers 403 for the reviews",
			scenario: func(f *accAPI, c string) accScenario {
				f.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
				f.reviews[12] = reply{status: 403}
				return accScenario{codeowners: map[string]string{"CODEOWNERS": "* @alice\n"}}
			},
			wantStatus: "unknown", wantReason: "~403", wantPR: 12,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newAccAPI(t)
			// the scenario needs the intent commit's sha to arm the API, and the repository to know the
			// sha: build once to learn it, with a throwaway API, then arm and re-run on the same repo
			probe := tc.scenario(&accAPI{pulls: map[string]reply{}, reviews: map[int]reply{}}, "")
			r, intentCommit := probe.build(t)
			tc.scenario(api, intentCommit)
			code, out, stdout, stderr := r.acceptance(t)
			if code != 0 {
				t.Fatalf("exit %d, want 0 whatever the status\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if strings.Contains(stdout+stderr, accToken) {
				t.Errorf("the token is in the output: %q %q", stdout, stderr)
			}
			d := readAcc(t, out)
			if d.Schema != "intentgate.acceptance/1" || len(d.Intents) != 1 {
				t.Fatalf("acceptance.json = %+v, want one intent", d)
			}
			in := d.Intents[0]
			if in.LockID != lockA || in.Path != accIntent || in.Blob != r.git("rev-parse", "HEAD:"+accIntent) {
				t.Errorf("intent = %+v", in)
			}
			if in.Status != tc.wantStatus || in.PR != tc.wantPR {
				t.Errorf("status %q pr %d (reason %q), want %q pr %d", in.Status, in.PR, in.Reason, tc.wantStatus, tc.wantPR)
			}
			if frag, ok := strings.CutPrefix(tc.wantReason, "~"); ok {
				if !strings.Contains(in.Reason, frag) || strings.Contains(in.Reason, accToken) {
					t.Errorf("reason = %q, want it to mention %q and not the token", in.Reason, frag)
				}
			} else if in.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", in.Reason, tc.wantReason)
			}
			if tc.wantNoAPI && len(api.calls()) != 0 {
				t.Errorf("API calls %v, want none: the first rule settled it", api.calls())
			}
		})
	}

	t.Run("a CODEOWNERS file that exists only at head is not used", func(t *testing.T) {
		api := newAccAPI(t)
		r, c := accScenario{}.build(t)
		api.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
		api.reviews[12] = reply{body: reviewsOf("alice:APPROVED")}
		// the PR itself adds a CODEOWNERS naming its approver: it must not count
		r.write("CODEOWNERS", "* @alice\n")
		r.commit("the PR adds CODEOWNERS", headDate)
		code, out, _, stderr := r.acceptance(t)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if in := readAcc(t, out).Intents[0]; in.Status != "unknown" || in.Reason != "no code owner for "+accIntent {
			t.Errorf("intent = %+v, want unknown: no code owner", in)
		}
	})

	t.Run("two locks, one accepted and one not: the bytes are known and sorted by lock id", func(t *testing.T) {
		api := newAccAPI(t)
		r := newVRepo(t)
		r.write("intents/a-one.md", accIntentText)
		r.write("CODEOWNERS", "/intents/ @alice\n")
		c := r.commit("add intent one and CODEOWNERS", baseDate)
		r.feature()
		r.write("intents/b-two.md", accIntentText) // added in the PR: not on the base
		blobOne := r.git("rev-parse", "main:intents/a-one.md")
		r.commit("add intent two", headDate)
		blobTwo := r.git("rev-parse", "HEAD:intents/b-two.md")
		r.write(lockPath(lockB), string(committedLock(t, lockB, "intents/a-one.md", blobOne)))
		r.write(lockPath(lockA), string(committedLock(t, lockA, "intents/b-two.md", blobTwo)))
		_, plain := mkLock(t, "01JD8Z6Q4M7R2T9X3B5N8K1D8H", "main", ratifiedOK, crit("C1", "plain", "src/**"))
		r.write(lockPath("01JD8Z6Q4M7R2T9X3B5N8K1D8H"), string(plain)) // not a committed intent: not listed
		r.write("src/a.go", "package src\n")
		r.commit("the work", headDate)
		api.pulls[c] = reply{body: "[" + mergedPull(12) + "]"}
		api.reviews[12] = reply{body: reviewsOf("alice:APPROVED")}

		code, out, _, stderr := r.acceptance(t)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		want := fmt.Sprintf(`{
  "schema": "intentgate.acceptance/1",
  "intents": [
    {
      "lock_id": %q,
      "path": "intents/b-two.md",
      "blob": %q,
      "status": "not-accepted",
      "reason": "this version of the intent is not on the base branch",
      "pr": 0
    },
    {
      "lock_id": %q,
      "path": "intents/a-one.md",
      "blob": %q,
      "status": "accepted",
      "reason": "approved in #12 by its code owner @alice",
      "pr": 12
    }
  ]
}
`, lockA, blobTwo, lockB, blobOne)
		if got := string(readOut(t, out, "acceptance.json")); got != want {
			t.Errorf("acceptance.json =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("a PR with no committed-intent lock gets an empty list, and verify.json is not touched", func(t *testing.T) {
		newAccAPI(t)
		r := newVRepo(t)
		r.feature()
		_, plain := mkLock(t, lockA, "main", ratifiedOK, crit("C1", "plain", "src/**"))
		r.write(lockPath(lockA), string(plain))
		r.write("src/a.go", "package src\n")
		r.commit("the work", headDate)
		_, out, _, _ := r.verify()
		before := readOut(t, out, "verify.json")
		args := []string{"acceptance", "--out-dir", out, "--repo", "octo/repo", "--base", "main", "--head", "feature"}
		var stdout, stderr bytes.Buffer
		if code := Run(args, strings.NewReader(""), &stdout, Deps{Dir: r.dir, Stderr: &stderr}); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
		want := "{\n  \"schema\": \"intentgate.acceptance/1\",\n  \"intents\": []\n}\n"
		if got := string(readOut(t, out, "acceptance.json")); got != want {
			t.Errorf("acceptance.json = %q, want %q", got, want)
		}
		if after := readOut(t, out, "verify.json"); !bytes.Equal(before, after) {
			t.Errorf("verify.json changed:\n%s\nwas\n%s", after, before)
		}
	})

	t.Run("a committed-intent case leaves verify.json byte-identical", func(t *testing.T) {
		api := newAccAPI(t)
		r, c := accScenario{codeowners: map[string]string{"CODEOWNERS": "* @alice\n"}}.build(t)
		api.pulls[c] = reply{body: "[]"}
		_, out, _, _ := r.verify()
		before := readOut(t, out, "verify.json")
		args := []string{"acceptance", "--out-dir", out, "--repo", "octo/repo", "--base", "main", "--head", "feature"}
		var stdout, stderr bytes.Buffer
		if code := Run(args, strings.NewReader(""), &stdout, Deps{Dir: r.dir, Stderr: &stderr}); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
		if after := readOut(t, out, "verify.json"); !bytes.Equal(before, after) {
			t.Errorf("verify.json changed")
		}
	})

	t.Run("exit codes: 2 for usage and a missing token, 3 when acceptance.json cannot be written", func(t *testing.T) {
		newAccAPI(t)
		r := newVRepo(t)
		r.feature()
		r.write("src/a.go", "package src\n")
		r.commit("the work", headDate)
		_, out, _, _ := r.verify()
		run := func(args ...string) int {
			var stdout, stderr bytes.Buffer
			return Run(append([]string{"acceptance"}, args...), strings.NewReader(""), &stdout, Deps{Dir: r.dir, Stderr: &stderr})
		}
		good := []string{"--out-dir", out, "--repo", "octo/repo", "--base", "main", "--head", "feature"}
		if code := run(good...); code != 0 {
			t.Fatalf("a good command line exits %d, want 0", code)
		}
		for name, args := range map[string][]string{
			"no --out-dir":  {"--repo", "octo/repo", "--base", "main", "--head", "feature"},
			"no --repo":     {"--out-dir", out, "--base", "main", "--head", "feature"},
			"bad --repo":    {"--out-dir", out, "--repo", "octo", "--base", "main", "--head", "feature"},
			"no --base":     {"--out-dir", out, "--repo", "octo/repo", "--head", "feature"},
			"no --head":     {"--out-dir", out, "--repo", "octo/repo", "--base", "main"},
			"extra operand": append(append([]string{}, good...), "extra"),
		} {
			if code := run(args...); code != 2 {
				t.Errorf("%s: exit %d, want 2", name, code)
			}
		}
		t.Setenv("GITHUB_TOKEN", "")
		if code := run(good...); code != 2 {
			t.Errorf("no GITHUB_TOKEN: exit %d, want 2", code)
		}
		t.Setenv("GITHUB_TOKEN", accToken)
		empty := t.TempDir()
		if code := run("--out-dir", empty, "--repo", "octo/repo", "--base", "main", "--head", "feature"); code != 3 {
			t.Errorf("no verify.json: exit %d, want 3", code)
		}
		if _, err := os.Stat(filepath.Join(empty, "acceptance.json")); err == nil {
			t.Error("acceptance.json was written without a verify.json")
		}
		if code := run("--out-dir", out, "--repo", "octo/repo", "--base", "no-such-branch", "--head", "feature"); code != 3 {
			t.Errorf("git cannot resolve the base: exit %d, want 3", code)
		}
	})
}
