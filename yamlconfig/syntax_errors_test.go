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

// plantedSecret is a recognizable secret value placed on or near every bad
// line, so the tests can prove diagnostics never echo it.
const plantedSecret = "sekrit-123"

// writeTree writes each name/content pair into a fresh t.TempDir and returns
// the directory.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", full, err)
		}
	}
	return dir
}

// requireLocatedError asserts err matches sentinel via errors.Is, is an
// *ashlar.Error whose origin renders exactly as wantLoc (primary location) and
// wantOrigin (with include chain; empty means same as wantLoc), and that no
// formatting of err contains any of the secrets. It returns the error's
// pointer so callers can check it.
func requireLocatedError(
	t *testing.T,
	err error,
	sentinel error,
	wantLoc string,
	wantOrigin string,
	secrets ...string,
) ashlar.Pointer {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %v at %s", sentinel, wantLoc)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is(err, %v) = false; err = %v", sentinel, err)
	}
	var aErr *ashlar.Error
	if !errors.As(err, &aErr) {
		t.Fatalf("errors.As(*ashlar.Error) = false; err = %v", err)
	}
	if wantOrigin == "" {
		wantOrigin = wantLoc
	}
	switch got := aErr.Origin.Location(); {
	case got != wantLoc:
		t.Errorf("Origin.Location() = %q, want %q (err = %v)", got, wantLoc, err)
	case aErr.Origin.String() != wantOrigin:
		t.Errorf("Origin.String() = %q, want %q", aErr.Origin.String(), wantOrigin)
	case !strings.HasPrefix(err.Error(), wantOrigin):
		t.Errorf("err.Error() = %q, want prefix %q", err.Error(), wantOrigin)
	}
	assertNoSecrets(t, err, secrets...)
	return aErr.Pointer
}

// assertNoSecrets fails if any secret appears in err's Error, %+v, or %q
// renderings.
func assertNoSecrets(t *testing.T, err error, secrets ...string) {
	t.Helper()
	renderings := map[string]string{
		"Error()": err.Error(),
		"%+v":     fmt.Sprintf("%+v", err),
		"%q":      fmt.Sprintf("%q", err),
	}
	for _, secret := range secrets {
		for form, text := range renderings {
			if strings.Contains(text, secret) {
				t.Errorf("%s leaks secret %q: %s", form, secret, text)
			}
		}
	}
}

// TestSyntaxErrorsReportExactLocationWithoutSecrets reads malformed YAML
// files through ReadFS and checks that every syntax, duplicate-key,
// trailing-document, and tab-indentation failure carries the right sentinel,
// its location, and no planted secret. Duplicate keys and well-formed trailing
// documents are located by Ashlar from yaml.Node positions, so they carry an
// exact file:line:column. Every other syntax error is located by yaml.v3, which
// reports a line and no column. For parser errors that line can be where the
// enclosing construct starts, which may be earlier than the offending
// character. Those cases expect "file:line" with yaml.v3's line, so a guessed
// column can never come back.
func TestSyntaxErrorsReportExactLocationWithoutSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		yaml     string
		sentinel error
		wantLoc  string
		secrets  []string
	}{
		{
			// "@" is reserved and cannot start a token; it is the first byte of line 2.
			name:     "top_level_reserved_indicator_at_line_start",
			yaml:     "name: ok\n@token: sekrit-123\n",
			sentinel: ashlar.ErrSyntax,
			wantLoc:  "cfg.yaml:2",
		},
		{
			// "port: " is 6 bytes, so the "@" sits at column 7.
			name:     "top_level_reserved_indicator_mid_line",
			yaml:     "name: ok\nport: @sekrit-123\n",
			sentinel: ashlar.ErrSyntax,
			wantLoc:  "cfg.yaml:2",
		},
		{
			// "token: " is 7 bytes and "sekrit-123" fills columns 8-17,
			// so the illegal second ":" is at column 18.
			name:     "top_level_mapping_value_in_plain_scalar",
			yaml:     "name: ok\ntoken: sekrit-123: extra\n",
			sentinel: ashlar.ErrSyntax,
			wantLoc:  "cfg.yaml:2",
		},
		{
			// The quoted scalar that never closes opens at column 8 of line 2.
			name:     "top_level_unterminated_double_quote",
			yaml:     "name: ok\ntoken: \"sekrit-123\n",
			sentinel: ashlar.ErrSyntax,
			wantLoc:  "cfg.yaml:2",
		},
		{
			// "  port: " is 8 bytes, so the backtick is at column 9.
			name:     "nested_reserved_backtick",
			yaml:     "service:\n  name: ok\n  port: `sekrit-123`\n",
			sentinel: ashlar.ErrSyntax,
			wantLoc:  "cfg.yaml:3",
		},
		{
			// Line 3 is over-indented, so it continues the plain scalar "ok";
			// the ":" after "    password" (12 bytes) at column 13 is illegal.
			name:     "nested_over_indented_key",
			yaml:     "service:\n  name: ok\n    password: sekrit-123\n",
			sentinel: ashlar.ErrSyntax,
			wantLoc:  "cfg.yaml:3",
		},
		{
			// "  password: " is 12 bytes; the unclosed "[" is at column 13.
			name:     "nested_unclosed_flow_sequence",
			yaml:     "service:\n  name: ok\n  password: [sekrit-123\n",
			sentinel: ashlar.ErrSyntax,
			wantLoc:  "cfg.yaml:2",
		},
		{
			// The tab used for indentation is the first byte of line 2.
			name:     "tab_indentation_first_child",
			yaml:     "service:\n\tpassword: sekrit-123\n",
			sentinel: ashlar.ErrSyntax,
			wantLoc:  "cfg.yaml:2",
		},
		{
			// The tab used for indentation is the first byte of line 3.
			name:     "tab_indentation_after_space_indented_sibling",
			yaml:     "service:\n  name: ok\n\tpassword: sekrit-123\n",
			sentinel: ashlar.ErrSyntax,
			wantLoc:  "cfg.yaml:2",
		},
		{
			// The second "token" key starts line 3.
			name:     "duplicate_key_top_level",
			yaml:     "token: sekrit-123\nport: 1\ntoken: sekrit-456\n",
			sentinel: ashlar.ErrDuplicateKey,
			wantLoc:  "cfg.yaml:3:1",
			secrets:  []string{"sekrit-456"},
		},
		{
			// The second "token" key is indented two spaces on line 3.
			name:     "duplicate_key_nested",
			yaml:     "service:\n  token: sekrit-123\n  token: sekrit-456\n",
			sentinel: ashlar.ErrDuplicateKey,
			wantLoc:  "cfg.yaml:3:3",
			secrets:  []string{"sekrit-456"},
		},
		{
			// The trailing document begins at its "---" marker.
			name:     "trailing_document_after_marker",
			yaml:     "name: ok\n---\ntoken: sekrit-123\n",
			sentinel: ashlar.ErrTrailingContent,
			wantLoc:  "cfg.yaml:2:1",
		},
		{
			// "..." ends document one; document two has no marker and starts at line 3.
			name:     "trailing_document_after_end_marker",
			yaml:     "name: ok\n...\ntoken: sekrit-123\n",
			sentinel: ashlar.ErrTrailingContent,
			wantLoc:  "cfg.yaml:2",
		},
		{
			// A malformed second document is still trailing content that
			// begins at its "---" marker.
			name:     "trailing_malformed_document",
			yaml:     "name: ok\n---\ntoken: [sekrit-123\n",
			sentinel: ashlar.ErrTrailingContent,
			wantLoc:  "cfg.yaml:2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(tc.yaml, plantedSecret) {
				t.Fatalf("test input must plant %q", plantedSecret)
			}
			dir := writeTree(t, map[string]string{"cfg.yaml": tc.yaml})

			_, err := yamlconfig.ReadFS(os.DirFS(dir), "cfg.yaml")

			secrets := append([]string{plantedSecret}, tc.secrets...)
			requireLocatedError(t, err, tc.sentinel, tc.wantLoc, "", secrets...)
		})
	}
}

