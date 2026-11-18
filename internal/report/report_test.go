package report_test

import (
	"encoding/json"
	"strings"
	"testing"

	"intentgate/internal/footprint"
	"intentgate/internal/policy"
	"intentgate/internal/report"
	"intentgate/internal/verify"
)

const lockID = "01JD8Z6Q4M7R2T9X3B5N8K1C6F"

func twoFindings() verify.Result {
	return verify.Result{
		Outcome:  verify.Findings,
		ExitCode: 1,
		Findings: []footprint.Finding{
			// deliberately not in path order: the report sorts
			{Path: "internal/gitx/diff.go", Status: footprint.Modified, NearestCriterion: "C2", LockID: lockID,
				Fix: "intentgate amend " + lockID + " --criterion C2 --add 'internal/gitx/**'"},
			{Path: "cmd/main.go", Status: footprint.Added, NearestCriterion: "C1", LockID: lockID,
				Fix: "intentgate amend " + lockID + " --criterion C1 --add 'cmd/**'"},
		},
	}
}

// REQ-0036
func TestSARIF(t *testing.T) {
	got := string(report.SARIF(twoFindings(), "1.2.3"))
	want := `{
  "$schema": "https://json.schemastore.org/sarif-2.1.0.json",
  "version": "2.1.0",
  "runs": [
    {
      "tool": {
        "driver": {
          "name": "IntentGate",
          "version": "1.2.3",
          "rules": [
            {
              "id": "intentgate/outside-footprint",
              "shortDescription": {
                "text": "A changed file is outside every criterion's footprint"
              }
            }
          ]
        }
      },
      "results": [
        {
          "ruleId": "intentgate/outside-footprint",
          "level": "warning",
          "message": {
            "text": "cmd/main.go is outside every criterion's footprint (nearest criterion: C1 of lock ` + lockID + `). If the change is intended, run: intentgate amend ` + lockID + ` --criterion C1 --add 'cmd/**'"
          },
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {
                  "uri": "cmd/main.go"
                }
              }
            }
          ]
        },
        {
          "ruleId": "intentgate/outside-footprint",
          "level": "warning",
          "message": {
            "text": "internal/gitx/diff.go is outside every criterion's footprint (nearest criterion: C2 of lock ` + lockID + `). If the change is intended, run: intentgate amend ` + lockID + ` --criterion C2 --add 'internal/gitx/**'"
          },
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": {
                  "uri": "internal/gitx/diff.go"
                }
              }
            }
          ]
        }
      ]
    }
  ]
}
`
	if got != want {
		t.Fatalf("SARIF differs.\n got:\n%s\nwant:\n%s", got, want)
	}

	// the shape of SARIF 2.1.0 that code scanning needs
	var doc struct {
		Schema  string `json:"$schema"`
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name, Version string
					Rules         []struct{ ID string }
				}
			}
			Results []struct {
				RuleID    string `json:"ruleId"`
				Level     string
				Locations []struct {
					PhysicalLocation map[string]json.RawMessage `json:"physicalLocation"`
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("SARIF is not JSON: %v", err)
	}
	if doc.Version != "2.1.0" || !strings.Contains(doc.Schema, "sarif-2.1.0") || len(doc.Runs) != 1 {
		t.Fatalf("version %q schema %q runs %d", doc.Version, doc.Schema, len(doc.Runs))
	}
	run := doc.Runs[0]
	if run.Tool.Driver.Name != "IntentGate" || run.Tool.Driver.Version != "1.2.3" || len(run.Tool.Driver.Rules) != 1 || run.Tool.Driver.Rules[0].ID != "intentgate/outside-footprint" {
		t.Errorf("driver: %+v", run.Tool.Driver)
	}
	if len(run.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(run.Results))
	}
	for _, res := range run.Results {
		if res.RuleID != "intentgate/outside-footprint" || res.Level != "warning" || len(res.Locations) != 1 {
			t.Errorf("result: %+v", res)
		}
		if _, hasRegion := res.Locations[0].PhysicalLocation["region"]; hasRegion {
			t.Error("a finding has no line, so the location must have no region")
		}
	}
}

func TestSARIF_NoFindingsHasEmptyResults(t *testing.T) {
	got := string(report.SARIF(verify.Result{Outcome: verify.Clean}, "dev"))
	if !strings.Contains(got, `"results": []`) {
		t.Errorf("want an empty results array:\n%s", got)
	}
}

func TestSARIF_VersionIsAnArgument(t *testing.T) {
	a := string(report.SARIF(twoFindings(), "1.0.0"))
	b := string(report.SARIF(twoFindings(), "2.0.0"))
	if !strings.Contains(a, `"version": "1.0.0"`) || !strings.Contains(b, `"version": "2.0.0"`) || a == b {
		t.Error("the tool version must come from the argument")
	}
}

func TestComment_Marker(t *testing.T) {
	r := twoFindings()
	d := policy.Decide(r)
	c := report.Comment(r, d)
	if !strings.HasPrefix(c, "<!-- intentgate:pr-comment -->\n") {
		t.Errorf("comment must start with the marker:\n%s", c)
	}
	if !strings.Contains(c, "intentgate amend "+lockID+" --criterion C1") || !strings.Contains(c, "cmd/main.go") {
		t.Errorf("comment must list findings with their fix commands:\n%s", c)
	}

	clean := verify.Result{Outcome: verify.Clean}
	if c := report.Comment(clean, policy.Decide(clean)); !strings.HasPrefix(c, "<!-- intentgate:pr-comment -->\n") || !strings.Contains(c, "All criteria inside their footprints") {
		t.Errorf("clean comment (an update of an existing one): %q", c)
	}

	nolock := verify.Result{Outcome: verify.NoLock}
	if c := report.Comment(nolock, policy.Decide(nolock)); c != "" {
		t.Errorf("no lock means no comment, got %q", c)
	}
}

func TestComment_ListsSuspectAndProblems(t *testing.T) {
	r := verify.Result{
		Outcome: verify.Unverifiable, ExitCode: 3,
		Problems: []string{"footprint: .intentgate.yml: version must be 1, got 2"},
		Locks: []verify.LockResult{
			{Path: ".intent/" + lockID + ".yml", ID: lockID, Revision: 1, Suspect: []string{"expired", "retargeted"},
				Problems: []string{}, Criteria: []verify.Criterion{{ID: "C1", Text: "Do the thing.", Footprint: []string{"a/**"}}}},
			{Path: ".intent/bad.yml", Problems: []string{"lock: empty document"}, Suspect: []string{}, Criteria: []verify.Criterion{}},
		},
		Findings: []footprint.Finding{},
	}
	c := report.Comment(r, policy.Decide(r))
	for _, want := range []string{"expired", "retargeted", "lock: empty document", "version must be 1", ".intent/bad.yml"} {
		if !strings.Contains(c, want) {
			t.Errorf("comment lacks %q:\n%s", want, c)
		}
	}
	s := report.CheckSummary(r, policy.Decide(r))
	for _, want := range []string{"C1", "Do the thing.", "expired", "lock: empty document", "version must be 1"} {
		if !strings.Contains(s, want) {
			t.Errorf("check summary lacks %q:\n%s", want, s)
		}
	}
	if txt := report.Text(r); !strings.Contains(txt, "Do the thing.") || !strings.Contains(txt, "unverifiable") {
		t.Errorf("text lacks the document's content:\n%s", txt)
	}
}
