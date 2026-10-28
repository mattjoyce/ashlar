package yamlconfig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mattjoyce/ashlar"
)

// Candidate describes one configuration file search location for Discover.
type Candidate struct {
	Path     string
	Origin   ashlar.Origin
	Required bool
}

// SymlinkWarning captures a path that resolves through a symbolic link.
type SymlinkWarning struct {
	Path     string
	Resolved string
}

// Discover resolves the first existing regular configuration file from
// candidates, enforcing Required semantics.
//
// When a candidate has Required == true, a missing or non-regular target fails
// closed immediately with an *ashlar.Error wrapping ashlar.ErrNotFound. When
// Required == false, missing files are skipped until a candidate matches or all
// candidates are exhausted.
func Discover(candidates ...Candidate) (string, ashlar.Origin, error) {
	for _, c := range candidates {
		if strings.TrimSpace(c.Path) == "" {
			continue
		}
		resolved, err := ResolveRegularFile(c.Path)
		if err == nil {
			orig := c.Origin
			if !orig.Valid() {
				orig = ashlar.FileOrigin(ashlar.SourceSpan{Source: resolved})
			}
			return resolved, orig, nil
		}
		if c.Required || !errors.Is(err, ashlar.ErrNotFound) {
			orig := c.Origin
			if !orig.Valid() {
				orig = ashlar.FileOrigin(ashlar.SourceSpan{Source: c.Path})
			}
			return "", orig, ashlar.NewError(
				ashlar.ErrNotFound,
				ashlar.RootPointer,
				orig,
				fmt.Sprintf("required configuration path %q not found", c.Path),
				err,
			)
		}
	}
	return "", ashlar.Origin{}, ashlar.NewError(
		ashlar.ErrNotFound,
		ashlar.RootPointer,
		ashlar.Origin{},
		"no configuration file found in search candidates",
		nil,
	)
}

