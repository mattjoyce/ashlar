package yamlconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"github.com/mattjoyce/ashlar"
)

// Resolved is an immutable, composed runtime configuration value with full
// per-node provenance and deterministic multi-file source hashing.
type Resolved struct {
	root       ashlar.Value
	files      []string
	sourceHash string
	redactor   ashlar.Redactor
}

// Root returns the immutable root ashlar.Value of r.
func (r Resolved) Root() ashlar.Value {
	return r.root
}

// Files returns a defensive copy of the fragment paths included in r.
func (r Resolved) Files() []string {
	if len(r.files) == 0 {
		return nil
	}
	out := make([]string, len(r.files))
	copy(out, r.files)
	return out
}

// SourceHash returns the deterministic SHA-256 digest ("sha256:<hex>") computed
// across all included YAML fragments in r.
func (r Resolved) SourceHash() string {
	return r.sourceHash
}

// DecodeStrict decodes r.Root() into dst, rejecting unknown struct fields with
// source-located ashlar.ErrUnknownField diagnostics.
func (r Resolved) DecodeStrict(dst any) error {
	return DecodeValueStrict(r.root, dst)
}

// Explain formats a human-readable provenance summary for the value at ptr,
// honoring secret redaction.
func (r Resolved) Explain(ptr ashlar.Pointer) string {
	return r.root.At(ptr).Explain(r.redactor, ptr)
}

// ExplainAll returns the provenance explanation for every leaf field in r in
// deterministic declaration order, honoring secret redaction.
func (r Resolved) ExplainAll() []ashlar.FieldProvenance {
	return r.root.ExplainAll(r.redactor)
}

// FormatProvenanceReport renders a multi-line provenance report for every leaf
// field in r, honoring secret redaction.
func (r Resolved) FormatProvenanceReport() string {
	return r.root.FormatProvenanceReport(r.redactor)
}

// Sanitize returns a copy of r.Root() where every sensitive scalar or subtree
// is replaced with ashlar.RedactedLiteral (or a domain-separated
// ashlar.SecretFingerprint when useFingerprint is true).
func (r Resolved) Sanitize(useFingerprint bool) ashlar.Value {
	return r.root.Sanitize(r.redactor, useFingerprint)
}

func computeFragmentsHash(fragments []Fragment) ([]string, string) {
	if len(fragments) == 0 {
		return nil, ""
	}
	files := make([]string, len(fragments))
	type pair struct {
		path   string
		digest string
	}
	pairs := make([]pair, len(fragments))
	for i, f := range fragments {
		files[i] = f.Path
		sum := sha256.Sum256(f.Raw)
		pairs[i] = pair{path: f.Path, digest: hex.EncodeToString(sum[:])}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].path < pairs[j].path })
	h := sha256.New()
	for _, p := range pairs {
		_, _ = h.Write([]byte(p.path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(p.digest))
		_, _ = h.Write([]byte{'\n'})
	}
	return files, "sha256:" + hex.EncodeToString(h.Sum(nil))
}
