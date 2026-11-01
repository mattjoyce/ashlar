package yamlconfig_test

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

// Planted secret values. None of them may appear in any provenance output.
const (
	provSecretA   = "key-from-file-a-5150"
	provSecretB   = "key-from-file-b-6160"
	provSecretEnv = "key-from-env-7170"
)

// fileSpan returns a SourceSpan for source at line:col, as the YAML loader
// records it (no byte offset).
func fileSpan(source string, line, col int) ashlar.SourceSpan {
	return ashlar.SourceSpan{Source: source, Start: ashlar.SourcePos{Line: line, Column: col}}
}

// provChainFixture describes one way of supplying file A and file B.
type provChainFixture struct {
	name string
	// resolve runs the pipeline over files written to dir.
	resolve func(t *testing.T, dir string, opts yamlconfig.ResolveOptions) yamlconfig.Resolved
	// viaB is the include chain of nodes read from b.yaml; viaC of c.yaml.
	viaB []ashlar.SourceSpan
	viaC []ashlar.SourceSpan
}

// provChainFiles returns the a.yaml, b.yaml, and c.yaml sources for the
// override-chain tests. With withIncludes, a.yaml includes b.yaml and b.yaml
// includes c.yaml; without, the include lines become comments so every other
// node keeps the same line and column.
func provChainFiles(withIncludes bool) map[string]string {
	incA, incB := "include:\n  - b.yaml\n", "include:\n  - c.yaml\n"
	if !withIncludes {
		incA, incB = "# file a\n# layered\n", "# file b\n# layered\n"
	}
	return map[string]string{
		"a.yaml": incA + `service:
  name: gateway
  port: 2000
auth:
  api_key: ` + provSecretA + `
`,
		"b.yaml": incB + `service:
  port: 3000
auth:
  api_key: ` + provSecretB + `
`,
		"c.yaml": `service:
  log_level: debug
  tags: []
  labels: {}
`,
	}
}

// provChainFixtures returns the include-graph and layered-file variants of the
// default -> file A -> file B -> env -> explicit chain.
func provChainFixtures() []provChainFixture {
	return []provChainFixture{
		{
			name: "include graph",
			resolve: func(t *testing.T, dir string, opts yamlconfig.ResolveOptions) yamlconfig.Resolved {
				t.Helper()
				writeTestFiles(t, dir, provChainFiles(true))
				r, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "a.yaml", opts)
				if err != nil {
					t.Fatalf("Resolve(a.yaml) error = %v", err)
				}
				return r
			},
			viaB: []ashlar.SourceSpan{fileSpan("a.yaml", 2, 5)},
			viaC: []ashlar.SourceSpan{fileSpan("a.yaml", 2, 5), fileSpan("b.yaml", 2, 5)},
		},
		{
			name: "layered files",
			resolve: func(t *testing.T, dir string, opts yamlconfig.ResolveOptions) yamlconfig.Resolved {
				t.Helper()
				writeTestFiles(t, dir, provChainFiles(false))
				fsys := os.DirFS(dir)
				var frags []yamlconfig.Fragment
				for _, name := range []string{"a.yaml", "b.yaml", "c.yaml"} {
					root, err := yamlconfig.ReadFS(fsys, name)
					if err != nil {
						t.Fatalf("ReadFS(%s) error = %v", name, err)
					}
					frags = append(frags, yamlconfig.Fragment{Path: name, Root: root})
				}
				r, err := yamlconfig.ResolveFragments(context.Background(), frags, opts)
				if err != nil {
					t.Fatalf("ResolveFragments() error = %v", err)
				}
				return r
			},
		},
	}
}

// provChainOptions returns ResolveOptions that put a default under
// /service/port and /service/log_level, bind env with the PROVCHAIN prefix, and
// set an explicit /service/port.
func provChainOptions(t *testing.T) yamlconfig.ResolveOptions {
	t.Helper()
	none := ashlar.Provenance{}
	defaultPort, err := ashlar.NumberValue("1000", none)
	if err != nil {
		t.Fatalf("NumberValue(1000) error = %v", err)
	}
	explicitPort, err := ashlar.NumberValue("5000", none)
	if err != nil {
		t.Fatalf("NumberValue(5000) error = %v", err)
	}
	return yamlconfig.ResolveOptions{
		Defaults: []yamlconfig.Override{
			yamlconfig.DefaultSetting(ashlar.NewPointer("service", "port"), "port", defaultPort),
			yamlconfig.DefaultSetting(ashlar.NewPointer("service", "log_level"), "log_level", ashlar.StringValue("info", none)),
		},
		Env: yamlconfig.EnvBinding{Prefix: "PROVCHAIN"},
		Explicit: []yamlconfig.Override{
			yamlconfig.ExplicitSetting(ashlar.NewPointer("service", "port"), "--port", explicitPort),
		},
	}
}

