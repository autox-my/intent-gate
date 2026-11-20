package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/autox-my/intent-gate/internal/gitx"
	"github.com/autox-my/intent-gate/internal/ratify"
)

// committedIntent is a committed intent file read for locking (intent-lock.md §6, REQ-0043): its
// repo-relative path, the git blob id at HEAD, and its text, which equals that blob's content and
// has been through the secret scan.
type committedIntent struct {
	Path string
	Blob string
	Text string
}

// record is what the lock records of it; nil for no committed intent.
func (c *committedIntent) record() *ratify.CommittedIntent {
	if c == nil {
		return nil
	}
	return &ratify.CommittedIntent{Path: c.Path, Blob: c.Blob}
}

// describe is the line that says what the root intent is.
func (c committedIntent) describe() string {
	return "root intent: committed intent " + c.Path + " (blob " + c.Blob[:12] + ")"
}

// loadCommittedIntent reads the intent file at path (already checked against the path rule) in the
// repository at top. The file must be tracked at HEAD and its working-tree bytes must equal the
// blob there, so the lock binds to text anyone can read from git; the text must not look like it
// holds a secret. Every error starts "no lock: " and is exit 3: the lock command cannot decide,
// the human has to fix the repository.
func loadCommittedIntent(top, path string) (committedIntent, error) {
	blob, found, err := gitx.BlobAt(top, "HEAD", path)
	if err != nil {
		return committedIntent{}, fmt.Errorf("no lock: the committed intent %s: %v", path, err)
	}
	if !found {
		return committedIntent{}, fmt.Errorf("no lock: the committed intent %s is not tracked at HEAD; commit it first", path)
	}
	committed, err := gitx.CatBlob(top, blob)
	if err != nil {
		return committedIntent{}, fmt.Errorf("no lock: the committed intent %s: %v", path, err)
	}
	file := filepath.Join(top, filepath.FromSlash(path))
	if info, err := os.Lstat(file); err != nil || !info.Mode().IsRegular() {
		return committedIntent{}, fmt.Errorf("no lock: the committed intent %s is not a regular file in the working tree; restore it from HEAD", path)
	}
	disk, err := os.ReadFile(file)
	if err != nil {
		return committedIntent{}, fmt.Errorf("no lock: cannot read the committed intent %s: %v", path, err)
	}
	if !bytes.Equal(disk, committed) {
		return committedIntent{}, fmt.Errorf("no lock: %s has uncommitted changes; commit or discard them so the lock binds to what git holds", path)
	}
	if _, err := ratify.ScanCommitted(nil, string(disk)); err != nil {
		if errors.Is(err, ratify.ErrSecretInIntent) {
			return committedIntent{}, fmt.Errorf("no lock: %w: %s is already public, so masking it would hide nothing; fix the file and its history", err, path)
		}
		return committedIntent{}, fmt.Errorf("no lock: %w: %v", errScan, err)
	}
	return committedIntent{Path: path, Blob: blob, Text: string(disk)}, nil
}
