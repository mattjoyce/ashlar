package yamlconfig_test

import (
	"reflect"
	"testing"

	"github.com/mattjoyce/ashlar"
)

// atomInner is a closed struct reached through a pointer, a slice and a map.
type atomInner struct {
	Name string `yaml:"name"`
}

// atomConfig has scalars before and after nested closed structs, so an
// unknown key late in the document follows fields that would already be set.
type atomConfig struct {
	Host   string               `yaml:"host"`
	Port   int                  `yaml:"port"`
	Inner  *atomInner           `yaml:"inner"`
	Routes []atomInner          `yaml:"routes"`
	ByName map[string]atomInner `yaml:"by_name"`
	Debug  bool                 `yaml:"debug"`
}

// TestDecodeUnknownKeyLeavesDestinationUntouched checks that an unknown key
// anywhere in the document, at any depth, fails before anything is written.
// The destination keeps the defaults the caller filled in, and no pointer is
// allocated.
func TestDecodeUnknownKeyLeavesDestinationUntouched(t *testing.T) {
	t.Parallel()

	head := "host: new-host\nport: 9000\ninner:\n  name: x\n"
	cases := []struct {
		name string
		doc  string
		ptr  ashlar.Pointer
		loc  string
	}{
		{"top_level_last", head + "bogus: 1\n", "/bogus", "config.yaml:5:1"},
		{"in_pointer_struct", "host: new-host\ninner:\n  name: x\n  bogus: 1\n", "/inner/bogus", "config.yaml:4:3"},
		{"in_slice_element", head + "routes:\n  - name: a\n  - bogus: 1\n", "/routes/1/bogus", "config.yaml:7:5"},
		{"in_map_value", head + "by_name:\n  a:\n    bogus: 1\n", "/by_name/a/bogus", "config.yaml:7:5"},
	}
	for _, tc := range cases {
		for _, ep := range decodeEntryPoints {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				dir := writeYAMLFile(t, "config.yaml", tc.doc)
				dst := atomConfig{Host: "default-host", Port: 80, Debug: true}
				want := dst

				err := ep.decode(t, dir, "config.yaml", &dst)

				requirePointerError(t, err, ashlar.ErrUnknownField, tc.ptr, tc.loc)
				if !reflect.DeepEqual(dst, want) {
					t.Errorf("destination changed on unknown key: got %+v, want %+v", dst, want)
				}
			})
		}
	}
}

// TestDecodeValueErrorMayLeaveDestinationPartlyWritten pins the documented
// limit of the guarantee: a value error, unlike an unknown key, is found while
// decoding, so fields before it are already set. Callers who need
// all-or-nothing decode into a fresh value and assign it on success.
func TestDecodeValueErrorMayLeaveDestinationPartlyWritten(t *testing.T) {
	t.Parallel()
	for _, ep := range decodeEntryPoints {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()
			dir := writeYAMLFile(t, "config.yaml", "host: new-host\nport: banana\n")
			dst := atomConfig{Host: "default-host"}

			err := ep.decode(t, dir, "config.yaml", &dst)

			requirePointerError(t, err, ashlar.ErrValidation, "/port", "config.yaml:2:7")
			if dst.Host != "new-host" {
				t.Errorf("Host = %q, want the field before the error already written", dst.Host)
			}
		})
	}
}
