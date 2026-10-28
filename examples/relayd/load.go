package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"sync/atomic"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/jsoncontract"
	"github.com/mattjoyce/ashlar/schema"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

const (
	configSchemaURI   = "https://example.org/relayd/config.schema.json"
	manifestSchemaURI = "https://example.org/relayd/manifest.schema.json"
)

// Snapshot is everything relayd knows about its configuration and stream contract at one
// instant. Both Resolved and Contract are immutable values, so a Snapshot is safe to
// share across goroutines without a lock.
type Snapshot struct {
	Config   Config
	Resolved yamlconfig.Resolved
	Manifest Manifest
	Contract jsoncontract.Document
}

// LoadOptions customizes environment lookup and explicit CLI overrides for LoadSnapshot.
type LoadOptions struct {
	EnvLookup func(string) (string, bool)
	Explicit  []yamlconfig.Override
}

// LoadSnapshot loads and validates both the runtime YAML configuration and the
// quarantined JSON stream manifest from fsys, failing all-or-nothing on the first error.
//
// Four checks must accept the runtime configuration before Config is populated:
//
//  1. yamlconfig.Resolve parses every fragment strictly (refusing duplicate keys,
//     aliases, trailing documents, and include cycles) and composes them under Policy().
//  2. Environment bindings (RELAYD_* and ${VAR} interpolation) and Explicit overrides
//     layer onto the composed tree with provenance and secret redaction.
//  3. The compiled JSON Schema (config.schema.json) validates the resolved ashlar.Value
//     tree before struct decoding.
//  4. Resolved.DecodeStrict decodes into Config, refusing any unknown struct field.
//
// Separately, LoadManifest reads and validates manifest.json through jsoncontract
// without includes, merging, environment interpolation, or default insertion.
func LoadSnapshot(ctx context.Context, fsys fs.FS, opts LoadOptions) (Snapshot, error) {
	resolved, cfg, err := loadRuntimeConfig(ctx, fsys, opts)
	if err != nil {
		return Snapshot{}, err
	}
	contractDoc, manifest, err := LoadManifest(fsys, "manifest.json", "manifest.schema.json")
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Config:   cfg,
		Resolved: resolved,
		Manifest: manifest,
		Contract: contractDoc,
	}, nil
}

func loadRuntimeConfig(
	ctx context.Context,
	fsys fs.FS,
	opts LoadOptions,
) (yamlconfig.Resolved, Config, error) {
	schemaDoc, err := jsoncontract.ReadFS(fsys, "config.schema.json")
	if err != nil {
		return yamlconfig.Resolved{}, Config{}, err
	}
	bundle := schema.NewBundle()
	if err := bundle.AddDocument(configSchemaURI, schemaDoc); err != nil {
		return yamlconfig.Resolved{}, Config{}, err
	}
	validator, err := bundle.Compile(configSchemaURI)
	if err != nil {
		return yamlconfig.Resolved{}, Config{}, err
	}

	resolved, err := yamlconfig.Resolve(ctx, fsys, "config.yaml", yamlconfig.ResolveOptions{
		Defaults: Defaults(),
		Policy:   Policy(),
		Env: yamlconfig.EnvBinding{
			Prefix:            "RELAYD",
			Lookup:            opts.EnvLookup,
			InterpolateScalar: true,
		},
		Explicit: opts.Explicit,
		Required: RequiredPointers(),
	})
	if err != nil {
		return yamlconfig.Resolved{}, Config{}, err
	}

	if err := validator.Validate(resolved.Root(), nil); err != nil {
		return yamlconfig.Resolved{}, Config{}, err
	}

	var cfg Config
	if err := resolved.DecodeStrict(&cfg); err != nil {
		return yamlconfig.Resolved{}, Config{}, err
	}
	return resolved, cfg, nil
}

