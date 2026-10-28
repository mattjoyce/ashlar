package yamlconfig_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

type resolvedAppConfig struct {
	Service struct {
		Name         string        `yaml:"name"`
		Port         int           `yaml:"port"`
		TickInterval time.Duration `yaml:"tick_interval"`
	} `yaml:"service"`
	Auth struct {
		WebhookToken string `yaml:"webhook_token"`
		APIKey       string `yaml:"api_key"`
	} `yaml:"auth"`
}

func TestResolvePrecedenceLayersSecretHooksAndExplain(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rootYAML := `include:
  - secrets.yaml.enc
service:
  name: gateway
  port: 8080
  tick_interval: 15s
`
	encryptedAuthYAML := `ENC:auth:
  webhook_token: "${WEBHOOK_HEADER_VAL}"
  api_key: file-raw-val-111
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(rootYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(config.yaml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml.enc"), []byte(encryptedAuthYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(secrets.yaml.enc) error = %v", err)
	}

	const rawWebhookVal = "webhook-live-val-777"
	const rawEnvVal = "env-live-val-222"
	envMap := map[string]string{
		"APP_SERVICE_PORT":   "9090",
		"WEBHOOK_HEADER_VAL": rawWebhookVal,
		"APP_AUTH_API_KEY":   rawEnvVal,
	}

	namePtr := ashlar.NewPointer("service", "name")
	portPtr := ashlar.NewPointer("service", "port")
	tickPtr := ashlar.NewPointer("service", "tick_interval")
	webhookPtr := ashlar.NewPointer("auth", "webhook_token")
	apiKeyPtr := ashlar.NewPointer("auth", "api_key")

	defaultPort, _ := ashlar.NumberValue("8000", ashlar.Provenance{})
	resolved, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "config.yaml", yamlconfig.ResolveOptions{
		Defaults: []yamlconfig.Override{
			yamlconfig.DefaultSetting(portPtr, "port", defaultPort),
			yamlconfig.DefaultSetting(tickPtr, "tick_interval", ashlar.StringValue("5s", ashlar.Provenance{})),
		},
		Include: yamlconfig.IncludeOptions{
			Transformer: func(_ context.Context, p string, raw []byte) ([]byte, error) {
				if strings.HasSuffix(p, ".enc") {
					return bytes.TrimPrefix(raw, []byte("ENC:")), nil
				}
				return raw, nil
			},
		},
		Env: yamlconfig.EnvBinding{
			Prefix:            "APP",
			InterpolateScalar: true,
			Lookup: func(k string) (string, bool) {
				v, ok := envMap[k]
				return v, ok
			},
		},
		Explicit: []yamlconfig.Override{
			yamlconfig.ExplicitSetting(tickPtr, "--tick-interval", ashlar.StringValue("30s", ashlar.Provenance{})),
		},
		Required: []ashlar.Pointer{namePtr, portPtr, tickPtr, webhookPtr, apiKeyPtr},
	})
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}

	// Verify Files() and SourceHash().
	if gotFiles := resolved.Files(); len(gotFiles) != 2 || gotFiles[0] != "config.yaml" || gotFiles[1] != "secrets.yaml.enc" {
		t.Errorf("resolved.Files() = %v, want [config.yaml secrets.yaml.enc]", gotFiles)
	}
	if !strings.HasPrefix(resolved.SourceHash(), "sha256:") {
		t.Errorf("resolved.SourceHash() = %q, want sha256:...", resolved.SourceHash())
	}

	// Verify DecodeStrict into target struct.
	var cfg resolvedAppConfig
	if err := resolved.DecodeStrict(&cfg); err != nil {
		t.Fatalf("DecodeStrict() unexpected error: %v", err)
	}
	if cfg.Service.Port != 9090 || cfg.Service.TickInterval != 30*time.Second || cfg.Auth.WebhookToken != rawWebhookVal || cfg.Auth.APIKey != rawEnvVal {
		t.Errorf("DecodeStrict() = %+v, unexpected field values", cfg)
	}

	// Verify Explain() provenance chains and secret redaction.
	wantPortExplain := "env:APP_SERVICE_PORT = 9090 (overrode config.yaml:5:9 = 8080, default:port = 8000)"
	if got := resolved.Explain(portPtr); got != wantPortExplain {
		t.Errorf("Explain(/service/port) =\n  %s\nwant:\n  %s", got, wantPortExplain)
	}

	wantTickExplain := `explicit:--tick-interval = "30s" (overrode config.yaml:6:18 = "15s", default:tick_interval = "5s")`
	if got := resolved.Explain(tickPtr); got != wantTickExplain {
		t.Errorf("Explain(/service/tick_interval) =\n  %s\nwant:\n  %s", got, wantTickExplain)
	}

	// Sensitive values must NEVER appear in Explain() output.
	for _, ptr := range []ashlar.Pointer{webhookPtr, apiKeyPtr} {
		exp := resolved.Explain(ptr)
		if strings.Contains(exp, rawWebhookVal) || strings.Contains(exp, rawEnvVal) || strings.Contains(exp, "file-raw-val-111") {
			t.Fatalf("sensitive value leaked in Explain(%s): %s", ptr, exp)
		}
		if !strings.Contains(exp, ashlar.RedactedLiteral) {
			t.Errorf("Explain(%s) = %q, expected %s", ptr, exp, ashlar.RedactedLiteral)
		}
	}
}

func TestResolveFailsClosedOnUnresolvedEnvOrMissingRequired(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("service:\n  name: \"${MISSING_ENV_VAR}\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// 1. Unresolved ${MISSING_ENV_VAR} fails closed with ErrUnresolved and file:line:column.
	_, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "config.yaml", yamlconfig.ResolveOptions{
		Env: yamlconfig.EnvBinding{
			InterpolateScalar: true,
			Lookup:            func(string) (string, bool) { return "", false },
		},
	})
	if !errors.Is(err, ashlar.ErrUnresolved) {
		t.Fatalf("Resolve(unresolved ${VAR}) error = %v, want ErrUnresolved", err)
	}
	var aErr *ashlar.Error
	if errors.As(err, &aErr) {
		if got, want := aErr.Origin.Location(), "config.yaml:2:9"; got != want {
			t.Errorf("Origin.Location() = %q, want %q", got, want)
		}
	}

	// 2. Missing Required pointer fails closed with ErrUnresolved.
	if err := os.WriteFile(filepath.Join(dir, "config2.yaml"), []byte("service:\n  name: ok\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err = yamlconfig.Resolve(context.Background(), os.DirFS(dir), "config2.yaml", yamlconfig.ResolveOptions{
		Required: []ashlar.Pointer{ashlar.NewPointer("service", "port")},
	})
	if !errors.Is(err, ashlar.ErrUnresolved) {
		t.Fatalf("Resolve(missing required) error = %v, want ErrUnresolved", err)
	}
}

func TestOverrideArrayIndexGrammar(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("items: [old]\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	build := func(ptr string) yamlconfig.ResolveOptions {
		return yamlconfig.ResolveOptions{
			Explicit: []yamlconfig.Override{
				yamlconfig.ExplicitSetting(ashlar.Pointer(ptr), "--override", ashlar.StringValue("new", ashlar.Provenance{})),
			},
		}
	}

	// /items/0 succeeds.
	_, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "a.yaml", build("/items/0"))
	if err != nil {
		t.Errorf("/items/0 should succeed, got: %v", err)
	}

	rows := []string{"/items/+0", "/items/-0", "/items/00", "/items/x"}
	for _, ptr := range rows {
		_, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "a.yaml", build(ptr))
		if !errors.Is(err, ashlar.ErrComposition) {
			t.Errorf("%s error = %v, want ErrComposition", ptr, err)
		}
		if !strings.Contains(err.Error(), "invalid array index") {
			t.Errorf("%s message = %q, want 'invalid array index'", ptr, err.Error())
		}
	}

	// /items/1 fails with out of bounds.
	_, err = yamlconfig.Resolve(context.Background(), os.DirFS(dir), "a.yaml", build("/items/1"))
	if !errors.Is(err, ashlar.ErrComposition) {
		t.Errorf("/items/1 error = %v, want ErrComposition", err)
	}
	if !strings.Contains(err.Error(), "out of bounds") {
		t.Errorf("/items/1 message = %q, want 'out of bounds'", err.Error())
	}
}

func TestParseDotEnv(t *testing.T) {
	t.Parallel()

	raw := `# Comment line
export SERVICE_HOST="127.0.0.1"
SERVICE_PORT='8080'
PLAIN_VAR=hello
`
	vars, err := yamlconfig.ParseDotEnv(".env", []byte(raw))
	if err != nil {
		t.Fatalf("ParseDotEnv() unexpected error: %v", err)
	}
	if vars["SERVICE_HOST"] != "127.0.0.1" || vars["SERVICE_PORT"] != "8080" || vars["PLAIN_VAR"] != "hello" {
		t.Errorf("ParseDotEnv() = %v, unexpected values", vars)
	}

	// Malformed line without '=' fails with line-located ErrSyntax.
	_, err = yamlconfig.ParseDotEnv(".env", []byte("VALID=1\nBROKEN_LINE\n"))
	if !errors.Is(err, ashlar.ErrSyntax) {
		t.Fatalf("ParseDotEnv(broken) error = %v, want ErrSyntax", err)
	}
	var aErr *ashlar.Error
	if errors.As(err, &aErr) {
		if got, want := aErr.Origin.Location(), ".env:2:1"; got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	}
}

func TestOverrideAncestorPolicy(t *testing.T) {
	t.Parallel()

	const overrideValue = "override-value"

	forbid := func(ptr ashlar.Pointer) yamlconfig.CompositionPolicy {
		policy := yamlconfig.DefaultCompositionPolicy()
		policy.Rules[ptr] = yamlconfig.MergeForbidDuplicate
		return policy
	}
	explicit := func(ptr ashlar.Pointer) yamlconfig.Override {
		return yamlconfig.ExplicitSetting(ptr, "override", ashlar.StringValue(overrideValue, ashlar.Provenance{}))
	}
	scalar := func(at ashlar.Pointer) func(t *testing.T, r yamlconfig.Resolved) {
		return func(t *testing.T, r yamlconfig.Resolved) {
			t.Helper()
			if got, _ := r.Root().At(at).StringVal(); got != overrideValue {
				t.Errorf("At(%s) = %q, want %q", at, got, overrideValue)
			}
		}
	}

	tests := []struct {
		name    string
		fragYML string
		policy  yamlconfig.CompositionPolicy
		opts    yamlconfig.ResolveOptions
		reject  bool
		check   func(t *testing.T, r yamlconfig.Resolved)
	}{
		{
			name:    "explicit under forbid",
			fragYML: "locked:\n  x: old\n",
			policy:  forbid(ashlar.NewPointer("locked")),
			opts: yamlconfig.ResolveOptions{
				Explicit: []yamlconfig.Override{explicit(ashlar.NewPointer("locked", "x"))},
			},
			reject: true,
		},
		{
			name:    "env under forbid",
			fragYML: "locked:\n  x: old\n",
			policy:  forbid(ashlar.NewPointer("locked")),
			opts: yamlconfig.ResolveOptions{
				Env: yamlconfig.EnvBinding{
					Bindings: map[ashlar.Pointer]string{ashlar.NewPointer("locked", "x"): "LOCKED_X"},
					Lookup:   func(string) (string, bool) { return overrideValue, true },
				},
			},
			reject: true,
		},
		{
			name:    "deep under forbid",
			fragYML: "locked:\n  inner:\n    y: old\n",
			policy:  forbid(ashlar.NewPointer("locked")),
			opts: yamlconfig.ResolveOptions{
				Explicit: []yamlconfig.Override{explicit(ashlar.NewPointer("locked", "inner", "y"))},
			},
			reject: true,
		},
		{
			name:    "pattern forbid",
			fragYML: "plugins:\n  a:\n    x: old\n",
			policy: func() yamlconfig.CompositionPolicy {
				policy := yamlconfig.DefaultCompositionPolicy()
				policy.PatternRules = []yamlconfig.PatternMergeRule{
					{Pattern: "/plugins/*", Rule: yamlconfig.MergeForbidDuplicate},
				}
				return policy
			}(),
			opts: yamlconfig.ResolveOptions{
				Explicit: []yamlconfig.Override{explicit(ashlar.NewPointer("plugins", "a", "x"))},
			},
			reject: true,
		},
		{
			name:    "array forbid",
			fragYML: "items:\n  - x: old\n",
			policy:  forbid(ashlar.NewPointer("items")),
			opts: yamlconfig.ResolveOptions{
				Explicit: []yamlconfig.Override{explicit(ashlar.NewPointer("items", "0", "x"))},
			},
			reject: true,
		},
		{
			name:   "two defaults under forbid",
			policy: forbid(ashlar.NewPointer("locked")),
			opts: yamlconfig.ResolveOptions{
				Defaults: []yamlconfig.Override{
					yamlconfig.DefaultSetting(ashlar.NewPointer("locked", "a"), "a", ashlar.StringValue(overrideValue, ashlar.Provenance{})),
					yamlconfig.DefaultSetting(ashlar.NewPointer("locked", "b"), "b", ashlar.StringValue(overrideValue, ashlar.Provenance{})),
				},
			},
			reject: true,
		},
		{
			name:   "absent ancestor created",
			policy: forbid(ashlar.NewPointer("locked")),
			opts: yamlconfig.ResolveOptions{
				Explicit: []yamlconfig.Override{explicit(ashlar.NewPointer("locked", "x"))},
			},
			check: scalar(ashlar.NewPointer("locked", "x")),
		},
		{
			name:    "replace keeps siblings",
			fragYML: "service:\n  host: old\n  port: 80\n",
			policy: func() yamlconfig.CompositionPolicy {
				policy := yamlconfig.DefaultCompositionPolicy()
				policy.Rules[ashlar.NewPointer("service")] = yamlconfig.MergeReplace
				return policy
			}(),
			opts: yamlconfig.ResolveOptions{
				Explicit: []yamlconfig.Override{explicit(ashlar.NewPointer("service", "host"))},
			},
			check: func(t *testing.T, r yamlconfig.Resolved) {
				t.Helper()
				scalar(ashlar.NewPointer("service", "host"))(t, r)
				if got, _ := r.Root().At(ashlar.NewPointer("service", "port")).Int64(); got != 80 {
					t.Errorf("At(/service/port) = %d, want 80", got)
				}
			},
		},
		{
			name:    "map keeps siblings",
			fragYML: "service:\n  host: old\n  port: 80\n",
			policy:  yamlconfig.DefaultCompositionPolicy(),
			opts: yamlconfig.ResolveOptions{
				Explicit: []yamlconfig.Override{explicit(ashlar.NewPointer("service", "host"))},
			},
			check: func(t *testing.T, r yamlconfig.Resolved) {
				t.Helper()
				scalar(ashlar.NewPointer("service", "host"))(t, r)
				if got, _ := r.Root().At(ashlar.NewPointer("service", "port")).Int64(); got != 80 {
					t.Errorf("At(/service/port) = %d, want 80", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var fragments []yamlconfig.Fragment
			if tt.fragYML != "" {
				root, err := yamlconfig.Read(tt.name+".yaml", []byte(tt.fragYML))
				if err != nil {
					t.Fatalf("Read(%s) error = %v", tt.name+".yaml", err)
				}
				fragments = []yamlconfig.Fragment{{Path: tt.name + ".yaml", Root: root}}
			}

			opts := tt.opts
			opts.Policy = tt.policy
			resolved, err := yamlconfig.ResolveFragments(context.Background(), fragments, opts)
			if tt.reject {
				if !errors.Is(err, ashlar.ErrComposition) {
					t.Fatalf("ResolveFragments() error = %v, want ErrComposition", err)
				}
				if strings.Contains(err.Error(), overrideValue) {
					t.Fatalf("ResolveFragments() error leaked override value %q: %v", overrideValue, err)
				}
				if !resolved.Root().IsOmitted() {
					t.Fatalf("resolved.Root() = %v, want omitted on rejection", resolved.Root().Summary(ashlar.DefaultRedactor(), ashlar.RootPointer))
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveFragments() unexpected error: %v", err)
			}
			tt.check(t, resolved)
		})
	}
}
