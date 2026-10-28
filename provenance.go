package ashlar

import (
	"fmt"
	"strings"
)

// ProvenanceEntry records a prior value that was superseded during fragment
// composition or override layering.
type ProvenanceEntry struct {
	Origin    Origin
	Summary   string
	Sensitive bool
}

// RedactedSummary returns RedactedLiteral when the entry or target pointer is
// classified as sensitive, and the recorded Summary otherwise.
func (e ProvenanceEntry) RedactedSummary(r Redactor, ptr Pointer) string {
	if e.Sensitive || IsSensitivePath(r, ptr, ptr.LastToken()) {
		return RedactedLiteral
	}
	return e.Summary
}

// Provenance records the effective origin of a Value and the ordered history of
// prior values it superseded (oldest first or in override order).
// Provenance is an immutable value; its superseded slice is defensively copied.
type Provenance struct {
	Effective  Origin
	Sensitive  bool
	superseded []ProvenanceEntry
}

// NewProvenance constructs an immutable Provenance with the given effective origin,
// sensitivity flag, and optional superseded history.
func NewProvenance(effective Origin, sensitive bool, superseded ...ProvenanceEntry) Provenance {
	return Provenance{
		Effective:  effective,
		Sensitive:  sensitive,
		superseded: cloneEntries(superseded),
	}
}

// Superseded returns a defensive copy of the ordered override history, from the
// earliest value to the most recently superseded value.
func (p Provenance) Superseded() []ProvenanceEntry {
	return cloneEntries(p.superseded)
}

// WithSensitive returns a copy of p with Sensitive set to the given value.
func (p Provenance) WithSensitive(sensitive bool) Provenance {
	p.Sensitive = sensitive
	if sensitive {
		entries := cloneEntries(p.superseded)
		for i := range entries {
			entries[i].Sensitive = true
			entries[i].Summary = RedactedLiteral
		}
		p.superseded = entries
	}
	return p
}

// RecordOverride returns a new Provenance where next is the effective provenance
// and p (with priorSummary) is recorded in the superseded chain before any
// history already carried by next.
func (p Provenance) RecordOverride(next Provenance, priorSummary string, sensitive bool) Provenance {
	isSens := p.Sensitive || next.Sensitive || sensitive
	history := make([]ProvenanceEntry, 0, len(p.superseded)+1+len(next.superseded))
	history = append(history, p.superseded...)
	if p.Effective.Valid() || priorSummary != "" {
		sum := priorSummary
		if isSens {
			sum = RedactedLiteral
		}
		history = append(history, ProvenanceEntry{
			Origin:    p.Effective,
			Summary:   sum,
			Sensitive: isSens,
		})
	}
	history = append(history, next.superseded...)
	if isSens {
		for i := range history {
			history[i].Sensitive = true
			history[i].Summary = RedactedLiteral
		}
	}
	return Provenance{
		Effective:  next.Effective,
		Sensitive:  isSens,
		superseded: history,
	}
}

// Explain formats a single-line human-readable explanation of the effective origin,
// value summary, and override history, redacting all values if the path or
// provenance is marked sensitive.
func (p Provenance) Explain(r Redactor, ptr Pointer, currentSummary string) string {
	sens := p.Sensitive || IsSensitivePath(r, ptr, ptr.LastToken())
	effVal := currentSummary
	if sens {
		effVal = RedactedLiteral
	}
	effLoc := p.Effective.String()
	if effLoc == "" {
		effLoc = "unknown"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s = %s", effLoc, effVal)
	if len(p.superseded) == 0 {
		return b.String()
	}

	b.WriteString(" (overrode ")
	// Render most recent override first so the reader traces backwards from effective -> prior -> default.
	for i := len(p.superseded) - 1; i >= 0; i-- {
		if i < len(p.superseded)-1 {
			b.WriteString(", ")
		}
		entry := p.superseded[i]
		loc := entry.Origin.String()
		if loc == "" {
			loc = "unknown"
		}
		fmt.Fprintf(&b, "%s = %s", loc, entry.RedactedSummary(r, ptr))
	}
	b.WriteByte(')')
	return b.String()
}

func cloneEntries(in []ProvenanceEntry) []ProvenanceEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]ProvenanceEntry, len(in))
	copy(out, in)
	return out
}

