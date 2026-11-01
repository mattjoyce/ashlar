package yamlconfig_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

// strictRoute is a closed structure decoded from elements of a sequence.
type strictRoute struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// strictPlugin is a closed structure decoded from values of a mapping.
type strictPlugin struct {
	Enabled bool `yaml:"enabled"`
}

// strictService is a closed structure nested under the root.
type strictService struct {
	Name  string `yaml:"name"`
	Port  int    `yaml:"port"`
	Debug bool   `yaml:"debug"`
	Token string `yaml:"token"`
}

// strictConfig is the closed root structure used by the strictness tests.
type strictConfig struct {
	Name    string                  `yaml:"name"`
	Service strictService           `yaml:"service"`
	Routes  []strictRoute           `yaml:"routes"`
	Plugins map[string]strictPlugin `yaml:"plugins"`
}

// requireNoPartialResolved fails if res carries any part of a configuration.
func requireNoPartialResolved(t *testing.T, res yamlconfig.Resolved) {
	t.Helper()
	if !res.Root().IsOmitted() {
		t.Errorf("Resolved.Root() kind = %v, want omitted (no partial configuration)", res.Root().Kind())
	}
	if files := res.Files(); files != nil {
		t.Errorf("Resolved.Files() = %v, want nil", files)
	}
	if sum := res.SourceHash(); sum != "" {
		t.Errorf("Resolved.SourceHash() = %q, want empty", sum)
	}
	var cfg strictConfig
	if err := res.DecodeStrict(&cfg); err == nil {
		t.Errorf("DecodeStrict() on failed Resolved = nil error, want error")
	}
	if cfg.Name != "" || cfg.Service != (strictService{}) || cfg.Routes != nil || cfg.Plugins != nil {
		t.Errorf("DecodeStrict() on failed Resolved populated %+v, want zero value", cfg)
	}
}

// TestDecodeStrictRejectsUnknownFieldsWithLocation resolves real YAML files
// and decodes them into closed structs, checking that an unknown key at the
// root, nested, inside slice elements, inside map values, inside an included
// fragment, and from an explicit override is rejected with
// ashlar.ErrUnknownField, the exact pointer, and the key's origin.
func TestDecodeStrictRejectsUnknownFieldsWithLocation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		files      map[string]string
		explicit   []yamlconfig.Override
		wantPtr    ashlar.Pointer
		wantLoc    string
		wantOrigin string
	}{
		{
			name:    "root",
			files:   map[string]string{"cfg.yaml": "name: ok\nbogus_token: sekrit-123\n"},
			wantPtr: "/bogus_token",
			wantLoc: "cfg.yaml:2:1",
		},
		{
			name:    "nested",
			files:   map[string]string{"cfg.yaml": "service:\n  name: ok\n  bogus_token: sekrit-123\n"},
			wantPtr: "/service/bogus_token",
			wantLoc: "cfg.yaml:3:3",
		},
		{
			// "  - " then "    " indent: the key in the second element is at 5:5.
			name: "slice_of_structs_block",
			files: map[string]string{
				"cfg.yaml": "routes:\n  - from: a\n    to: b\n  - from: c\n    bogus_token: sekrit-123\n",
			},
			wantPtr: "/routes/1/bogus_token",
			wantLoc: "cfg.yaml:5:5",
		},
		{
			// "routes: [{from: a, " is 19 bytes, so the key starts at 1:20.
			name:    "slice_of_structs_flow",
			files:   map[string]string{"cfg.yaml": "routes: [{from: a, bogus_token: sekrit-123}]\n"},
			wantPtr: "/routes/0/bogus_token",
			wantLoc: "cfg.yaml:1:20",
		},
		{
			name: "map_value",
			files: map[string]string{
				"cfg.yaml": "plugins:\n  alpha:\n    enabled: true\n    bogus_token: sekrit-123\n",
			},
			wantPtr: "/plugins/alpha/bogus_token",
			wantLoc: "cfg.yaml:4:5",
		},
		{
			// "include: " is 9 bytes, so the include site is cfg.yaml:1:10.
			name: "included_fragment",
			files: map[string]string{
				"cfg.yaml":   "include: child.yaml\nname: ok\n",
				"child.yaml": "service:\n  bogus_token: sekrit-123\n",
			},
			wantPtr:    "/service/bogus_token",
			wantLoc:    "child.yaml:2:3",
			wantOrigin: "child.yaml:2:3 (included via cfg.yaml:1:10)",
		},
		{
			name:  "explicit_override",
			files: map[string]string{"cfg.yaml": "name: ok\n"},
			explicit: []yamlconfig.Override{
				yamlconfig.ExplicitSetting(
					ashlar.NewPointer("service", "bogus_token"),
					"--set service.bogus_token",
					ashlar.StringValue(plantedSecret, ashlar.Provenance{}),
				),
			},
			wantPtr: "/service/bogus_token",
			wantLoc: "explicit:--set service.bogus_token",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeTree(t, tc.files)
			res, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "cfg.yaml", yamlconfig.ResolveOptions{
				Explicit: tc.explicit,
			})
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}

			var cfg strictConfig
			err = res.DecodeStrict(&cfg)

			gotPtr := requireLocatedError(t, err, ashlar.ErrUnknownField, tc.wantLoc, tc.wantOrigin, plantedSecret)
			if gotPtr != tc.wantPtr {
				t.Errorf("Pointer = %q, want %q", gotPtr, tc.wantPtr)
			}
		})
	}
}

