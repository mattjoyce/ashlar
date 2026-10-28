package yamlconfig

import (
	"fmt"
	"strings"

	"github.com/mattjoyce/ashlar"
)

// MergeRule defines how two nodes at the same RFC 6901 Pointer compose across
// configuration fragments.
type MergeRule uint8

const (
	// MergeUnspecified indicates no explicit MergeRule was configured; objects
	// default to MergeMap and sequences/scalars default to MergeReplace.
	MergeUnspecified MergeRule = iota
	// MergeReplace replaces the earlier value with the later fragment's value,
	// recording the superseded value and its Origin in Provenance.
	MergeReplace
	// MergeMap recursively deep-merges mapping keys across fragments.
	MergeMap
	// MergeAppendSlice appends sequence elements in fragment order.
	MergeAppendSlice
	// MergeForbidDuplicate rejects a key or path if it is defined in more than
	// one fragment.
	MergeForbidDuplicate
)

// String returns a human-readable name for r.
func (r MergeRule) String() string {
	switch r {
	case MergeUnspecified:
		return "unspecified"
	case MergeReplace:
		return "replace"
	case MergeMap:
		return "map"
	case MergeAppendSlice:
		return "append_slice"
	case MergeForbidDuplicate:
		return "forbid_duplicate"
	default:
		return "unknown"
	}
}

// PatternMergeRule associates a pointer pattern (supporting "*" as a single-token
// wildcard and "~*" for a literal "*" token, e.g. "/plugins/*" or "/a~1b/*")
// with a MergeRule.
type PatternMergeRule struct {
	Pattern string
	Rule    MergeRule
}

type patternToken struct {
	literal  string
	wildcard bool
}

// CompositionPolicy declares the structural and per-path composition rules used
// when merging multiple YAML fragments.
type CompositionPolicy struct {
	ObjectRule   MergeRule
	ArrayRule    MergeRule
	ScalarRule   MergeRule
	Rules        map[ashlar.Pointer]MergeRule
	PatternRules []PatternMergeRule
	Redactor     ashlar.Redactor
}

// DefaultCompositionPolicy returns a policy that deep-merges mappings
// (MergeMap) and replaces sequences and scalars (MergeReplace) while recording
// provenance with ashlar.DefaultRedactor().
func DefaultCompositionPolicy() CompositionPolicy {
	return CompositionPolicy{
		ObjectRule: MergeMap,
		ArrayRule:  MergeReplace,
		ScalarRule: MergeReplace,
		Rules:      make(map[ashlar.Pointer]MergeRule),
		Redactor:   ashlar.DefaultRedactor(),
	}
}

func (p CompositionPolicy) redactor() ashlar.Redactor {
	if p.Redactor != nil {
		return p.Redactor
	}
	return ashlar.DefaultRedactor()
}

func (p CompositionPolicy) validate() error {
	for ptr := range p.Rules {
		if !ptr.Valid() {
			return ashlar.NewError(
				ashlar.ErrComposition,
				ashlar.RootPointer,
				ashlar.Origin{},
				fmt.Sprintf("invalid pointer %q in CompositionPolicy.Rules", ptr),
				ashlar.ErrPointer,
			)
		}
	}
	for _, pr := range p.PatternRules {
		if _, err := parsePatternTokens(pr.Pattern); err != nil {
			return ashlar.NewError(
				ashlar.ErrComposition,
				ashlar.RootPointer,
				ashlar.Origin{},
				fmt.Sprintf("invalid pattern %q in CompositionPolicy.PatternRules", pr.Pattern),
				err,
			)
		}
	}
	return nil
}

