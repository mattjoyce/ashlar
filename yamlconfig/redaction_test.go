package yamlconfig_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

// secretClass names which redactor recognizes a planted value's key.
type secretClass int

const (
	// classPublic values must stay visible under every redactor.
	classPublic secretClass = iota
	// classDefault values sit under keys only DefaultRedactor classifies.
	classDefault
	// classCustom values sit under keys only pinRedactor classifies.
	classCustom
)

// pinRedactor is a custom redactor that knows nothing of DefaultRedactor's
// names. It classifies any key containing "pin_code" and the "pins" key.
var pinRedactor = ashlar.RedactorFunc(func(_ ashlar.Pointer, key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "pin_code") || k == "pins"
})

// redactorCase is one CompositionPolicy.Redactor configuration and the secret
// classes it is expected to hide.
type redactorCase struct {
	name     string
	redactor ashlar.Redactor
	hides    map[secretClass]bool
}

// redactorCases returns the redactor configurations every surface runs under.
// A supplied redactor replaces DefaultRedactor, so "custom" hides only custom
// keys, while CombineRedactors hides the union of its parts.
func redactorCases() []redactorCase {
	return []redactorCase{
		{name: "default_nil", redactor: nil, hides: map[secretClass]bool{classDefault: true}},
		{name: "default_explicit", redactor: ashlar.DefaultRedactor(), hides: map[secretClass]bool{classDefault: true}},
		{name: "custom_only", redactor: pinRedactor, hides: map[secretClass]bool{classCustom: true}},
		{
			name:     "combined",
			redactor: ashlar.CombineRedactors(ashlar.DefaultRedactor(), pinRedactor),
			hides:    map[secretClass]bool{classDefault: true, classCustom: true},
		},
		{
			name:     "combined_reversed_with_nil",
			redactor: ashlar.CombineRedactors(pinRedactor, nil, ashlar.DefaultRedactor()),
			hides:    map[secretClass]bool{classDefault: true, classCustom: true},
		},
	}
}

// plantedValue is a value written into the fixture at a known location.
// superseded values are overridden later and only survive in provenance history.
type plantedValue struct {
	where      string
	value      string
	ptr        ashlar.Pointer
	class      secretClass
	superseded bool
}

const redactMainYAML = `include:
  - extra.yaml
service:
  name: public-service-name
  api_token: plant-default-key
  pin_code: plant-custom-key
database:
  host: public-db-host
  credentials:
    user: plant-default-nested
  pins:
    front: plant-custom-nested
webhooks:
  - url: public-hook-url
    secret: plant-default-list
    pin_code: plant-custom-list
api_tokens:
  - plant-default-scalar-list
rotation:
  api_token: plant-default-old
  pin_code: plant-custom-old
env_layer:
  region: public-region
  api_token: plant-default-envfile
  pin_code: plant-custom-envfile
`

const redactExtraYAML = `smtp:
  relay: public-smtp-relay
  password: plant-default-include
  pin_code: plant-custom-include
rotation:
  api_token: plant-default-new
  pin_code: plant-custom-new
`

const (
	envDefaultName = "ASHLAR_REDACT_TEST_API_TOKEN"
	envCustomName  = "ASHLAR_REDACT_TEST_PIN_CODE"
)

