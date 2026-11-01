package yamlconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattjoyce/ashlar"
	"github.com/mattjoyce/ashlar/yamlconfig"
)

// hiddenEmbed is unexported, so a nil *hiddenEmbed embedded in another struct
// cannot be allocated through reflection.
type hiddenEmbed struct {
	V int `yaml:"v"`
}

// TestDecodeUnexportedEmbeddedPointer checks that decoding a promoted field of
// a nil pointer to an unexported embedded struct returns a located
// ErrValidation instead of panicking, and that the same field decodes when
// the caller has already allocated the pointer.
func TestDecodeUnexportedEmbeddedPointer(t *testing.T) {
	t.Parallel()

	type outer struct {
		*hiddenEmbed
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("v: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := yamlconfig.ReadFS(os.DirFS(dir), "config.yaml")
	if err != nil {
		t.Fatalf("ReadFS() error = %v", err)
	}

	var nilEmbed outer
	err = yamlconfig.DecodeValueStrict(root, &nilEmbed)
	var aErr *ashlar.Error
	if !errors.As(err, &aErr) || !errors.Is(err, ashlar.ErrValidation) {
		t.Fatalf("DecodeValueStrict() error = %v, want located ErrValidation", err)
	}
	if aErr.Pointer != "/v" || aErr.Origin.Location() != "config.yaml:1:4" {
		t.Errorf("error at %s %s, want /v config.yaml:1:4", aErr.Pointer, aErr.Origin.Location())
	}

	allocated := outer{hiddenEmbed: &hiddenEmbed{}}
	if err := yamlconfig.DecodeValueStrict(root, &allocated); err != nil {
		t.Fatalf("DecodeValueStrict(allocated) error = %v", err)
	}
	if allocated.V != 1 {
		t.Errorf("V = %d, want 1", allocated.V)
	}
}
