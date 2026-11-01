package yamlconfig

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/mattjoyce/ashlar"
)

// valueToGoAny converts val to plain Go values. A number becomes int64 or
// uint64 when its lexeme is an integer that fits, otherwise float64 when the
// float64's shortest decimal form has the same value as the lexeme (so 0.1
// stays a float64). Any other number, such as 1e400, 1e-400, an integer past
// uint64, or 9007199254740993.0, becomes json.Number with its exact lexeme
// rather than losing value. With exactNumbers set, every number becomes
// json.Number.
func valueToGoAny(val ashlar.Value, exactNumbers bool) any {
	switch val.Kind() {
	case ashlar.KindNull:
		return nil
	case ashlar.KindBool:
		b, _ := val.Bool()
		return b
	case ashlar.KindNumber:
		lex, _ := val.NumberLexeme()
		if exactNumbers {
			return json.Number(lex)
		}
		if i, err := val.Int64(); err == nil {
			return i
		}
		if u, err := val.Uint64(); err == nil {
			return u
		}
		if f, err := val.Float64(); err == nil && sameDecimal(string(lex), strconv.FormatFloat(f, 'g', -1, 64)) {
			return f
		}
		return json.Number(lex)
	case ashlar.KindString:
		s, _ := val.StringVal()
		return s
	case ashlar.KindArray:
		elems := val.Elements()
		out := make([]any, len(elems))
		for i, el := range elems {
			out[i] = valueToGoAny(el, exactNumbers)
		}
		return out
	case ashlar.KindObject:
		members := val.Members()
		out := make(map[string]any, len(members))
		for _, m := range members {
			out[m.Key] = valueToGoAny(m.Value, exactNumbers)
		}
		return out
	default:
		return nil
	}
}

// sameDecimal reports whether JSON number literals a and b denote the same
// value. It compares sign, significant digits and decimal exponent as strings,
// so a huge exponent costs nothing. A literal it cannot parse never matches.
func sameDecimal(a, b string) bool {
	na, okA := normalizeDecimal(a)
	nb, okB := normalizeDecimal(b)
	return okA && okB && na == nb
}

// normalizeDecimal reduces a JSON number literal to "sign digits e exponent",
// where digits has no leading or trailing zeros and the value is
// 0.digits × 10^exponent. Zero normalizes to "0" whatever its sign.
func normalizeDecimal(lit string) (string, bool) {
	sign := ""
	if strings.HasPrefix(lit, "-") {
		sign, lit = "-", lit[1:]
	}
	mant, expPart, hasExp := strings.Cut(strings.ToLower(lit), "e")
	exp := 0
	if hasExp {
		e, err := strconv.Atoi(expPart)
		if err != nil {
			return "", false
		}
		exp = e
	}
	intPart, frac, _ := strings.Cut(mant, ".")
	digits := intPart + frac
	exp += len(intPart)
	trimmed := strings.TrimLeft(digits, "0")
	exp -= len(digits) - len(trimmed)
	trimmed = strings.TrimRight(trimmed, "0")
	if trimmed == "" {
		return "0", true
	}
	return sign + trimmed + "e" + strconv.Itoa(exp), true
}
