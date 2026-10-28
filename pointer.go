package ashlar

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrPointer is returned when an RFC 6901 JSON Pointer is syntactically invalid.
var ErrPointer = errors.New("ashlar: invalid json pointer")

// maxInt is the maximum value of int on this platform.
const maxInt = int(^uint(0) >> 1)

// Pointer is an RFC 6901 JSON Pointer (e.g., "", "/service/tick_interval", "/routes/0/from").
type Pointer string

// RootPointer addresses the root value of a document.
const RootPointer Pointer = ""

// ParsePointer validates raw as an RFC 6901 JSON Pointer and returns it.
// An empty string is valid and addresses the document root; any non-empty pointer
// must start with '/' and may only use '~' as "~0" or "~1".
func ParsePointer(raw string) (Pointer, error) {
	if raw == "" {
		return RootPointer, nil
	}
	if raw[0] != '/' {
		return "", fmt.Errorf("%w: %q must be empty or start with '/'", ErrPointer, raw)
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '~' {
			continue
		}
		if i+1 >= len(raw) || (raw[i+1] != '0' && raw[i+1] != '1') {
			return "", fmt.Errorf("%w: %q has invalid escape at byte %d (want ~0 or ~1)", ErrPointer, raw, i)
		}
		i++
	}
	return Pointer(raw), nil
}

// NewPointer constructs an escaped RFC 6901 JSON Pointer from unescaped reference tokens.
func NewPointer(tokens ...string) Pointer {
	if len(tokens) == 0 {
		return RootPointer
	}
	var b strings.Builder
	for _, tok := range tokens {
		b.WriteByte('/')
		b.WriteString(escapeToken(tok))
	}
	return Pointer(b.String())
}

// Child appends a single unescaped member name or token to p.
func (p Pointer) Child(token string) Pointer {
	return Pointer(string(p) + "/" + escapeToken(token))
}

// Index appends an array index token to p.
func (p Pointer) Index(i int) Pointer {
	return Pointer(string(p) + "/" + strconv.Itoa(i))
}

// Valid reports whether p is a syntactically valid RFC 6901 JSON Pointer.
func (p Pointer) Valid() bool {
	_, err := ParsePointer(string(p))
	return err == nil
}

// Tokens decodes and returns the unescaped reference tokens of p.
// RootPointer ("") returns (nil, nil).
func (p Pointer) Tokens() ([]string, error) {
	if _, err := ParsePointer(string(p)); err != nil {
		return nil, err
	}
	if p == RootPointer {
		return nil, nil
	}
	rawParts := strings.Split(string(p)[1:], "/")
	out := make([]string, len(rawParts))
	for i, part := range rawParts {
		out[i] = unescapeToken(part)
	}
	return out, nil
}

// TopToken returns the first unescaped reference token of p, or "" if p is empty or invalid.
func (p Pointer) TopToken() string {
	if p == "" || p[0] != '/' {
		return ""
	}
	rest := string(p)[1:]
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		rest = rest[:idx]
	}
	return unescapeToken(rest)
}

// LastToken returns the final unescaped reference token of p, or "" if p is empty or invalid.
func (p Pointer) LastToken() string {
	if p == "" || p[0] != '/' {
		return ""
	}
	idx := strings.LastIndexByte(string(p), '/')
	return unescapeToken(string(p)[idx+1:])
}

// ParseArrayIndex reports whether tok is a valid RFC 6901 array index ("0" or
// a nonzero decimal digit followed by decimal digits, with no sign or
// whitespace) and returns its value. Tokens that overflow int are invalid.
func ParseArrayIndex(tok string) (int, bool) {
	if tok == "" {
		return 0, false
	}
	if tok[0] == '0' && len(tok) > 1 {
		return 0, false // RFC 6901 forbids leading zeros on array indices
	}
	n := 0
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		d := int(c - '0')
		if n > (maxInt-d)/10 {
			return 0, false // would overflow int
		}
		n = n*10 + d
	}
	return n, true
}

// String returns the RFC 6901 string representation of p.
func (p Pointer) String() string {
	return string(p)
}

func escapeToken(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

func unescapeToken(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	return strings.ReplaceAll(s, "~0", "~")
}

// At resolves an RFC 6901 JSON Pointer against v, returning OmittedValue() if
// ptr is invalid or any token along the path does not exist. The returned Value
// keeps its own Provenance; explicit sensitivity already reaches descendants
// when a Value is built.
func (v Value) At(ptr Pointer) Value {
	if ptr == RootPointer {
		return v
	}
	tokens, err := ptr.Tokens()
	if err != nil {
		return OmittedValue()
	}
	cur := v
	for _, tok := range tokens {
		switch cur.Kind() {
		case KindObject:
			next, ok := cur.Lookup(tok)
			if !ok {
				return OmittedValue()
			}
			cur = next
		case KindArray:
			idx, ok := ParseArrayIndex(tok)
			if !ok || idx >= cur.Len() {
				return OmittedValue()
			}
			cur = cur.Index(idx)
		default:
			return OmittedValue()
		}
	}
	return cur
}

// PresenceAt returns the Presence of the node at ptr (PresenceOmitted, PresenceNull, or PresencePresent).
func (v Value) PresenceAt(ptr Pointer) Presence {
	return v.At(ptr).Presence()
}
