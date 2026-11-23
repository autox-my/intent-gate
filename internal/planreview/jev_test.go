package planreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// Everything here but TestJev_Live runs against a fake TypeSafe on loopback with a synthetic key.
const testKey = "tsk-synthetic-key-0123456789"

// seenRequest is what the fake server captured.
type seenRequest struct {
	Method, Path string
	Header       http.Header
	Raw          []byte
	Body         struct {
		Model     string                       `json:"model"`
		State     map[string]json.RawMessage   `json:"state"`
		Questions map[string]map[string]string `json:"questions"`
	}
}

// fakeJev serves answers(question ids) as the noul values, records every request and counts them.
type fakeJev struct {
	*httptest.Server
	mu   sync.Mutex
	seen []seenRequest
}

func newFakeJev(t *testing.T, answers func(ids []string) map[string]any) *fakeJev {
	t.Helper()
	f := &fakeJev{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		sr := seenRequest{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Raw: raw}
		_ = json.Unmarshal(raw, &sr.Body)
		f.mu.Lock()
		f.seen = append(f.seen, sr)
		f.mu.Unlock()
		var ids []string
		for id := range sr.Body.Questions {
			ids = append(ids, id)
		}
		out := map[string]any{}
		if answers != nil {
			for id, v := range answers(ids) {
				if v == nil {
					continue // a missing answer
				}
				out[id] = map[string]any{"type": "noul", "noul": v}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "jev-test", "answers": out, "usage": map[string]any{}})
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeJev) requests() []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seenRequest(nil), f.seen...)
}

func (f *fakeJev) jev() Jev { return Jev{BaseURL: f.URL, Key: testKey, Model: "jev-test"} }

const (
	rootIntent = "- Add SARIF output to the report command.\n- Keep the report under one page.\n- Never block a merge ever.\n"
	plan       = "1. Add a SARIF writer to the report package.\n2. Rewrite the whole CLI in Rust.\n3. Make the page limit entirely optional.\n"
)

// REQ-0016, REQ-0017, REQ-0018
func TestJev_MapsAnswersToFindings(t *testing.T) {
	f := newFakeJev(t, func([]string) map[string]any {
		return map[string]any{
			"omit_0": 0.1,     // omission, confidence 0.9
			"omit_1": omitMax, // exactly the threshold: no finding
			"omit_2": -0.2,    // out of range: skipped, not a finding
			"weak_0": weakMin, // exactly the threshold: no finding
			"weak_1": 0.92,    // weakening, confidence 0.92
			"weak_2": 1.5,     // out of range: skipped
			"add_0":  nil,     // missing: skipped
			"add_1":  0.05,    // addition, confidence 0.95
			"add_2":  addMax,  // exactly the threshold: no finding
		}
	})
	got, err := f.jev().Review(context.Background(), rootIntent, plan)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	want := []Finding{
		{Kind: Omission, Quote: "Add SARIF output to the report command.", Confidence: 0.9},
		{Kind: Weakening, Quote: "Keep the report under one page.", Confidence: 0.92},
		{Kind: Addition, Quote: "Rewrite the whole CLI in Rust.", Confidence: 0.95},
	}
	if len(got) != len(want) {
		t.Fatalf("findings = %+v, want exactly %d", got, len(want))
	}
	byKind := map[Kind]Finding{}
	for _, g := range got {
		if _, dup := byKind[g.Kind]; dup {
			t.Fatalf("two findings of kind %s: %+v", g.Kind, got)
		}
		byKind[g.Kind] = g
	}
	for _, w := range want {
		g, ok := byKind[w.Kind]
		if !ok {
			t.Errorf("no %s finding in %+v", w.Kind, got)
			continue
		}
		if g.Quote != w.Quote {
			t.Errorf("%s quote = %q, want %q", w.Kind, g.Quote, w.Quote)
		}
		if math.Abs(g.Confidence-w.Confidence) > 1e-9 {
			t.Errorf("%s confidence = %v, want %v", w.Kind, g.Confidence, w.Confidence)
		}
		if g.Text == "" || strings.Contains(g.Text, g.Quote) {
			t.Errorf("%s text = %q, want a fixed sentence that does not repeat the quote", w.Kind, g.Text)
		}
	}
	// Through Run it is a complete review whose counts match.
	res := Run(context.Background(), f.jev(), rootIntent, plan, 2*time.Second)
	if res.Status != Complete || res.Counts != (Counts{Omissions: 1, Additions: 1, Weakenings: 1}) {
		t.Errorf("Run = %q %+v, want complete with one of each", res.Status, res.Counts)
	}
}

