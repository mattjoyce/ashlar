package yamlconfig

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/mattjoyce/ashlar"
)

// Override binds an RFC 6901 Pointer to a single scalar or container Value at a
// declared precedence layer (default, environment, or explicit override).
//
// An Override is a targeted update: only the node at Pointer changes, and
// sibling members and elements of its ancestors are kept. The composition rule
// for Pointer itself is applied as in fragment composition. An existing
// ancestor whose rule is MergeForbidDuplicate rejects the update with
// ErrComposition; ancestors that do not yet exist are created.
type Override struct {
	Pointer ashlar.Pointer
	Value   ashlar.Value
}

// DefaultSetting constructs an Override at ptr with ashlar.DefaultOrigin(label)
// recursively stamped onto val and all descendant nodes.
func DefaultSetting(ptr ashlar.Pointer, label string, val ashlar.Value) Override {
	if label == "" {
		label = ptr.LastToken()
	}
	orig := ashlar.DefaultOrigin(label)
	return Override{Pointer: ptr, Value: val.StampLayerOrigin(orig, val.Provenance().Sensitive)}
}

// ExplicitSetting constructs an Override at ptr with ashlar.ExplicitOrigin(label)
// recursively stamped onto val and all descendant nodes.
func ExplicitSetting(ptr ashlar.Pointer, label string, val ashlar.Value) Override {
	if label == "" {
		label = string(ptr)
	}
	orig := ashlar.ExplicitOrigin(label)
	return Override{Pointer: ptr, Value: val.StampLayerOrigin(orig, val.Provenance().Sensitive)}
}

// ResolveOptions configures the end-to-end runtime configuration resolution
// pipeline (defaults -> composed YAML fragments -> env -> explicit overrides).
type ResolveOptions struct {
	Defaults      []Override
	Include       IncludeOptions
	Policy        CompositionPolicy
	Env           EnvBinding
	Explicit      []Override
	RelativePaths []ashlar.Pointer
	Required      []ashlar.Pointer
}

// Resolve loads the YAML include graph starting at entryPath in fsys and runs
// the full resolution pipeline under opts.
func Resolve(ctx context.Context, fsys fs.FS, entryPath string, opts ResolveOptions) (Resolved, error) {
	fragments, err := LoadIncludeGraph(ctx, fsys, entryPath, opts.Include)
	if err != nil {
		return Resolved{}, err
	}
	return ResolveFragments(ctx, fragments, opts)
}

// ResolveFragments layers Defaults, composed YAML fragments, Env bindings, and
// Explicit overrides in precedence order, enforcing RelativePaths and Required
// constraints all-or-nothing.
func ResolveFragments(ctx context.Context, fragments []Fragment, opts ResolveOptions) (Resolved, error) {
	if err := ctx.Err(); err != nil {
		return Resolved{}, ashlar.NewError(ashlar.ErrComposition, ashlar.RootPointer, ashlar.Origin{}, "context canceled", err)
	}
	policy := normalizePolicy(opts.Policy)
	if err := policy.validate(); err != nil {
		return Resolved{}, err
	}
	red := policy.redactor()

	acc, err := applyOverrides(policy, ashlar.OmittedValue(), opts.Defaults)
	if err != nil {
		return Resolved{}, err
	}

	if len(fragments) > 0 {
		composedFiles, err := ComposeFragments(policy, fragments)
		if err != nil {
			return Resolved{}, err
		}
		acc, err = ComposeValues(policy, acc, composedFiles)
		if err != nil {
			return Resolved{}, err
		}
	}

	acc, err = applyEnvLayer(acc, policy, opts.Env)
	if err != nil {
		return Resolved{}, err
	}

	acc, err = applyOverrides(policy, acc, opts.Explicit)
	if err != nil {
		return Resolved{}, err
	}

	if len(opts.RelativePaths) > 0 {
		acc, err = ResolveRelativePaths(acc, ".", opts.RelativePaths...)
		if err != nil {
			return Resolved{}, err
		}
	}

	acc = acc.FreezeSensitivity(red)

	if err := verifyRequiredPointers(acc, opts.Required); err != nil {
		return Resolved{}, err
	}

	files, sum := computeFragmentsHash(fragments)
	return Resolved{
		root:       acc,
		files:      files,
		sourceHash: sum,
	}, nil
}

func normalizePolicy(p CompositionPolicy) CompositionPolicy {
	if p.Redactor == nil {
		p.Redactor = ashlar.DefaultRedactor()
	}
	return p
}

func applyOverrides(policy CompositionPolicy, base ashlar.Value, overrides []Override) (ashlar.Value, error) {
	acc := base
	for _, ov := range overrides {
		tokens, err := ov.Pointer.Tokens()
		if err != nil {
			return ashlar.Value{}, ashlar.NewError(
				ashlar.ErrSyntax,
				ov.Pointer,
				ov.Value.Provenance().Effective,
				"invalid override pointer",
				err,
			)
		}
		stamped := ov.Value
		if orig := stamped.Provenance().Effective; orig.Valid() {
			stamped = stamped.StampLayerOrigin(orig, stamped.Provenance().Sensitive)
		}
		acc, err = applyOverrideAt(policy, acc, ashlar.RootPointer, tokens, stamped)
		if err != nil {
			return ashlar.Value{}, err
		}
	}
	return acc, nil
}

