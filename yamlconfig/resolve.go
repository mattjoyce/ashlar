package yamlconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mattjoyce/ashlar"
)

var reEnvInterpolate = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// EnvBinding configures UPPER_SNAKE environment variable binding and optional
// ${VAR} scalar interpolation during configuration resolution.
type EnvBinding struct {
	// Prefix is an optional UPPER_SNAKE prefix (e.g., "APP" -> "APP_SERVICE_PORT").
	Prefix string
	// Bindings maps RFC 6901 Pointers to explicit environment variable names.
	Bindings map[ashlar.Pointer]string
	// Lookup resolves an environment variable by name (defaults to os.LookupEnv).
	Lookup func(string) (string, bool)
	// InterpolateScalar enables ${VAR} expansion on string scalars, failing
	// closed with ashlar.ErrUnresolved when a referenced variable is unset.
	InterpolateScalar bool
}

func (e EnvBinding) lookup(key string) (string, bool) {
	if e.Lookup != nil {
		return e.Lookup(key)
	}
	return os.LookupEnv(key)
}

// Override binds an RFC 6901 Pointer to a single scalar or container Value at a
// declared precedence layer (default or explicit override).
type Override struct {
	Pointer ashlar.Pointer
	Value   ashlar.Value
}

// DefaultSetting constructs an Override at ptr with ashlar.DefaultOrigin(label).
func DefaultSetting(ptr ashlar.Pointer, label string, val ashlar.Value) Override {
	if label == "" {
		label = ptr.LastToken()
	}
	prov := ashlar.NewProvenance(ashlar.DefaultOrigin(label), val.Provenance().Sensitive)
	return Override{Pointer: ptr, Value: val.WithProvenance(prov)}
}