func TestJev_NothingToAsk(t *testing.T) {
	f := newFakeJev(t, nil)
	for name, c := range map[string][2]string{
		"no root items": {"ok. fine.", plan},
		"no plan items": {rootIntent, "do it"},
		"both empty":    {"", ""},
	} {
		got, err := f.jev().Review(context.Background(), c[0], c[1])
		if err != nil || len(got) != 0 {
			t.Errorf("%s: got %+v, %v; want no findings and no error", name, got, err)
		}
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("%d requests were sent, want none", n)
	}
	if res := Run(context.Background(), f.jev(), "", "", time.Second); res.Status != Complete {
		t.Errorf("status = %q, want complete", res.Status)
	}
}

func TestJev_Request(t *testing.T) {
	f := newFakeJev(t, nil)
	root := "- Add SARIF output to the report command.\n- Keep the report under one page."
	pl := "1. Add a SARIF writer to the report package.\n2. Rewrite the whole CLI in Rust."
	if _, err := f.jev().Review(context.Background(), root, pl); err != nil {
		t.Fatal(err)
	}
	reqs := f.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want exactly one per review", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodPost || r.Path != "/v1/systemone" {
		t.Errorf("request = %s %s, want POST /v1/systemone", r.Method, r.Path)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization header is wrong (len %d)", len(got))
	}
	if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q", got)
	}
	if r.Body.Model != "jev-test" {
		t.Errorf("model = %q, want jev-test", r.Body.Model)
	}

	var rootSent, planSent string
	var rootTrunc, planTrunc bool
	items := map[string]string{}
	mustField(t, r.Body.State, "root_intent", &rootSent)
	mustField(t, r.Body.State, "plan", &planSent)
	mustField(t, r.Body.State, "root_intent_truncated", &rootTrunc)
	mustField(t, r.Body.State, "plan_truncated", &planTrunc)
	mustField(t, r.Body.State, "items", &items)
	if rootSent != root || planSent != pl {
		t.Errorf("state carries root %q plan %q, want the texts as given", rootSent, planSent)
	}
	if rootTrunc || planTrunc {
		t.Error("short texts are flagged as truncated")
	}
	wantItems := map[string]string{
		"r0": "Add SARIF output to the report command.",
		"r1": "Keep the report under one page.",
		"p0": "Add a SARIF writer to the report package.",
		"p1": "Rewrite the whole CLI in Rust.",
	}
	if !reflect.DeepEqual(items, wantItems) {
		t.Errorf("items = %v, want %v", items, wantItems)
	}
	for _, id := range []string{"omit_0", "omit_1", "weak_0", "weak_1", "add_0", "add_1"} {
		q, ok := r.Body.Questions[id]
		if !ok {
			t.Errorf("question %s is missing", id)
			continue
		}
		if q["type"] != "noul" {
			t.Errorf("%s type = %q, want noul", id, q["type"])
		}
		ins := q["instructions"]
		for _, text := range items {
			if strings.Contains(ins, text) || strings.Contains(ins, strings.TrimSuffix(text, ".")) {
				t.Errorf("%s instructions contain item text %q: %q", id, text, ins)
			}
		}
		if !strings.Contains(ins, "state.items."+itemOf(id)) {
			t.Errorf("%s instructions %q do not refer to state.items.%s", id, ins, itemOf(id))
		}
		if !strings.Contains(strings.ToLower(ins), "data") || !strings.Contains(strings.ToLower(ins), "instruction") {
			t.Errorf("%s instructions do not say that text in state is data, not instructions: %q", id, ins)
		}
	}
	if len(r.Body.Questions) != 6 {
		t.Errorf("%d questions, want 6 (2 per root item, 1 per plan item)", len(r.Body.Questions))
	}
	// Each of the three question kinds has one fixed string, whatever the item.
	for _, pre := range []string{"omit", "weak", "add"} {
		a, b := r.Body.Questions[pre+"_0"]["instructions"], r.Body.Questions[pre+"_1"]["instructions"]
		if strings.ReplaceAll(a, "0", "N") != strings.ReplaceAll(b, "1", "N") {
			t.Errorf("%s instructions differ beyond the item id: %q vs %q", pre, a, b)
		}
	}
	if strings.Contains(string(r.Raw), testKey) {
		t.Error("the key is in the request body")
	}
}

