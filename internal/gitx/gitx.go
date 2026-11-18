// Package gitx is the adapter to the git CLI: the few repository facts the hooks need.
package gitx

// TopLevel returns the root of the working tree that contains dir.
func TopLevel(dir string) (string, error) { return "", nil }

// UserEmail returns git's user.email as seen from dir; empty is an error.
func UserEmail(dir string) (string, error) { return "", nil }

// BaseRef returns the ref a lock's base_ref should name for the repository at dir.
func BaseRef(dir string) (string, error) { return "", nil }
