package jsoncontract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"strings"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
)

// errUnmarshalSentinel is the sentinel error a custom unmarshaler wraps so a
// caller can still reach the underlying unmarshaler error via errors.Is.
var errUnmarshalSentinel = errors.New("unmarshal sentinel")

// secretJSON fails with an error that quotes its input, mimicking an
// UnmarshalJSON method that echoes a secret into its error text.
type secretJSON string

func (s *secretJSON) UnmarshalJSON(data []byte) error {
	return fmt.Errorf("%w: bad input %s", errUnmarshalSentinel, data)
}

// secretText fails with an error that quotes its input, mimicking an
// UnmarshalText method that echoes a secret into its error text.
type secretText string

func (s *secretText) UnmarshalText(data []byte) error {
	return fmt.Errorf("%w: bad input %s", errUnmarshalSentinel, data)
}

// Blob is a named []byte type exercised by the byte-slice base64 case.
type Blob []byte

// acceptRow is a table row whose JSON decodes successfully with both
// encoding/json and Ashlar's DecodeStrict into equal destinations.
type acceptRow struct {
	desc   string
	json   string
	newDst func() any
}

// rejectRow is a table row whose JSON both encoders reject with a kind of
// error that must be satisfied by the Ashlar error, and (when pointer is
// non-empty) whose *ashlar.Error pointer must equal pointer.
type rejectRow struct {
	desc    string
	json    string
	newDst  func() any
	kind    error
	pointer string
}

