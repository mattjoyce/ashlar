package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mattjoyce/ashlar"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// Validator is a compiled, reusable JSON Schema (Draft 2020-12) validator.
// It is safe for concurrent use by multiple goroutines.
type Validator struct {
	targetURI string
	compiled  *jsonschema.Schema
}

// TargetURI returns the schema URI (including any fragment anchor) that v was
// compiled from.
func (v *Validator) TargetURI() string {
	if v == nil {
		return ""
	}
	return v.targetURI
}

// Validate checks val against the compiled schema without mutating raw bytes or
// rounding numeric lexemes.
//
// Violations return structured *ashlar.Error (or *ashlar.ValidationErrors when
// multiple leaf violations occur) wrapping ashlar.ErrSchema and carrying each
// offending node's RFC 6901 Pointer and Origin (file:line:column). Sensitive
// values identified by redactor (or ashlar.DefaultRedactor when redactor is nil)
// are redacted from error messages.
func (v *Validator) Validate(val ashlar.Value, redactor ashlar.Redactor) error {
	if v == nil || v.compiled == nil {
		return ashlar.NewError(
			ashlar.ErrSchema,
			ashlar.RootPointer,
			val.Provenance().Effective,
			"validator is nil",
			nil,
		)
	}
	if redactor == nil {
		redactor = ashlar.DefaultRedactor()
	}

	inst, err := valueToInstance(val)
	if err != nil {
		return err
	}

	err = v.compiled.Validate(inst)
	if err == nil {
		return nil
	}

	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return ashlar.NewError(
			ashlar.ErrSchema,
			ashlar.RootPointer,
			val.Provenance().Effective,
			err.Error(),
			err,
		)
	}

	printer := message.NewPrinter(language.English)
	var leaves []error
	collectLeafViolations(ve, val, redactor, printer, &leaves)
	return ashlar.JoinErrors(leaves...)
}

func valueToInstance(val ashlar.Value) (any, error) {
	switch val.Kind() {
	case ashlar.KindNull:
		return nil, nil
	case ashlar.KindBool:
		b, _ := val.Bool()
		return b, nil
	case ashlar.KindNumber:
		lex, _ := val.NumberLexeme()
		return json.Number(lex), nil
	case ashlar.KindString:
		s, _ := val.StringVal()
		return s, nil
	case ashlar.KindArray:
		elems := val.Elements()
		out := make([]any, len(elems))
		for i, el := range elems {
			conv, err := valueToInstance(el)
			if err != nil {
				return nil, err
			}
			out[i] = conv
		}
		return out, nil
	case ashlar.KindObject:
		members := val.Members()
		out := make(map[string]any, len(members))
		for _, m := range members {
			conv, err := valueToInstance(m.Value)
			if err != nil {
				return nil, err
			}
			out[m.Key] = conv
		}
		return out, nil
	default:
		return nil, ashlar.NewError(
			ashlar.ErrSchema,
			ashlar.RootPointer,
			val.Provenance().Effective,
			"cannot validate omitted or invalid value",
			nil,
		)
	}
}

func collectLeafViolations(
	ve *jsonschema.ValidationError,
	root ashlar.Value,
	redactor ashlar.Redactor,
	printer *message.Printer,
	out *[]error,
) {
	if len(ve.Causes) > 0 {
		for _, cause := range ve.Causes {
			collectLeafViolations(cause, root, redactor, printer, out)
		}
		return
	}

	ptr := ashlar.NewPointer(ve.InstanceLocation...)
	if props := unexpectedPropertyNames(ve.ErrorKind); len(props) > 0 {
		objVal := root.At(ptr)
		for _, prop := range props {
			propPtr := ptr.Child(prop)
			orig := resolveOrigin(root, propPtr)
			if m, ok := objVal.LookupMember(prop); ok && m.KeyOrigin.Valid() {
				orig = m.KeyOrigin
			}
			msg := fmt.Sprintf("additional property %q is not allowed", prop)
			*out = append(*out, ashlar.NewError(ashlar.ErrSchema, propPtr, orig, msg, nil))
		}
		return
	}

	orig := resolveOrigin(root, ptr)
	msg := ve.ErrorKind.LocalizedString(printer)
	msg = redactViolationMessage(msg, root.At(ptr), ptr, redactor)
	*out = append(*out, ashlar.NewError(ashlar.ErrSchema, ptr, orig, msg, nil))
}

func unexpectedPropertyNames(ek jsonschema.ErrorKind) []string {
	switch k := ek.(type) {
	case *kind.AdditionalProperties:
		return k.Properties
	default:
		return nil
	}
}

func resolveOrigin(root ashlar.Value, ptr ashlar.Pointer) ashlar.Origin {
	if v := root.At(ptr); v.IsPresent() || v.IsNull() {
		if orig := v.Provenance().Effective; orig.Valid() {
			return orig
		}
	}
	tokens, err := ptr.Tokens()
	if err == nil {
		for i := len(tokens) - 1; i >= 0; i-- {
			ancPtr := ashlar.NewPointer(tokens[:i]...)
			if anc := root.At(ancPtr); anc.IsPresent() || anc.IsNull() {
				if orig := anc.Provenance().Effective; orig.Valid() {
					return orig
				}
			}
		}
	}
	return root.Provenance().Effective
}

func redactViolationMessage(msg string, node ashlar.Value, ptr ashlar.Pointer, redactor ashlar.Redactor) string {
	sensitive := node.Provenance().Sensitive || (redactor != nil && redactor.IsSensitive(ptr, ptr.LastToken()))
	if !sensitive {
		return msg
	}
	if s, ok := node.StringVal(); ok && s != "" {
		msg = strings.ReplaceAll(msg, s, ashlar.RedactedLiteral)
	}
	if lex, ok := node.NumberLexeme(); ok && lex != "" {
		msg = strings.ReplaceAll(msg, lex, ashlar.RedactedLiteral)
	}
	return msg
}
