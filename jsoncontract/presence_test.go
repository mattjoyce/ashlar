package jsoncontract_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
)

// TestPresenceThroughJSONContract checks that a JSON contract read from a real
// file keeps an omitted key, explicit false, 0, "", [], {}, and null apart at
// the Value level, each located at its own file:line:column.
func TestPresenceThroughJSONContract(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	raw := `{
  "flag_false": false,
  "count_zero": 0,
  "name_empty": "",
  "list_empty": [],
  "map_empty": {},
  "value_null": null,
  "nested": {"inner_null": null, "inner_list": []}
}
`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest.json) error = %v", err)
	}
	doc, err := jsoncontract.ReadFS(os.DirFS(dir), "manifest.json")
	if err != nil {
		t.Fatalf("ReadFS(manifest.json) error = %v", err)
	}
	root := doc.Root()

	tests := []struct {
		name     string
		ptr      ashlar.Pointer
		presence ashlar.Presence
		kind     ashlar.Kind
		origin   string // expected Effective.String(), or "" for omitted
		content  func(t *testing.T, v ashlar.Value)
	}{
		{"explicit false", ashlar.NewPointer("flag_false"), ashlar.PresencePresent, ashlar.KindBool, "manifest.json:2:17",
			func(t *testing.T, v ashlar.Value) {
				if b, ok := v.Bool(); !ok || b {
					t.Errorf("Bool() = (%v, %v), want (false, true)", b, ok)
				}
			}},
		{"explicit zero", ashlar.NewPointer("count_zero"), ashlar.PresencePresent, ashlar.KindNumber, "manifest.json:3:17",
			func(t *testing.T, v ashlar.Value) {
				if lex, ok := v.NumberLexeme(); !ok || lex != "0" {
					t.Errorf("NumberLexeme() = (%q, %v), want (\"0\", true)", lex, ok)
				}
			}},
		{"explicit empty string", ashlar.NewPointer("name_empty"), ashlar.PresencePresent, ashlar.KindString, "manifest.json:4:17",
			func(t *testing.T, v ashlar.Value) {
				if s, ok := v.StringVal(); !ok || s != "" {
					t.Errorf("StringVal() = (%q, %v), want (\"\", true)", s, ok)
				}
			}},
		{"explicit empty list", ashlar.NewPointer("list_empty"), ashlar.PresencePresent, ashlar.KindArray, "manifest.json:5:17",
			func(t *testing.T, v ashlar.Value) {
				if v.Len() != 0 {
					t.Errorf("Len() = %d, want 0", v.Len())
				}
			}},
		{"explicit empty map", ashlar.NewPointer("map_empty"), ashlar.PresencePresent, ashlar.KindObject, "manifest.json:6:16",
			func(t *testing.T, v ashlar.Value) {
				if v.Len() != 0 || v.Keys() != nil {
					t.Errorf("Len(), Keys() = %d, %v, want 0, nil", v.Len(), v.Keys())
				}
			}},
		{"null", ashlar.NewPointer("value_null"), ashlar.PresenceNull, ashlar.KindNull, "manifest.json:7:17", nil},
		{"nested null", ashlar.NewPointer("nested", "inner_null"), ashlar.PresenceNull, ashlar.KindNull, "manifest.json:8:28", nil},
		{"nested empty list", ashlar.NewPointer("nested", "inner_list"), ashlar.PresencePresent, ashlar.KindArray, "manifest.json:8:48", nil},
		{"omitted top-level key", ashlar.NewPointer("missing"), ashlar.PresenceOmitted, ashlar.KindInvalid, "", nil},
		{"omitted key under empty map", ashlar.NewPointer("map_empty", "missing"), ashlar.PresenceOmitted, ashlar.KindInvalid, "", nil},
		{"omitted key under null", ashlar.NewPointer("value_null", "missing"), ashlar.PresenceOmitted, ashlar.KindInvalid, "", nil},
		{"omitted index in empty list", ashlar.NewPointer("list_empty", "0"), ashlar.PresenceOmitted, ashlar.KindInvalid, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := root.PresenceAt(tt.ptr); got != tt.presence {
				t.Errorf("PresenceAt(%s) = %v, want %v", tt.ptr, got, tt.presence)
			}
			node := root.At(tt.ptr)
			if got := node.Kind(); got != tt.kind {
				t.Errorf("At(%s).Kind() = %v, want %v", tt.ptr, got, tt.kind)
			}
			if node.IsOmitted() != (tt.presence == ashlar.PresenceOmitted) ||
				node.IsNull() != (tt.presence == ashlar.PresenceNull) ||
				node.IsPresent() != (tt.presence == ashlar.PresencePresent) {
				t.Errorf("At(%s): IsOmitted/IsNull/IsPresent = %v/%v/%v, inconsistent with %v",
					tt.ptr, node.IsOmitted(), node.IsNull(), node.IsPresent(), tt.presence)
			}
			if got := node.Provenance().Effective.String(); tt.origin != "" && got != tt.origin {
				t.Errorf("At(%s) origin = %q, want %q", tt.ptr, got, tt.origin)
			}
			if tt.content != nil {
				tt.content(t, node)
			}
		})
	}

	if _, ok := root.Lookup("value_null"); !ok {
		t.Error(`Lookup("value_null") ok = false, want true for an explicit null`)
	}
	if _, ok := root.Lookup("missing"); ok {
		t.Error(`Lookup("missing") ok = true, want false for an omitted key`)
	}
}

