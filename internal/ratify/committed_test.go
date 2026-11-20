package ratify

import (
	"errors"
	"strings"
	"testing"

	"github.com/autox-my/intent-gate/internal/lock"
)

// S-22 support (intent-lock.md §6): not linked to a claim.

const committedText = "# Checkout v2\n\nOne-page checkout.\n"

func committedInput() BuildInput {
	in := testInput()
	in.Committed = &CommittedIntent{Path: "intents/checkout-v2.md", Blob: strings.Repeat("3f", 20)}
	return in
}

func TestMaskAndBuild_CommittedIntent(t *testing.T) {
	t.Run("the lock records the path and blob, hashes the bytes and has no redactions", func(t *testing.T) {
		l, m, err := MaskAndBuild(func(string) ([]Finding, error) { return nil, nil }, committedText, committedInput())
		if err != nil {
			t.Fatal(err)
		}
		ri := l.RootIntent
		if ri.Source != lock.SourceCommittedIntent || ri.Path != "intents/checkout-v2.md" || ri.Blob != strings.Repeat("3f", 20) ||
			ri.SHA256 != sha(committedText) || ri.Redactions == nil || *ri.Redactions != 0 || m.String() != committedText {
			t.Errorf("root_intent = %+v, masked %q", ri, m.String())
		}
	})
	t.Run("a finding is ErrSecretInIntent and builds nothing", func(t *testing.T) {
		det := func(text string) ([]Finding, error) { return []Finding{{RuleID: "r", Secret: "One-page"}}, nil }
		l, m, err := MaskAndBuild(det, committedText, committedInput())
		if !errors.Is(err, ErrSecretInIntent) || l.ID != "" || m.String() != "" {
			t.Errorf("got %+v, %q, %v", l, m.String(), err)
		}
	})
	t.Run("a detector error is a scan failure, not a finding", func(t *testing.T) {
		det := func(string) ([]Finding, error) { return nil, errors.New("boom") }
		_, _, err := MaskAndBuild(det, committedText, committedInput())
		if err == nil || errors.Is(err, ErrSecretInIntent) || !strings.Contains(err.Error(), "secret scan failed") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a malformed path or blob is refused by the lock's validation", func(t *testing.T) {
		for _, c := range []*CommittedIntent{{Path: "x.md", Blob: strings.Repeat("3f", 20)}, {Path: "intents/x.md", Blob: "abc"}, {}} {
			in := committedInput()
			in.Committed = c
			if _, _, err := MaskAndBuild(func(string) ([]Finding, error) { return nil, nil }, committedText, in); err == nil {
				t.Errorf("%+v accepted", c)
			}
		}
	})
}

func TestStore_CommittedIntentHasNoRecord(t *testing.T) {
	repo, state := emptyDirs(t)
	in := committedInput()
	in.RootIntent = masked(committedText)
	l, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := (Store{RepoRoot: repo, StateDir: state}).Write(l, masked(committedText)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !isEmpty(t, state) {
		t.Error("a state directory entry was written for a committed intent")
	}
	if err := (Store{RepoRoot: t.TempDir(), StateDir: state}).Write(l, masked("other text")); err == nil {
		t.Error("Write accepted text that does not match root_intent.sha256")
	}

	t.Run("Amend refuses a changed path or blob", func(t *testing.T) {
		next := l
		next.Revision = 2
		next.Amendments = []lock.Amendment{{Revision: 2, Kind: lock.KindReratify, By: "git:dev@example.com", At: "2026-11-18T10:00:00Z", Method: "cli-confirm"}}
		next.RootIntent.Blob = strings.Repeat("4a", 20)
		h, _ := lock.Hash(next)
		next.Hash = h
		if err := (Store{RepoRoot: repo, StateDir: state}).Amend(l, next); err == nil || !strings.Contains(err.Error(), "root_intent changed") {
			t.Errorf("Amend with another blob: %v", err)
		}
	})
}
