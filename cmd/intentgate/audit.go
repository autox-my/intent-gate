package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"intentgate/internal/audit"
	"intentgate/internal/lock"
)

// The audit log (audit-metrics.md §1): one intentgate.audit/1 event per `verify`, `lock` and
// `amend`, appended to <state>/audit.jsonl. A failure to write it is a warning on stderr and never
// changes an exit code.

const auditLogFile = "audit.jsonl"

// inCI reports whether the process runs in CI ($CI is exactly "true"): there the audit event goes
// into the out dir and the artifact, not into a local log.
func inCI() bool { return os.Getenv("CI") == "true" }

// appendAudit appends the event as one line to <stateDir>/audit.jsonl (file 0600, directory 0700,
// O_APPEND, one Write so lines never interleave). It warns on stderr and returns when it cannot.
func appendAudit(stateDir string, e audit.Event, stderr io.Writer) {
	if stateDir == "" {
		return
	}
	if err := writeAudit(stateDir, audit.JSON(e)); err != nil {
		if stderr == nil {
			return
		}
		fmt.Fprintf(stderr, "intentgate: warning: cannot append to the audit log: %v\n", err)
	}
}

func writeAudit(stateDir string, line []byte) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(stateDir, auditLogFile), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// repoOf is "owner/name" from the origin remote of the repository at dir, or "" if there is none or
// it does not parse as a GitHub remote.
func repoOf(deps Deps, dir string) string {
	if deps.Git == nil {
		return ""
	}
	url, err := deps.Git.RemoteURL(dir, "origin")
	if err != nil {
		return ""
	}
	if owner, name, ok := repoFromRemote(url); ok {
		return owner + "/" + name
	}
	return ""
}

// auditLockWritten appends the lock or amend event of a lock that was just written.
func auditLockWritten(deps Deps, event string, l lock.Lock, at time.Time, dir string) {
	appendAudit(deps.StateDir, audit.NewLockEvent(event, l, at, version, repoOf(deps, dir)), deps.Stderr)
}
