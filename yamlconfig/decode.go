package yamlconfig

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/internal/numfit"
)

var (
	durationType        = reflect.TypeFor[time.Duration]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	jsonNumberType      = reflect.TypeFor[json.Number]()
)

// decoder carries the redactor that classifies sensitive nodes for decode
// error messages. A node is sensitive when its frozen provenance flag is set or
// the redactor classifies its pointer; a nil redactor means DefaultRedactor.
type decoder struct {
	redactor ashlar.Redactor
}

// DecodeValueStrict decodes an immutable ashlar.Value tree into dst (which must
// be a non-nil pointer), honoring "yaml" (and fallback "json") struct tags,
// parsing time.Duration strings, and rejecting unknown struct fields with
// source-located ashlar.ErrUnknownField diagnostics. Custom unmarshalers are
// dispatched as tryCustomUnmarshaler describes. Error messages name the target
// type and wrap the parser's own error, as Go libraries do. Decode errors
// redact a node's value when its provenance is marked sensitive or
// DefaultRedactor classifies its pointer, so values that were never resolved
// under a policy are still protected.
//
// Unknown keys are rejected before dst is written, so that failure leaves dst
// untouched. Any other failure, such as a value of the wrong type or a custom
// unmarshaler error, is found while decoding and can leave dst partly
// written. When all-or-nothing matters, decode into a fresh value and assign
// it on success:
//
//	var next Config
//	if err := yamlconfig.DecodeValueStrict(root, &next); err != nil {
//		return err
//	}
//	current = next
func DecodeValueStrict(val ashlar.Value, dst any) error {
	return decoder{}.decodeRoot(val, dst)
}

func (dc decoder) decodeRoot(val ashlar.Value, dst any) error {
	rv := reflect.ValueOf(dst)
	if !rv.IsValid() || rv.Kind() != reflect.Pointer || rv.IsNil() {
		return ashlar.NewError(
			ashlar.ErrValidation,
			ashlar.RootPointer,
			val.Provenance().Effective,
			"DecodeValueStrict destination must be a non-nil pointer",
			nil,
		)
	}
	if val.IsOmitted() {
		return ashlar.NewError(
			ashlar.ErrSyntax,
			ashlar.RootPointer,
			ashlar.Origin{},
			"cannot decode omitted value",
			nil,
		)
	}
	// Reject unknown keys before writing anything, so the failure strict
	// decoding exists for never leaves dst half-filled.
	if err := checkKnownKeys(val, ashlar.RootPointer, rv.Elem().Type()); err != nil {
		return err
	}
	return dc.decodeInto(val, ashlar.RootPointer, rv.Elem())
}

// tryCustomUnmarshaler handles time.Duration and types with their own
// unmarshalers. Failures follow Go convention: Ashlar's message names the
// target type, and the parser's own error is wrapped as the cause, so its text
// (which may quote the value) appears after it. A node the redactor classifies
// as sensitive gets a redacted message and no cause.
//
// Dispatch: null never reaches here (decodeInto zeroes it). A number goes to
// json.Unmarshaler when the type has one, because the type then accepts JSON
// numbers. Every other kind goes to encoding.TextUnmarshaler first, then
// json.Unmarshaler. A type with only a TextUnmarshaler rejects numbers, bools
// and containers. The chosen method's error is final; there is no fallback.
func (dc decoder) tryCustomUnmarshaler(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) (bool, error) {
	if dst.Type() == durationType {
		s, ok := val.StringVal()
		if !ok {
			return true, dc.validationErr(val, ptr, fmt.Sprintf("expected string for %s, got %s", dst.Type(), val.Kind()), nil)
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			return true, dc.parseErr(val, ptr, dst.Type(), dc.isSensitiveNode(val, ptr), err)
		}
		dst.SetInt(int64(d))
		return true, nil
	}
	if !dst.CanAddr() {
		return false, nil
	}
	addr := dst.Addr()
	isText := addr.Type().Implements(textUnmarshalerType)
	isJSON := addr.Type().Implements(jsonUnmarshalerType)
	switch {
	case isJSON && (val.Kind() == ashlar.KindNumber || !isText):
		return true, dc.callJSONUnmarshaler(val, ptr, dst.Type(), addr.Interface().(json.Unmarshaler))
	case isText:
		s, ok := val.StringVal()
		if !ok {
			return true, dc.validationErr(val, ptr, fmt.Sprintf("expected string for %s, got %s", dst.Type(), val.Kind()), nil)
		}
		if err := addr.Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s)); err != nil {
			return true, dc.parseErr(val, ptr, dst.Type(), dc.isSensitiveNode(val, ptr), err)
		}
		return true, nil
	}
	return false, nil
}

