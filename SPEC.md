# Ashlar Architecture & Public Surface Specification (`SPEC.md`)

**Ashlar** (`github.com/mattjoyce/ashlar`) is a Go library for strict document reading, structural schema validation, and traceable runtime configuration composition.

This specification defines the package boundaries, immutable value and provenance model, error taxonomy, consumer extension hooks, and dependency budget.

---

## 1. Architectural Invariants

1. **Quarantined Data Contracts (`jsoncontract`):**
   Single-document JSON contracts and manifests are read and validated without `include:` traversal, fragment merging, environment interpolation, or default insertion. Original document bytes and exact numeric lexemes (`1`, `1.0`, `1e0`, large integers) are preserved verbatim.
2. **Immutable Values & Presence (`ashlar`):**
   Every parsed document, AST node, and resolved configuration is an immutable value. Field presence explicitly distinguishes `Omitted` from `Null` (`null`) and `Present` zero/empty values (`false`, `0`, `""`, `[]`, `{}`).
3. **First-Class Provenance (`ashlar`):**
   Every node in a parsed or resolved value carries its `Provenance`: the effective `Origin` (`default`, `file:line:column`, `env:VAR`, `explicit`) and the ordered slice of prior origins it superseded.
4. **Node-Level Composition (`yamlconfig`):**
   Multi-fragment YAML configurations compose at the immutable `Value` node level under caller-declared per-path rules before decoding into Go structs. Go zero-value struct merging is never used.
5. **Fail Fast, Fail Closed, Redact Secrets:**
   Duplicate keys, trailing tokens/documents, unknown fields, unresolved required values, and include cycles fail all-or-nothing with source-located errors (`file:line:column`). Sensitive values are redacted in all `.Error()` strings, diagnostics, and provenance explanations.

---

## 2. Package Layout

Packages are grouped by concern with acyclic imports:

```text
github.com/mattjoyce/ashlar              # Core value, presence, provenance, source pos, pointers, errors, redaction
github.com/mattjoyce/ashlar/jsoncontract # Quarantined strict single-document JSON reader (stdlib only)
github.com/mattjoyce/ashlar/schema       # Offline JSON Schema (Draft 2020-12) compilation & Value validation
github.com/mattjoyce/ashlar/yamlconfig   # Strict YAML loader, cycle-checked includes, composition, env/defaults, paths
```

Dependency DAG between packages:
- `ashlar` imports only the Go standard library.
- `jsoncontract` imports `ashlar` and the Go standard library. It never imports `yamlconfig`.
- `schema` imports `ashlar` and the approved JSON Schema compiler.
- `yamlconfig` imports `ashlar` (and optionally `schema` for two-stage fragment/whole validation).

---

## 3. Public Surface by Package

### 3.1 Root Package (`github.com/mattjoyce/ashlar`)

#### Source Coordinates & Origin (`1.3`)

```go
// OriginKind classifies where a value originated.
type OriginKind string

const (
	OriginDefault  OriginKind = "default"
	OriginFile     OriginKind = "file"
	OriginEnv      OriginKind = "env"
	OriginExplicit OriginKind = "explicit"
)

// SourcePos identifies a 1-based line, column, and 0-based byte offset within a source.
type SourcePos struct {
	Line   int
	Column int
	Offset int64
}

// SourceSpan identifies a contiguous range within a named source document.
type SourceSpan struct {
	Source string
	Start  SourcePos
	End    SourcePos
}

// Origin describes the concrete source of a configuration or contract value.
type Origin struct {
	Kind   OriginKind
	Name   string     // File path, environment variable name, flag/override key, or default label
	Span   SourceSpan // Populated when Kind == OriginFile
	Via    []SourceSpan // Include chain leading to this file, outermost first
}
```

#### Error Taxonomy & Secret Redaction (`1.3`)

