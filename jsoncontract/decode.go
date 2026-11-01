package jsoncontract

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/mattjoyce/ashlar"
)

var (
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	jsonNumberType      = reflect.TypeFor[json.Number]()
)

// jsonField records the destination type and the ",string" tag option of one
// struct field, as needed by the DecodeStrict pre-check.
type jsonField struct {
	typ    reflect.Type
	quoted bool
}

// DecodeStrict decodes d into dst (which must be a non-nil pointer) with
// encoding/json semantics (DisallowUnknownFields and UseNumber), plus one
// restriction: object keys must match a field's JSON name exactly, where
// encoding/json also accepts case-insensitive matches. Unknown or mismatched
// keys fail with source-located ashlar.ErrUnknownField diagnostics. Inputs
// encoding/json rejects fail with ashlar.ErrValidation, source-located where
// the failing value can be identified. Value-type pre-checks only locate
// errors: an input encoding/json accepts is never rejected for its type.
func (d Document) DecodeStrict(dst any) error {
	rv := reflect.ValueOf(dst)
	if !rv.IsValid() || rv.Kind() != reflect.Pointer || rv.IsNil() {
		return ashlar.NewError(
			ashlar.ErrValidation,
			ashlar.RootPointer,
			d.root.Provenance().Effective,
			"DecodeStrict destination must be a non-nil pointer",
			nil,
		)
	}
	if !d.root.IsPresent() && !d.root.IsNull() {
		return ashlar.NewError(
			ashlar.ErrSyntax,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: d.source}),
			"cannot decode empty or uninitialized JSON document",
			nil,
		)
	}

	elemType := rv.Type().Elem()

	// First pass: reject unknown fields authoritatively, with no value-type
	// checks.
	if err := checkKnownFields(d.root, ashlar.RootPointer, elemType, false); err != nil {
		return err
	}
	// Decode once, into the caller's destination, so custom unmarshalers see
	// the existing value and run exactly once. encoding/json alone decides
	// success.
	dec := json.NewDecoder(bytes.NewReader(d.raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	err := dec.Decode(dst)
	if err == nil {
		return nil
	}
	// Second pass, only after a real failure: locate a value-type error that
	// encoding/json reported without a usable position.
	if located := checkKnownFields(d.root, ashlar.RootPointer, elemType, true); located != nil {
		return located
	}
	return d.classifyDecodeError(err)
}

// withheldDecodeError wraps an error from encoding/json or a custom
// unmarshaler. Its Error text never includes the wrapped message, which may
// quote input values; errors.Is and errors.As still reach the original error.
type withheldDecodeError struct{ err error }

func (e withheldDecodeError) Error() string { return "decoder error withheld from diagnostics" }

func (e withheldDecodeError) Unwrap() error { return e.err }

func (d Document) classifyDecodeError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		// A custom unmarshaler can build this error itself, so Field and
		// Value are caller-controlled text. The pointer is used only when it
		// names a node in the document, and the kind only when it is a known
		// JSON kind word.
		ptr := pointerFromDotField(typeErr.Field)
		if v := d.root.At(ptr); !v.IsPresent() && !v.IsNull() {
			ptr = ashlar.RootPointer
		}
		orig := d.originAt(ptr)
		return ashlar.NewError(
			ashlar.ErrValidation,
			ptr,
			orig,
			fmt.Sprintf("cannot unmarshal JSON %s into Go type %s", kindFromTypeValue(typeErr.Value), typeErr.Type),
			withheldDecodeError{err},
		)
	}
	// Unknown fields never reach this point: checkKnownFields rejects them
	// with located ErrUnknownField errors before encoding/json runs, so this
	// function does not inspect encoding/json error text.
	return ashlar.NewError(
		ashlar.ErrValidation,
		ashlar.RootPointer,
		d.root.Provenance().Effective,
		"JSON value rejected by decoder",
		withheldDecodeError{err},
	)
}

