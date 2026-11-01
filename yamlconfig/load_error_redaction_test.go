package yamlconfig_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

// loadSecret is planted in malformed input. Loading happens before any
// caller redaction policy applies, so no load error may show it under any
// key or in any formatting.
const loadSecret = "sekrit-123"

// assertWithheld fails when err is nil or when loadSecret appears in any
// rendering of err.
func assertWithheld(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want a load error")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%q", "%s"} {
		if got := fmt.Sprintf(format, err); strings.Contains(got, loadSecret) {
			t.Errorf("format %q shows %q: %s", format, loadSecret, got)
		}
	}
}

// TestLoadErrorsWithholdDocumentContent checks every load-time path that used
// to quote document content: yaml.v3 parser messages (an unknown anchor name,
// including in a trailing document), boolean conversion, include targets that
// escape the root or do not exist, and malformed dotenv lines.
func TestLoadErrorsWithholdDocumentContent(t *testing.T) {
	t.Parallel()

	yamlCases := []struct {
		name  string
		files map[string]string
	}{
		{"bool_conversion", map[string]string{"root.yaml": "pin: !!bool " + loadSecret + "\n"}},
		{"unknown_anchor", map[string]string{"root.yaml": "pin: *" + loadSecret + "\n"}},
		{"unknown_anchor_in_trailing_document", map[string]string{"root.yaml": "a: 1\n---\nb: *" + loadSecret + "\n"}},
		{"escaping_include", map[string]string{"root.yaml": "include: ../" + loadSecret + "\n"}},
		{"missing_include", map[string]string{"root.yaml": "include: " + loadSecret + ".yaml\n"}},
		{"non_string_key", map[string]string{"root.yaml": "!!bool " + loadSecret + ": value\n"}},
		{"custom_tag_on_key", map[string]string{"root.yaml": "!" + loadSecret + " name: value\n"}},
	}
	for _, tc := range yamlCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "root.yaml", yamlconfig.ResolveOptions{})
			assertWithheld(t, err)
		})
	}

	t.Run("unresolved_interpolation", func(t *testing.T) {
		t.Parallel()
		// ${...} names are identifiers, so this case plants its own secret.
		const varSecret = "sekrit123"
		dir := t.TempDir()
		doc := "password: '${" + varSecret + "}'\n"
		if err := os.WriteFile(filepath.Join(dir, "root.yaml"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "root.yaml", yamlconfig.ResolveOptions{
			Env: yamlconfig.EnvBinding{
				InterpolateScalar: true,
				Lookup:            func(string) (string, bool) { return "", false },
			},
		})
		if !errors.Is(err, ashlar.ErrUnresolved) {
			t.Fatalf("Resolve() error = %v, want ErrUnresolved", err)
		}
		for _, format := range []string{"%v", "%+v", "%#v", "%q"} {
			if got := fmt.Sprintf(format, err); strings.Contains(got, varSecret) {
				t.Errorf("format %q shows %q: %s", format, varSecret, got)
			}
		}
	})

	t.Run("dotenv_malformed_name", func(t *testing.T) {
		t.Parallel()
		_, err := yamlconfig.ParseDotEnv(".env", []byte("TOKEN "+loadSecret+"=x\n"))
		assertWithheld(t, err)
	})
}
