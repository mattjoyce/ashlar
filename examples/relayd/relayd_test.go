package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

// TestSnapshotProvenanceAndRedaction pins the resolved configuration, the four-layer
// provenance chain (default -> file -> env -> explicit), and secret redaction.
func TestSnapshotProvenanceAndRedaction(t *testing.T) {
	t.Parallel()

	const rawSecret = "super-sensitive-hmac-key-9911"
	envMap := map[string]string{
		"RELAYD_SIGNING_SECRET":        rawSecret,
		"RELAYD_SERVICE_TICK_INTERVAL": "45s",
	}
	explicitWorkers, _ := ashlar.NumberValue("24", ashlar.Provenance{})

	s, err := LoadSnapshot(context.Background(), os.DirFS("."), LoadOptions{
		EnvLookup: func(k string) (string, bool) {
			v, ok := envMap[k]
			return v, ok
		},
		Explicit: []yamlconfig.Override{
			yamlconfig.ExplicitSetting(ashlar.NewPointer("service", "max_workers"), "--max-workers", explicitWorkers),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if s.Config.Service.TickInterval != 45*time.Second {
		t.Errorf("TickInterval = %v, want 45s", s.Config.Service.TickInterval)
	}
	if s.Config.Service.MaxWorkers != 24 {
		t.Errorf("MaxWorkers = %d, want 24", s.Config.Service.MaxWorkers)
	}
	if len(s.Config.Routes) != 3 {
		t.Errorf("len(Routes) = %d, want 3 (appended across config.yaml and routes.yaml)", len(s.Config.Routes))
	}
	if s.Config.Auth.SigningSecret != rawSecret {
		t.Errorf("SigningSecret = %q, want %q", s.Config.Auth.SigningSecret, rawSecret)
	}

	report := s.Resolved.FormatProvenanceReport()
	if strings.Contains(report, rawSecret) {
		t.Fatalf("raw secret leaked in provenance report:\n%s", report)
	}

	wantLines := []string{
		`/service/listen_addr: config.yaml:7:16 = "127.0.0.1:8080" (overrode default:listen_addr = "127.0.0.1:9000")`,
		`/service/tick_interval: env:RELAYD_SERVICE_TICK_INTERVAL = "45s" (overrode default:tick_interval = "15s")`,
		`/service/max_workers: explicit:--max-workers = 24 (overrode config.yaml:8:16 = 8, default:max_workers = 4)`,
		`/auth/token_ref: config.yaml:11:14 = "vault://relayd/ingest-token"`,
		`/auth/signing_secret: env:RELAYD_SIGNING_SECRET = [REDACTED] (overrode config.yaml:12:19 = [REDACTED])`,
	}
	for _, want := range wantLines {
		if !strings.Contains(report, want) {
			t.Errorf("provenance report missing %q in:\n%s", want, report)
		}
	}

	sanitized := s.Resolved.Sanitize(true)
	fp, _ := sanitized.At("/auth/signing_secret").StringVal()
	if !strings.HasPrefix(fp, "secretfp_sha256:") || strings.Contains(fp, rawSecret) {
		t.Errorf("Sanitize(true) signing_secret = %q, want secretfp_sha256:<hex>", fp)
	}
}

// TestContractQuarantineAndNumericFidelity pins the quarantined JSON contract reader:
// raw bytes are preserved verbatim, exact number lexemes ("1.0", "1e0", uint64 max) are
// retained without float64 normalization, and omitted fields are distinguished from
// explicit 0 and explicit null.
func TestContractQuarantineAndNumericFidelity(t *testing.T) {
	t.Parallel()

	rawOnDisk, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, m, err := LoadManifest(os.DirFS("."), "manifest.json", "manifest.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(doc.RawBytes(), rawOnDisk) {
		t.Error("Contract.RawBytes() did not preserve original document bytes")
	}
	if m.HighWatermark != 18446744073709551615 {
		t.Errorf("HighWatermark = %d, want 18446744073709551615", m.HighWatermark)
	}

	root := doc.Root()
	wantLexemes := map[ashlar.Pointer]string{
		"/schema_version":    "1",
		"/calibration_ratio": "1.0",
		"/gain_scale":        "1e0",
		"/high_watermark":    "18446744073709551615",
		"/compression_level": "0",
	}
	for ptr, want := range wantLexemes {
		if got, ok := root.At(ptr).NumberLexeme(); !ok || got != want {
			t.Errorf("At(%s).NumberLexeme() = (%q, %v), want (%q, true)", ptr, got, ok, want)
		}
	}

	if got := root.PresenceAt("/compression_level"); got != ashlar.PresencePresent {
		t.Errorf("PresenceAt(/compression_level) = %v, want PresencePresent (explicit 0)", got)
	}
	if got := root.PresenceAt("/sampling_rate"); got != ashlar.PresenceOmitted {
		t.Errorf("PresenceAt(/sampling_rate) = %v, want PresenceOmitted", got)
	}
	if got := root.PresenceAt("/notes"); got != ashlar.PresenceNull {
		t.Errorf("PresenceAt(/notes) = %v, want PresenceNull", got)
	}
}

// TestBadInputsFailClosed exercises the refusal layers across YAML composition, schema
// validation, secret redaction, and JSON contract reading.
func TestBadInputsFailClosed(t *testing.T) {
	t.Parallel()

	const badSecret = "raw-secret-must-never-leak-777"

	cases := []struct {
		name      string
		mutate    func(dir string)
		env       map[string]string
		wantError error
		forbidStr string
	}{
		{
			name: "unknown YAML key rejected by schema",
			mutate: func(dir string) {
				replaceInFile(t, dir, "config.yaml", "max_workers: 8", "max_workrs: 8")
			},
			wantError: ashlar.ErrSchema,
		},
		{
			name: "duplicate plugin across fragments forbidden by policy",
			mutate: func(dir string) {
				appendToFile(t, dir, "config.yaml", "\nplugins:\n  archive:\n    enabled: false\n    entrypoint: bin/other\n")
			},
			wantError: ashlar.ErrComposition,
		},
		{
			name: "include cycle rejected",
			mutate: func(dir string) {
				prependToFile(t, dir, "routes.yaml", "include:\n  - config.yaml\n")
			},
			wantError: ashlar.ErrIncludeCycle,
		},
		{
			name:   "unresolved env interpolation rejected",
			mutate: func(_ string) {},
			env: map[string]string{
				"RELAYD_SIGNING_SECRET": "",
			},
			wantError: ashlar.ErrUnresolved,
		},
		{
			name:   "sensitive boolean env failure redacts raw secret",
			mutate: func(_ string) {},
			env: map[string]string{
				"RELAYD_SIGNING_SECRET":          "ok-secret",
				"RELAYD_PLUGINS_ARCHIVE_ENABLED": badSecret,
			},
			wantError: ashlar.ErrValidation,
			forbidStr: badSecret,
		},
		{
			name: "duplicate key in JSON manifest rejected",
			mutate: func(dir string) {
				replaceInFile(t, dir, "manifest.json", `"stream_id": "edge-sensors-west",`, `"stream_id": "edge-sensors-west", "stream_id": "dup",`)
			},
			wantError: ashlar.ErrDuplicateKey,
		},
		{
			name: "trailing content in JSON manifest rejected",
			mutate: func(dir string) {
				appendToFile(t, dir, "manifest.json", "\n{\"extra\": true}\n")
			},
			wantError: ashlar.ErrTrailingContent,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := copyFixtureDir(t)
			tc.mutate(dir)

			envMap := map[string]string{"RELAYD_SIGNING_SECRET": "test-signing-secret"}
			for k, v := range tc.env {
				envMap[k] = v
			}

			_, err := LoadSnapshot(context.Background(), os.DirFS(dir), LoadOptions{
				EnvLookup: func(k string) (string, bool) {
					v, ok := envMap[k]
					if !ok || v == "" {
						return "", false
					}
					return v, true
				},
			})
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("err = %v, want errors.Is(..., %v)", err, tc.wantError)
			}
			if tc.forbidStr != "" && strings.Contains(err.Error(), tc.forbidStr) {
				t.Fatalf("forbidden secret %q leaked in error: %v", tc.forbidStr, err)
			}
		})
	}
}

// TestGuardKeepsOldSnapshotOnBadReload checks that a failed reload leaves the previous
// immutable Snapshot in force.
func TestGuardKeepsOldSnapshotOnBadReload(t *testing.T) {
	t.Parallel()

	dir := copyFixtureDir(t)
	opts := LoadOptions{
		EnvLookup: func(k string) (string, bool) {
			if k == "RELAYD_SIGNING_SECRET" {
				return "initial-secret", true
			}
			return "", false
		},
	}

	g, err := NewGuard(context.Background(), os.DirFS(dir), opts)
	if err != nil {
		t.Fatal(err)
	}

	// Break routes.yaml with an unknown field and attempt Reload.
	appendToFile(t, dir, "routes.yaml", "  - from: /bad\n    unknown_key: true\n")
	if err := g.Reload(context.Background(), os.DirFS(dir), opts); !errors.Is(err, ashlar.ErrSchema) {
		t.Fatalf("Reload() error = %v, want ErrSchema", err)
	}

	cur := g.Current()
	if cur.Config.Service.Name != "relayd" || len(cur.Config.Routes) != 3 {
		t.Fatalf("active Snapshot changed after failed reload: %+v", cur.Config)
	}
}

func copyFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := []string{
		"config.yaml",
		"routes.yaml",
		"plugins.yaml",
		"config.schema.json",
		"manifest.json",
		"manifest.schema.json",
	}
	srcFS := os.DirFS(".")
	for _, name := range files {
		raw, err := fs.ReadFile(srcFS, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func replaceInFile(t *testing.T, dir, name, oldStr, newStr string) {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(dir), name)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), oldStr, newStr, 1)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendToFile(t *testing.T, dir, name, suffix string) {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(dir), name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), append(raw, []byte(suffix)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func prependToFile(t *testing.T, dir, name, prefix string) {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(dir), name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), append([]byte(prefix), raw...), 0o600); err != nil {
		t.Fatal(err)
	}
}
