package yamlconfig_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

// writeTestFiles writes each name -> content pair under dir, creating parent
// directories as needed.
func writeTestFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", full, err)
		}
	}
}

// wantNode describes the expected presence, kind, and effective origin of one
// node in a Value tree.
type wantNode struct {
	ptr      ashlar.Pointer
	presence ashlar.Presence
	kind     ashlar.Kind
	// origin is the expected Effective.String(); empty skips the check.
	origin string
}

// checkNode asserts that root holds the node described by want, including the
// zero or empty content that a present node of that kind must carry.
func checkNode(t *testing.T, root ashlar.Value, want wantNode) {
	t.Helper()
	if got := root.PresenceAt(want.ptr); got != want.presence {
		t.Errorf("PresenceAt(%s) = %v, want %v", want.ptr, got, want.presence)
	}
	node := root.At(want.ptr)
	if got := node.Kind(); got != want.kind {
		t.Errorf("At(%s).Kind() = %v, want %v", want.ptr, got, want.kind)
	}
	switch want.presence {
	case ashlar.PresenceOmitted:
		if !node.IsOmitted() || node.IsNull() || node.IsPresent() {
			t.Errorf("At(%s): IsOmitted/IsNull/IsPresent = %v/%v/%v, want true/false/false",
				want.ptr, node.IsOmitted(), node.IsNull(), node.IsPresent())
		}
	case ashlar.PresenceNull:
		if node.IsOmitted() || !node.IsNull() || node.IsPresent() {
			t.Errorf("At(%s): IsOmitted/IsNull/IsPresent = %v/%v/%v, want false/true/false",
				want.ptr, node.IsOmitted(), node.IsNull(), node.IsPresent())
		}
	case ashlar.PresencePresent:
		if node.IsOmitted() || node.IsNull() || !node.IsPresent() {
			t.Errorf("At(%s): IsOmitted/IsNull/IsPresent = %v/%v/%v, want false/false/true",
				want.ptr, node.IsOmitted(), node.IsNull(), node.IsPresent())
		}
	}
	if want.origin != "" {
		if got := node.Provenance().Effective.String(); got != want.origin {
			t.Errorf("At(%s) origin = %q, want %q", want.ptr, got, want.origin)
		}
	}
}

