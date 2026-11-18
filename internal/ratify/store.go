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
// and then the lock <RepoRoot>/.intent/<id>.yml, each created exclusively (never overwritten). It
// refuses, writing nothing, when the lock is invalid or its hash doesn't verify, when the SHA-256
// of maskedRootIntent isn't l.RootIntent.SHA256, or when either file for this id already exists. If
// the lock can't be written, the record this call wrote is removed again.
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
	recPath := filepath.Join(recDir, "root-intent.md")
	if err := createExclusive(recPath, []byte(maskedRootIntent.text), 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		_ = os.Remove(recPath)
		return fmt.Errorf("ratify: %w", err)
	}
	if err := createExclusive(lockPath, data, 0o644); err != nil {
		_ = os.Remove(recPath) // the record is ours: createExclusive got it, so no earlier lock owns it
		return err
	}
	return nil
}

// createExclusive puts data at path with the given mode, and fails if path already exists, so
// neither the root intent record nor a lock can ever be overwritten. The bytes go to a temp file in
// path's directory first; the target is then created with O_CREATE|O_EXCL and filled from them, and
// removed again if that fails.
func createExclusive(path string, data []byte, mode os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("ratify: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("ratify: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("ratify: %s already exists", path)
	}
	if err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	src, err := os.ReadFile(tmp.Name())
	if err != nil {
		f.Close()
		return fmt.Errorf("ratify: %w", err)
	}
	if _, err := f.Write(src); err != nil {
		f.Close()
		return fmt.Errorf("ratify: %w", err)
	}
	if err := f.Chmod(mode); err != nil { // the umask may have narrowed the mode; make it exact
		f.Close()
		return fmt.Errorf("ratify: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("ratify: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	return nil
}

// Amend replaces the committed lock for old.ID with next. STUB (red at the base).
func (s Store) Amend(old, next lock.Lock) error {
	return errors.New("ratify: Amend is not implemented")
}