func TestDecodeStrictMatchesEncodingJSON(t *testing.T) {
	t.Parallel()

	type quotedInt struct {
		Port int `json:"port,string"`
	}
	type quotedPtrInt struct {
		Port *int `json:"port,string"`
	}
	type quotedBool struct {
		On bool `json:"on,string"`
	}
	type quotedString struct {
		Name string `json:"name,string"`
	}
	type intKeys struct {
		Inner struct {
			Ports map[int]string `json:"ports"`
		} `json:"inner"`
	}
	type byteSlice struct {
		Data []byte `json:"data"`
	}
	type namedByteSlice struct {
		Data Blob `json:"data"`
	}
	type byteArray struct {
		Data [2]byte `json:"data"`
	}
	type nestedQuoted struct {
		Items []struct {
			N int `json:"n,string"`
		} `json:"items"`
	}
	type nestedInt struct {
		Items []struct {
			N int `json:"n"`
		} `json:"items"`
	}
	type plainPort struct {
		Port int `json:"port"`
	}

	accept := []acceptRow{
		{
			desc:   "quoted int",
			json:   `{"port":"8080"}`,
			newDst: func() any { return new(quotedInt) },
		},
		{
			desc:   "quoted pointer int",
			json:   `{"port":"7"}`,
			newDst: func() any { return new(quotedPtrInt) },
		},
		{
			desc:   "quoted bool",
			json:   `{"on":"true"}`,
			newDst: func() any { return new(quotedBool) },
		},
		{
			desc:   "quoted null",
			json:   `{"port":null}`,
			newDst: func() any { return new(quotedInt) },
		},
		{
			desc:   "byte slice base64",
			json:   `{"data":"aGVsbG8="}`,
			newDst: func() any { return new(byteSlice) },
		},
		{
			desc:   "named byte slice",
			json:   `{"data":"aGVsbG8="}`,
			newDst: func() any { return new(namedByteSlice) },
		},
		{
			desc:   "byte slice array",
			json:   `{"data":[104,105]}`,
			newDst: func() any { return new(byteSlice) },
		},
		{
			desc:   "nested quoted in slice",
			json:   `{"items":[{"n":"3"}]}`,
			newDst: func() any { return new(nestedQuoted) },
		},
	}

	accept = append(accept,
		acceptRow{desc: "quoted null literal", json: `{"port":"null"}`, newDst: func() any { return new(quotedInt) }},
		acceptRow{desc: "quoted pointer null literal", json: `{"port":"null"}`, newDst: func() any { return new(quotedPtrInt) }},
		acceptRow{desc: "quoted string literal", json: `{"name":"\"x\""}`, newDst: func() any { return new(quotedString) }},
		acceptRow{desc: "quoted pointer int", json: `{"port":"8080"}`, newDst: func() any { return new(quotedPtrInt) }},
		acceptRow{desc: "int map keys", json: `{"inner":{"ports":{"80":"http"}}}`, newDst: func() any { return new(intKeys) }},
	)

	reject := []rejectRow{
		{
			desc:    "quoted bad content",
			json:    `{"port":"abc"}`,
			newDst:  func() any { return new(quotedInt) },
			kind:    ashlar.ErrValidation,
			pointer: "/port",
		},
		{
			desc:    "quoted leading space",
			json:    `{"port":" 5"}`,
			newDst:  func() any { return new(quotedInt) },
			kind:    ashlar.ErrValidation,
			pointer: "/port",
		},
		{
			desc:    "quoted trailing space",
			json:    `{"port":"5 "}`,
			newDst:  func() any { return new(quotedInt) },
			kind:    ashlar.ErrValidation,
			pointer: "/port",
		},
		{
			desc:    "quoted fraction into int",
			json:    `{"port":"1.5"}`,
			newDst:  func() any { return new(quotedInt) },
			kind:    ashlar.ErrValidation,
			pointer: "/port",
		},
		{
			desc:    "quoted nested string into int",
			json:    `{"port":"\"5\""}`,
			newDst:  func() any { return new(quotedInt) },
			kind:    ashlar.ErrValidation,
			pointer: "/port",
		},
		{
			desc:    "quoted bad pointer content",
			json:    `{"port":"x"}`,
			newDst:  func() any { return new(quotedPtrInt) },
			kind:    ashlar.ErrValidation,
			pointer: "/port",
		},
		{
			desc:    "quoted bool from digit",
			json:    `{"on":"1"}`,
			newDst:  func() any { return new(quotedBool) },
			kind:    ashlar.ErrValidation,
			pointer: "/on",
		},
		{
			desc:    "quoted string without inner quotes",
			json:    `{"name":"x"}`,
			newDst:  func() any { return new(quotedString) },
			kind:    ashlar.ErrValidation,
			pointer: "/name",
		},
		{
			desc:    "non-integer map key",
			json:    `{"inner":{"ports":{"abc":"x"}}}`,
			newDst:  func() any { return new(intKeys) },
			kind:    ashlar.ErrValidation,
			pointer: "/inner/ports",
		},
		{
			desc:    "byte array from string",
			json:    `{"data":"aGk="}`,
			newDst:  func() any { return new(byteArray) },
			kind:    ashlar.ErrValidation,
			pointer: "/data",
		},
		{
			desc:    "real type error located",
			json:    `{"items":[{"n":true}]}`,
			newDst:  func() any { return new(nestedInt) },
			kind:    ashlar.ErrValidation,
			pointer: "/items/0/n",
		},
		{
			desc:    "unknown beside quoted",
			json:    `{"port":"8080","bogus":1}`,
			newDst:  func() any { return new(quotedInt) },
			kind:    ashlar.ErrUnknownField,
			pointer: "/bogus",
		},
	}

	for _, row := range accept {
		row := row
		t.Run(row.desc, func(t *testing.T) {
			t.Parallel()
			dstJSON := row.newDst()
			if err := decodeStd(row.json, dstJSON); err != nil {
				t.Fatalf("encoding/json decode: unexpected error: %v", err)
			}
			doc, err := jsoncontract.Read("contract.json", []byte(row.json))
			if err != nil {
				t.Fatalf("Read() unexpected error: %v", err)
			}
			dstAshlar := row.newDst()
			if err := doc.DecodeStrict(dstAshlar); err != nil {
				t.Fatalf("DecodeStrict() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(dstJSON, dstAshlar) {
				t.Errorf("destinations differ:\nencoding/json: %#v\nashlar:        %#v", dstJSON, dstAshlar)
			}
		})
	}

	for _, row := range reject {
		row := row
		t.Run(row.desc, func(t *testing.T) {
			t.Parallel()
			if err := decodeStd(row.json, row.newDst()); err == nil {
				t.Fatal("encoding/json decode: expected an error, got nil")
			}
			doc, err := jsoncontract.Read("contract.json", []byte(row.json))
			if err != nil {
				t.Fatalf("Read() unexpected error: %v", err)
			}
			err = doc.DecodeStrict(row.newDst())
			if !errors.Is(err, row.kind) {
				t.Fatalf("DecodeStrict() error = %v, want %v", err, row.kind)
			}
			if row.pointer != "" {
				var aErr *ashlar.Error
				if !errors.As(err, &aErr) {
					t.Fatalf("expected *ashlar.Error, got %T", err)
				}
				if got := aErr.Pointer.String(); got != row.pointer {
					t.Errorf("aErr.Pointer = %q, want %q", got, row.pointer)
				}
			}
		})
	}

	// Ashlar-only row: encoding/json accepts a case-mismatched key
	// case-insensitively, while DecodeStrict rejects it as ErrUnknownField.
	t.Run("case mismatch (Ashlar only)", func(t *testing.T) {
		t.Parallel()
		json := `{"PORT":1}`
		if err := decodeStd(json, new(plainPort)); err != nil {
			t.Fatalf("encoding/json decode: unexpected error: %v", err)
		}
		doc, err := jsoncontract.Read("contract.json", []byte(json))
		if err != nil {
			t.Fatalf("Read() unexpected error: %v", err)
		}
		err = doc.DecodeStrict(new(plainPort))
		if !errors.Is(err, ashlar.ErrUnknownField) {
			t.Fatalf("DecodeStrict() error = %v, want ErrUnknownField", err)
		}
		var aErr *ashlar.Error
		if !errors.As(err, &aErr) {
			t.Fatalf("expected *ashlar.Error, got %T", err)
		}
		if got := aErr.Pointer.String(); got != "/PORT" {
			t.Errorf("aErr.Pointer = %q, want /PORT", got)
		}
	})
}

// decodeStd decodes raw into dst with encoding/json using the same settings
// DecodeStrict applies (DisallowUnknownFields and UseNumber).
func decodeStd(raw string, dst any) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	return dec.Decode(dst)
}

