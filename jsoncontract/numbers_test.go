package jsoncontract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
)

// The JSON corpus differs from the YAML one by grammar: RFC 8259 has no hex
// or octal integers, no leading '+', no bare '.5' or '1.', and no Infinity or
// NaN. Those spellings are syntax errors here, while YAML accepts them.

// jsonNumberLoc is the file:line:column of LIT in the document built by
// writeJSONNumberDoc ("{\n  \"value\": LIT\n}\n").
const jsonNumberLoc = "config.json:2:12"

// jsonNumberPtr is the pointer of the value in that document.
const jsonNumberPtr = ashlar.Pointer("/value")

// jsonNumberField is a one-field decode target for a single JSON number.
type jsonNumberField[T any] struct {
	Value T `json:"value"`
}

// newJSONNumberField returns a constructor for a fresh *jsonNumberField[T].
func newJSONNumberField[T any]() func() any {
	return func() any { return new(jsonNumberField[T]) }
}

// writeJSONNumberDoc writes {"value": lit} across three lines to config.json
// in a fresh t.TempDir and returns the directory and the exact bytes written.
func writeJSONNumberDoc(t *testing.T, lit string) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	raw := []byte("{\n  \"value\": " + lit + "\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return dir, raw
}

// readJSONNumberDoc writes and reads the document for lit through ReadFS.
func readJSONNumberDoc(t *testing.T, lit string) (jsoncontract.Document, []byte) {
	t.Helper()
	dir, raw := writeJSONNumberDoc(t, lit)
	doc, err := jsoncontract.ReadFS(os.DirFS(dir), "config.json")
	if err != nil {
		t.Fatalf("ReadFS(%s) error = %v", lit, err)
	}
	return doc, raw
}

// requireJSONLocatedError fails t unless err is an *ashlar.Error matching
// kind with exactly ptr and loc.
func requireJSONLocatedError(t *testing.T, err error, kind error, ptr ashlar.Pointer, loc string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %v at %s (%s)", kind, loc, ptr)
	}
	if !errors.Is(err, kind) {
		t.Errorf("error = %v, want errors.Is(%v)", err, kind)
	}
	var ae *ashlar.Error
	if !errors.As(err, &ae) {
		t.Fatalf("error %T = %v, want *ashlar.Error", err, err)
	}
	if ae.Pointer != ptr {
		t.Errorf("Pointer = %q, want %q (error: %v)", ae.Pointer, ptr, err)
	}
	if got := ae.Origin.String(); got != loc {
		t.Errorf("Origin = %q, want %q (error: %v)", got, loc, err)
	}
}

// TestReadPreservesNumberLexemes checks that every JSON number spelling in the
// compatibility corpus reads as a KindNumber node whose NumberLexeme is the
// exact source spelling at the exact location, and that RawBytes is unchanged.
func TestReadPreservesNumberLexemes(t *testing.T) {
	t.Parallel()

	for _, lit := range []string{
		"1",
		"1.0",
		"1e0",
		"1E+2",
		"0.1e-7",
		"-0",
		"-0.0",
		"9223372036854775807",
		"9223372036854775808",
		"18446744073709551615",
		"18446744073709551616",
		"99999999999999999999999",
		"1e400",
	} {
		t.Run(lit, func(t *testing.T) {
			t.Parallel()
			doc, raw := readJSONNumberDoc(t, lit)
			v := doc.Root().At(jsonNumberPtr)
			if v.Kind() != ashlar.KindNumber {
				t.Fatalf("Kind() = %v, want number", v.Kind())
			}
			lex, ok := v.NumberLexeme()
			if !ok || lex != lit {
				t.Errorf("NumberLexeme() = %q, %v; want %q, true", lex, ok, lit)
			}
			jn, ok := v.JSONNumber()
			if !ok || string(jn) != lit {
				t.Errorf("JSONNumber() = %q, %v; want %q, true", jn, ok, lit)
			}
			if got := v.Provenance().Effective.Location(); got != jsonNumberLoc {
				t.Errorf("Location() = %q, want %q", got, jsonNumberLoc)
			}
			if !bytes.Equal(doc.RawBytes(), raw) {
				t.Errorf("RawBytes() = %q, want %q", doc.RawBytes(), raw)
			}
		})
	}
}