// TestIncludeCyclesAreRejectedWithIncludeSiteLocation builds direct and
// indirect include cycles on disk and checks that both LoadIncludeGraph and
// Resolve reject them with ashlar.ErrIncludeCycle located at the include
// entry that closes the cycle, returning no fragments and no configuration.
func TestIncludeCyclesAreRejectedWithIncludeSiteLocation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		files      map[string]string
		wantPtr    ashlar.Pointer
		wantLoc    string
		wantOrigin string
	}{
		{
			// "include: " is 9 bytes, so the target starts at a.yaml:1:10.
			name: "direct_scalar",
			files: map[string]string{
				"a.yaml": "include: a.yaml\ntoken: sekrit-123\n",
			},
			wantPtr: "/include",
			wantLoc: "a.yaml:1:10",
		},
		{
			// "  - " is 4 bytes, so the sequence entry starts at a.yaml:3:5.
			name: "direct_sequence",
			files: map[string]string{
				"a.yaml": "token: sekrit-123\ninclude:\n  - a.yaml\n",
			},
			wantPtr: "/include/0",
			wantLoc: "a.yaml:3:5",
		},
		{
			name: "indirect_two_files",
			files: map[string]string{
				"a.yaml": "include: b.yaml\nname: a\n",
				"b.yaml": "include: a.yaml\ntoken: sekrit-123\n",
			},
			wantPtr:    "/include",
			wantLoc:    "b.yaml:1:10",
			wantOrigin: "b.yaml:1:10 (included via a.yaml:1:10)",
		},
		{
			name: "indirect_three_files",
			files: map[string]string{
				"a.yaml": "include: b.yaml\nname: a\n",
				"b.yaml": "include: c.yaml\nname: b\n",
				"c.yaml": "token: sekrit-123\ninclude:\n  - a.yaml\n",
			},
			wantPtr:    "/include/0",
			wantLoc:    "c.yaml:3:5",
			wantOrigin: "c.yaml:3:5 (included via a.yaml:1:10 -> b.yaml:1:10)",
		},
		{
			// A directory include whose member climbs back to the entry file.
			name: "indirect_through_directory",
			files: map[string]string{
				"a.yaml":        "include: conf.d\nname: a\n",
				"conf.d/x.yaml": "include: ../a.yaml\ntoken: sekrit-123\n",
			},
			wantPtr:    "/include",
			wantLoc:    "conf.d/x.yaml:1:10",
			wantOrigin: "conf.d/x.yaml:1:10 (included via a.yaml:1:10)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := os.DirFS(writeTree(t, tc.files))

			frags, err := yamlconfig.LoadIncludeGraph(context.Background(), fsys, "a.yaml", yamlconfig.IncludeOptions{})
			gotPtr := requireLocatedError(t, err, ashlar.ErrIncludeCycle, tc.wantLoc, tc.wantOrigin, plantedSecret)
			if gotPtr != tc.wantPtr {
				t.Errorf("Pointer = %q, want %q", gotPtr, tc.wantPtr)
			}
			if frags != nil {
				t.Errorf("LoadIncludeGraph() fragments = %d, want nil on error", len(frags))
			}

			res, err := yamlconfig.Resolve(context.Background(), fsys, "a.yaml", yamlconfig.ResolveOptions{})
			requireLocatedError(t, err, ashlar.ErrIncludeCycle, tc.wantLoc, tc.wantOrigin, plantedSecret)
			requireNoPartialResolved(t, res)
		})
	}
}

