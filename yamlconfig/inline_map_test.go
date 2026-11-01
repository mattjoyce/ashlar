package yamlconfig_test

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

// timeoutsConfig mirrors Ductile's TimeoutsConfig: named timeouts plus a
// catch-all map of per-command overrides.
type timeoutsConfig struct {
	Poll      time.Duration            `yaml:"poll"`
	Handle    time.Duration            `yaml:"handle"`
	Health    time.Duration            `yaml:"health,omitempty"`
	Init      time.Duration            `yaml:"init,omitempty"`
	Overrides map[string]time.Duration `yaml:",inline,omitempty"`
}

// pluginTimeouts holds a catch-all struct under a named key.
type pluginTimeouts struct {
	Timeouts timeoutsConfig `yaml:"timeouts"`
}

// jsonFallbackCatchAll is a catch-all tagged only through the json fallback.
type jsonFallbackCatchAll struct {
	Name  string         `json:"name"`
	Extra map[string]int `json:",inline"`
}

// yamlOverJSONCatchAll tags the map inline for YAML and named for JSON. The
// YAML tag wins, so the field is the catch-all.
type yamlOverJSONCatchAll struct {
	Extra map[string]int `yaml:",inline" json:"extra"`
}

// excludedCatchAll is excluded by yaml:"-", so the struct stays closed.
type excludedCatchAll struct {
	Name  string         `yaml:"name"`
	Extra map[string]int `yaml:"-" json:",inline"`
}

// namedInlineCatchAll gives the catch-all a name, which is ignored.
type namedInlineCatchAll struct {
	Extra map[string]int `yaml:"overrides,inline"`
}

// catchAllBase is promoted into catchAllWithPromoted.
type catchAllBase struct {
	Region string `yaml:"region"`
}

// catchAllWithPromoted has named, promoted and catch-all keys.
type catchAllWithPromoted struct {
	catchAllBase `yaml:",inline"`
	Name         string            `yaml:"name"`
	Extra        map[string]string `yaml:",inline"`
}

// command is a named string key type.
type command string

// namedKeyCatchAll is a catch-all keyed by a named string type.
type namedKeyCatchAll struct {
	Poll      time.Duration             `yaml:"poll"`
	Overrides map[command]time.Duration `yaml:",inline"`
}

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

func TestDecodeInlineMapCatchAll(t *testing.T) {
	t.Parallel()
	runDecodeCases(t, []decodeCase{
		{
			name:   "ductile_shape",
			doc:    "timeouts:\n  poll: 1s\n  handle: 2s\n  cpu: 15s\n",
			newDst: func() any { return new(pluginTimeouts) },
			want: &pluginTimeouts{Timeouts: timeoutsConfig{
				Poll: time.Second, Handle: 2 * time.Second,
				Overrides: map[string]time.Duration{"cpu": 15 * time.Second},
			}},
		},
		{
			name: "json_tag_fallback", doc: "name: a\nx: 1\n",
			newDst: func() any { return new(jsonFallbackCatchAll) },
			want:   &jsonFallbackCatchAll{Name: "a", Extra: map[string]int{"x": 1}},
		},
		{
			name: "yaml_tag_beats_json_tag", doc: "extra: 1\n",
			newDst: func() any { return new(yamlOverJSONCatchAll) },
			want:   &yamlOverJSONCatchAll{Extra: map[string]int{"extra": 1}},
		},
		{
			name: "yaml_dash_excludes", doc: "name: a\nx: 1\n",
			newDst: func() any { return new(excludedCatchAll) },
			err:    &locatedWant{kind: ashlar.ErrUnknownField, ptr: "/x", loc: "config.yaml:2:1"},
		},
		{
			name: "named_inline_is_catch_all", doc: "overrides: 1\ncpu: 2\n",
			newDst: func() any { return new(namedInlineCatchAll) },
			want:   &namedInlineCatchAll{Extra: map[string]int{"overrides": 1, "cpu": 2}},
		},
		{
			name: "named_and_promoted_fields_win", doc: "name: a\nregion: r\nzone: z\n",
			newDst: func() any { return new(catchAllWithPromoted) },
			want: &catchAllWithPromoted{
				catchAllBase: catchAllBase{Region: "r"}, Name: "a",
				Extra: map[string]string{"zone": "z"},
			},
		},
		{
			name: "named_string_key_type", doc: "poll: 1s\ncpu: 15s\n",
			newDst: func() any { return new(namedKeyCatchAll) },
			want: &namedKeyCatchAll{
				Poll: time.Second, Overrides: map[command]time.Duration{"cpu": 15 * time.Second},
			},
		},
	})
}

