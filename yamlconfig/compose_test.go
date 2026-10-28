package yamlconfig_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

func TestComposeValuesMergeRulesAndProvenance(t *testing.T) {
	t.Parallel()

	baseYAML := `service:
  name: gateway
  port: 8080
  debug: true
routes:
  - from: /v1
tags:
  - alpha
  - beta
plugins:
  echo:
    enabled: true
`
	overlayYAML := `service:
  port: 9090
  debug: false
routes:
  - from: /v2
tags:
  - gamma
plugins:
  metrics:
    enabled: true
`
	vBase, err := yamlconfig.Read("base.yaml", []byte(baseYAML))
	if err != nil {
		t.Fatalf("Read(base.yaml) error = %v", err)
	}
	vOverlay, err := yamlconfig.Read("overlay.yaml", []byte(overlayYAML))
	if err != nil {
		t.Fatalf("Read(overlay.yaml) error = %v", err)
	}

	policy := yamlconfig.DefaultCompositionPolicy()
	policy.Rules[ashlar.NewPointer("routes")] = yamlconfig.MergeAppendSlice
	policy.PatternRules = []yamlconfig.PatternMergeRule{
		{Pattern: "/plugins/*", Rule: yamlconfig.MergeForbidDuplicate},
	}

	composed, err := yamlconfig.ComposeValues(policy, vBase, vOverlay)
	if err != nil {
		t.Fatalf("ComposeValues() unexpected error: %v", err)
	}

	// 1. /service/port overridden: effective = overlay.yaml:2:9 (9090), superseded = base.yaml:3:9 (8080).
	portPtr := ashlar.NewPointer("service", "port")
	portVal := composed.At(portPtr)
	if p, _ := portVal.Int64(); p != 9090 {
		t.Errorf("/service/port = %d, want 9090", p)
	}
	wantPortExplain := "overlay.yaml:2:9 = 9090 (overrode base.yaml:3:9 = 8080)"
	if got := portVal.Explain(nil, portPtr); got != wantPortExplain {
		t.Errorf("port Explain() = %q, want %q", got, wantPortExplain)
	}

	// 2. /service/debug overridden with explicit false -> PresencePresent, bool == false.
	debugVal := composed.At(ashlar.NewPointer("service", "debug"))
	if debugVal.Presence() != ashlar.PresencePresent {
		t.Errorf("/service/debug Presence = %v, want PresencePresent", debugVal.Presence())
	}
	if b, ok := debugVal.Bool(); !ok || b {
		t.Errorf("/service/debug Bool() = (%v, %v), want (false, true)", b, ok)
	}

	// 3. /routes uses MergeAppendSlice -> 2 elements preserving individual origins.
	routesVal := composed.At(ashlar.NewPointer("routes"))
	if routesVal.Len() != 2 {
		t.Fatalf("/routes Len() = %d, want 2", routesVal.Len())
	}
	if loc0 := routesVal.Index(0).At(ashlar.NewPointer("from")).Provenance().Effective.Location(); loc0 != "base.yaml:6:11" {
		t.Errorf("/routes/0/from Location = %q, want base.yaml:6:11", loc0)
	}
	if loc1 := routesVal.Index(1).At(ashlar.NewPointer("from")).Provenance().Effective.Location(); loc1 != "overlay.yaml:5:11" {
		t.Errorf("/routes/1/from Location = %q, want overlay.yaml:5:11", loc1)
	}

	// 4. /tags uses default MergeReplace -> 1 element ("gamma").
	tagsVal := composed.At(ashlar.NewPointer("tags"))
	if tagsVal.Len() != 1 {
		t.Fatalf("/tags Len() = %d, want 1", tagsVal.Len())
	}

	// 5. Disjoint /plugins/* keys ("echo" and "metrics") merge cleanly under /plugins.
	if !composed.At(ashlar.NewPointer("plugins", "echo", "enabled")).IsPresent() ||
		!composed.At(ashlar.NewPointer("plugins", "metrics", "enabled")).IsPresent() {
		t.Error("expected both /plugins/echo and /plugins/metrics to be present")
	}
}

func TestComposeValuesRejectsForbiddenDuplicatesAndKindConflicts(t *testing.T) {
	t.Parallel()

	policy := yamlconfig.DefaultCompositionPolicy()
	policy.PatternRules = []yamlconfig.PatternMergeRule{
		{Pattern: "/plugins/*", Rule: yamlconfig.MergeForbidDuplicate},
	}

	frag1, err := yamlconfig.Read("frag1.yaml", []byte("plugins:\n  echo:\n    enabled: true\n"))
	if err != nil {
		t.Fatalf("Read(frag1) error = %v", err)
	}
	frag2, err := yamlconfig.Read("frag2.yaml", []byte("plugins:\n  echo:\n    enabled: false\n"))
	if err != nil {
		t.Fatalf("Read(frag2) error = %v", err)
	}

	// 1. Duplicate /plugins/echo across fragments fails closed with ErrComposition naming both files.
	_, err = yamlconfig.ComposeValues(policy, frag1, frag2)
	if !errors.Is(err, ashlar.ErrComposition) {
		t.Fatalf("ComposeValues(duplicate /plugins/echo) error = %v, want ErrComposition", err)
	}
	var aErr *ashlar.Error
	if !errors.As(err, &aErr) {
		t.Fatalf("expected *ashlar.Error, got %T", err)
	}
	if got, want := aErr.Pointer.String(), "/plugins/echo"; got != want {
		t.Errorf("Pointer = %q, want %q", got, want)
	}
	if got, want := aErr.Origin.Location(), "frag2.yaml:3:5"; got != want {
		t.Errorf("Origin.Location() = %q, want %q", got, want)
	}
	if !strings.Contains(aErr.Error(), "frag1.yaml:3:5") {
		t.Errorf("error = %q, want first-defined location frag1.yaml:3:5", aErr.Error())
	}

	// 2. Scalar overriding mapping fails closed with ErrComposition naming both locations.
	mapFrag, _ := yamlconfig.Read("map.yaml", []byte("service:\n  name: alpha\n"))
	scalarFrag, _ := yamlconfig.Read("scalar.yaml", []byte("service: not-a-map\n"))
	_, err = yamlconfig.ComposeValues(policy, mapFrag, scalarFrag)
	if !errors.Is(err, ashlar.ErrComposition) {
		t.Fatalf("ComposeValues(kind conflict) error = %v, want ErrComposition", err)
	}
	if !strings.Contains(err.Error(), "map.yaml:2:3") || !strings.Contains(err.Error(), "scalar.yaml:1:10") {
		t.Errorf("kind conflict error = %q, want both conflicting locations", err.Error())
	}
}

