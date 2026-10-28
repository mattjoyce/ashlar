package jsoncontract

import (
	"errors"
	"fmt"
	"io/fs"
	"unicode/utf8"

	"github.com/mattjoyce/ashlar"
)

const maxNestingDepth = 1000

// Document is an immutable, verified single-document JSON contract.
//
// It preserves a defensive copy of the original raw bytes and an immutable
// ashlar.Value root tree with source coordinates and exact numeric lexemes.
type Document struct {
	source string
	raw    []byte
	root   ashlar.Value
}

// Read parses raw bytes as a single strict JSON document from source.
//
// It fails with ashlar.ErrDuplicateKey on duplicate object keys at any nesting
// depth, ashlar.ErrTrailingContent on any token following the top-level JSON
// value, and ashlar.ErrSyntax on malformed JSON or invalid UTF-8. Original
// bytes and exact numeric lexemes are preserved without mutation.
func Read(source string, raw []byte) (Document, error) {
	if !utf8.Valid(raw) {
		return Document{}, ashlar.NewError(
			ashlar.ErrSyntax,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{
				Source: source,
				Start:  ashlar.SourcePos{Line: 1, Column: 1, Offset: 0},
			}),
			"document contains invalid UTF-8 bytes",
			nil,
		)
	}

	rawCopy := make([]byte, len(raw))
	copy(rawCopy, raw)

	p := &parser{lex: newLexer(source, rawCopy)}
	tok, err := p.lex.next(ashlar.RootPointer)
	if err != nil {
		return Document{}, err
	}
	if tok.kind == tokEOF {
		return Document{}, p.lex.syntaxError(tok.span, ashlar.RootPointer, "unexpected end of JSON input")
	}

	root, err := p.parseValue(tok, ashlar.RootPointer, 0)
	if err != nil {
		return Document{}, err
	}

	trailing, err := p.lex.next(ashlar.RootPointer)
	if err != nil {
		return Document{}, ashlar.NewError(
			ashlar.ErrTrailingContent,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: source, Start: p.lex.pos()}),
			"trailing content after top-level JSON value",
			err,
		)
	}
	if trailing.kind != tokEOF {
		return Document{}, ashlar.NewError(
			ashlar.ErrTrailingContent,
			ashlar.RootPointer,
			ashlar.FileOrigin(trailing.span),
			fmt.Sprintf("unexpected trailing token %q after top-level JSON value", trailing.raw),
			nil,
		)
	}

	return Document{
		source: source,
		raw:    rawCopy,
		root:   root,
	}, nil
}

// ReadFS reads and parses a single strict JSON document from fsys at path.
func ReadFS(fsys fs.FS, path string) (Document, error) {
	if fsys == nil {
		return Document{}, ashlar.NewError(
			ashlar.ErrNotFound,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: path}),
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
		return Document{}, ashlar.NewError(
			cat,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: path}),
			fmt.Sprintf("read JSON contract %q", path),
			err,
		)
	}
	return Read(path, raw)
}

// Source returns the logical source path or identifier of d.
func (d Document) Source() string {
	return d.source
}

// RawBytes returns a defensive copy of the original document bytes as read.
func (d Document) RawBytes() []byte {
	if d.raw == nil {
		return nil
	}
	cp := make([]byte, len(d.raw))
	copy(cp, d.raw)
	return cp
}

// Root returns the immutable root ashlar.Value of the document.
func (d Document) Root() ashlar.Value {
	return d.root
}

type parser struct {
	lex *lexer
}

func (p *parser) parseValue(tok token, ptr ashlar.Pointer, depth int) (ashlar.Value, error) {
	if depth > maxNestingDepth {
		return ashlar.Value{}, p.lex.syntaxError(tok.span, ptr, "exceeded maximum JSON nesting depth")
	}
	prov := ashlar.NewProvenance(ashlar.FileOrigin(tok.span), false)
	switch tok.kind {
	case tokNull:
		return ashlar.NullValue(prov), nil
	case tokTrue:
		return ashlar.BoolValue(true, prov), nil
	case tokFalse:
		return ashlar.BoolValue(false, prov), nil
	case tokString:
		return ashlar.StringValue(tok.str, prov), nil
	case tokNumber:
		return ashlar.NumberValue(tok.raw, prov)
	case tokLBrace:
		return p.parseObject(tok.span.Start, ptr, depth+1)
	case tokLBracket:
		return p.parseArray(tok.span.Start, ptr, depth+1)
	default:
		return ashlar.Value{}, p.lex.syntaxError(tok.span, ptr, fmt.Sprintf("unexpected token %q", tok.raw))
	}
}