func applyOverrideAt(
	policy CompositionPolicy,
	cur ashlar.Value,
	ptr ashlar.Pointer,
	tokens []string,
	leaf ashlar.Value,
) (ashlar.Value, error) {
	if len(tokens) == 0 {
		if cur.IsOmitted() {
			return leaf, nil
		}
		return composeNode(policy, ptr, cur, leaf)
	}

	// Enforce a MergeForbidDuplicate rule on the way down: an existing
	// ancestor the walk passes through rejects the targeted update. Omitted
	// ancestors (KindInvalid) are being created and have no prior definition,
	// so they are not checked.
	if cur.Kind() == ashlar.KindObject || cur.Kind() == ashlar.KindArray {
		if policy.ruleFor(ptr, cur.Kind()) == MergeForbidDuplicate {
			return ashlar.Value{}, conflictError(
				ptr,
				cur,
				leaf,
				fmt.Sprintf("override beneath %q is forbidden by composition policy (first defined at %s)",
					ptrOrRoot(ptr), cur.Provenance().Effective.Location()),
			)
		}
	}

	head := tokens[0]
	rest := tokens[1:]
	childPtr := ptr.Child(head)

	switch cur.Kind() {
	case ashlar.KindArray:
		return applyOverrideInArray(policy, cur, ptr, head, rest, leaf)
	case ashlar.KindObject:
		return applyOverrideInObject(policy, cur, childPtr, head, rest, leaf)
	case ashlar.KindInvalid:
		child, err := applyOverrideAt(policy, ashlar.OmittedValue(), childPtr, rest, leaf)
		if err != nil {
			return ashlar.Value{}, err
		}
		return ashlar.ObjectValue([]ashlar.Member{{
			Key:       head,
			KeyOrigin: leaf.Provenance().Effective,
			Value:     child,
		}}, leaf.Provenance())
	default:
		return ashlar.Value{}, ashlar.NewError(
			ashlar.ErrComposition,
			childPtr,
			leaf.Provenance().Effective,
			fmt.Sprintf("cannot traverse %q beneath scalar %s at %q", head, cur.Kind(), ptrOrRoot(ptr)),
			nil,
		)
	}
}

func applyOverrideInArray(
	policy CompositionPolicy,
	cur ashlar.Value,
	ptr ashlar.Pointer,
	head string,
	rest []string,
	leaf ashlar.Value,
) (ashlar.Value, error) {
	idx, ok := ashlar.ParseArrayIndex(head)
	if !ok {
		return ashlar.Value{}, ashlar.NewError(
			ashlar.ErrComposition,
			ptr.Child(head),
			leaf.Provenance().Effective,
			fmt.Sprintf("invalid array index %q at %q", head, ptrOrRoot(ptr)),
			nil,
		)
	}
	elems := cur.Elements()
	if idx >= len(elems) {
		return ashlar.Value{}, ashlar.NewError(
			ashlar.ErrComposition,
			ptr.Index(idx),
			leaf.Provenance().Effective,
			fmt.Sprintf("array index %d out of bounds (len %d) at %q", idx, len(elems), ptrOrRoot(ptr)),
			nil,
		)
	}
	updated, err := applyOverrideAt(policy, elems[idx], ptr.Index(idx), rest, leaf)
	if err != nil {
		return ashlar.Value{}, err
	}
	elems[idx] = updated
	return ashlar.ArrayValue(elems, cur.Provenance()), nil
}

func applyOverrideInObject(
	policy CompositionPolicy,
	cur ashlar.Value,
	childPtr ashlar.Pointer,
	head string,
	rest []string,
	leaf ashlar.Value,
) (ashlar.Value, error) {
	members := cur.Members()
	for i, m := range members {
		if m.Key == head {
			updated, err := applyOverrideAt(policy, m.Value, childPtr, rest, leaf)
			if err != nil {
				return ashlar.Value{}, err
			}
			members[i].Value = updated
			if len(rest) == 0 && leaf.Provenance().Effective.Valid() {
				members[i].KeyOrigin = leaf.Provenance().Effective
			}
			return ashlar.ObjectValue(members, cur.Provenance())
		}
	}
	created, err := applyOverrideAt(policy, ashlar.OmittedValue(), childPtr, rest, leaf)
	if err != nil {
		return ashlar.Value{}, err
	}
	members = append(members, ashlar.Member{
		Key:       head,
		KeyOrigin: leaf.Provenance().Effective,
		Value:     created,
	})
	return ashlar.ObjectValue(members, cur.Provenance())
}

func verifyRequiredPointers(root ashlar.Value, required []ashlar.Pointer) error {
	for _, ptr := range required {
		v := root.At(ptr)
		if v.IsOmitted() || v.IsNull() {
			return ashlar.NewError(
				ashlar.ErrUnresolved,
				ptr,
				root.Provenance().Effective,
				fmt.Sprintf("required configuration value at %q is missing or null", ptr),
				nil,
			)
		}
		if s, ok := v.StringVal(); ok && strings.TrimSpace(s) == "" {
			return ashlar.NewError(
				ashlar.ErrUnresolved,
				ptr,
				v.Provenance().Effective,
				fmt.Sprintf("required string value at %q must not be empty", ptr),
				nil,
			)
		}
	}
	return nil
}