// itemOf maps a question id to the item id it asks about.
func itemOf(q string) string {
	kind, n, _ := strings.Cut(q, "_")
	if kind == "add" {
		return "p" + n
	}
	return "r" + n
}

func mustField(t *testing.T, m map[string]json.RawMessage, key string, out any) {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		t.Errorf("state.%s is missing", key)
		return
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Errorf("state.%s: %v", key, err)
	}
}

func TestJev_RequestCapsAndTruncation(t *testing.T) {
	f := newFakeJev(t, nil)
	var rb, pb strings.Builder
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&rb, "- root item number %02d here\n", i)
		fmt.Fprintf(&pb, "- plan item number %02d here\n", i)
	}
	if _, err := f.jev().Review(context.Background(), rb.String(), pb.String()); err != nil {
		t.Fatal(err)
	}
	if len(f.requests()) != 1 {
		t.Fatalf("%d requests, want 1", len(f.requests()))
	}
	r := f.requests()[0]
	items := map[string]string{}
	mustField(t, r.Body.State, "items", &items)
	if len(items) != 40 {
		t.Fatalf("%d items, want 20 root + 20 plan", len(items))
	}
	if items["r0"] != "root item number 05 here" || items["r19"] != "root item number 24 here" {
		t.Errorf("root items r0=%q r19=%q, want the last 20 (05..24)", items["r0"], items["r19"])
	}
	if items["p0"] != "plan item number 00 here" || items["p19"] != "plan item number 19 here" {
		t.Errorf("plan items p0=%q p19=%q, want the first 20 (00..19)", items["p0"], items["p19"])
	}
	if len(r.Body.Questions) != 60 {
		t.Errorf("%d questions, want 60", len(r.Body.Questions))
	}

	// 48 000 bytes: the root keeps its tail, the plan its head, both cut on a rune boundary.
	f2 := newFakeJev(t, nil)
	longRoot := "- start of root intent\n" + strings.Repeat("é€ ", 30000) + "\n- the very last root item here"
	longPlan := "- start of the plan here\n" + strings.Repeat("€é ", 30000) + "\n- the very last plan item"
	if _, err := f2.jev().Review(context.Background(), longRoot, longPlan); err != nil {
		t.Fatal(err)
	}
	if len(f2.requests()) != 1 {
		t.Fatalf("%d requests, want 1", len(f2.requests()))
	}
	r = f2.requests()[0]
	var rootSent, planSent string
	var rootTrunc, planTrunc bool
	mustField(t, r.Body.State, "root_intent", &rootSent)
	mustField(t, r.Body.State, "plan", &planSent)
	mustField(t, r.Body.State, "root_intent_truncated", &rootTrunc)
	mustField(t, r.Body.State, "plan_truncated", &planTrunc)
	if !rootTrunc || !planTrunc {
		t.Errorf("truncation flags = %v, %v, want both true", rootTrunc, planTrunc)
	}
	if len(rootSent) > 48000 || len(rootSent) < 47990 || !utf8.ValidString(rootSent) || !strings.HasSuffix(longRoot, rootSent) {
		t.Errorf("root_intent: %d bytes, valid=%v, tail=%v; want the last <=48000 bytes on a rune boundary", len(rootSent), utf8.ValidString(rootSent), strings.HasSuffix(longRoot, rootSent))
	}
	if len(planSent) > 48000 || len(planSent) < 47990 || !utf8.ValidString(planSent) || !strings.HasPrefix(longPlan, planSent) {
		t.Errorf("plan: %d bytes, valid=%v, head=%v; want the first <=48000 bytes on a rune boundary", len(planSent), utf8.ValidString(planSent), strings.HasPrefix(longPlan, planSent))
	}
}