```go
// Sentinel errors for programmatic classification via errors.Is.
var (
	ErrSyntax          = errors.New("ashlar: syntax error")
	ErrDuplicateKey    = errors.New("ashlar: duplicate key")
	ErrTrailingContent = errors.New("ashlar: trailing content")
	ErrSchema          = errors.New("ashlar: schema violation")
	ErrUnknownField    = errors.New("ashlar: unknown field")
	ErrComposition     = errors.New("ashlar: composition error")
	ErrIncludeCycle    = errors.New("ashlar: include cycle")
	ErrValidation      = errors.New("ashlar: validation error")
	ErrNotFound        = errors.New("ashlar: source not found")
)

// Error is a structured, source-located diagnostic error supporting errors.Is and errors.As.
// Error messages never contain raw secret values; sensitive values are replaced via RedactedLiteral.
type Error struct {
	Kind    error      // Sentinel category (e.g., ErrSyntax, ErrSchema, ErrComposition, ErrValidation)
	Pointer Pointer    // RFC 6901 JSON Pointer within the document, when applicable
	Origin  Origin     // Source origin (file:line:column, env:VAR, etc.)
	Message string     // Redacted human-readable diagnostic message
	Err     error      // Underlying cause, if any (redacted if necessary)
}

// RedactedLiteral is the canonical replacement string for sensitive values in diagnostics.
const RedactedLiteral = "[REDACTED]"

// Redactor determines whether a field path or key holds sensitive material that must be redacted.
type Redactor interface {
	IsSensitive(ptr Pointer, key string) bool
}

// RedactorFunc adapts a function to the Redactor interface.
type RedactorFunc func(ptr Pointer, key string) bool
```

#### Immutable Value, Presence & Provenance (`1.4`)

```go
// Pointer is an RFC 6901 JSON Pointer (e.g. "", "/service/tick_interval", "/routes/0/from").
type Pointer string

// Presence distinguishes omitted keys from explicit null and explicit values.
type Presence uint8

const (
	PresenceOmitted Presence = iota // Key/path does not exist in the value tree
	PresenceNull                    // Key/path is explicitly set to null
	PresencePresent                 // Key/path is explicitly set to a non-null scalar, object, or array
)

// Kind identifies the JSON/YAML data model kind of a Value.
type Kind uint8

const (
	KindInvalid Kind = iota
	KindNull
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// Provenance records the effective origin of a Value and the ordered history of values it overrode.
type Provenance struct {
	Effective  Origin
	Sensitive  bool
	Superseded []ProvenanceEntry
}

// ProvenanceEntry records a prior value that was overridden during composition or layering.
type ProvenanceEntry struct {
	Origin  Origin
	Summary string // Redacted summary of the superseded value (or RedactedLiteral if Sensitive)
}

// Value is an immutable document or configuration node.
// Numbers retain their exact source lexeme (e.g., "1", "1.0", "1e0", "9223372036854775807").
// Objects preserve both key lookup and deterministic declaration/sorted iteration.
type Value struct {
	// Unexported fields ensure immutability.
}

func (v Value) Kind() Kind
func (v Value) Presence() Presence
func (v Value) Provenance() Provenance
func (v Value) At(ptr Pointer) Value
func (v Value) PresenceAt(ptr Pointer) Presence
func (v Value) Bool() (bool, bool)
func (v Value) StringVal() (string, bool)
func (v Value) NumberLexeme() (string, bool)
func (v Value) Int64() (int64, error)
func (v Value) Float64() (float64, error)
func (v Value) Len() int
func (v Value) Index(i int) Value
func (v Value) Keys() []string
func (v Value) Lookup(key string) (Value, bool)
```

---

### 3.2 Quarantined JSON Contract Reader (`github.com/mattjoyce/ashlar/jsoncontract`)

```go
// Document is an immutable, verified single-document JSON contract.
type Document struct {
	// Unexported fields holding raw bytes and root ashlar.Value.
}

// Read parses raw bytes as a single strict JSON document.
// It fails with ErrDuplicateKey on duplicate object keys at any depth, and ErrTrailingContent
// on any token following the top-level JSON value. Original bytes and number lexemes are preserved.
func Read(source string, raw []byte) (Document, error)

// ReadFS reads and parses a single strict JSON document from fsys at path.
func ReadFS(fsys fs.FS, path string) (Document, error)

// RawBytes returns a defensive copy of the original document bytes as read.
func (d Document) RawBytes() []byte

// Root returns the immutable root ashlar.Value of the document.
func (d Document) Root() ashlar.Value

// DecodeStrict decodes the document into dst (a non-nil struct pointer), rejecting unknown fields
// with source-located ErrUnknownField diagnostics and preserving Presence queries via Root().
func (d Document) DecodeStrict(dst any) error
```

---

### 3.3 Structural Schema Validator (`github.com/mattjoyce/ashlar/schema`)

