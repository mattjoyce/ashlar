package ashlar

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// RedactedLiteral is the canonical replacement string for sensitive values in
// errors, diagnostics, and provenance explanations.
const RedactedLiteral = "[REDACTED]"

// Redactor determines whether a field path or key holds sensitive material that
// must be redacted in diagnostics, error messages, and provenance reports.
type Redactor interface {
	IsSensitive(ptr Pointer, key string) bool
}

// RedactorFunc adapts a function to the Redactor interface.
type RedactorFunc func(ptr Pointer, key string) bool

// IsSensitive implements Redactor.
func (f RedactorFunc) IsSensitive(ptr Pointer, key string) bool {
	if f == nil {
		return false
	}
	return f(ptr, key)
}

var defaultSecretSubstrings = []string{
	"token",
	"api_key",
	"secret",
	"password",
	"passwd",
	"credential",
	"private_key",
	"passphrase",
	"bearer",
}

var defaultSafeSuffixes = []string{
	"_ref",
	"_file",
	"_path",
	"_dir",
	"_sha256",
	"_hash",
}

// DefaultRedactor returns a Redactor that classifies common secret-bearing keys
// (token, api_key, secret, password, credential, private_key, passphrase, bearer)
// as sensitive while exempting reference, path, and digest suffixes (_ref, _file,
// _path, _dir, _sha256, _hash).
func DefaultRedactor() Redactor {
	return NewKeyRedactor(defaultSecretSubstrings, defaultSafeSuffixes)
}

// NewKeyRedactor constructs a Redactor that matches the target field key (or the
// leaf key of ptr when key is empty) against secretSubstrings (after lowercasing
// and normalizing '-' to '_'), unless the normalized key ends with one of safeSuffixes.
func NewKeyRedactor(secretSubstrings, safeSuffixes []string) Redactor {
	patterns := normalizeList(secretSubstrings)
	safe := normalizeList(safeSuffixes)
	return RedactorFunc(func(ptr Pointer, key string) bool {
		target := key
		if target == "" {
			target = leafKey(ptr)
		}
		if target == "" {
			return false
		}
		return matchesKeyPolicy(target, patterns, safe)
	})
}

func leafKey(ptr Pointer) string {
	tokens, err := ptr.Tokens()
	if err != nil || len(tokens) == 0 {
		return ptr.LastToken()
	}
	for i := len(tokens) - 1; i >= 0; i-- {
		if !isDigitsOnly(tokens[i]) {
			return tokens[i]
		}
	}
	return tokens[len(tokens)-1]
}

func isDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// CombineRedactors returns a Redactor that reports true if any non-nil redactor
// in the slice reports true.
func CombineRedactors(redactors ...Redactor) Redactor {
	return RedactorFunc(func(ptr Pointer, key string) bool {
		for _, r := range redactors {
			if r != nil && r.IsSensitive(ptr, key) {
				return true
			}
		}
		return false
	})
}

// IsSensitivePath checks whether r (or DefaultRedactor when r is nil) classifies
// ptr or key as sensitive.
func IsSensitivePath(r Redactor, ptr Pointer, key string) bool {
	if r == nil {
		r = DefaultRedactor()
	}
	return r.IsSensitive(ptr, key)
}

// RedactString returns RedactedLiteral when sensitive is true, and value otherwise.
func RedactString(value string, sensitive bool) string {
	if sensitive {
		return RedactedLiteral
	}
	return value
}

// ScrubSecret replaces every non-empty occurrence of secret in text with RedactedLiteral.
func ScrubSecret(text, secret string) string {
	if secret == "" || text == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, RedactedLiteral)
}

// SecretFingerprint computes a non-reversible, domain-separated SHA-256 digest
// ("secretfp_sha256:<hex>") over a purpose/path label and secret value so callers
// can record secret presence and detect rotation without storing raw secrets.
func SecretFingerprint(purpose, value string) string {
	sum := sha256.Sum256([]byte("ashlar secret fingerprint v1\x00" + purpose + "\x00" + value))
	return "secretfp_sha256:" + hex.EncodeToString(sum[:])
}

func normalizeList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		norm := normalizeKey(s)
		if norm != "" {
			out = append(out, norm)
		}
	}
	return out
}

func normalizeKey(key string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
}

func matchesKeyPolicy(key string, patterns, safeSuffixes []string) bool {
	norm := normalizeKey(key)
	if norm == "" {
		return false
	}
	for _, suffix := range safeSuffixes {
		if strings.HasSuffix(norm, suffix) {
			return false
		}
	}
	for _, p := range patterns {
		if norm == p || strings.Contains(norm, p) {
			return true
		}
	}
	return false
}