func (p CompositionPolicy) ruleFor(ptr ashlar.Pointer, kind ashlar.Kind) MergeRule {
	if r, ok := p.Rules[ptr]; ok && r != MergeUnspecified {
		return r
	}
	for _, pr := range p.PatternRules {
		if pr.Rule != MergeUnspecified && matchPointerPattern(pr.Pattern, ptr) {
			return pr.Rule
		}
	}
	switch kind {
	case ashlar.KindObject:
		if p.ObjectRule != MergeUnspecified {
			return p.ObjectRule
		}
		return MergeMap
	case ashlar.KindArray:
		if p.ArrayRule != MergeUnspecified {
			return p.ArrayRule
		}
		return MergeReplace
	default:
		if p.ScalarRule != MergeUnspecified {
			return p.ScalarRule
		}
		return MergeReplace
	}
}

func parsePatternTokens(pattern string) ([]patternToken, error) {
	if pattern == "" {
		return nil, nil
	}
	if pattern[0] != '/' {
		return nil, fmt.Errorf("%w: pattern %q must be empty or start with '/'", ashlar.ErrPointer, pattern)
	}
	rawParts := strings.Split(pattern[1:], "/")
	out := make([]patternToken, len(rawParts))
	for i, part := range rawParts {
		if part == "*" {
			out[i] = patternToken{wildcard: true}
			continue
		}
		decoded, err := decodePatternSegment(part)
		if err != nil {
			return nil, err
		}
		out[i] = patternToken{literal: decoded}
	}
	return out, nil
}

func decodePatternSegment(part string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(part); i++ {
		if part[i] != '~' {
			b.WriteByte(part[i])
			continue
		}
		if i+1 >= len(part) {
			return "", fmt.Errorf("%w: trailing '~' in pattern segment %q", ashlar.ErrPointer, part)
		}
		switch part[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		case '*':
			b.WriteByte('*')
		default:
			return "", fmt.Errorf("%w: invalid escape '~%c' in pattern segment %q", ashlar.ErrPointer, part[i+1], part)
		}
		i++
	}
	return b.String(), nil
}

func matchPointerPattern(pattern string, ptr ashlar.Pointer) bool {
	patTokens, err := parsePatternTokens(pattern)
	if err != nil {
		return false
	}
	ptrTokens, err := ptr.Tokens()
	if err != nil || len(patTokens) != len(ptrTokens) {
		return false
	}
	for i, pt := range patTokens {
		if pt.wildcard {
			continue
		}
		if pt.literal != ptrTokens[i] {
			return false
		}
	}
	return true
}

// ComposeFragments merges the Root values of fragments in order under policy.
// It is all-or-nothing: any conflict returns an empty Value and a source-located
// *ashlar.Error wrapping ashlar.ErrComposition.
func ComposeFragments(policy CompositionPolicy, fragments []Fragment) (ashlar.Value, error) {
	if err := policy.validate(); err != nil {
		return ashlar.Value{}, err
	}
	if len(fragments) == 0 {
		return ashlar.OmittedValue(), nil
	}
	vals := make([]ashlar.Value, len(fragments))
	for i, f := range fragments {
		vals[i] = f.Root
	}
	return ComposeValues(policy, vals...)
}

// ComposeValues merges values in order under policy, extending provenance
// chains on every overridden or merged node.
func ComposeValues(policy CompositionPolicy, values ...ashlar.Value) (ashlar.Value, error) {
	if err := policy.validate(); err != nil {
		return ashlar.Value{}, err
	}
	var acc ashlar.Value
	for _, v := range values {
		if v.IsOmitted() {
			continue
		}
		if acc.IsOmitted() {
			acc = v
			continue
		}
		merged, err := composeNode(policy, ashlar.RootPointer, acc, v)
		if err != nil {
			return ashlar.Value{}, err
		}
		acc = merged
	}
	return acc, nil
}

