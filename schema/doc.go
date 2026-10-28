// Package schema provides offline JSON Schema (Draft 2020-12) compilation and
// structural validation against source-located ashlar.Value trees.
//
// All schema compilation is strictly offline: external network $ref resolution
// is disabled by construction. Schema resources are parsed through
// jsoncontract.Read so duplicate keys or trailing tokens inside a schema
// definition fail closed before compilation.
//
// Validation operates directly on immutable ashlar.Value trees (from either
// jsoncontract.Read or yamlconfig resolution) without mutating raw bytes or
// rounding numeric lexemes, and maps every schema violation back to the
// offending document node's RFC 6901 Pointer and Origin (file:line:column),
// honoring caller-supplied secret redaction.
package schema