func TestJev_Transport(t *testing.T) {
	ctx := context.Background()
	noKey := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("no error")
		}
		if strings.Contains(err.Error(), testKey) {
			t.Errorf("the key is in the error: %v", err)
		}
	}

	t.Run("plain http to a non-loopback host is refused", func(t *testing.T) {
		for _, base := range []string{"http://api.typesafe.ai", "http://example.test:8080", "http://127.0.0.1.example.test", "ftp://127.0.0.1"} {
			_, err := Jev{BaseURL: base, Key: testKey}.Review(ctx, rootIntent, plan)
			noKey(t, err)
		}
	})

	t.Run("a redirect is not followed", func(t *testing.T) {
		var hits int
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
		defer target.Close()
		redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/v1/systemone", http.StatusTemporaryRedirect)
		}))
		defer redir.Close()
		_, err := Jev{BaseURL: redir.URL, Key: testKey}.Review(ctx, rootIntent, plan)
		noKey(t, err)
		if hits != 0 {
			t.Errorf("the redirect target was reached %d times", hits)
		}
		// A caller-supplied client gets the same rule.
		_, err = Jev{BaseURL: redir.URL, Key: testKey, HTTP: &http.Client{}}.Review(ctx, rootIntent, plan)
		noKey(t, err)
		if hits != 0 {
			t.Errorf("the redirect target was reached %d times with a custom client", hits)
		}
	})

	t.Run("non-2xx is an error with the status and error_type", func(t *testing.T) {
		for _, c := range []struct {
			status int
			etype  string
		}{{401, "unauthorized"}, {403, "forbidden_key"}, {429, "rate_limited"}, {529, "overloaded"}, {500, "internal_error"}} {
			var hits int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				w.WriteHeader(c.status)
				fmt.Fprintf(w, `{"detail":{"error_type":%q,"message":"bad key %s"}}`, c.etype, testKey)
			}))
			_, err := Jev{BaseURL: srv.URL, Key: testKey}.Review(ctx, rootIntent, plan)
			srv.Close()
			noKey(t, err)
			if !strings.Contains(err.Error(), fmt.Sprint(c.status)) || !strings.Contains(err.Error(), c.etype) {
				t.Errorf("%d: error %q lacks the status or %q", c.status, err, c.etype)
			}
			if hits != 1 {
				t.Errorf("%d: %d requests, want 1 (no retries)", c.status, hits)
			}
			if res := Run(ctx, Jev{BaseURL: srv.URL, Key: testKey}, rootIntent, plan, time.Second); res.Status == Complete {
				t.Errorf("%d: Run says complete", c.status)
			}
		}
	})

	t.Run("a non-JSON error body still names the status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "<html>bad gateway "+testKey+"</html>", http.StatusBadGateway)
		}))
		defer srv.Close()
		_, err := Jev{BaseURL: srv.URL, Key: testKey}.Review(ctx, rootIntent, plan)
		noKey(t, err)
		if !strings.Contains(err.Error(), "502") {
			t.Errorf("error %q lacks the status", err)
		}
	})

	t.Run("a 2 MB body is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"model":"x","answers":{},"pad":"`)
			fmt.Fprint(w, strings.Repeat("a", 2<<20))
			fmt.Fprint(w, `"}`)
		}))
		defer srv.Close()
		_, err := Jev{BaseURL: srv.URL, Key: testKey}.Review(ctx, rootIntent, plan)
		noKey(t, err)
	})

	t.Run("a cancelled context returns promptly with ctx.Err()", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		defer srv.Close()
		defer close(release)

		cctx, cancel := context.WithCancel(ctx)
		time.AfterFunc(50*time.Millisecond, cancel)
		start := time.Now()
		_, err := Jev{BaseURL: srv.URL, Key: testKey}.Review(cctx, rootIntent, plan)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("returned after %v", d)
		}

		dctx, dcancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer dcancel()
		start = time.Now()
		_, err = Jev{BaseURL: srv.URL, Key: testKey}.Review(dctx, rootIntent, plan)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want context.DeadlineExceeded", err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("returned after %v", d)
		}
	})
}

// rawJev serves body verbatim with the given status.
func rawJev(t *testing.T, status int, body string) Jev {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return Jev{BaseURL: srv.URL, Key: testKey, Model: "jev-test"}
}

