package ashlar

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrPointer is returned when an RFC 6901 JSON Pointer is syntactically invalid.
var ErrPointer = errors.New("ashlar: invalid json pointer")

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
