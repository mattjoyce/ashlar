package yamlconfig_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

// numberDoc is the fixture every numeric case is written into. The comment on
// line 1 puts the value at line 2, column 8, so an off-by-one in either
// coordinate shows up.
const numberDoc = "# numeric fidelity\nvalue: %s\n"

// numberLoc is the file:line:column of the value in numberDoc.
const numberLoc = "config.yaml:2:8"

// numberPtr is the pointer of the value in numberDoc.
const numberPtr = ashlar.Pointer("/value")

// numberField is a one-field decode target for a single YAML number.
type numberField[T any] struct {
	Value T `yaml:"value"`
}

// newNumberField returns a constructor for a fresh *numberField[T].
func newNumberField[T any]() func() any {
	return func() any { return new(numberField[T]) }
}

// writeYAMLFile writes content to name inside a fresh t.TempDir and returns
// the directory.
func writeYAMLFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
	return dir
}

// writeNumberDoc writes numberDoc with lit substituted to config.yaml.
func writeNumberDoc(t *testing.T, lit string) string {
	t.Helper()
	return writeYAMLFile(t, "config.yaml", strings.Replace(numberDoc, "%s", lit, 1))
}

// readYAMLFile reads name from dir through the public ReadFS entry point.
func readYAMLFile(t *testing.T, dir, name string) ashlar.Value {
	t.Helper()
	root, err := yamlconfig.ReadFS(os.DirFS(dir), name)
	if err != nil {
		t.Fatalf("ReadFS(%s) error = %v", name, err)
	}
	return root
}

// decodeEntryPoints names the two public YAML decode paths. Every decode case
// runs through both, and both must agree.
var decodeEntryPoints = []struct {
	name   string
	decode func(t *testing.T, dir, file string, dst any) error
}{
	{
		name: "DecodeValueStrict",
		decode: func(t *testing.T, dir, file string, dst any) error {
			t.Helper()
			return yamlconfig.DecodeValueStrict(readYAMLFile(t, dir, file), dst)
		},
	},
	{
		name: "Resolved.DecodeStrict",
		decode: func(t *testing.T, dir, file string, dst any) error {
			t.Helper()
			res, err := yamlconfig.Resolve(t.Context(), os.DirFS(dir), file, yamlconfig.ResolveOptions{})
			if err != nil {
				t.Fatalf("Resolve(%s) error = %v", file, err)
			}
			return res.DecodeStrict(dst)
		},
	},
}

// requireLocatedError fails t unless err is an *ashlar.Error matching kind,
// carrying exactly ptr and loc, and rendering them as the prefix of Error().
// It returns the error's Message.
func requirePointerError(t *testing.T, err error, kind error, ptr ashlar.Pointer, loc string) (message string) {
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
	prefix := loc + ": "
	if ptr != "" {
		prefix = loc + " (" + string(ptr) + "): "
	}
	if !strings.HasPrefix(err.Error(), prefix) {
		t.Errorf("Error() = %q, want prefix %q", err.Error(), prefix)
	}
	return ae.Message
}

// TestYAMLNumberLexemesPreserved checks that each YAML number spelling the
// compatibility corpus names survives Read as a KindNumber node whose
// NumberLexeme is the exact source spelling, located at file:line:column.
func TestYAMLNumberLexemesPreserved(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		lit  string
	}{
		{name: "integer", lit: "1"},
		{name: "decimal_with_zero_fraction", lit: "1.0"},
		{name: "exponent", lit: "1e0"},
		{name: "upper_exponent", lit: "1E3"},
		{name: "negative_zero", lit: "-0"},
		{name: "negative_zero_float", lit: "-0.0"},
		{name: "max_int64", lit: "9223372036854775807"},
		{name: "two_pow_63", lit: "9223372036854775808"},
		{name: "max_uint64", lit: "18446744073709551615"},
		{name: "two_pow_64", lit: "18446744073709551616"},
		{name: "past_uint64", lit: "99999999999999999999999"},
		{name: "float_past_float64_range", lit: "1e400"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := readYAMLFile(t, writeNumberDoc(t, tc.lit), "config.yaml")
			v := root.At(numberPtr)
			if v.Kind() != ashlar.KindNumber {
				t.Fatalf("Kind() = %v, want number", v.Kind())
			}
			lex, ok := v.NumberLexeme()
			if !ok || lex != tc.lit {
				t.Errorf("NumberLexeme() = %q, %v; want %q, true", lex, ok, tc.lit)
			}
			jn, ok := v.JSONNumber()
			if !ok || string(jn) != tc.lit {
				t.Errorf("JSONNumber() = %q, %v; want %q, true", jn, ok, tc.lit)
			}
			if got := v.Provenance().Effective.String(); got != numberLoc {
				t.Errorf("Origin = %q, want %q", got, numberLoc)
			}
		})
	}
}