func (p *parser) parseObject(start ashlar.SourcePos, ptr ashlar.Pointer, depth int) (ashlar.Value, error) {
	tok, err := p.lex.next(ptr)
	if err != nil {
		return ashlar.Value{}, err
	}
	if tok.kind == tokRBrace {
		span := ashlar.SourceSpan{Source: p.lex.source, Start: start, End: tok.span.End}
		return ashlar.ObjectValue(nil, ashlar.NewProvenance(ashlar.FileOrigin(span), false))
	}

	var members []ashlar.Member
	seen := make(map[string]ashlar.Origin)

	for {
		if tok.kind != tokString {
			return ashlar.Value{}, p.lex.syntaxError(tok.span, ptr, "expected string key in object")
		}
		keyOrigin := ashlar.FileOrigin(tok.span)
		memberPtr := ptr.Child(tok.str)

		if firstOrig, dup := seen[tok.str]; dup {
			return ashlar.Value{}, ashlar.NewError(
				ashlar.ErrDuplicateKey,
				memberPtr,
				keyOrigin,
				fmt.Sprintf("duplicate object key %q (first defined at %s)", tok.str, firstOrig.Location()),
				nil,
			)
		}

		val, err := p.parseObjectMemberValue(memberPtr, depth)
		if err != nil {
			return ashlar.Value{}, err
		}
		seen[tok.str] = keyOrigin
		members = append(members, ashlar.Member{
			Key:       tok.str,
			KeyOrigin: keyOrigin,
			Value:     val,
		})

		sep, err := p.lex.next(ptr)
		if err != nil {
			return ashlar.Value{}, err
		}
		if sep.kind == tokRBrace {
			span := ashlar.SourceSpan{Source: p.lex.source, Start: start, End: sep.span.End}
			return ashlar.ObjectValue(members, ashlar.NewProvenance(ashlar.FileOrigin(span), false))
		}
		if sep.kind != tokComma {
			return ashlar.Value{}, p.lex.syntaxError(sep.span, ptr, "expected ',' or '}' after object member")
		}
		tok, err = p.lex.next(ptr)
		if err != nil {
			return ashlar.Value{}, err
		}
	}
}

func (p *parser) parseObjectMemberValue(memberPtr ashlar.Pointer, depth int) (ashlar.Value, error) {
	colon, err := p.lex.next(memberPtr)
	if err != nil {
		return ashlar.Value{}, err
	}
	if colon.kind != tokColon {
		return ashlar.Value{}, p.lex.syntaxError(colon.span, memberPtr, "expected ':' after object key")
	}
	valTok, err := p.lex.next(memberPtr)
	if err != nil {
		return ashlar.Value{}, err
	}
	if valTok.kind == tokEOF {
		return ashlar.Value{}, p.lex.syntaxError(valTok.span, memberPtr, "unexpected end of input after ':'")
	}
	return p.parseValue(valTok, memberPtr, depth)
}

func (p *parser) parseArray(start ashlar.SourcePos, ptr ashlar.Pointer, depth int) (ashlar.Value, error) {
	tok, err := p.lex.next(ptr)
	if err != nil {
		return ashlar.Value{}, err
	}
	if tok.kind == tokRBracket {
		span := ashlar.SourceSpan{Source: p.lex.source, Start: start, End: tok.span.End}
		return ashlar.ArrayValue(nil, ashlar.NewProvenance(ashlar.FileOrigin(span), false)), nil
	}

	var elems []ashlar.Value
	for idx := 0; ; idx++ {
		elemPtr := ptr.Index(idx)
		val, err := p.parseValue(tok, elemPtr, depth)
		if err != nil {
			return ashlar.Value{}, err
		}
		elems = append(elems, val)

		sep, err := p.lex.next(ptr)
		if err != nil {
			return ashlar.Value{}, err
		}
		if sep.kind == tokRBracket {
			span := ashlar.SourceSpan{Source: p.lex.source, Start: start, End: sep.span.End}
			return ashlar.ArrayValue(elems, ashlar.NewProvenance(ashlar.FileOrigin(span), false)), nil
		}
		if sep.kind != tokComma {
			return ashlar.Value{}, p.lex.syntaxError(sep.span, ptr, "expected ',' or ']' after array element")
		}
		tok, err = p.lex.next(ptr)
		if err != nil {
			return ashlar.Value{}, err
		}
	}
}
