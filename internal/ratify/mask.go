package ratify

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/zricethezav/gitleaks/v8/config"
	"github.com/zricethezav/gitleaks/v8/detect"
)

// Scanner masks the secrets in a root intent and reports how many it replaced. Mask is the
// production scanner; MaskAndBuild takes one as an argument so that a scanner failure can be tested.
type Scanner func(text string) (masked string, redactions int, err error)

var (
	cfgOnce sync.Once
	cfg     config.Config
	cfgErr  error
)

// defaultConfig parses gitleaks' embedded default config once. It goes through gitleaks'
// NewDetectorDefaultConfig, which uses global viper state, so it must not run concurrently.
func defaultConfig() (config.Config, error) {
	cfgOnce.Do(func() {
		d, err := detect.NewDetectorDefaultConfig()
		if err != nil {
			cfgErr = err
			return
		}
		cfg = d.Config
	})
	return cfg, cfgErr
}

// Mask scans text with gitleaks' detection library (default config) and replaces each finding's
// secret with [REDACTED:<rule-id>]. It returns the masked text and the number of replacements.
// The result is deterministic: findings are applied longest secret first, ties by rule id.
func Mask(text string) (string, int, error) {
	c, err := defaultConfig()
	if err != nil {
		return "", 0, fmt.Errorf("secret scan: %w", err)
	}
	findings := detect.NewDetector(c).DetectString(text)

	type hit struct{ secret, rule string }
	seen := map[hit]bool{}
	var hits []hit
	for _, f := range findings {
		h := hit{f.Secret, f.RuleID}
		if f.Secret == "" || seen[h] {
			continue
		}
		seen[h] = true
		hits = append(hits, h)
	}
	sort.Slice(hits, func(i, j int) bool {
		if len(hits[i].secret) != len(hits[j].secret) {
			return len(hits[i].secret) > len(hits[j].secret)
		}
		if hits[i].rule != hits[j].rule {
			return hits[i].rule < hits[j].rule
		}
		return hits[i].secret < hits[j].secret
	})

	n := 0
	for _, h := range hits {
		c := strings.Count(text, h.secret)
		if c == 0 {
			continue // already inside an earlier replacement
		}
		n += c
		text = strings.ReplaceAll(text, h.secret, "[REDACTED:"+h.rule+"]")
	}
	return text, n, nil
}
