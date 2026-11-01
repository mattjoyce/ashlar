package numfit

import (
	"reflect"
	"testing"
)

// TestProblem covers every failure case for each numeric width, and the
// boundary values that fit.
func TestProblem(t *testing.T) {
	t.Parallel()

	i8 := reflect.TypeFor[int8]()
	i64 := reflect.TypeFor[int64]()
	u8 := reflect.TypeFor[uint8]()
	u64 := reflect.TypeFor[uint64]()
	f32 := reflect.TypeFor[float32]()
	f64 := reflect.TypeFor[float64]()
	str := reflect.TypeFor[string]()

	cases := []struct {
		lexeme string
		t      reflect.Type
		want   string
	}{
		{"127", i8, ""},
		{"-128", i8, ""},
		{"128", i8, "number out of range for int8"},
		{"-129", i8, "number out of range for int8"},
		{"9223372036854775807", i64, ""},
		{"9223372036854775808", i64, "number out of range for int64"},
		{"1.5", i64, "number requires integer notation for int64"},
		{"1.0", i64, "number requires integer notation for int64"},
		{"1e0", i64, "number requires integer notation for int64"},
		{"1E2", i64, "number requires integer notation for int64"},
		{"255", u8, ""},
		{"256", u8, "number out of range for uint8"},
		{"-1", u8, "negative sign is not allowed for uint8"},
		{"-0", u8, "negative sign is not allowed for uint8"},
		{"-1.5", u8, "number requires integer notation for uint8"},
		{"18446744073709551615", u64, ""},
		{"18446744073709551616", u64, "number out of range for uint64"},
		{"3.4e38", f32, ""},
		{"1e39", f32, "number out of range for float32"},
		{"1e308", f64, ""},
		{"1e309", f64, "number out of range for float64"},
		{"-1e309", f64, "number out of range for float64"},
		{"1.5", f64, ""},
		{"1", str, ""},
	}
	for _, tc := range cases {
		if got := Problem(tc.lexeme, tc.t); got != tc.want {
			t.Errorf("Problem(%q, %s) = %q, want %q", tc.lexeme, tc.t, got, tc.want)
		}
	}
}