// TestPresenceThroughYAMLLoader checks that the YAML loader keeps an omitted
// key, explicit false, 0, "", [], {}, and every YAML spelling of null apart at
// the Value level, with each node located at its own file:line:column.
func TestPresenceThroughYAMLLoader(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"config.yaml": `flag_false: false
count_zero: 0
name_empty: ""
list_empty: []
map_empty: {}
value_null: null
value_tilde: ~
value_blank:
nested:
  inner_null: null
  inner_list: []
`})
	root, err := yamlconfig.ReadFS(os.DirFS(dir), "config.yaml")
	if err != nil {
		t.Fatalf("ReadFS(config.yaml) error = %v", err)
	}

	tests := []struct {
		name string
		want wantNode
		// content checks the zero or empty payload of a present node.
		content func(t *testing.T, v ashlar.Value)
	}{
		{
			name: "explicit false",
			want: wantNode{ashlar.NewPointer("flag_false"), ashlar.PresencePresent, ashlar.KindBool, "config.yaml:1:13"},
			content: func(t *testing.T, v ashlar.Value) {
				if b, ok := v.Bool(); !ok || b {
					t.Errorf("Bool() = (%v, %v), want (false, true)", b, ok)
				}
			},
		},
		{
			name: "explicit zero",
			want: wantNode{ashlar.NewPointer("count_zero"), ashlar.PresencePresent, ashlar.KindNumber, "config.yaml:2:13"},
			content: func(t *testing.T, v ashlar.Value) {
				if lex, ok := v.NumberLexeme(); !ok || lex != "0" {
					t.Errorf("NumberLexeme() = (%q, %v), want (\"0\", true)", lex, ok)
				}
			},
		},
		{
			name: "explicit empty string",
			want: wantNode{ashlar.NewPointer("name_empty"), ashlar.PresencePresent, ashlar.KindString, "config.yaml:3:13"},
			content: func(t *testing.T, v ashlar.Value) {
				if s, ok := v.StringVal(); !ok || s != "" {
					t.Errorf("StringVal() = (%q, %v), want (\"\", true)", s, ok)
				}
			},
		},
		{
			name: "explicit empty list",
			want: wantNode{ashlar.NewPointer("list_empty"), ashlar.PresencePresent, ashlar.KindArray, "config.yaml:4:13"},
			content: func(t *testing.T, v ashlar.Value) {
				if v.Len() != 0 {
					t.Errorf("Len() = %d, want 0", v.Len())
				}
			},
		},
		{
			name: "explicit empty map",
			want: wantNode{ashlar.NewPointer("map_empty"), ashlar.PresencePresent, ashlar.KindObject, "config.yaml:5:12"},
			content: func(t *testing.T, v ashlar.Value) {
				if v.Len() != 0 || v.Keys() != nil {
					t.Errorf("Len(), Keys() = %d, %v, want 0, nil", v.Len(), v.Keys())
				}
			},
		},
		{
			name: "null literal",
			want: wantNode{ashlar.NewPointer("value_null"), ashlar.PresenceNull, ashlar.KindNull, "config.yaml:6:13"},
		},
		{
			name: "tilde null",
			want: wantNode{ashlar.NewPointer("value_tilde"), ashlar.PresenceNull, ashlar.KindNull, "config.yaml:7:14"},
		},
		{
			name: "blank value is null",
			want: wantNode{ashlar.NewPointer("value_blank"), ashlar.PresenceNull, ashlar.KindNull, ""},
		},
		{
			name: "nested null",
			want: wantNode{ashlar.NewPointer("nested", "inner_null"), ashlar.PresenceNull, ashlar.KindNull, "config.yaml:10:15"},
		},
		{
			name: "nested empty list",
			want: wantNode{ashlar.NewPointer("nested", "inner_list"), ashlar.PresencePresent, ashlar.KindArray, "config.yaml:11:15"},
		},
		{
			name: "omitted top-level key",
			want: wantNode{ashlar.NewPointer("missing"), ashlar.PresenceOmitted, ashlar.KindInvalid, ""},
		},
		{
			name: "omitted key under present map",
			want: wantNode{ashlar.NewPointer("nested", "missing"), ashlar.PresenceOmitted, ashlar.KindInvalid, ""},
		},
		{
			name: "omitted key under empty map",
			want: wantNode{ashlar.NewPointer("map_empty", "missing"), ashlar.PresenceOmitted, ashlar.KindInvalid, ""},
		},
		{
			name: "omitted key under null",
			want: wantNode{ashlar.NewPointer("value_null", "missing"), ashlar.PresenceOmitted, ashlar.KindInvalid, ""},
		},
		{
			name: "omitted index in empty list",
			want: wantNode{ashlar.NewPointer("list_empty", "0"), ashlar.PresenceOmitted, ashlar.KindInvalid, ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkNode(t, root, tt.want)
			if tt.content != nil {
				tt.content(t, root.At(tt.want.ptr))
			}
		})
	}

	// Lookup distinguishes a key bound to null from a key that is absent.
	if _, ok := root.Lookup("value_null"); !ok {
		t.Error(`Lookup("value_null") ok = false, want true for an explicit null`)
	}
	if _, ok := root.Lookup("missing"); ok {
		t.Error(`Lookup("missing") ok = true, want false for an omitted key`)
	}
}

// presenceTarget is a decode destination whose fields can each tell an
// omitted key (field untouched), null (field reset), and a zero or empty value
// (field set to a non-nil zero) apart.
type presenceTarget struct {
	Marker   int               `yaml:"marker"`
	Flag     *bool             `yaml:"flag"`
	Count    *int              `yaml:"count"`
	Name     *string           `yaml:"name"`
	List     []string          `yaml:"list"`
	Labels   map[string]string `yaml:"labels"`
	AnyValue any               `yaml:"any_value"`
}

