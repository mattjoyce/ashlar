package schema_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
	"github.com/mattjoyce/ashlar/schema"
)

const baseConfigSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.org/schemas/config.schema.json",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "service": { "$ref": "#/$defs/Service" },
    "auth": { "$ref": "https://example.org/schemas/auth.schema.json" }
  },
  "$defs": {
    "Service": {
      "type": "object",
      "additionalProperties": false,
      "required": ["name", "port"],
      "properties": {
        "name": { "type": "string", "minLength": 1 },
        "port": { "type": "integer", "minimum": 1, "maximum": 65535 }
      }
    },
    "WholeConfig": {
      "allOf": [
        { "$ref": "#" },
        { "required": ["service", "auth"] }
      ]
    }
  }
}`

const authSubSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://example.org/schemas/auth.schema.json",
  "type": "object",
  "additionalProperties": false,
  "required": ["api_token"],
  "properties": {
    "api_token": {
      "type": "string",
      "enum": ["allowed-placeholder-1", "allowed-placeholder-2"]
    }
  }
}`

func TestBundleCompileAndFragmentTarget(t *testing.T) {
	t.Parallel()

	b := schema.NewBundle()
	if err := b.AddResource("", []byte(baseConfigSchema)); err != nil {
		t.Fatalf("AddResource(baseConfigSchema) error = %v", err)
	}
	if err := b.AddResource("", []byte(authSubSchema)); err != nil {
		t.Fatalf("AddResource(authSubSchema) error = %v", err)
	}

	lenientVal, err := b.Compile("https://example.org/schemas/config.schema.json")
	if err != nil {
		t.Fatalf("Compile(lenient) error = %v", err)
	}
	wholeVal, err := b.Compile("https://example.org/schemas/config.schema.json#/$defs/WholeConfig")
	if err != nil {
		t.Fatalf("Compile(WholeConfig) error = %v", err)
	}

	// Partial fragment (only "service", omitting "auth") passes lenient root schema
	// but fails #/$defs/WholeConfig.
	fragmentDoc, err := jsoncontract.Read("fragment.json", []byte(`{
  "service": {
    "name": "gateway",
    "port": 8080
  }
}`))
	if err != nil {
		t.Fatalf("Read(fragment.json) error = %v", err)
	}
	if err := lenientVal.Validate(fragmentDoc.Root(), nil); err != nil {
		t.Errorf("lenientVal.Validate(fragment) unexpected error = %v", err)
	}
	if err := wholeVal.Validate(fragmentDoc.Root(), nil); !errors.Is(err, ashlar.ErrSchema) {
		t.Errorf("wholeVal.Validate(fragment) error = %v, want ErrSchema", err)
	}
}

func TestValidateReportsSourceLocationsAndRedactsSecrets(t *testing.T) {
	t.Parallel()

	b := schema.NewBundle()
	if err := b.AddResource("", []byte(baseConfigSchema)); err != nil {
		t.Fatalf("AddResource(base) error = %v", err)
	}
	if err := b.AddResource("", []byte(authSubSchema)); err != nil {
		t.Fatalf("AddResource(auth) error = %v", err)
	}
	v, err := b.Compile("https://example.org/schemas/config.schema.json#/$defs/WholeConfig")
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// 1. Unknown property in closed object reports exact property Pointer and line:col.
	unknownPropDoc, err := jsoncontract.Read("config.json", []byte(`{
  "service": {
    "name": "gateway",
    "port": 8080,
    "rogue_field": true
  },
  "auth": {
    "api_token": "allowed-placeholder-1"
  }
}`))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	err = v.Validate(unknownPropDoc.Root(), nil)
	if !errors.Is(err, ashlar.ErrSchema) {
		t.Fatalf("Validate(unknownPropDoc) error = %v, want ErrSchema", err)
	}
	var aErr *ashlar.Error
	if !errors.As(err, &aErr) {
		t.Fatalf("expected *ashlar.Error, got %T", err)
	}
	if got, want := aErr.Pointer.String(), "/service/rogue_field"; got != want {
		t.Errorf("aErr.Pointer = %q, want %q", got, want)
	}
	if got, want := aErr.Origin.Location(), "config.json:5:5"; got != want {
		t.Errorf("aErr.Origin.Location() = %q, want %q", got, want)
	}

	// 2. Type/range violation reports offending value's Pointer and line:col.
	badPortDoc, err := jsoncontract.Read("config.json", []byte(`{
  "service": {
    "name": "gateway",
    "port": 99999
  },
  "auth": {
    "api_token": "allowed-placeholder-1"
  }
}`))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	err = v.Validate(badPortDoc.Root(), nil)
	if !errors.Is(err, ashlar.ErrSchema) || !errors.As(err, &aErr) {
		t.Fatalf("Validate(badPortDoc) error = %v, want *ashlar.Error(ErrSchema)", err)
	}
	if got, want := aErr.Pointer.String(), "/service/port"; got != want {
		t.Errorf("aErr.Pointer = %q, want %q", got, want)
	}
	if got, want := aErr.Origin.Location(), "config.json:4:13"; got != want {
		t.Errorf("aErr.Origin.Location() = %q, want %q", got, want)
	}

	// 3. Violation on a sensitive key ("api_token") must never leak raw secret material.
	const rawSensitiveValue = "super-sensitive-bearer-val-999"
	sensitiveDoc, err := jsoncontract.Read("config.json", []byte(`{
  "service": {
    "name": "gateway",
    "port": 8080
  },
  "auth": {
    "api_token": "`+rawSensitiveValue+`"
  }
}`))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	err = v.Validate(sensitiveDoc.Root(), ashlar.DefaultRedactor())
	if !errors.Is(err, ashlar.ErrSchema) {
		t.Fatalf("Validate(sensitiveDoc) error = %v, want ErrSchema", err)
	}
	if strings.Contains(err.Error(), rawSensitiveValue) {
		t.Fatalf("sensitive value leaked in schema validation error: %s", err.Error())
	}
}

