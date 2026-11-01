package yamlconfig_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

// locatedWant describes the located failure a decode case must produce.
type locatedWant struct {
	kind     error
	ptr      ashlar.Pointer
	loc      string
	contains string // substring of Error(), when non-empty
	is       error  // extra target for errors.Is, when non-nil
}

// decodeCase decodes doc (written to config.yaml) into newDst(). Exactly one
// of want (a pointer equal to the decoded destination) and err is set.
type decodeCase struct {
	name   string
	doc    string
	newDst func() any
	want   any
	err    *locatedWant
}

// runDecodeCases runs every case through both public decode entry points.
func runDecodeCases(t *testing.T, cases []decodeCase) {
	t.Helper()
	for _, tc := range cases {
		for _, ep := range decodeEntryPoints {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				dir := writeYAMLFile(t, "config.yaml", tc.doc)
				dst := tc.newDst()
				err := ep.decode(t, dir, "config.yaml", dst)
				if tc.err != nil {
					requirePointerError(t, err, tc.err.kind, tc.err.ptr, tc.err.loc)
					if tc.err.contains != "" && !strings.Contains(err.Error(), tc.err.contains) {
						t.Errorf("Error() = %q, want it to contain %q", err.Error(), tc.err.contains)
					}
					if tc.err.is != nil && !errors.Is(err, tc.err.is) {
						t.Errorf("errors.Is(%v, %v) = false, want true", err, tc.err.is)
					}
					return
				}
				if err != nil {
					t.Fatalf("decode error = %v", err)
				}
				if !reflect.DeepEqual(dst, tc.want) {
					t.Errorf("decoded %#v, want %#v", dst, tc.want)
				}
			})
		}
	}
}

// validationAt is shorthand for a located ErrValidation expectation.
func validationAt(ptr ashlar.Pointer, loc, contains string) *locatedWant {
	return &locatedWant{kind: ashlar.ErrValidation, ptr: ptr, loc: loc, contains: contains}
}

// routeSpec is a struct element decoded from a YAML sequence.
type routeSpec struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// sliceTargets holds every slice shape the sequence cases decode into.
type sliceTargets struct {
	Ports  []int       `yaml:"ports"`
	Hosts  []string    `yaml:"hosts"`
	Routes []routeSpec `yaml:"routes"`
	Matrix [][]string  `yaml:"matrix"`
	IDs    []uint64    `yaml:"ids"`
}

// TestDecodeSequencesIntoSlices checks YAML sequences decoding into scalar,
// struct, and nested slices, including empty and null sequences, and that a
// mismatch fails at the exact element.
func TestDecodeSequencesIntoSlices(t *testing.T) {
	t.Parallel()

	newDst := func() any { return new(sliceTargets) }
	runDecodeCases(t, []decodeCase{
		{
			name: "flow_ints", doc: "ports: [80, 443]\n", newDst: newDst,
			want: &sliceTargets{Ports: []int{80, 443}},
		},
		{
			name: "block_strings", doc: "hosts:\n  - alpha\n  - beta\n", newDst: newDst,
			want: &sliceTargets{Hosts: []string{"alpha", "beta"}},
		},
		{
			name: "structs", doc: "routes:\n  - from: a\n    to: b\n  - from: c\n    to: d\n", newDst: newDst,
			want: &sliceTargets{Routes: []routeSpec{{From: "a", To: "b"}, {From: "c", To: "d"}}},
		},
		{
			name: "nested", doc: "matrix: [[a, b], [c]]\n", newDst: newDst,
			want: &sliceTargets{Matrix: [][]string{{"a", "b"}, {"c"}}},
		},
		{
			name: "uint64_past_int64", doc: "ids: [1, 18446744073709551615]\n", newDst: newDst,
			want: &sliceTargets{IDs: []uint64{1, 18446744073709551615}},
		},
		{
			name: "empty_is_non_nil", doc: "ports: []\n", newDst: newDst,
			want: &sliceTargets{Ports: []int{}},
		},
		{
			name: "null_is_nil", doc: "ports: null\n", newDst: newDst,
			want: &sliceTargets{},
		},
		{
			name: "scalar_for_slice", doc: "ports: 80\n", newDst: newDst,
			err: validationAt("/ports", "config.yaml:1:8", "expected sequence"),
		},
		{
			name: "bad_element", doc: "ports: [80, http]\n", newDst: newDst,
			err: validationAt("/ports/1", "config.yaml:1:13", ""),
		},
		{
			name: "bad_nested_element", doc: "matrix: [[a], b]\n", newDst: newDst,
			err: validationAt("/matrix/1", "config.yaml:1:15", "expected sequence"),
		},
		{
			name: "scalar_for_struct_element", doc: "routes:\n  - from: a\n  - 7\n", newDst: newDst,
			err: validationAt("/routes/1", "config.yaml:3:5", "expected mapping"),
		},
		{
			name: "unknown_field_in_element", doc: "routes:\n  - from: a\n    via: b\n", newDst: newDst,
			err: &locatedWant{kind: ashlar.ErrUnknownField, ptr: "/routes/0/via", loc: "config.yaml:3:5"},
		},
	})
}