// TestYAMLNumberSpellingsOutsideJSONGrammar checks that YAML number spellings
// with no JSON equivalent (signs, bare points, radix prefixes, digit
// separators) read as KindNumber nodes with the exact numeric value.
func TestYAMLNumberSpellingsOutsideJSONGrammar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		lit       string
		wantInt   *int64
		wantUint  *uint64
		wantFloat *float64
	}{
		{name: "hex", lit: "0x1F", wantInt: ptrTo[int64](31)},
		{name: "negative_hex", lit: "-0x1F", wantInt: ptrTo[int64](-31)},
		{name: "octal_0o", lit: "0o17", wantInt: ptrTo[int64](15)},
		{name: "plus_sign_int", lit: "+1", wantInt: ptrTo[int64](1)},
		{name: "digit_separator", lit: "1_000", wantInt: ptrTo[int64](1000)},
		{name: "hex_max_uint64", lit: "0xFFFFFFFFFFFFFFFF", wantUint: ptrTo[uint64](math.MaxUint64)},
		{name: "leading_point", lit: ".5", wantFloat: ptrTo(0.5)},
		{name: "trailing_point", lit: "1.", wantFloat: ptrTo(1.0)},
		{name: "plus_sign_float", lit: "+1.5", wantFloat: ptrTo(1.5)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := readYAMLFile(t, writeNumberDoc(t, tc.lit), "config.yaml")
			v := root.At(numberPtr)
			if v.Kind() != ashlar.KindNumber {
				t.Fatalf("Kind() = %v, want number", v.Kind())
			}
			if got := v.Provenance().Effective.String(); got != numberLoc {
				t.Errorf("Origin = %q, want %q", got, numberLoc)
			}
			switch {
			case tc.wantInt != nil:
				var dst numberField[int64]
				if err := yamlconfig.DecodeValueStrict(root, &dst); err != nil {
					t.Fatalf("decode int64 error = %v", err)
				}
				if dst.Value != *tc.wantInt {
					t.Errorf("int64 = %d, want %d", dst.Value, *tc.wantInt)
				}
			case tc.wantUint != nil:
				var dst numberField[uint64]
				if err := yamlconfig.DecodeValueStrict(root, &dst); err != nil {
					t.Fatalf("decode uint64 error = %v", err)
				}
				if dst.Value != *tc.wantUint {
					t.Errorf("uint64 = %d, want %d", dst.Value, *tc.wantUint)
				}
			default:
				var dst numberField[float64]
				if err := yamlconfig.DecodeValueStrict(root, &dst); err != nil {
					t.Fatalf("decode float64 error = %v", err)
				}
				if dst.Value != *tc.wantFloat {
					t.Errorf("float64 = %v, want %v", dst.Value, *tc.wantFloat)
				}
			}
		})
	}
}

// ptrTo returns a pointer to v.
func ptrTo[T any](v T) *T { return &v }

// TestYAMLNonFiniteFloats checks the YAML 1.2 special floats. Each must either
// be kept with its exact spelling, or be rejected with an ErrSyntax error that
// points at the node (pointer and file:line:column) and quotes the source
// spelling rather than a Go rendering of the parsed float.
func TestYAMLNonFiniteFloats(t *testing.T) {
	t.Parallel()

	for _, lit := range []string{".inf", "-.inf", "+.inf", ".Inf", ".INF", ".nan", ".NaN", ".NAN"} {
		t.Run(lit, func(t *testing.T) {
			t.Parallel()
			dir := writeNumberDoc(t, lit)
			root, err := yamlconfig.ReadFS(os.DirFS(dir), "config.yaml")
			if !root.IsOmitted() {
				t.Errorf("ReadFS() root = %v, want omitted on error", root.Kind())
			}
			msg := requirePointerError(t, err, ashlar.ErrSyntax, numberPtr, numberLoc)
			if strings.Contains(msg, lit) {
				t.Errorf("Message = %q quotes the scalar %q; load errors must withhold it", msg, lit)
			}
		})
	}
}

