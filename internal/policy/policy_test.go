package policy_test

import (
	"testing"

	"intentgate/internal/footprint"
	"intentgate/internal/policy"
	"intentgate/internal/verify"
)

var allOutcomes = []verify.Outcome{verify.Skipped, verify.NoLock, verify.Clean, verify.Findings, verify.Unverifiable}

// REQ-0033
func TestDecide_NoLockNoComment(t *testing.T) {
	d := policy.Decide(verify.Result{Outcome: verify.NoLock})
	if !d.Publish || d.Conclusion != "neutral" || d.Comment != policy.None || d.Title != "No intent lock in this pull request" {
		t.Errorf("decision = %+v, want published, neutral, no comment, title %q", d, "No intent lock in this pull request")
	}
}

// REQ-0038
func TestDecide_CleanNoNewComment(t *testing.T) {
	d := policy.Decide(verify.Result{Outcome: verify.Clean})
	if !d.Publish || d.Conclusion != "success" {
		t.Errorf("decision = %+v, want published success", d)
	}
	if d.Comment != policy.UpdateIfExists {
		t.Errorf("comment = %q, want %q: a clean result never creates a comment", d.Comment, policy.UpdateIfExists)
	}
	if d.Title != "All criteria inside their footprints" {
		t.Errorf("title = %q", d.Title)
	}
}

// REQ-0039
func TestDecide_AdvisoryConclusions(t *testing.T) {
	for _, o := range allOutcomes {
		t.Run(string(o), func(t *testing.T) {
			d := policy.Decide(verify.Result{Outcome: o, Findings: []footprint.Finding{{Path: "x"}}})
			if d.Conclusion != "success" && d.Conclusion != "neutral" {
				t.Errorf("conclusion = %q, want success or neutral", d.Conclusion)
			}
			if d.Publish != (o != verify.Skipped) {
				t.Errorf("publish = %v for %s", d.Publish, o)
			}
			if o != verify.Clean && d.Conclusion != "neutral" {
				t.Errorf("only a clean result is success; %s gave %q", o, d.Conclusion)
			}
		})
	}
	t.Run("findings and unverifiable upsert a comment", func(t *testing.T) {
		for _, o := range []verify.Outcome{verify.Findings, verify.Unverifiable} {
			if d := policy.Decide(verify.Result{Outcome: o}); d.Comment != policy.Upsert || d.Title == "" {
				t.Errorf("%s: decision = %+v, want upsert with a title", o, d)
			}
		}
	})
	t.Run("skipped publishes nothing", func(t *testing.T) {
		if d := policy.Decide(verify.Result{Outcome: verify.Skipped}); d.Publish || d.Comment != policy.None {
			t.Errorf("decision = %+v", d)
		}
	})
	t.Run("an unknown outcome is still advisory", func(t *testing.T) {
		if d := policy.Decide(verify.Result{Outcome: "bogus"}); d.Conclusion != "neutral" {
			t.Errorf("conclusion = %q", d.Conclusion)
		}
	})
}
