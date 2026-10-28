package jsoncontract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
)

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

	reject := []rejectRow{
		{
			desc:    "quoted bad content",
			json:    `{"port":"abc"}`,
			newDst:  func() any { return new(quotedInt) },
			kind:    ashlar.ErrValidation,
			pointer: "/port",
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