```go
// Bundle compiles one or more self-contained JSON Schema (Draft 2020-12) resources offline.
type Bundle struct {
	// Unexported resource set.
}

// NewBundle creates an offline schema bundle. External network $ref resolution is always disabled.
func NewBundle() *Bundle

// AddResource registers a JSON Schema document under its URI or $id.
// The schema bytes are parsed strictly via jsoncontract.Read.
func (b *Bundle) AddResource(uri string, rawSchema []byte) error

// Compile compiles the schema at targetURI (which may include a fragment anchor such as
// "https://example.org/config.schema.json#/$defs/WholeConfig").
func (b *Bundle) Compile(targetURI string) (*Validator, error)

// Validator validates an ashlar.Value against a compiled JSON Schema.
type Validator struct {
	// Unexported compiled schema.
}

// Validate checks val against the compiled schema without mutating bytes or rounding numeric lexemes.
// Violations return structured *ashlar.Error values (wrapping ErrSchema) carrying the offending
// node's RFC 6901 Pointer and Origin (file:line:column), redacted with redactor when non-nil.
func (v *Validator) Validate(val ashlar.Value, redactor ashlar.Redactor) error
```

---

### 3.4 YAML Loader, Includes & Composition (`github.com/mattjoyce/ashlar/yamlconfig`)

```go
// ByteTransformer is a narrow hook invoked on raw file bytes before YAML or env parsing
// (e.g., for in-memory age decryption).
type ByteTransformer func(ctx context.Context, path string, raw []byte) ([]byte, error)

// IntegrityVerifier is a narrow hook invoked on discovered/included file paths before admission
// (e.g., for SHA-256 .checksums verification).
type IntegrityVerifier func(ctx context.Context, path string, raw []byte) error

// MergeRule defines how two nodes at the same Pointer compose across YAML fragments.
type MergeRule uint8

const (
	MergeReplace         MergeRule = iota // Later fragment replaces earlier value (recording Superseded)
	MergeMap                              // Deep-merge mapping keys recursively
	MergeAppendSlice                      // Append sequence elements in include order
	MergeForbidDuplicate                  // Reject if key/path is specified in more than one fragment
)

// EnvBinding configures UPPER_SNAKE environment variable binding and optional ${VAR} scalar interpolation.
type EnvBinding struct {
	Prefix            string                      // Optional prefix (e.g., "ANAM" -> "ANAM_<UPPER_SNAKE_KEY>")
	Lookup            func(string) (string, bool) // Defaults to os.LookupEnv
	InterpolateScalar bool                        // Enable node-level ${VAR} expansion on string scalars
}

// Candidate describes one configuration file search location for Discover.
type Candidate struct {
	Path     string
	Origin   ashlar.Origin
	Required bool // True for explicit flag/env paths (missing = ErrNotFound); false for default search dirs
}

// Discover resolves the first existing configuration file from candidates, enforcing Required semantics.
func Discover(candidates ...Candidate) (path string, origin ashlar.Origin, err error)

// Resolved is an immutable, composed runtime configuration value with full provenance.
type Resolved struct {
	// Unexported root ashlar.Value, included file list, and Redactor.
}

func (r Resolved) Root() ashlar.Value
func (r Resolved) Files() []string
func (r Resolved) DecodeStrict(dst any) error
func (r Resolved) Explain(ptr ashlar.Pointer) string
```

---

## 4. External Dependency Budget & Approval Accounting

Per `AGENTS.md` §2, `go.mod` is initialized with **zero external dependencies**. External dependencies are proposed only for Rung `2.2` and Rung `3.1`:

| Target Rung & Package | Proposed Direct Dependency | What Go `stdlib` Lacks | Transitive Cost (`go.mod`) | Status |
| :--- | :--- | :--- | :--- | :--- |
| **Rung 1 (`ashlar`)** | *None (stdlib only)* | — | 0 | Active |
| **Rung 2.1 (`jsoncontract`)** | *None (stdlib only)* | Custom scanner/decoder over stdlib `encoding/json` + byte/line tracker enforces duplicate-key and trailing-token rejection with 0 external deps. | 0 | Active |
| **Rung 2.2 (`schema`)** | `github.com/santhosh-tekuri/jsonschema/v6 v6.0.3` | Go stdlib has no JSON Schema Draft 2020-12 compiler/evaluator (`$defs`, `$ref`, `additionalProperties`, `patternProperties`, combinators). | 1 indirect (`golang.org/x/text`) | Requires approval before Rung 2.2 |
| **Rung 3.1 (`yamlconfig`)** | `go.yaml.in/yaml/v3 v3.0.4` | Go stdlib has no YAML 1.2 lexer/parser (`yaml.Node` AST with line/column coordinates). | 0 indirect | Requires approval before Rung 3.1 |