// anyTargets holds the empty-interface shapes the any cases decode into.
type anyTargets struct {
	Data   any            `yaml:"data"`
	Labels map[string]any `yaml:"labels"`
	Items  []any          `yaml:"items"`
}

// stringerTarget has a non-empty interface field, which decoding cannot fill.
type stringerTarget struct {
	Name fmt.Stringer `yaml:"name"`
}

// anyDoc exercises every value kind under an any destination.
const anyDoc = `name: gateway
port: 8080
ratio: 1.0
big: 9223372036854775808
huge: 18446744073709551616
enabled: false
nothing: null
tags: [a, b]
nested:
  depth: 2
`

// anyDocWant is anyDoc decoded into a Go any.
func anyDocWant() map[string]any {
	return map[string]any{
		"name":    "gateway",
		"port":    int64(8080),
		"ratio":   1.0,
		"big":     uint64(1 << 63),
		"huge":    18446744073709551616.0,
		"enabled": false,
		"nothing": nil,
		"tags":    []any{"a", "b"},
		"nested":  map[string]any{"depth": int64(2)},
	}
}

// TestDecodeIntoAny checks decoding into any, map[string]any and []any, and
// that a kind mismatch or a non-empty interface fails at the exact node.
func TestDecodeIntoAny(t *testing.T) {
	t.Parallel()

	newDst := func() any { return new(anyTargets) }
	topAny := anyDocWant()
	var topWant any = topAny
	runDecodeCases(t, []decodeCase{
		{
			name: "top_level_any", doc: anyDoc, newDst: func() any { return new(any) },
			want: &topWant,
		},
		{
			name: "top_level_map", doc: anyDoc, newDst: func() any { return new(map[string]any) },
			want: ptrTo(anyDocWant()),
		},
		{
			name: "field_any_scalar", doc: "data: hello\n", newDst: newDst,
			want: &anyTargets{Data: "hello"},
		},
		{
			name: "field_any_null", doc: "data: null\n", newDst: newDst,
			want: &anyTargets{},
		},
		{
			name: "field_map", doc: "labels:\n  team: core\n  tier: 1\n", newDst: newDst,
			want: &anyTargets{Labels: map[string]any{"team": "core", "tier": int64(1)}},
		},
		{
			name: "field_slice", doc: "items: [x, 2, true, null]\n", newDst: newDst,
			want: &anyTargets{Items: []any{"x", int64(2), true, nil}},
		},
		{
			name: "sequence_for_map", doc: "labels: [a, b]\n", newDst: newDst,
			err: validationAt("/labels", "config.yaml:1:9", "expected mapping"),
		},
		{
			name: "mapping_for_slice", doc: "items:\n  key: v\n", newDst: newDst,
			err: validationAt("/items", "config.yaml:2:3", "expected sequence"),
		},
		{
			name: "non_empty_interface", doc: "name: gateway\n", newDst: func() any { return new(stringerTarget) },
			err: validationAt("/name", "config.yaml:1:7", "unsupported destination type fmt.Stringer"),
		},
	})
}

// errUnknownLevel is returned by logLevel.UnmarshalText.
var errUnknownLevel = errors.New("unknown log level")

// logLevel is an encoding.TextUnmarshaler destination.
type logLevel string

// UnmarshalText accepts debug, info and warn.
func (l *logLevel) UnmarshalText(text []byte) error {
	switch s := string(text); s {
	case "debug", "info", "warn":
		*l = logLevel(s)
		return nil
	default:
		return fmt.Errorf("%w %q", errUnknownLevel, s)
	}
}

