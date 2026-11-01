package ashlar_test

import (
	"testing"

	"github.com/mattjoyce/ashlar"
)

// TestPresenceString checks the name of every Presence value, including one
// outside the defined set.
func TestPresenceString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		p    ashlar.Presence
		want string
	}{
		{ashlar.PresenceOmitted, "omitted"},
		{ashlar.PresenceNull, "null"},
		{ashlar.PresencePresent, "present"},
		{ashlar.Presence(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("Presence(%d).String() = %q, want %q", int(tc.p), got, tc.want)
		}
	}
}

// TestSourceSpanValidAndString checks which spans count as valid and how
// each renders, including a line-only position (Column 0).
func TestSourceSpanValidAndString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		span      ashlar.SourceSpan
		wantValid bool
		wantStr   string
	}{
		{"empty", ashlar.SourceSpan{}, false, ""},
		{"source_only", ashlar.SourceSpan{Source: "cfg.yaml"}, true, "cfg.yaml"},
		{"full", ashlar.SourceSpan{Source: "cfg.yaml", Start: ashlar.SourcePos{Line: 3, Column: 5}}, true, "cfg.yaml:3:5"},
		{"line_only", ashlar.SourceSpan{Source: "cfg.yaml", Start: ashlar.SourcePos{Line: 3}}, true, "cfg.yaml:3"},
		{"position_only", ashlar.SourceSpan{Start: ashlar.SourcePos{Line: 2, Column: 1}}, true, "2:1"},
		{"column_without_line", ashlar.SourceSpan{Start: ashlar.SourcePos{Column: 4}}, false, ""},
		{"negative_column", ashlar.SourceSpan{Start: ashlar.SourcePos{Line: 1, Column: -1}}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.span.Valid(); got != tc.wantValid {
				t.Errorf("Valid() = %v, want %v", got, tc.wantValid)
			}
			if got := tc.span.String(); got != tc.wantStr {
				t.Errorf("String() = %q, want %q", got, tc.wantStr)
			}
		})
	}
}
