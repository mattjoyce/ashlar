package yamlconfig_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

func TestLoadIncludeGraphAndRelativePaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	confDir := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(subDir, 0o750); err != nil {
		t.Fatalf("MkdirAll(subDir) error = %v", err)
	}
	if err := os.MkdirAll(confDir, 0o750); err != nil {
		t.Fatalf("MkdirAll(confDir) error = %v", err)
	}

	files := map[string]string{
		filepath.Join(dir, "root.yaml"): `include:
  - sub/routes.yaml
  - conf.d
service:
  name: gateway
`,
		filepath.Join(subDir, "routes.yaml"): `routes:
  script_path: scripts/run.sh
`,
		filepath.Join(confDir, "01-first.yaml"): `plugins:
  first: true
`,
		filepath.Join(confDir, "02-second.yaml"): `plugins:
  second: true
`,
	}
	for p, content := range files {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", p, err)
		}
	}

	var verifiedPaths []string
	frags, err := yamlconfig.LoadIncludeGraph(context.Background(), os.DirFS(dir), "root.yaml", yamlconfig.IncludeOptions{
		Verifier: func(_ context.Context, p string, _ []byte) error {
			verifiedPaths = append(verifiedPaths, p)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("LoadIncludeGraph() unexpected error: %v", err)
	}

	wantOrder := []string{
		"root.yaml",
		"sub/routes.yaml",
		"conf.d/01-first.yaml",
		"conf.d/02-second.yaml",
	}
	if len(frags) != len(wantOrder) {
		t.Fatalf("len(frags) = %d, want %d", len(frags), len(wantOrder))
	}
	for i, want := range wantOrder {
		if frags[i].Path != want {
			t.Errorf("frags[%d].Path = %q, want %q", i, frags[i].Path, want)
		}
		if frags[i].Root.At(ashlar.NewPointer("include")).IsPresent() {
			t.Errorf("frags[%d] still contains top-level 'include' key", i)
		}
	}
	if len(verifiedPaths) != len(wantOrder) {
		t.Errorf("Verifier called %d times, want %d", len(verifiedPaths), len(wantOrder))
	}

	// Verify include breadcrumb and relative path resolution on sub/routes.yaml.
	scriptPtr := ashlar.NewPointer("routes", "script_path")
	resolvedRoot, err := yamlconfig.ResolveRelativePaths(frags[1].Root, ".", scriptPtr)
	if err != nil {
		t.Fatalf("ResolveRelativePaths() unexpected error: %v", err)
	}
	scriptVal := resolvedRoot.At(scriptPtr)
	if got, _ := scriptVal.StringVal(); got != "sub/scripts/run.sh" {
		t.Errorf("resolved script_path = %q, want %q", got, "sub/scripts/run.sh")
	}
	if got, want := scriptVal.Provenance().Effective.String(), "sub/routes.yaml:2:16 (included via root.yaml:2:5)"; got != want {
		t.Errorf("script_path Origin.String() = %q, want %q", got, want)
	}
}

func TestLoadIncludeGraphRejectsDirectAndIndirectCycles(t *testing.T) {
	t.Parallel()

	// 1. Direct cycle: a.yaml -> a.yaml
	dirDirect := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirDirect, "a.yaml"), []byte("include: a.yaml\nservice:\n  name: loop\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err := yamlconfig.LoadIncludeGraph(context.Background(), os.DirFS(dirDirect), "a.yaml", yamlconfig.IncludeOptions{})
	if !errors.Is(err, ashlar.ErrComposition) {
		t.Fatalf("Direct cycle error = %v, want ErrComposition", err)
	}
	if !strings.Contains(err.Error(), "a.yaml -> a.yaml") {
		t.Errorf("Direct cycle error = %q, want 'a.yaml -> a.yaml'", err.Error())
	}

	// 2. Indirect cycle: a.yaml -> b.yaml -> c.yaml -> a.yaml
	dirIndirect := t.TempDir()
	cycleFiles := map[string]string{
		"a.yaml": "include:\n  - b.yaml\n",
		"b.yaml": "include:\n  - c.yaml\n",
		"c.yaml": "include:\n  - a.yaml\n",
	}
	for name, content := range cycleFiles {
		if err := os.WriteFile(filepath.Join(dirIndirect, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	_, err = yamlconfig.LoadIncludeGraph(context.Background(), os.DirFS(dirIndirect), "a.yaml", yamlconfig.IncludeOptions{})
	if !errors.Is(err, ashlar.ErrComposition) {
		t.Fatalf("Indirect cycle error = %v, want ErrComposition", err)
	}
	if !strings.Contains(err.Error(), "a.yaml -> b.yaml -> c.yaml -> a.yaml") {
		t.Errorf("Indirect cycle error = %q, want full cycle chain", err.Error())
	}
	if !strings.Contains(err.Error(), "c.yaml:2:5") {
		t.Errorf("Indirect cycle error = %q, want include site c.yaml:2:5", err.Error())
	}

	// 3. Failed IntegrityVerifier aborts all-or-nothing with ErrComposition.
	_, err = yamlconfig.LoadIncludeGraph(context.Background(), os.DirFS(dirIndirect), "a.yaml", yamlconfig.IncludeOptions{
		Verifier: func(_ context.Context, _ string, _ []byte) error {
			return errors.New("checksum mismatch")
		},
	})
	if !errors.Is(err, ashlar.ErrComposition) {
		t.Fatalf("Verifier failure error = %v, want ErrComposition", err)
	}
}

func TestPathPrimitivesAndDiscovery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	realCfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(realCfg, []byte("service:\n  name: ok\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// ExpandPath rejects ~otheruser.
	if _, err := yamlconfig.ExpandPath("~otheruser/config.yaml"); !errors.Is(err, ashlar.ErrSyntax) {
		t.Errorf("ExpandPath(~otheruser) error = %v, want ErrSyntax", err)
	}

	// Symlink detection.
	linkCfg := filepath.Join(dir, "link-config.yaml")
	if err := os.Symlink(realCfg, linkCfg); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	warnings, err := yamlconfig.DetectSymlinks([]string{linkCfg})
	if err != nil {
		t.Fatalf("DetectSymlinks() unexpected error: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("len(warnings) = %d, want 1", len(warnings))
	}

	// Discover: optional missing candidate skipped, next existing candidate selected.
	gotPath, gotOrig, err := yamlconfig.Discover(
		yamlconfig.Candidate{Path: filepath.Join(dir, "missing-optional.yaml"), Required: false},
		yamlconfig.Candidate{Path: realCfg, Origin: ashlar.ExplicitOrigin("--config"), Required: true},
	)
	if err != nil {
		t.Fatalf("Discover() unexpected error: %v", err)
	}
	if gotOrig.String() != "explicit:--config" || gotPath == "" {
		t.Errorf("Discover() = (%q, %s), want non-empty path and explicit:--config", gotPath, gotOrig)
	}

	// Discover: missing Required candidate fails closed with ErrNotFound.
	_, _, err = yamlconfig.Discover(
		yamlconfig.Candidate{Path: filepath.Join(dir, "missing-required.yaml"), Origin: ashlar.ExplicitOrigin("--config"), Required: true},
		yamlconfig.Candidate{Path: realCfg, Required: false},
	)
	if !errors.Is(err, ashlar.ErrNotFound) {
		t.Fatalf("Discover(missing required) error = %v, want ErrNotFound", err)
	}

	// ComputeFileHash and ComputeKeyedFileHash.
	h1, err := yamlconfig.ComputeFileHash(realCfg)
	if err != nil || !strings.HasPrefix(h1, "sha256:") {
		t.Errorf("ComputeFileHash() = %q, %v", h1, err)
	}
	key := make([]byte, 32)
	kh, err := yamlconfig.ComputeKeyedFileHash(realCfg, key)
	if err != nil || !strings.HasPrefix(kh, "sha256:") || kh == h1 {
		t.Errorf("ComputeKeyedFileHash() = %q, %v", kh, err)
	}
	if _, err := yamlconfig.ComputeKeyedFileHash(realCfg, []byte("short")); !errors.Is(err, ashlar.ErrValidation) {
		t.Errorf("ComputeKeyedFileHash(short key) error = %v, want ErrValidation", err)
	}
}