// Review 1 of S-12: each answer is decoded on its own.
func TestJev_PerAnswerDecode(t *testing.T) {
	ctx := context.Background()
	j := rawJev(t, 200, `{"answers":{"omit_0":{"noul":"0.1"},"omit_1":{"noul":0.05},"omit_2":"oops","weak_0":[1],"weak_1":{"type":"noul","noul":null},"add_0":{"noul":{"x":1}}}}`)
	got, err := j.Review(ctx, rootIntent, plan)
	if err != nil {
		t.Fatalf("a wrong-typed answer failed the review: %v", err)
	}
	if len(got) != 1 || got[0].Kind != Omission || got[0].Quote != "Keep the report under one page." || math.Abs(got[0].Confidence-0.95) > 1e-9 {
		t.Errorf("findings = %+v, want exactly the omission of %q at 0.95", got, "Keep the report under one page.")
	}
	// Only a body that is not an object with an answers object is an error.
	for name, body := range map[string]string{
		"not JSON":             `<html>`,
		"an array":             `[]`,
		"answers is an array":  `{"answers":[]}`,
		"answers is a string":  `{"answers":"none"}`,
		"answers is missing":   `{"model":"x"}`,
		"answers is null":      `{"answers":null}`,
		"an empty object body": `{}`,
	} {
		if _, err := rawJev(t, 200, body).Review(ctx, rootIntent, plan); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if got, err := rawJev(t, 200, `{"answers":{}}`).Review(ctx, rootIntent, plan); err != nil || len(got) != 0 {
		t.Errorf("an empty answers object: got %+v, %v; want no findings and no error", got, err)
	}
}

// Review 1 of S-12: the HTTP backstop (10 s in production, shortened here) is on a copy of a
// caller's zero-Timeout client, and a client timeout reads as a deadline, never as Go's text.
func TestJev_Backstop(t *testing.T) {
	if backstop != 10*time.Second {
		t.Errorf("backstop = %v, want 10s", backstop)
	}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	caller := &http.Client{}
	j := Jev{BaseURL: srv.URL, Key: testKey, HTTP: caller, backstopFor: 150 * time.Millisecond}
	// The caller's context has a far deadline, so only the backstop can end this in time.
	far, farCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer farCancel()
	start := time.Now()
	_, err := j.Review(far, rootIntent, plan)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the backstop did not fire: returned after %v", d)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	if err != nil && (strings.Contains(err.Error(), "Client.Timeout") || strings.Contains(err.Error(), "net/http")) {
		t.Errorf("Go's transport text leaks: %v", err)
	}
	if caller.Timeout != 0 {
		t.Errorf("the caller's client was modified: Timeout = %v", caller.Timeout)
	}

	// Through Run: a hung server is partial/unavailable, never error, and Display has no Go text.
	res := Run(context.Background(), j, rootIntent, plan, 2*time.Second)
	if out := Display(res); strings.Contains(out, "Client.Timeout") || strings.Contains(out, "net/http") {
		t.Errorf("backstop: Display has Go transport text: %q", out)
	}
	if res.Status != Unavail && res.Status != Partial {
		t.Errorf("backstop: Run status = %q (%q), want unavailable or partial", res.Status, res.Err)
	}
	// Under the caller's deadline (500 ms budget) the deadline fires first.
	hung := Jev{BaseURL: srv.URL, Key: testKey, HTTP: &http.Client{}}
	cctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start = time.Now()
	if _, err := hung.Review(cctx, rootIntent, plan); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("a 500 ms deadline returned after %v", d)
	}
	start = time.Now()
	res = Run(context.Background(), hung, rootIntent, plan, 500*time.Millisecond)
	if res.Status != Unavail && res.Status != Partial {
		t.Errorf("deadline: Run status = %q (%q), want unavailable or partial", res.Status, res.Err)
	}
	if out := Display(res); strings.Contains(out, "Client.Timeout") || strings.Contains(out, "net/http") || strings.Contains(out, "Post ") {
		t.Errorf("Display has Go transport text: %q", out)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Run returned after %v, want within its 500 ms budget", d)
	}
}