// setProvChainEnv sets the env layer of the override chain.
func setProvChainEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PROVCHAIN_SERVICE_PORT", "4000")
	t.Setenv("PROVCHAIN_AUTH_API_KEY", provSecretEnv)
}

// TestProvenanceOverrideChainSuperseded checks that Provenance.Superseded
// returns every origin a value overrode, oldest first (default, file A, file
// B, env), with exact file:line:col spans, include chains on origins read
// through an include, and redacted summaries under a sensitive key.
func TestProvenanceOverrideChainSuperseded(t *testing.T) {
	// Not parallel: uses t.Setenv.
	setProvChainEnv(t)

	for _, fx := range provChainFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			resolved := fx.resolve(t, t.TempDir(), provChainOptions(t))
			root := resolved.Root()

			tests := []struct {
				ptr           ashlar.Pointer
				wantEffective ashlar.Origin
				wantSensitive bool
				wantSup       []ashlar.ProvenanceEntry
			}{
				{
					ptr:           ashlar.NewPointer("service", "port"),
					wantEffective: ashlar.ExplicitOrigin("--port"),
					wantSup: []ashlar.ProvenanceEntry{
						{Origin: ashlar.DefaultOrigin("port"), Summary: "1000"},
						{Origin: ashlar.FileOrigin(fileSpan("a.yaml", 5, 9)), Summary: "2000"},
						{Origin: ashlar.FileOrigin(fileSpan("b.yaml", 4, 9)).WithVia(fx.viaB...), Summary: "3000"},
						{Origin: ashlar.EnvOrigin("PROVCHAIN_SERVICE_PORT"), Summary: "4000"},
					},
				},
				{
					ptr:           ashlar.NewPointer("service", "log_level"),
					wantEffective: ashlar.FileOrigin(fileSpan("c.yaml", 2, 14), fx.viaC...),
					wantSup: []ashlar.ProvenanceEntry{
						{Origin: ashlar.DefaultOrigin("log_level"), Summary: `"info"`},
					},
				},
				{
					ptr:           ashlar.NewPointer("service", "name"),
					wantEffective: ashlar.FileOrigin(fileSpan("a.yaml", 4, 9)),
				},
				{
					ptr:           ashlar.NewPointer("auth", "api_key"),
					wantEffective: ashlar.EnvOrigin("PROVCHAIN_AUTH_API_KEY"),
					wantSensitive: true,
					wantSup: []ashlar.ProvenanceEntry{
						{Origin: ashlar.FileOrigin(fileSpan("a.yaml", 7, 12)), Summary: ashlar.RedactedLiteral, Sensitive: true},
						{Origin: ashlar.FileOrigin(fileSpan("b.yaml", 6, 12), fx.viaB...), Summary: ashlar.RedactedLiteral, Sensitive: true},
					},
				},
			}
			for _, tt := range tests {
				t.Run(string(tt.ptr), func(t *testing.T) {
					prov := root.At(tt.ptr).Provenance()
					if !reflect.DeepEqual(prov.Effective, tt.wantEffective) {
						t.Errorf("Effective = %#v, want %#v", prov.Effective, tt.wantEffective)
					}
					if prov.Sensitive != tt.wantSensitive {
						t.Errorf("Sensitive = %v, want %v", prov.Sensitive, tt.wantSensitive)
					}
					got := prov.Superseded()
					if !reflect.DeepEqual(got, tt.wantSup) {
						t.Errorf("Superseded() =\n  %#v\nwant\n  %#v", got, tt.wantSup)
					}
					if len(got) != len(tt.wantSup) {
						return
					}
					for i, e := range got {
						if !reflect.DeepEqual(e.Origin.Via(), tt.wantSup[i].Origin.Via()) {
							t.Errorf("Superseded()[%d].Origin.Via() = %v, want %v", i, e.Origin.Via(), tt.wantSup[i].Origin.Via())
						}
					}
				})
			}
		})
	}
}

