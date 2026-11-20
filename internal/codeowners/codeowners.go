// Package codeowners parses a CODEOWNERS file and says who owns a path, by GitHub's rules. It is
// pure: no I/O.
package codeowners

// Locations are the places GitHub looks for the file, in the order it uses: the first that exists
// at a revision is the one that counts.
var Locations = []string{}

// Kind says what an owner is.
type Kind int

// The kinds of owner.
const (
	User  Kind = iota + 1 // @login
	Team                  // @org/team
	Email                 // an email address
	Other                 // anything GitHub would not accept as an owner
)

// Owner is one owner of a path. Name is the login, the team as org/team (no @) or the email.
type Owner struct {
	Name string
	Kind Kind
}

// File is a parsed CODEOWNERS file.
type File struct{}

// Parse reads a CODEOWNERS file.
func Parse(data []byte) *File { return &File{} }

// Owners returns the owners of path (slash separated, relative to the repository root): those of
// the last line whose pattern matches. No matching line, and a matching line without owners, both
// give nil.
func (f *File) Owners(path string) []Owner { return nil }
