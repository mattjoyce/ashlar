package ashlar

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// Exported sentinel error categories for programmatic inspection via errors.Is.
// Specific sub-sentinels wrap their parent category so callers can match either
// the specific failure (e.g., ErrDuplicateKey) or the category (e.g., ErrSyntax).
var (
	ErrSyntax          = errors.New("ashlar: syntax error")
	ErrDuplicateKey    = fmt.Errorf("%w: duplicate key", ErrSyntax)
	ErrTrailingContent = fmt.Errorf("%w: trailing content", ErrSyntax)

	ErrSchema       = errors.New("ashlar: schema violation")
	ErrUnknownField = fmt.Errorf("%w: unknown field", ErrSchema)

	ErrComposition  = errors.New("ashlar: composition error")
	ErrIncludeCycle = fmt.Errorf("%w: include cycle", ErrComposition)
	ErrUnresolved   = fmt.Errorf("%w: unresolved required value", ErrComposition)

	ErrValidation = errors.New("ashlar: validation error")
	ErrNotFound   = errors.New("ashlar: source not found")
)

// Error is a structured, source-located diagnostic error supporting errors.Is
// and errors.As. Sensitive values are scrubbed so .Error() and fmt.Formatter
// never emit raw secret material.
type Error struct {
	Kind      error
	Pointer   Pointer
	Origin    Origin
	Message   string
	Sensitive bool
	cause     error
}

// NewError constructs a structured *Error for the given category, pointer, origin,
// message, and optional underlying cause.
func NewError(kind error, ptr Pointer, origin Origin, message string, cause error) *Error {
	if kind == nil {
		kind = ErrValidation
	}
	return &Error{
		Kind:    kind,
		Pointer: ptr,
		Origin:  origin,
		Message: message,
		cause:   cause,
	}
}

// NewRedactedError constructs a structured *Error and scrubs rawSecret from both
// the message and the underlying cause whenever r (or DefaultRedactor if r is nil)
// classifies ptr as sensitive, or when forceRedact is true.
func NewRedactedError(
	kind error,
	ptr Pointer,
	origin Origin,
	message string,
	cause error,
	r Redactor,
	rawSecret string,
	forceRedact bool,
) *Error {
	sensitive := forceRedact || IsSensitivePath(r, ptr, ptr.LastToken())
	if sensitive {
		message = ScrubSecret(message, rawSecret)
		cause = sanitizeCause(cause, rawSecret)
	}
	err := NewError(kind, ptr, origin, message, cause)
	err.Sensitive = sensitive
	return err
}

// Cause returns the underlying cause error (with secret material scrubbed if Sensitive).
func (e *Error) Cause() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Unwrap returns the error's category sentinel and underlying cause for errors.Is / errors.As.
func (e *Error) Unwrap() []error {
	if e == nil {
		return nil
	}
	if e.Kind != nil && e.cause != nil {
		return []error{e.Kind, e.cause}
	}
	if e.Kind != nil {
		return []error{e.Kind}
	}
	if e.cause != nil {
		return []error{e.cause}
	}
	return nil
}

// Error formats the structured diagnostic with source coordinates, JSON Pointer,
// category, and redacted message.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	loc := e.Origin.String()
	if loc != "" {
		b.WriteString(loc)
	}
	if e.Pointer != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('(')
		b.WriteString(string(e.Pointer))
		b.WriteByte(')')
	}
	if b.Len() > 0 {
		b.WriteString(": ")
	}
	if e.Kind != nil {
		b.WriteString(e.Kind.Error())
		if e.Message != "" || e.cause != nil {
			b.WriteString(": ")
		}
	}
	if e.Message != "" {
		b.WriteString(e.Message)
		if e.cause != nil && !strings.Contains(e.Message, e.cause.Error()) {
			b.WriteString(": ")
			b.WriteString(e.cause.Error())
		}
	} else if e.cause != nil {
		b.WriteString(e.cause.Error())
	}
	return b.String()
}

// Format implements fmt.Formatter so "%v", "%+v", and "%s" always use the
// redacted diagnostic representation and never leak unredacted fields.
func (e *Error) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v', 's':
		_, _ = io.WriteString(s, e.Error())
	case 'q':
		_, _ = fmt.Fprintf(s, "%q", e.Error())
	}
}

// Errors aggregates multiple errors for all-or-nothing validation and composition reporting.
type Errors struct {
	errs []error
}

// JoinErrors combines non-nil errors, flattening any nested *Errors values.
// It returns nil if no non-nil errors are supplied, the single error if only one
// is present, or an *Errors value containing all errors in order.
func JoinErrors(errs ...error) error {
	var flat []error
	for _, err := range errs {
		if err == nil {
			continue
		}
		var multi *Errors
		if errors.As(err, &multi) && multi != nil {
			flat = append(flat, multi.errs...)
			continue
		}
		flat = append(flat, err)
	}
	switch len(flat) {
	case 0:
		return nil
	case 1:
		return flat[0]
	default:
		return &Errors{errs: flat}
	}
}

// List returns a defensive copy of the aggregated errors.
func (m *Errors) List() []error {
	if m == nil || len(m.errs) == 0 {
		return nil
	}
	out := make([]error, len(m.errs))
	copy(out, m.errs)
	return out
}

// Unwrap returns the underlying slice of errors for errors.Is and errors.As.
func (m *Errors) Unwrap() []error {
	return m.List()
}

// Error formats all aggregated errors separated by semicolons.
func (m *Errors) Error() string {
	if m == nil || len(m.errs) == 0 {
		return ""
	}
	parts := make([]string, len(m.errs))
	for i, err := range m.errs {
		parts[i] = err.Error()
	}
	return fmt.Sprintf("%d errors: %s", len(m.errs), strings.Join(parts, "; "))
}

// Format implements fmt.Formatter so "%v" and "%+v" render the redacted error list.
func (m *Errors) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v', 's':
		_, _ = io.WriteString(s, m.Error())
	case 'q':
		_, _ = fmt.Fprintf(s, "%q", m.Error())
	}
}

type redactedCause struct {
	msg   string
	inner error
}

func (r redactedCause) Error() string {
	return r.msg
}

func (r redactedCause) Unwrap() error {
	return r.inner
}

func sanitizeCause(cause error, rawSecret string) error {
	if cause == nil {
		return nil
	}
	orig := cause.Error()
	if rawSecret == "" {
		return errors.New(RedactedLiteral)
	}
	scrubbed := ScrubSecret(orig, rawSecret)
	if scrubbed == orig {
		return cause
	}
	// Preserve sentinel matching only if inner does not echo the secret when formatted,
	// by replacing the text with a clean error when the cause itself carries rawSecret.
	return redactedCause{msg: scrubbed, inner: nil}
}
