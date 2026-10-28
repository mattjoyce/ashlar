package yamlconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattjoyce/ashlar"
	"go.yaml.in/yaml/v3"
)

var (
	reYAMLMappingDup = regexp.MustCompile(`line (\d+): mapping key "([^"]+)" already defined at line (\d+)`)
	reYAMLLine       = regexp.MustCompile(`line (\d+):`)
)

// Read parses raw YAML bytes from source into an immutable ashlar.Value tree,
// attaching source coordinates (file:line:column) and optional include-chain
// spans (via) to every node.
//
// Read fails closed with:
//   - ashlar.ErrDuplicateKey on duplicate mapping keys at any depth;
//   - ashlar.ErrTrailingContent on multi-document YAML streams ("---");
//   - ashlar.ErrSyntax on malformed YAML, invalid UTF-8, aliases, or non-string
//     mapping keys.
func Read(source string, raw []byte, via ...ashlar.SourceSpan) (ashlar.Value, error) {
	if !utf8.Valid(raw) {
		return ashlar.Value{}, ashlar.NewError(
			ashlar.ErrSyntax,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{
				Source: source,
				Start:  ashlar.SourcePos{Line: 1, Column: 1},
			}, via...),
			"YAML document contains invalid UTF-8 bytes",
			nil,
		)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return ashlar.Value{}, ashlar.NewError(
			ashlar.ErrSyntax,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{
				Source: source,
				Start:  ashlar.SourcePos{Line: 1, Column: 1},
			}, via...),
			"YAML document is empty",
			nil,
		)
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var docNode yaml.Node
	if err := dec.Decode(&docNode); err != nil {
		return ashlar.Value{}, classifyYAMLError(source, raw, via, err)
	}

	var trailingNode yaml.Node
	err := dec.Decode(&trailingNode)
	if err == nil {
		pos := ashlar.SourcePos{Line: trailingNode.Line, Column: trailingNode.Column}
		if len(trailingNode.Content) > 0 && trailingNode.Line == 0 {
			pos = ashlar.SourcePos{
				Line:   trailingNode.Content[0].Line,
				Column: trailingNode.Content[0].Column,
			}
		}
		if !pos.Valid() {
			pos = ashlar.SourcePos{Line: 1, Column: 1}
		}
		return ashlar.Value{}, ashlar.NewError(
			ashlar.ErrTrailingContent,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: source, Start: pos}, via...),
			"multiple YAML documents are not permitted in a single configuration file",
			nil,
		)
	}
	if !errors.Is(err, io.EOF) {
		return ashlar.Value{}, ashlar.NewError(
			ashlar.ErrTrailingContent,
			ashlar.RootPointer,
			classifyYAMLOrigin(source, raw, via, err),
			"trailing content after YAML document",
			err,
		)
	}

	return convertDocumentNode(source, via, &docNode)
}

// ReadFS reads and parses a single strict YAML document from fsys at path.
func ReadFS(fsys fs.FS, path string, via ...ashlar.SourceSpan) (ashlar.Value, error) {
	if fsys == nil {
		return ashlar.Value{}, ashlar.NewError(
			ashlar.ErrNotFound,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: path}, via...),
			"filesystem is nil",
			nil,
		)
	}
	raw, err := fs.ReadFile(fsys, path)
	if err != nil {
		cat := ashlar.ErrSyntax
		if errors.Is(err, fs.ErrNotExist) {
			cat = ashlar.ErrNotFound
		}
		return ashlar.Value{}, ashlar.NewError(
			cat,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: path}, via...),
			fmt.Sprintf("read YAML file %q", path),
			err,
		)
	}
	return Read(path, raw, via...)
}

