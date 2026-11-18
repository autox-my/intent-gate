// Command intentgate locks the intent behind agent-written changes. This slice has one command:
// `intentgate hook claude-code pre|post|print-config`.
package main

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"time"
)

func main() {
	os.Exit(Run(os.Args[1:], os.Stdin, os.Stdout, Deps{
		Now:      time.Now,
		Rand:     rand.Reader,
		StateDir: stateDir(),
		Git:      gitCLI{},
		Stderr:   os.Stderr,
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
