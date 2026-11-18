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