// TestYAMLExplicitNumericTags checks explicitly tagged scalars: a valid
// !!int or !!float keeps its lexeme, and one whose text is not a number of
// that tag fails with a located ErrSyntax naming the bad text.
func TestYAMLExplicitNumericTags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		lit     string
		wantLex string
		wantMsg string
	}{
		{name: "float_tag_on_integer_text", lit: "!!float 1", wantLex: "1"},
		{name: "int_tag_on_integer_text", lit: "!!int 42", wantLex: "42"},
		{name: "float_tag_on_word", lit: "!!float abc", wantMsg: "invalid YAML float"},
		{name: "int_tag_on_word", lit: "!!int abc", wantMsg: "invalid YAML integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeNumberDoc(t, tc.lit)
			root, err := yamlconfig.ReadFS(os.DirFS(dir), "config.yaml")
			if tc.wantMsg != "" {
				msg := requirePointerError(t, err, ashlar.ErrSyntax, numberPtr, numberLoc)
				if msg != tc.wantMsg {
					t.Errorf("Message = %q, want %q", msg, tc.wantMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadFS() error = %v", err)
			}
			lex, ok := root.At(numberPtr).NumberLexeme()
			if !ok || lex != tc.wantLex {
				t.Errorf("NumberLexeme() = %q, %v; want %q, true", lex, ok, tc.wantLex)
			}
		})
	}
}

// numberDecodeCase is one YAML or JSON number decoded into one Go type. A nil
// want means the decode must fail with a located ErrValidation.
type numberDecodeCase struct {
	lit    string
	target string
	newDst func() any
	want   any
}

// yamlNumberDecodeCases lists every spelling and target pair the compatibility
// corpus names for YAML.
func yamlNumberDecodeCases() []numberDecodeCase {
	i := newNumberField[int]()
	i64 := newNumberField[int64]()
	u := newNumberField[uint]()
	f := newNumberField[float64]()
	jn := newNumberField[json.Number]()
	return []numberDecodeCase{
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

		{"0x1F", "int", i, 31},
		{"0x1F", "uint", u, uint(31)},
		{"0o17", "int", i, 15},
	}
}

// fieldValue returns the Value field of a *numberField[T] destination.
func fieldValue(dst any) any {
	return reflect.ValueOf(dst).Elem().Field(0).Interface()
}

// sameNumber compares decoded numbers exactly, telling -0 from +0.
func sameNumber(got, want any) bool {
	if gf, ok := got.(float64); ok {
		wf, ok := want.(float64)
		return ok && math.Float64bits(gf) == math.Float64bits(wf)
	}
	return reflect.DeepEqual(got, want)
}

// TestYAMLDecodeNumbersIntoGoTypes checks that decoding each corpus spelling
// into int, int64, uint, float64 and json.Number either yields the exact value
// or fails with an ErrValidation located at the number's file:line:column.
func TestYAMLDecodeNumbersIntoGoTypes(t *testing.T) {
	t.Parallel()

	for _, tc := range yamlNumberDecodeCases() {
		for _, ep := range decodeEntryPoints {
			t.Run(ep.name+"/"+tc.lit+"/"+tc.target, func(t *testing.T) {
				t.Parallel()
				dir := writeNumberDoc(t, tc.lit)
				dst := tc.newDst()
				err := ep.decode(t, dir, "config.yaml", dst)
				if tc.want == nil {
					requirePointerError(t, err, ashlar.ErrValidation, numberPtr, numberLoc)
					return
				}
				if err != nil {
					t.Fatalf("decode error = %v, want %v", err, tc.want)
				}
				if got := fieldValue(dst); !sameNumber(got, tc.want) {
					t.Errorf("decoded %T(%v), want %T(%v)", got, got, tc.want, tc.want)
				}
			})
		}
	}
}

