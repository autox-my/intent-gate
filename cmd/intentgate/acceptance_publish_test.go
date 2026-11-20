package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const twoIntents = `{
  "schema": "intentgate.acceptance/1",
  "intents": [
    {
      "lock_id": "01JD8Z6Q4M7R2T9X3B5N8K1C6F",
      "path": "intents/b-two.md",
      "blob": "3f9a3f9a3f9a3f9a3f9a3f9a3f9a3f9a3f9a3f9a",
      "status": "not-accepted",
      "reason": "this version of the intent is not on the base branch",
      "pr": 0
    },
    {
      "lock_id": "01JD8Z6Q4M7R2T9X3B5N8K1C7G",
      "path": "intents/a-one.md",
      "blob": "5be15be15be15be15be15be15be15be15be15be1",
      "status": "accepted",
      "reason": "approved in #12 by its code owner @alice",
      "pr": 12
    }
  ]
}
`

const committedSection = "### Committed intents\n\n" +
	"- **not-accepted** `intents/b-two.md`: this version of the intent is not on the base branch\n" +
	"- **accepted** `intents/a-one.md`: approved in #12 by its code owner @alice ([#12](https://github.com/octo/repo/pull/12))\n"

func withAcceptance(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "acceptance.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// REQ-0045
func TestPublish_CommittedIntents(t *testing.T) {
	t.Setenv("GITHUB_SERVER_URL", "")

	t.Run("the check run summary gains the section, and no comment is created because of it", func(t *testing.T) {
		api := newPubAPI(t)
		dir := outDir(t, true, "neutral", "none", "Findings")
		withAcceptance(t, dir, twoIntents)
		mustPublish(t, dir)
		if len(api.runs) != 1 {
			t.Fatalf("check runs = %+v", api.runs)
		}
		want := "## Summary\nfor Findings\n\n" + committedSection
		if api.runs[0].Sum != want {
			t.Errorf("summary =\n%q\nwant\n%q", api.runs[0].Sum, want)
		}
		if api.runs[0].Conclusion != "neutral" || api.runs[0].Title != "Findings" {
			t.Errorf("conclusion/title changed: %+v", api.runs[0])
		}
		if len(api.comments) != 0 || api.count("POST") != 1 {
			t.Errorf("calls = %v, comments = %+v; want one POST (the check run) and no comment", api.called(), api.comments)
		}
	})

	t.Run("a comment that is posted anyway has the section too", func(t *testing.T) {
		api := newPubAPI(t)
		dir := outDir(t, true, "neutral", "upsert", "Findings")
		withAcceptance(t, dir, twoIntents)
		mustPublish(t, dir)
		if len(api.comments) != 1 || !strings.HasSuffix(api.comments[0].Body, "\n"+committedSection) {
			t.Errorf("comments = %+v, want one ending with the section", api.comments)
		}
		if !strings.HasSuffix(api.runs[0].Sum, "\n"+committedSection) {
			t.Errorf("check run summary = %q", api.runs[0].Sum)
		}
	})

	t.Run("update-if-exists with no comment on the PR still creates none", func(t *testing.T) {
		api := newPubAPI(t)
		dir := outDir(t, true, "neutral", "update-if-exists", "Findings")
		withAcceptance(t, dir, twoIntents)
		mustPublish(t, dir)
		if len(api.comments) != 0 {
			t.Errorf("comments = %+v, want none", api.comments)
		}
	})

	t.Run("the conclusion is whatever decision.json says", func(t *testing.T) {
		api := newPubAPI(t)
		dir := outDir(t, true, "success", "none", "Clean")
		withAcceptance(t, dir, twoIntents)
		mustPublish(t, dir)
		if api.runs[0].Conclusion != "success" {
			t.Errorf("conclusion = %q, want success", api.runs[0].Conclusion)
		}
	})

	t.Run("no acceptance.json, an empty list or an unreadable file: the summary is unchanged and publish exits 0", func(t *testing.T) {
		for name, content := range map[string]string{
			"absent":     "",
			"empty list": "{\n  \"schema\": \"intentgate.acceptance/1\",\n  \"intents\": []\n}\n",
			"garbage":    "not json",
			"wrong id":   strings.Replace(twoIntents, "acceptance/1", "acceptance/9", 1),
		} {
			api := newPubAPI(t)
			dir := outDir(t, true, "neutral", "none", "Findings")
			if content != "" {
				withAcceptance(t, dir, content)
			}
			mustPublish(t, dir)
			if want := "## Summary\nfor Findings\n"; api.runs[0].Sum != want {
				t.Errorf("%s: summary = %q, want %q", name, api.runs[0].Sum, want)
			}
		}
	})

	t.Run("the PR link follows GITHUB_SERVER_URL", func(t *testing.T) {
		api := newPubAPI(t)
		t.Setenv("GITHUB_SERVER_URL", "https://git.example.com")
		dir := outDir(t, true, "neutral", "none", "Findings")
		withAcceptance(t, dir, twoIntents)
		mustPublish(t, dir)
		if !strings.Contains(api.runs[0].Sum, "([#12](https://git.example.com/octo/repo/pull/12))") {
			t.Errorf("summary = %q", api.runs[0].Sum)
		}
	})
}
