package jsoncontract_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
)

// stickyCalls counts stickyInt.UnmarshalJSON invocations. The tests that use
// stickyInt do not run in parallel, so their counts don't interleave.
var stickyCalls atomic.Int64

// errStickyUninitialized is returned when stickyInt is decoded into a
// zero-valued receiver.
var errStickyUninitialized = errors.New("sticky int must be initialized before decoding")

// stickyInt is an integer whose unmarshaler requires an initialized
// destination, the way some caller types require defaults to be set first.
type stickyInt int

func (s *stickyInt) UnmarshalJSON([]byte) error {
	stickyCalls.Add(1)
	if *s == 0 {
		return errStickyUninitialized
	}
	*s++
	return nil
}

// TestDecodeStrictQuotedCustomUnmarshaler checks that a ,string field whose
// type has its own UnmarshalJSON is decoded once, into the caller's existing
// value, exactly as encoding/json does. The quoted-content pre-check must not
// call the unmarshaler on a zero-valued copy and blame the field.
func TestDecodeStrictQuotedCustomUnmarshaler(t *testing.T) {
	type config struct {
		N stickyInt `json:"n,string"`
	}

	doc, err := jsoncontract.Read("contract.json", []byte(`{"n":"7"}`))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	dst := config{N: 5}
	before := stickyCalls.Load()
	if err := doc.DecodeStrict(&dst); err != nil {
		t.Fatalf("DecodeStrict() error = %v", err)
	}
	if got := stickyCalls.Load() - before; got != 1 {
		t.Errorf("UnmarshalJSON called %d times, want 1", got)
	}
	if dst.N != 6 {
		t.Errorf("N = %d, want 6", dst.N)
	}

	var zero config
	err = doc.DecodeStrict(&zero)
	if !errors.Is(err, errStickyUninitialized) || !errors.Is(err, ashlar.ErrValidation) {
		t.Errorf("DecodeStrict(zero) error = %v, want the unmarshaler's error as ErrValidation", err)
	}
}

// forgedTypeError is a type whose UnmarshalJSON returns a
// *json.UnmarshalTypeError built from its input, so Value and Field carry
// document content that DecodeStrict must not print.
type forgedTypeError string

func (f *forgedTypeError) UnmarshalJSON(raw []byte) error {
	return &json.UnmarshalTypeError{Value: string(raw), Field: string(raw), Type: reflect.TypeFor[int]()}
}

// TestDecodeStrictForgedTypeErrorWithheld checks that a type error built by a
// custom unmarshaler cannot place its input in the message or the pointer.
func TestDecodeStrictForgedTypeErrorWithheld(t *testing.T) {
	t.Parallel()

	const secret = "sekrit-123"
	doc, err := jsoncontract.Read("contract.json", []byte(`{"token":"`+secret+`"}`))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	var dst struct {
		Token forgedTypeError `json:"token"`
	}
	err = doc.DecodeStrict(&dst)
	if !errors.Is(err, ashlar.ErrValidation) {
		t.Fatalf("DecodeStrict() error = %v, want ErrValidation", err)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%q"} {
		if got := fmt.Sprintf(format, err); strings.Contains(got, secret) {
			t.Errorf("format %q shows %q: %s", format, secret, got)
		}
	}
}

// TestDecodeStrictDecodesOnceIntoDestination checks that DecodeStrict runs a
// custom unmarshaler once, on the caller's initialized value, even when the
// document also holds array elements that encoding/json ignores. This input
// succeeds with encoding/json, so it must succeed here.
func TestDecodeStrictDecodesOnceIntoDestination(t *testing.T) {
	doc, err := jsoncontract.Read("contract.json", []byte(`{"v":"7","a":[1,"ignored"]}`))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	dst := struct {
		V stickyInt `json:"v,string"`
		A [1]int    `json:"a"`
	}{V: 5}
	before := stickyCalls.Load()
	if err := doc.DecodeStrict(&dst); err != nil {
		t.Fatalf("DecodeStrict() error = %v", err)
	}
	if got := stickyCalls.Load() - before; got != 1 {
		t.Errorf("UnmarshalJSON called %d times, want 1", got)
	}
	if dst.V != 6 || dst.A != [1]int{1} {
		t.Errorf("dst = %+v, want {V:6 A:[1]}", dst)
	}
}