// callJSONUnmarshaler hands val to u as JSON. Numbers go in as json.Number, so
// u receives the stored JSON number lexeme rather than a re-marshalled float64.
// The stored lexeme is the normalized JSON spelling, so YAML 0x1F arrives as
// 31.
func (dc decoder) callJSONUnmarshaler(val ashlar.Value, ptr ashlar.Pointer, t reflect.Type, u json.Unmarshaler) error {
	sensitive := dc.subtreeSensitive(val, ptr)
	raw, err := json.Marshal(valueToGoAny(val, true))
	if err != nil {
		if sensitive {
			return dc.validationErr(val, ptr, fmt.Sprintf("cannot prepare value for %s: %s", t, ashlar.RedactedLiteral), nil)
		}
		return dc.validationErr(val, ptr, fmt.Sprintf("cannot prepare value for %s", t), err)
	}
	if err := u.UnmarshalJSON(raw); err != nil {
		return dc.parseErr(val, ptr, t, sensitive, err)
	}
	return nil
}

// parseErr reports that a parser rejected val. For a sensitive node the
// message is redacted and the cause dropped, since the cause can quote the
// value.
func (dc decoder) parseErr(val ashlar.Value, ptr ashlar.Pointer, t reflect.Type, sensitive bool, cause error) error {
	if sensitive {
		return dc.validationErr(val, ptr, fmt.Sprintf("cannot parse value as %s: %s", t, ashlar.RedactedLiteral), nil)
	}
	return dc.validationErr(val, ptr, fmt.Sprintf("cannot parse value as %s", t), cause)
}

func (dc decoder) decodeStruct(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) error {
	if val.Kind() != ashlar.KindObject {
		return dc.validationErr(val, ptr, fmt.Sprintf("expected mapping for %s, got %s", dst.Type(), val.Kind()), nil)
	}
	fields := indexStructFields(dst.Type())
	for _, m := range val.Members() {
		idxPath, ok := fields[m.Key]
		if !ok {
			return unknownFieldErr(ptr, m)
		}
		targetField, ok := fieldByIndexPath(dst, idxPath)
		if !ok {
			return dc.validationErr(
				m.Value,
				ptr.Child(m.Key),
				fmt.Sprintf("cannot set field %q through a nil pointer to an unexported embedded struct in %s", m.Key, dst.Type()),
				nil,
			)
		}
		if !targetField.CanSet() {
			return dc.validationErr(
				m.Value,
				ptr.Child(m.Key),
				fmt.Sprintf("cannot set field %q in %s: it is unexported", m.Key, dst.Type()),
				nil,
			)
		}
		if err := dc.decodeInto(m.Value, ptr.Child(m.Key), targetField); err != nil {
			return err
		}
	}
	return nil
}

func (dc decoder) decodeMap(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) error {
	if val.Kind() != ashlar.KindObject {
		return dc.validationErr(val, ptr, fmt.Sprintf("expected mapping for %s, got %s", dst.Type(), val.Kind()), nil)
	}
	if dst.Type().Key().Kind() != reflect.String {
		return dc.validationErr(val, ptr, "map key type must be string", nil)
	}
	out := reflect.MakeMapWithSize(dst.Type(), val.Len())
	elemType := dst.Type().Elem()
	for _, m := range val.Members() {
		elemVal := reflect.New(elemType).Elem()
		if err := dc.decodeInto(m.Value, ptr.Child(m.Key), elemVal); err != nil {
			return err
		}
		out.SetMapIndex(reflect.ValueOf(m.Key).Convert(dst.Type().Key()), elemVal)
	}
	dst.Set(out)
	return nil
}

