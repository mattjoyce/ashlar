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
