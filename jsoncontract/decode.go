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

// DecodeStrict decodes d into dst (which must be a non-nil pointer), rejecting
// unknown struct fields with source-located ashlar.ErrUnknownField diagnostics
// and reporting type mismatches as source-located ashlar.ErrValidation errors.
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

	if err := checkKnownFields(d.root, ashlar.RootPointer, rv.Type().Elem()); err != nil {
		return err
	}

	dec := json.NewDecoder(bytes.NewReader(d.raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return d.classifyDecodeError(err)
	}
	return nil
}

func (d Document) classifyDecodeError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		ptr := pointerFromDotField(typeErr.Field)
		orig := d.originAt(ptr)
		return ashlar.NewError(
			ashlar.ErrValidation,
			ptr,
			orig,
			fmt.Sprintf("cannot unmarshal JSON %s into Go type %s", typeErr.Value, typeErr.Type),
			err,
		)
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "json: unknown field ") {
		field := strings.TrimPrefix(msg, "json: unknown field ")
		field = strings.Trim(field, `"`)
		ptr := ashlar.NewPointer(field)
		return ashlar.NewError(
			ashlar.ErrUnknownField,
			ptr,
			d.originAt(ptr),
			fmt.Sprintf("unknown field %q", field),
			err,
		)
	}
	return ashlar.NewError(
		ashlar.ErrValidation,
		ashlar.RootPointer,
		d.root.Provenance().Effective,
		msg,
		err,
	)
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

func checkKnownFields(val ashlar.Value, ptr ashlar.Pointer, t reflect.Type) error {
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
		return checkStructFields(val, ptr, t)
	case reflect.Slice, reflect.Array:
		if val.Kind() != ashlar.KindArray {
			return typeMismatchError(val, ptr, t)
		}
		elemType := t.Elem()
		for i, elem := range val.Elements() {
			if err := checkKnownFields(elem, ptr.Index(i), elemType); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if val.Kind() != ashlar.KindObject {
			return typeMismatchError(val, ptr, t)
		}
		elemType := t.Elem()
		for _, m := range val.Members() {
			if err := checkKnownFields(m.Value, ptr.Child(m.Key), elemType); err != nil {
				return err
			}
		}
		return nil
	default:
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

func checkStructFields(val ashlar.Value, ptr ashlar.Pointer, t reflect.Type) error {
	if val.Kind() != ashlar.KindObject {
		return typeMismatchError(val, ptr, t)
	}
	fields := collectStructJSONFields(t)
	for _, m := range val.Members() {
		fieldType, ok := fields[m.Key]
		if !ok {
			return ashlar.NewError(
				ashlar.ErrUnknownField,
				ptr.Child(m.Key),
				m.KeyOrigin,
				fmt.Sprintf("unknown field %q", m.Key),
				nil,
			)
		}
		if err := checkKnownFields(m.Value, ptr.Child(m.Key), fieldType); err != nil {
			return err
		}
	}
	return nil
}

func collectStructJSONFields(t reflect.Type) map[string]reflect.Type {
	out := make(map[string]reflect.Type)
	collectStructJSONFieldsInto(t, out)
	return out
}

func collectStructJSONFieldsInto(t reflect.Type, out map[string]reflect.Type) {
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
		name, _, _ := strings.Cut(tag, ",")
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
			out[name] = sf.Type
		}
	}
}

func implementsCustomUnmarshaler(t reflect.Type) bool {
	if t.Implements(jsonUnmarshalerType) || t.Implements(textUnmarshalerType) {
		return true
	}
	ptrType := reflect.PointerTo(t)
	return ptrType.Implements(jsonUnmarshalerType) || ptrType.Implements(textUnmarshalerType)
}
