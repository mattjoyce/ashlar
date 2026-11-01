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
// unmarshalers, which own their shape, at interfaces, and at the targets the
// decoder rejects outright (arrays, maps with non-string keys). A shape
// mismatch is left for decodeInto to report.
func checkKnownKeys(val ashlar.Value, ptr ashlar.Pointer, t reflect.Type) error {
	if !val.IsPresent() {
		return nil
	}
	// A pointer type can point to itself (type P *P). It has no struct to
	// check, and decodeInto rejects it.
	if isPointerCycle(t) {
		return nil
	}
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
		if t.Key().Kind() != reflect.String {
			return nil
		}
		for _, m := range val.Members() {
			if err := checkKnownKeys(m.Value, ptr.Child(m.Key), t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Slice:
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

// indexStructFields maps each YAML key a struct accepts to the index path of
// its field, promoting the fields of embedded and inline structs.
func indexStructFields(t reflect.Type) map[string][]int {
	out := make(map[string][]int)
	indexStructFieldsInto(t, nil, out, map[reflect.Type]bool{t: true})
	return out
}

// indexStructFieldsInto adds t's keys to out. onPath holds the struct types
// already being expanded on this branch, so a type that embeds itself, such as
// struct{ *T }, is not expanded again and cannot recurse forever.
func indexStructFieldsInto(t reflect.Type, prefix []int, out map[string][]int, onPath map[reflect.Type]bool) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		idxPath := append(append([]int(nil), prefix...), i)
		name, skip, inline := parseFieldTag(sf)
		if skip {
			continue
		}
		// A named unexported field cannot be set through reflection, so an
		// inline tag on one is ignored like any unexported field. An embedded
		// unexported struct still promotes its exported fields, which can be
		// set.
		if (sf.Anonymous && name == "") || (inline && (sf.Anonymous || sf.IsExported())) {
			ft := sf.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				if !onPath[ft] {
					onPath[ft] = true
					indexStructFieldsInto(ft, idxPath, out, onPath)
					delete(onPath, ft)
				}
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

// isPointerCycle reports whether unwrapping pointer type t never reaches a
// non-pointer, as with type P *P. Decoding a value into one would allocate
// forever.
func isPointerCycle(t reflect.Type) bool {
	seen := map[reflect.Type]bool{}
	for ; t.Kind() == reflect.Pointer; t = t.Elem() {
		if seen[t] {
			return true
		}
		seen[t] = true
	}
	return false
}