// sentinelPresenceTarget returns a presenceTarget with every field preset to a
// non-zero sentinel, so a decode that leaves a field alone is observable.
func sentinelPresenceTarget() presenceTarget {
	flag := true
	count := 7
	name := "sentinel"
	return presenceTarget{
		Flag:     &flag,
		Count:    &count,
		Name:     &name,
		List:     []string{"sentinel"},
		Labels:   map[string]string{"sentinel": "sentinel"},
		AnyValue: "sentinel",
	}
}

// presenceDecodeCase is one decode-level presence case shared by the YAML
// loader, include, and environment tests.
type presenceDecodeCase struct {
	name  string
	doc   string
	check func(t *testing.T, got presenceTarget)
}

// presenceDecodeCases returns the decode-level presence table. Every document
// sets marker so that the omitted case still decodes a non-empty mapping.
func presenceDecodeCases() []presenceDecodeCase {
	return []presenceDecodeCase{
		{
			name: "omitted keys leave every field untouched",
			doc:  "marker: 1\n",
			check: func(t *testing.T, got presenceTarget) {
				want := sentinelPresenceTarget()
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
			doc:  "marker: 1\nflag: false\n",
			check: func(t *testing.T, got presenceTarget) {
				if got.Flag == nil || *got.Flag {
					t.Errorf("Flag = %v, want non-nil false", got.Flag)
				}
			},
		},
		{
			name: "explicit zero",
			doc:  "marker: 1\ncount: 0\n",
			check: func(t *testing.T, got presenceTarget) {
				if got.Count == nil || *got.Count != 0 {
					t.Errorf("Count = %v, want non-nil 0", got.Count)
				}
			},
		},
		{
			name: "explicit empty string",
			doc:  "marker: 1\nname: \"\"\n",
			check: func(t *testing.T, got presenceTarget) {
				if got.Name == nil || *got.Name != "" {
					t.Errorf("Name = %v, want non-nil \"\"", got.Name)
				}
			},
		},
		{
			name: "explicit empty list",
			doc:  "marker: 1\nlist: []\n",
			check: func(t *testing.T, got presenceTarget) {
				if got.List == nil || len(got.List) != 0 {
					t.Errorf("List = %#v, want non-nil empty slice", got.List)
				}
			},
		},
		{
			name: "explicit empty map",
			doc:  "marker: 1\nlabels: {}\n",
			check: func(t *testing.T, got presenceTarget) {
				if got.Labels == nil || len(got.Labels) != 0 {
					t.Errorf("Labels = %#v, want non-nil empty map", got.Labels)
				}
			},
		},
		{
			name: "null resets pointer, slice, and map fields",
			doc:  "marker: 1\nflag: null\ncount: null\nname: null\nlist: null\nlabels: null\n",
			check: func(t *testing.T, got presenceTarget) {
				if got.Flag != nil || got.Count != nil || got.Name != nil || got.List != nil || got.Labels != nil {
					t.Errorf("null decode = %+v, want nil Flag, Count, Name, List, and Labels", got)
				}
			},
		},
		{
			name: "any: empty list",
			doc:  "marker: 1\nany_value: []\n",
			check: func(t *testing.T, got presenceTarget) {
				list, ok := got.AnyValue.([]any)
				if !ok || list == nil || len(list) != 0 {
					t.Errorf("AnyValue = %#v, want non-nil []any{}", got.AnyValue)
				}
			},
		},
		{
			name: "any: empty map",
			doc:  "marker: 1\nany_value: {}\n",
			check: func(t *testing.T, got presenceTarget) {
				m, ok := got.AnyValue.(map[string]any)
				if !ok || m == nil || len(m) != 0 {
					t.Errorf("AnyValue = %#v, want non-nil map[string]any{}", got.AnyValue)
				}
			},
		},
		{
			name: "any: null",
			doc:  "marker: 1\nany_value: null\n",
			check: func(t *testing.T, got presenceTarget) {
				if got.AnyValue != nil {
					t.Errorf("AnyValue = %#v, want nil", got.AnyValue)
				}
			},
		},
	}
}

// TestPresenceThroughYAMLDecode checks that DecodeValueStrict and
// Resolved.DecodeStrict keep an omitted key, explicit false, 0, "", [], {},
// and null apart in Go struct fields.
func TestPresenceThroughYAMLDecode(t *testing.T) {
	t.Parallel()

	for _, tt := range presenceDecodeCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeTestFiles(t, dir, map[string]string{"config.yaml": tt.doc})

			root, err := yamlconfig.ReadFS(os.DirFS(dir), "config.yaml")
			if err != nil {
				t.Fatalf("ReadFS(config.yaml) error = %v", err)
			}
			got := sentinelPresenceTarget()
			if err := yamlconfig.DecodeValueStrict(root, &got); err != nil {
				t.Fatalf("DecodeValueStrict() error = %v", err)
			}
			tt.check(t, got)

			resolved, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "config.yaml", yamlconfig.ResolveOptions{})
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			got = sentinelPresenceTarget()
			if err := resolved.DecodeStrict(&got); err != nil {
				t.Fatalf("Resolved.DecodeStrict() error = %v", err)
			}
			tt.check(t, got)
		})
	}
}

