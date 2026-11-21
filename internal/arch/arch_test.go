package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkDir reports the problems in the non-test .go files of one package directory.
// STUB (red at the base): it reports nothing.
func checkDir(dir string) ([]string, error) { return nil, nil }

// checkDesign reads the PURE packages from the design overview at readme and checks each,
// resolving package paths against root. STUB (red at the base): it reports nothing.
func checkDesign(root, readme string) ([]string, error) { return nil, nil }

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func TestPurePackages(t *testing.T) {
	root := repoRoot(t)
	problems, err := checkDesign(root, filepath.Join(root, "docs", "design", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

func TestPurePackages_Canary(t *testing.T) {
	dir := t.TempDir()
	src := `package bad

import (
	"os"
	t2 "time"

	"github.com/autox-my/intent-gate/internal/gitx"
)

var _ = os.Args
var _ = gitx.X

func f() { _ = t2.Now() }
`
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// a test file is not checked
	if err := os.WriteFile(filepath.Join(dir, "bad_test.go"), []byte("package bad\n\nimport \"net\"\n\nvar _ = net.IPv4len\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, err := checkDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(problems, "\n")
	for _, want := range []string{`bad.go:4`, `"os"`, `bad.go:12`, "time.Now", `internal/gitx`} {
		if !strings.Contains(all, want) {
			t.Errorf("canary: problems do not mention %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "bad_test.go") {
		t.Errorf("canary: a _test.go file was checked:\n%s", all)
	}
}

func TestPurePackages_DesignListMissingDir(t *testing.T) {
	root := t.TempDir()
	readme := filepath.Join(root, "README.md")
	doc := "# x\n\n```text\ninternal/nowhere/   PURE  does not exist\ninternal/other/    adapter  not pure\n```\n"
	if err := os.WriteFile(readme, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, err := checkDesign(root, readme)
	if err == nil && !strings.Contains(strings.Join(problems, "\n"), "internal/nowhere") {
		t.Fatalf("a PURE line naming a missing directory was not reported (problems=%v, err=%v)", problems, err)
	}
	if err != nil && !strings.Contains(err.Error(), "internal/nowhere") {
		t.Fatalf("error does not name the missing directory: %v", err)
	}
}
