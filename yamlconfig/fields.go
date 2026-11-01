package yamlconfig

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/mattjoyce/ashlar"
)

// checkKnownKeys walks val alongside type t and returns the first unknown
// struct key as the same ErrUnknownField decodeStruct would report. It reads
// only key names and uses the decoder's own field index, so the two cannot
// disagree about which keys a struct accepts. It stops at types with custom
// unmarshalers, which own their shape, and at interfaces. A shape mismatch
// is left for decodeInto to report.
func checkKnownKeys(val ashlar.Value, ptr ashlar.Pointer, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == durationType || reflect.PointerTo(t).Implements(textUnmarshalerType) ||
		reflect.PointerTo(t).Implements(jsonUnmarshalerType) {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		if val.Kind() != ashlar.KindObject {
			return nil
		}
		fields := indexStructFields(t)
		for _, m := range val.Members() {
			idxPath, ok := fields[m.Key]
			if !ok {
				return unknownFieldErr(ptr, m)
			}
			if err := checkKnownKeys(m.Value, ptr.Child(m.Key), t.FieldByIndex(idxPath).Type); err != nil {
				return err
			}
		}
	case reflect.Map:
		for _, m := range val.Members() {
			if err := checkKnownKeys(m.Value, ptr.Child(m.Key), t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i, el := range val.Elements() {
			if err := checkKnownKeys(el, ptr.Index(i), t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

// unknownFieldErr reports key m as unknown in the struct mapping at ptr.
func unknownFieldErr(ptr ashlar.Pointer, m ashlar.Member) error {
	return ashlar.NewError(
		ashlar.ErrUnknownField,
		ptr.Child(m.Key),
		m.KeyOrigin,
		fmt.Sprintf("unknown field %q", m.Key),
		nil,
	)
}

func (dc decoder) decodeInto(val ashlar.Value, ptr ashlar.Pointer, dst reflect.Value) error {
	if val.IsNull() {
		dst.Set(reflect.Zero(dst.Type()))
		return nil
	}

	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return dc.decodeInto(val, ptr, dst.Elem())
	}

	if ok, err := dc.tryCustomUnmarshaler(val, ptr, dst); ok {
		return err
	}

	switch dst.Kind() {
	case reflect.Struct:
		return dc.decodeStruct(val, ptr, dst)
	case reflect.Map:
		return dc.decodeMap(val, ptr, dst)
	case reflect.Slice:
		return dc.decodeSlice(val, ptr, dst)
	case reflect.Interface:
		if dst.NumMethod() == 0 {
			dst.Set(reflect.ValueOf(valueToGoAny(val, false)))
			return nil
		}
	default:
		return dc.decodeScalar(val, ptr, dst)
	}
	return dc.validationErr(val, ptr, fmt.Sprintf("unsupported destination type %s", dst.Type()), nil)
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

// fieldByIndexPath walks path from v, allocating nil embedded struct pointers
// on the way. It reports false instead of panicking when a nil pointer cannot
// be set, which happens for a pointer to an unexported embedded struct.
func fieldByIndexPath(v reflect.Value, path []int) (reflect.Value, bool) {
	cur := v
	for _, idx := range path {
		if cur.Kind() == reflect.Pointer {
			if cur.IsNil() {
				if !cur.CanSet() {
					return reflect.Value{}, false
				}
				cur.Set(reflect.New(cur.Type().Elem()))
			}
			cur = cur.Elem()
		}
		cur = cur.Field(idx)
	}
	return cur, true
}