func classifyYAMLError(source string, raw []byte, via []ashlar.SourceSpan, err error) error {
	msg := err.Error()
	if m := reYAMLMappingDup.FindStringSubmatch(msg); len(m) == 4 {
		dupLine, _ := strconv.Atoi(m[1])
		key := m[2]
		origLine, _ := strconv.Atoi(m[3])
		dupCol := findKeyColumnOnLine(raw, dupLine, key)
		origCol := findKeyColumnOnLine(raw, origLine, key)
		dupSpan := ashlar.SourceSpan{
			Source: source,
			Start:  ashlar.SourcePos{Line: dupLine, Column: dupCol},
		}
		origSpan := ashlar.SourceSpan{
			Source: source,
			Start:  ashlar.SourcePos{Line: origLine, Column: origCol},
		}
		return ashlar.NewError(
			ashlar.ErrDuplicateKey,
			ashlar.NewPointer(key),
			ashlar.FileOrigin(dupSpan, via...),
			fmt.Sprintf("duplicate mapping key %q (first defined at %s)", key, origSpan.String()),
			err,
		)
	}
	return ashlar.NewError(
		ashlar.ErrSyntax,
		ashlar.RootPointer,
		classifyYAMLOrigin(source, raw, via, err),
		msg,
		err,
	)
}

func classifyYAMLOrigin(source string, raw []byte, via []ashlar.SourceSpan, err error) ashlar.Origin {
	pos := ashlar.SourcePos{Line: 1, Column: 1}
	if m := reYAMLLine.FindStringSubmatch(err.Error()); len(m) == 2 {
		if line, convErr := strconv.Atoi(m[1]); convErr == nil && line > 0 {
			pos = ashlar.SourcePos{Line: line, Column: firstNonSpaceColumn(raw, line)}
		}
	}
	return ashlar.FileOrigin(ashlar.SourceSpan{Source: source, Start: pos}, via...)
}

func findKeyColumnOnLine(raw []byte, line int, key string) int {
	lines := bytes.Split(raw, []byte{'\n'})
	if line < 1 || line > len(lines) {
		return 1
	}
	lineStr := string(lines[line-1])
	if idx := strings.Index(lineStr, key); idx >= 0 {
		return idx + 1
	}
	return firstNonSpaceColumn(raw, line)
}

func firstNonSpaceColumn(raw []byte, line int) int {
	lines := bytes.Split(raw, []byte{'\n'})
	if line < 1 || line > len(lines) {
		return 1
	}
	for i, b := range lines[line-1] {
		if b != ' ' && b != '\t' {
			return i + 1
		}
	}
	return 1
}

func convertDocumentNode(source string, via []ashlar.SourceSpan, doc *yaml.Node) (ashlar.Value, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		span := ashlar.SourceSpan{Source: source, Start: ashlar.SourcePos{Line: 1, Column: 1}}
		return ashlar.NullValue(ashlar.NewProvenance(ashlar.FileOrigin(span, via...), false)), nil
	}
	return convertNode(source, via, ashlar.RootPointer, doc.Content[0])
}

func convertNode(source string, via []ashlar.SourceSpan, ptr ashlar.Pointer, n *yaml.Node) (ashlar.Value, error) {
	span := ashlar.SourceSpan{
		Source: source,
		Start:  ashlar.SourcePos{Line: n.Line, Column: n.Column},
	}
	orig := ashlar.FileOrigin(span, via...)
	prov := ashlar.NewProvenance(orig, false)

	switch n.Kind {
	case yaml.MappingNode:
		return convertMappingNode(source, via, ptr, n, prov)
	case yaml.SequenceNode:
		return convertSequenceNode(source, via, ptr, n, prov)
	case yaml.ScalarNode:
		return convertScalarNode(ptr, n, prov)
	case yaml.AliasNode:
		return ashlar.Value{}, ashlar.NewError(
			ashlar.ErrSyntax,
			ptr,
			orig,
			"YAML aliases are not permitted in strict configuration documents",
			nil,
		)
	default:
		return ashlar.Value{}, ashlar.NewError(
			ashlar.ErrSyntax,
			ptr,
			orig,
			fmt.Sprintf("unsupported YAML node kind %v", n.Kind),
			nil,
		)
	}
}

