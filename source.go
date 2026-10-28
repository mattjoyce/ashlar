package ashlar

import (
	"fmt"
	"strings"
)

// OriginKind classifies where a document node or resolved configuration value originated.
type OriginKind string

// Supported origin kinds.
const (
	OriginDefault  OriginKind = "default"
	OriginFile     OriginKind = "file"
	OriginEnv      OriginKind = "env"
	OriginExplicit OriginKind = "explicit"
)

// SourcePos identifies a 1-based line and column coordinate and a 0-based byte
// offset within a source document.
type SourcePos struct {
	Line   int
	Column int
	Offset int64
}

// Valid reports whether p holds a positive 1-based line and column coordinate.
func (p SourcePos) Valid() bool {
	return p.Line > 0 && p.Column > 0
}

// String formats p as "line:column" when valid, or "" otherwise.
func (p SourcePos) String() string {
	if !p.Valid() {
		return ""
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// SourceSpan identifies a contiguous range within a named source document.
type SourceSpan struct {
	Source string
	Start  SourcePos
	End    SourcePos
}

// Valid reports whether s names a source or has a valid start position.
func (s SourceSpan) Valid() bool {
	return s.Source != "" || s.Start.Valid()
}

// String formats the span's starting coordinate as "file:line:column" when both
// source and position are present, "file" when only source is present, or
// "line:column" when only position is present.
func (s SourceSpan) String() string {
	switch {
	case s.Source != "" && s.Start.Valid():
		return fmt.Sprintf("%s:%d:%d", s.Source, s.Start.Line, s.Start.Column)
	case s.Source != "":
		return s.Source
	case s.Start.Valid():
		return s.Start.String()
	default:
		return ""
	}
}

// Origin describes the concrete source of a configuration or contract value.
// Origin is an immutable value; its include chain is defensively copied.
type Origin struct {
	Kind OriginKind
	Name string
	Span SourceSpan
	via  []SourceSpan
}

// DefaultOrigin constructs an Origin representing an application-supplied default.
func DefaultOrigin(name string) Origin {
	return Origin{Kind: OriginDefault, Name: name}
}

// FileOrigin constructs an Origin backed by a source file span and optional include chain.
func FileOrigin(span SourceSpan, via ...SourceSpan) Origin {
	return Origin{
		Kind: OriginFile,
		Name: span.Source,
		Span: span,
		via:  cloneSpans(via),
	}
}

// EnvOrigin constructs an Origin representing an environment variable binding or interpolation.
func EnvOrigin(varName string) Origin {
	return Origin{Kind: OriginEnv, Name: varName}
}

// ExplicitOrigin constructs an Origin representing an explicit caller/CLI override.
func ExplicitOrigin(name string) Origin {
	return Origin{Kind: OriginExplicit, Name: name}
}

// WithVia returns a copy of o with the given include chain attached.
func (o Origin) WithVia(via ...SourceSpan) Origin {
	o.via = cloneSpans(via)
	return o
}

// Via returns a defensive copy of the include chain leading to this origin, outermost first.
func (o Origin) Via() []SourceSpan {
	return cloneSpans(o.via)
}

// Valid reports whether o has a non-empty OriginKind.
func (o Origin) Valid() bool {
	return o.Kind != ""
}

// Location returns the primary "file:line:column" or synthetic origin label without the include chain.
func (o Origin) Location() string {
	switch o.Kind {
	case OriginFile:
		if s := o.Span.String(); s != "" {
			return s
		}
		if o.Name != "" {
			return o.Name
		}
		return string(OriginFile)
	case OriginDefault, OriginEnv, OriginExplicit:
		if o.Name != "" {
			return string(o.Kind) + ":" + o.Name
		}
		return string(o.Kind)
	default:
		if s := o.Span.String(); s != "" {
			return s
		}
		return o.Name
	}
}

// String formats o including its primary location and any include chain.
func (o Origin) String() string {
	loc := o.Location()
	if len(o.via) == 0 {
		return loc
	}
	parts := make([]string, 0, len(o.via))
	for _, span := range o.via {
		if s := span.String(); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return loc
	}
	return fmt.Sprintf("%s (included via %s)", loc, strings.Join(parts, " -> "))
}

func cloneSpans(in []SourceSpan) []SourceSpan {
	if len(in) == 0 {
		return nil
	}
	out := make([]SourceSpan, len(in))
	copy(out, in)
	return out
}
