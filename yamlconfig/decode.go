package yamlconfig

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/mattjoyce/ashlar"
)

var (
	durationType        = reflect.TypeFor[time.Duration]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
)

// DecodeValueStrict decodes an immutable ashlar.Value tree into dst (which must
// be a non-nil pointer), honoring "yaml" (and fallback "json") struct tags,
// parsing time.Duration strings, and rejecting unknown struct fields with
// source-located ashlar.ErrUnknownField diagnostics.
func DecodeValueStrict(val ashlar.Value, dst any) error {
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
	return decodeInto(val, ashlar.RootPointer, rv.Elem())
}

func decodeInto(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) error {
	if val.IsNull() {
		dst.Set(reflect.Zero(dst.Type()))
		return nil
	}

	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return decodeInto(val, ptr, dst.Elem())
	}

	if ok, err := tryCustomUnmarshaler(val, ptr, dst); ok {
		return err
	}

	switch dst.Kind() {
	case reflect.Struct:
		return decodeStruct(val, ptr, dst)
	case reflect.Map:
		return decodeMap(val, ptr, dst)
	case reflect.Slice:
		return decodeSlice(val, ptr, dst)
	case reflect.Interface:
		if dst.NumMethod() == 0 {
			dst.Set(reflect.ValueOf(valueToGoAny(val)))
			return nil
		}
	default:
		return decodeScalar(val, ptr, dst)
	}
	return validationErr(val, ptr, fmt.Sprintf("unsupported destination type %s", dst.Type()), nil)
}

func tryCustomUnmarshaler(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) (bool, error) {
	if dst.Type() == durationType {
		s, ok := val.StringVal()
		if !ok {
			return true, validationErr(val, ptr, "expected duration string", nil)
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			if isSensitiveNode(val, ptr) {
				return true, validationErr(val, ptr, fmt.Sprintf("invalid duration value %s", ashlar.RedactedLiteral), nil)
			}
			return true, validationErr(val, ptr, fmt.Sprintf("invalid duration %q", s), nil)
		}
		dst.SetInt(int64(d))
		return true, nil
	}
	if dst.CanAddr() {
		addr := dst.Addr()
		if addr.Type().Implements(textUnmarshalerType) {
			s, ok := val.StringVal()
			if !ok {
				return true, validationErr(val, ptr, fmt.Sprintf("expected string for %s", dst.Type()), nil)
			}
			u := addr.Interface().(encoding.TextUnmarshaler)
			if err := u.UnmarshalText([]byte(s)); err != nil {
				if isSensitiveNode(val, ptr) {
					return true, validationErr(val, ptr, fmt.Sprintf("invalid %s value %s", dst.Type(), ashlar.RedactedLiteral), nil)
				}
				return true, validationErr(val, ptr, err.Error(), err)
			}
			return true, nil
		}
		if addr.Type().Implements(jsonUnmarshalerType) {
			raw, err := json.Marshal(valueToGoAny(val))
			if err != nil {
				if isSensitiveNode(val, ptr) {
					return true, validationErr(val, ptr, fmt.Sprintf("invalid %s value %s", dst.Type(), ashlar.RedactedLiteral), nil)
				}
				return true, validationErr(val, ptr, err.Error(), err)
			}
			u := addr.Interface().(json.Unmarshaler)
			if err := u.UnmarshalJSON(raw); err != nil {
				if isSensitiveNode(val, ptr) {
					return true, validationErr(val, ptr, fmt.Sprintf("invalid %s value %s", dst.Type(), ashlar.RedactedLiteral), nil)
				}
				return true, validationErr(val, ptr, err.Error(), err)
			}
			return true, nil
		}
	}
	return false, nil
}

func decodeStruct(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) error {
	if val.Kind() != ashlar.KindObject {
		return validationErr(val, ptr, fmt.Sprintf("expected mapping for %s, got %s", dst.Type(), val.Kind()), nil)
	}
	fields := indexStructFields(dst.Type())
	for _, m := range val.Members() {
		idxPath, ok := fields[m.Key]
		if !ok {
			return ashlar.NewError(
				ashlar.ErrUnknownField,
				ptr.Child(m.Key),
				m.KeyOrigin,
				fmt.Sprintf("unknown field %q", m.Key),
				nil,
			)
		}
		targetField := fieldByIndexPath(dst, idxPath)
		if err := decodeInto(m.Value, ptr.Child(m.Key), targetField); err != nil {
			return err
		}
	}
	return nil
}

func decodeMap(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) error {
	if val.Kind() != ashlar.KindObject {
		return validationErr(val, ptr, fmt.Sprintf("expected mapping for %s, got %s", dst.Type(), val.Kind()), nil)
	}
	if dst.Type().Key().Kind() != reflect.String {
		return validationErr(val, ptr, "map key type must be string", nil)
	}
	out := reflect.MakeMapWithSize(dst.Type(), val.Len())
	elemType := dst.Type().Elem()
	for _, m := range val.Members() {
		elemVal := reflect.New(elemType).Elem()
		if err := decodeInto(m.Value, ptr.Child(m.Key), elemVal); err != nil {
			return err
		}
		out.SetMapIndex(reflect.ValueOf(m.Key).Convert(dst.Type().Key()), elemVal)
	}
	dst.Set(out)
	return nil
}

