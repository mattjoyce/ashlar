// Command relayd is a worked example of embedding Ashlar in a service.
//
// The service is a small telemetry relay with two distinct input boundaries:
//
//  1. Runtime configuration (config.yaml -> routes.yaml, plugins.yaml), where defaults,
//     included YAML fragments, environment bindings, and CLI flags compose under declared
//     rules and carry full provenance.
//  2. Quarantined data contracts (manifest.json), where a single JSON document is verified
//     against a closed schema without includes, merging, environment expansion, or default
//     insertion, preserving original bytes and exact numeric spellings ("1.0", "1e0").
//
// Read the files in this order:
//
//	config.yaml           the root service configuration and its include list
//	routes.yaml           additional routes appended by composition policy
//	plugins.yaml          plugin definitions guarded against duplicate declarations
//	manifest.json         a quarantined JSON stream manifest with exact number lexemes
//	config.go             (this file) the Go shapes, defaults, and composition policy
//	load.go               reading both boundaries all-or-nothing, and atomic hot reload
//	main.go               a command that prints the snapshot, explains one path, or applies --set
//	relayd_test.go        provenance, redaction, contract fidelity, and refusal layers pinned as tests
//
// Run it from this directory:
//
//	go run .                                   # resolved snapshot, provenance report, and manifest summary
//	go run . /service/tick_interval            # provenance chain for one setting
//	go run . /auth/signing_secret              # redacted provenance chain for a secret
//	go run . --set /service/max_workers=32     # explicit CLI override layered at the top of precedence
//	go test ./...
package main

import (
	"time"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

// Config is the decoded runtime configuration of relayd.
//
// Decoding happens last, after defaults, YAML fragments, environment variables, and
// explicit overrides have composed at the ashlar.Value node level and passed
// config.schema.json. Go zero values never participate in merging.
type Config struct {
	Service ServiceConfig           `yaml:"service"`
	Auth    AuthConfig              `yaml:"auth"`
	Routes  []RouteConfig           `yaml:"routes"`
	Plugins map[string]PluginConfig `yaml:"plugins"`
}

// ServiceConfig holds the listener and worker pool settings under /service.
type ServiceConfig struct {
	Name         string        `yaml:"name"`
	ListenAddr   string        `yaml:"listen_addr"`
	TickInterval time.Duration `yaml:"tick_interval"`
	MaxWorkers   int           `yaml:"max_workers"`
}

// AuthConfig holds the non-secret vault reference (token_ref) and the sensitive HMAC
// signing secret (signing_secret). Ashlar's default redactor classifies signing_secret
// as sensitive while exempting token_ref because of its _ref suffix.
type AuthConfig struct {
	TokenRef      string `yaml:"token_ref"`
	SigningSecret string `yaml:"signing_secret"`
}

// RouteConfig maps an inbound path to a sink plugin name.
type RouteConfig struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// PluginConfig declares one sink plugin under /plugins/<name>.
type PluginConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Entrypoint string `yaml:"entrypoint"`
}

// Manifest is the decoded shape of manifest.json.
//
// Because Go numeric fields cannot distinguish an omitted key from an explicit 0, or
// preserve the spelling difference between 1, 1.0, and 1e0, callers inspect the
// jsoncontract.Document's Root() alongside this struct when presence or canonical
// lexemes matter.
type Manifest struct {
	SchemaVersion    int      `json:"schema_version"`
	StreamID         string   `json:"stream_id"`
	CalibrationRatio float64  `json:"calibration_ratio"`
	GainScale        float64  `json:"gain_scale"`
	HighWatermark    uint64   `json:"high_watermark"`
	CompressionLevel int      `json:"compression_level"`
	SamplingRate     *float64 `json:"sampling_rate"`
	Notes            *string  `json:"notes"`
}

// Policy returns relayd's declarative YAML composition rules:
//
//   - Mappings deep-merge by default (MergeMap).
//   - /routes appends across fragments in include order (MergeAppendSlice) so routes.yaml
//     extends config.yaml rather than replacing it.
//   - /plugins/* forbids duplicate keys across fragments (MergeForbidDuplicate) so two
//     fragments cannot silently redefine the same plugin.
func Policy() yamlconfig.CompositionPolicy {
	p := yamlconfig.DefaultCompositionPolicy()
	p.Rules[ashlar.NewPointer("routes")] = yamlconfig.MergeAppendSlice
	p.PatternRules = []yamlconfig.PatternMergeRule{
		{Pattern: "/plugins/*", Rule: yamlconfig.MergeForbidDuplicate},
	}
	return p
}

// Defaults returns relayd's built-in default layer.
//
// Defaults sit at the bottom of precedence (default -> file -> env -> explicit). Every
// value carries ashlar.DefaultOrigin so provenance reports show which settings stayed at
// their default and which were overridden by a file, environment variable, or flag.
func Defaults() []yamlconfig.Override {
	defaultWorkers, _ := ashlar.NumberValue("4", ashlar.Provenance{})
	return []yamlconfig.Override{
		yamlconfig.DefaultSetting(
			ashlar.NewPointer("service", "listen_addr"),
			"listen_addr",
			ashlar.StringValue("127.0.0.1:9000", ashlar.Provenance{}),
		),
		yamlconfig.DefaultSetting(
			ashlar.NewPointer("service", "tick_interval"),
			"tick_interval",
			ashlar.StringValue("15s", ashlar.Provenance{}),
		),
		yamlconfig.DefaultSetting(
			ashlar.NewPointer("service", "max_workers"),
			"max_workers",
			defaultWorkers,
		),
	}
}

// RequiredPointers lists the configuration paths that must be present and non-empty
// after all precedence layers have been applied.
func RequiredPointers() []ashlar.Pointer {
	return []ashlar.Pointer{
		ashlar.NewPointer("service", "name"),
		ashlar.NewPointer("service", "listen_addr"),
		ashlar.NewPointer("service", "tick_interval"),
		ashlar.NewPointer("service", "max_workers"),
		ashlar.NewPointer("auth", "token_ref"),
		ashlar.NewPointer("auth", "signing_secret"),
	}
}