// TestReadRejectsNonJSONNumberSpellings checks that number spellings YAML
// accepts but RFC 8259 does not fail with a located ErrSyntax. The location is
// the first byte that breaks the grammar: the 'x' or 'o' after a leading 0,
// and the start of the token otherwise.
func TestReadRejectsNonJSONNumberSpellings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		lit string
		loc string
	}{
		{lit: "0x1F", loc: "config.json:2:13"},
		{lit: "0o17", loc: "config.json:2:13"},
		{lit: "017", loc: "config.json:2:12"},
		{lit: "+1", loc: "config.json:2:12"},
		{lit: ".5", loc: "config.json:2:12"},
		{lit: "1.", loc: "config.json:2:12"},
		{lit: "1e", loc: "config.json:2:12"},
		{lit: "-", loc: "config.json:2:12"},
		{lit: "NaN", loc: "config.json:2:12"},
		{lit: "Infinity", loc: "config.json:2:12"},
		{lit: "-Infinity", loc: "config.json:2:12"},
		{lit: ".inf", loc: "config.json:2:12"},
	}
	for _, tc := range cases {
		t.Run(tc.lit, func(t *testing.T) {
			t.Parallel()
			dir, _ := writeJSONNumberDoc(t, tc.lit)
			_, err := jsoncontract.ReadFS(os.DirFS(dir), "config.json")
			if err == nil {
				t.Fatalf("ReadFS(%s) error = nil, want ErrSyntax", tc.lit)
			}
			if !errors.Is(err, ashlar.ErrSyntax) {
				t.Errorf("error = %v, want ErrSyntax", err)
			}
			var ae *ashlar.Error
			if !errors.As(err, &ae) {
				t.Fatalf("error %T = %v, want *ashlar.Error", err, err)
			}
			if got := ae.Origin.Location(); got != tc.loc {
				t.Errorf("Location() = %q, want %q (error: %v)", got, tc.loc, err)
			}
			if !strings.HasPrefix(err.Error(), tc.loc) {
				t.Errorf("Error() = %q, want prefix %q", err.Error(), tc.loc)
			}
		})
	}
}

// jsonNumberDecodeCase is one JSON number decoded into one Go type. A nil
// want means DecodeStrict must fail with a located ErrValidation.
type jsonNumberDecodeCase struct {
	lit    string
	target string
	newDst func() any
	want   any
}

// jsonNumberDecodeCases lists every spelling and target pair the
// compatibility corpus names for JSON. The rows match the YAML table except
// for hex and octal, which JSON cannot express.
func jsonNumberDecodeCases() []jsonNumberDecodeCase {
	i := newJSONNumberField[int]()
	i64 := newJSONNumberField[int64]()
	u := newJSONNumberField[uint]()
	f := newJSONNumberField[float64]()
	jn := newJSONNumberField[json.Number]()
	return []jsonNumberDecodeCase{
		{"1", "int", i, 1},
		{"1", "int64", i64, int64(1)},
		{"1", "uint", u, uint(1)},
		{"1", "float64", f, 1.0},
		{"1", "json.Number", jn, json.Number("1")},

		{"1.0", "int", i, nil},
		{"1.0", "int64", i64, nil},
		{"1.0", "uint", u, nil},
		{"1.0", "float64", f, 1.0},
		{"1.0", "json.Number", jn, json.Number("1.0")},

		{"1e0", "int", i, nil},
		{"1e0", "int64", i64, nil},
		{"1e0", "float64", f, 1.0},
		{"1e0", "json.Number", jn, json.Number("1e0")},

		{"-0", "int", i, 0},
		{"-0", "int64", i64, int64(0)},
		{"-0", "uint", u, nil},
		{"-0", "float64", f, math.Copysign(0, -1)},
		{"-0", "json.Number", jn, json.Number("-0")},

		{"-1", "uint", u, nil},

		{"9223372036854775807", "int", i, math.MaxInt},
		{"9223372036854775807", "int64", i64, int64(math.MaxInt64)},
		{"9223372036854775807", "uint", u, uint(math.MaxInt64)},
		{"9223372036854775807", "float64", f, float64(math.MaxInt64)},
		{"9223372036854775807", "json.Number", jn, json.Number("9223372036854775807")},

		{"9223372036854775808", "int", i, nil},
		{"9223372036854775808", "int64", i64, nil},
		{"9223372036854775808", "uint", u, uint(1 << 63)},
		{"9223372036854775808", "float64", f, 9223372036854775808.0},
		{"9223372036854775808", "json.Number", jn, json.Number("9223372036854775808")},

		{"18446744073709551615", "int64", i64, nil},
		{"18446744073709551615", "uint", u, uint(math.MaxUint64)},
		{"18446744073709551615", "json.Number", jn, json.Number("18446744073709551615")},

		{"18446744073709551616", "int64", i64, nil},
		{"18446744073709551616", "uint", u, nil},
		{"18446744073709551616", "float64", f, 18446744073709551616.0},
		{"18446744073709551616", "json.Number", jn, json.Number("18446744073709551616")},

		{"1e400", "float64", f, nil},
		{"1e400", "json.Number", jn, json.Number("1e400")},
	}
}