// Review 1 of S-12: a base URL with userinfo, a query or a fragment is refused, and no error
// holds the userinfo.
func TestJev_BaseURLHygiene(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	for _, base := range []string{
		"https://user:pw@127.0.0.1",
		"https://user:pw@localhost:1",
		"http://user:pw@127.0.0.1:9",
		"https://user:pw@127.0.0.1:notaport",
		"https://:pw@127.0.0.1",
		"https://127.0.0.1:1?key=1",
		"https://127.0.0.1:1/?",
		"https://127.0.0.1:1#frag",
		"https://127.0.0.1:1/#",
		srv.URL + "?a=b",
		srv.URL + "#x",
	} {
		_, err := Jev{BaseURL: base, Key: testKey}.Review(context.Background(), rootIntent, plan)
		if err == nil {
			t.Errorf("%q: no error", base)
			continue
		}
		if strings.Contains(err.Error(), "pw") || strings.Contains(err.Error(), "user:") {
			t.Errorf("%q: the error holds the userinfo: %v", base, err)
		}
		if strings.Contains(err.Error(), testKey) {
			t.Errorf("%q: the key is in the error", base)
		}
	}
	if hits != 0 {
		t.Errorf("%d requests were sent to a refused base URL", hits)
	}
}

// Review 1 of S-12: the 1 MB limit comes before the status.
func TestJev_BodyLimitBeforeStatus(t *testing.T) {
	big := `{"detail":{"error_type":"internal_error"},"pad":"` + strings.Repeat("a", 2<<20) + `"}`
	_, err := rawJev(t, 500, big).Review(context.Background(), rootIntent, plan)
	if err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("over %d bytes", maxRespBytes)) {
		t.Errorf("error = %q, want the body-limit error", err)
	}
	// A small error body is still decoded for its error_type.
	_, err = rawJev(t, 500, `{"detail":{"error_type":"internal_error"}}`).Review(context.Background(), rootIntent, plan)
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "internal_error") {
		t.Errorf("error = %v, want the status and error_type", err)
	}
}

// Review 1 of S-12: printing a Jev, with any verb, never shows its Key.
func TestJev_NeverPrintsKey(t *testing.T) {
	j := Jev{BaseURL: "https://api.typesafe.ai", Key: testKey, Model: "jev-test"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		for name, arg := range map[string]any{"value": j, "pointer": &j} {
			if out := fmt.Sprintf(verb, arg); strings.Contains(out, testKey) {
				t.Errorf("%s of a Jev %s prints its key: %q", verb, name, out)
			}
		}
	}
}

func TestItems(t *testing.T) {
	cases := []struct {
		name, in string
		max      int
		want     []string
	}{
		{"dash, star and plus bullets", "- one two three\n* four five six\n+ seven eight nine\n", 0,
			[]string{"one two three", "four five six", "seven eight nine"}},
		{"numbered lines", "1. alpha beta gamma\n2) delta epsilon zeta\n10. eta theta iota", 0,
			[]string{"alpha beta gamma", "delta epsilon zeta", "eta theta iota"}},
		{"sentences", "Add the flag. Run the tests! Does it work? yes", 0,
			[]string{"Add the flag.", "Run the tests!", "Does it work?"}},
		{"a dot not followed by a space does not split", "Ship version 1.2 to everyone. Then stop.", 0,
			[]string{"Ship version 1.2 to everyone."}},
		{"a bullet is one item even with several sentences", "- First part here. Second part here.", 0,
			[]string{"First part here. Second part here."}},
		{"a marker needs a space after it", "**bold** words here\n-dashed words here", 0,
			[]string{"**bold** words here", "-dashed words here"}},
		{"under three words are dropped", "- ok\n- two words\n- this has three\n1. no\nDone.", 0,
			[]string{"this has three"}},
		{"white space is collapsed", "-   lots   of\tspace here  ", 0, []string{"lots of space here"}},
		{"blank lines and CRLF", "\r\n- first item here\r\n\r\n- second item here\r\n", 0,
			[]string{"first item here", "second item here"}},
		{"duplicates keep the last occurrence", "- alpha beta gamma\n- delta epsilon zeta\n- alpha beta gamma\n", 0,
			[]string{"delta epsilon zeta", "alpha beta gamma"}},
		{"empty", "", 0, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Items(c.in, c.max)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Items(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
			}
		})
	}

	var b strings.Builder
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&b, "%d. item number %02d here\n", i+1, i)
	}
	t.Run("a cap keeps the last items", func(t *testing.T) {
		got := Items(b.String(), 20)
		if len(got) != 20 || got[0] != "item number 05 here" || got[19] != "item number 24 here" {
			t.Errorf("got %d items %q ... %q, want the last 20", len(got), first(got), last(got))
		}
	})
	t.Run("no cap keeps all", func(t *testing.T) {
		if got := Items(b.String(), 0); len(got) != 25 {
			t.Errorf("got %d items, want 25", len(got))
		}
	})
	t.Run("deterministic", func(t *testing.T) {
		if !reflect.DeepEqual(Items(b.String(), 7), Items(b.String(), 7)) {
			t.Error("Items is not pure")
		}
	})
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

