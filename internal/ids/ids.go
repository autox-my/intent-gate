// Package ids makes lock ids: ULIDs (26-character Crockford base32, 48-bit millisecond time and 80
// random bits), in-house so that the module takes no dependency for them.
package ids

import (
	"errors"
	"fmt"
	"io"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID returns a ULID for t, with its 80 random bits read from r (production passes
// crypto/rand.Reader). t must be between 1970 and the year 10889, the range of 48 bits of
// milliseconds.
func NewULID(t time.Time, r io.Reader) (string, error) {
	ms := t.UnixMilli()
	if ms < 0 || ms >= 1<<48 {
		return "", fmt.Errorf("ids: time %s does not fit a ULID", t.UTC().Format(time.RFC3339))
	}
	var b [16]byte
	for i := 0; i < 6; i++ {
		b[i] = byte(ms >> (8 * (5 - i)))
	}
	if _, err := io.ReadFull(r, b[6:]); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return "", fmt.Errorf("ids: reading random bytes: %w", err)
	}
	// 128 bits in 26 groups of 5: the first group holds only the top 3 bits.
	var out [26]byte
	for i := range out {
		var v byte
		for j := 0; j < 5; j++ {
			bit := i*5 + j - 2 // position in the 128-bit stream; negative is padding
			v <<= 1
			if bit >= 0 {
				v |= b[bit/8] >> (7 - bit%8) & 1
			}
		}
		out[i] = alphabet[v]
	}
	return string(out[:]), nil
}