// kindFromTypeValue returns the JSON kind word that starts a
// *json.UnmarshalTypeError Value ("number 123" gives "number"), or "value"
// when the first word is not a known kind. Value can come from a custom
// unmarshaler and quote input, so nothing outside the fixed list is returned.
func kindFromTypeValue(value string) string {
	kind, _, _ := strings.Cut(value, " ")
	switch kind {
	case "string", "number", "bool", "array", "object", "null":
		return kind
	default:
		return "value"
	}
}

func (d Document) originAt(ptr ashlar.Pointer) ashlar.Origin {
	if v := d.root.At(ptr); v.IsPresent() || v.IsNull() {
		return v.Provenance().Effective
	}
	return d.root.Provenance().Effective
}

func pointerFromDotField(field string) ashlar.Pointer {
	if field == "" {
		return ashlar.RootPointer
	}
	return ashlar.NewPointer(strings.Split(field, ".")...)
}

func checkKnownFields(val ashlar.Value, ptr ashlar.Pointer, t reflect.Type, types bool) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if implementsCustomUnmarshaler(t) || val.IsNull() || val.IsOmitted() {
		return nil
	}

	switch t.Kind() {
	case reflect.Interface:
		return nil
	case reflect.Struct:
		return checkStructFields(val, ptr, t, types)
	case reflect.Slice, reflect.Array:
		if val.Kind() != ashlar.KindArray {
			// A []byte (but not [N]byte) also accepts a JSON string; the
			// standard decoder validates the base64 content itself.
			if types && t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 && val.Kind() == ashlar.KindString {
				return nil
			}
			if !types {
				return nil
			}
			return typeMismatchError(val, ptr, t)
		}
		elemType := t.Elem()
		elems := val.Elements()
		if t.Kind() == reflect.Array && len(elems) > t.Len() {
			// encoding/json ignores elements past a fixed array's length.
			elems = elems[:t.Len()]
		}
		for i, elem := range elems {
			if err := checkKnownFields(elem, ptr.Index(i), elemType, types); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if val.Kind() != ashlar.KindObject {
			if !types {
				return nil
			}
			return typeMismatchError(val, ptr, t)
		}
		elemType := t.Elem()
		for _, m := range val.Members() {
			if err := checkKnownFields(m.Value, ptr.Child(m.Key), elemType, types); err != nil {
				return err
			}
		}
		return nil
	default:
		if !types {
			return nil
		}
		return checkScalarType(val, ptr, t)
	}
}