// TestDecodeInlineMapDestinationReuse checks that the catch-all follows
// decodeMap: untouched with no extra keys, replaced when any arrive.
func TestDecodeInlineMapDestinationReuse(t *testing.T) {
	t.Parallel()
	prefilled := func() any {
		return &timeoutsConfig{Overrides: map[string]time.Duration{"old": time.Minute}}
	}
	runDecodeCases(t, []decodeCase{
		{
			name: "nil_stays_nil", doc: "poll: 1s\n",
			newDst: func() any { return new(timeoutsConfig) },
			want:   &timeoutsConfig{Poll: time.Second},
		},
		{
			name: "existing_entries_kept_without_extras", doc: "poll: 1s\n",
			newDst: prefilled,
			want: &timeoutsConfig{
				Poll: time.Second, Overrides: map[string]time.Duration{"old": time.Minute},
			},
		},
		{
			name: "replaced_with_extras", doc: "poll: 1s\ncpu: 15s\n",
			newDst: prefilled,
			want: &timeoutsConfig{
				Poll: time.Second, Overrides: map[string]time.Duration{"cpu": 15 * time.Second},
			},
		},
	})
}

// TestDecodeInlineMapEntryValues covers null, zero and invalid entries, and
// the change for the wrapper key the old decoder invented.
func TestDecodeInlineMapEntryValues(t *testing.T) {
	t.Parallel()
	runDecodeCases(t, []decodeCase{
		{
			name: "null_entry_is_zero", doc: "cpu: null\n",
			newDst: func() any { return new(timeoutsConfig) },
			want:   &timeoutsConfig{Overrides: map[string]time.Duration{"cpu": 0}},
		},
		{
			name: "zero_entry", doc: "cpu: 0s\n",
			newDst: func() any { return new(timeoutsConfig) },
			want:   &timeoutsConfig{Overrides: map[string]time.Duration{"cpu": 0}},
		},
		{
			name: "invalid_entry_located", doc: "timeouts:\n  poll: 1s\n  cpu: banana\n",
			newDst: func() any { return new(pluginTimeouts) },
			err:    validationAt("/timeouts/cpu", "config.yaml:3:8", "cannot parse value as time.Duration"),
		},
		{
			name: "wrapper_key_no_longer_accepted", doc: "timeouts:\n  overrides:\n    cpu: 15s\n",
			newDst: func() any { return new(pluginTimeouts) },
			err:    validationAt("/timeouts/overrides", "config.yaml:3:5", "expected string for time.Duration"),
		},
		{
			name: "wrapper_key_is_an_entry", doc: "overrides: 15s\n",
			newDst: func() any { return new(timeoutsConfig) },
			want: &timeoutsConfig{
				Overrides: map[string]time.Duration{"overrides": 15 * time.Second},
			},
		},
	})
}

