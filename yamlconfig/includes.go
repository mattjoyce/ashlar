package yamlconfig

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/mattjoyce/ashlar"
)

// ByteTransformer is a narrow hook invoked on raw file bytes before YAML or
// environment file parsing (e.g., for in-memory age decryption).
type ByteTransformer func(ctx context.Context, path string, raw []byte) ([]byte, error)

// IntegrityVerifier is a narrow hook invoked on discovered or included file
// paths before admission (e.g., for SHA-256 .checksums verification).
type IntegrityVerifier func(ctx context.Context, path string, raw []byte) error

// Fragment represents a single verified, parsed YAML configuration fragment in
// deterministic include-traversal order.
type Fragment struct {
	Path string
	Raw  []byte
	Root ashlar.Value
	Via  []ashlar.SourceSpan
}

// IncludeOptions configures LoadIncludeGraph.
type IncludeOptions struct {
	// IncludeKey is the top-level mapping key holding include paths (defaults to "include").
	IncludeKey  string
	Transformer ByteTransformer
	Verifier    IntegrityVerifier
}

type includeSpec struct {
	target string
	span   ashlar.SourceSpan
	ptr    ashlar.Pointer
}

type graphLoader struct {
	fsys       fs.FS
	includeKey string
	opts       IncludeOptions
	visited    map[string]bool
	fragments  []Fragment
}

// LoadIncludeGraph traverses the include graph starting at entryPath in fsys,
// returning the ordered fragments (root first, followed by included fragments
// in depth-first declaration order).
//
// It fails closed on direct or indirect include cycles (ashlar.ErrComposition),
// missing included files (ashlar.ErrNotFound), path escapes outside fsys, or
// failed IntegrityVerifier / ByteTransformer hooks.
func LoadIncludeGraph(ctx context.Context, fsys fs.FS, entryPath string, opts IncludeOptions) ([]Fragment, error) {
	if fsys == nil {
		return nil, ashlar.NewError(
			ashlar.ErrNotFound,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: entryPath}),
			"filesystem is nil",
			nil,
		)
	}
	incKey := strings.TrimSpace(opts.IncludeKey)
	if incKey == "" {
		incKey = "include"
	}
	cleanEntry, err := cleanFSPath(entryPath, ashlar.RootPointer, ashlar.Origin{})
	if err != nil {
		return nil, err
	}

	loader := &graphLoader{
		fsys:       fsys,
		includeKey: incKey,
		opts:       opts,
		visited:    make(map[string]bool),
	}
	if err := loader.loadFile(ctx, cleanEntry, nil, []string{cleanEntry}); err != nil {
		return nil, err
	}
	return loader.fragments, nil
}

func (g *graphLoader) loadFile(
	ctx context.Context,
	cleanPath string,
	via []ashlar.SourceSpan,
	chain []string,
) error {
	if err := ctx.Err(); err != nil {
		return ashlar.NewError(
			ashlar.ErrComposition,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: cleanPath}, via...),
			"context canceled during include traversal",
			err,
		)
	}
	g.visited[cleanPath] = true

	raw, err := g.readAndVerify(ctx, cleanPath, via)
	if err != nil {
		return err
	}

	val, err := Read(cleanPath, raw, via...)
	if err != nil {
		return err
	}

	strippedVal, specs, err := extractIncludes(val, g.includeKey)
	if err != nil {
		return err
	}

	g.fragments = append(g.fragments, Fragment{
		Path: cleanPath,
		Raw:  raw,
		Root: strippedVal,
		Via:  append([]ashlar.SourceSpan(nil), via...),
	})

	baseDir := path.Dir(cleanPath)
	for _, spec := range specs {
		childVia := append(append([]ashlar.SourceSpan(nil), via...), spec.span)
		if err := g.expandIncludeTarget(ctx, baseDir, spec, childVia, chain); err != nil {
			return err
		}
	}
	return nil
}

func (g *graphLoader) readAndVerify(
	ctx context.Context,
	cleanPath string,
	via []ashlar.SourceSpan,
) ([]byte, error) {
	orig := ashlar.FileOrigin(ashlar.SourceSpan{Source: cleanPath}, via...)
	raw, err := fs.ReadFile(g.fsys, cleanPath)
	if err != nil {
		cat := ashlar.ErrComposition
		if errors.Is(err, fs.ErrNotExist) {
			cat = ashlar.ErrNotFound
		}
		return nil, ashlar.NewError(cat, ashlar.RootPointer, orig, fmt.Sprintf("read %q", cleanPath), err)
	}
	if g.opts.Verifier != nil {
		if err := g.opts.Verifier(ctx, cleanPath, raw); err != nil {
			return nil, ashlar.NewError(
				ashlar.ErrComposition,
				ashlar.RootPointer,
				orig,
				fmt.Sprintf("integrity verification failed for %q", cleanPath),
				err,
			)
		}
	}
	if g.opts.Transformer != nil {
		transformed, err := g.opts.Transformer(ctx, cleanPath, raw)
		if err != nil {
			return nil, ashlar.NewError(
				ashlar.ErrComposition,
				ashlar.RootPointer,
				orig,
				fmt.Sprintf("transform bytes for %q", cleanPath),
				err,
			)
		}
		raw = transformed
	}
	return raw, nil
}