// rawJSONRecorder is a json.Unmarshaler that records the bytes it receives.
type rawJSONRecorder struct {
	raw string
}

// UnmarshalJSON records data.
func (r *rawJSONRecorder) UnmarshalJSON(data []byte) error {
	r.raw = string(data)
	return nil
}

// TestYAMLJSONUnmarshalerReceivesExactNumberLexeme checks that a
// json.Unmarshaler target receives the number exactly as written, so a
// big-number or decimal type sees no float64 rounding.
func TestYAMLJSONUnmarshalerReceivesExactNumberLexeme(t *testing.T) {
	t.Parallel()

	for _, lit := range []string{
		"1",
		"1.0",
		"1e0",
		"-0",
		"-0.0",
		"9223372036854775808",
		"18446744073709551617",
		"123456789012345678901234567890",
		"0.1000000000000000055511151231257827",
	} {
		for _, ep := range decodeEntryPoints {
			t.Run(ep.name+"/"+lit, func(t *testing.T) {
				t.Parallel()
				dir := writeNumberDoc(t, lit)
				var dst numberField[rawJSONRecorder]
				if err := ep.decode(t, dir, "config.yaml", &dst); err != nil {
					t.Fatalf("decode error = %v", err)
				}
				if dst.Value.raw != lit {
					t.Errorf("UnmarshalJSON received %q, want %q", dst.Value.raw, lit)
				}
			})
		}
	}
}

// TestYAMLNonJSONSpellingsNormalizeToDecimal checks that YAML integer spellings
// outside the JSON number grammar keep their value but not their spelling:
// Ashlar numbers hold JSON lexemes, so hex, octal, signs and digit separators
// become the JSON decimal form (SPEC.md §3.1).
func TestYAMLNonJSONSpellingsNormalizeToDecimal(t *testing.T) {
	t.Parallel()

	cases := []struct{ lit, wantLex string }{
		{"0x1F", "31"},
		{"-0x1F", "-31"},
		{"0o17", "15"},
		{"+1", "1"},
		{"1_000", "1000"},
	}
	for _, tc := range cases {
		t.Run(tc.lit, func(t *testing.T) {
			t.Parallel()
			root := readYAMLFile(t, writeNumberDoc(t, tc.lit), "config.yaml")
			lex, ok := root.At(numberPtr).NumberLexeme()
			if !ok || string(lex) != tc.wantLex {
				t.Errorf("NumberLexeme() = %q, %v; want %q, true", lex, ok, tc.wantLex)
			}
		})
	}
}

// TestYAMLScalarSyntaxErrorWithholdsValue checks that a malformed tagged
// number fails with a located ErrSyntax that never shows the scalar. The
// loader runs before any caller redaction policy, so it withholds the value
// under every key: one DefaultRedactor flags, one only a custom policy would
// flag, and one carrying characters that %q escapes.
func TestYAMLScalarSyntaxErrorWithholdsValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		line   string
		secret string
	}{
		{"default_sensitive_key", "password: !!int hunter2-sekrit", "hunter2-sekrit"},
		{"custom_sensitive_key", "pin: !!int hidden-value", "hidden-value"},
		{"escaped_characters", `password: !!float "line1\nline2"`, "line1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			doc := "app: demo\n" + tc.line + "\n"
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(doc), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := yamlconfig.ReadFS(os.DirFS(dir), "config.yaml")
			if !errors.Is(err, ashlar.ErrSyntax) {
				t.Fatalf("ReadFS() error = %v, want ErrSyntax", err)
			}
			for _, format := range []string{"%v", "%+v", "%#v", "%q"} {
				if got := fmt.Sprintf(format, err); strings.Contains(got, tc.secret) {
					t.Errorf("format %q shows %q: %s", format, tc.secret, got)
				}
			}
		})
	}
}

