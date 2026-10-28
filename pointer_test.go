package ashlar_test

import (
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

func TestParseArrayIndex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tok   string
		want  int
		valid bool
	}{
		{tok: "0", want: 0, valid: true},
		{tok: "1", want: 1, valid: true},
		{tok: "10", want: 10, valid: true},
		{tok: "907", want: 907, valid: true},
		{tok: "", want: 0, valid: false},
		{tok: "-", want: 0, valid: false},
		{tok: "00", want: 0, valid: false},
		{tok: "01", want: 0, valid: false},
		{tok: "+0", want: 0, valid: false},
		{tok: "-0", want: 0, valid: false},
		{tok: "-1", want: 0, valid: false},
		{tok: "+1", want: 0, valid: false},
		{tok: "1e0", want: 0, valid: false},
		{tok: " 1", want: 0, valid: false},
		{tok: "1 ", want: 0, valid: false},
		{tok: "0x1", want: 0, valid: false},
		{tok: "１", want: 0, valid: false},
		{tok: "99999999999999999999", want: 0, valid: false},
	}
	for _, tc := range tests {
		got, ok := ashlar.ParseArrayIndex(tc.tok)
		if ok != tc.valid || got != tc.want {
			t.Errorf("ParseArrayIndex(%q) = (%d, %v), want (%d, %v)", tc.tok, got, ok, tc.want, tc.valid)
		}
	}
}

func TestValueAtArrayIndexGrammar(t *testing.T) {
	t.Parallel()

	root, err := yamlconfig.Read("a.yaml", []byte("items: [a, b]\n\"+0\": x\n"))
	if err != nil {
		t.Fatal(err)
	}

	// Valid array indices are present.
	if v := root.At("/items/0"); v.IsOmitted() {
		t.Errorf("At(/items/0) is omitted, want 'a'")
	} else if s, ok := v.StringVal(); !ok || s != "a" {
		t.Errorf("At(/items/0) = %v, want 'a'", v)
	}
	if v := root.At("/items/1"); v.IsOmitted() {
		t.Errorf("At(/items/1) is omitted, want 'b'")
	} else if s, ok := v.StringVal(); !ok || s != "b" {
		t.Errorf("At(/items/1) = %v, want 'b'", v)
	}

	// Invalid array indices are omitted.
	if v := root.At("/items/+0"); !v.IsOmitted() {
		t.Errorf("At(/items/+0) = %v, want omitted", v)
	}
	if v := root.At("/items/-0"); !v.IsOmitted() {
		t.Errorf("At(/items/-0) = %v, want omitted", v)
	}
	if v := root.At("/items/00"); !v.IsOmitted() {
		t.Errorf("At(/items/00) = %v, want omitted", v)
	}
	if v := root.At("/items/2"); !v.IsOmitted() {
		t.Errorf("At(/items/2) = %v, want omitted", v)
	}
	if v := root.At("/items/-"); !v.IsOmitted() {
		t.Errorf("At(/items/-) = %v, want omitted", v)
	}

	// Object keys are unrestricted: "/+0" addresses the key "+0".
	if v := root.At("/+0"); v.IsOmitted() {
		t.Errorf("At(/+0) is omitted, want 'x'")
	} else if s, ok := v.StringVal(); !ok || s != "x" {
		t.Errorf("At(/+0) = %v, want 'x'", v)
	}
}