// ExpandPath expands a leading "~" or "~/" (rejecting "~user" forms) and cleans
// the path into an absolute filesystem path.
func ExpandPath(rawPath string) (string, error) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return "", ashlar.NewError(ashlar.ErrSyntax, ashlar.RootPointer, ashlar.Origin{}, "path is empty", nil)
	}
	expanded := trimmed
	if strings.HasPrefix(trimmed, "~") {
		if trimmed != "~" && !strings.HasPrefix(trimmed, "~/") && !strings.HasPrefix(trimmed, `~\`) {
			return "", ashlar.NewError(
				ashlar.ErrSyntax,
				ashlar.RootPointer,
				ashlar.Origin{},
				fmt.Sprintf("unsupported tilde user expansion in path %q", rawPath),
				nil,
			)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", ashlar.NewError(ashlar.ErrSyntax, ashlar.RootPointer, ashlar.Origin{}, "resolve home directory", err)
		}
		if trimmed == "~" {
			expanded = home
		} else {
			expanded = filepath.Join(home, trimmed[2:])
		}
	}
	abs, err := filepath.Abs(filepath.Clean(expanded))
	if err != nil {
		return "", ashlar.NewError(ashlar.ErrSyntax, ashlar.RootPointer, ashlar.Origin{}, "resolve absolute path", err)
	}
	return abs, nil
}

// ResolveRegularFile expands rawPath, resolves symbolic links, and verifies the
// target exists and is a regular file using os.OpenRoot.
func ResolveRegularFile(rawPath string) (string, error) {
	expanded, err := ExpandPath(rawPath)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(expanded)
	if err != nil {
		cat := ashlar.ErrSyntax
		if errors.Is(err, fs.ErrNotExist) {
			cat = ashlar.ErrNotFound
		}
		return "", ashlar.NewError(
			cat,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: rawPath}),
			fmt.Sprintf("resolve file %q", rawPath),
			err,
		)
	}
	info, err := statInRoot(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", ashlar.NewError(
			ashlar.ErrValidation,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: resolved}),
			fmt.Sprintf("path %q is not a regular file", resolved),
			nil,
		)
	}
	return resolved, nil
}

func statInRoot(resolved string) (fs.FileInfo, error) {
	dir := filepath.Dir(resolved)
	name := filepath.Base(resolved)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, ashlar.NewError(
			ashlar.ErrNotFound,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: resolved}),
			fmt.Sprintf("open parent directory %q", dir),
			err,
		)
	}
	defer func() { _ = root.Close() }()

	info, err := root.Stat(name)
	if err != nil {
		return nil, ashlar.NewError(
			ashlar.ErrNotFound,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: resolved}),
			fmt.Sprintf("stat regular file %q", resolved),
			err,
		)
	}
	return info, nil
}

// DetectSymlinks reports any paths in the slice whose EvalSymlinks target
// differs from the cleaned path.
func DetectSymlinks(paths []string) ([]SymlinkWarning, error) {
	var warnings []SymlinkWarning
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		expanded, err := ExpandPath(p)
		if err != nil {
			return nil, err
		}
		if seen[expanded] {
			continue
		}
		seen[expanded] = true
		resolved, err := filepath.EvalSymlinks(expanded)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, ashlar.NewError(
				ashlar.ErrSyntax,
				ashlar.RootPointer,
				ashlar.FileOrigin(ashlar.SourceSpan{Source: p}),
				fmt.Sprintf("evaluate symlinks for %q", p),
				err,
			)
		}
		if filepath.Clean(resolved) != filepath.Clean(expanded) {
			warnings = append(warnings, SymlinkWarning{
				Path:     expanded,
				Resolved: resolved,
			})
		}
	}
	return warnings, nil
}

// ComputeFileHash returns the SHA-256 digest of a regular file formatted as
// "sha256:<hex>".
func ComputeFileHash(rawPath string) (string, error) {
	data, err := readRegularFileBytes(rawPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ComputeKeyedFileHash returns the HMAC-SHA256 digest of a regular file using a
// 32-byte key formatted as "sha256:<hex>".
func ComputeKeyedFileHash(rawPath string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", ashlar.NewError(
			ashlar.ErrValidation,
			ashlar.RootPointer,
			ashlar.Origin{},
			fmt.Sprintf("HMAC key must be exactly 32 bytes (got %d)", len(key)),
			nil,
		)
	}
	data, err := readRegularFileBytes(rawPath)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return "sha256:" + hex.EncodeToString(mac.Sum(nil)), nil
}

func readRegularFileBytes(rawPath string) ([]byte, error) {
	resolved, err := ResolveRegularFile(rawPath)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(resolved)
	name := filepath.Base(resolved)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, ashlar.NewError(ashlar.ErrNotFound, ashlar.RootPointer, ashlar.Origin{}, "open root directory", err)
	}
	defer func() { _ = root.Close() }()

	f, err := root.Open(name)
	if err != nil {
		return nil, ashlar.NewError(ashlar.ErrNotFound, ashlar.RootPointer, ashlar.Origin{}, "open file", err)
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// ResolveRelativePaths resolves the string values at the specified RFC 6901
// pointers relative to their declaring fragment's directory (from each node's
// FileOrigin.Span.Source, falling back to fallbackBaseDir), returning a new
// immutable ashlar.Value tree with provenance preserved.
func ResolveRelativePaths(root ashlar.Value, fallbackBaseDir string, pointers ...ashlar.Pointer) (ashlar.Value, error) {
	targetSet := make(map[ashlar.Pointer]bool, len(pointers))
	for _, p := range pointers {
		targetSet[p] = true
	}
	return resolvePathsAt(root, ashlar.RootPointer, fallbackBaseDir, targetSet)
}

func resolvePathsAt(
	val ashlar.Value,
	ptr ashlar.Pointer,
	fallbackBaseDir string,
	targets map[ashlar.Pointer]bool,
) (ashlar.Value, error) {
	if targets[ptr] && val.Kind() == ashlar.KindString {
		rawStr, _ := val.StringVal()
		if rawStr == "" || path.IsAbs(rawStr) || filepath.IsAbs(rawStr) {
			return val, nil
		}
		baseDir := fallbackBaseDir
		if src := val.Provenance().Effective.Span.Source; src != "" {
			if dir := path.Dir(filepath.ToSlash(src)); dir != "." && dir != "" {
				baseDir = dir
			}
		}
		joined := path.Clean(path.Join(filepath.ToSlash(baseDir), filepath.ToSlash(rawStr)))
		return ashlar.StringValue(joined, val.Provenance()), nil
	}
	switch val.Kind() {
	case ashlar.KindObject:
		members := val.Members()
		for i, m := range members {
			updated, err := resolvePathsAt(m.Value, ptr.Child(m.Key), fallbackBaseDir, targets)
			if err != nil {
				return ashlar.Value{}, err
			}
			members[i].Value = updated
		}
		return ashlar.ObjectValue(members, val.Provenance())
	case ashlar.KindArray:
		elems := val.Elements()
		for i, el := range elems {
			updated, err := resolvePathsAt(el, ptr.Index(i), fallbackBaseDir, targets)
			if err != nil {
				return ashlar.Value{}, err
			}
			elems[i] = updated
		}
		return ashlar.ArrayValue(elems, val.Provenance()), nil
	default:
		return val, nil
	}
}