// TestSyntaxErrorInIncludedFileReportsChildLocationAndChain resolves a root
// file whose included fragment is malformed, and checks the error points at
// the exact location inside the included file, names the include site in its
// chain, and never echoes the planted secret.
func TestSyntaxErrorInIncludedFileReportsChildLocationAndChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		files      map[string]string
		sentinel   error
		wantLoc    string
		wantOrigin string
	}{
		{
			// "include: " is 9 bytes, so the include target is at root.yaml:1:10.
			// "  port: " is 8 bytes, so the backtick is at child.yaml:2:9.
			name: "reserved_indicator_in_child",
			files: map[string]string{
				"root.yaml":  "include: child.yaml\nname: ok\n",
				"child.yaml": "service:\n  port: `sekrit-123`\n",
			},
			sentinel:   ashlar.ErrSyntax,
			wantLoc:    "child.yaml:2",
			wantOrigin: "child.yaml:2 (included via root.yaml:1:10)",
		},
		{
			// The tab is the first byte of child.yaml line 2.
			name: "tab_indentation_in_child",
			files: map[string]string{
				"root.yaml":  "include: child.yaml\nname: ok\n",
				"child.yaml": "service:\n\tpassword: sekrit-123\n",
			},
			sentinel:   ashlar.ErrSyntax,
			wantLoc:    "child.yaml:2",
			wantOrigin: "child.yaml:2 (included via root.yaml:1:10)",
		},
		{
			// Sequence form: "  - " is 4 bytes, so the include entry is at
			// root.yaml:2:5. The duplicate key starts child.yaml line 3.
			name: "duplicate_key_in_child",
			files: map[string]string{
				"root.yaml":  "include:\n  - child.yaml\nname: ok\n",
				"child.yaml": "token: sekrit-123\nport: 1\ntoken: sekrit-123\n",
			},
			sentinel:   ashlar.ErrDuplicateKey,
			wantLoc:    "child.yaml:3:1",
			wantOrigin: "child.yaml:3:1 (included via root.yaml:2:5)",
		},
		{
			// The grandchild is reached through two include sites.
			name: "trailing_document_in_grandchild",
			files: map[string]string{
				"root.yaml":       "include: mid.yaml\nname: ok\n",
				"mid.yaml":        "include: leaf/leaf.yaml\nport: 1\n",
				"leaf/leaf.yaml":  "name: ok\n---\ntoken: sekrit-123\n",
				"leaf/other.yaml": "unused: true\n",
			},
			sentinel:   ashlar.ErrTrailingContent,
			wantLoc:    "leaf/leaf.yaml:2:1",
			wantOrigin: "leaf/leaf.yaml:2:1 (included via root.yaml:1:10 -> mid.yaml:1:10)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeTree(t, tc.files)

			res, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "root.yaml", yamlconfig.ResolveOptions{})

			requireLocatedError(t, err, tc.sentinel, tc.wantLoc, tc.wantOrigin, plantedSecret)
			if !res.Root().IsOmitted() || res.Files() != nil {
				t.Errorf("Resolve() returned partial configuration on error: root kind %v, files %v",
					res.Root().Kind(), res.Files())
			}
		})
	}
}