func (dc decoder) decodeSlice(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) error {
	if val.Kind() != ashlar.KindArray {
		return dc.validationErr(val, ptr, fmt.Sprintf("expected sequence for %s, got %s", dst.Type(), val.Kind()), nil)
	}
	elems := val.Elements()
	out := reflect.MakeSlice(dst.Type(), len(elems), len(elems))
	for i, el := range elems {
		if err := dc.decodeInto(el, ptr.Index(i), out.Index(i)); err != nil {
			return err
		}
	}
	dst.Set(out)
	return nil
}

func (dc decoder) decodeScalar(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) error {
	if dst.Type() == jsonNumberType {
		if lex, ok := val.NumberLexeme(); ok {
			dst.SetString(string(lex))
			return nil
		}
	}
	switch dst.Kind() {
	case reflect.String:
		s, ok := val.StringVal()
		if !ok {
			return dc.validationErr(val, ptr, fmt.Sprintf("expected string, got %s", val.Kind()), nil)
		}
		dst.SetString(s)
		return nil
	case reflect.Bool:
		b, ok := val.Bool()
		if !ok {
			return dc.validationErr(val, ptr, fmt.Sprintf("expected boolean, got %s", val.Kind()), nil)
		}
		dst.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return dc.decodeNumber(val, ptr, dst)
	default:
		return dc.validationErr(val, ptr, fmt.Sprintf("unsupported scalar type %s", dst.Type()), nil)
	}
}

// decodeNumber stores a number in an integer, unsigned or float target. A
// number that does not fit gets one of numfit's fixed messages, which say
// whether it needs integer notation, has a disallowed sign, or is out of range.
func (dc decoder) decodeNumber(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) error {
	lex, ok := val.NumberLexeme()
	if !ok {
		return dc.validationErr(val, ptr, fmt.Sprintf("expected %s, got %s", dst.Type(), val.Kind()), nil)
	}
	if msg := numfit.Problem(string(lex), dst.Type()); msg != "" {
		return dc.validationErr(val, ptr, msg, nil)
	}
	// numfit has confirmed the number parses and fits, so these cannot fail.
	switch dst.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := val.Int64()
		if err != nil {
			return dc.validationErr(val, ptr, fmt.Sprintf("expected %s, got number", dst.Type()), nil)
		}
		dst.SetInt(i)
	case reflect.Float32, reflect.Float64:
		f, err := val.Float64()
		if err != nil {
			return dc.validationErr(val, ptr, fmt.Sprintf("expected %s, got number", dst.Type()), nil)
		}
		dst.SetFloat(f)
	default:
		u, err := val.Uint64()
		if err != nil {
			return dc.validationErr(val, ptr, fmt.Sprintf("expected %s, got number", dst.Type()), nil)
		}
		dst.SetUint(u)
	}
	return nil
}

func (dc decoder) isSensitiveNode(val ashlar.Value, ptr ashlar.Pointer) bool {
	return val.Provenance().Sensitive || ashlar.IsSensitivePath(dc.redactor, ptr, ptr.LastToken())
}

// subtreeSensitive reports whether val or any node beneath it is sensitive. A
// custom unmarshaler that rejects a container can echo every value in it, so
// one sensitive descendant is enough to withhold the whole message.
func (dc decoder) subtreeSensitive(val ashlar.Value, ptr ashlar.Pointer) bool {
	if dc.isSensitiveNode(val, ptr) {
		return true
	}
	for i, el := range val.Elements() {
		if dc.subtreeSensitive(el, ptr.Index(i)) {
			return true
		}
	}
	for _, m := range val.Members() {
		if dc.subtreeSensitive(m.Value, ptr.Child(m.Key)) {
			return true
		}
	}
	return false
}

func (dc decoder) validationErr(val ashlar.Value, ptr ashlar.Pointer, msg string, cause error) error {
	rawScalar := ""
	if s, ok := val.StringVal(); ok {
		rawScalar = s
	} else if n, ok := val.NumberLexeme(); ok {
		rawScalar = string(n)
	}
	return ashlar.NewRedactedError(
		ashlar.ErrValidation,
		ptr,
		val.Provenance().Effective,
		msg,
		cause,
		dc.redactor,
		rawScalar,
		val.Provenance().Sensitive,
	)
}
