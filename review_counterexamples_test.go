package ashlar_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

type failingSecretText struct{}

func (f *failingSecretText) UnmarshalText(b []byte) error {
	return errors.New("custom text unmarshal failed on prefix: " + string(b[:4]))
}

func TestLuminaryReviewCounterexamples(t *testing.T) {
	t.Parallel()

	t.Run("C1_SensitiveEnvAndDecoderErrorRedaction", func(t *testing.T) {
		t.Parallel()
		const rawSecret = "sensitive-probe-material-9000"
		root, err := yamlconfig.Read("a.yaml", []byte("secret: false\ntoken_ttl: 5s\nclient_secret: ok\n"))
		if err != nil {
			t.Fatal(err)
		}
		// 1. Boolean coercion failure on sensitive env var must never leak rawSecret.
		_, err = yamlconfig.ResolveFragments(
			context.Background(),
			[]yamlconfig.Fragment{{Path: "a.yaml", Root: root}},
			yamlconfig.ResolveOptions{
				Env: yamlconfig.EnvBinding{
					Bindings: map[ashlar.Pointer]string{"/secret": "SECRET"},
					Lookup:   func(string) (string, bool) { return rawSecret, true },
				},
			},
		)
		if err == nil {
			t.Fatal("expected boolean coercion failure")
		}
		if strings.Contains(err.Error(), rawSecret) {
			t.Fatalf("C1 regression: raw secret leaked in env boolean error: %v", err)
		}

		// 2. Duration and TextUnmarshaler failures on sensitive nodes must never leak rawSecret or substrings.
		badSecretRoot, err := yamlconfig.Read("b.yaml", []byte("api_secret: "+rawSecret+"\nclient_secret: "+rawSecret+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		var durTarget struct {
			APISecret    time.Duration `yaml:"api_secret"`
			ClientSecret string        `yaml:"client_secret"`
		}
		err = yamlconfig.DecodeValueStrict(badSecretRoot, &durTarget)
		if err == nil || strings.Contains(err.Error(), rawSecret) {
			t.Fatalf("C1 regression: raw secret leaked in duration decode error: %v", err)
		}
		var textTarget struct {
			APISecret    string            `yaml:"api_secret"`
			ClientSecret failingSecretText `yaml:"client_secret"`
		}
		err = yamlconfig.DecodeValueStrict(badSecretRoot, &textTarget)
		if err == nil || strings.Contains(err.Error(), rawSecret[:4]) {
			t.Fatalf("C1 regression: custom TextUnmarshaler prefix leaked in decode error: %v", err)
		}
	})

	t.Run("C2_S1_ContainerSensitivityInheritanceAndFrozenRedaction", func(t *testing.T) {
		t.Parallel()
		const credSecret = "private-credential-material"
		const boxSecret = "custom-box-secret-material"
		root, err := yamlconfig.Read("config.yaml", []byte("credentials:\n  user: "+credSecret+"\nbox:\n  item: "+boxSecret+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		// Direct Value report on "credentials" parent key must redact child "/credentials/user".
		directReport := root.FormatProvenanceReport(nil)
		if strings.Contains(directReport, credSecret) {
			t.Fatalf("C2 regression: child of sensitive container leaked in FormatProvenanceReport:\n%s", directReport)
		}
		if got := root.At("/credentials/user").Explain(nil, "/credentials/user"); strings.Contains(got, credSecret) {
			t.Fatalf("C2 regression: At(/credentials/user).Explain leaked secret: %s", got)
		}

		// S1: Mutating state captured by RedactorFunc after ResolveFragments must not unredact Resolved.
		redactBox := true
		policy := yamlconfig.DefaultCompositionPolicy()
		policy.Redactor = ashlar.RedactorFunc(func(ptr ashlar.Pointer, _ string) bool {
			return redactBox && ptr == "/box"
		})
		resolved, err := yamlconfig.ResolveFragments(
			context.Background(),
			[]yamlconfig.Fragment{{Path: "config.yaml", Root: root}},
			yamlconfig.ResolveOptions{
				Policy: policy,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		redactBox = false
		if got := resolved.FormatProvenanceReport(); strings.Contains(got, boxSecret) || strings.Contains(got, credSecret) {
			t.Fatalf("S1/C2 regression: mutating caller redactor state after resolution leaked secret:\n%s", got)
		}
		if got := resolved.Explain("/box/item"); strings.Contains(got, boxSecret) {
			t.Fatalf("S1/C2 regression: resolved.Explain(/box/item) leaked secret: %s", got)
		}
		sanitized := resolved.Sanitize(false)
		if s, _ := sanitized.At("/box/item").StringVal(); s != ashlar.RedactedLiteral {
			t.Fatalf("S1/C2 regression: Sanitize(false).At(/box/item) = %q, want %q", s, ashlar.RedactedLiteral)
		}
	})

	t.Run("C3_MergeReplaceRootPolicyPreserved", func(t *testing.T) {
		t.Parallel()
		a, err := yamlconfig.Read("a.yaml", []byte("a: 1\n"))
		if err != nil {
			t.Fatal(err)
		}
		b, err := yamlconfig.Read("b.yaml", []byte("b: 2\n"))
		if err != nil {
			t.Fatal(err)
		}
		policy := yamlconfig.CompositionPolicy{ObjectRule: yamlconfig.MergeReplace}
		direct, err := yamlconfig.ComposeValues(policy, a, b)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := yamlconfig.ResolveFragments(
			context.Background(),
			[]yamlconfig.Fragment{{Path: "a.yaml", Root: a}, {Path: "b.yaml", Root: b}},
			yamlconfig.ResolveOptions{
				Policy: policy,
				Defaults: []yamlconfig.Override{
					yamlconfig.DefaultSetting("/from_default", "d", ashlar.BoolValue(true, ashlar.Provenance{})),
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if direct.Len() != 1 || resolved.Root().Len() != 1 || !resolved.Root().At("/a").IsOmitted() || !resolved.Root().At("/from_default").IsOmitted() {
			t.Fatalf("C3 regression: MergeReplace root policy did not replace earlier object keys: direct=%v resolved=%v", direct.Keys(), resolved.Root().Keys())
		}
	})

	t.Run("C4_ArrayPointerOverrides", func(t *testing.T) {
		t.Parallel()
		root, err := yamlconfig.Read("a.yaml", []byte("servers:\n  - host: old\nitems:\n  - first\n"))
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := yamlconfig.ResolveFragments(
			context.Background(),
			[]yamlconfig.Fragment{{Path: "a.yaml", Root: root}},
			yamlconfig.ResolveOptions{
				Explicit: []yamlconfig.Override{
					yamlconfig.ExplicitSetting("/servers/0/host", "--server-host", ashlar.StringValue("new", ashlar.Provenance{})),
					yamlconfig.ExplicitSetting("/items/0", "--item-0", ashlar.StringValue("updated", ashlar.Provenance{})),
				},
			},
		)
		if err != nil {
			t.Fatalf("C4 regression: array element override failed: %v", err)
		}
		if got, _ := resolved.Root().At("/servers/0/host").StringVal(); got != "new" {
			t.Fatalf("At(/servers/0/host) = %q, want \"new\"", got)
		}
		if got, _ := resolved.Root().At("/items/0").StringVal(); got != "updated" {
			t.Fatalf("At(/items/0) = %q, want \"updated\"", got)
		}
	})

	t.Run("S2_ContainerOverrideOriginPropagation", func(t *testing.T) {
		t.Parallel()
		v, err := ashlar.ObjectValue([]ashlar.Member{
			{Key: "port", Value: ashlar.StringValue("80", ashlar.Provenance{})},
		}, ashlar.Provenance{})
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := yamlconfig.ResolveFragments(
			context.Background(),
			nil,
			yamlconfig.ResolveOptions{
				Defaults: []yamlconfig.Override{
					yamlconfig.DefaultSetting("/service", "app", v),
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		childOrig := resolved.Root().At("/service/port").Provenance().Effective
		if !childOrig.Valid() || childOrig.String() != "default:app" {
			t.Fatalf("S2 regression: child origin = %q (valid=%v), want default:app", childOrig, childOrig.Valid())
		}
	})

	t.Run("S3_RFC6901EscapedPatternRulesAndValidation", func(t *testing.T) {
		t.Parallel()
		a, _ := yamlconfig.Read("a.yaml", []byte("a/b:\n  x: first\n"))
		b, _ := yamlconfig.Read("b.yaml", []byte("a/b:\n  x: second\n"))
		policy := yamlconfig.DefaultCompositionPolicy()
		policy.PatternRules = []yamlconfig.PatternMergeRule{
			{Pattern: "/a~1b/*", Rule: yamlconfig.MergeForbidDuplicate},
		}
		_, err := yamlconfig.ComposeFragments(policy, []yamlconfig.Fragment{
			{Path: "a.yaml", Root: a},
			{Path: "b.yaml", Root: b},
		})
		if !errors.Is(err, ashlar.ErrComposition) {
			t.Fatalf("S3 regression: escaped pattern /a~1b/* did not trigger MergeForbidDuplicate: %v", err)
		}

		badPolicy := yamlconfig.DefaultCompositionPolicy()
		badPolicy.PatternRules = []yamlconfig.PatternMergeRule{
			{Pattern: "missing-leading-slash/*", Rule: yamlconfig.MergeReplace},
		}
		if _, err := yamlconfig.ComposeValues(badPolicy, a, b); !errors.Is(err, ashlar.ErrComposition) {
			t.Fatalf("S3 regression: malformed pattern was not rejected: %v", err)
		}
	})

	t.Run("S4_SinglePassScalarInterpolation", func(t *testing.T) {
		t.Parallel()
		root, err := yamlconfig.Read("config.yaml", []byte("value: '${A}/${B}'\n"))
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := yamlconfig.ResolveFragments(
			context.Background(),
			[]yamlconfig.Fragment{{Path: "config.yaml", Root: root}},
			yamlconfig.ResolveOptions{
				Env: yamlconfig.EnvBinding{
					InterpolateScalar: true,
					Lookup: func(k string) (string, bool) {
						v, ok := map[string]string{"A": "${B}", "B": "end"}[k]
						return v, ok
					},
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := resolved.Root().At("/value").StringVal(); got != "${B}/end" {
			t.Fatalf("S4 regression: expected single-pass interpolation \"${B}/end\", got %q", got)
		}
	})

	t.Run("NestedJSONSliceDecodeLocation", func(t *testing.T) {
		t.Parallel()
		doc, err := jsoncontract.Read("a.json", []byte("{\n  \"items\": [{\"port\": \"wrong\"}]\n}"))
		if err != nil {
			t.Fatal(err)
		}
		var dst struct {
			Items []struct {
				Port int `json:"port"`
			} `json:"items"`
		}
		err = doc.DecodeStrict(&dst)
		var aErr *ashlar.Error
		if !errors.As(err, &aErr) {
			t.Fatalf("DecodeStrict error = %v, want *ashlar.Error", err)
		}
		if aErr.Pointer != "/items/0/port" || aErr.Origin.Location() != "a.json:2:22" {
			t.Fatalf("got pointer=%q location=%q, want (/items/0/port, a.json:2:22)", aErr.Pointer, aErr.Origin.Location())
		}
	})
}