// redactPlanted lists every value in the fixture, including its env overrides.
func redactPlanted() []plantedValue {
	p := ashlar.NewPointer
	return []plantedValue{
		{"public scalar", "public-service-name", p("service", "name"), classPublic, false},
		{"public nested", "public-db-host", p("database", "host"), classPublic, false},
		{"public list element", "public-hook-url", p("webhooks", "0", "url"), classPublic, false},
		{"public include", "public-smtp-relay", p("smtp", "relay"), classPublic, false},
		{"public env sibling", "public-region", p("env_layer", "region"), classPublic, false},
		{"default key", "plant-default-key", p("service", "api_token"), classDefault, false},
		{"custom key", "plant-custom-key", p("service", "pin_code"), classCustom, false},
		{"default nested map", "plant-default-nested", p("database", "credentials", "user"), classDefault, false},
		{"custom nested map", "plant-custom-nested", p("database", "pins", "front"), classCustom, false},
		{"default list element", "plant-default-list", p("webhooks", "0", "secret"), classDefault, false},
		{"custom list element", "plant-custom-list", p("webhooks", "0", "pin_code"), classCustom, false},
		{"default scalar list", "plant-default-scalar-list", p("api_tokens", "0"), classDefault, false},
		{"default include", "plant-default-include", p("smtp", "password"), classDefault, false},
		{"custom include", "plant-custom-include", p("smtp", "pin_code"), classCustom, false},
		{"default superseded by include", "plant-default-old", p("rotation", "api_token"), classDefault, true},
		{"custom superseded by include", "plant-custom-old", p("rotation", "pin_code"), classCustom, true},
		{"default include override", "plant-default-new", p("rotation", "api_token"), classDefault, false},
		{"custom include override", "plant-custom-new", p("rotation", "pin_code"), classCustom, false},
		{"default superseded by env", "plant-default-envfile", p("env_layer", "api_token"), classDefault, true},
		{"custom superseded by env", "plant-custom-envfile", p("env_layer", "pin_code"), classCustom, true},
		{"default env override", "plant-default-envlive", p("env_layer", "api_token"), classDefault, false},
		{"custom env override", "plant-custom-envlive", p("env_layer", "pin_code"), classCustom, false},
	}
}

// writeRedactFixture writes files into a fresh temp directory and returns it.
func writeRedactFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	return dir
}

// dumpScalars renders every string and number scalar in v, one per line.
func dumpScalars(v ashlar.Value) string {
	var b strings.Builder
	var walk func(ashlar.Value)
	walk = func(v ashlar.Value) {
		switch v.Kind() {
		case ashlar.KindObject:
			for _, m := range v.Members() {
				walk(m.Value)
			}
		case ashlar.KindArray:
			for _, el := range v.Elements() {
				walk(el)
			}
		default:
			if s, ok := v.StringVal(); ok {
				b.WriteString(s)
				b.WriteByte('\n')
			} else if n, ok := v.NumberLexeme(); ok {
				b.WriteString(n)
				b.WriteByte('\n')
			}
		}
	}
	walk(v)
	return b.String()
}

// TestPlantedValuesAreDistinct guards the fixture: no planted value may contain
// another, or a substring match would hide a leak or fake a visible value.
func TestPlantedValuesAreDistinct(t *testing.T) {
	t.Parallel()
	planted := redactPlanted()
	for i, a := range planted {
		for j, b := range planted {
			if i != j && strings.Contains(a.value, b.value) {
				t.Errorf("planted %q contains planted %q", a.value, b.value)
			}
		}
	}
}