func last(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}

func TestSelect(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	unavailable := func(t *testing.T, name string, r Reviewer) {
		t.Helper()
		if _, err := r.Review(context.Background(), "a b c", "d e f"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: Review err = %v, want ErrUnavailable (%T)", name, err, r)
		}
	}

	unavailable(t, "no environment", Select(env(nil)))
	unavailable(t, "nil getenv", Select(nil))
	unavailable(t, "off without a key", Select(env(map[string]string{"INTENTGATE_PLAN_REVIEW": "off"})))
	unavailable(t, "off wins over a key", Select(env(map[string]string{"INTENTGATE_PLAN_REVIEW": "off", "TYPESAFE_API_KEY": testKey})))

	j, ok := Select(env(map[string]string{"TYPESAFE_API_KEY": testKey})).(Jev)
	if !ok {
		t.Fatalf("a key alone selects %T, want Jev", Select(env(map[string]string{"TYPESAFE_API_KEY": testKey})))
	}
	if j.Key != testKey || j.BaseURL != "https://api.typesafe.ai" || j.Model != "jev-latest" {
		t.Errorf("defaults: base %q model %q (key ok: %v), want https://api.typesafe.ai and jev-latest", j.BaseURL, j.Model, j.Key == testKey)
	}

	j, ok = Select(env(map[string]string{"TYPESAFE_API_KEY": testKey, "TYPESAFE_BASE_URL": "http://127.0.0.1:9", "TYPESAFE_MODEL": "jev-1.14.0"})).(Jev)
	if !ok || j.BaseURL != "http://127.0.0.1:9" || j.Model != "jev-1.14.0" {
		t.Errorf("custom: base %q model %q, want base http://127.0.0.1:9 and model jev-1.14.0", j.BaseURL, j.Model)
	}
	// Any other value of INTENTGATE_PLAN_REVIEW leaves a key in charge.
	if _, ok := Select(env(map[string]string{"INTENTGATE_PLAN_REVIEW": "on", "TYPESAFE_API_KEY": testKey})).(Jev); !ok {
		t.Error("INTENTGATE_PLAN_REVIEW=on with a key does not select Jev")
	}
}

// TestJev_Live is the one call to the real service. It is not part of the gate: it needs
// INTENTGATE_LIVE_JEV=1 and TYPESAFE_API_KEY (exported by the user; intentgate never reads .env).
func TestJev_Live(t *testing.T) {
	if os.Getenv("INTENTGATE_LIVE_JEV") != "1" || os.Getenv("TYPESAFE_API_KEY") == "" {
		t.Skip("set INTENTGATE_LIVE_JEV=1 and TYPESAFE_API_KEY to run the live check")
	}
	j, ok := Select(os.Getenv).(Jev)
	if !ok {
		t.Fatalf("Select returned %T with a key set", Select(os.Getenv))
	}
	cases := []struct{ name, root, plan string }{
		{"clean plan",
			"Add a --json flag to the status command.\nWrite tests for it.",
			"1. Add a --json flag to the status command.\n2. Write tests for the --json flag."},
		{"clear omission",
			"- Add a --json flag to the status command.\n- Add a --quiet flag to the status command.",
			"1. Add a --json flag to the status command."},
		{"clear addition",
			"Add a --json flag to the status command.",
			"1. Add a --json flag to the status command.\n2. Rewrite the whole project in Rust.\n3. Replace the CI provider."},
		{"clear weakening",
			"Every request must be authenticated. Reject anonymous calls with a 401.",
			"1. Authentication is optional on every request.\n2. Anonymous calls are allowed through."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := time.Now()
			fs, err := j.Review(ctx, c.root, c.plan)
			d := time.Since(start)
			if err != nil {
				t.Fatalf("Review after %v: %v", d, err)
			}
			if d > 5*time.Second {
				t.Errorf("took %v", d)
			}
			t.Logf("%s: %d findings in %v", c.name, len(fs), d.Round(time.Millisecond))
			for _, f := range fs {
				t.Logf("  %s (%.2f): %q", f.Kind, f.Confidence, f.Quote)
			}
		})
	}
}

