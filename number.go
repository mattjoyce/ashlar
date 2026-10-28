package ashlar

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// isValidNumberLexeme reports whether s conforms to the RFC 8259 JSON number
// grammar: -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?
func isValidNumberLexeme(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[i] == '-' {
		i++
		if i >= len(s) {
			return false
		}
	}
	switch {
	case s[i] == '0':
		i++
	case s[i] >= '1' && s[i] <= '9':
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	default:
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		startFrac := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == startFrac {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		startExp := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == startExp {
			return false
		}
	}
	return i == len(s)
}

// ParseByteSize parses human-readable positive byte counts such as "1024",
// "512KB", "64MB", or "2GB" with int64 overflow protection, preserving the
// caller's original string in error messages.
func ParseByteSize(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("%w: byte size is empty", ErrValidation)
	}
	original := trimmed
	upper := strings.ToUpper(trimmed)
	multiplier := int64(1)

	switch {
	case strings.HasSuffix(upper, "KB"):
		multiplier = 1024
		trimmed = strings.TrimSuffix(upper, "KB")
	case strings.HasSuffix(upper, "MB"):
		multiplier = 1024 * 1024
		trimmed = strings.TrimSuffix(upper, "MB")
	case strings.HasSuffix(upper, "GB"):
		multiplier = 1024 * 1024 * 1024
		trimmed = strings.TrimSuffix(upper, "GB")
	}

	val, err := strconv.ParseInt(strings.TrimSpace(trimmed), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid byte size %q", ErrValidation, original)
	}
	if val <= 0 {
		return 0, fmt.Errorf("%w: byte size %q must be positive", ErrValidation, original)
	}
	if multiplier > 1 && val > (math.MaxInt64/multiplier) {
		return 0, fmt.Errorf("%w: byte size %q overflows int64", ErrValidation, original)
	}
	return val * multiplier, nil
}