// TestResolvedSurfacesRedactPlantedSecrets resolves a fixture with secrets
// planted under default-only and custom-only keys, in nested maps, list
// elements, an included file, and env overrides. Under each redactor it checks
// Explain, ExplainAll (including superseded history), FormatProvenanceReport,
// and Sanitize with and without fingerprints. Hidden values must never appear;
// every other value must still appear, which also proves a supplied redactor
// replaces DefaultRedactor instead of extending it.
func TestResolvedSurfacesRedactPlantedSecrets(t *testing.T) {
	dir := writeRedactFixture(t, map[string]string{
		"main.yaml":  redactMainYAML,
		"extra.yaml": redactExtraYAML,
	})
	t.Setenv(envDefaultName, "plant-default-envlive")
	t.Setenv(envCustomName, "plant-custom-envlive")

	for _, rc := range redactorCases() {
		t.Run(rc.name, func(t *testing.T) {
			policy := yamlconfig.DefaultCompositionPolicy()
			policy.Redactor = rc.redactor
			resolved, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "main.yaml", yamlconfig.ResolveOptions{
				Policy: policy,
				Env: yamlconfig.EnvBinding{Bindings: map[ashlar.Pointer]string{
					ashlar.NewPointer("env_layer", "api_token"): envDefaultName,
					ashlar.NewPointer("env_layer", "pin_code"):  envCustomName,
				}},
			})
			if err != nil {
				t.Fatalf("Resolve(main.yaml) error = %v", err)
			}

			entries := resolved.ExplainAll()
			var explainAll strings.Builder
			var explainEach strings.Builder
			for _, e := range entries {
				explainAll.WriteString(e.Explanation)
				explainAll.WriteByte('\n')
				for _, s := range e.Provenance.Superseded() {
					explainAll.WriteString(s.Summary)
					explainAll.WriteByte('\n')
				}
				explainEach.WriteString(resolved.Explain(e.Pointer))
				explainEach.WriteByte('\n')
			}
			sanitized := resolved.Sanitize(false)
			fingerprinted := resolved.Sanitize(true)

			surfaces := []struct {
				name string
				text string
				// effectiveOnly surfaces carry only effective values, not history.
				effectiveOnly bool
			}{
				{name: "Explain", text: explainEach.String()},
				{name: "ExplainAll", text: explainAll.String()},
				{name: "FormatProvenanceReport", text: resolved.FormatProvenanceReport()},
				{name: "Sanitize(false)", text: dumpScalars(sanitized), effectiveOnly: true},
				{name: "Sanitize(true)", text: dumpScalars(fingerprinted), effectiveOnly: true},
			}

			for _, pv := range redactPlanted() {
				hidden := rc.hides[pv.class]
				for _, s := range surfaces {
					has := strings.Contains(s.text, pv.value)
					switch {
					case hidden && has:
						t.Errorf("%s: %s leaked %s value %q at %s:\n%s", rc.name, s.name, pv.where, pv.value, pv.ptr, s.text)
					case !hidden && !has && (!s.effectiveOnly || !pv.superseded):
						t.Errorf("%s: %s is missing visible %s value %q at %s:\n%s", rc.name, s.name, pv.where, pv.value, pv.ptr, s.text)
					}
				}
				if pv.superseded {
					continue
				}
				explain := resolved.Explain(pv.ptr)
				if got := strings.Contains(explain, ashlar.RedactedLiteral); got != hidden {
					t.Errorf("%s: Explain(%s) = %q, contains %s = %v, want %v", rc.name, pv.ptr, explain, ashlar.RedactedLiteral, got, hidden)
				}
				wantSanitized, wantFP := pv.value, pv.value
				if hidden {
					wantSanitized = ashlar.RedactedLiteral
					wantFP = ashlar.SecretFingerprint(string(pv.ptr), pv.value)
				}
				if got, _ := sanitized.At(pv.ptr).StringVal(); got != wantSanitized {
					t.Errorf("%s: Sanitize(false).At(%s) = %q, want %q", rc.name, pv.ptr, got, wantSanitized)
				}
				if got, _ := fingerprinted.At(pv.ptr).StringVal(); got != wantFP {
					t.Errorf("%s: Sanitize(true).At(%s) = %q, want %q", rc.name, pv.ptr, got, wantFP)
				}
			}
		})
	}
}

// durationTarget is a decode destination whose durations sit under keys of
// each secret class.
type durationTarget struct {
	Timeouts    map[string]time.Duration   `yaml:"timeouts"`
	Credentials map[string]time.Duration   `yaml:"credentials"`
	Pins        map[string]time.Duration   `yaml:"pins"`
	Rotations   []map[string]time.Duration `yaml:"rotations"`
}