// jsonPresenceTarget is a decode destination whose fields can each tell an
// omitted key (field untouched), null (field reset), and a zero or empty value
// (field set to a non-nil zero) apart.
type jsonPresenceTarget struct {
	Marker   int               `json:"marker"`
	Flag     *bool             `json:"flag"`
	Count    *int              `json:"count"`
	Name     *string           `json:"name"`
	List     []string          `json:"list"`
	Labels   map[string]string `json:"labels"`
	AnyValue any               `json:"any_value"`
}

// sentinelJSONPresenceTarget returns a jsonPresenceTarget with every field
// preset to a non-zero sentinel, so a decode that leaves a field alone is
// observable.
func sentinelJSONPresenceTarget() jsonPresenceTarget {
	flag := true
	count := 7
	name := "sentinel"
	return jsonPresenceTarget{
		Flag:     &flag,
		Count:    &count,
		Name:     &name,
		List:     []string{"sentinel"},
		Labels:   map[string]string{"sentinel": "sentinel"},
		AnyValue: "sentinel",
	}
}

// TestPresenceThroughJSONDecodeStrict checks that Document.DecodeStrict keeps
// an omitted key, explicit false, 0, "", [], {}, and null apart in Go struct
// fields: omitted keys leave fields untouched, null resets them, and zero or
// empty values set them to a non-nil zero.
func TestPresenceThroughJSONDecodeStrict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
		// fresh decodes into a zero target instead of the sentinel target.
		fresh bool
		check func(t *testing.T, got jsonPresenceTarget)
	}{
		{
			name: "omitted keys leave every field untouched",
			doc:  `{"marker": 1}`,
			check: func(t *testing.T, got jsonPresenceTarget) {
				want := sentinelJSONPresenceTarget()
				if got.Flag == nil || *got.Flag != *want.Flag ||
					got.Count == nil || *got.Count != *want.Count ||
					got.Name == nil || *got.Name != *want.Name ||
					len(got.List) != 1 || len(got.Labels) != 1 || got.AnyValue != want.AnyValue {
					t.Errorf("decode of omitted keys changed fields: %+v", got)
				}
			},
		},
		{
			name: "explicit false",
			doc:  `{"marker": 1, "flag": false}`,
			check: func(t *testing.T, got jsonPresenceTarget) {
				if got.Flag == nil || *got.Flag {
					t.Errorf("Flag = %v, want non-nil false", got.Flag)
				}
			},
		},
		{
			name: "explicit zero",
			doc:  `{"marker": 1, "count": 0}`,
			check: func(t *testing.T, got jsonPresenceTarget) {
				if got.Count == nil || *got.Count != 0 {
					t.Errorf("Count = %v, want non-nil 0", got.Count)
				}
			},
		},
		{
			name: "explicit empty string",
			doc:  `{"marker": 1, "name": ""}`,
			check: func(t *testing.T, got jsonPresenceTarget) {
				if got.Name == nil || *got.Name != "" {
					t.Errorf("Name = %v, want non-nil \"\"", got.Name)
				}
			},
		},
		{
			name: "explicit empty list",
			doc:  `{"marker": 1, "list": []}`,
			check: func(t *testing.T, got jsonPresenceTarget) {
				if got.List == nil || len(got.List) != 0 {
					t.Errorf("List = %#v, want non-nil empty slice", got.List)
				}
			},
		},
		// DecodeStrict keeps encoding/json semantics, and encoding/json
		// merges an object into an existing map rather than replacing it. The
		// two map cases therefore start from a zero target, where {} must
		// allocate an empty map and an omitted key must leave the map nil.
		{
			name:  "explicit empty map",
			doc:   `{"marker": 1, "labels": {}}`,
			fresh: true,
			check: func(t *testing.T, got jsonPresenceTarget) {
				if got.Labels == nil || len(got.Labels) != 0 {
					t.Errorf("Labels = %#v, want non-nil empty map", got.Labels)
				}
			},
		},
		{
			name:  "omitted map stays nil",
			doc:   `{"marker": 1}`,
			fresh: true,
			check: func(t *testing.T, got jsonPresenceTarget) {
				if got.Labels != nil {
					t.Errorf("Labels = %#v, want nil", got.Labels)
				}
			},
		},
		{
			name: "null resets pointer, slice, and map fields",
			doc:  `{"marker": 1, "flag": null, "count": null, "name": null, "list": null, "labels": null}`,
			check: func(t *testing.T, got jsonPresenceTarget) {
				if got.Flag != nil || got.Count != nil || got.Name != nil || got.List != nil || got.Labels != nil {
					t.Errorf("null decode = %+v, want nil Flag, Count, Name, List, and Labels", got)
				}
			},
		},
		{
			name: "any: empty list",
			doc:  `{"marker": 1, "any_value": []}`,
			check: func(t *testing.T, got jsonPresenceTarget) {
				list, ok := got.AnyValue.([]any)
				if !ok || list == nil || len(list) != 0 {
					t.Errorf("AnyValue = %#v, want non-nil []any{}", got.AnyValue)
				}
			},
		},
		{
			name: "any: empty map",
			doc:  `{"marker": 1, "any_value": {}}`,
			check: func(t *testing.T, got jsonPresenceTarget) {
				m, ok := got.AnyValue.(map[string]any)
				if !ok || m == nil || len(m) != 0 {
					t.Errorf("AnyValue = %#v, want non-nil map[string]any{}", got.AnyValue)
				}
			},
		},
		{
			name: "any: null",
			doc:  `{"marker": 1, "any_value": null}`,
			check: func(t *testing.T, got jsonPresenceTarget) {
				if got.AnyValue != nil {
					t.Errorf("AnyValue = %#v, want nil", got.AnyValue)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "contract.json"), []byte(tt.doc), 0o600); err != nil {
				t.Fatalf("WriteFile(contract.json) error = %v", err)
			}
			doc, err := jsoncontract.ReadFS(os.DirFS(dir), "contract.json")
			if err != nil {
				t.Fatalf("ReadFS(contract.json) error = %v", err)
			}
			got := sentinelJSONPresenceTarget()
			if tt.fresh {
				got = jsonPresenceTarget{}
			}
			if err := doc.DecodeStrict(&got); err != nil {
				t.Fatalf("DecodeStrict() error = %v", err)
			}
			tt.check(t, got)
		})
	}
}