// sameJSONNumber compares decoded numbers exactly, telling -0 from +0.
func sameJSONNumber(got, want any) bool {
	if gf, ok := got.(float64); ok {
		wf, ok := want.(float64)
		return ok && math.Float64bits(gf) == math.Float64bits(wf)
	}
	return reflect.DeepEqual(got, want)
}

// TestDecodeStrictNumbersIntoGoTypes checks that DecodeStrict of each corpus
// spelling into int, int64, uint, float64 and json.Number either yields the
// exact value or fails with an ErrValidation located at the number.
func TestDecodeStrictNumbersIntoGoTypes(t *testing.T) {
	t.Parallel()

	for _, tc := range jsonNumberDecodeCases() {
		t.Run(tc.lit+"/"+tc.target, func(t *testing.T) {
			t.Parallel()
			doc, _ := readJSONNumberDoc(t, tc.lit)
			dst := tc.newDst()
			err := doc.DecodeStrict(dst)
			if tc.want == nil {
				requireJSONLocatedError(t, err, ashlar.ErrValidation, jsonNumberPtr, jsonNumberLoc)
				return
			}
			if err != nil {
				t.Fatalf("DecodeStrict() error = %v, want %v", err, tc.want)
			}
			got := reflect.ValueOf(dst).Elem().Field(0).Interface()
			if !sameJSONNumber(got, tc.want) {
				t.Errorf("decoded %T(%v), want %T(%v)", got, got, tc.want, tc.want)
			}
		})
	}
}

// TestDecodeStrictAnyKeepsNumberLexemes checks that numbers decoded into any
// and map[string]any arrive as json.Number carrying the exact source lexeme.
func TestDecodeStrictAnyKeepsNumberLexemes(t *testing.T) {
	t.Parallel()

	const raw = `{"int": 1, "decimal": 1.0, "exp": 1e0, "neg_zero": -0, "big": 18446744073709551616}`
	want := map[string]any{
		"int":      json.Number("1"),
		"decimal":  json.Number("1.0"),
		"exp":      json.Number("1e0"),
		"neg_zero": json.Number("-0"),
		"big":      json.Number("18446744073709551616"),
	}
	cases := []struct {
		name   string
		newDst func() any
		get    func(dst any) any
	}{
		{
			name:   "map",
			newDst: func() any { return new(map[string]any) },
			get:    func(dst any) any { return *dst.(*map[string]any) },
		},
		{
			name:   "any",
			newDst: func() any { return new(any) },
			get:    func(dst any) any { return *dst.(*any) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			doc, err := jsoncontract.ReadFS(os.DirFS(dir), "config.json")
			if err != nil {
				t.Fatalf("ReadFS() error = %v", err)
			}
			dst := tc.newDst()
			if err := doc.DecodeStrict(dst); err != nil {
				t.Fatalf("DecodeStrict() error = %v", err)
			}
			got, ok := tc.get(dst).(map[string]any)
			if !ok {
				t.Fatalf("decoded %T, want map[string]any", tc.get(dst))
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("decoded %#v, want %#v", got, want)
			}
		})
	}
}