func composeNode(
	policy CompositionPolicy,
	ptr ashlar.Pointer,
	base ashlar.Value,
	overlay ashlar.Value,
) (ashlar.Value, error) {
	red := policy.redactor()
	rule := policy.ruleFor(ptr, overlay.Kind())
	if rule == MergeForbidDuplicate {
		return ashlar.Value{}, conflictError(
			ptr,
			base,
			overlay,
			fmt.Sprintf("duplicate definition at %q is forbidden by composition policy (first defined at %s)",
				ptrOrRoot(ptr), base.Provenance().Effective.Location()),
		)
	}

	if err := checkKindCompatibility(ptr, base, overlay, rule); err != nil {
		return ashlar.Value{}, err
	}

	switch rule {
	case MergeReplace:
		return base.OverrideWith(overlay, red, ptr), nil
	case MergeMap:
		if base.Kind() == ashlar.KindObject && overlay.Kind() == ashlar.KindObject {
			return composeObjects(policy, ptr, base, overlay)
		}
		return base.OverrideWith(overlay, red, ptr), nil
	case MergeAppendSlice:
		if base.Kind() == ashlar.KindArray && overlay.Kind() == ashlar.KindArray {
			return composeArraysAppend(policy, ptr, base, overlay), nil
		}
		return base.OverrideWith(overlay, red, ptr), nil
	default:
		return base.OverrideWith(overlay, red, ptr), nil
	}
}

func checkKindCompatibility(
	ptr ashlar.Pointer,
	base ashlar.Value,
	overlay ashlar.Value,
	rule MergeRule,
) error {
	if base.Kind() == overlay.Kind() {
		return nil
	}
	// Explicit null may replace or be replaced by a scalar under MergeReplace,
	// but container/scalar mismatches (e.g. scalar overriding object or array)
	// always fail closed with ErrComposition.
	baseContainer := base.Kind() == ashlar.KindObject || base.Kind() == ashlar.KindArray
	overlayContainer := overlay.Kind() == ashlar.KindObject || overlay.Kind() == ashlar.KindArray
	if baseContainer || overlayContainer || rule == MergeMap || rule == MergeAppendSlice {
		return conflictError(
			ptr,
			base,
			overlay,
			fmt.Sprintf("cannot compose %s (at %s) with %s (at %s)",
				base.Kind(), base.Provenance().Effective.Location(),
				overlay.Kind(), overlay.Provenance().Effective.Location()),
		)
	}
	return nil
}

func composeObjects(
	policy CompositionPolicy,
	ptr ashlar.Pointer,
	base ashlar.Value,
	overlay ashlar.Value,
) (ashlar.Value, error) {
	mergedMembers := base.Members()
	idxByKey := make(map[string]int, len(mergedMembers))
	for i, m := range mergedMembers {
		idxByKey[m.Key] = i
	}

	for _, om := range overlay.Members() {
		childPtr := ptr.Child(om.Key)
		if existingIdx, exists := idxByKey[om.Key]; exists {
			existing := mergedMembers[existingIdx]
			composedVal, err := composeNode(policy, childPtr, existing.Value, om.Value)
			if err != nil {
				return ashlar.Value{}, err
			}
			mergedMembers[existingIdx] = ashlar.Member{
				Key:       om.Key,
				KeyOrigin: om.KeyOrigin,
				Value:     composedVal,
			}
			continue
		}
		idxByKey[om.Key] = len(mergedMembers)
		mergedMembers = append(mergedMembers, om)
	}

	mergedContainer := base.OverrideWith(overlay, policy.redactor(), ptr)
	return ashlar.ObjectValue(mergedMembers, mergedContainer.Provenance())
}

func composeArraysAppend(
	policy CompositionPolicy,
	ptr ashlar.Pointer,
	base ashlar.Value,
	overlay ashlar.Value,
) ashlar.Value {
	combined := append(base.Elements(), overlay.Elements()...)
	mergedContainer := base.OverrideWith(overlay, policy.redactor(), ptr)
	return ashlar.ArrayValue(combined, mergedContainer.Provenance())
}

func conflictError(ptr ashlar.Pointer, base ashlar.Value, overlay ashlar.Value, msg string) error {
	orig := overlay.Provenance().Effective
	if !orig.Valid() {
		orig = base.Provenance().Effective
	}
	return ashlar.NewError(ashlar.ErrComposition, ptr, orig, msg, nil)
}

func ptrOrRoot(ptr ashlar.Pointer) string {
	if ptr == ashlar.RootPointer {
		return "/"
	}
	return string(ptr)
}
