package jsoncontract_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
)

// stickyCalls counts stickyInt.UnmarshalJSON invocations. Only
// TestDecodeStrictQuotedCustomUnmarshaler uses stickyInt, so the count is
// not shared with other tests.
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
