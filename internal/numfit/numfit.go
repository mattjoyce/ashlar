// Package numfit explains why a JSON number lexeme does not fit a Go numeric
// type. yamlconfig and jsoncontract share it so both report the same fixed
// messages, none of which quotes the number.
package numfit

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Problem returns a fixed message saying why lexeme, a JSON number literal,
// cannot be stored in a value of type t, or "" when it fits. It handles the
// integer, unsigned and float kinds and returns "" for any other kind. The
// checks run in order: integer notation, then sign, then range.
func Problem(lexeme string, t reflect.Type) string {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if !isIntegerNotation(lexeme) {
			return fmt.Sprintf("number requires integer notation for %s", t)
		}
		i, err := strconv.ParseInt(lexeme, 10, 64)
		if err != nil || reflect.Zero(t).OverflowInt(i) {
			return rangeOr(err, t)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if !isIntegerNotation(lexeme) {
			return fmt.Sprintf("number requires integer notation for %s", t)
		}
		// strconv reports a minus sign as a syntax error for unsigned
		// parsing, not a range error, so the sign is checked from the
		// spelling. That includes -0.
		if strings.HasPrefix(lexeme, "-") {
			return fmt.Sprintf("negative sign is not allowed for %s", t)
		}
		u, err := strconv.ParseUint(lexeme, 10, 64)
		if err != nil || reflect.Zero(t).OverflowUint(u) {
			return rangeOr(err, t)
		}
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(lexeme, 64)
		if err != nil || reflect.Zero(t).OverflowFloat(f) {
			return rangeOr(err, t)
		}
	}
	return ""
}

// rangeOr reports an out-of-range number. A nil err means the value parsed but
// overflows the narrower target. A non-range parse error should not happen for
// a valid JSON lexeme, so it gets a generic message.
func rangeOr(err error, t reflect.Type) string {
	var numErr *strconv.NumError
	if err == nil || (errors.As(err, &numErr) && errors.Is(numErr.Err, strconv.ErrRange)) {
		return fmt.Sprintf("number out of range for %s", t)
	}
	return fmt.Sprintf("expected %s, got number", t)
}

// isIntegerNotation reports whether a JSON number literal has no fraction and
// no exponent.
func isIntegerNotation(lexeme string) bool {
	return !strings.ContainsAny(lexeme, ".eE")
}
