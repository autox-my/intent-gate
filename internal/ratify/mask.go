package ratify

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/zricethezav/gitleaks/v8/config"
	"github.com/zricethezav/gitleaks/v8/detect"
)

// Masked is root intent text that has been through the secret scan. Only MaskWith (and so Mask and
// MaskAndBuild) can make a non-zero one, so Build and Store.Write, which take a Masked, can't be
// handed unscanned text by construction.
type Masked struct{ text string }

// String is the masked text.
func (m Masked) String() string { return m.text }

// Finding is one secret a Detector found: the secret as it appears in the text, and the id of the
// rule that matched it.
type Finding struct {
	RuleID string
	Secret string
}

// Detector finds the secrets in a text. Mask uses gitleaks'; MaskAndBuild takes one as an argument
// so that a detector failure, and findings gitleaks wouldn't produce, can be tested.
type Detector func(text string) ([]Finding, error)

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

// gitleaksDetector is the production Detector: gitleaks' detection library with its default config.
func gitleaksDetector(text string) ([]Finding, error) {
	c, err := defaultConfig()
	if err != nil {
		return nil, fmt.Errorf("secret scan: %w", err)
	}
	var out []Finding
	for _, f := range detect.NewDetector(c).DetectString(text) {
		out = append(out, Finding{RuleID: f.RuleID, Secret: f.Secret})
	}
	return out, nil
}

// Mask scans text with gitleaks' detection library (default config) and replaces each finding's
// secret with [REDACTED:<rule-id>]. It returns the masked text and the number of replacements.
// The result is deterministic: findings are applied longest secret first, ties by rule id.
func Mask(text string) (Masked, int, error) { return MaskWith(gitleaksDetector, text) }

// MaskWith is Mask with a given Detector.
func MaskWith(d Detector, text string) (Masked, int, error) {
	findings, err := d(text)
	if err != nil {
		return Masked{}, 0, err
	}
	hits := findings[:0:0]
	seen := map[Finding]bool{}
	for _, f := range findings {
		if f.Secret == "" || seen[f] {
			continue
		}
		seen[f] = true
		hits = append(hits, f)
	}
	sort.Slice(hits, func(i, j int) bool {
		if len(hits[i].Secret) != len(hits[j].Secret) {
			return len(hits[i].Secret) > len(hits[j].Secret)
		}
		if hits[i].RuleID != hits[j].RuleID {
			return hits[i].RuleID < hits[j].RuleID
		}
		return hits[i].Secret < hits[j].Secret
	})

	n := 0
	for _, h := range hits {
		c := strings.Count(text, h.Secret)
		if c == 0 {
			continue // already inside an earlier replacement
		}
		n += c
		text = strings.ReplaceAll(text, h.Secret, "[REDACTED:"+h.RuleID+"]")
	}
	return Masked{text: text}, n, nil
}