// TestYAMLNumberMismatchMessages checks that a number that does not fit its
// target names the reason with a fixed message and never quotes the number.
func TestYAMLNumberMismatchMessages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		doc    string
		newDst func() any
		want   string
	}{
		{"value: 300\n", newNumberField[int8](), "number out of range for int8"},
		{"value: 1.5\n", newNumberField[int](), "number requires integer notation for int"},
		{"value: 1.0\n", newNumberField[int64](), "number requires integer notation for int64"},
		{"value: -7\n", newNumberField[uint16](), "negative sign is not allowed for uint16"},
		{"value: 70000\n", newNumberField[uint16](), "number out of range for uint16"},
		{"value: 1e40\n", newNumberField[float32](), "number out of range for float32"},
		{"value: 1e400\n", newNumberField[float64](), "number out of range for float64"},
		{"value: \"7\"\n", newNumberField[int](), "expected int, got string"},
	}
	for _, tc := range cases {
		for _, ep := range decodeEntryPoints {
			t.Run(ep.name+"/"+strings.TrimSpace(tc.doc), func(t *testing.T) {
				t.Parallel()
				dir := writeYAMLFile(t, "config.yaml", tc.doc)
				err := ep.decode(t, dir, "config.yaml", tc.newDst())
				if msg := requirePointerError(t, err, ashlar.ErrValidation, "/value", "config.yaml:1:8"); msg != tc.want {
					t.Errorf("Message = %q, want %q", msg, tc.want)
				}
			})
		}
	}
}

// TestYAMLLeadingZeroIntegersRejected checks that integer spellings with a
// leading zero fail with a located ErrSyntax and a fixed message that does not
// quote the scalar, while unambiguous spellings and strings still load.
func TestYAMLLeadingZeroIntegersRejected(t *testing.T) {
	t.Parallel()

	rejected := []string{
		"00", "007", "017", "08", "09", "0017", "0_17", "+017", "-017",
		"!!int 017", "!!float 017", "!!float 08", "!!int \"017\"",
		"0123456789012345678901234567890",
	}
	for _, lit := range rejected {
		t.Run("reject/"+lit, func(t *testing.T) {
			t.Parallel()
			dir := writeNumberDoc(t, lit)
			_, err := yamlconfig.ReadFS(os.DirFS(dir), "config.yaml")
			msg := requirePointerError(t, err, ashlar.ErrSyntax, "/value", numberLoc)
			if !strings.HasPrefix(msg, "leading-zero integer spelling is not allowed") {
				t.Errorf("Message = %q, want the fixed leading-zero message", msg)
			}
			bare := strings.TrimPrefix(strings.TrimPrefix(lit, "!!int "), "!!float ")
			if strings.Contains(err.Error(), strings.Trim(bare, "\"")) {
				t.Errorf("Error() = %q, quotes the scalar %q", err.Error(), bare)
			}
		})
	}

	accepted := []struct {
		lit     string
		kind    ashlar.Kind
		wantLex string
	}{
		{"0", ashlar.KindNumber, "0"},
		{"-0", ashlar.KindNumber, "-0"},
		{"0.5", ashlar.KindNumber, "0.5"},
		{"0e1", ashlar.KindNumber, "0e1"},
		{"0o17", ashlar.KindNumber, "15"},
		{"0x1F", ashlar.KindNumber, "31"},
		{"010.5", ashlar.KindNumber, "10.5"},
		{"1_000", ashlar.KindNumber, "1000"},
		{"!!str 017", ashlar.KindString, "017"},
		{"\"017\"", ashlar.KindString, "017"},
		{"'017'", ashlar.KindString, "017"},
	}
	for _, tc := range accepted {
		t.Run("accept/"+tc.lit, func(t *testing.T) {
			t.Parallel()
			dir := writeNumberDoc(t, tc.lit)
			v := readYAMLFile(t, dir, "config.yaml").At("/value")
			if v.Kind() != tc.kind {
				t.Fatalf("Kind() = %v, want %v", v.Kind(), tc.kind)
			}
			got, _ := v.NumberLexeme()
			if tc.kind == ashlar.KindString {
				s, _ := v.StringVal()
				got = s
			}
			if string(got) != tc.wantLex {
				t.Errorf("value = %q, want %q", got, tc.wantLex)
			}
		})
	}
}
