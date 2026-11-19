package ids

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/autox-my/intent-gate/internal/lock"
)

func TestNewULID(t *testing.T) {
	ff := bytes.Repeat([]byte{0xff}, 10)
	zero := make([]byte, 10)
	cases := []struct {
		name string
		at   time.Time
		rnd  []byte
		want string
	}{
		{"zero time and bytes", time.UnixMilli(0), zero, "00000000000000000000000000"},
		// 1469918176385 ms is the time of the ULID spec's example, 01ARYZ6S41.
		{"spec time, zero bytes", time.UnixMilli(1469918176385), zero, "01ARYZ6S410000000000000000"},
		{"spec time, all-ones bytes", time.UnixMilli(1469918176385), ff, "01ARYZ6S41ZZZZZZZZZZZZZZZZ"},
		{"one in the last random bit", time.UnixMilli(0), append(append([]byte{}, zero[:9]...), 1), "00000000000000000000000001"},
		{"one in the first random bit", time.UnixMilli(0), append([]byte{0x80}, zero[:9]...), "0000000000G000000000000000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NewULID(c.at, bytes.NewReader(c.rnd))
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("NewULID = %q, want %q", got, c.want)
			}
			if len(got) != 26 {
				t.Errorf("length %d, want 26", len(got))
			}
		})
	}

	t.Run("passes the lock's ULID rule", func(t *testing.T) {
		id, err := NewULID(time.Date(2026, 11, 18, 9, 14, 3, 0, time.UTC), bytes.NewReader(ff))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range lock.Validate(lock.Lock{ID: id}) {
			if p.Path == "id" {
				t.Errorf("lock.Validate rejects %q: %s", id, p)
			}
		}
	})

	t.Run("time-ordered", func(t *testing.T) {
		var prev string
		for i := 0; i < 5; i++ {
			id, err := NewULID(time.UnixMilli(1_700_000_000_000+int64(i)*1000), bytes.NewReader(make([]byte, 10)))
			if err != nil {
				t.Fatal(err)
			}
			if prev != "" && !(prev < id) {
				t.Errorf("%q is not before %q", prev, id)
			}
			prev = id
		}
	})

	t.Run("a short random source is an error", func(t *testing.T) {
		if _, err := NewULID(time.UnixMilli(0), strings.NewReader("short")); err == nil {
			t.Error("want an error")
		}
	})

	t.Run("a time outside 48 bits is an error", func(t *testing.T) {
		if _, err := NewULID(time.UnixMilli(-1), bytes.NewReader(make([]byte, 10))); err == nil {
			t.Error("want an error for a time before 1970")
		}
	})
}