// textTargets holds TextUnmarshaler destinations.
type textTargets struct {
	Level    logLevel   `yaml:"level"`
	LevelPtr *logLevel  `yaml:"level_ptr"`
	Levels   []logLevel `yaml:"levels"`
}

// TestDecodeTextUnmarshalerTargets checks encoding.TextUnmarshaler
// destinations, directly, through a pointer and inside a slice, and that an
// UnmarshalText error is located and still reachable with errors.Is.
func TestDecodeTextUnmarshalerTargets(t *testing.T) {
	t.Parallel()

	newDst := func() any { return new(textTargets) }
	warn := logLevel("warn")
	runDecodeCases(t, []decodeCase{
		{
			name: "value", doc: "level: debug\n", newDst: newDst,
			want: &textTargets{Level: "debug"},
		},
		{
			name: "pointer", doc: "level_ptr: warn\n", newDst: newDst,
			want: &textTargets{LevelPtr: &warn},
		},
		{
			name: "slice", doc: "levels: [info, warn]\n", newDst: newDst,
			want: &textTargets{Levels: []logLevel{"info", "warn"}},
		},
		{
			name: "rejected_text", doc: "level: loud\n", newDst: newDst,
			err: &locatedWant{
				kind: ashlar.ErrValidation, ptr: "/level", loc: "config.yaml:1:8",
				contains: `unknown log level "loud"`, is: errUnknownLevel,
			},
		},
		{
			name: "number_for_text", doc: "level: 3\n", newDst: newDst,
			err: validationAt("/level", "config.yaml:1:8", "expected string for yamlconfig_test.logLevel"),
		},
		{
			name: "rejected_slice_element", doc: "levels: [info, nope]\n", newDst: newDst,
			err: &locatedWant{
				kind: ashlar.ErrValidation, ptr: "/levels/1", loc: "config.yaml:1:16",
				contains: `unknown log level "nope"`, is: errUnknownLevel,
			},
		},
	})
}

// errBadPort is returned by tcpPort.UnmarshalJSON.
var errBadPort = errors.New("invalid port")

// tcpPort is a json.Unmarshaler destination that accepts 1 through 65535.
type tcpPort int

// UnmarshalJSON decodes a JSON integer in the TCP port range.
func (p *tcpPort) UnmarshalJSON(data []byte) error {
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("%w: %s is not an integer", errBadPort, data)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("%w: %d out of range", errBadPort, n)
	}
	*p = tcpPort(n)
	return nil
}

// upstream is a json.Unmarshaler destination built from a YAML mapping.
type upstream struct {
	Host string
	Port int
}

// UnmarshalJSON decodes {"host": ..., "port": ...} strictly.
func (u *upstream) UnmarshalJSON(data []byte) error {
	var wire struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return fmt.Errorf("upstream: %w", err)
	}
	u.Host, u.Port = wire.Host, wire.Port
	return nil
}

// jsonTargets holds json.Unmarshaler destinations.
type jsonTargets struct {
	Port     tcpPort   `yaml:"port"`
	Ports    []tcpPort `yaml:"ports"`
	Upstream upstream  `yaml:"upstream"`
}

// TestDecodeJSONUnmarshalerTargets checks json.Unmarshaler destinations for
// scalars, slices and mappings, and that an UnmarshalJSON error is located
// and still reachable with errors.Is.
func TestDecodeJSONUnmarshalerTargets(t *testing.T) {
	t.Parallel()

	newDst := func() any { return new(jsonTargets) }
	runDecodeCases(t, []decodeCase{
		{
			name: "scalar", doc: "port: 8080\n", newDst: newDst,
			want: &jsonTargets{Port: 8080},
		},
		{
			name: "slice", doc: "ports: [80, 443]\n", newDst: newDst,
			want: &jsonTargets{Ports: []tcpPort{80, 443}},
		},
		{
			name: "mapping", doc: "upstream:\n  host: db\n  port: 5432\n", newDst: newDst,
			want: &jsonTargets{Upstream: upstream{Host: "db", Port: 5432}},
		},
		{
			name: "out_of_range", doc: "port: 70000\n", newDst: newDst,
			err: &locatedWant{
				kind: ashlar.ErrValidation, ptr: "/port", loc: "config.yaml:1:7",
				contains: "70000 out of range", is: errBadPort,
			},
		},
		{
			name: "string_for_int", doc: "port: http\n", newDst: newDst,
			err: &locatedWant{kind: ashlar.ErrValidation, ptr: "/port", loc: "config.yaml:1:7", is: errBadPort},
		},
		{
			name: "rejected_slice_element", doc: "ports: [80, 0]\n", newDst: newDst,
			err: &locatedWant{
				kind: ashlar.ErrValidation, ptr: "/ports/1", loc: "config.yaml:1:13",
				contains: "0 out of range", is: errBadPort,
			},
		},
		{
			name: "rejected_mapping", doc: "upstream:\n  host: db\n  weight: 3\n", newDst: newDst,
			err: validationAt("/upstream", "config.yaml:2:3", `unknown field "weight"`),
		},
	})
}

