package jsoncontract_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
)

func TestReadPreservesRawBytesNumberLexemesAndPresence(t *testing.T) {
	t.Parallel()

	const rawManifest = `{
  "manifest_version": 2,
  "plugin": "echo",
  "int_one": 1,
  "float_one": 1.0,
  "exp_one": 1e0,
  "max_uint64": 18446744073709551615,
  "explicit_null": null,
  "explicit_false": false,
  "explicit_zero": 0,
  "explicit_empty_str": "",
  "explicit_empty_arr": [],
  "literal_env": "${DO_NOT_INTERPOLATE}",
  "include": ["literal_not_included.json"]
}
`
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(rawManifest), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	doc, err := jsoncontract.ReadFS(os.DirFS(dir), "manifest.json")
	if err != nil {
		t.Fatalf("ReadFS() unexpected error: %v", err)
	}
	if got := doc.Source(); got != "manifest.json" {
		t.Errorf("doc.Source() = %q, want %q", got, "manifest.json")
	}

	// Verify RawBytes preserves exact bytes and returns a defensive copy.
	gotRaw := doc.RawBytes()
	if !bytes.Equal(gotRaw, []byte(rawManifest)) {
		t.Fatalf("RawBytes() mismatch:\ngot:  %q\nwant: %q", gotRaw, rawManifest)
	}
	gotRaw[0] = 'X'
	if !bytes.Equal(doc.RawBytes(), []byte(rawManifest)) {
		t.Fatal("RawBytes() mutated internal document bytes")
	}

	root := doc.Root()
	// Verify exact number lexeme preservation.
	lexemeCases := []struct {
		ptr  ashlar.Pointer
		want string
		line int
	}{
		{ashlar.NewPointer("int_one"), "1", 4},
		{ashlar.NewPointer("float_one"), "1.0", 5},
		{ashlar.NewPointer("exp_one"), "1e0", 6},
		{ashlar.NewPointer("max_uint64"), "18446744073709551615", 7},
	}
	for _, tc := range lexemeCases {
		v := root.At(tc.ptr)
		rawLex, ok := v.NumberLexeme()
		if !ok {
			t.Fatalf("root.At(%s) not a Number", tc.ptr)
		}
		if rawLex != tc.want {
			t.Errorf("root.At(%s).NumberLexeme() = %q, want %q", tc.ptr, rawLex, tc.want)
		}
		if gotLine := v.Provenance().Effective.Span.Start.Line; gotLine != tc.line {
			t.Errorf("root.At(%s) line = %d, want %d", tc.ptr, gotLine, tc.line)
		}
	}

	// Verify Presence differentiation (omitted vs null vs zero values).
	if got := root.At(ashlar.NewPointer("missing_field")).Presence(); got != ashlar.PresenceOmitted {
		t.Errorf("missing_field Presence = %v, want PresenceOmitted", got)
	}
	if got := root.At(ashlar.NewPointer("explicit_null")).Presence(); got != ashlar.PresenceNull {
		t.Errorf("explicit_null Presence = %v, want PresenceNull", got)
	}
	for _, key := range []string{"explicit_false", "explicit_zero", "explicit_empty_str", "explicit_empty_arr"} {
		if got := root.At(ashlar.NewPointer(key)).Presence(); got != ashlar.PresencePresent {
			t.Errorf("%s Presence = %v, want PresencePresent", key, got)
		}
	}

	// Verify quarantine: no env interpolation or include expansion occurs.
	if s, _ := root.At(ashlar.NewPointer("literal_env")).StringVal(); s != "${DO_NOT_INTERPOLATE}" {
		t.Errorf("literal_env = %q, want uninterpolated literal", s)
	}
}

func TestReadRejectsDuplicateKeysAtAnyDepth(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		input       string
		wantPtr     string
		wantDupLoc  string
		wantOrigLoc string
	}{
		{
			name: "root_duplicate_key",
			input: `{
  "version": 1,
  "name": "contract",
  "version": 2
}`,
			wantPtr:     "/version",
			wantDupLoc:  "contract.json:4:3",
			wantOrigLoc: "contract.json:2:3",
		},
		{
			name: "nested_object_duplicate_key",
			input: `{
  "plugin": {
    "commands": [
      {
        "name": "poll",
        "type": "read",
        "name": "handle"
      }
    ]
  }
}`,
			wantPtr:     "/plugin/commands/0/name",
			wantDupLoc:  "contract.json:7:9",
			wantOrigLoc: "contract.json:5:9",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := jsoncontract.Read("contract.json", []byte(tc.input))
			if !errors.Is(err, ashlar.ErrDuplicateKey) {
				t.Fatalf("Read() error = %v, want ErrDuplicateKey", err)
			}
			var aErr *ashlar.Error
			if !errors.As(err, &aErr) {
				t.Fatalf("expected *ashlar.Error, got %T", err)
			}
			if got := aErr.Pointer.String(); got != tc.wantPtr {
				t.Errorf("aErr.Pointer = %q, want %q", got, tc.wantPtr)
			}
			if got := aErr.Origin.Location(); got != tc.wantDupLoc {
				t.Errorf("aErr.Origin.Location() = %q, want %q", got, tc.wantDupLoc)
			}
			if !strings.Contains(aErr.Error(), tc.wantOrigLoc) {
				t.Errorf("aErr.Error() = %q, want mention of first defined location %q", aErr.Error(), tc.wantOrigLoc)
			}
		})
	}
}