// TestDecodeStrictNumberMismatchMessages checks that jsoncontract reports a
// number that does not fit its target with the same fixed messages as
// yamlconfig, located at the value, without quoting the number.
func TestDecodeStrictNumberMismatchMessages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lit    string
		newDst func() any
		want   string
	}{
		{"300", newJSONNumberField[int8](), "number out of range for int8"},
		{"1.5", newJSONNumberField[int](), "number requires integer notation for int"},
		{"1e0", newJSONNumberField[int64](), "number requires integer notation for int64"},
		{"-7", newJSONNumberField[uint16](), "negative sign is not allowed for uint16"},
		{"70000", newJSONNumberField[uint16](), "number out of range for uint16"},
		{"1e40", newJSONNumberField[float32](), "number out of range for float32"},
	}
	for _, tc := range cases {
		t.Run(tc.lit, func(t *testing.T) {
			t.Parallel()
			doc, _ := readJSONNumberDoc(t, tc.lit)
			err := doc.DecodeStrict(tc.newDst())
			requireJSONLocatedError(t, err, ashlar.ErrValidation, "/value", "config.json:2:12")
			var ae *ashlar.Error
			if errors.As(err, &ae) && ae.Message != tc.want {
				t.Errorf("Message = %q, want %q", ae.Message, tc.want)
			}
		})
	}
}

// TestReadMalformedValueSuffixPointer checks that a number or literal
// immediately followed by a byte that can only continue a malformed value is
// reported at that value's pointer, located at the offending byte, without
// quoting it. Whitespace-separated garbage stays a separator error at the
// container, and at the root it stays trailing content.
func TestReadMalformedValueSuffixPointer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		doc  string
		kind error
		ptr  ashlar.Pointer
		loc  string
		msg  string
	}{
		{"hex_member", `{"v": 0x1F}`, ashlar.ErrSyntax, "/v", "c.json:1:8", "invalid number"},
		{"octal_member", `{"v": 0o17}`, ashlar.ErrSyntax, "/v", "c.json:1:8", "invalid number"},
		{"number_then_literal", `{"v": 1true}`, ashlar.ErrSyntax, "/v", "c.json:1:8", "invalid number"},
		{"double_fraction", `{"v": 1.5.5}`, ashlar.ErrSyntax, "/v", "c.json:1:10", "invalid number"},
		{"literal_suffix", `{"v": truex}`, ashlar.ErrSyntax, "/v", "c.json:1:11", "invalid literal"},
		{"null_suffix", `{"v": nullx}`, ashlar.ErrSyntax, "/v", "c.json:1:11", "invalid literal"},
		{"nested", `{"a": {"v": 0x1F}}`, ashlar.ErrSyntax, "/a/v", "c.json:1:14", "invalid number"},
		{"array_first", `{"a": [0x1F]}`, ashlar.ErrSyntax, "/a/0", "c.json:1:9", "invalid number"},
		{"array_later", `{"a": [1, 2, 0x1F]}`, ashlar.ErrSyntax, "/a/2", "c.json:1:15", "invalid number"},
		{"root", `0x1F`, ashlar.ErrSyntax, "", "c.json:1:2", "invalid number"},
		{"missing_comma_object", `{"v": 1 2}`, ashlar.ErrSyntax, "", "c.json:1:9", "expected ',' or '}' after object member"},
		{"string_after_number", `{"v": 1"a"}`, ashlar.ErrSyntax, "", "c.json:1:8", "expected ',' or '}' after object member"},
		{"missing_comma_array", `[1 2]`, ashlar.ErrSyntax, "", "c.json:1:4", "expected ',' or ']' after array element"},
		{"root_trailing", `1 2`, ashlar.ErrTrailingContent, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := jsoncontract.Read("c.json", []byte(tc.doc))
			if !errors.Is(err, tc.kind) {
				t.Fatalf("error = %v, want %v", err, tc.kind)
			}
			if tc.msg == "" {
				return
			}
			requireJSONLocatedError(t, err, tc.kind, tc.ptr, tc.loc)
			var ae *ashlar.Error
			if errors.As(err, &ae) && ae.Message != tc.msg {
				t.Errorf("Message = %q, want %q", ae.Message, tc.msg)
			}
		})
	}
}