// durationTargets holds time.Duration destinations.
type durationTargets struct {
	Timeout    time.Duration   `yaml:"timeout"`
	TimeoutPtr *time.Duration  `yaml:"timeout_ptr"`
	Timeouts   []time.Duration `yaml:"timeouts"`
}

// TestDecodeDurationTargets checks time.Duration destinations, directly,
// through a pointer and inside a slice, and that bad input fails at the node.
func TestDecodeDurationTargets(t *testing.T) {
	t.Parallel()

	newDst := func() any { return new(durationTargets) }
	twoHours := 2 * time.Hour
	runDecodeCases(t, []decodeCase{
		{
			name: "value", doc: "timeout: 1m30s\n", newDst: newDst,
			want: &durationTargets{Timeout: 90 * time.Second},
		},
		{
			name: "pointer", doc: "timeout_ptr: 2h\n", newDst: newDst,
			want: &durationTargets{TimeoutPtr: &twoHours},
		},
		{
			name: "slice", doc: "timeouts: [1s, 250ms]\n", newDst: newDst,
			want: &durationTargets{Timeouts: []time.Duration{time.Second, 250 * time.Millisecond}},
		},
		{
			name: "unparseable", doc: "timeout: soon\n", newDst: newDst,
			err: validationAt("/timeout", "config.yaml:1:10", `invalid duration "soon"`),
		},
		{
			name: "bare_number", doc: "timeout: 30\n", newDst: newDst,
			err: validationAt("/timeout", "config.yaml:1:10", "expected duration string"),
		},
		{
			name: "bad_slice_element", doc: "timeouts: [1s, later]\n", newDst: newDst,
			err: validationAt("/timeouts/1", "config.yaml:1:16", `invalid duration "later"`),
		},
	})
}

// echoText is a TextUnmarshaler whose error quotes its whole input.
type echoText struct{}

// UnmarshalText always fails, echoing text.
func (echoText) UnmarshalText(text []byte) error { return fmt.Errorf("rejected %s", text) }

// echoJSON is a json.Unmarshaler whose error quotes its whole input.
type echoJSON struct{}

// UnmarshalJSON always fails, echoing data.
func (echoJSON) UnmarshalJSON(data []byte) error { return fmt.Errorf("rejected %s", data) }

// errorTexts returns the text of err rendered every way a caller might print
// it, plus the Error() of every error reachable through Unwrap.
func errorTexts(err error) []string {
	out := []string{
		err.Error(),
		fmt.Sprintf("%v", err),
		fmt.Sprintf("%+v", err),
		fmt.Sprintf("%#v", err),
		fmt.Sprintf("%s", err),
		fmt.Sprintf("%q", err),
	}
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		out = append(out, e.Error())
		switch u := e.(type) {
		case interface{ Unwrap() error }:
			walk(u.Unwrap())
		case interface{ Unwrap() []error }:
			for _, inner := range u.Unwrap() {
				walk(inner)
			}
		}
	}
	walk(err)
	return out
}

