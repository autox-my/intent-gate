// Package gitx is the adapter to the git CLI: the few repository facts the hooks need.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// run runs git in dir and returns its trimmed standard output.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(out.String()), nil
}

// TopLevel returns the root of the working tree that contains dir.
func TopLevel(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("no working directory")
	}
	top, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if top == "" {
		return "", errors.New("git rev-parse --show-toplevel printed nothing")
	}
	return top, nil
}

// UserEmail returns git's user.email as seen from dir; empty is an error, since the ratifier is
// required.
func UserEmail(dir string) (string, error) {
	out, err := run(dir, "config", "user.email")
	if err != nil || out == "" {
		return "", errors.New("git user.email is not set")
	}
	return out, nil
}

// BaseRef returns the ref a lock's base_ref should name for the repository at dir:
// $INTENTGATE_BASE_REF if set; else the short name of refs/remotes/origin/HEAD without origin/;
// else main if a local branch main exists, else master; else an error.
func BaseRef(dir string) (string, error) {
	if v := os.Getenv("INTENTGATE_BASE_REF"); v != "" {
		return v, nil
	}
	if short, err := run(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && short != "" {
		if name := strings.TrimPrefix(short, "origin/"); name != "" {
			return name, nil
		}
	}
	for _, b := range []string{"main", "master"} {
		if _, err := run(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b, nil
		}
	}
	return "", errors.New("cannot tell the base branch: set INTENTGATE_BASE_REF")
}
