package yamlconfig_test

import (
	"testing"

	"github.com/mattjoyce/ashlar"
)

// limitsConfig is a closed struct.
type limitsConfig struct {
	Max int `yaml:"max"`
}

// structCatchAll holds closed structs in its catch-all, beside a closed
// sibling struct.
type structCatchAll struct {
	Limits limitsConfig            `yaml:"limits"`
	Extra  map[string]limitsConfig `yaml:",inline"`
}

// unexportedCatchAll's inline map cannot be set, so it is ignored.
type unexportedCatchAll struct {
	Name  string         `yaml:"name"`
	extra map[string]int `yaml:",inline"`
}

// twoCatchAlls declares two inline maps.
type twoCatchAlls struct {
	A map[string]int `yaml:",inline"`
	B map[string]int `yaml:",inline"`
}

// intKeyCatchAll has a catch-all with a non-string key kind.
type intKeyCatchAll struct {
	Extra map[int]string `yaml:",inline"`
}

// pointerCatchAll tags a pointer to a map inline.
type pointerCatchAll struct {
	Extra *map[string]int `yaml:",inline"`
}

// NestedCatchAll is valid on its own, but not promoted into another struct.
type NestedCatchAll struct {
	Name  string         `yaml:"name"`
	Extra map[string]int `yaml:",inline"`
}

// inlineNestedCatchAll promotes NestedCatchAll through an inline tag.
type inlineNestedCatchAll struct {
	Inner NestedCatchAll `yaml:",inline"`
}

// embeddedNestedCatchAll promotes NestedCatchAll by embedding it.
type embeddedNestedCatchAll struct {
	NestedCatchAll
}

// pointerNestedCatchAll promotes NestedCatchAll through an inline pointer.
type pointerNestedCatchAll struct {
	Inner *NestedCatchAll `yaml:",inline"`
}

// badShapeHolder places an invalid catch-all struct under a named key.
type badShapeHolder struct {
	Bad  twoCatchAlls `yaml:"bad"`
	Name string       `yaml:"name"`
}

// TestDecodeInlineMapShapeErrors checks that every unsupported catch-all
// shape fails at the struct mapping's pointer, even when the mapping is
// empty, and that null or omitted positions decode no mapping and so pass.
func TestDecodeInlineMapShapeErrors(t *testing.T) {
	t.Parallel()
	runDecodeCases(t, []decodeCase{
		{
			name: "two_inline_maps", doc: "{}\n",
			newDst: func() any { return new(twoCatchAlls) },
			err:    validationAt("", "config.yaml:1:1", "more than one inline map"),
		},
		{
			name: "non_string_key", doc: "{}\n",
			newDst: func() any { return new(intKeyCatchAll) },
			err:    validationAt("", "config.yaml:1:1", "Extra"),
		},
		{
			name: "pointer_to_map", doc: "{}\n",
			newDst: func() any { return new(pointerCatchAll) },
			err:    validationAt("", "config.yaml:1:1", "pointer to a map"),
		},
		{
			name: "inside_inline_struct", doc: "name: a\n",
			newDst: func() any { return new(inlineNestedCatchAll) },
			err:    validationAt("", "config.yaml:1:1", "NestedCatchAll"),
		},
		{
			name: "inside_embedded_struct", doc: "name: a\n",
			newDst: func() any { return new(embeddedNestedCatchAll) },
			err:    validationAt("", "config.yaml:1:1", "NestedCatchAll"),
		},
		{
			name: "inside_inline_pointer_struct", doc: "name: a\n",
			newDst: func() any { return new(pointerNestedCatchAll) },
			err:    validationAt("", "config.yaml:1:1", "NestedCatchAll"),
		},
		{
			name: "located_at_nested_mapping", doc: "name: a\nbad:\n  x: 1\n",
			newDst: func() any { return new(badShapeHolder) },
			err:    validationAt("/bad", "config.yaml:3:3", "twoCatchAlls"),
		},
		{
			name: "null_position_passes", doc: "bad: null\nname: a\n",
			newDst: func() any { return new(badShapeHolder) },
			want:   &badShapeHolder{Name: "a"},
		},
		{
			name: "omitted_position_passes", doc: "name: a\n",
			newDst: func() any { return new(badShapeHolder) },
			want:   &badShapeHolder{Name: "a"},
		},
		{
			name: "same_type_valid_alone", doc: "name: a\nx: 1\n",
			newDst: func() any { return new(NestedCatchAll) },
			want:   &NestedCatchAll{Name: "a", Extra: map[string]int{"x": 1}},
		},
		{
			name: "unexported_inline_map_stays_closed", doc: "name: a\nx: 1\n",
			newDst: func() any { return &unexportedCatchAll{extra: nil} },
			err:    &locatedWant{kind: ashlar.ErrUnknownField, ptr: "/x", loc: "config.yaml:2:1"},
		},
	})
}

// TestDecodeInlineMapClosedness checks that a catch-all opens only its own
// struct: structs inside entries and sibling structs stay closed.
func TestDecodeInlineMapClosedness(t *testing.T) {
	t.Parallel()
	runDecodeCases(t, []decodeCase{
		{
			name: "struct_entries_decode", doc: "limits:\n  max: 1\ncpu:\n  max: 2\n",
			newDst: func() any { return new(structCatchAll) },
			want: &structCatchAll{
				Limits: limitsConfig{Max: 1}, Extra: map[string]limitsConfig{"cpu": {Max: 2}},
			},
		},
		{
			name: "struct_entry_stays_closed", doc: "cpu:\n  max: 2\n  bogus: 3\n",
			newDst: func() any { return new(structCatchAll) },
			err:    &locatedWant{kind: ashlar.ErrUnknownField, ptr: "/cpu/bogus", loc: "config.yaml:3:3"},
		},
		{
			name: "sibling_struct_stays_closed", doc: "limits:\n  max: 1\n  bogus: 3\n",
			newDst: func() any { return new(structCatchAll) },
			err:    &locatedWant{kind: ashlar.ErrUnknownField, ptr: "/limits/bogus", loc: "config.yaml:3:3"},
		},
	})
}