// TestResolveRejectsUnresolvedRequiredPointers checks that every pointer in
// ResolveOptions.Required that is omitted, null, or a blank string fails
// resolution with ashlar.ErrUnresolved, the required pointer, and the most
// specific origin available: the offending node when it exists, otherwise
// the document root.
func TestResolveRejectsUnresolvedRequiredPointers(t *testing.T) {
	t.Parallel()

	tokenPtr := ashlar.NewPointer("service", "token")
	tests := []struct {
		name     string
		yaml     string
		explicit []yamlconfig.Override
		required ashlar.Pointer
		wantLoc  string
	}{
		{
			// Nothing to point at but the document that should hold it.
			name:     "omitted_leaf",
			yaml:     "service:\n  password: sekrit-123\n",
			required: tokenPtr,
			wantLoc:  "cfg.yaml:1:1",
		},
		{
			name:     "omitted_parent",
			yaml:     "service:\n  password: sekrit-123\n",
			required: ashlar.NewPointer("database", "dsn"),
			wantLoc:  "cfg.yaml:1:1",
		},
		{
			// "  token: " is 9 bytes, so the value starts at 2:10.
			name:     "explicit_null",
			yaml:     "service:\n  token: null\n  password: sekrit-123\n",
			required: tokenPtr,
			wantLoc:  "cfg.yaml:2:10",
		},
		{
			name:     "tilde_null",
			yaml:     "service:\n  token: ~\n  password: sekrit-123\n",
			required: tokenPtr,
			wantLoc:  "cfg.yaml:2:10",
		},
		{
			name:     "empty_string",
			yaml:     "service:\n  token: \"\"\n  password: sekrit-123\n",
			required: tokenPtr,
			wantLoc:  "cfg.yaml:2:10",
		},
		{
			name:     "blank_string",
			yaml:     "service:\n  token: \"   \"\n  password: sekrit-123\n",
			required: tokenPtr,
			wantLoc:  "cfg.yaml:2:10",
		},
		{
			// The null came from an explicit override, so that is its origin.
			name: "null_from_explicit_override",
			yaml: "service:\n  token: sekrit-123\n",
			explicit: []yamlconfig.Override{
				yamlconfig.ExplicitSetting(tokenPtr, "--token", ashlar.NullValue(ashlar.Provenance{})),
			},
			required: tokenPtr,
			wantLoc:  "explicit:--token",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeTree(t, map[string]string{"cfg.yaml": tc.yaml})

			res, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "cfg.yaml", yamlconfig.ResolveOptions{
				Explicit: tc.explicit,
				Required: []ashlar.Pointer{tc.required},
			})

			gotPtr := requireLocatedError(t, err, ashlar.ErrUnresolved, tc.wantLoc, "", plantedSecret)
			if gotPtr != tc.required {
				t.Errorf("Pointer = %q, want %q", gotPtr, tc.required)
			}
			requireNoPartialResolved(t, res)
		})
	}
}

