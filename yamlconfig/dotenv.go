package yamlconfig

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattjoyce/ashlar"
)

// ParseDotEnv parses a .env byte stream (supporting '#' comments, blank lines,
// optional "export " prefixes, and single- or double-quoted values) into
// key-value pairs, returning source-located ashlar.ErrSyntax diagnostics on
// malformed lines.
func ParseDotEnv(source string, raw []byte) (map[string]string, error) {
	if !utf8.Valid(raw) {
		return nil, ashlar.NewError(
			ashlar.ErrSyntax,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{
				Source: source,
				Start:  ashlar.SourcePos{Line: 1, Column: 1},
			}),
			".env content contains invalid UTF-8 bytes",
			nil,
		)
	}

	vars := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, dotEnvSyntaxErr(source, lineNum, "expected KEY=VALUE assignment")
		}
		key = strings.TrimSpace(key)
		if !isValidEnvName(key) {
			return nil, dotEnvSyntaxErr(source, lineNum, fmt.Sprintf("invalid environment variable name %q", key))
		}
		val = unquoteDotEnvValue(strings.TrimSpace(val))
		vars[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, ashlar.NewError(
			ashlar.ErrSyntax,
			ashlar.RootPointer,
			ashlar.FileOrigin(ashlar.SourceSpan{Source: source}),
			"scan .env content",
			err,
		)
	}
	return vars, nil
}

func dotEnvSyntaxErr(source string, line int, msg string) error {
	return ashlar.NewError(
		ashlar.ErrSyntax,
		ashlar.RootPointer,
		ashlar.FileOrigin(ashlar.SourceSpan{
			Source: source,
			Start:  ashlar.SourcePos{Line: line, Column: 1},
		}),
		msg,
		nil,
	)
}

func isValidEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		isAlpha := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
		isDigit := c >= '0' && c <= '9'
		if i == 0 && !isAlpha {
			return false
		}
		if !isAlpha && !isDigit {
			return false
		}
	}
	return true
}

func unquoteDotEnvValue(val string) string {
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			return val[1 : len(val)-1]
		}
	}
	return val
}
