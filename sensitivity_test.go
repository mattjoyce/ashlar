package ashlar_test

import (
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

func TestSensitivityRuleAgreesAcrossPaths(t *testing.T) {
	t.Parallel()

	policyYAML := `token_file: /etc/tok
credentials:
  user: alice-sekrit
  token_file: /etc/cred
api:
  auth:
    tokens:
      - secret_ref: vault/ref-1
        token: tok-sekrit
secrets:
  age_key_file: /etc/age
service:
  tick_interval: 5s
`
	policy, err := yamlconfig.Read("p.yaml", []byte(policyYAML))
	if err != nil {
		t.Fatalf("yamlconfig.Read(p.yaml): %v", err)
	}

	explicit, _ := ashlar.ObjectValue([]ashlar.Member{
		{Key: "token_file", Value: ashlar.StringValue("/etc/explicit", ashlar.Provenance{})},
	}, ashlar.NewProvenance(ashlar.ExplicitOrigin("cli"), true))

	type row struct {
		tree      ashlar.Value
		ptr       ashlar.Pointer
		sensitive bool
	}

	cases := []row{
		{policy, ashlar.NewPointer("token_file"), false},
		{policy, ashlar.NewPointer("credentials", "user"), true},
		{policy, ashlar.NewPointer("credentials", "token_file"), false},
		{policy, ashlar.NewPointer("api", "auth", "tokens", "0", "secret_ref"), false},
		{policy, ashlar.NewPointer("api", "auth", "tokens", "0", "token"), true},
		{policy, ashlar.NewPointer("secrets", "age_key_file"), false},
		{policy, ashlar.NewPointer("service", "tick_interval"), false},
		{explicit, ashlar.NewPointer("token_file"), true},
	}

	for _, tc := range cases {
		ptrStr := string(tc.ptr)

		// 1. FormatProvenanceReport(nil) line for pointer contains RedactedLiteral iff sensitive.
		report := tc.tree.FormatProvenanceReport(nil)
		reportHasRedacted := false
		for _, line := range strings.Split(report, "\n") {
			if len(line) > len(ptrStr)+2 && line[:len(ptrStr)+2] == ptrStr+": " && strings.Contains(line, ashlar.RedactedLiteral) {
				reportHasRedacted = true
				break
			}
		}
		if reportHasRedacted != tc.sensitive {
			t.Errorf("FormatProvenanceReport(nil) pointer %s: hasRedacted=%v, sensitive=%v\nreport:\n%s", tc.ptr, reportHasRedacted, tc.sensitive, report)
		}

		// 2. At(ptr).Explain(nil, ptr) contains RedactedLiteral iff sensitive.
		explain := tc.tree.At(tc.ptr).Explain(nil, tc.ptr)
		explainHasRedacted := strings.Contains(explain, ashlar.RedactedLiteral)
		if explainHasRedacted != tc.sensitive {
			t.Errorf("At(%s).Explain(nil, %s) = %q, sensitive=%v", tc.ptr, tc.ptr, explain, tc.sensitive)
		}

		// 3. FreezeSensitivity(nil).At(ptr).Provenance().Sensitive == sensitive.
		frozen := tc.tree.FreezeSensitivity(nil)
		frozenSens := frozen.At(tc.ptr).Provenance().Sensitive
		if frozenSens != tc.sensitive {
			t.Errorf("FreezeSensitivity(nil).At(%s).Provenance().Sensitive = %v, want %v", tc.ptr, frozenSens, tc.sensitive)
		}

		// 4. FreezeSensitivity(nil).FormatProvenanceReport(nil) line for ptr contains RedactedLiteral iff sensitive.
		frozenReport := frozen.FormatProvenanceReport(nil)
		frozenReportHasRedacted := false
		for _, line := range strings.Split(frozenReport, "\n") {
			if len(line) > len(ptrStr)+2 && line[:len(ptrStr)+2] == ptrStr+": " && strings.Contains(line, ashlar.RedactedLiteral) {
				frozenReportHasRedacted = true
				break
			}
		}
		if frozenReportHasRedacted != tc.sensitive {
			t.Errorf("FreezeSensitivity(nil).FormatProvenanceReport(nil) pointer %s: hasRedacted=%v, sensitive=%v\nreport:\n%s", tc.ptr, frozenReportHasRedacted, tc.sensitive, frozenReport)
		}

		// 5. Sanitize(nil, false).At(ptr).StringVal() == RedactedLiteral iff sensitive.
		sanitized := tc.tree.Sanitize(nil, false)
		if s, ok := sanitized.At(tc.ptr).StringVal(); ok {
			isRedacted := s == ashlar.RedactedLiteral
			if isRedacted != tc.sensitive {
				t.Errorf("Sanitize(nil, false).At(%s).StringVal() = %q, sensitive=%v", tc.ptr, s, tc.sensitive)
			}
		}
	}
}