// TestResolveRejectsUnresolvedInterpolatedEnv sets real environment variables
// with t.Setenv and checks that a ${VAR} reference to an unset or empty
// variable fails with ashlar.ErrUnresolved at the scalar's location, without
// echoing the value of a sensitive variable that did resolve.
func TestResolveRejectsUnresolvedInterpolatedEnv(t *testing.T) {
	t.Setenv("ASHLAR_STRICT_DB_PASS", plantedSecret)
	t.Setenv("ASHLAR_STRICT_EMPTY", "")
	t.Setenv("ASHLAR_STRICT_UNSET", "placeholder")
	if err := os.Unsetenv("ASHLAR_STRICT_UNSET"); err != nil {
		t.Fatalf("Unsetenv() error = %v", err)
	}

	tests := []struct {
		name    string
		yaml    string
		wantPtr ashlar.Pointer
		wantLoc string
	}{
		{
			// "  dsn: " is 7 bytes, so the quoted scalar starts at 2:8.
			name:    "unset_after_resolved_secret",
			yaml:    "service:\n  dsn: \"postgres://app:${ASHLAR_STRICT_DB_PASS}@${ASHLAR_STRICT_UNSET}/app\"\n",
			wantPtr: "/service/dsn",
			wantLoc: "cfg.yaml:2:8",
		},
		{
			// "  token: " is 9 bytes, so the plain scalar starts at 2:10.
			name:    "empty_variable",
			yaml:    "service:\n  token: ${ASHLAR_STRICT_EMPTY}\n",
			wantPtr: "/service/token",
			wantLoc: "cfg.yaml:2:10",
		},
		{
			// "  - " is 4 bytes, so the second element starts at 3:5.
			name:    "unset_in_sequence",
			yaml:    "hosts:\n  - ${ASHLAR_STRICT_DB_PASS}\n  - ${ASHLAR_STRICT_UNSET}\n",
			wantPtr: "/hosts/1",
			wantLoc: "cfg.yaml:3:5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTree(t, map[string]string{"cfg.yaml": tc.yaml})

			res, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "cfg.yaml", yamlconfig.ResolveOptions{
				Env: yamlconfig.EnvBinding{InterpolateScalar: true},
			})

			gotPtr := requireLocatedError(t, err, ashlar.ErrUnresolved, tc.wantLoc, "", plantedSecret)
			if gotPtr != tc.wantPtr {
				t.Errorf("Pointer = %q, want %q", gotPtr, tc.wantPtr)
			}
			requireNoPartialResolved(t, res)
		})
	}
}

// TestDiscoverRejectsMissingRequiredCandidate checks SPEC §3.4 Candidate
// semantics: a missing Required candidate fails with ashlar.ErrNotFound
// located at the candidate's declared origin (or its path when none is
// given), while a missing optional candidate is skipped.
func TestDiscoverRejectsMissingRequiredCandidate(t *testing.T) {
	t.Parallel()

	dir := writeTree(t, map[string]string{"found.yaml": "name: ok\n"})
	missing := filepath.Join(dir, "missing.yaml")
	found := filepath.Join(dir, "found.yaml")

	tests := []struct {
		name       string
		candidates []yamlconfig.Candidate
		wantLoc    string
	}{
		{
			name: "required_with_env_origin",
			candidates: []yamlconfig.Candidate{
				{Path: missing, Origin: ashlar.EnvOrigin("APP_CONFIG"), Required: true},
				{Path: found},
			},
			wantLoc: "env:APP_CONFIG",
		},
		{
			name: "required_with_explicit_origin",
			candidates: []yamlconfig.Candidate{
				{Path: missing, Origin: ashlar.ExplicitOrigin("--config"), Required: true},
				{Path: found},
			},
			wantLoc: "explicit:--config",
		},
		{
			name: "required_without_origin",
			candidates: []yamlconfig.Candidate{
				{Path: missing, Required: true},
				{Path: found},
			},
			wantLoc: missing,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path, _, err := yamlconfig.Discover(tc.candidates...)
			requireLocatedError(t, err, ashlar.ErrNotFound, tc.wantLoc, "")
			if path != "" {
				t.Errorf("Discover() path = %q, want empty on error", path)
			}
		})
	}

	t.Run("optional_missing_is_skipped", func(t *testing.T) {
		t.Parallel()
		path, _, err := yamlconfig.Discover(
			yamlconfig.Candidate{Path: missing},
			yamlconfig.Candidate{Path: found},
		)
		if err != nil {
			t.Fatalf("Discover() error = %v", err)
		}
		want, err := filepath.EvalSymlinks(found)
		if err != nil {
			t.Fatalf("EvalSymlinks() error = %v", err)
		}
		if path != want {
			t.Errorf("Discover() path = %q, want %q", path, want)
		}
	})
}

