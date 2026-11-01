package ashlar_test

import (
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

// pinKeyRedactor classifies keys containing "pin_code" and the "pins" key. It
// shares no names with DefaultRedactor.
var pinKeyRedactor = ashlar.RedactorFunc(func(_ ashlar.Pointer, key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "pin_code") || k == "pins"
})

// doorKeyRedactor classifies keys containing "door_code". It shares no names
// with DefaultRedactor or pinKeyRedactor.
var doorKeyRedactor = ashlar.RedactorFunc(func(_ ashlar.Pointer, key string) bool {
	return strings.Contains(strings.ToLower(key), "door_code")
})

// TestCombineRedactorsUnionsParts checks that CombineRedactors reports a key as
// sensitive when any one part does, and only then, skipping nil parts.
func TestCombineRedactorsUnionsParts(t *testing.T) {
	t.Parallel()

	p := ashlar.NewPointer
	combined := ashlar.CombineRedactors(ashlar.DefaultRedactor(), nil, pinKeyRedactor, doorKeyRedactor)
	tests := []struct {
		name string
		r    ashlar.Redactor
		ptr  ashlar.Pointer
		want bool
	}{
		{"default half only", combined, p("service", "api_token"), true},
		{"custom half only", combined, p("service", "pin_code"), true},
		{"third part only", combined, p("site", "door_code"), true},
		{"default ancestor", combined, p("credentials", "user"), true},
		{"custom ancestor via IsSensitivePath", combined, p("pins", "front"), true},
		{"list element under custom key", combined, p("doors", "0", "door_code"), true},
		{"no part matches", combined, p("service", "name"), false},
		{"safe suffix stays public", combined, p("service", "token_file"), false},
		{"empty combination", ashlar.CombineRedactors(), p("service", "api_token"), false},
		{"only nil parts", ashlar.CombineRedactors(nil, nil), p("service", "api_token"), false},
		{"single part keeps its rules", ashlar.CombineRedactors(pinKeyRedactor), p("service", "api_token"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ashlar.IsSensitivePath(tt.r, tt.ptr, tt.ptr.LastToken()); got != tt.want {
				t.Errorf("IsSensitivePath(%s) = %v, want %v", tt.ptr, got, tt.want)
			}
		})
	}
}

// TestRedactString checks that RedactString replaces only sensitive values.
func TestRedactString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value     string
		sensitive bool
		want      string
	}{
		{"plain-value", false, "plain-value"},
		{"sekrit-value", true, ashlar.RedactedLiteral},
		{"", true, ashlar.RedactedLiteral},
		{"", false, ""},
	}
	for _, tt := range tests {
		if got := ashlar.RedactString(tt.value, tt.sensitive); got != tt.want {
			t.Errorf("RedactString(%q, %v) = %q, want %q", tt.value, tt.sensitive, got, tt.want)
		}
	}
}

// surfaceRedactor is a redactor configuration and the classes it hides.
type surfaceRedactor struct {
	name        string
	r           ashlar.Redactor
	hideDefault bool
	hideCustom  bool
}

func surfaceRedactors() []surfaceRedactor {
	return []surfaceRedactor{
		{"default_nil", nil, true, false},
		{"default", ashlar.DefaultRedactor(), true, false},
		{"custom_only", pinKeyRedactor, false, true},
		{"combined", ashlar.CombineRedactors(ashlar.DefaultRedactor(), pinKeyRedactor), true, true},
	}
}