// TestDecodeFailureErrorsRedactPlantedSecrets resolves fixtures in which one
// sensitive value fails to decode as a time.Duration, then checks err.Error()
// and every fmt verb on the returned error. The bad value is hidden exactly
// when the active redactor classifies its key; otherwise it must appear so the
// diagnostic stays useful and a supplied redactor is seen to replace
// DefaultRedactor.
func TestDecodeFailureErrorsRedactPlantedSecrets(t *testing.T) {
	type decodeCase struct {
		name   string
		main   string
		extra  string
		envVal string
		ptr    ashlar.Pointer
		secret string
		class  secretClass
	}
	p := ashlar.NewPointer
	cases := []decodeCase{
		{
			name: "public_key", main: "timeouts:\n  connect: banana-public\n",
			ptr: p("timeouts", "connect"), secret: "banana-public", class: classPublic,
		},
		{
			name: "default_key", main: "timeouts:\n  api_token: banana-sekrit\n",
			ptr: p("timeouts", "api_token"), secret: "banana-sekrit", class: classDefault,
		},
		{
			name: "custom_key", main: "timeouts:\n  pin_code: banana-pin-sekrit\n",
			ptr: p("timeouts", "pin_code"), secret: "banana-pin-sekrit", class: classCustom,
		},
		{
			name: "default_nested_map", main: "credentials:\n  ttl: banana-cred-sekrit\n",
			ptr: p("credentials", "ttl"), secret: "banana-cred-sekrit", class: classDefault,
		},
		{
			name: "custom_nested_map", main: "pins:\n  ttl: banana-pins-sekrit\n",
			ptr: p("pins", "ttl"), secret: "banana-pins-sekrit", class: classCustom,
		},
		{
			name: "default_list_element", main: "rotations:\n  - api_token: banana-list-sekrit\n",
			ptr: p("rotations", "0", "api_token"), secret: "banana-list-sekrit", class: classDefault,
		},
		{
			name: "custom_list_element", main: "rotations:\n  - pin_code: banana-listpin-sekrit\n",
			ptr: p("rotations", "0", "pin_code"), secret: "banana-listpin-sekrit", class: classCustom,
		},
		{
			name: "default_include", main: "include: extra.yaml\n", extra: "timeouts:\n  api_token: banana-inc-sekrit\n",
			ptr: p("timeouts", "api_token"), secret: "banana-inc-sekrit", class: classDefault,
		},
		{
			name: "custom_include", main: "include: extra.yaml\n", extra: "timeouts:\n  pin_code: banana-incpin-sekrit\n",
			ptr: p("timeouts", "pin_code"), secret: "banana-incpin-sekrit", class: classCustom,
		},
		{
			name: "default_env", main: "timeouts:\n  api_token: 1s\n", envVal: "banana-env-sekrit",
			ptr: p("timeouts", "api_token"), secret: "banana-env-sekrit", class: classDefault,
		},
		{
			name: "custom_env", main: "timeouts:\n  pin_code: 1s\n", envVal: "banana-envpin-sekrit",
			ptr: p("timeouts", "pin_code"), secret: "banana-envpin-sekrit", class: classCustom,
		},
	}

	const envName = "ASHLAR_REDACT_TEST_DECODE"
	for _, tc := range cases {
		for _, rc := range redactorCases() {
			t.Run(tc.name+"/"+rc.name, func(t *testing.T) {
				files := map[string]string{"main.yaml": tc.main}
				if tc.extra != "" {
					files["extra.yaml"] = tc.extra
				}
				dir := writeRedactFixture(t, files)
				opts := yamlconfig.ResolveOptions{}
				opts.Policy.Redactor = rc.redactor
				if tc.envVal != "" {
					t.Setenv(envName, tc.envVal)
					opts.Env.Bindings = map[ashlar.Pointer]string{tc.ptr: envName}
				}
				resolved, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "main.yaml", opts)
				if err != nil {
					t.Fatalf("Resolve(main.yaml) error = %v", err)
				}

				var dst durationTarget
				err = resolved.DecodeStrict(&dst)
				if err == nil {
					t.Fatalf("DecodeStrict() error = nil, want a duration error at %s", tc.ptr)
				}
				if !errors.Is(err, ashlar.ErrValidation) {
					t.Errorf("errors.Is(err, ErrValidation) = false for %v", err)
				}
				var aerr *ashlar.Error
				if !errors.As(err, &aerr) || aerr.Pointer != tc.ptr {
					t.Errorf("errors.As(*ashlar.Error) pointer mismatch, want %s, err = %v", tc.ptr, err)
				}

				hidden := rc.hides[tc.class]
				for _, s := range errorSurfaces(err) {
					has := strings.Contains(s.text, tc.secret)
					if hidden && has {
						t.Errorf("%s leaked %q: %s", s.name, tc.secret, s.text)
					}
					if !hidden && !has {
						t.Errorf("%s withholds non-sensitive value %q under %s: %s", s.name, tc.secret, rc.name, s.text)
					}
				}
			})
		}
	}
}

// errorSurface is one rendering of an error.
type errorSurface struct {
	name string
	text string
}

// errorSurfaces renders err through Error() and every fmt verb a caller might
// reasonably log it with.
func errorSurfaces(err error) []errorSurface {
	return []errorSurface{
		{"Error()", err.Error()},
		{"%v", fmt.Sprintf("%v", err)},
		{"%+v", fmt.Sprintf("%+v", err)},
		{"%s", fmt.Sprintf("%s", err)},
		{"%q", fmt.Sprintf("%q", err)},
		{"%#v", fmt.Sprintf("%#v", err)},
	}
}