// TestDecodeInlineMapPresence checks that omitted, null and present entries
// stay distinct at their flat pointers.
func TestDecodeInlineMapPresence(t *testing.T) {
	t.Parallel()
	dir := writeYAMLFile(t, "config.yaml", "timeouts:\n  poll: 1s\n  cpu: null\n  mem: 0s\n")
	root := readYAMLFile(t, dir, "config.yaml")
	for ptr, want := range map[ashlar.Pointer]ashlar.Presence{
		"/timeouts/disk": ashlar.PresenceOmitted,
		"/timeouts/cpu":  ashlar.PresenceNull,
		"/timeouts/mem":  ashlar.PresencePresent,
	} {
		if got := root.PresenceAt(ptr); got != want {
			t.Errorf("PresenceAt(%s) = %v, want %v", ptr, got, want)
		}
	}
	var dst pluginTimeouts
	if err := yamlconfig.DecodeValueStrict(root, &dst); err != nil {
		t.Fatalf("DecodeValueStrict error = %v", err)
	}
	want := map[string]time.Duration{"cpu": 0, "mem": 0}
	if len(dst.Timeouts.Overrides) != len(want) {
		t.Fatalf("Overrides = %v, want %v", dst.Timeouts.Overrides, want)
	}
	if _, ok := dst.Timeouts.Overrides["disk"]; ok {
		t.Errorf("Overrides has an entry for the omitted key: %v", dst.Timeouts.Overrides)
	}
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

// TestDecodeInlineMapRedaction checks that a sensitive catch-all entry stays
// redacted in decode errors and provenance output, under the default
// classification and under a caller-supplied redactor.
func TestDecodeInlineMapRedaction(t *testing.T) {
	t.Parallel()
	const secret = "s3cr3t-banana"
	cases := []struct {
		name   string
		key    string
		policy yamlconfig.CompositionPolicy
	}{
		{name: "default_redactor", key: "api_token", policy: yamlconfig.DefaultCompositionPolicy()},
		{name: "caller_redactor", key: "pin", policy: yamlconfig.CompositionPolicy{
			Redactor: ashlar.RedactorFunc(func(_ ashlar.Pointer, key string) bool { return key == "pin" }),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := "poll: 1s\n" + tc.key + ": " + secret + "\n"
			dir := writeYAMLFile(t, "config.yaml", doc)
			res, err := yamlconfig.Resolve(t.Context(), os.DirFS(dir), "config.yaml",
				yamlconfig.ResolveOptions{Policy: tc.policy})
			if err != nil {
				t.Fatalf("Resolve error = %v", err)
			}
			var dst timeoutsConfig
			err = res.DecodeStrict(&dst)
			requirePointerError(t, err, ashlar.ErrValidation, ashlar.RootPointer.Child(tc.key), "config.yaml:2:"+colAfter(tc.key))
			assertNoSecrets(t, err, secret)
			for what, text := range map[string]string{
				"FormatProvenanceReport": res.FormatProvenanceReport(),
				"Explain":                res.Explain(ashlar.RootPointer.Child(tc.key)),
			} {
				if strings.Contains(text, secret) {
					t.Errorf("%s leaks the secret: %q", what, text)
				}
			}
		})
	}
}

// colAfter returns the column where a scalar value starts after "key: ".
func colAfter(key string) string {
	return strconv.Itoa(len(key) + 3)
}

// TestDecodeInlineMapProvenance checks that an entry keeps its flat pointer
// and source origin, including keys that need RFC 6901 escaping, and that an
// included file's override wins with its own origin.
func TestDecodeInlineMapProvenance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"main.yaml":  "include:\n  - extra.yaml\npoll: 1s\ncpu: 10s\na/b: 2s\nc~d: 3s\n",
		"extra.yaml": "cpu: 15s\n",
	})
	res, err := yamlconfig.Resolve(t.Context(), os.DirFS(dir), "main.yaml", yamlconfig.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	var dst timeoutsConfig
	if err := res.DecodeStrict(&dst); err != nil {
		t.Fatalf("DecodeStrict error = %v", err)
	}
	wantMap := map[string]time.Duration{"cpu": 15 * time.Second, "a/b": 2 * time.Second, "c~d": 3 * time.Second}
	for k, v := range wantMap {
		if dst.Overrides[k] != v {
			t.Errorf("Overrides[%q] = %v, want %v", k, dst.Overrides[k], v)
		}
	}
	for ptr, wantLoc := range map[ashlar.Pointer]string{
		"/cpu":  "extra.yaml:1:6",
		"/a~1b": "main.yaml:5:6",
		"/c~0d": "main.yaml:6:6",
		"/poll": "main.yaml:3:7",
	} {
		if got := res.Explain(ptr); !strings.Contains(got, wantLoc) {
			t.Errorf("Explain(%s) = %q, want it to contain %q", ptr, got, wantLoc)
		}
	}
}