// TestProvenanceSupersededReturnsCopy checks that Superseded returns a
// defensive copy: changing the returned slice does not change the Provenance.
func TestProvenanceSupersededReturnsCopy(t *testing.T) {
	t.Parallel()

	first := ashlar.ProvenanceEntry{Origin: ashlar.DefaultOrigin("port"), Summary: "1000"}
	prov := ashlar.NewProvenance(ashlar.ExplicitOrigin("--port"), false, first)

	got := prov.Superseded()
	got[0].Summary = "tampered"
	got[0].Origin = ashlar.EnvOrigin("TAMPERED")

	if again := prov.Superseded(); !reflect.DeepEqual(again, []ashlar.ProvenanceEntry{first}) {
		t.Errorf("Superseded() after mutating a returned copy = %#v, want %#v", again, []ashlar.ProvenanceEntry{first})
	}
	if got := ashlar.NewProvenance(ashlar.DefaultOrigin("x"), false).Superseded(); got != nil {
		t.Errorf("Superseded() with no history = %#v, want nil", got)
	}
}

// TestProvenanceOverrideChainExplain checks Resolved.Explain against exact
// strings for every origin kind and the full override history, most recent
// override first, with include chains and redaction under a sensitive key.
func TestProvenanceOverrideChainExplain(t *testing.T) {
	// Not parallel: uses t.Setenv.
	setProvChainEnv(t)

	for _, fx := range provChainFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			resolved := fx.resolve(t, t.TempDir(), provChainOptions(t))
			viaB, viaC := viaSuffix(fx.viaB), viaSuffix(fx.viaC)

			tests := []struct {
				ptr  ashlar.Pointer
				want string
			}{
				{
					ashlar.NewPointer("service", "port"),
					"explicit:--port = 5000 (overrode env:PROVCHAIN_SERVICE_PORT = 4000, b.yaml:4:9" + viaB +
						" = 3000, a.yaml:5:9 = 2000, default:port = 1000)",
				},
				{
					ashlar.NewPointer("service", "log_level"),
					`c.yaml:2:14` + viaC + ` = "debug" (overrode default:log_level = "info")`,
				},
				{ashlar.NewPointer("service", "name"), `a.yaml:4:9 = "gateway"`},
				{ashlar.NewPointer("service", "tags"), "c.yaml:3:9" + viaC + " = [array len=0]"},
				{ashlar.NewPointer("service", "labels"), "c.yaml:4:11" + viaC + " = {object keys=0}"},
				{
					ashlar.NewPointer("auth", "api_key"),
					"env:PROVCHAIN_AUTH_API_KEY = [REDACTED] (overrode b.yaml:6:12" + viaB +
						" = [REDACTED], a.yaml:7:12 = [REDACTED])",
				},
				{ashlar.NewPointer("service", "missing"), "omitted"},
			}
			for _, tt := range tests {
				t.Run(string(tt.ptr), func(t *testing.T) {
					got := resolved.Explain(tt.ptr)
					if got != tt.want {
						t.Errorf("Explain(%s) =\n  %s\nwant\n  %s", tt.ptr, got, tt.want)
					}
					assertNoProvSecret(t, "Explain", got)
				})
			}
		})
	}
}

