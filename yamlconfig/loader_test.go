package yamlconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

func TestReadYAMLPreservesPresenceAndProvenance(t *testing.T) {
	t.Parallel()

	const rawYAML = `service:
  name: gateway
  tick_interval: 10s
  explicit_false: false
  explicit_zero: 0
  explicit_empty_str: ""
  explicit_empty_seq: []
  explicit_null: null
`
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(rawYAML), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	viaSpan := ashlar.SourceSpan{
		Source: "root.yaml",
		Start:  ashlar.SourcePos{Line: 2, Column: 3},
	}
	root, err := yamlconfig.ReadFS(os.DirFS(dir), "config.yaml", viaSpan)
	if err != nil {
		t.Fatalf("ReadFS() unexpected error: %v", err)
	}

	// Verify Presence differentiation.
	if got := root.At(ashlar.NewPointer("service", "missing")).Presence(); got != ashlar.PresenceOmitted {
		t.Errorf("missing Presence = %v, want PresenceOmitted", got)
	}
	if got := root.At(ashlar.NewPointer("service", "explicit_null")).Presence(); got != ashlar.PresenceNull {
		t.Errorf("explicit_null Presence = %v, want PresenceNull", got)
	}
	for _, key := range []string{"explicit_false", "explicit_zero", "explicit_empty_str", "explicit_empty_seq"} {
		ptr := ashlar.NewPointer("service", key)
		if got := root.At(ptr).Presence(); got != ashlar.PresencePresent {
			t.Errorf("%s Presence = %v, want PresencePresent", ptr, got)
		}
	}

	// Verify line/column and via chain on /service/tick_interval.
	tickVal := root.At(ashlar.NewPointer("service", "tick_interval"))
	if got, want := tickVal.Provenance().Effective.Location(), "config.yaml:3:18"; got != want {
		t.Errorf("tick_interval Location() = %q, want %q", got, want)
	}
	if got, want := tickVal.Provenance().Effective.String(), "config.yaml:3:18 (included via root.yaml:2:3)"; got != want {
		t.Errorf("tick_interval Origin.String() = %q, want %q", got, want)
	}
}

func TestReadYAMLRejectsDuplicateKeysTrailingDocsAndAliases(t *testing.T) {
	t.Parallel()

	// 1. Duplicate mapping key fails closed with ErrDuplicateKey and both locations.
	dupYAML := `service:
  name: alpha
  port: 8080
  name: beta
`
	_, err := yamlconfig.Read("config.yaml", []byte(dupYAML))
	if !errors.Is(err, ashlar.ErrDuplicateKey) {
		t.Fatalf("Read(dupYAML) error = %v, want ErrDuplicateKey", err)
	}
	var aErr *ashlar.Error
	if !errors.As(err, &aErr) {
		t.Fatalf("expected *ashlar.Error, got %T", err)
	}
	if got, want := aErr.Origin.Location(), "config.yaml:4:3"; got != want {
		t.Errorf("dup Location = %q, want %q", got, want)
	}
	if !strings.Contains(aErr.Error(), "config.yaml:2:3") {
		t.Errorf("dup error = %q, want mention of first defined location config.yaml:2:3", aErr.Error())
	}

	// 2. Multi-document YAML stream ("---") fails closed with ErrTrailingContent.
	multiDocYAML := `service:
  name: alpha
---
service:
  name: beta
`
	_, err = yamlconfig.Read("config.yaml", []byte(multiDocYAML))
	if !errors.Is(err, ashlar.ErrTrailingContent) {
		t.Fatalf("Read(multiDocYAML) error = %v, want ErrTrailingContent", err)
	}
	if errors.As(err, &aErr) {
		if got, want := aErr.Origin.Location(), "config.yaml:3:1"; got != want {
			t.Errorf("trailing doc Location = %q, want %q", got, want)
		}
	}

	// 3. YAML alias fails closed with ErrSyntax.
	aliasYAML := `defaults: &def
  timeout: 5s
service: *def
`
	if _, err := yamlconfig.Read("config.yaml", []byte(aliasYAML)); !errors.Is(err, ashlar.ErrSyntax) {
		t.Fatalf("Read(aliasYAML) error = %v, want ErrSyntax", err)
	}
}

type sampleYAMLConfig struct {
	Service struct {
		Name         string        `yaml:"name"`
		TickInterval time.Duration `yaml:"tick_interval"`
		Port         int           `yaml:"port"`
	} `yaml:"service"`
	Labels map[string]string `yaml:"labels"`
}

func TestDecodeValueStrict(t *testing.T) {
	t.Parallel()

	validYAML := `service:
  name: worker
  tick_interval: 15s
  port: 9090
labels:
  env: prod
`
	val, err := yamlconfig.Read("config.yaml", []byte(validYAML))
	if err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}
	var cfg sampleYAMLConfig
	if err := yamlconfig.DecodeValueStrict(val, &cfg); err != nil {
		t.Fatalf("DecodeValueStrict() unexpected error: %v", err)
	}
	if cfg.Service.Name != "worker" || cfg.Service.TickInterval != 15*time.Second || cfg.Service.Port != 9090 || cfg.Labels["env"] != "prod" {
		t.Errorf("DecodeValueStrict() = %+v, unexpected field values", cfg)
	}

	// Unknown field in nested struct must fail with ErrUnknownField, Pointer, and file:line:column.
	unknownYAML := `service:
  name: worker
  tick_interval: 15s
  rogue_setting: true
`
	uVal, err := yamlconfig.Read("config.yaml", []byte(unknownYAML))
	if err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}
	err = yamlconfig.DecodeValueStrict(uVal, &cfg)
	if !errors.Is(err, ashlar.ErrUnknownField) {
		t.Fatalf("DecodeValueStrict(unknown) error = %v, want ErrUnknownField", err)
	}
	var aErr *ashlar.Error
	if errors.As(err, &aErr) {
		if got, want := aErr.Pointer.String(), "/service/rogue_setting"; got != want {
			t.Errorf("Pointer = %q, want %q", got, want)
		}
		if got, want := aErr.Origin.Location(), "config.yaml:4:3"; got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
	}
}