// FieldProvenance describes the effective provenance and override history of a
// single leaf field in a Value tree.
type FieldProvenance struct {
	Pointer     Pointer
	Provenance  Provenance
	Explanation string
}

// ExplainAll walks v in deterministic declaration order and returns the
// provenance explanation for every leaf value (scalars, empty arrays, and empty
// objects), honoring r (or DefaultRedactor when r is nil).
func (v Value) ExplainAll(r Redactor) []FieldProvenance {
	if r == nil {
		r = DefaultRedactor()
	}
	if v.IsOmitted() {
		return nil
	}
	var out []FieldProvenance
	collectLeafProvenance(v, RootPointer, r, &out)
	return out
}

// FormatProvenanceReport renders a multi-line provenance report ("<pointer>: <explanation>")
// for all leaf values in v, honoring r (or DefaultRedactor when r is nil).
func (v Value) FormatProvenanceReport(r Redactor) string {
	entries := v.ExplainAll(r)
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		ptr := string(e.Pointer)
		if ptr == "" {
			ptr = "/"
		}
		fmt.Fprintf(&b, "%s: %s", ptr, e.Explanation)
	}
	return b.String()
}

func collectLeafProvenance(v Value, ptr Pointer, r Redactor, out *[]FieldProvenance) {
	switch v.Kind() {
	case KindObject:
		members := v.Members()
		if len(members) == 0 {
			*out = append(*out, FieldProvenance{
				Pointer:     ptr,
				Provenance:  v.Provenance(),
				Explanation: v.Explain(r, ptr),
			})
			return
		}
		for _, m := range members {
			collectLeafProvenance(m.Value, ptr.Child(m.Key), r, out)
		}
	case KindArray:
		elems := v.Elements()
		if len(elems) == 0 {
			*out = append(*out, FieldProvenance{
				Pointer:     ptr,
				Provenance:  v.Provenance(),
				Explanation: v.Explain(r, ptr),
			})
			return
		}
		for i, el := range elems {
			collectLeafProvenance(el, ptr.Index(i), r, out)
		}
	default:
		*out = append(*out, FieldProvenance{
			Pointer:     ptr,
			Provenance:  v.Provenance(),
			Explanation: v.Explain(r, ptr),
		})
	}
}

// Sanitize returns a copy of v where every sensitive scalar or subtree
// identified by r (or DefaultRedactor when r is nil) is replaced with
// RedactedLiteral (or a domain-separated SecretFingerprint when useFingerprint
// is true).
func (v Value) Sanitize(r Redactor, useFingerprint bool) Value {
	if r == nil {
		r = DefaultRedactor()
	}
	return sanitizeTree(v, RootPointer, false, r, useFingerprint)
}

func sanitizeTree(v Value, ptr Pointer, parentSensitive bool, r Redactor, useFingerprint bool) Value {
	if v.IsOmitted() {
		return v
	}
	sens := parentSensitive || v.Provenance().Sensitive || IsSensitivePath(r, ptr, ptr.LastToken())
	switch v.Kind() {
	case KindObject:
		members := v.Members()
		for i, m := range members {
			keySens := sens || IsSensitivePath(r, ptr.Child(m.Key), m.Key)
			members[i].Value = sanitizeTree(m.Value, ptr.Child(m.Key), keySens, r, useFingerprint)
		}
		obj, _ := ObjectValue(members, v.Provenance())
		return obj
	case KindArray:
		elems := v.Elements()
		for i, el := range elems {
			elems[i] = sanitizeTree(el, ptr.Index(i), sens, r, useFingerprint)
		}
		return ArrayValue(elems, v.Provenance())
	default:
		if !sens {
			return v
		}
		replacement := RedactedLiteral
		if useFingerprint {
			if s, ok := v.StringVal(); ok {
				replacement = SecretFingerprint(string(ptr), s)
			} else if lex, ok := v.NumberLexeme(); ok {
				replacement = SecretFingerprint(string(ptr), lex)
			}
		}
		return StringValue(replacement, NewProvenance(v.Provenance().Effective, true))
	}
}