func TestCompositionPolicyRejectsUnknownMergeRule(t *testing.T) {
	t.Parallel()

	valA, err := yamlconfig.Read("a.yaml", []byte("x: old\n"))
	if err != nil {
		t.Fatalf("Read(a.yaml) error = %v", err)
	}
	valB, err := yamlconfig.Read("b.yaml", []byte("x: new\n"))
	if err != nil {
		t.Fatalf("Read(b.yaml) error = %v", err)
	}
	fragA := yamlconfig.Fragment{Path: "a.yaml", Root: valA}
	fragB := yamlconfig.Fragment{Path: "b.yaml", Root: valB}

	tests := []struct {
		name         string
		mutate       func(p *yamlconfig.CompositionPolicy)
		composeFrags []yamlconfig.Fragment
		resolveFrags []yamlconfig.Fragment
		wantErr      bool
	}{
		{
			name:         "ObjectRule",
			mutate:       func(p *yamlconfig.CompositionPolicy) { p.ObjectRule = yamlconfig.MergeRule(255) },
			composeFrags: []yamlconfig.Fragment{fragA, fragB},
			resolveFrags: []yamlconfig.Fragment{fragA},
			wantErr:      true,
		},
		{
			name:         "ArrayRule",
			mutate:       func(p *yamlconfig.CompositionPolicy) { p.ArrayRule = yamlconfig.MergeRule(255) },
			composeFrags: []yamlconfig.Fragment{fragA, fragB},
			resolveFrags: []yamlconfig.Fragment{fragA},
			wantErr:      true,
		},
		{
			name:         "ScalarRule",
			mutate:       func(p *yamlconfig.CompositionPolicy) { p.ScalarRule = yamlconfig.MergeRule(255) },
			composeFrags: []yamlconfig.Fragment{fragA, fragB},
			resolveFrags: []yamlconfig.Fragment{fragA},
			wantErr:      true,
		},
		{
			name:         "Rules[/x]",
			mutate:       func(p *yamlconfig.CompositionPolicy) { p.Rules[ashlar.NewPointer("x")] = yamlconfig.MergeRule(255) },
			composeFrags: []yamlconfig.Fragment{fragA, fragB},
			resolveFrags: []yamlconfig.Fragment{fragA},
			wantErr:      true,
		},
		{
			name: "PatternRules[/x/*]",
			mutate: func(p *yamlconfig.CompositionPolicy) {
				p.PatternRules = []yamlconfig.PatternMergeRule{{Pattern: "/x/*", Rule: yamlconfig.MergeRule(255)}}
			},
			composeFrags: []yamlconfig.Fragment{fragA, fragB},
			resolveFrags: []yamlconfig.Fragment{fragA},
			wantErr:      true,
		},
		{
			name: "Rules[/x] MergeForbidDuplicate accepted",
			mutate: func(p *yamlconfig.CompositionPolicy) {
				p.Rules[ashlar.NewPointer("x")] = yamlconfig.MergeForbidDuplicate
			},
			composeFrags: []yamlconfig.Fragment{fragA},
			resolveFrags: []yamlconfig.Fragment{fragA},
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := yamlconfig.DefaultCompositionPolicy()
			tt.mutate(&policy)

			v, err := yamlconfig.ComposeFragments(policy, tt.composeFrags)
			checkUnknownRuleOutcome(t, v, err, tt.wantErr)

			resolved, err := yamlconfig.ResolveFragments(context.Background(), tt.resolveFrags, yamlconfig.ResolveOptions{Policy: policy})
			checkUnknownRuleOutcome(t, resolved.Root(), err, tt.wantErr)
		})
	}
}

func checkUnknownRuleOutcome(t *testing.T, v ashlar.Value, err error, wantErr bool) {
	t.Helper()
	if wantErr {
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ashlar.ErrComposition) {
			t.Errorf("error = %v, want ErrComposition", err)
		}
		if !strings.Contains(err.Error(), "unsupported MergeRule 255") {
			t.Errorf("error = %q, want it to contain %q", err.Error(), "unsupported MergeRule 255")
		}
		if !v.IsOmitted() {
			t.Error("returned value should be omitted on rejection")
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.IsOmitted() {
		t.Error("returned value should be present on success")
	}
}
