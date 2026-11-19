// Command intentgate locks the intent behind agent-written changes. Commands:
// `intentgate hook claude-code pre|post|print-config`, `intentgate lock --from-plan <plan.md>` or `--issue <n>`, and
// `intentgate amend <id> ...`, `intentgate verify`, `intentgate publish`, `intentgate stats` and `intentgate init`.
package main

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"time"
)

// version is IntentGate's version, set with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	dir, _ := os.Getwd()
	os.Exit(Run(os.Args[1:], os.Stdin, os.Stdout, Deps{
		Now:      time.Now,
		Rand:     rand.Reader,
		StateDir: stateDir(),
		Git:      gitCLI{},
		Stderr:   os.Stderr,

		GHAuthToken: ghAuthToken,

		Dir:        dir,
		IsTerminal: isTerminal,
	}))
}

// stateDir is $INTENTGATE_HOME, else ~/.intentgate; "" if neither can be found (the hooks then
// report "no lock").
func stateDir() string {
	if d := os.Getenv("INTENTGATE_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".intentgate")
}