// TestPresenceThroughIncludes checks that presence survives the include graph:
// a value set only in an included file, a root value that an included file
// overrides with false, 0, "", [], or null, and a root zero value that no
// included file touches. Included fragments compose after the including file
// ("root first, followed by included fragments", includes.go), so an included
// value replaces a root value.
func TestPresenceThroughIncludes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"config.yaml": `include:
  - values.yaml
flag: true
count: 7
name: from_root
list: [from_root]
cleared: from_root
keep_false: false
keep_null: null
keep_empty: ""
`,
		"values.yaml": `flag: false
count: 0
name: ""
list: []
labels: {}
cleared: null
`,
	})

	resolved, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "config.yaml", yamlconfig.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	root := resolved.Root()

	const via = " (included via config.yaml:2:5)"
	tests := []struct {
		want wantNode
		// overrode is the expected Superseded origin, or "" when the value
		// overrode nothing.
		overrode string
	}{
		{wantNode{ashlar.NewPointer("flag"), ashlar.PresencePresent, ashlar.KindBool, "values.yaml:1:7" + via}, "config.yaml:3:7"},
		{wantNode{ashlar.NewPointer("count"), ashlar.PresencePresent, ashlar.KindNumber, "values.yaml:2:8" + via}, "config.yaml:4:8"},
		{wantNode{ashlar.NewPointer("name"), ashlar.PresencePresent, ashlar.KindString, "values.yaml:3:7" + via}, "config.yaml:5:7"},
		{wantNode{ashlar.NewPointer("list"), ashlar.PresencePresent, ashlar.KindArray, "values.yaml:4:7" + via}, "config.yaml:6:7"},
		{wantNode{ashlar.NewPointer("labels"), ashlar.PresencePresent, ashlar.KindObject, "values.yaml:5:9" + via}, ""},
		{wantNode{ashlar.NewPointer("cleared"), ashlar.PresenceNull, ashlar.KindNull, "values.yaml:6:10" + via}, "config.yaml:7:10"},
		{wantNode{ashlar.NewPointer("keep_false"), ashlar.PresencePresent, ashlar.KindBool, "config.yaml:8:13"}, ""},
		{wantNode{ashlar.NewPointer("keep_null"), ashlar.PresenceNull, ashlar.KindNull, "config.yaml:9:12"}, ""},
		{wantNode{ashlar.NewPointer("keep_empty"), ashlar.PresencePresent, ashlar.KindString, "config.yaml:10:13"}, ""},
		{wantNode{ashlar.NewPointer("missing"), ashlar.PresenceOmitted, ashlar.KindInvalid, ""}, ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.want.ptr), func(t *testing.T) {
			t.Parallel()
			checkNode(t, root, tt.want)
			sup := root.At(tt.want.ptr).Provenance().Superseded()
			switch {
			case tt.overrode == "" && len(sup) != 0:
				t.Errorf("Superseded() = %+v, want none", sup)
			case tt.overrode != "" && (len(sup) != 1 || sup[0].Origin.String() != tt.overrode):
				t.Errorf("Superseded() = %+v, want one entry from %s", sup, tt.overrode)
			}
		})
	}

	type includeTarget struct {
		Flag      *bool             `yaml:"flag"`
		Count     *int              `yaml:"count"`
		Name      *string           `yaml:"name"`
		List      []string          `yaml:"list"`
		Labels    map[string]string `yaml:"labels"`
		Cleared   *string           `yaml:"cleared"`
		KeepFalse *bool             `yaml:"keep_false"`
		KeepNull  *string           `yaml:"keep_null"`
		KeepEmpty *string           `yaml:"keep_empty"`
		Missing   *string           `yaml:"missing"`
	}
	sentinel := "sentinel"
	got := includeTarget{Cleared: &sentinel, KeepNull: &sentinel, Missing: &sentinel}
	if err := resolved.DecodeStrict(&got); err != nil {
		t.Fatalf("DecodeStrict() error = %v", err)
	}
	if got.Flag == nil || *got.Flag {
		t.Errorf("Flag = %v, want non-nil false", got.Flag)
	}
	if got.Count == nil || *got.Count != 0 {
		t.Errorf("Count = %v, want non-nil 0", got.Count)
	}
	if got.Name == nil || *got.Name != "" {
		t.Errorf("Name = %v, want non-nil \"\"", got.Name)
	}
	if got.List == nil || len(got.List) != 0 {
		t.Errorf("List = %#v, want non-nil empty slice", got.List)
	}
	if got.Labels == nil || len(got.Labels) != 0 {
		t.Errorf("Labels = %#v, want non-nil empty map", got.Labels)
	}
	if got.Cleared != nil || got.KeepNull != nil {
		t.Errorf("Cleared, KeepNull = %v, %v, want nil, nil", got.Cleared, got.KeepNull)
	}
	if got.KeepFalse == nil || *got.KeepFalse {
		t.Errorf("KeepFalse = %v, want non-nil false", got.KeepFalse)
	}
	if got.KeepEmpty == nil || *got.KeepEmpty != "" {
		t.Errorf("KeepEmpty = %v, want non-nil \"\"", got.KeepEmpty)
	}
	if got.Missing != &sentinel {
		t.Errorf("Missing = %v, want the untouched sentinel pointer", got.Missing)
	}
}