// ExplicitSetting constructs an Override at ptr with ashlar.ExplicitOrigin(label).
func ExplicitSetting(ptr ashlar.Pointer, label string, val ashlar.Value) Override {
	if label == "" {
		label = string(ptr)
	}
	prov := ashlar.NewProvenance(ashlar.ExplicitOrigin(label), val.Provenance().Sensitive)
	return Override{Pointer: ptr, Value: val.WithProvenance(prov)}
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

// Resolved is an immutable, composed runtime configuration value with full
// per-node provenance and deterministic multi-file source hashing.
type Resolved struct {
	root       ashlar.Value
	files      []string
	sourceHash string
	redactor   ashlar.Redactor
}

// Root returns the immutable root ashlar.Value of r.
func (r Resolved) Root() ashlar.Value {
	return r.root
}

// Files returns a defensive copy of the fragment paths included in r.
func (r Resolved) Files() []string {
	if len(r.files) == 0 {
		return nil
	}
	out := make([]string, len(r.files))
	copy(out, r.files)
	return out
}

// SourceHash returns the deterministic SHA-256 digest ("sha256:<hex>") computed
// across all included YAML fragments in r.
func (r Resolved) SourceHash() string {
	return r.sourceHash
}

// DecodeStrict decodes r.Root() into dst, rejecting unknown struct fields with
// source-located ashlar.ErrUnknownField diagnostics.
func (r Resolved) DecodeStrict(dst any) error {
	return DecodeValueStrict(r.root, dst)
}

// Explain formats a human-readable provenance summary for the value at ptr,
// honoring secret redaction.
func (r Resolved) Explain(ptr ashlar.Pointer) string {
	return r.root.At(ptr).Explain(r.redactor, ptr)
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

	if err := verifyRequiredPointers(acc, opts.Required); err != nil {
		return Resolved{}, err
	}

	files, sum := computeFragmentsHash(fragments)
	return Resolved{
		root:       acc,
		files:      files,
		sourceHash: sum,
		redactor:   red,
	}, nil
}

func normalizePolicy(p CompositionPolicy) CompositionPolicy {
	if p.Rules == nil && len(p.PatternRules) == 0 && p.ObjectRule == MergeReplace {
		p.ObjectRule = MergeMap
	}
	if p.Redactor == nil {
		p.Redactor = ashlar.DefaultRedactor()
	}
	return p
}

func applyOverrides(policy CompositionPolicy, base ashlar.Value, overrides []Override) (ashlar.Value, error) {
	acc := base
	for _, ov := range overrides {
		tree, err := buildPointerTree(ov.Pointer, ov.Value)
		if err != nil {
			return ashlar.Value{}, err
		}
		acc, err = ComposeValues(policy, acc, tree)
		if err != nil {
			return ashlar.Value{}, err
		}
	}
	return acc, nil
}

func buildPointerTree(ptr ashlar.Pointer, leaf ashlar.Value) (ashlar.Value, error) {
	tokens, err := ptr.Tokens()
	if err != nil {
		return ashlar.Value{}, ashlar.NewError(ashlar.ErrSyntax, ptr, leaf.Provenance().Effective, "invalid pointer", err)
	}
	cur := leaf
	for i := len(tokens) - 1; i >= 0; i-- {
		tok := tokens[i]
		obj, err := ashlar.ObjectValue([]ashlar.Member{{
			Key:       tok,
			KeyOrigin: leaf.Provenance().Effective,
			Value:     cur,
		}}, leaf.Provenance())
		if err != nil {
			return ashlar.Value{}, err
		}
		cur = obj
	}
	return cur, nil
}

func applyEnvLayer(root ashlar.Value, policy CompositionPolicy, env EnvBinding) (ashlar.Value, error) {
	cur := root
	if env.InterpolateScalar && cur.IsPresent() {
		interpolated, err := interpolateTreeScalars(cur, ashlar.RootPointer, env, policy.redactor())
		if err != nil {
			return ashlar.Value{}, err
		}
		cur = interpolated
	}

	bindings := collectEnvBindings(cur, env)
	keys := make([]string, 0, len(bindings))
	for p := range bindings {
		keys = append(keys, string(p))
	}
	sort.Strings(keys)

	var envOverrides []Override
	for _, rawPtr := range keys {
		ptr := ashlar.Pointer(rawPtr)
		envName := bindings[ptr]
		rawVal, ok := env.lookup(envName)
		if !ok {
			continue
		}
		existing := cur.At(ptr)
		sensitive := policy.redactor().IsSensitive(ptr, ptr.LastToken()) || policy.redactor().IsSensitive(ptr, envName)
		prov := ashlar.NewProvenance(ashlar.EnvOrigin(envName), sensitive)
		parsed, err := coerceEnvScalar(rawVal, existing.Kind(), prov, ptr)
		if err != nil {
			return ashlar.Value{}, err
		}
		envOverrides = append(envOverrides, Override{Pointer: ptr, Value: parsed})
	}
	return applyOverrides(policy, cur, envOverrides)
}

func interpolateTreeScalars(
	val ashlar.Value,
	ptr ashlar.Pointer,
	env EnvBinding,
	red ashlar.Redactor,
) (ashlar.Value, error) {
	switch val.Kind() {
	case ashlar.KindString:
		s, _ := val.StringVal()
		if !strings.Contains(s, "${") {
			return val, nil
		}
		return interpolateSingleString(val, ptr, s, env, red)
	case ashlar.KindObject:
		members := val.Members()
		for i, m := range members {
			updated, err := interpolateTreeScalars(m.Value, ptr.Child(m.Key), env, red)
			if err != nil {
				return ashlar.Value{}, err
			}
			members[i].Value = updated
		}
		return ashlar.ObjectValue(members, val.Provenance())
	case ashlar.KindArray:
		elems := val.Elements()
		for i, el := range elems {
			updated, err := interpolateTreeScalars(el, ptr.Index(i), env, red)
			if err != nil {
				return ashlar.Value{}, err
			}
			elems[i] = updated
		}
		return ashlar.ArrayValue(elems, val.Provenance()), nil
	default:
		return val, nil
	}
}

func interpolateSingleString(
	val ashlar.Value,
	ptr ashlar.Pointer,
	s string,
	env EnvBinding,
	red ashlar.Redactor,
) (ashlar.Value, error) {
	matches := reEnvInterpolate.FindAllStringSubmatch(s, -1)
	var firstVar string
	sensitive := val.Provenance().Sensitive || red.IsSensitive(ptr, ptr.LastToken())
	expanded := s
	for _, m := range matches {
		varName := m[1]
		if firstVar == "" {
			firstVar = varName
		}
		if red.IsSensitive(ptr, varName) {
			sensitive = true
		}
		envVal, ok := env.lookup(varName)
		if !ok || envVal == "" {
			return ashlar.Value{}, ashlar.NewError(
				ashlar.ErrUnresolved,
				ptr,
				val.Provenance().Effective,
				fmt.Sprintf("unresolved environment variable %q in ${%s}", varName, varName),
				nil,
			)
		}
		expanded = strings.ReplaceAll(expanded, m[0], envVal)
	}
	envVal := ashlar.StringValue(expanded, ashlar.NewProvenance(ashlar.EnvOrigin(firstVar), sensitive))
	return val.OverrideWith(envVal, red, ptr), nil
}

func collectEnvBindings(root ashlar.Value, env EnvBinding) map[ashlar.Pointer]string {
	out := make(map[ashlar.Pointer]string)
	for ptr, name := range env.Bindings {
		if name != "" {
			out[ptr] = name
		}
	}
	if strings.TrimSpace(env.Prefix) != "" && root.IsPresent() {
		collectLeafEnvNames(root, ashlar.RootPointer, strings.TrimSpace(env.Prefix), out)
	}
	return out
}

func collectLeafEnvNames(val ashlar.Value, ptr ashlar.Pointer, prefix string, out map[ashlar.Pointer]string) {
	if val.Kind() == ashlar.KindObject {
		for _, m := range val.Members() {
			collectLeafEnvNames(m.Value, ptr.Child(m.Key), prefix, out)
		}
		return
	}
	if ptr == ashlar.RootPointer {
		return
	}
	if _, exists := out[ptr]; exists {
		return
	}
	tokens, err := ptr.Tokens()
	if err != nil || len(tokens) == 0 {
		return
	}
	parts := make([]string, 0, len(tokens)+1)
	parts = append(parts, strings.ToUpper(prefix))
	for _, t := range tokens {
		parts = append(parts, strings.ToUpper(strings.ReplaceAll(t, "-", "_")))
	}
	out[ptr] = strings.Join(parts, "_")
}

func coerceEnvScalar(
	raw string,
	targetKind ashlar.Kind,
	prov ashlar.Provenance,
	ptr ashlar.Pointer,
) (ashlar.Value, error) {
	switch targetKind {
	case ashlar.KindBool:
		b, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return ashlar.Value{}, ashlar.NewError(
				ashlar.ErrValidation,
				ptr,
				prov.Effective,
				"invalid boolean value in environment variable",
				err,
			)
		}
		return ashlar.BoolValue(b, prov), nil
	case ashlar.KindNumber:
		v, err := ashlar.NumberValue(strings.TrimSpace(raw), prov)
		if err != nil {
			return ashlar.Value{}, ashlar.NewError(
				ashlar.ErrValidation,
				ptr,
				prov.Effective,
				"invalid numeric value in environment variable",
				nil,
			)
		}
		return v, nil
	default:
		return ashlar.StringValue(raw, prov), nil
	}
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

func computeFragmentsHash(fragments []Fragment) ([]string, string) {
	if len(fragments) == 0 {
		return nil, ""
	}
	files := make([]string, len(fragments))
	type pair struct {
		path   string
		digest string
	}
	pairs := make([]pair, len(fragments))
	for i, f := range fragments {
		files[i] = f.Path
		sum := sha256.Sum256(f.Raw)
		pairs[i] = pair{path: f.Path, digest: hex.EncodeToString(sum[:])}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].path < pairs[j].path })
	h := sha256.New()
	for _, p := range pairs {
		_, _ = h.Write([]byte(p.path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(p.digest))
		_, _ = h.Write([]byte{'\n'})
	}
	return files, "sha256:" + hex.EncodeToString(h.Sum(nil))
}