func TestReadRejectsTrailingContent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		wantLoc string
	}{
		{
			name:    "second_json_document",
			input:   "{\"id\": 1}\n{\"id\": 2}\n",
			wantLoc: "manifest.json:2:1",
		},
		{
			name:    "trailing_scalar",
			input:   "[1, 2, 3] true",
			wantLoc: "manifest.json:1:11",
		},
		{
			name:    "trailing_comma_after_root",
			input:   "{\"ok\": true},",
			wantLoc: "manifest.json:1:13",
		},
		{
			name:    "trailing_invalid_character",
			input:   "{\"ok\": true}\n@",
			wantLoc: "manifest.json:2:2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := jsoncontract.Read("manifest.json", []byte(tc.input))
			if !errors.Is(err, ashlar.ErrTrailingContent) {
				t.Fatalf("Read() error = %v, want ErrTrailingContent", err)
			}
			var aErr *ashlar.Error
			if !errors.As(err, &aErr) {
				t.Fatalf("expected *ashlar.Error, got %T", err)
			}
			if got := aErr.Origin.Location(); got != tc.wantLoc {
				t.Errorf("Origin.Location() = %q, want %q", got, tc.wantLoc)
			}
		})
	}
}

func TestReadRejectsSyntaxErrorsWithLocation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input []byte
	}{
		{"empty_input", []byte("   \n  ")},
		{"invalid_utf8", []byte{0xff, 0xfe}},
		{"leading_zero", []byte(`{"port": 0123}`)},
		{"trailing_comma_object", []byte(`{"a": 1,}`)},
		{"trailing_comma_array", []byte(`[1, 2,]`)},
		{"unquoted_key", []byte(`{key: 1}`)},
		{"missing_colon", []byte(`{"key" 1}`)},
		{"unescaped_newline_in_string", []byte("{\"key\": \"line1\nline2\"}")},
		{"unterminated_string", []byte(`{"key": "unterminated`)},
		{"bare_minus", []byte(`{"n": -}`)},
		{"trailing_dot", []byte(`{"n": 1.}`)},
		{"incomplete_exponent", []byte(`{"n": 1e+}`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := jsoncontract.Read("bad.json", tc.input)
			if !errors.Is(err, ashlar.ErrSyntax) {
				t.Fatalf("Read(%q) error = %v, want ErrSyntax", tc.name, err)
			}
		})
	}
}

type sampleContract struct {
	ManifestVersion int            `json:"manifest_version"`
	Name            string         `json:"name"`
	Endpoints       []endpointSpec `json:"endpoints"`
}

type endpointSpec struct {
	Path   string `json:"path"`
	Method string `json:"method"`
}

func TestDecodeStrict(t *testing.T) {
	t.Parallel()

	validJSON := `{
  "manifest_version": 1,
  "name": "billing",
  "endpoints": [
    {"path": "/v1/charge", "method": "POST"}
  ]
}`
	doc, err := jsoncontract.Read("contract.json", []byte(validJSON))
	if err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}

	var got sampleContract
	if err := doc.DecodeStrict(&got); err != nil {
		t.Fatalf("DecodeStrict() unexpected error: %v", err)
	}
	if got.ManifestVersion != 1 || got.Name != "billing" || len(got.Endpoints) != 1 || got.Endpoints[0].Path != "/v1/charge" {
		t.Errorf("DecodeStrict() decoded %+v, unexpected field values", got)
	}

	// Unknown nested field must fail with ErrUnknownField, exact Pointer, and file:line:column.
	unknownFieldJSON := `{
  "manifest_version": 1,
  "name": "billing",
  "endpoints": [
    {
      "path": "/v1/charge",
      "method": "POST",
      "timout_ms": 500
    }
  ]
}`
	docUnknown, err := jsoncontract.Read("contract.json", []byte(unknownFieldJSON))
	if err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}
	var dst sampleContract
	err = docUnknown.DecodeStrict(&dst)
	if !errors.Is(err, ashlar.ErrUnknownField) {
		t.Fatalf("DecodeStrict() error = %v, want ErrUnknownField", err)
	}
	var aErr *ashlar.Error
	if !errors.As(err, &aErr) {
		t.Fatalf("expected *ashlar.Error, got %T", err)
	}
	if gotPtr, wantPtr := aErr.Pointer.String(), "/endpoints/0/timout_ms"; gotPtr != wantPtr {
		t.Errorf("aErr.Pointer = %q, want %q", gotPtr, wantPtr)
	}
	if gotLoc, wantLoc := aErr.Origin.Location(), "contract.json:8:7"; gotLoc != wantLoc {
		t.Errorf("aErr.Origin.Location() = %q, want %q", gotLoc, wantLoc)
	}

	// Case-mismatched field ("NAME" instead of "name") must also be rejected as ErrUnknownField.
	caseMismatchJSON := `{"manifest_version": 1, "NAME": "billing"}`
	docCase, err := jsoncontract.Read("contract.json", []byte(caseMismatchJSON))
	if err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}
	if err := docCase.DecodeStrict(&dst); !errors.Is(err, ashlar.ErrUnknownField) {
		t.Fatalf("case-mismatched field DecodeStrict() error = %v, want ErrUnknownField", err)
	}

	// Type mismatch must fail with ErrValidation and offending Pointer + Origin.
	typeMismatchJSON := `{
  "manifest_version": "not-an-int",
  "name": "billing"
}`
	docType, err := jsoncontract.Read("contract.json", []byte(typeMismatchJSON))
	if err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}
	err = docType.DecodeStrict(&dst)
	if !errors.Is(err, ashlar.ErrValidation) {
		t.Fatalf("type mismatch DecodeStrict() error = %v, want ErrValidation", err)
	}
	if errors.As(err, &aErr) {
		if aErr.Pointer.String() != "/manifest_version" {
			t.Errorf("type mismatch Pointer = %q, want /manifest_version", aErr.Pointer)
		}
		if aErr.Origin.Location() != "contract.json:2:23" {
			t.Errorf("type mismatch Location = %q, want contract.json:2:23", aErr.Origin.Location())
		}
	}
}