// TestPresenceThroughEnvOverrides checks presence through the environment
// layer: env values of "false", "0", and "" replace file values as present
// values with an env origin, an env value replaces an explicit null, unset
// variables leave false, 0, "", [], and null from the file untouched, and an
// explicit binding creates an omitted key only when its variable is set.
// An empty variable counts as set: EnvBinding.Lookup reports presence with its
// bool result, and os.LookupEnv returns ("", true) for VAR="".
func TestPresenceThroughEnvOverrides(t *testing.T) {
	// Not parallel: uses t.Setenv.
	t.Setenv("ASHLAR_PRESENCE_FLAG", "false")
	t.Setenv("ASHLAR_PRESENCE_COUNT", "0")
	t.Setenv("ASHLAR_PRESENCE_NAME", "")
	t.Setenv("ASHLAR_PRESENCE_NULL_TO_ENV", "from_env")
	t.Setenv("ASHLAR_PRESENCE_ADDED", "")

	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"config.yaml": `flag: true
count: 7
name: from_file
null_to_env: null
keep_false: false
keep_zero: 0
keep_empty: ""
keep_list: []
keep_map: {}
keep_null: null
`})

	addedPtr := ashlar.NewPointer("added_by_env")
	unsetPtr := ashlar.NewPointer("still_missing")
	resolved, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "config.yaml", yamlconfig.ResolveOptions{
		Env: yamlconfig.EnvBinding{
			Prefix: "ASHLAR_PRESENCE",
			Bindings: map[ashlar.Pointer]string{
				addedPtr: "ASHLAR_PRESENCE_ADDED",
				unsetPtr: "ASHLAR_PRESENCE_UNSET_FOR_TEST",
			},
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	root := resolved.Root()

	tests := []struct {
		want     wantNode
		overrode string
	}{
		{wantNode{ashlar.NewPointer("flag"), ashlar.PresencePresent, ashlar.KindBool, "env:ASHLAR_PRESENCE_FLAG"}, "config.yaml:1:7"},
		{wantNode{ashlar.NewPointer("count"), ashlar.PresencePresent, ashlar.KindNumber, "env:ASHLAR_PRESENCE_COUNT"}, "config.yaml:2:8"},
		{wantNode{ashlar.NewPointer("name"), ashlar.PresencePresent, ashlar.KindString, "env:ASHLAR_PRESENCE_NAME"}, "config.yaml:3:7"},
		{wantNode{ashlar.NewPointer("null_to_env"), ashlar.PresencePresent, ashlar.KindString, "env:ASHLAR_PRESENCE_NULL_TO_ENV"}, "config.yaml:4:14"},
		{wantNode{ashlar.NewPointer("keep_false"), ashlar.PresencePresent, ashlar.KindBool, "config.yaml:5:13"}, ""},
		{wantNode{ashlar.NewPointer("keep_zero"), ashlar.PresencePresent, ashlar.KindNumber, "config.yaml:6:12"}, ""},
		{wantNode{ashlar.NewPointer("keep_empty"), ashlar.PresencePresent, ashlar.KindString, "config.yaml:7:13"}, ""},
		{wantNode{ashlar.NewPointer("keep_list"), ashlar.PresencePresent, ashlar.KindArray, "config.yaml:8:12"}, ""},
		{wantNode{ashlar.NewPointer("keep_map"), ashlar.PresencePresent, ashlar.KindObject, "config.yaml:9:11"}, ""},
		{wantNode{ashlar.NewPointer("keep_null"), ashlar.PresenceNull, ashlar.KindNull, "config.yaml:10:12"}, ""},
		{wantNode{addedPtr, ashlar.PresencePresent, ashlar.KindString, "env:ASHLAR_PRESENCE_ADDED"}, ""},
		{wantNode{unsetPtr, ashlar.PresenceOmitted, ashlar.KindInvalid, ""}, ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.want.ptr), func(t *testing.T) {
			checkNode(t, root, tt.want)
			sup := root.At(tt.want.ptr).Provenance().Superseded()
			switch {
			case tt.overrode == "" && len(sup) != 0:
				t.Errorf("Superseded() = %+v, want none", sup)
			case tt.overrode != "" && (len(sup) != 1 || sup[0].Origin.String() != tt.overrode):
				t.Errorf("Superseded() = %+v, want one entry from %s", sup, tt.overrode)
			}
		})
	}

	if b, ok := root.At(ashlar.NewPointer("flag")).Bool(); !ok || b {
		t.Errorf("/flag Bool() = (%v, %v), want (false, true)", b, ok)
	}
	if lex, ok := root.At(ashlar.NewPointer("count")).NumberLexeme(); !ok || lex != "0" {
		t.Errorf("/count NumberLexeme() = (%q, %v), want (\"0\", true)", lex, ok)
	}
	for _, ptr := range []ashlar.Pointer{ashlar.NewPointer("name"), addedPtr} {
		if s, ok := root.At(ptr).StringVal(); !ok || s != "" {
			t.Errorf("%s StringVal() = (%q, %v), want (\"\", true)", ptr, s, ok)
		}
	}

	type envTarget struct {
		Flag      *bool             `yaml:"flag"`
		Count     *int              `yaml:"count"`
		Name      *string           `yaml:"name"`
		NullToEnv *string           `yaml:"null_to_env"`
		KeepFalse *bool             `yaml:"keep_false"`
		KeepZero  *int              `yaml:"keep_zero"`
		KeepEmpty *string           `yaml:"keep_empty"`
		KeepList  []string          `yaml:"keep_list"`
		KeepMap   map[string]string `yaml:"keep_map"`
		KeepNull  *string           `yaml:"keep_null"`
		Added     *string           `yaml:"added_by_env"`
		Missing   *string           `yaml:"still_missing"`
	}
	sentinel := "sentinel"
	got := envTarget{KeepNull: &sentinel, Missing: &sentinel}
	if err := resolved.DecodeStrict(&got); err != nil {
		t.Fatalf("DecodeStrict() error = %v", err)
	}
	if got.Flag == nil || *got.Flag || got.KeepFalse == nil || *got.KeepFalse {
		t.Errorf("Flag, KeepFalse = %v, %v, want non-nil false", got.Flag, got.KeepFalse)
	}
	if got.Count == nil || *got.Count != 0 || got.KeepZero == nil || *got.KeepZero != 0 {
		t.Errorf("Count, KeepZero = %v, %v, want non-nil 0", got.Count, got.KeepZero)
	}
	if got.Name == nil || *got.Name != "" || got.KeepEmpty == nil || *got.KeepEmpty != "" || got.Added == nil || *got.Added != "" {
		t.Errorf("Name, KeepEmpty, Added = %v, %v, %v, want non-nil \"\"", got.Name, got.KeepEmpty, got.Added)
	}
	if got.NullToEnv == nil || *got.NullToEnv != "from_env" {
		t.Errorf("NullToEnv = %v, want non-nil \"from_env\"", got.NullToEnv)
	}
	if got.KeepList == nil || len(got.KeepList) != 0 || got.KeepMap == nil || len(got.KeepMap) != 0 {
		t.Errorf("KeepList, KeepMap = %#v, %#v, want non-nil empty", got.KeepList, got.KeepMap)
	}
	if got.KeepNull != nil {
		t.Errorf("KeepNull = %v, want nil", got.KeepNull)
	}
	if got.Missing != &sentinel {
		t.Errorf("Missing = %v, want the untouched sentinel pointer", got.Missing)
	}
}