func TestOfflineBundleRejectsExternalRefAndDuplicateSchemaKeys(t *testing.T) {
	t.Parallel()

	// Duplicate key in schema JSON must fail closed with ErrDuplicateKey.
	bDup := schema.NewBundle()
	err := bDup.AddResource("https://example.org/dup.json", []byte(`{
  "type": "object",
  "type": "string"
}`))
	if !errors.Is(err, ashlar.ErrDuplicateKey) {
		t.Fatalf("AddResource(dup) error = %v, want ErrDuplicateKey", err)
	}

	// Unregistered remote $ref must fail closed during Compile with zero network I/O.
	bRemote := schema.NewBundle()
	if err := bRemote.AddResource("https://example.org/local.json", []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "ext": { "$ref": "https://unregistered.example.invalid/remote.json" }
  }
}`)); err != nil {
		t.Fatalf("AddResource(local) error = %v", err)
	}
	if _, err := bRemote.Compile("https://example.org/local.json"); !errors.Is(err, ashlar.ErrSchema) {
		t.Fatalf("Compile(unregistered remote $ref) error = %v, want ErrSchema", err)
	}
}

func TestVerifyClosed(t *testing.T) {
	t.Parallel()

	closedDoc, err := jsoncontract.Read("closed.schema.json", []byte(`{
  "type": "object",
  "additionalProperties": false,
  "examples": [{"type": "object"}],
  "properties": {
    "labels": {
      "type": "object",
      "additionalProperties": { "type": "string" }
    }
  }
}`))
	if err != nil {
		t.Fatalf("Read(closed) error = %v", err)
	}
	if err := schema.VerifyClosed(closedDoc.Root()); err != nil {
		t.Errorf("VerifyClosed(closedDoc) unexpected error = %v", err)
	}

	openDoc, err := jsoncontract.Read("open.schema.json", []byte(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "nested": {
      "type": "object",
      "properties": {
        "x": { "type": "integer" }
      }
    }
  }
}`))
	if err != nil {
		t.Fatalf("Read(open) error = %v", err)
	}
	err = schema.VerifyClosed(openDoc.Root())
	if !errors.Is(err, ashlar.ErrSchema) {
		t.Fatalf("VerifyClosed(openDoc) error = %v, want ErrSchema", err)
	}
	var aErr *ashlar.Error
	if errors.As(err, &aErr) {
		if got, want := aErr.Pointer.String(), "/properties/nested/additionalProperties"; got != want {
			t.Errorf("aErr.Pointer = %q, want %q", got, want)
		}
		if got, want := aErr.Origin.Location(), "open.schema.json:5:15"; got != want {
			t.Errorf("aErr.Origin.Location() = %q, want %q", got, want)
		}
	}
}
