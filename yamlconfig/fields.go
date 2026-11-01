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
		if fields.shapeErr != "" {
			return fields.shapeError(val, ptr)
		}
		for _, m := range val.Members() {
			ft, ok := fields.target(t, m.Key)
			if !ok {
				return unknownFieldErr(ptr, m)
			}
			if err := checkKnownKeys(m.Value, ptr.Child(m.Key), ft); err != nil {
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

// structFields is the decoder's view of one struct type. keys maps each
// named YAML key, including keys promoted from embedded and inline structs, to
// its field's index path. catchAll is the index of the struct's own inline map
// field, or -1 when the struct is closed. shapeErr, when set, describes an
// inline map the decoder does not support; it is reported whenever a mapping
// is decoded into the struct.
type structFields struct {
	keys     map[string][]int
	catchAll int
	shapeErr string
}

// target returns the field type a key decodes into: its named field, else the
// catch-all's element type. It reports false when the struct has no place for
// the key.
func (sf structFields) target(t reflect.Type, key string) (reflect.Type, bool) {
	if idxPath, ok := sf.keys[key]; ok {
		return t.FieldByIndex(idxPath).Type, true
	}
	if sf.catchAll >= 0 {
		return t.Field(sf.catchAll).Type.Elem(), true
	}
	return nil, false
}

// shapeError reports sf.shapeErr at the struct mapping val decoded at ptr.
func (sf structFields) shapeError(val ashlar.Value, ptr ashlar.Pointer) error {
	return ashlar.NewError(ashlar.ErrValidation, ptr, val.Provenance().Effective, sf.shapeErr, nil)
}

// indexStructFields indexes the keys struct type t accepts, promoting the
// fields of embedded and inline structs, and finds its inline map catch-all.
func indexStructFields(t reflect.Type) structFields {
	sf := structFields{keys: make(map[string][]int), catchAll: -1}
	sf.indexInto(t, t, nil, map[reflect.Type]bool{t: true})
	return sf
}

// indexInto adds the keys of t, reached from root through prefix, to sf.
// onPath holds the struct types already being expanded on this branch, so a
// type that embeds itself, such as struct{ *T }, is not expanded again and
// cannot recurse forever.
func (sf *structFields) indexInto(root, t reflect.Type, prefix []int, onPath map[reflect.Type]bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		idxPath := append(append([]int(nil), prefix...), i)
		name, skip, inline := parseFieldTag(f)
		if skip {
			continue
		}
		if inline && isMapOrMapPointer(f.Type) {
			// An unexported inline map cannot be set, so it is ignored like
			// any unexported field and the struct stays closed.
			if f.IsExported() {
				sf.addCatchAll(root, t, f, prefix)
			}
			continue
		}
		// A named unexported field cannot be set through reflection, so an
		// inline tag on one is ignored like any unexported field. An embedded
		// unexported struct still promotes its exported fields, which can be
		// set.
		if (f.Anonymous && name == "") || (inline && (f.Anonymous || f.IsExported())) {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				if !onPath[ft] {
					onPath[ft] = true
					sf.indexInto(root, ft, idxPath, onPath)
					delete(onPath, ft)
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		if _, exists := sf.keys[name]; !exists {
			sf.keys[name] = idxPath
		}
	}
}

// addCatchAll records f, an exported inline map field of owner, as root's
// catch-all, or records why it cannot be one. Only the first shape problem is
// kept.
func (sf *structFields) addCatchAll(root, owner reflect.Type, f reflect.StructField, prefix []int) {
	if sf.shapeErr != "" {
		return
	}
	switch {
	case prefix != nil:
		sf.shapeErr = fmt.Sprintf(
			"%s: inline map field %s.%s is inside an embedded or inline struct, which is not supported; declare the inline map on %s itself",
			root, owner, f.Name, root)
	case f.Type.Kind() == reflect.Pointer:
		sf.shapeErr = fmt.Sprintf("%s: inline field %s must be a map, not a pointer to a map", root, f.Name)
	case f.Type.Key().Kind() != reflect.String:
		sf.shapeErr = fmt.Sprintf("%s: inline map field %s must have string keys, got %s", root, f.Name, f.Type.Key())
	case sf.catchAll >= 0:
		sf.shapeErr = fmt.Sprintf("%s: more than one inline map field (%s and %s)", root, root.Field(sf.catchAll).Name, f.Name)
	default:
		sf.catchAll = f.Index[0]
	}
}

// isMapOrMapPointer reports whether t is a map or a pointer to a map.
func isMapOrMapPointer(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Map
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