// TestPresenceSurvivesDefaultAndExplicitLayers checks that a file's explicit
// false, 0, "", [], and null replace non-zero defaults instead of falling back
// to them, that an omitted key keeps its default, and that an explicit null
// override replaces a file value with null.
func TestPresenceSurvivesDefaultAndExplicitLayers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"config.yaml": `flag: false
count: 0
name: ""
list: []
note: null
cleared_by_flag: from_file
`})

	seven, err := ashlar.NumberValue("7", ashlar.Provenance{})
	if err != nil {
		t.Fatalf("NumberValue(7) error = %v", err)
	}
	none := ashlar.Provenance{}
	resolved, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "config.yaml", yamlconfig.ResolveOptions{
		Defaults: []yamlconfig.Override{
			yamlconfig.DefaultSetting(ashlar.NewPointer("flag"), "flag", ashlar.BoolValue(true, none)),
			yamlconfig.DefaultSetting(ashlar.NewPointer("count"), "count", seven),
			yamlconfig.DefaultSetting(ashlar.NewPointer("name"), "name", ashlar.StringValue("default", none)),
			yamlconfig.DefaultSetting(ashlar.NewPointer("list"), "list", ashlar.ArrayValue([]ashlar.Value{ashlar.StringValue("default", none)}, none)),
			yamlconfig.DefaultSetting(ashlar.NewPointer("note"), "note", ashlar.StringValue("default", none)),
			yamlconfig.DefaultSetting(ashlar.NewPointer("defaulted_false"), "defaulted_false", ashlar.BoolValue(false, none)),
		},
		Explicit: []yamlconfig.Override{
			yamlconfig.ExplicitSetting(ashlar.NewPointer("cleared_by_flag"), "--cleared-by-flag", ashlar.NullValue(none)),
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	root := resolved.Root()

	tests := []struct {
		want     wantNode
		overrode string
	}{
		{wantNode{ashlar.NewPointer("flag"), ashlar.PresencePresent, ashlar.KindBool, "config.yaml:1:7"}, "default:flag"},
		{wantNode{ashlar.NewPointer("count"), ashlar.PresencePresent, ashlar.KindNumber, "config.yaml:2:8"}, "default:count"},
		{wantNode{ashlar.NewPointer("name"), ashlar.PresencePresent, ashlar.KindString, "config.yaml:3:7"}, "default:name"},
		{wantNode{ashlar.NewPointer("list"), ashlar.PresencePresent, ashlar.KindArray, "config.yaml:4:7"}, "default:list"},
		{wantNode{ashlar.NewPointer("note"), ashlar.PresenceNull, ashlar.KindNull, "config.yaml:5:7"}, "default:note"},
		{wantNode{ashlar.NewPointer("cleared_by_flag"), ashlar.PresenceNull, ashlar.KindNull, "explicit:--cleared-by-flag"}, "config.yaml:6:18"},
		{wantNode{ashlar.NewPointer("defaulted_false"), ashlar.PresencePresent, ashlar.KindBool, "default:defaulted_false"}, ""},
		{wantNode{ashlar.NewPointer("missing"), ashlar.PresenceOmitted, ashlar.KindInvalid, ""}, ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.want.ptr), func(t *testing.T) {
			t.Parallel()
			checkNode(t, root, tt.want)
			sup := root.At(tt.want.ptr).Provenance().Superseded()
			switch {
			case tt.overrode == "" && len(sup) != 0:
				t.Errorf("Superseded() = %+v, want none", sup)
			case tt.overrode != "" && (len(sup) != 1 || sup[0].Origin.String() != tt.overrode):
				t.Errorf("Superseded() = %+v, want one entry from %s", sup, tt.overrode)
			}
		})
	}
	if b, ok := root.At(ashlar.NewPointer("flag")).Bool(); !ok || b {
		t.Errorf("/flag Bool() = (%v, %v), want (false, true)", b, ok)
	}
	if n := root.At(ashlar.NewPointer("list")).Len(); n != 0 {
		t.Errorf("/list Len() = %d, want 0", n)
	}
}