// REQ-0016, REQ-0017, REQ-0018: a dropped or weakened requirement is one finding, not two (S-27).
func TestJev_DedupeCrossKind(t *testing.T) {
	f := newFakeJev(t, func([]string) map[string]any {
		return map[string]any{
			"omit_0": 0.05, // omission 0.95 ...
			"weak_0": 0.88, // ... and weakening 0.88 on r0: omission wins
			"omit_1": 0.09, // omission 0.91 ...
			"weak_1": 0.99, // ... and weakening 0.99 on r1: weakening wins
			"omit_2": 0.10, // omission 0.90 ...
			"weak_2": 0.90, // ... and weakening 0.90 on r2: the tie goes to omission
			"add_1":  0.05, // addition 0.95 on a plan item: untouched
		}
	})
	want := map[string]Finding{
		"Add SARIF output to the report command.": {Kind: Omission, Confidence: 0.95},
		"Keep the report under one page.":         {Kind: Weakening, Confidence: 0.99},
		"Never block a merge ever.":               {Kind: Omission, Confidence: 0.90},
		"Rewrite the whole CLI in Rust.":          {Kind: Addition, Confidence: 0.95},
	}
	wantCounts := Counts{Omissions: 2, Additions: 1, Weakenings: 1}
	verify := func(t *testing.T, got []Finding, label string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: findings = %+v, want exactly %d", label, got, len(want))
		}
		for _, g := range got {
			w, ok := want[g.Quote]
			if !ok {
				t.Errorf("%s: unexpected finding %+v", label, g)
				continue
			}
			if g.Kind != w.Kind || math.Abs(g.Confidence-w.Confidence) > 1e-9 {
				t.Errorf("%s: %q = %s %v, want %s %v", label, g.Quote, g.Kind, g.Confidence, w.Kind, w.Confidence)
			}
		}
	}
	got, err := f.jev().Review(context.Background(), rootIntent, plan)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	verify(t, got, "Review")
	res := Run(context.Background(), f.jev(), rootIntent, plan, 2*time.Second)
	if res.Status != Complete {
		t.Fatalf("Run status = %q, want complete", res.Status)
	}
	verify(t, res.Findings, "Run")
	if res.Counts != wantCounts {
		t.Errorf("Run counts = %+v, want %+v", res.Counts, wantCounts)
	}
}

// REQ-0016, REQ-0017, REQ-0018: per-kind calibrated noul thresholds strictly gate each finding family (S-29).
func TestJev_PerKindThresholds(t *testing.T) {
	for name, v := range map[string]float64{"omitMax": omitMax, "weakMin": weakMin, "addMax": addMax} {
		if !(v > 0 && v < 1) {
			t.Fatalf("%s = %v, want strictly in (0, 1)", name, v)
		}
	}
	const eps = 0.001
	f := newFakeJev(t, func([]string) map[string]any {
		return map[string]any{
			"omit_0": omitMax - eps,
			"omit_1": omitMax,
			"weak_0": weakMin,
			"weak_1": weakMin + eps,
			"add_0":  addMax - eps,
			"add_1":  addMax,
		}
	})
	got, err := f.jev().Review(context.Background(), rootIntent, plan)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	want := map[Kind]string{
		Omission:  "Add SARIF output to the report command.",
		Weakening: "Keep the report under one page.",
		Addition:  "Add a SARIF writer to the report package.",
	}
	if len(got) != len(want) {
		t.Fatalf("findings = %+v, want %d (one per kind)", got, len(want))
	}
	for _, g := range got {
		if g.Quote != want[g.Kind] {
			t.Errorf("%s quote = %q, want %q", g.Kind, g.Quote, want[g.Kind])
		}
	}
}
