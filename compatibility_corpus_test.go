package ashlar_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
	"github.com/mattjoyce/ashlar/schema"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

func TestCompatibilityCorpusJSONContractAndSchema(t *testing.T) {
	t.Parallel()

	const facetSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:contract:definition:host_observation",
  "type": "object",
  "additionalProperties": false,
  "required": ["host_id", "observed_at", "seq", "ratio", "scale", "big_counter"],
  "properties": {
    "host_id": { "type": "string", "minLength": 1 },
    "observed_at": { "type": "string", "minLength": 1 },
    "seq": { "type": "integer" },
    "ratio": { "type": "number" },
    "scale": { "type": "number" },
    "big_counter": { "type": "integer" },
    "optional_note": { "type": ["string", "null"] },
    "active": { "type": "boolean" },
    "tags": {
      "type": "array",
      "items": { "type": "string" }
    }
  }
}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.json"), []byte(facetSchemaJSON), 0o600); err != nil {
		t.Fatalf("WriteFile(schema.json) error = %v", err)
	}

	schemaDoc, err := jsoncontract.ReadFS(os.DirFS(dir), "schema.json")
	if err != nil {
		t.Fatalf("ReadFS(schema.json) error = %v", err)
	}
	if err := schema.VerifyClosed(schemaDoc.Root()); err != nil {
		t.Fatalf("VerifyClosed(schemaDoc) error = %v", err)
	}

	bundle := schema.NewBundle()
	if err := bundle.AddDocument("", schemaDoc); err != nil {
		t.Fatalf("AddDocument() error = %v", err)
	}
	validator, err := bundle.Compile("urn:contract:definition:host_observation")
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	const validPayload = `{
  "host_id": "srv-01",
  "observed_at": "2026-10-28T17:00:00Z",
  "seq": 1,
  "ratio": 1.0,
  "scale": 1e0,
  "big_counter": 18446744073709551615,
  "optional_note": null,
  "active": false,
  "tags": []
}`
	payloadDoc, err := jsoncontract.Read("payload.json", []byte(validPayload))
	if err != nil {
		t.Fatalf("Read(payload.json) error = %v", err)
	}
	if !bytes.Equal(payloadDoc.RawBytes(), []byte(validPayload)) {
		t.Fatal("payload RawBytes() did not preserve exact bytes")
	}
	if err := validator.Validate(payloadDoc.Root(), nil); err != nil {
		t.Fatalf("Validate(validPayload) unexpected error: %v", err)
	}

	// Verify exact number lexemes and presence differentiation.
	root := payloadDoc.Root()
	for ptrStr, wantLex := range map[string]string{
		"/seq":         "1",
		"/ratio":       "1.0",
		"/scale":       "1e0",
		"/big_counter": "18446744073709551615",
	} {
		ptr, _ := ashlar.ParsePointer(ptrStr)
		if lex, ok := root.At(ptr).NumberLexeme(); !ok || lex != wantLex {
			t.Errorf("At(%s).NumberLexeme() = (%q, %v), want (%q, true)", ptrStr, lex, ok, wantLex)
		}
	}
	if root.At(ashlar.NewPointer("omitted_field")).Presence() != ashlar.PresenceOmitted {
		t.Error("omitted_field should have PresenceOmitted")
	}
	if root.At(ashlar.NewPointer("optional_note")).Presence() != ashlar.PresenceNull {
		t.Error("optional_note should have PresenceNull")
	}
	if root.At(ashlar.NewPointer("active")).Presence() != ashlar.PresencePresent {
		t.Error("active (false) should have PresencePresent")
	}
	if root.At(ashlar.NewPointer("tags")).Presence() != ashlar.PresencePresent {
		t.Error("tags ([]) should have PresencePresent")
	}

	// Empty required string ("host_id": "") must fail closed with source-located ErrSchema.
	emptyHostDoc, err := jsoncontract.Read("bad_payload.json", []byte(`{
  "host_id": "",
  "observed_at": "2026-10-28T17:00:00Z",
  "seq": 1,
  "ratio": 1.0,
  "scale": 1e0,
  "big_counter": 1
}`))
	if err != nil {
		t.Fatalf("Read(bad_payload.json) error = %v", err)
	}
	err = validator.Validate(emptyHostDoc.Root(), nil)
	if !errors.Is(err, ashlar.ErrSchema) {
		t.Fatalf("Validate(emptyHostDoc) error = %v, want ErrSchema", err)
	}
	var aErr *ashlar.Error
	if errors.As(err, &aErr) {
		if aErr.Pointer.String() != "/host_id" || aErr.Origin.Location() != "bad_payload.json:2:14" {
			t.Errorf("got (%s, %s), want (/host_id, bad_payload.json:2:14)", aErr.Pointer, aErr.Origin.Location())
		}
	}
}