func (g *graphLoader) expandIncludeTarget(
	ctx context.Context,
	baseDir string,
	spec includeSpec,
	childVia []ashlar.SourceSpan,
	chain []string,
) error {
	orig := ashlar.FileOrigin(spec.span, childVia[:len(childVia)-1]...)
	joined := spec.target
	if baseDir != "." && baseDir != "" && !strings.HasPrefix(spec.target, "/") {
		joined = path.Join(baseDir, spec.target)
	}
	cleanTarget, err := cleanFSPath(joined, spec.ptr, orig)
	if err != nil {
		return err
	}

	info, err := fs.Stat(g.fsys, cleanTarget)
	if err != nil {
		cat := ashlar.ErrComposition
		if errors.Is(err, fs.ErrNotExist) {
			cat = ashlar.ErrNotFound
		}
		return ashlar.NewError(
			cat,
			spec.ptr,
			orig,
			fmt.Sprintf("included path %q not found", cleanTarget),
			err,
		)
	}

	if info.IsDir() {
		files, err := walkYAMLDir(g.fsys, cleanTarget)
		if err != nil {
			return ashlar.NewError(ashlar.ErrComposition, spec.ptr, orig, "walk included directory", err)
		}
		for _, f := range files {
			if err := g.checkAndLoadTarget(ctx, f, spec.ptr, orig, childVia, chain); err != nil {
				return err
			}
		}
		return nil
	}
	return g.checkAndLoadTarget(ctx, cleanTarget, spec.ptr, orig, childVia, chain)
}

func (g *graphLoader) checkAndLoadTarget(
	ctx context.Context,
	targetPath string,
	ptr ashlar.Pointer,
	siteOrigin ashlar.Origin,
	childVia []ashlar.SourceSpan,
	chain []string,
) error {
	nextChain := append(append([]string(nil), chain...), targetPath)
	if g.visited[targetPath] {
		return ashlar.NewError(
			ashlar.ErrIncludeCycle,
			ptr,
			siteOrigin,
			fmt.Sprintf("include cycle or duplicate fragment detected: %s", strings.Join(nextChain, " -> ")),
			nil,
		)
	}
	return g.loadFile(ctx, targetPath, childVia, nextChain)
}

func extractIncludes(val ashlar.Value, includeKey string) (ashlar.Value, []includeSpec, error) {
	if val.Kind() != ashlar.KindObject {
		return val, nil, nil
	}
	incMember, ok := val.LookupMember(includeKey)
	if !ok {
		return val, nil, nil
	}
	incPtr := ashlar.NewPointer(includeKey)
	var specs []includeSpec
	switch incMember.Value.Kind() {
	case ashlar.KindString:
		s, _ := incMember.Value.StringVal()
		if strings.TrimSpace(s) == "" {
			return ashlar.Value{}, nil, ashlar.NewError(
				ashlar.ErrComposition,
				incPtr,
				incMember.Value.Provenance().Effective,
				"include path must not be empty",
				nil,
			)
		}
		specs = append(specs, includeSpec{
			target: s,
			span:   incMember.Value.Provenance().Effective.Span,
			ptr:    incPtr,
		})
	case ashlar.KindArray:
		for i, el := range incMember.Value.Elements() {
			elPtr := incPtr.Index(i)
			s, ok := el.StringVal()
			if !ok || strings.TrimSpace(s) == "" {
				return ashlar.Value{}, nil, ashlar.NewError(
					ashlar.ErrComposition,
					elPtr,
					el.Provenance().Effective,
					"include entry must be a non-empty string",
					nil,
				)
			}
			specs = append(specs, includeSpec{
				target: s,
				span:   el.Provenance().Effective.Span,
				ptr:    elPtr,
			})
		}
	default:
		return ashlar.Value{}, nil, ashlar.NewError(
			ashlar.ErrComposition,
			incPtr,
			incMember.Value.Provenance().Effective,
			fmt.Sprintf("%q must be a string or sequence of strings", includeKey),
			nil,
		)
	}

	var remaining []ashlar.Member
	for _, m := range val.Members() {
		if m.Key != includeKey {
			remaining = append(remaining, m)
		}
	}
	stripped, err := ashlar.ObjectValue(remaining, val.Provenance())
	return stripped, specs, err
}

func walkYAMLDir(fsys fs.FS, dir string) ([]string, error) {
	var files []string
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(path.Ext(p))
		if ext == ".yaml" || ext == ".yml" {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func cleanFSPath(raw string, ptr ashlar.Pointer, orig ashlar.Origin) (string, error) {
	trimmed := strings.TrimPrefix(path.Clean(strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")), "/")
	if !fs.ValidPath(trimmed) || strings.HasPrefix(trimmed, "..") {
		return "", ashlar.NewError(
			ashlar.ErrComposition,
			ptr,
			orig,
			fmt.Sprintf("invalid or escaping include path %q", raw),
			nil,
		)
	}
	return trimmed, nil
}