// TestDecodeCustomUnmarshalerErrorRedactsSensitiveNode checks that when a
// custom unmarshaler's error echoes its input, the decode error for a
// sensitive node carries the location but never the secret. A node is
// sensitive by its own key, by an ancestor key, by a caller redactor frozen
// in at resolution, or because it is a container holding a sensitive child.
func TestDecodeCustomUnmarshalerErrorRedactsSensitiveNode(t *testing.T) {
	t.Parallel()

	dsnPolicy := yamlconfig.DefaultCompositionPolicy()
	dsnPolicy.Redactor = ashlar.CombineRedactors(
		ashlar.DefaultRedactor(),
		ashlar.RedactorFunc(func(_ ashlar.Pointer, key string) bool { return key == "dsn" }),
	)

	type apiTokenText struct {
		APIToken echoText `yaml:"api_token"`
	}
	type apiTokenJSON struct {
		APIToken echoJSON `yaml:"api_token"`
	}
	type authJSON struct {
		Auth echoJSON `yaml:"auth"`
	}
	type tokensJSON struct {
		Tokens echoJSON `yaml:"tokens"`
	}
	type dsnText struct {
		DSN echoText `yaml:"dsn"`
	}

	cases := []struct {
		name    string
		doc     string
		newDst  func() any
		secrets []string
		ptr     ashlar.Pointer
		loc     string
		// policy, when set, limits the case to Resolve under that policy:
		// the node is sensitive only to a caller redactor.
		policy *yamlconfig.CompositionPolicy
	}{
		{
			name: "text_sensitive_key", doc: "api_token: s3cr3t-text-0001\n",
			newDst: func() any { return new(apiTokenText) }, secrets: []string{"s3cr3t-text-0001"},
			ptr: "/api_token", loc: "config.yaml:1:12",
		},
		{
			name: "json_sensitive_key", doc: "api_token: s3cr3t-json-0002\n",
			newDst: func() any { return new(apiTokenJSON) }, secrets: []string{"s3cr3t-json-0002"},
			ptr: "/api_token", loc: "config.yaml:1:12",
		},
		{
			name: "json_sequence_under_sensitive_key", doc: "tokens: [tok-seq-0003a, tok-seq-0003b]\n",
			newDst: func() any { return new(tokensJSON) }, secrets: []string{"tok-seq-0003a", "tok-seq-0003b"},
			ptr: "/tokens", loc: "config.yaml:1:9",
		},
		{
			name: "json_mapping_with_sensitive_child", doc: "auth:\n  user: bob\n  password: hunter2-nested-0004\n",
			newDst: func() any { return new(authJSON) }, secrets: []string{"hunter2-nested-0004"},
			ptr: "/auth", loc: "config.yaml:2:3",
		},
		{
			name: "text_caller_redactor", doc: "dsn: postgres://app:pw-custom-0005@db/app\n",
			newDst: func() any { return new(dsnText) }, secrets: []string{"pw-custom-0005"},
			ptr: "/dsn", loc: "config.yaml:1:6", policy: &dsnPolicy,
		},
	}
	for _, tc := range cases {
		for _, ep := range decodeEntryPoints {
			if tc.policy != nil && ep.name != "Resolved.DecodeStrict" {
				continue
			}
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				dir := writeYAMLFile(t, "config.yaml", tc.doc)
				dst := tc.newDst()
				var err error
				if tc.policy != nil {
					res, rerr := yamlconfig.Resolve(t.Context(), os.DirFS(dir), "config.yaml",
						yamlconfig.ResolveOptions{Policy: *tc.policy})
					if rerr != nil {
						t.Fatalf("Resolve() error = %v", rerr)
					}
					err = res.DecodeStrict(dst)
				} else {
					err = ep.decode(t, dir, "config.yaml", dst)
				}
				requirePointerError(t, err, ashlar.ErrValidation, tc.ptr, tc.loc)
				texts := errorTexts(err)
				for _, secret := range tc.secrets {
					for _, text := range texts {
						if strings.Contains(text, secret) {
							t.Errorf("secret %q leaked in error text %q", secret, text)
							break
						}
					}
				}
			})
		}
	}

	// Control: the same echoing unmarshaler on a non-sensitive key does show
	// its input, so the redaction cases above are not passing vacuously.
	t.Run("control_non_sensitive_key_echoes", func(t *testing.T) {
		t.Parallel()
		type labelText struct {
			Label echoText `yaml:"label"`
		}
		dir := writeYAMLFile(t, "config.yaml", "label: visible-0006\n")
		var dst labelText
		err := yamlconfig.DecodeValueStrict(readYAMLFile(t, dir, "config.yaml"), &dst)
		requirePointerError(t, err, ashlar.ErrValidation, "/label", "config.yaml:1:8")
		if !strings.Contains(err.Error(), "rejected visible-0006") {
			t.Errorf("Error() = %q, want the unmarshaler text for a non-sensitive key", err.Error())
		}
	})
}