func convertMappingNode(
	source string,
	via []ashlar.SourceSpan,
	ptr ashlar.Pointer,
	n *yaml.Node,
	prov ashlar.Provenance,
) (ashlar.Value, error) {
	members := make([]ashlar.Member, 0, len(n.Content)/2)
	seen := make(map[string]ashlar.Origin, len(n.Content)/2)

	for i := 0; i+1 < len(n.Content); i += 2 {
		kNode := n.Content[i]
		vNode := n.Content[i+1]
		kSpan := ashlar.SourceSpan{
			Source: source,
			Start:  ashlar.SourcePos{Line: kNode.Line, Column: kNode.Column},
		}
		kOrig := ashlar.FileOrigin(kSpan, via...)

		if kNode.Kind != yaml.ScalarNode || kNode.ShortTag() != "!!str" {
			return ashlar.Value{}, ashlar.NewError(
				ashlar.ErrSyntax,
				ptr,
				kOrig,
				fmt.Sprintf("YAML mapping key %q must be a string scalar (got %s)", kNode.Value, kNode.ShortTag()),
				nil,
			)
		}

		childPtr := ptr.Child(kNode.Value)
		if firstOrig, dup := seen[kNode.Value]; dup {
			return ashlar.Value{}, ashlar.NewError(
				ashlar.ErrDuplicateKey,
				childPtr,
				kOrig,
				fmt.Sprintf("duplicate mapping key %q (first defined at %s)", kNode.Value, firstOrig.Location()),
				nil,
			)
		}

		val, err := convertNode(source, via, childPtr, vNode)
		if err != nil {
			return ashlar.Value{}, err
		}
		seen[kNode.Value] = kOrig
		members = append(members, ashlar.Member{
			Key:       kNode.Value,
			KeyOrigin: kOrig,
			Value:     val,
		})
	}
	return ashlar.ObjectValue(members, prov)
}

func convertSequenceNode(
	source string,
	via []ashlar.SourceSpan,
	ptr ashlar.Pointer,
	n *yaml.Node,
	prov ashlar.Provenance,
) (ashlar.Value, error) {
	elems := make([]ashlar.Value, len(n.Content))
	for i, child := range n.Content {
		val, err := convertNode(source, via, ptr.Index(i), child)
		if err != nil {
			return ashlar.Value{}, err
		}
		elems[i] = val
	}
	return ashlar.ArrayValue(elems, prov), nil
}

func convertScalarNode(ptr ashlar.Pointer, n *yaml.Node, prov ashlar.Provenance) (ashlar.Value, error) {
	switch n.ShortTag() {
	case "!!null":
		return ashlar.NullValue(prov), nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return ashlar.Value{}, ashlar.NewError(ashlar.ErrSyntax, ptr, prov.Effective, "invalid YAML boolean", err)
		}
		return ashlar.BoolValue(b, prov), nil
	case "!!int":
		return convertYAMLInt(ptr, n, prov)
	case "!!float":
		return convertYAMLFloat(ptr, n, prov)
	default:
		return ashlar.StringValue(n.Value, prov), nil
	}
}

func convertYAMLInt(ptr ashlar.Pointer, n *yaml.Node, prov ashlar.Provenance) (ashlar.Value, error) {
	cleaned := strings.ReplaceAll(n.Value, "_", "")
	if v, err := ashlar.NumberValue(cleaned, prov); err == nil {
		return v, nil
	}
	var i64 int64
	if err := n.Decode(&i64); err == nil {
		return ashlar.NumberValue(strconv.FormatInt(i64, 10), prov)
	}
	var u64 uint64
	if err := n.Decode(&u64); err == nil {
		return ashlar.NumberValue(strconv.FormatUint(u64, 10), prov)
	}
	return ashlar.Value{}, ashlar.NewError(
		ashlar.ErrSyntax,
		ptr,
		prov.Effective,
		fmt.Sprintf("invalid YAML integer %q", n.Value),
		nil,
	)
}

func convertYAMLFloat(ptr ashlar.Pointer, n *yaml.Node, prov ashlar.Provenance) (ashlar.Value, error) {
	cleaned := strings.ReplaceAll(n.Value, "_", "")
	if v, err := ashlar.NumberValue(cleaned, prov); err == nil {
		return v, nil
	}
	var f64 float64
	if err := n.Decode(&f64); err == nil {
		return ashlar.NumberValue(strconv.FormatFloat(f64, 'g', -1, 64), prov)
	}
	return ashlar.Value{}, ashlar.NewError(
		ashlar.ErrSyntax,
		ptr,
		prov.Effective,
		fmt.Sprintf("invalid YAML float %q", n.Value),
		nil,
	)
}
