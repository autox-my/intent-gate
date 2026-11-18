package lock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// maxSafeInt is the largest integer RFC 8785 (via IEEE 754 doubles) represents exactly.
const maxSafeInt = 1<<53 - 1

var integerRE = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// CanonicalJSON returns the canonical form of the JSON document in data: the subset of
// RFC 8785 (JCS) the lock needs. Object keys are sorted by UTF-16 code units, there is no
// insignificant whitespace, strings use the RFC 8785 escaping, and numbers must be integers
// within ±(2^53-1). Floats, duplicate keys, trailing data and invalid JSON are errors.
func CanonicalJSON(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var out bytes.Buffer
	if err := canonValue(dec, &out); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("canonical json: trailing data after the document")
	}
	return out.Bytes(), nil
}

func canonValue(dec *json.Decoder, out *bytes.Buffer) error {
	tok, err := dec.Token()
	if err != nil {
		if err == io.EOF {
			return errors.New("canonical json: empty or truncated document")
		}
		return fmt.Errorf("canonical json: %w", err)
	}
	switch v := tok.(type) {
	case json.Delim:
		if v == '{' {
			return canonObject(dec, out)
		}
		return canonArray(dec, out)
	case string:
		return writeString(out, v)
	case json.Number:
		return writeNumber(out, v)
	case bool:
		out.WriteString(strconv.FormatBool(v))
	case nil:
		out.WriteString("null")
	default:
		return fmt.Errorf("canonical json: unexpected token %v", tok)
	}
	return nil
}

func canonArray(dec *json.Decoder, out *bytes.Buffer) error {
	out.WriteByte('[')
	first := true
	for dec.More() {
		if !first {
			out.WriteByte(',')
		}
		first = false
		if err := canonValue(dec, out); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // the closing ]
		return fmt.Errorf("canonical json: %w", err)
	}
	out.WriteByte(']')
	return nil
}

func canonObject(dec *json.Decoder, out *bytes.Buffer) error {
	type member struct {
		key   string
		units []uint16
		value []byte
	}
	var members []member
	seen := map[string]bool{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return fmt.Errorf("canonical json: %w", err)
		}
		key, ok := kt.(string)
		if !ok {
			return fmt.Errorf("canonical json: object key %v is not a string", kt)
		}
		if seen[key] {
			return fmt.Errorf("canonical json: duplicate key %q", key)
		}
		seen[key] = true
		var val bytes.Buffer
		if err := canonValue(dec, &val); err != nil {
			return err
		}
		members = append(members, member{key, utf16.Encode([]rune(key)), val.Bytes()})
	}
	if _, err := dec.Token(); err != nil { // the closing }
		return fmt.Errorf("canonical json: %w", err)
	}
	sort.Slice(members, func(i, j int) bool { return lessUTF16(members[i].units, members[j].units) })
	out.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			out.WriteByte(',')
		}
		if err := writeString(out, m.key); err != nil {
			return err
		}
		out.WriteByte(':')
		out.Write(m.value)
	}
	out.WriteByte('}')
	return nil
}

func lessUTF16(a, b []uint16) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func writeNumber(out *bytes.Buffer, n json.Number) error {
	s := n.String()
	if !integerRE.MatchString(s) || s == "-0" {
		return fmt.Errorf("canonical json: %q is not an integer (floats are rejected)", s)
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil || i > maxSafeInt || i < -maxSafeInt {
		return fmt.Errorf("canonical json: integer %s is outside ±(2^53-1)", s)
	}
	out.WriteString(s)
	return nil
}

func writeString(out *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return errors.New("canonical json: string is not valid UTF-8")
	}
	const hex = "0123456789abcdef"
	out.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case r == '\\':
			out.WriteString(`\\`)
		case r == '\b':
			out.WriteString(`\b`)
		case r == '\f':
			out.WriteString(`\f`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r < 0x20:
			out.WriteString(`\u00`)
			out.WriteByte(hex[r>>4])
			out.WriteByte(hex[r&0xf])
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return nil
}
