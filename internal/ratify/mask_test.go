package ratify

import (
	"strings"
	"testing"
)

// A well-formed but made-up GitHub personal access token (not a real credential).
const fakeToken = "ghp_" + "R8s2Kd91LmQw7XvB3nTz5YcJ4hFg6PaE0uVi"
const fakeToken2 = "ghp_" + "Zq4Wn8Tb2Lk6Vd0Xs9Ym3Hc7Fj1Pa5Ru8Ge2"

// REQ-0015
func TestMask_RedactsSecrets(t *testing.T) {
	t.Run("a token is replaced by its rule id and counted", func(t *testing.T) {
		in := "Add a deploy step.\nuse token " + fakeToken + " for the push\n"
		got, n, err := Mask(in)
		if err != nil {
			t.Fatalf("Mask: %v", err)
		}
		want := "Add a deploy step.\nuse token [REDACTED:github-pat] for the push\n"
		if got.String() != want || n != 1 {
			t.Fatalf("got (%q, %d), want (%q, 1)", got, n, want)
		}
		if strings.Contains(got.String(), fakeToken) {
			t.Fatal("the secret survived masking")
		}
	})
	t.Run("two different secrets and a repeated one are all counted", func(t *testing.T) {
		in := fakeToken + " and " + fakeToken2 + " and " + fakeToken + "\n"
		got, n, err := Mask(in)
		if err != nil {
			t.Fatalf("Mask: %v", err)
		}
		if n != 3 || strings.Contains(got.String(), "ghp_") || strings.Count(got.String(), "[REDACTED:github-pat]") != 3 {
			t.Fatalf("got (%q, %d), want three redactions", got, n)
		}
	})
	t.Run("clean text is unchanged with 0", func(t *testing.T) {
		in := "Add SARIF output.\n\nNon-ASCII é 日本 and a trailing blank line.\n\n"
		got, n, err := Mask(in)
		if err != nil || got.String() != in || n != 0 {
			t.Fatalf("got (%q, %d, %v), want the input unchanged and 0", got, n, err)
		}
	})
	t.Run("same input twice gives the same output", func(t *testing.T) {
		in := "a " + fakeToken2 + " b " + fakeToken + " c " + fakeToken2 + "\n"
		a, na, err := Mask(in)
		if err != nil {
			t.Fatal(err)
		}
		b, nb, err := Mask(in)
		if err != nil {
			t.Fatal(err)
		}
		if a.String() != b.String() || na != nb || na == 0 {
			t.Fatalf("first (%q, %d), second (%q, %d)", a, na, b, nb)
		}
	})
}

func stub(fs ...Finding) Detector { return func(string) ([]Finding, error) { return fs, nil } }

// REQ-0015
func TestMaskWith_FindingNotInText(t *testing.T) {
	for name, fs := range map[string][]Finding{
		"a secret absent from the text":      {{RuleID: "r1", Secret: "nowhere-to-be-found"}},
		"one present and one absent":         {{RuleID: "r1", Secret: "abc"}, {RuleID: "r2", Secret: "nowhere"}},
		"an empty secret cannot be located":  {{RuleID: "r1", Secret: ""}},
		"a secret that differs only in case": {{RuleID: "r1", Secret: "ABC"}},
	} {
		t.Run(name, func(t *testing.T) {
			m, n, err := MaskWith(stub(fs...), "xx abc yy")
			if err == nil {
				t.Fatalf("want an error, got (%q, %d)", m, n)
			}
			if m != (Masked{}) || n != 0 {
				t.Fatalf("an error must come with the zero Masked and 0, got (%q, %d)", m, n)
			}
		})
	}
	t.Run("MaskAndBuild writes nothing on it", func(t *testing.T) {
		l, m, err := MaskAndBuild(stub(Finding{RuleID: "r1", Secret: "nope"}), "xx abc yy", testInput())
		if err == nil || m != (Masked{}) || l.ID != "" {
			t.Fatalf("got (%+v, %q, %v)", l, m, err)
		}
	})
}

// REQ-0015
func TestMaskWith_Spans(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		fs   []Finding
		want string
		n    int
	}{
		{"overlapping findings are one span", "xxabcdefghixx",
			[]Finding{{"r1", "abcdef"}, {"r2", "defghi"}}, "xx[REDACTED:r1]xx", 1},
		{"the order of the findings does not matter", "xxabcdefghixx",
			[]Finding{{"r2", "defghi"}, {"r1", "abcdef"}}, "xx[REDACTED:r1]xx", 1},
		{"adjacent findings are one span", "ab-cdcd-ef",
			[]Finding{{"r1", "cdcd"}, {"r2", "ab-"}, {"r3", "-ef"}}, "[REDACTED:r2]", 1},
		{"a finding inside another is absorbed", "x SECRETVALUE y",
			[]Finding{{"r2", "RETV"}, {"r1", "SECRETVALUE"}}, "x [REDACTED:r1] y", 1},
		{"the rule of the earliest-starting finding names the span", "xxabcdefghixx",
			[]Finding{{"a-late", "defghi"}, {"z-early", "abcdef"}}, "xx[REDACTED:z-early]xx", 1},
		{"same start: the smaller rule id names the span", "xxabcdefxx",
			[]Finding{{"r2", "abcdef"}, {"r1", "abc"}}, "xx[REDACTED:r1]xx", 1},
		{"disjoint findings stay separate", "a-AAA-b-BBB-c",
			[]Finding{{"r2", "BBB"}, {"r1", "AAA"}}, "a-[REDACTED:r1]-b-[REDACTED:r2]-c", 2},
		{"every occurrence of a secret is a span", "AAA and AAA and AAA",
			[]Finding{{"r1", "AAA"}}, "[REDACTED:r1] and [REDACTED:r1] and [REDACTED:r1]", 3},
		{"a duplicate finding changes nothing", "x AAA y",
			[]Finding{{"r1", "AAA"}, {"r1", "AAA"}}, "x [REDACTED:r1] y", 1},
		{"non-ASCII text around a span", "é日本 AAA 日本é",
			[]Finding{{"r1", "AAA"}}, "é日本 [REDACTED:r1] 日本é", 1},
		{"no findings: unchanged", "nothing here\n", nil, "nothing here\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, n, err := MaskWith(stub(tc.fs...), tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if m.String() != tc.want || n != tc.n {
				t.Fatalf("got (%q, %d), want (%q, %d)", m, n, tc.want, tc.n)
			}
		})
	}
}