func checkScalarType(val ashlar.Value, ptr ashlar.Pointer, t reflect.Type) error {
	if t == jsonNumberType {
		if val.Kind() != ashlar.KindNumber && val.Kind() != ashlar.KindString {
			return typeMismatchError(val, ptr, t)
		}
		return nil
	}
	switch t.Kind() {
	case reflect.String:
		if val.Kind() != ashlar.KindString {
			return typeMismatchError(val, ptr, t)
		}
	case reflect.Bool:
		if val.Kind() != ashlar.KindBool {
			return typeMismatchError(val, ptr, t)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := val.Int64()
		if err != nil || reflect.Zero(t).OverflowInt(i) {
			return typeMismatchError(val, ptr, t)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u, err := val.Uint64()
		if err != nil || reflect.Zero(t).OverflowUint(u) {
			return typeMismatchError(val, ptr, t)
		}
	case reflect.Float32, reflect.Float64:
		f, err := val.Float64()
		if err != nil || reflect.Zero(t).OverflowFloat(f) {
			return typeMismatchError(val, ptr, t)
		}
	}
	return nil
}

func typeMismatchError(val ashlar.Value, ptr ashlar.Pointer, t reflect.Type) error {
	return ashlar.NewError(
		ashlar.ErrValidation,
		ptr,
		val.Provenance().Effective,
		fmt.Sprintf("cannot unmarshal JSON %s into Go type %s", val.Kind(), t),
		nil,
	)
}

func checkStructFields(val ashlar.Value, ptr ashlar.Pointer, t reflect.Type, types bool) error {
	if val.Kind() != ashlar.KindObject {
		if !types {
			return nil
		}
		return typeMismatchError(val, ptr, t)
	}
	fields := collectStructJSONFields(t)
	for _, m := range val.Members() {
		field, ok := fields[m.Key]
		if !ok {
			return ashlar.NewError(
				ashlar.ErrUnknownField,
				ptr.Child(m.Key),
				m.KeyOrigin,
				fmt.Sprintf("unknown field %q", m.Key),
				nil,
			)
		}
		// A field tagged with the ,string option accepts JSON null, or a JSON
		// string whose content is itself a JSON literal of the field's type.
		// The content check locates bad literals that encoding/json reports
		// without a field name.
		if field.quoted && types && (m.Value.Kind() == ashlar.KindString || m.Value.IsNull()) {
			if err := checkQuotedContent(m.Value, ptr.Child(m.Key), field.typ); err != nil {
				return err
			}
			continue
		}
		if err := checkKnownFields(m.Value, ptr.Child(m.Key), field.typ, types); err != nil {
			return err
		}
	}
	return nil
}

// checkQuotedContent reports a located ErrValidation when val, the JSON string
// held by a ,string field of type t, is content encoding/json rejects for that
// field. It decodes the string through a one-field struct carrying the same
// type and ",string" tag, so the check uses encoding/json's own rules for
// quoted content, such as accepting "01" for an int. A null val passes. The
// message names only the Go type, never the content, which may be secret.
func checkQuotedContent(val ashlar.Value, ptr ashlar.Pointer, t reflect.Type) error {
	s, ok := val.StringVal()
	if !ok {
		return nil
	}
	base := t
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	// A custom unmarshaler decides validity itself, may need the existing
	// destination value, and must run once; leave it to the real decode.
	if implementsCustomUnmarshaler(base) {
		return nil
	}
	quoted, err := json.Marshal(s)
	if err != nil {
		return nil // A Go string always marshals; nothing to locate.
	}
	wrapper := reflect.StructOf([]reflect.StructField{{
		Name: "V",
		Type: t,
		Tag:  `json:"v,string"`,
	}})
	raw := append(append([]byte(`{"v":`), quoted...), '}')
	if json.Unmarshal(raw, reflect.New(wrapper).Interface()) == nil {
		return nil
	}
	return ashlar.NewError(
		ashlar.ErrValidation,
		ptr,
		val.Provenance().Effective,
		fmt.Sprintf("invalid ,string literal for Go type %s", base),
		nil,
	)
}

func collectStructJSONFields(t reflect.Type) map[string]jsonField {
	out := make(map[string]jsonField)
	collectStructJSONFieldsInto(t, out)
	return out
}

func collectStructJSONFieldsInto(t reflect.Type, out map[string]jsonField) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if sf.Anonymous && name == "" {
			ft := sf.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				collectStructJSONFieldsInto(ft, out)
				continue
			}
		}
		if !sf.IsExported() {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		if _, exists := out[name]; !exists {
			out[name] = jsonField{typ: sf.Type, quoted: jsonTagQuoted(sf.Type, opts)}
		}
	}
}

// jsonTagQuoted reports whether opts (the comma-separated json tag options
// after the first comma) includes "string" and t is a type encoding/json
// quotes: a bool, integer, unsigned integer, float, or string, either directly
// or through one pointer level.
func jsonTagQuoted(t reflect.Type, opts string) bool {
	quoted := false
	for _, opt := range strings.Split(opts, ",") {
		if opt == "string" {
			quoted = true
			break
		}
	}
	if !quoted {
		return false
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return true
	}
	return false
}

func implementsCustomUnmarshaler(t reflect.Type) bool {
	if t.Implements(jsonUnmarshalerType) || t.Implements(textUnmarshalerType) {
		return true
	}
	ptrType := reflect.PointerTo(t)
	return ptrType.Implements(jsonUnmarshalerType) || ptrType.Implements(textUnmarshalerType)
}
