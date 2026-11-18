package main

import (
	"intentgate/internal/gitx"
	"io"
	"time"
)

// Git is the repository facts the hook needs (internal/gitx behind an interface for tests).
type Git interface {
	TopLevel(dir string) (string, error)
	UserEmail(dir string) (string, error)
	BaseRef(dir string) (string, error)
}

// Deps is everything Run takes from the outside world.
type Deps struct {
	Now      func() time.Time
	Rand     io.Reader
	StateDir string
	Git      Git
	Stderr   io.Writer
}

// Run executes the intentgate command line and returns the exit code.
func Run(args []string, stdin io.Reader, stdout io.Writer, deps Deps) int { return 0 }

// gitCLI is the real Git: internal/gitx.
type gitCLI struct{}

func (gitCLI) TopLevel(dir string) (string, error)  { return gitx.TopLevel(dir) }
func (gitCLI) UserEmail(dir string) (string, error) { return gitx.UserEmail(dir) }
func (gitCLI) BaseRef(dir string) (string, error)   { return gitx.BaseRef(dir) }
