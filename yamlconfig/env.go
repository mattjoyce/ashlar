package yamlconfig

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mattjoyce/ashlar"
)

var reEnvInterpolate = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// EnvBinding configures UPPER_SNAKE environment variable binding and optional
// single-pass ${VAR} scalar interpolation during configuration resolution.
type EnvBinding struct {
	// Prefix is an optional UPPER_SNAKE prefix (e.g., "APP" -> "APP_SERVICE_PORT").
	Prefix string
	// Bindings maps RFC 6901 Pointers to explicit environment variable names.
	Bindings map[ashlar.Pointer]string
	// Lookup resolves an environment variable by name (defaults to os.LookupEnv).
	Lookup func(string) (string, bool)
	// InterpolateScalar enables single-pass ${VAR} expansion on string scalars,
	// failing closed with ashlar.ErrUnresolved when a referenced variable is unset.
	InterpolateScalar bool
}

func (e EnvBinding) lookup(key string) (string, bool) {
	if e.Lookup != nil {
		return e.Lookup(key)
	}
	return os.LookupEnv(key)
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
		sensitive := existing.Provenance().Sensitive ||
			ashlar.IsSensitivePath(policy.redactor(), ptr, ptr.LastToken()) ||
			ashlar.IsSensitivePath(policy.redactor(), ptr, envName)
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
	locs := reEnvInterpolate.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return val, nil
	}
	var firstVar string
	sensitive := val.Provenance().Sensitive || ashlar.IsSensitivePath(red, ptr, ptr.LastToken())
	var b strings.Builder
	lastIdx := 0
	for _, loc := range locs {
		b.WriteString(s[lastIdx:loc[0]])
		varName := s[loc[2]:loc[3]]
		if firstVar == "" {
			firstVar = varName
		}
		if ashlar.IsSensitivePath(red, ptr, varName) {
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
		b.WriteString(envVal)
		lastIdx = loc[1]
	}
	b.WriteString(s[lastIdx:])
	envVal := ashlar.StringValue(b.String(), ashlar.NewProvenance(ashlar.EnvOrigin(firstVar), sensitive))
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
			// Do not attach raw-input-bearing strconv.NumError cause on conversion failure.
			return ashlar.Value{}, ashlar.NewRedactedError(
				ashlar.ErrValidation,
				ptr,
				prov.Effective,
				"invalid boolean value in environment variable",
				nil,
				nil,
				raw,
				prov.Sensitive,
			)
		}
		return ashlar.BoolValue(b, prov), nil
	case ashlar.KindNumber:
		v, err := ashlar.NumberValue(strings.TrimSpace(raw), prov)
		if err != nil {
			return ashlar.Value{}, ashlar.NewRedactedError(
				ashlar.ErrValidation,
				ptr,
				prov.Effective,
				"invalid numeric value in environment variable",
				nil,
				nil,
				raw,
				prov.Sensitive,
			)
		}
		return v, nil
	default:
		return ashlar.StringValue(raw, prov), nil
	}
}