// TestResolveFailurePartwayReturnsNoPartialConfiguration makes resolution
// fail at each stage after real work has been done (a later include, fragment
// composition, the environment layer, explicit overrides, and the final
// required check) and checks the failure is located and that Resolve returns
// no partial configuration. It uses t.Setenv, so it does not run in parallel.
func TestResolveFailurePartwayReturnsNoPartialConfiguration(t *testing.T) {
	t.Setenv("ASHLAR_STRICT_DEBUG", plantedSecret)

	baseFiles := map[string]string{
		"cfg.yaml":  "include:\n  - good.yaml\n  - other.yaml\nservice:\n  port: 1\n  debug: false\n",
		"good.yaml": "name: good\n",
	}
	withOther := func(other string) map[string]string {
		files := map[string]string{"other.yaml": other}
		for k, v := range baseFiles {
			files[k] = v
		}
		return files
	}

	tests := []struct {
		name       string
		files      map[string]string
		opts       yamlconfig.ResolveOptions
		sentinel   error
		wantLoc    string
		wantOrigin string
	}{
		{
			// The second include breaks after the first loaded cleanly.
			name:       "later_include_broken",
			files:      withOther("port: 2\ntoken: sekrit-123\nport: 3\n"),
			sentinel:   ashlar.ErrDuplicateKey,
			wantLoc:    "other.yaml:3:1",
			wantOrigin: "other.yaml:3:1 (included via cfg.yaml:3:5)",
		},
		{
			// "  port: " is 8 bytes; the conflicting overlay value is other.yaml:2:9.
			name:  "composition_conflict",
			files: withOther("service:\n  port: 2\n  token: sekrit-123\n"),
			opts: yamlconfig.ResolveOptions{
				Policy: yamlconfig.CompositionPolicy{
					Rules: map[ashlar.Pointer]yamlconfig.MergeRule{
						ashlar.NewPointer("service", "port"): yamlconfig.MergeForbidDuplicate,
					},
				},
			},
			sentinel:   ashlar.ErrComposition,
			wantLoc:    "other.yaml:2:9",
			wantOrigin: "other.yaml:2:9 (included via cfg.yaml:3:5)",
		},
		{
			name:  "env_layer_bad_boolean",
			files: withOther("service:\n  port: 2\n"),
			opts: yamlconfig.ResolveOptions{
				Env: yamlconfig.EnvBinding{
					Bindings: map[ashlar.Pointer]string{
						ashlar.NewPointer("service", "debug"): "ASHLAR_STRICT_DEBUG",
					},
				},
			},
			sentinel: ashlar.ErrValidation,
			wantLoc:  "env:ASHLAR_STRICT_DEBUG",
		},
		{
			name:  "explicit_override_through_scalar",
			files: withOther("service:\n  port: 2\n"),
			opts: yamlconfig.ResolveOptions{
				Explicit: []yamlconfig.Override{
					yamlconfig.ExplicitSetting(
						ashlar.NewPointer("service", "port", "inner"),
						"--set service.port.inner",
						ashlar.StringValue(plantedSecret, ashlar.Provenance{}),
					),
				},
			},
			sentinel: ashlar.ErrComposition,
			wantLoc:  "explicit:--set service.port.inner",
		},
		{
			// "  token: " is 9 bytes; the blank required value is other.yaml:2:10.
			name:  "required_check_last",
			files: withOther("service:\n  token: \"\"\n  password: sekrit-123\n"),
			opts: yamlconfig.ResolveOptions{
				Required: []ashlar.Pointer{ashlar.NewPointer("service", "token")},
			},
			sentinel:   ashlar.ErrUnresolved,
			wantLoc:    "other.yaml:2:10",
			wantOrigin: "other.yaml:2:10 (included via cfg.yaml:3:5)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTree(t, tc.files)

			res, err := yamlconfig.Resolve(context.Background(), os.DirFS(dir), "cfg.yaml", tc.opts)

			requireLocatedError(t, err, tc.sentinel, tc.wantLoc, tc.wantOrigin, plantedSecret)
			requireNoPartialResolved(t, res)
		})
	}
}
