package report

import (
	"strings"

	"intentgate/internal/footprint"
	"intentgate/internal/verify"
)

// RuleID is the SARIF rule of a footprint finding (REQ-0036).
const RuleID = "intentgate/outside-footprint"

type sarifDoc struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	Rules   []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string    `json:"id"`
	ShortDescription sarifText `json:"shortDescription"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifText       `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

type sarifLocation struct {
	PhysicalLocation struct {
		ArtifactLocation struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
	} `json:"physicalLocation"`
}

// SARIF renders the footprint findings as SARIF 2.1.0 (REQ-0036): one run, one result per finding
// in path order, located at the file with no region. version is IntentGate's version, supplied by
// the caller.
func SARIF(r verify.Result, version string) []byte {
	results := make([]sarifResult, 0, len(r.Findings))
	for _, f := range sortedFindings(r.Findings) {
		var loc sarifLocation
		loc.PhysicalLocation.ArtifactLocation.URI = f.Path
		results = append(results, sarifResult{
			RuleID:    RuleID,
			Level:     "warning",
			Message:   sarifText{Text: sarifMessage(f)},
			Locations: []sarifLocation{loc},
		})
	}
	return marshal(sarifDoc{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:    "IntentGate",
				Version: version,
				Rules: []sarifRule{{
					ID:               RuleID,
					ShortDescription: sarifText{Text: "A changed file is outside every criterion's footprint"},
				}},
			}},
			Results: results,
		}},
	})
}

func sarifMessage(f footprint.Finding) string {
	var b strings.Builder
	b.WriteString(f.Path + " is outside every criterion's footprint")
	if f.NearestCriterion != "" {
		b.WriteString(" (nearest criterion: " + f.NearestCriterion + " of lock " + f.LockID + ")")
	}
	if f.Fix != "" {
		b.WriteString(". If the change is intended, run: " + f.Fix)
	}
	return b.String()
}