// TestProvenanceExplainAllAndReport checks Resolved.ExplainAll against a full
// struct literal and Resolved.FormatProvenanceReport against a golden string
// for the default -> file A -> file B -> env -> explicit chain.
func TestProvenanceExplainAllAndReport(t *testing.T) {
	// Not parallel: uses t.Setenv.
	setProvChainEnv(t)

	for _, fx := range provChainFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			resolved := fx.resolve(t, t.TempDir(), provChainOptions(t))
			viaB, viaC := viaSuffix(fx.viaB), viaSuffix(fx.viaC)

			portExplain := "explicit:--port = 5000 (overrode env:PROVCHAIN_SERVICE_PORT = 4000, b.yaml:4:9" + viaB +
				" = 3000, a.yaml:5:9 = 2000, default:port = 1000)"
			logExplain := `c.yaml:2:14` + viaC + ` = "debug" (overrode default:log_level = "info")`
			nameExplain := `a.yaml:4:9 = "gateway"`
			tagsExplain := "c.yaml:3:9" + viaC + " = [array len=0]"
			labelsExplain := "c.yaml:4:11" + viaC + " = {object keys=0}"
			keyExplain := "env:PROVCHAIN_AUTH_API_KEY = [REDACTED] (overrode b.yaml:6:12" + viaB +
				" = [REDACTED], a.yaml:7:12 = [REDACTED])"

			want := []ashlar.FieldProvenance{
				{
					Pointer: ashlar.NewPointer("service", "port"),
					Provenance: ashlar.NewProvenance(ashlar.ExplicitOrigin("--port"), false,
						ashlar.ProvenanceEntry{Origin: ashlar.DefaultOrigin("port"), Summary: "1000"},
						ashlar.ProvenanceEntry{Origin: ashlar.FileOrigin(fileSpan("a.yaml", 5, 9)), Summary: "2000"},
						ashlar.ProvenanceEntry{Origin: ashlar.FileOrigin(fileSpan("b.yaml", 4, 9), fx.viaB...), Summary: "3000"},
						ashlar.ProvenanceEntry{Origin: ashlar.EnvOrigin("PROVCHAIN_SERVICE_PORT"), Summary: "4000"},
					),
					Explanation: portExplain,
				},
				{
					Pointer: ashlar.NewPointer("service", "log_level"),
					Provenance: ashlar.NewProvenance(ashlar.FileOrigin(fileSpan("c.yaml", 2, 14), fx.viaC...), false,
						ashlar.ProvenanceEntry{Origin: ashlar.DefaultOrigin("log_level"), Summary: `"info"`},
					),
					Explanation: logExplain,
				},
				{
					Pointer:     ashlar.NewPointer("service", "name"),
					Provenance:  ashlar.NewProvenance(ashlar.FileOrigin(fileSpan("a.yaml", 4, 9)), false),
					Explanation: nameExplain,
				},
				{
					Pointer:     ashlar.NewPointer("service", "tags"),
					Provenance:  ashlar.NewProvenance(ashlar.FileOrigin(fileSpan("c.yaml", 3, 9), fx.viaC...), false),
					Explanation: tagsExplain,
				},
				{
					Pointer:     ashlar.NewPointer("service", "labels"),
					Provenance:  ashlar.NewProvenance(ashlar.FileOrigin(fileSpan("c.yaml", 4, 11), fx.viaC...), false),
					Explanation: labelsExplain,
				},
				{
					Pointer: ashlar.NewPointer("auth", "api_key"),
					Provenance: ashlar.NewProvenance(ashlar.EnvOrigin("PROVCHAIN_AUTH_API_KEY"), true,
						ashlar.ProvenanceEntry{Origin: ashlar.FileOrigin(fileSpan("a.yaml", 7, 12)), Summary: ashlar.RedactedLiteral, Sensitive: true},
						ashlar.ProvenanceEntry{Origin: ashlar.FileOrigin(fileSpan("b.yaml", 6, 12), fx.viaB...), Summary: ashlar.RedactedLiteral, Sensitive: true},
					),
					Explanation: keyExplain,
				},
			}

			got := resolved.ExplainAll()
			if len(got) != len(want) {
				t.Fatalf("ExplainAll() returned %d entries, want %d:\n%#v", len(got), len(want), got)
			}
			for i := range want {
				if !reflect.DeepEqual(got[i], want[i]) {
					t.Errorf("ExplainAll()[%d] =\n  %#v\nwant\n  %#v", i, got[i], want[i])
				}
				assertNoProvSecret(t, "ExplainAll", got[i].Explanation)
				for _, e := range got[i].Provenance.Superseded() {
					assertNoProvSecret(t, "ExplainAll Superseded summary", e.Summary)
				}
			}

			wantReport := strings.Join([]string{
				"/service/port: " + portExplain,
				"/service/log_level: " + logExplain,
				"/service/name: " + nameExplain,
				"/service/tags: " + tagsExplain,
				"/service/labels: " + labelsExplain,
				"/auth/api_key: " + keyExplain,
			}, "\n")
			report := resolved.FormatProvenanceReport()
			if report != wantReport {
				t.Errorf("FormatProvenanceReport() =\n%s\nwant\n%s", report, wantReport)
			}
			assertNoProvSecret(t, "FormatProvenanceReport", report)
		})
	}
}