func TestDecodeStrictErrorsWithholdInput(t *testing.T) {
	t.Parallel()

	type customJSONStruct struct {
		Token secretJSON `json:"token"`
	}
	type customTextStruct struct {
		Token secretText `json:"token"`
	}
	type quotedBadInt struct {
		Pin int `json:"pin,string"`
	}
	type overflowInt struct {
		Pin int8 `json:"pin"`
	}
	type intKeys struct {
		Inner struct {
			Ports map[int]string `json:"ports"`
		} `json:"inner"`
	}

	rows := []struct {
		desc   string
		json   string
		newDst func() any
		leak   string
		alsoIs error
	}{
		{
			desc:   "custom UnmarshalJSON",
			json:   `{"token":"tok-sekrit-1"}`,
			newDst: func() any { return new(customJSONStruct) },
			leak:   "tok-sekrit-1",
			alsoIs: errUnmarshalSentinel,
		},
		{
			desc:   "custom UnmarshalText",
			json:   `{"token":"tok-sekrit-2"}`,
			newDst: func() any { return new(customTextStruct) },
			leak:   "tok-sekrit-2",
			alsoIs: errUnmarshalSentinel,
		},
		{
			desc:   "quoted bad content",
			json:   `{"pin":"pin-sekrit-3"}`,
			newDst: func() any { return new(quotedBadInt) },
			leak:   "pin-sekrit-3",
		},
		{
			desc:   "non-integer map key",
			json:   `{"inner":{"ports":{"key-sekrit-4":"x"}}}`,
			newDst: func() any { return new(intKeys) },
			leak:   "key-sekrit-4",
		},
		{
			desc:   "number overflow",
			json:   `{"pin":123456789012}`,
			newDst: func() any { return new(overflowInt) },
			leak:   "123456789012",
		},
	}

	for _, row := range rows {
		row := row
		t.Run(row.desc, func(t *testing.T) {
			t.Parallel()
			doc, err := jsoncontract.Read("contract.json", []byte(row.json))
			if err != nil {
				t.Fatalf("Read() unexpected error: %v", err)
			}
			err = doc.DecodeStrict(row.newDst())
			if !errors.Is(err, ashlar.ErrValidation) {
				t.Fatalf("DecodeStrict() error = %v, want ErrValidation", err)
			}
			if row.alsoIs != nil && !errors.Is(err, row.alsoIs) {
				t.Errorf("errors.Is(err, sentinel) = false, want true: %v", err)
			}
			for _, format := range []string{"%v", "%+v", "%q"} {
				if got := fmt.Sprintf(format, err); strings.Contains(got, row.leak) {
					t.Errorf("format %q leaks %q: %s", format, row.leak, got)
				}
			}
		})
	}
}

// TestDecodeStrictLocatesErrors checks the reported file:line:column for the
// two error paths that do not come from the value-type pre-check: a bad
// ,string literal and an encoding/json UnmarshalTypeError (a map key that is
// not an integer).
func TestDecodeStrictLocatesErrors(t *testing.T) {
	t.Parallel()

	type quoted struct {
		Port int `json:"port,string"`
	}
	type intKeys struct {
		Inner struct {
			Ports map[int]string `json:"ports"`
		} `json:"inner"`
	}

	rows := []struct {
		desc     string
		json     string
		newDst   func() any
		pointer  string
		location string
	}{
		{
			desc:     "bad quoted literal",
			json:     "{\n  \"port\": \"abc\"\n}",
			newDst:   func() any { return new(quoted) },
			pointer:  "/port",
			location: "contract.json:2:11",
		},
		{
			desc:     "decoder type error",
			json:     "{\n  \"inner\": {\n    \"ports\": {\"abc\": \"x\"}\n  }\n}",
			newDst:   func() any { return new(intKeys) },
			pointer:  "/inner/ports",
			location: "contract.json:3:14",
		},
	}

	for _, row := range rows {
		row := row
		t.Run(row.desc, func(t *testing.T) {
			t.Parallel()
			doc, err := jsoncontract.Read("contract.json", []byte(row.json))
			if err != nil {
				t.Fatalf("Read() unexpected error: %v", err)
			}
			err = doc.DecodeStrict(row.newDst())
			var aErr *ashlar.Error
			if !errors.As(err, &aErr) {
				t.Fatalf("DecodeStrict() error = %v, want *ashlar.Error", err)
			}
			if !errors.Is(err, ashlar.ErrValidation) {
				t.Errorf("DecodeStrict() error = %v, want ErrValidation", err)
			}
			if got := aErr.Pointer.String(); got != row.pointer {
				t.Errorf("Pointer = %q, want %q", got, row.pointer)
			}
			if got := aErr.Origin.Location(); got != row.location {
				t.Errorf("Origin.Location() = %q, want %q", got, row.location)
			}
		})
	}
}