// LoadManifest reads a single-document JSON contract from fsys, verifies that its JSON
// Schema is closed (additionalProperties: false on every object), validates the document
// against that schema, and decodes it strictly.
func LoadManifest(fsys fs.FS, manifestPath, schemaPath string) (jsoncontract.Document, Manifest, error) {
	schemaDoc, err := jsoncontract.ReadFS(fsys, schemaPath)
	if err != nil {
		return jsoncontract.Document{}, Manifest{}, err
	}
	if err := schema.VerifyClosed(schemaDoc.Root()); err != nil {
		return jsoncontract.Document{}, Manifest{}, err
	}
	bundle := schema.NewBundle()
	if err := bundle.AddDocument(manifestSchemaURI, schemaDoc); err != nil {
		return jsoncontract.Document{}, Manifest{}, err
	}
	validator, err := bundle.Compile(manifestSchemaURI)
	if err != nil {
		return jsoncontract.Document{}, Manifest{}, err
	}

	doc, err := jsoncontract.ReadFS(fsys, manifestPath)
	if err != nil {
		return jsoncontract.Document{}, Manifest{}, err
	}
	if err := validator.Validate(doc.Root(), nil); err != nil {
		return jsoncontract.Document{}, Manifest{}, err
	}

	var m Manifest
	if err := doc.DecodeStrict(&m); err != nil {
		return jsoncontract.Document{}, Manifest{}, err
	}
	return doc, m, nil
}

// ContractDigest computes the canonical SHA-256 digest over the untouched raw bytes of d.
func ContractDigest(d jsoncontract.Document) string {
	sum := sha256.Sum256(d.RawBytes())
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Guard holds the active Snapshot and swaps a new one in atomically on Reload.
//
// Because Snapshot is an immutable value, requests in flight continue reading the old
// Snapshot while Reload validates incoming files. If any file is malformed or violates
// schema or composition policy, Reload returns the error and the old Snapshot remains
// untouched.
type Guard struct {
	cur atomic.Pointer[Snapshot]
}

// NewGuard loads the initial Snapshot and fails if any check refuses the input.
func NewGuard(ctx context.Context, fsys fs.FS, opts LoadOptions) (*Guard, error) {
	g := &Guard{}
	if err := g.Reload(ctx, fsys, opts); err != nil {
		return nil, err
	}
	return g, nil
}

// Reload builds a fresh Snapshot from fsys and swaps it in only if every check succeeds.
func (g *Guard) Reload(ctx context.Context, fsys fs.FS, opts LoadOptions) error {
	s, err := LoadSnapshot(ctx, fsys, opts)
	if err != nil {
		return err
	}
	g.cur.Store(&s)
	return nil
}

// Current returns the Snapshot in force at this instant.
func (g *Guard) Current() Snapshot {
	return *g.cur.Load()
}

// ParseSetFlag parses a "--set /pointer=value" CLI override into a yamlconfig.Override.
func ParseSetFlag(expr string, base ashlar.Value) (yamlconfig.Override, error) {
	ptrStr, rawVal, ok := splitSetExpr(expr)
	if !ok {
		return yamlconfig.Override{}, ashlar.NewError(
			ashlar.ErrSyntax,
			ashlar.RootPointer,
			ashlar.ExplicitOrigin("--set"),
			"expected /pointer=value in --set flag",
			nil,
		)
	}
	ptr, err := ashlar.ParsePointer(ptrStr)
	if err != nil {
		return yamlconfig.Override{}, err
	}
	val, err := parseOverrideScalar(ptr, rawVal, base.At(ptr).Kind())
	if err != nil {
		return yamlconfig.Override{}, err
	}
	return yamlconfig.ExplicitSetting(ptr, "--set", val), nil
}

func splitSetExpr(expr string) (string, string, bool) {
	for i := 0; i < len(expr); i++ {
		if expr[i] == '=' {
			if i == 0 {
				return "", "", false
			}
			return expr[:i], expr[i+1:], true
		}
	}
	return "", "", false
}

func parseOverrideScalar(ptr ashlar.Pointer, raw string, targetKind ashlar.Kind) (ashlar.Value, error) {
	switch targetKind {
	case ashlar.KindNumber:
		v, err := ashlar.NumberValue(raw, ashlar.Provenance{})
		if err != nil {
			return ashlar.Value{}, ashlar.NewRedactedError(
				ashlar.ErrValidation,
				ptr,
				ashlar.ExplicitOrigin("--set"),
				"invalid numeric value in --set override",
				nil,
				nil,
				raw,
				false,
			)
		}
		return v, nil
	case ashlar.KindBool:
		if raw == "true" {
			return ashlar.BoolValue(true, ashlar.Provenance{}), nil
		}
		if raw == "false" {
			return ashlar.BoolValue(false, ashlar.Provenance{}), nil
		}
		return ashlar.Value{}, ashlar.NewRedactedError(
			ashlar.ErrValidation,
			ptr,
			ashlar.ExplicitOrigin("--set"),
			"invalid boolean value in --set override",
			nil,
			nil,
			raw,
			false,
		)
	default:
		return ashlar.StringValue(raw, ashlar.Provenance{}), nil
	}
}