// TestProvenanceIncludeChainUnderSensitiveKey checks that a sensitive value
// read through an include, with no later layer overriding it, reports its
// file:line:col and full include chain while its value stays redacted, in
// Explain, ExplainAll, and FormatProvenanceReport alike.
func TestProvenanceIncludeChainUnderSensitiveKey(t *testing.T) {
	t.Parallel()

	const secret = "nested-secret-value-8180"
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{
		"main.yaml": "include:\n  - conf/mid.yaml\nservice:\n  name: gateway\n",
		"conf/mid.yaml": "include:\n" +
			"  - secrets/leaf.yaml\n",
		"conf/secrets/leaf.yaml": "auth:\n  client_secret: " + secret + "\n  token_list:\n    - " + secret + "\n",
	})
	resolved, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "main.yaml", yamlconfig.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve(main.yaml) error = %v", err)
	}

	wantVia := []ashlar.SourceSpan{fileSpan("main.yaml", 2, 5), fileSpan("conf/mid.yaml", 2, 5)}
	const chain = " (included via main.yaml:2:5 -> conf/mid.yaml:2:5)"

	secretPtr := ashlar.NewPointer("auth", "client_secret")
	listPtr := ashlar.NewPointer("auth", "token_list", "0")
	wantSecretOrigin := ashlar.FileOrigin(fileSpan("conf/secrets/leaf.yaml", 2, 18)).WithVia(wantVia...)
	if got := resolved.Root().At(secretPtr).Provenance().Effective; !reflect.DeepEqual(got, wantSecretOrigin) {
		t.Errorf("client_secret Effective = %#v, want %#v", got, wantSecretOrigin)
	}
	if got := resolved.Root().At(secretPtr).Provenance().Effective.Via(); !reflect.DeepEqual(got, wantVia) {
		t.Errorf("client_secret Origin.Via() = %v, want %v", got, wantVia)
	}

	wantSecretExplain := "conf/secrets/leaf.yaml:2:18" + chain + " = [REDACTED]"
	wantListExplain := "conf/secrets/leaf.yaml:4:7" + chain + " = [REDACTED]"
	if got := resolved.Explain(secretPtr); got != wantSecretExplain {
		t.Errorf("Explain(%s) = %q, want %q", secretPtr, got, wantSecretExplain)
	}
	if got := resolved.Explain(listPtr); got != wantListExplain {
		t.Errorf("Explain(%s) = %q, want %q", listPtr, got, wantListExplain)
	}

	want := []ashlar.FieldProvenance{
		{
			Pointer:     ashlar.NewPointer("service", "name"),
			Provenance:  ashlar.NewProvenance(ashlar.FileOrigin(fileSpan("main.yaml", 4, 9)), false),
			Explanation: `main.yaml:4:9 = "gateway"`,
		},
		{
			Pointer:     secretPtr,
			Provenance:  ashlar.NewProvenance(wantSecretOrigin, true),
			Explanation: wantSecretExplain,
		},
		{
			Pointer:     listPtr,
			Provenance:  ashlar.NewProvenance(ashlar.FileOrigin(fileSpan("conf/secrets/leaf.yaml", 4, 7), wantVia...), true),
			Explanation: wantListExplain,
		},
	}
	if got := resolved.ExplainAll(); !reflect.DeepEqual(got, want) {
		t.Errorf("ExplainAll() =\n  %#v\nwant\n  %#v", got, want)
	}

	wantReport := "/service/name: main.yaml:4:9 = \"gateway\"\n" +
		"/auth/client_secret: " + wantSecretExplain + "\n" +
		"/auth/token_list/0: " + wantListExplain
	report := resolved.FormatProvenanceReport()
	if report != wantReport {
		t.Errorf("FormatProvenanceReport() =\n%s\nwant\n%s", report, wantReport)
	}
	if strings.Contains(report, secret) {
		t.Errorf("FormatProvenanceReport() leaked the secret:\n%s", report)
	}
}