// TestValueSurfacesHonorSuppliedRedactor reads a YAML file from disk and runs
// Value.Explain, ExplainAll, FormatProvenanceReport and Sanitize with the
// redactor passed at report time. Secrets under default-only keys, custom-only
// keys, nested maps and list elements are hidden exactly when the redactor
// classifies them, and every other value still appears.
func TestValueSurfacesHonorSuppliedRedactor(t *testing.T) {
	t.Parallel()

	const doc = `service:
  name: surf-public-name
  api_token: surf-default-key
  pin_code: surf-custom-key
database:
  credentials:
    user: surf-default-nested
  pins:
    front: surf-custom-nested
webhooks:
  - url: surf-public-url
    secret: surf-default-list
    pin_code: surf-custom-list
`
	dir := t.TempDir()
	path := filepath.Join(dir, "surfaces.yaml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("WriteFile(surfaces.yaml): %v", err)
	}
	root, err := yamlconfig.ReadFS(os.DirFS(dir), "surfaces.yaml")
	if err != nil {
		t.Fatalf("ReadFS(surfaces.yaml): %v", err)
	}

	p := ashlar.NewPointer
	planted := []struct {
		ptr       ashlar.Pointer
		value     string
		isDefault bool
		isCustom  bool
	}{
		{p("service", "name"), "surf-public-name", false, false},
		{p("webhooks", "0", "url"), "surf-public-url", false, false},
		{p("service", "api_token"), "surf-default-key", true, false},
		{p("service", "pin_code"), "surf-custom-key", false, true},
		{p("database", "credentials", "user"), "surf-default-nested", true, false},
		{p("database", "pins", "front"), "surf-custom-nested", false, true},
		{p("webhooks", "0", "secret"), "surf-default-list", true, false},
		{p("webhooks", "0", "pin_code"), "surf-custom-list", false, true},
	}

	for _, sr := range surfaceRedactors() {
		t.Run(sr.name, func(t *testing.T) {
			t.Parallel()
			var all strings.Builder
			for _, e := range root.ExplainAll(sr.r) {
				all.WriteString(e.Explanation)
				all.WriteByte('\n')
			}
			report := root.FormatProvenanceReport(sr.r)
			sanitized := root.Sanitize(sr.r, false)
			fingerprinted := root.Sanitize(sr.r, true)

			for _, pv := range planted {
				hidden := (pv.isDefault && sr.hideDefault) || (pv.isCustom && sr.hideCustom)
				explain := root.At(pv.ptr).Explain(sr.r, pv.ptr)
				wantSanitized, wantFP := pv.value, pv.value
				if hidden {
					wantSanitized = ashlar.RedactedLiteral
					wantFP = ashlar.SecretFingerprint(string(pv.ptr), pv.value)
				}
				gotSanitized, _ := sanitized.At(pv.ptr).StringVal()
				gotFP, _ := fingerprinted.At(pv.ptr).StringVal()

				for name, text := range map[string]string{
					"Explain":                explain,
					"ExplainAll":             all.String(),
					"FormatProvenanceReport": report,
					"Sanitize(false)":        gotSanitized,
					"Sanitize(true)":         gotFP,
				} {
					if has := strings.Contains(text, pv.value); has == hidden {
						t.Errorf("%s at %s: contains %q = %v, want %v\n%s", name, pv.ptr, pv.value, has, !hidden, text)
					}
				}
				if gotSanitized != wantSanitized {
					t.Errorf("Sanitize(false).At(%s) = %q, want %q", pv.ptr, gotSanitized, wantSanitized)
				}
				if gotFP != wantFP {
					t.Errorf("Sanitize(true).At(%s) = %q, want %q", pv.ptr, gotFP, wantFP)
				}
			}
		})
	}
}