type corpusGatewayConfig struct {
	Service struct {
		Name         string        `yaml:"name"`
		TickInterval time.Duration `yaml:"tick_interval"`
		MaxWorkers   int           `yaml:"max_workers"`
	} `yaml:"service"`
	Routes []struct {
		From string `yaml:"from"`
		To   string `yaml:"to"`
	} `yaml:"routes"`
	Plugins map[string]struct {
		Enabled    bool   `yaml:"enabled"`
		Entrypoint string `yaml:"entrypoint"`
	} `yaml:"plugins"`
	Auth struct {
		APIKey        string `yaml:"api_key"`
		WebhookSecret string `yaml:"webhook_secret"`
	} `yaml:"auth"`
}

func TestCompatibilityCorpusYAMLCompositionSchemaAndProvenance(t *testing.T) {
	// Not parallel: exercises t.Setenv for hermetic environment variable testing.
	const envSigningVal = "live-hmac-signing-val-888"
	const fileAPIVal = "live-gateway-key-val-444"
	t.Setenv("APP_SERVICE_TICK_INTERVAL", "25s")
	t.Setenv("WEBHOOK_SIGNING_VAL", envSigningVal)

	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	files := map[string]string{
		filepath.Join(dir, "config.yaml"): `include:
  - routes.yaml
  - plugins/echo.yaml
  - auth.yaml.enc
service:
  name: core-gateway
  tick_interval: 10s
routes:
  - from: /healthz
    to: internal
`,
		filepath.Join(dir, "routes.yaml"): `routes:
  - from: /v1/events
    to: echo
`,
		filepath.Join(pluginsDir, "echo.yaml"): `plugins:
  echo:
    enabled: true
    entrypoint: bin/echo-plugin
`,
		filepath.Join(dir, "auth.yaml.enc"): "AGE_CIPHERTEXT:\nauth:\n  api_key: " + fileAPIVal + "\n  webhook_secret: \"${WEBHOOK_SIGNING_VAL}\"\n",
	}

	checksums := make(map[string]string, len(files))
	for fullPath, content := range files {
		if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", fullPath, err)
		}
		rel, _ := filepath.Rel(dir, fullPath)
		sum := sha256.Sum256([]byte(content))
		checksums[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
	}

	discoveredPath, _, err := yamlconfig.Discover(
		yamlconfig.Candidate{Path: filepath.Join(dir, "nonexistent.yaml"), Required: false},
		yamlconfig.Candidate{Path: filepath.Join(dir, "config.yaml"), Origin: ashlar.ExplicitOrigin("--config"), Required: true},
	)
	if err != nil || discoveredPath == "" {
		t.Fatalf("Discover() = (%q, %v), expected config.yaml", discoveredPath, err)
	}

	policy := yamlconfig.DefaultCompositionPolicy()
	policy.Rules[ashlar.NewPointer("routes")] = yamlconfig.MergeAppendSlice
	policy.PatternRules = []yamlconfig.PatternMergeRule{
		{Pattern: "/plugins/*", Rule: yamlconfig.MergeForbidDuplicate},
	}

	tickPtr := ashlar.NewPointer("service", "tick_interval")
	workersPtr := ashlar.NewPointer("service", "max_workers")
	entrypointPtr := ashlar.NewPointer("plugins", "echo", "entrypoint")
	defaultWorkers, _ := ashlar.NumberValue("4", ashlar.Provenance{})
	explicitWorkers, _ := ashlar.NumberValue("16", ashlar.Provenance{})

	resolved, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "config.yaml", yamlconfig.ResolveOptions{
		Defaults: []yamlconfig.Override{
			yamlconfig.DefaultSetting(tickPtr, "tick_interval", ashlar.StringValue("5s", ashlar.Provenance{})),
			yamlconfig.DefaultSetting(workersPtr, "max_workers", defaultWorkers),
		},
		Include: yamlconfig.IncludeOptions{
			Verifier: func(_ context.Context, p string, raw []byte) error {
				sum := sha256.Sum256(raw)
				if hex.EncodeToString(sum[:]) != checksums[p] {
					return errors.New("sha256 mismatch")
				}
				return nil
			},
			Transformer: func(_ context.Context, p string, raw []byte) ([]byte, error) {
				if strings.HasSuffix(p, ".enc") {
					return bytes.TrimPrefix(raw, []byte("AGE_CIPHERTEXT:\n")), nil
				}
				return raw, nil
			},
		},
		Policy: policy,
		Env: yamlconfig.EnvBinding{
			Prefix:            "APP",
			InterpolateScalar: true,
		},
		Explicit: []yamlconfig.Override{
			yamlconfig.ExplicitSetting(workersPtr, "--max-workers", explicitWorkers),
		},
		RelativePaths: []ashlar.Pointer{entrypointPtr},
		Required: []ashlar.Pointer{
			ashlar.NewPointer("service", "name"),
			tickPtr,
			workersPtr,
			ashlar.NewPointer("auth", "api_key"),
			ashlar.NewPointer("auth", "webhook_secret"),
		},
	})
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}

	// Validate the resolved YAML tree against a WholeConfig JSON Schema.
	const gatewaySchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.org/gateway.schema.json",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "service": {
      "type": "object",
      "additionalProperties": false,
      "required": ["name", "tick_interval", "max_workers"],
      "properties": {
        "name": { "type": "string", "minLength": 1 },
        "tick_interval": { "type": "string", "minLength": 2 },
        "max_workers": { "type": "integer", "minimum": 1 }
      }
    },
    "routes": {
      "type": "array",
      "minItems": 2,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["from", "to"],
        "properties": {
          "from": { "type": "string" },
          "to": { "type": "string" }
        }
      }
    },
    "plugins": {
      "type": "object",
      "additionalProperties": {
        "type": "object",
        "additionalProperties": false,
        "required": ["enabled", "entrypoint"],
        "properties": {
          "enabled": { "type": "boolean" },
          "entrypoint": { "type": "string" }
        }
      }
    },
    "auth": {
      "type": "object",
      "additionalProperties": false,
      "required": ["api_key", "webhook_secret"],
      "properties": {
        "api_key": { "type": "string", "minLength": 1 },
        "webhook_secret": { "type": "string", "minLength": 1 }
      }
    }
  },
  "$defs": {
    "WholeConfig": {
      "allOf": [
        { "$ref": "#" },
        { "required": ["service", "routes", "plugins", "auth"] }
      ]
    }
  }
}`
	sb := schema.NewBundle()
	if err := sb.AddResource("", []byte(gatewaySchema)); err != nil {
		t.Fatalf("AddResource() error = %v", err)
	}
	wholeValidator, err := sb.Compile("https://example.org/gateway.schema.json#/$defs/WholeConfig")
	if err != nil {
		t.Fatalf("Compile(WholeConfig) error = %v", err)
	}
	if err := wholeValidator.Validate(resolved.Root(), nil); err != nil {
		t.Fatalf("wholeValidator.Validate(resolved) unexpected error: %v", err)
	}

	var decoded corpusGatewayConfig
	if err := resolved.DecodeStrict(&decoded); err != nil {
		t.Fatalf("DecodeStrict() unexpected error: %v", err)
	}
	if decoded.Service.TickInterval != 25*time.Second || decoded.Service.MaxWorkers != 16 || len(decoded.Routes) != 2 {
		t.Errorf("decoded = %+v, unexpected values", decoded)
	}
	if decoded.Plugins["echo"].Entrypoint != "plugins/bin/echo-plugin" {
		t.Errorf("resolved plugin entrypoint = %q, want %q", decoded.Plugins["echo"].Entrypoint, "plugins/bin/echo-plugin")
	}

	// Verify full provenance report and secret sanitization.
	report := resolved.FormatProvenanceReport()
	if strings.Contains(report, envSigningVal) || strings.Contains(report, fileAPIVal) {
		t.Fatalf("secret leaked in FormatProvenanceReport():\n%s", report)
	}
	for _, wantSub := range []string{
		`/service/tick_interval: env:APP_SERVICE_TICK_INTERVAL = "25s" (overrode config.yaml:7:18 = "10s", default:tick_interval = "5s")`,
		`/service/max_workers: explicit:--max-workers = 16 (overrode default:max_workers = 4)`,
		`/auth/api_key: auth.yaml.enc:2:12 (included via config.yaml:4:5) = [REDACTED]`,
		`/auth/webhook_secret: env:WEBHOOK_SIGNING_VAL = [REDACTED] (overrode auth.yaml.enc:3:19 (included via config.yaml:4:5) = [REDACTED])`,
	} {
		if !strings.Contains(report, wantSub) {
			t.Errorf("FormatProvenanceReport() missing %q in:\n%s", wantSub, report)
		}
	}

	sanitized := resolved.Sanitize(true)
	fpVal, _ := sanitized.At(ashlar.NewPointer("auth", "api_key")).StringVal()
	if !strings.HasPrefix(fpVal, "secretfp_sha256:") || fpVal == fileAPIVal {
		t.Errorf("Sanitize(true) /auth/api_key = %q, want secretfp_sha256:<hex> fingerprint", fpVal)
	}
}
