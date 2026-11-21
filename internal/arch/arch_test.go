package arch

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/autox-my/intent-gate"

// forbiddenImports are the standard-library packages a PURE package may not import.
var forbiddenImports = map[string]bool{
	"os": true, "os/exec": true, "os/signal": true, "io/fs": true, "io/ioutil": true, "syscall": true,
	"crypto/rand": true, "math/rand": true, "math/rand/v2": true, "runtime/debug": true,
	"plugin": true, "unsafe": true,
}

// adapterPackages are the module's own adapter packages (and everything under them).
var adapterPackages = []string{
	"internal/gitx", "internal/forge", "internal/hooks", "internal/ratify", "internal/planreview",
}

// forbiddenTime are the time package functions that read the clock.
var forbiddenTime = map[string]bool{
	"Now": true, "Since": true, "Until": true, "Tick": true, "After": true, "NewTimer": true, "NewTicker": true,
}

func forbiddenImport(path string) bool {
	if forbiddenImports[path] || path == "net" || strings.HasPrefix(path, "net/") {
		return true
	}
	for _, a := range adapterPackages {
		full := modulePath + "/" + a
		if path == full || strings.HasPrefix(path, full+"/") {
			return true
		}
	}
	return false
}

// checkDir reports the problems in the non-test .go files of one package directory.
// Each problem names the file, the line and the import or call.
func checkDir(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	var problems []string
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		checked++
		report := func(pos token.Pos, what string) {
			problems = append(problems, fmt.Sprintf("%s:%d: %s", filepath.Base(name), fset.Position(pos).Line, what))
		}
		timeNames := map[string]bool{} // local names of the time package
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return nil, fmt.Errorf("%s: bad import %s", name, imp.Path.Value)
			}
			if forbiddenImport(path) {
				report(imp.Pos(), fmt.Sprintf("imports %q", path))
			}
			if path == "time" {
				local := "time"
				if imp.Name != nil {
					local = imp.Name.Name
				}
				if local == "." {
					report(imp.Pos(), `dot-imports "time" (its clock calls cannot be checked)`)
				} else if local != "_" {
					timeNames[local] = true
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && timeNames[id.Name] && forbiddenTime[sel.Sel.Name] {
				report(sel.Pos(), fmt.Sprintf("uses time.%s", sel.Sel.Name))
			}
			return true
		})
	}
	if checked == 0 {
		return nil, fmt.Errorf("no non-test .go files in %s", dir)
	}
	return problems, nil
}

// pureDirs returns the package paths of the lines of the first fenced text block of the design
// overview whose second column is PURE.
func pureDirs(markdown string) []string {
	var dirs []string
	inText := false
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inText = !inText && strings.TrimSpace(strings.TrimPrefix(trimmed, "```")) == "text"
			continue
		}
		if !inText {
			continue
		}
		cols := strings.Fields(line)
		if len(cols) >= 2 && cols[1] == "PURE" {
			dirs = append(dirs, cols[0])
		}
	}
	return dirs
}

// checkDesign reads the PURE packages from the design overview at readme and checks each,
// resolving package paths against root. A missing directory or an empty list is an error.
func checkDesign(root, readme string) ([]string, error) {
	data, err := os.ReadFile(readme)
	if err != nil {
		return nil, err
	}
	dirs := pureDirs(string(data))
	if len(dirs) == 0 {
		return nil, fmt.Errorf("%s lists no PURE package in its layout block", readme)
	}
	var problems []string
	for _, d := range dirs {
		dir := filepath.Join(root, filepath.FromSlash(d))
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("%s lists %s as PURE, but that directory does not exist", readme, d)
		}
		ps, err := checkDir(dir)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			problems = append(problems, d+p)
		}
	}
	return problems, nil
}

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
	for _, want := range []string{`bad.go:4`, `"os"`, `bad.go:13`, "time.Now", `internal/gitx`} {
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

// The PURE list must be non-trivial, so TestPurePackages cannot pass by checking nothing.
func TestPureDirs_FoundInDesignOverview(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs", "design", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	dirs := pureDirs(string(data))
	t.Logf("PURE packages: %v", dirs)
	if len(dirs) == 0 {
		t.Fatal("no PURE package found in docs/design/README.md")
	}
}