func decodeSlice(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) error {
	if val.Kind() != ashlar.KindArray {
		return validationErr(val, ptr, fmt.Sprintf("expected sequence for %s, got %s", dst.Type(), val.Kind()), nil)
	}
	elems := val.Elements()
	out := reflect.MakeSlice(dst.Type(), len(elems), len(elems))
	for i, el := range elems {
		if err := decodeInto(el, ptr.Index(i), out.Index(i)); err != nil {
			return err
		}
	}
	dst.Set(out)
	return nil
}

func decodeScalar(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) error {
	switch dst.Kind() {
	case reflect.String:
		s, ok := val.StringVal()
		if !ok {
			return validationErr(val, ptr, fmt.Sprintf("expected string, got %s", val.Kind()), nil)
		}
		dst.SetString(s)
		return nil
	case reflect.Bool:
		b, ok := val.Bool()
		if !ok {
			return validationErr(val, ptr, fmt.Sprintf("expected boolean, got %s", val.Kind()), nil)
		}
		dst.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := val.Int64()
		if err != nil || dst.OverflowInt(i) {
			return validationErr(val, ptr, fmt.Sprintf("expected %s, got %s", dst.Type(), val.Kind()), nil)
		}
		dst.SetInt(i)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := val.Uint64()
		if err != nil || dst.OverflowUint(u) {
			return validationErr(val, ptr, fmt.Sprintf("expected %s, got %s", dst.Type(), val.Kind()), nil)
		}
		dst.SetUint(u)
		return nil
	case reflect.Float32, reflect.Float64:
		f, err := val.Float64()
		if err != nil || dst.OverflowFloat(f) {
			return validationErr(val, ptr, fmt.Sprintf("expected %s, got %s", dst.Type(), val.Kind()), nil)
		}
		dst.SetFloat(f)
		return nil
	default:
		return validationErr(val, ptr, fmt.Sprintf("unsupported scalar type %s", dst.Type()), nil)
	}
}

func indexStructFields(t reflect.Type) map[string][]int {
	out := make(map[string][]int)
	indexStructFieldsInto(t, nil, out)
	return out
}

func indexStructFieldsInto(t reflect.Type, prefix []int, out map[string][]int) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		idxPath := append(append([]int(nil), prefix...), i)
		name, skip, inline := parseFieldTag(sf)
		if skip {
			continue
		}
		if (sf.Anonymous && name == "") || inline {
			ft := sf.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				indexStructFieldsInto(ft, idxPath, out)
				continue
			}
		}
		if !sf.IsExported() {
			continue
		}
		if name == "" {
			name = strings.ToLower(sf.Name)
		}
		if _, exists := out[name]; !exists {
			out[name] = idxPath
		}
	}
}

func parseFieldTag(sf reflect.StructField) (name string, skip bool, inline bool) {
	tag := sf.Tag.Get("yaml")
	if tag == "" {
		tag = sf.Tag.Get("json")
	}
	if tag == "-" {
		return "", true, false
	}
	name, rest, _ := strings.Cut(tag, ",")
	for _, opt := range strings.Split(rest, ",") {
		if opt == "inline" {
			inline = true
		}
	}
	return name, false, inline
}

func fieldByIndexPath(v reflect.Value, path []int) reflect.Value {
	cur := v
	for _, idx := range path {
		if cur.Kind() == reflect.Pointer {
			if cur.IsNil() {
				cur.Set(reflect.New(cur.Type().Elem()))
			}
			cur = cur.Elem()
		}
		cur = cur.Field(idx)
	}
	return cur
}

func valueToGoAny(val ashlar.Value) any {
	switch val.Kind() {
	case ashlar.KindNull:
		return nil
	case ashlar.KindBool:
		b, _ := val.Bool()
		return b
	case ashlar.KindNumber:
		if i, err := val.Int64(); err == nil {
			return i
		}
		if u, err := val.Uint64(); err == nil {
			return u
		}
		f, _ := val.Float64()
		return f
	case ashlar.KindString:
		s, _ := val.StringVal()
		return s
	case ashlar.KindArray:
		elems := val.Elements()
		out := make([]any, len(elems))
		for i, el := range elems {
			out[i] = valueToGoAny(el)
		}
		return out
	case ashlar.KindObject:
		members := val.Members()
		out := make(map[string]any, len(members))
		for _, m := range members {
			out[m.Key] = valueToGoAny(m.Value)
		}
		return out
	default:
		return nil
	}
}

func isSensitiveNode(val ashlar.Value, ptr ashlar.Pointer) bool {
	return val.Provenance().Sensitive || ashlar.IsSensitivePath(nil, ptr, ptr.LastToken())
}

func validationErr(val ashlar.Value, ptr ashlar.Pointer, msg string, cause error) error {
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
		nil,
		rawScalar,
		val.Provenance().Sensitive,
	)
}