// TestOriginWithVia checks that WithVia attaches an include chain, outermost
// first, that String renders it, that it replaces rather than appends, and
// that the chain is defensively copied on the way in and out.
func TestOriginWithVia(t *testing.T) {
	t.Parallel()

	outer := fileSpan("main.yaml", 2, 5)
	inner := fileSpan("conf/mid.yaml", 3, 7)
	base := ashlar.FileOrigin(fileSpan("leaf.yaml", 1, 9))

	tests := []struct {
		name       string
		origin     ashlar.Origin
		wantVia    []ashlar.SourceSpan
		wantString string
	}{
		{"no chain", base, nil, "leaf.yaml:1:9"},
		{"one hop", base.WithVia(outer), []ashlar.SourceSpan{outer}, "leaf.yaml:1:9 (included via main.yaml:2:5)"},
		{"two hops outermost first", base.WithVia(outer, inner), []ashlar.SourceSpan{outer, inner},
			"leaf.yaml:1:9 (included via main.yaml:2:5 -> conf/mid.yaml:3:7)"},
		{"replaces existing chain", base.WithVia(outer, inner).WithVia(inner), []ashlar.SourceSpan{inner},
			"leaf.yaml:1:9 (included via conf/mid.yaml:3:7)"},
		{"empty call clears chain", base.WithVia(outer).WithVia(), nil, "leaf.yaml:1:9"},
		{"matches FileOrigin variadic", ashlar.FileOrigin(fileSpan("leaf.yaml", 1, 9), outer, inner), []ashlar.SourceSpan{outer, inner},
			"leaf.yaml:1:9 (included via main.yaml:2:5 -> conf/mid.yaml:3:7)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.origin.Via(); !reflect.DeepEqual(got, tt.wantVia) {
				t.Errorf("Via() = %v, want %v", got, tt.wantVia)
			}
			if got := tt.origin.String(); got != tt.wantString {
				t.Errorf("String() = %q, want %q", got, tt.wantString)
			}
			if got := tt.origin.Location(); got != "leaf.yaml:1:9" {
				t.Errorf("Location() = %q, want leaf.yaml:1:9", got)
			}
		})
	}

	in := []ashlar.SourceSpan{outer}
	o := base.WithVia(in...)
	in[0] = inner
	out := o.Via()
	out[0] = inner
	if got := o.Via(); !reflect.DeepEqual(got, []ashlar.SourceSpan{outer}) {
		t.Errorf("Via() after mutating input and output slices = %v, want [%v]", got, outer)
	}
	if got := base.Via(); got != nil {
		t.Errorf("base.Via() after WithVia on a copy = %v, want nil", got)
	}
}

// TestProvenanceReportEdgeShapes checks ExplainAll and FormatProvenanceReport
// on an empty resolution (no fragments, defaults, or overrides) and on a
// document whose root is a scalar, which the report labels "/".
func TestProvenanceReportEdgeShapes(t *testing.T) {
	t.Parallel()

	empty, err := yamlconfig.ResolveFragments(context.Background(), nil, yamlconfig.ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveFragments(nil) error = %v", err)
	}
	if got := empty.ExplainAll(); got != nil {
		t.Errorf("empty ExplainAll() = %#v, want nil", got)
	}
	if got := empty.FormatProvenanceReport(); got != "" {
		t.Errorf("empty FormatProvenanceReport() = %q, want \"\"", got)
	}
	if got := empty.Explain(ashlar.RootPointer); got != "omitted" {
		t.Errorf("empty Explain(\"\") = %q, want \"omitted\"", got)
	}

	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"scalar.yaml": "42\n"})
	scalar, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "scalar.yaml", yamlconfig.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve(scalar.yaml) error = %v", err)
	}
	want := []ashlar.FieldProvenance{{
		Pointer:     ashlar.RootPointer,
		Provenance:  ashlar.NewProvenance(ashlar.FileOrigin(fileSpan("scalar.yaml", 1, 1)), false),
		Explanation: "scalar.yaml:1:1 = 42",
	}}
	if got := scalar.ExplainAll(); !reflect.DeepEqual(got, want) {
		t.Errorf("scalar ExplainAll() = %#v, want %#v", got, want)
	}
	if got, want := scalar.FormatProvenanceReport(), "/: scalar.yaml:1:1 = 42"; got != want {
		t.Errorf("scalar FormatProvenanceReport() = %q, want %q", got, want)
	}
}

// viaSuffix renders an include chain the way Origin.String appends it.
func viaSuffix(via []ashlar.SourceSpan) string {
	if len(via) == 0 {
		return ""
	}
	parts := make([]string, len(via))
	for i, s := range via {
		parts[i] = s.String()
	}
	return " (included via " + strings.Join(parts, " -> ") + ")"
}

// assertNoProvSecret fails the test if text contains any planted secret.
func assertNoProvSecret(t *testing.T, what, text string) {
	t.Helper()
	for _, s := range []string{provSecretA, provSecretB, provSecretEnv} {
		if strings.Contains(text, s) {
			t.Errorf("%s leaked secret %q: %s", what, s, text)
		}
	}
}
