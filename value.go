package ashlar

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Presence distinguishes omitted fields from explicit null and explicit values.
type Presence uint8

// Supported field-presence states.
const (
	PresenceOmitted Presence = iota
	PresenceNull
	PresencePresent
)

// String returns the human-readable name of p ("omitted", "null", or "present").
func (p Presence) String() string {
	switch p {
	case PresenceOmitted:
		return "omitted"
	case PresenceNull:
		return "null"
	case PresencePresent:
		return "present"
	default:
		return "unknown"
	}
}

// Kind identifies the structural data kind of an immutable Value node.
type Kind uint8

// Supported Value kinds.
const (
	KindInvalid Kind = iota
	KindNull
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// String returns the human-readable name of k.
func (k Kind) String() string {
	switch k {
	case KindInvalid:
		return "invalid"
	case KindNull:
		return "null"
	case KindBool:
		return " boolean"
	case KindNumber:
		return "number"
	case KindString:
		return "string"
	case KindArray:
		return "array"
	case KindObject:
		return "object"
	default:
		return "unknown"
	}
}

// Member represents a single key-value entry in an object Value, preserving the
// source origin of the key token alongside its Value node.
type Member struct {
	Key       string
	KeyOrigin Origin
	Value     Value
}

// Value is an immutable document or configuration node.
// A zero Value has Kind() == KindInvalid and Presence() == PresenceOmitted.
type Value struct {
	node *valueNode
}

type valueNode struct {
	kind    Kind
	prov    Provenance
	boolVal bool
	strVal  string // Holds string value for KindString, or exact number lexeme for KindNumber
	elems   []Value
	members []Member
	index   map[string]int
}

// OmittedValue returns the zero Value representing an omitted field (PresenceOmitted).
func OmittedValue() Value {
	return Value{}
}

// NullValue constructs an immutable explicit null Value (PresenceNull).
func NullValue(prov Provenance) Value {
	return Value{node: &valueNode{kind: KindNull, prov: prov}}
}

// BoolValue constructs an immutable boolean Value (PresencePresent).
func BoolValue(b bool, prov Provenance) Value {
	return Value{node: &valueNode{kind: KindBool, prov: prov, boolVal: b}}
}

// NumberValue constructs an immutable numeric Value preserving the exact source
// lexeme (e.g., "1", "1.0", "1e0", "18446744073709551615") without float64 rounding.
// It returns an error wrapping ErrSyntax if lexeme is not a valid JSON number literal.
func NumberValue(lexeme string, prov Provenance) (Value, error) {
	if !isValidNumberLexeme(lexeme) {
		return Value{}, NewError(
			ErrSyntax,
			RootPointer,
			prov.Effective,
			fmt.Sprintf("invalid number lexeme %q", lexeme),
			nil,
		)
	}
	return Value{node: &valueNode{kind: KindNumber, prov: prov, strVal: lexeme}}, nil
}

// StringValue constructs an immutable string Value (PresencePresent).
func StringValue(s string, prov Provenance) Value {
	return Value{node: &valueNode{kind: KindString, prov: prov, strVal: s}}
}

// ArrayValue constructs an immutable array Value, defensively copying elems.
func ArrayValue(elems []Value, prov Provenance) Value {
	copied := make([]Value, len(elems))
	copy(copied, elems)
	return Value{node: &valueNode{kind: KindArray, prov: prov, elems: copied}}
}

// ObjectValue constructs an immutable object Value, defensively copying members
// and rejecting duplicate keys with an *Error wrapping ErrDuplicateKey.
func ObjectValue(members []Member, prov Provenance) (Value, error) {
	copied := make([]Member, len(members))
	idx := make(map[string]int, len(members))
	for i, m := range members {
		if firstIdx, dup := idx[m.Key]; dup {
			firstLoc := copied[firstIdx].KeyOrigin.Location()
			msg := fmt.Sprintf("duplicate object key %q", m.Key)
			if firstLoc != "" {
				msg = fmt.Sprintf("duplicate object key %q (first defined at %s)", m.Key, firstLoc)
			}
			orig := m.KeyOrigin
			if !orig.Valid() {
				orig = prov.Effective
			}
			return Value{}, NewError(ErrDuplicateKey, NewPointer(m.Key), orig, msg, nil)
		}
		copied[i] = m
		idx[m.Key] = i
	}
	return Value{node: &valueNode{kind: KindObject, prov: prov, members: copied, index: idx}}, nil
}

// Kind returns the structural Kind of v.
func (v Value) Kind() Kind {
	if v.node == nil {
		return KindInvalid
	}
	return v.node.kind
}

// Presence reports whether v is PresenceOmitted, PresenceNull, or PresencePresent.
func (v Value) Presence() Presence {
	if v.node == nil || v.node.kind == KindInvalid {
		return PresenceOmitted
	}
	if v.node.kind == KindNull {
		return PresenceNull
	}
	return PresencePresent
}

// IsOmitted reports whether v has PresenceOmitted.
func (v Value) IsOmitted() bool { return v.Presence() == PresenceOmitted }

// IsNull reports whether v is an explicit null (PresenceNull).
func (v Value) IsNull() bool { return v.Presence() == PresenceNull }

// IsPresent reports whether v is present and non-null (PresencePresent).
func (v Value) IsPresent() bool { return v.Presence() == PresencePresent }

// Provenance returns the Provenance of v.
func (v Value) Provenance() Provenance {
	if v.node == nil {
		return Provenance{}
	}
	return v.node.prov
}

// WithProvenance returns a shallow copy of v with its Provenance set to prov.
func (v Value) WithProvenance(prov Provenance) Value {
	if v.node == nil {
		return Value{}
	}
	cp := *v.node
	cp.prov = prov
	return Value{node: &cp}
}

// Bool returns the boolean value when v.Kind() == KindBool.
func (v Value) Bool() (bool, bool) {
	if v.Kind() != KindBool {
		return false, false
	}
	return v.node.boolVal, true
}

// StringVal returns the string value when v.Kind() == KindString.
func (v Value) StringVal() (string, bool) {
	if v.Kind() != KindString {
		return "", false
	}
	return v.node.strVal, true
}

// NumberLexeme returns the exact source lexeme when v.Kind() == KindNumber.
func (v Value) NumberLexeme() (string, bool) {
	if v.Kind() != KindNumber {
		return "", false
	}
	return v.node.strVal, true
}

// JSONNumber returns the number as a json.Number when v.Kind() == KindNumber.
func (v Value) JSONNumber() (json.Number, bool) {
	if v.Kind() != KindNumber {
		return "", false
	}
	return json.Number(v.node.strVal), true
}

// Int64 parses a KindNumber value as a base-10 int64.
func (v Value) Int64() (int64, error) {
	if v.Kind() != KindNumber {
		return 0, fmt.Errorf("ashlar: value is %s, not number", strings.TrimSpace(v.Kind().String()))
	}
	return strconv.ParseInt(v.node.strVal, 10, 64)
}

// Uint64 parses a KindNumber value as a base-10 uint64.
func (v Value) Uint64() (uint64, error) {
	if v.Kind() != KindNumber {
		return 0, fmt.Errorf("ashlar: value is %s, not number", strings.TrimSpace(v.Kind().String()))
	}
	return strconv.ParseUint(v.node.strVal, 10, 64)
}

// Float64 parses a KindNumber value as a float64.
func (v Value) Float64() (float64, error) {
	if v.Kind() != KindNumber {
		return 0, fmt.Errorf("ashlar: value is %s, not number", strings.TrimSpace(v.Kind().String()))
	}
	return strconv.ParseFloat(v.node.strVal, 64)
}

// Len returns the number of elements in a KindArray or members in a KindObject, or 0 otherwise.
func (v Value) Len() int {
	switch v.Kind() {
	case KindArray:
		return len(v.node.elems)
	case KindObject:
		return len(v.node.members)
	default:
		return 0
	}
}

// Index returns the i-th element of a KindArray, or OmittedValue() if out of bounds.
func (v Value) Index(i int) Value {
	if v.Kind() != KindArray || i < 0 || i >= len(v.node.elems) {
		return OmittedValue()
	}
	return v.node.elems[i]
}

// Elements returns a defensive copy of the array's elements, or nil if not a KindArray.
func (v Value) Elements() []Value {
	if v.Kind() != KindArray || len(v.node.elems) == 0 {
		return nil
	}
	out := make([]Value, len(v.node.elems))
	copy(out, v.node.elems)
	return out
}

// Keys returns the object's keys in declaration order, or nil if not a KindObject.
func (v Value) Keys() []string {
	if v.Kind() != KindObject || len(v.node.members) == 0 {
		return nil
	}
	out := make([]string, len(v.node.members))
	for i, m := range v.node.members {
		out[i] = m.Key
	}
	return out
}

// Members returns a defensive copy of the object's members in declaration order.
func (v Value) Members() []Member {
	if v.Kind() != KindObject || len(v.node.members) == 0 {
		return nil
	}
	out := make([]Member, len(v.node.members))
	copy(out, v.node.members)
	return out
}

// Lookup returns the Value for key in a KindObject, or (OmittedValue(), false) if absent.
func (v Value) Lookup(key string) (Value, bool) {
	m, ok := v.LookupMember(key)
	if !ok {
		return OmittedValue(), false
	}
	return m.Value, true
}

// LookupMember returns the Member for key in a KindObject.
func (v Value) LookupMember(key string) (Member, bool) {
	if v.Kind() != KindObject {
		return Member{}, false
	}
	idx, ok := v.node.index[key]
	if !ok {
		return Member{}, false
	}
	return v.node.members[idx], true
}

// At resolves an RFC 6901 JSON Pointer against v, returning OmittedValue() if
// ptr is invalid or any token along the path does not exist.
func (v Value) At(ptr Pointer) Value {
	if ptr == RootPointer {
		return v
	}
	tokens, err := ptr.Tokens()
	if err != nil {
		return OmittedValue()
	}
	cur := v
	for _, tok := range tokens {
		switch cur.Kind() {
		case KindObject:
			next, ok := cur.Lookup(tok)
			if !ok {
				return OmittedValue()
			}
			cur = next
		case KindArray:
			if len(tok) > 1 && tok[0] == '0' {
				return OmittedValue() // RFC 6901 forbids leading zeros on array indices
			}
			idx, convErr := strconv.Atoi(tok)
			if convErr != nil || idx < 0 || idx >= cur.Len() {
				return OmittedValue()
			}
			cur = cur.Index(idx)
		default:
			return OmittedValue()
		}
	}
	return cur
}

// PresenceAt returns the Presence of the node at ptr (PresenceOmitted, PresenceNull, or PresencePresent).
func (v Value) PresenceAt(ptr Pointer) Presence {
	return v.At(ptr).Presence()
}

// Summary formats a concise representation of v, replacing sensitive scalars with RedactedLiteral.
func (v Value) Summary(r Redactor, ptr Pointer) string {
	if v.IsOmitted() {
		return "<omitted>"
	}
	if v.Provenance().Sensitive || IsSensitivePath(r, ptr, ptr.LastToken()) {
		return RedactedLiteral
	}
	switch v.Kind() {
	case KindNull:
		return "null"
	case KindBool:
		return strconv.FormatBool(v.node.boolVal)
	case KindNumber:
		return v.node.strVal
	case KindString:
		return strconv.Quote(v.node.strVal)
	case KindArray:
		return fmt.Sprintf("[array len=%d]", len(v.node.elems))
	case KindObject:
		return fmt.Sprintf("{object keys=%d}", len(v.node.members))
	default:
		return "<invalid>"
	}
}

// OverrideWith returns next with its Provenance updated to record v as superseded,
// preserving full override history and redaction.
func (v Value) OverrideWith(next Value, r Redactor, ptr Pointer) Value {
	if v.IsOmitted() || next.IsOmitted() {
		return next
	}
	sens := v.Provenance().Sensitive || next.Provenance().Sensitive || IsSensitivePath(r, ptr, ptr.LastToken())
	priorSum := v.Summary(r, ptr)
	mergedProv := v.Provenance().RecordOverride(next.Provenance(), priorSum, sens)
	return next.WithProvenance(mergedProv)
}

// Explain formats the full provenance chain of v at ptr, redacting sensitive values.
func (v Value) Explain(r Redactor, ptr Pointer) string {
	if v.IsOmitted() {
		return "omitted"
	}
	return v.Provenance().Explain(r, ptr, v.Summary(r, ptr))
}
