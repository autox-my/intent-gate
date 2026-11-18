package ratify

import (
	"reflect"
	"strings"
	"testing"

	"intentgate/internal/lock"
)

const fence = "```"

func block(info, body string) string { return fence + info + "\n" + body + fence + "\n" }

const twoCriteria = `criteria:
  - text: "Lock validation rejects more than 8 criteria"
    footprint: ["internal/lock/**"]
  - text: "Footprint findings carry an amend command"
    footprint: ["internal/footprint/**", "!internal/footprint/testdata/**"]
`

func manyCriteria(n int) string {
	var b strings.Builder
	b.WriteString("criteria:\n")
	for i := 0; i < n; i++ {
		b.WriteString("  - text: \"criterion\"\n    footprint: [\"src/**\"]\n")
	}
	return b.String()
}

// REQ-0007 (support; not linked)
func TestDraftCriteria(t *testing.T) {
	t.Run("one block gives ids C1..Cn in order", func(t *testing.T) {
		plan := "# Plan\n\nSome prose.\n\n" + block("intentgate", twoCriteria) + "\nMore prose.\n" + block("yaml", "criteria: nonsense\n")
		got, err := DraftCriteria(plan)
		if err != nil {
			t.Fatalf("DraftCriteria: %v", err)
		}
		want := []lock.Criterion{
			{ID: "C1", Text: "Lock validation rejects more than 8 criteria", Footprint: []string{"internal/lock/**"}},
			{ID: "C2", Text: "Footprint findings carry an amend command", Footprint: []string{"internal/footprint/**", "!internal/footprint/testdata/**"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})
	t.Run("eight criteria are fine", func(t *testing.T) {
		got, err := DraftCriteria(block("intentgate", manyCriteria(8)))
		if err != nil || len(got) != 8 || got[7].ID != "C8" {
			t.Fatalf("got %d criteria, err %v", len(got), err)
		}
	})
	t.Run("a four-backtick fence and a tilde fence also work", func(t *testing.T) {
		for _, plan := range []string{
			"````intentgate\n" + twoCriteria + "````\n",
			"~~~intentgate\n" + twoCriteria + "~~~\n",
			"  " + fence + "intentgate  \n" + twoCriteria + "  " + fence + "\n",
		} {
			if got, err := DraftCriteria(plan); err != nil || len(got) != 2 {
				t.Errorf("plan %q: got %d criteria, err %v", plan, len(got), err)
			}
		}
	})

	for _, tc := range []struct {
		name string
		plan string
	}{
		{"no block", "# Plan\n\nNo criteria here.\n"},
		{"a yaml block is not an intentgate block", block("yaml", twoCriteria)},
		{"an unlabelled block is not an intentgate block", block("", twoCriteria)},
		{"two blocks", block("intentgate", twoCriteria) + "\n" + block("intentgate", twoCriteria)},
		{"an intentgate block inside another fence is not a block", "````markdown\n" + block("intentgate", twoCriteria) + "````\n"},
		{"an unclosed block", fence + "intentgate\n" + twoCriteria},
		{"zero criteria", block("intentgate", "criteria: []\n")},
		{"an empty block", block("intentgate", "")},
		{"nine criteria", block("intentgate", manyCriteria(9))},
		{"unknown key on a criterion", block("intentgate", "criteria:\n  - text: x\n    footprint: [\"a/**\"]\n    owner: me\n")},
		{"unknown top-level key", block("intentgate", twoCriteria+"extra: 1\n")},
		{"missing footprint", block("intentgate", "criteria:\n  - text: x\n")},
		{"empty footprint", block("intentgate", "criteria:\n  - text: x\n    footprint: []\n")},
		{"missing text", block("intentgate", "criteria:\n  - footprint: [\"a/**\"]\n")},
		{"blank text", block("intentgate", "criteria:\n  - text: \"  \"\n    footprint: [\"a/**\"]\n")},
		{"invalid footprint glob", block("intentgate", "criteria:\n  - text: x\n    footprint: [\"src/[a-\"]\n")},
		{"a number as a footprint item", block("intentgate", "criteria:\n  - text: x\n    footprint: [1]\n")},
		{"a number as the text", block("intentgate", "criteria:\n  - text: 1\n    footprint: [\"a/**\"]\n")},
		{"a bool as the text", block("intentgate", "criteria:\n  - text: true\n    footprint: [\"a/**\"]\n")},
		{"a list as the text", block("intentgate", "criteria:\n  - text: [a]\n    footprint: [\"a/**\"]\n")},
		{"a null footprint item", block("intentgate", "criteria:\n  - text: x\n    footprint: [null]\n")},
		{"invalid YAML", block("intentgate", "criteria: [unclosed\n")},
		{"two YAML documents", block("intentgate", twoCriteria+"---\n"+twoCriteria)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DraftCriteria(tc.plan)
			if err == nil {
				t.Fatalf("want an error, got criteria %+v", got)
			}
			if !strings.HasPrefix(err.Error(), "no lock: ") {
				t.Fatalf("error %q does not start with %q", err, "no lock: ")
			}
			if got != nil {
				t.Fatalf("an error must come with no criteria, got %+v", got)
			}
		})
	}
	t.Run("more than 8 asks for a split", func(t *testing.T) {
		_, err := DraftCriteria(block("intentgate", manyCriteria(9)))
		if err == nil || !strings.Contains(err.Error(), "split") {
			t.Fatalf("error %v should ask for the plan to be split", err)
		}
	})
}
