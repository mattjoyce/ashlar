package jsoncontract

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/mattjoyce/ashlar"
)

type tokenKind uint8

const (
	tokEOF tokenKind = iota
	tokLBrace
	tokRBrace
	tokLBracket
	tokRBracket
	tokColon
	tokComma
	tokString
	tokNumber
	tokTrue
	tokFalse
	tokNull
)

type token struct {
	kind tokenKind
	span ashlar.SourceSpan
	raw  string
	str  string
}

type lexer struct {
	source string
	src    []byte
	offset int
	line   int
	col    int
}

func newLexer(source string, src []byte) *lexer {
	return &lexer{
		source: source,
		src:    src,
		offset: 0,
		line:   1,
		col:    1,
	}
}

func (l *lexer) pos() ashlar.SourcePos {
	return ashlar.SourcePos{
		Line:   l.line,
		Column: l.col,
		Offset: int64(l.offset),
	}
}

func (l *lexer) spanFrom(start ashlar.SourcePos) ashlar.SourceSpan {
	return ashlar.SourceSpan{
		Source: l.source,
		Start:  start,
		End:    l.pos(),
	}
}

func (l *lexer) advanceByte() byte {
	b := l.src[l.offset]
	l.offset++
	if b == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return b
}

func (l *lexer) skipWhitespace() {
	for l.offset < len(l.src) {
		switch l.src[l.offset] {
		case ' ', '\t', '\r', '\n':
			l.advanceByte()
		default:
			return
		}
	}
}

func (l *lexer) syntaxError(span ashlar.SourceSpan, ptr ashlar.Pointer, msg string) error {
	return ashlar.NewError(ashlar.ErrSyntax, ptr, ashlar.FileOrigin(span), msg, nil)
}

func (l *lexer) next(ptr ashlar.Pointer) (token, error) {
	l.skipWhitespace()
	start := l.pos()
	if l.offset >= len(l.src) {
		return token{kind: tokEOF, span: l.spanFrom(start)}, nil
	}

	b := l.src[l.offset]
	switch b {
	case '{':
		l.advanceByte()
		return token{kind: tokLBrace, span: l.spanFrom(start), raw: "{"}, nil
	case '}':
		l.advanceByte()
		return token{kind: tokRBrace, span: l.spanFrom(start), raw: "}"}, nil
	case '[':
		l.advanceByte()
		return token{kind: tokLBracket, span: l.spanFrom(start), raw: "["}, nil
	case ']':
		l.advanceByte()
		return token{kind: tokRBracket, span: l.spanFrom(start), raw: "]"}, nil
	case ':':
		l.advanceByte()
		return token{kind: tokColon, span: l.spanFrom(start), raw: ":"}, nil
	case ',':
		l.advanceByte()
		return token{kind: tokComma, span: l.spanFrom(start), raw: ","}, nil
	case '"':
		return l.scanString(start, ptr)
	case 't':
		return l.scanLiteral(start, ptr, "true", tokTrue)
	case 'f':
		return l.scanLiteral(start, ptr, "false", tokFalse)
	case 'n':
		return l.scanLiteral(start, ptr, "null", tokNull)
	default:
		if b == '-' || (b >= '0' && b <= '9') {
			return l.scanNumber(start, ptr)
		}
		l.advanceByte()
		return token{}, l.syntaxError(l.spanFrom(start), ptr, fmt.Sprintf("unexpected character %q", b))
	}
}

func (l *lexer) scanLiteral(start ashlar.SourcePos, ptr ashlar.Pointer, lit string, kind tokenKind) (token, error) {
	for i := 0; i < len(lit); i++ {
		if l.offset >= len(l.src) || l.src[l.offset] != lit[i] {
			if l.offset < len(l.src) {
				l.advanceByte()
			}
			return token{}, l.syntaxError(l.spanFrom(start), ptr, fmt.Sprintf("invalid literal (expected %q)", lit))
		}
		l.advanceByte()
	}
	return token{kind: kind, span: l.spanFrom(start), raw: lit}, nil
}

func (l *lexer) scanString(start ashlar.SourcePos, ptr ashlar.Pointer) (token, error) {
	l.advanceByte() // consume opening '"'
	for l.offset < len(l.src) {
		b := l.src[l.offset]
		if b < 0x20 {
			l.advanceByte()
			return token{}, l.syntaxError(l.spanFrom(start), ptr, "unescaped control character in string literal")
		}
		if b == '"' {
			l.advanceByte()
			rawBytes := l.src[start.Offset:l.offset]
			if !utf8.Valid(rawBytes) {
				return token{}, l.syntaxError(l.spanFrom(start), ptr, "invalid UTF-8 in string literal")
			}
			var decoded string
			if err := json.Unmarshal(rawBytes, &decoded); err != nil {
				return token{}, l.syntaxError(l.spanFrom(start), ptr, fmt.Sprintf("invalid string escape: %v", err))
			}
			return token{
				kind: tokString,
				span: l.spanFrom(start),
				raw:  string(rawBytes),
				str:  decoded,
			}, nil
		}
		if b == '\\' {
			l.advanceByte()
			if l.offset >= len(l.src) {
				return token{}, l.syntaxError(l.spanFrom(start), ptr, "unterminated string escape")
			}
			l.advanceByte()
			continue
		}
		l.advanceByte()
	}
	return token{}, l.syntaxError(l.spanFrom(start), ptr, "unterminated string literal")
}

func (l *lexer) scanNumber(start ashlar.SourcePos, ptr ashlar.Pointer) (token, error) {
	if l.src[l.offset] == '-' {
		l.advanceByte()
		if l.offset >= len(l.src) || !isDigit(l.src[l.offset]) {
			return token{}, l.syntaxError(l.spanFrom(start), ptr, "expected digit after '-' in number")
		}
	}
	if l.src[l.offset] == '0' {
		l.advanceByte()
		if l.offset < len(l.src) && isDigit(l.src[l.offset]) {
			l.advanceByte()
			return token{}, l.syntaxError(l.spanFrom(start), ptr, "leading zero in numeric literal is not permitted")
		}
	} else {
		for l.offset < len(l.src) && isDigit(l.src[l.offset]) {
			l.advanceByte()
		}
	}
	if err := l.scanFractionAndExponent(start, ptr); err != nil {
		return token{}, err
	}
	raw := string(l.src[start.Offset:l.offset])
	return token{kind: tokNumber, span: l.spanFrom(start), raw: raw}, nil
}

func (l *lexer) scanFractionAndExponent(start ashlar.SourcePos, ptr ashlar.Pointer) error {
	if l.offset < len(l.src) && l.src[l.offset] == '.' {
		l.advanceByte()
		if l.offset >= len(l.src) || !isDigit(l.src[l.offset]) {
			return l.syntaxError(l.spanFrom(start), ptr, "expected digit after decimal point in number")
		}
		for l.offset < len(l.src) && isDigit(l.src[l.offset]) {
			l.advanceByte()
		}
	}
	if l.offset < len(l.src) && (l.src[l.offset] == 'e' || l.src[l.offset] == 'E') {
		l.advanceByte()
		if l.offset < len(l.src) && (l.src[l.offset] == '+' || l.src[l.offset] == '-') {
			l.advanceByte()
		}
		if l.offset >= len(l.src) || !isDigit(l.src[l.offset]) {
			return l.syntaxError(l.spanFrom(start), ptr, "expected digit in numeric exponent")
		}
		for l.offset < len(l.src) && isDigit(l.src[l.offset]) {
			l.advanceByte()
		}
	}
	return nil
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