// TestPresenceOmittedOverrideLeavesNoPhantomKey checks that a default or
// explicit Override whose Value is omitted never leaves a phantom member: a key
// that Keys and Lookup report while PresenceAt reports it omitted. SPEC.md does
// not say whether such an Override is an error or a no-op, so either outcome
// passes; what fails is a tree that disagrees with itself about presence
// (SPEC.md §1.2: "Field presence explicitly distinguishes `Omitted` from
// `Null`").
func TestPresenceOmittedOverrideLeavesNoPhantomKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts yamlconfig.ResolveOptions
	}{
		{
			name: "explicit omitted over a file value",
			opts: yamlconfig.ResolveOptions{Explicit: []yamlconfig.Override{
				yamlconfig.ExplicitSetting(ashlar.NewPointer("opt"), "--opt", ashlar.OmittedValue()),
			}},
		},
		{
			name: "default omitted for an absent key",
			opts: yamlconfig.ResolveOptions{Defaults: []yamlconfig.Override{
				yamlconfig.DefaultSetting(ashlar.NewPointer("absent"), "absent", ashlar.OmittedValue()),
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeTestFiles(t, dir, map[string]string{"config.yaml": "opt: from_file\nkeep: 1\n"})
			resolved, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "config.yaml", tt.opts)
			if err != nil {
				return // Rejecting the override outright is acceptable.
			}
			root := resolved.Root()
			for _, m := range root.Members() {
				if !m.Value.IsOmitted() {
					continue
				}
				_, ok := root.Lookup(m.Key)
				t.Errorf("member %q is listed by Members()/Keys() %v and Lookup ok=%v, but PresenceAt = %v",
					m.Key, root.Keys(), ok, root.PresenceAt(ashlar.NewPointer(m.Key)))
			}
		})
	}
}
