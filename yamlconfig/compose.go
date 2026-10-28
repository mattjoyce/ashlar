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
	// MergeReplace replaces the earlier value with the later fragment's value,
	// recording the superseded value and its Origin in Provenance.
	MergeReplace MergeRule = iota
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
// wildcard, e.g. "/plugins/*") with a MergeRule.
type PatternMergeRule struct {
	Pattern string
	Rule    MergeRule
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

func (p CompositionPolicy) ruleFor(ptr ashlar.Pointer, kind ashlar.Kind) MergeRule {
	if r, ok := p.Rules[ptr]; ok {
		return r
	}
	for _, pr := range p.PatternRules {
		if matchPointerPattern(pr.Pattern, ptr) {
			return pr.Rule
		}
	}
	switch kind {
	case ashlar.KindObject:
		return p.ObjectRule
	case ashlar.KindArray:
		return p.ArrayRule
	default:
		return p.ScalarRule
	}
}

func matchPointerPattern(pattern string, ptr ashlar.Pointer) bool {
	if pattern == string(ptr) {
		return true
	}
	if !strings.HasPrefix(pattern, "/") || ptr == ashlar.RootPointer {
		return false
	}
	patParts := strings.Split(pattern[1:], "/")
	ptrTokens, err := ptr.Tokens()
	if err != nil || len(patParts) != len(ptrTokens) {
		return false
	}
	for i, part := range patParts {
		if part == "*" {
			continue
		}
		if part != ptrTokens[i] {
			return false
		}
	}
	return true
}

// ComposeFragments merges the Root values of fragments in order under policy.
// It is all-or-nothing: any conflict returns an empty Value and a source-located
// *ashlar.Error wrapping ashlar.ErrComposition.
func ComposeFragments(policy CompositionPolicy, fragments []Fragment) (ashlar.Value, error) {
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
