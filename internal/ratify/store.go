package ratify

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/autox-my/intent-gate/internal/lock"
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

// Amend replaces the committed lock <RepoRoot>/.intent/<id>.yml with next, which is old amended
// (ratification.md §3, footprint-check.md §5). It refuses, changing nothing, unless: next has old's
// id and root_intent, next.Revision is old.Revision+1, next.Amendments is old.Amendments unchanged
// plus exactly one appended entry whose revision is next.Revision (history is append-only), the file on disk still parses, verifies and
// hashes to old.Hash (nobody changed the lock since old was read), and next validates and verifies.
// The new file is written to a temp file next to it and renamed over it, so a reader sees the old
// lock or the new one. The root intent record under StateDir is never touched.
func (s Store) Amend(old, next lock.Lock) error {
	if next.ID != old.ID {
		return fmt.Errorf("ratify: refusing to amend: the id changed from %s to %s", old.ID, next.ID)
	}
	if !sameRootIntent(old.RootIntent, next.RootIntent) {
		return errors.New("ratify: refusing to amend: root_intent changed")
	}
	if next.Revision != old.Revision+1 {
		return fmt.Errorf("ratify: refusing to amend: revision must be %d, got %d", old.Revision+1, next.Revision)
	}
	if n := len(old.Amendments); len(next.Amendments) != n+1 ||
		!amendmentsEqual(next.Amendments[:n], old.Amendments) || next.Amendments[n].Revision != next.Revision {
		return errors.New("ratify: refusing to amend: amendments must be the old ones unchanged plus exactly one new entry for the new revision")
	}
	if ps := lock.Validate(next); len(ps) > 0 {
		return fmt.Errorf("ratify: refusing to write an invalid lock: %s", ps[0])
	}
	if !lock.VerifyHash(next) {
		return errors.New("ratify: refusing to write a lock whose hash does not verify")
	}
	if !lock.ValidID(old.ID) { // the id is a file name
		return fmt.Errorf("ratify: %q is not a lock id", old.ID)
	}

	path := filepath.Join(s.RepoRoot, ".intent", old.ID+".yml")
	disk, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	onDisk, err := lock.Parse(disk)
	if err != nil {
		return fmt.Errorf("ratify: refusing to amend: %s: %w", path, err)
	}
	if old.Hash == "" || onDisk.Hash != old.Hash || !lock.VerifyHash(onDisk) {
		return fmt.Errorf("ratify: refusing to amend: %s is not the lock that was read (its hash changed or does not verify)", path)
	}
	data, err := lock.Marshal(next)
	if err != nil {
		return err
	}
	return replaceAtomic(path, data, 0o644)
}

// amendmentsEqual compares two amendment lists field by field (Amendment holds only comparable fields).
func amendmentsEqual(a, b []lock.Amendment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameRootIntent(a, b lock.RootIntent) bool {
	if a.SHA256 != b.SHA256 || a.Source != b.Source || (a.Redactions == nil) != (b.Redactions == nil) {
		return false
	}
	return a.Redactions == nil || *a.Redactions == *b.Redactions
}

// replaceAtomic writes data to a temp file in path's directory and renames it over path.
func replaceAtomic(path string, data []byte, mode os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("ratify: %w", err)
	}
	if err = tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("ratify: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("ratify: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("ratify: %w", err)
	}
	return nil
}
