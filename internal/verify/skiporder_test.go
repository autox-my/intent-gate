package verify_test

import (
	"testing"

	"github.com/autox-my/intent-gate/internal/verify"
)

// Review 1 of S-07, skip order: the author type and the default bot logins are checked first;
// skip_authors counts only when the config parses. A broken config stays visible even for a login
// it lists.
func TestRun_SkipOrder(t *testing.T) {
	broken := map[string]string{
		"wrong version": "version: 3\nskip_authors:\n  - octocat\n",
		"unknown key":   "version: 1\nskip_authors:\n  - octocat\nnope: 1\n",
		"bad glob":      "version: 1\nskip_authors:\n  - octocat\nimplicit_footprints:\n  add:\n    - \"[\"\n",
	}
	for name, cfg := range broken {
		t.Run("skip-listed login, config "+name, func(t *testing.T) {
			in := base(t)
			in.Author, in.AuthorType, in.Config = "octocat", "User", []byte(cfg)
			r := verify.Run(in)
			wantOutcome(t, r, verify.Unverifiable, 3)
			if len(r.Problems) == 0 {
				t.Error("the config problem must be listed")
			}
		})
		t.Run("Bot type, config "+name, func(t *testing.T) {
			in := base(t)
			in.Author, in.AuthorType, in.Config = "octocat", "Bot", []byte(cfg)
			wantOutcome(t, verify.Run(in), verify.Skipped, 0)
		})
		t.Run("default bot login, config "+name, func(t *testing.T) {
			in := base(t)
			in.Author, in.AuthorType, in.Config = "renovate[bot]", "User", []byte(cfg)
			wantOutcome(t, verify.Run(in), verify.Skipped, 0)
		})
	}

	t.Run("skip-listed login, valid config", func(t *testing.T) {
		in := base(t)
		in.Author, in.Config = "octocat", []byte("version: 1\nskip_authors:\n  - octocat\n")
		wantOutcome(t, verify.Run(in), verify.Skipped, 0)
	})
}
