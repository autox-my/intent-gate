package ratify

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"intentgate/internal/lock"
)

// Store is where a ratified lock goes: the committed lock under RepoRoot/.intent and the root
// intent record under StateDir/locks/<id>. The caller supplies both directories (ratification.md
// §1 step 7; intent-lock.md §1); the package never looks up the home directory.
type Store struct {
	RepoRoot string // the repository the lock is committed to
	StateDir string // the ratifier's state directory, normally ~/.intentgate
}

// Write stores the root intent record <StateDir>/locks/<id>/root-intent.md (0600, directories 0700)
// and then the lock <RepoRoot>/.intent/<id>.yml, each by temp file and rename in its own directory.
// It refuses, writing nothing, when the lock is invalid or its hash doesn't verify, when the SHA-256
// of maskedRootIntent isn't l.RootIntent.SHA256, or when a lock with this id already exists.
func (s Store) Write(l lock.Lock, maskedRootIntent Masked) error {
	if ps := lock.Validate(l); len(ps) > 0 {
		return fmt.Errorf("ratify: refusing to write an invalid lock: %s", ps[0])
	}
	if !lock.VerifyHash(l) {
		return errors.New("ratify: refusing to write a lock whose hash does not verify")
	}
	sum := sha256.Sum256([]byte(maskedRootIntent.text))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(l.RootIntent.SHA256)) != 1 {
		return errors.New("ratify: refusing to write: the root intent text does not match root_intent.sha256")
	}
	data, err := lock.Marshal(l)
	if err != nil {
		return err
	}

	lockDir := filepath.Join(s.RepoRoot, ".intent")
	lockPath := filepath.Join(lockDir, l.ID+".yml")
	if _, err := os.Lstat(lockPath); err == nil {
		return fmt.Errorf("ratify: a lock with id %s already exists: %s", l.ID, lockPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("ratify: %w", err)
	}

	recDir := filepath.Join(s.StateDir, "locks", l.ID)
	if err := os.MkdirAll(recDir, 0o700); err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	if err := writeFile(filepath.Join(recDir, "root-intent.md"), []byte(maskedRootIntent.text), 0o600, true); err != nil {
		return err
	}
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	return writeFile(lockPath, data, 0o644, false)
}

// writeFile writes data to a temp file in path's directory and puts it at path. With replace it
// renames over an existing file; without, it links the temp file into place, which fails if path
// exists (so a concurrent writer of the same lock id can't be overwritten).
func writeFile(path string, data []byte, mode os.FileMode, replace bool) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("ratify: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("ratify: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("ratify: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	if replace {
		err = os.Rename(name, path)
	} else {
		err = os.Link(name, path)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("ratify: %s already exists", path)
		}
	}
	if err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	return nil
}
