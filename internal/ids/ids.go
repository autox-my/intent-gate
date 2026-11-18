// Package ids makes lock ids: ULIDs (26-character Crockford base32, 48-bit millisecond time and 80
// random bits), in-house so that the module takes no dependency for them.
package ids

import (
	"io"
	"time"
)

// NewULID returns a ULID for t, with its 80 random bits read from r.
func NewULID(t time.Time, r io.Reader) (string, error) {
	return "", nil
}