// TestErrorsAggregateRedactsEverySurface joins redacted errors produced under
// DefaultRedactor, a custom redactor, both halves of a CombineRedactors pair,
// and a real sensitive decode failure, then renders the *ashlar.Errors through
// Error() and every fmt verb. No secret may appear, the public details must,
// and Unwrap must expose every joined error to errors.Is and errors.As.
func TestErrorsAggregateRedactsEverySurface(t *testing.T) {
	t.Parallel()

	p := ashlar.NewPointer
	orig := ashlar.FileOrigin(ashlar.SourceSpan{Source: "agg.yaml", Start: ashlar.SourcePos{Line: 3, Column: 5}})
	combined := ashlar.CombineRedactors(ashlar.DefaultRedactor(), pinKeyRedactor)

	type planted struct {
		name      string
		kind      error
		ptr       ashlar.Pointer
		r         ashlar.Redactor
		secret    string
		wantShown bool
	}
	cases := []planted{
		{"default redactor", ashlar.ErrValidation, p("service", "api_token"), ashlar.DefaultRedactor(), "agg-default-sekrit", false},
		{"custom redactor", ashlar.ErrValidation, p("service", "pin_code"), pinKeyRedactor, "agg-custom-sekrit", false},
		{"combined default half", ashlar.ErrComposition, p("db", "password"), combined, "agg-combined-default-sekrit", false},
		{"combined custom half", ashlar.ErrComposition, p("database", "pins", "front"), combined, "agg-combined-custom-sekrit", false},
		{"combined list element", ashlar.ErrValidation, p("webhooks", "0", "secret"), combined, "agg-list-sekrit", false},
		{"custom replaces default", ashlar.ErrValidation, p("service", "api_token"), pinKeyRedactor, "agg-replaced-visible", true},
		{"public field", ashlar.ErrValidation, p("service", "name"), combined, "agg-public-visible", true},
	}

	var errs []error
	for _, c := range cases {
		cause := fmt.Errorf("upstream rejected %q", c.secret)
		errs = append(errs, ashlar.NewRedactedError(c.kind, c.ptr, orig, fmt.Sprintf("bad value %q", c.secret), cause, c.r, c.secret, false))
	}

	// A real decode failure on a value frozen under the combined redactor.
	const decodeSecret = "banana-agg-sekrit"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "decode.yaml"), []byte("pin_code: "+decodeSecret+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(decode.yaml): %v", err)
	}
	val, err := yamlconfig.ReadFS(os.DirFS(dir), "decode.yaml")
	if err != nil {
		t.Fatalf("ReadFS(decode.yaml): %v", err)
	}
	var dst struct {
		PinCode time.Duration `yaml:"pin_code"`
	}
	decodeErr := yamlconfig.DecodeValueStrict(val.FreezeSensitivity(combined), &dst)
	if decodeErr == nil {
		t.Fatal("DecodeValueStrict() error = nil, want duration failure")
	}

	// Join in two levels so JoinErrors must flatten the nested aggregate.
	inner := ashlar.JoinErrors(errs[:3]...)
	agg := ashlar.JoinErrors(append([]error{inner, nil, decodeErr}, errs[3:]...)...)
	var multi *ashlar.Errors
	if !errors.As(agg, &multi) {
		t.Fatalf("JoinErrors() returned %T, want *ashlar.Errors", agg)
	}
	wantCount := len(cases) + 1
	if got := len(multi.Unwrap()); got != wantCount {
		t.Errorf("len(Unwrap()) = %d, want %d", got, wantCount)
	}

	secrets := []string{decodeSecret}
	var visible []string
	for _, c := range cases {
		if c.wantShown {
			visible = append(visible, c.secret)
		} else {
			secrets = append(secrets, c.secret)
		}
	}

	surfaces := []struct {
		name string
		text string
	}{
		{"Error()", agg.Error()},
		{"%v", fmt.Sprintf("%v", agg)},
		{"%+v", fmt.Sprintf("%+v", agg)},
		{"%s", fmt.Sprintf("%s", agg)},
		{"%q", fmt.Sprintf("%q", agg)},
		{"%#v", fmt.Sprintf("%#v", agg)},
	}
	for _, s := range surfaces {
		if !strings.Contains(s.text, fmt.Sprintf("%d errors: ", wantCount)) {
			t.Errorf("%s = %q, want the %d-error prefix", s.name, s.text, wantCount)
		}
		for _, secret := range secrets {
			if strings.Contains(s.text, secret) {
				t.Errorf("%s leaked %q: %s", s.name, secret, s.text)
			}
		}
		for _, v := range visible {
			if !strings.Contains(s.text, v) {
				t.Errorf("%s withholds non-sensitive %q: %s", s.name, v, s.text)
			}
		}
	}

	for _, sentinel := range []error{ashlar.ErrValidation, ashlar.ErrComposition} {
		if !errors.Is(agg, sentinel) {
			t.Errorf("errors.Is(agg, %v) = false, want true", sentinel)
		}
	}
	if errors.Is(agg, ashlar.ErrSyntax) {
		t.Error("errors.Is(agg, ErrSyntax) = true, want false")
	}
	var first *ashlar.Error
	if !errors.As(agg, &first) || first.Pointer != cases[0].ptr {
		t.Errorf("errors.As(agg, *ashlar.Error) = %v, want pointer %s", first, cases[0].ptr)
	}
	for i, e := range multi.Unwrap() {
		var ae *ashlar.Error
		if !errors.As(e, &ae) {
			t.Errorf("Unwrap()[%d] is %T, want *ashlar.Error", i, e)
			continue
		}
		if cause := ae.Cause(); cause != nil {
			for _, secret := range secrets {
				if strings.Contains(cause.Error(), secret) {
					t.Errorf("Unwrap()[%d].Cause() leaked %q: %s", i, secret, cause.Error())
				}
			}
		}
	}
}
