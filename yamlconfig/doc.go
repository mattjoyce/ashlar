// Package yamlconfig provides strict YAML document loading, safe path and
// cycle-checked include resolution, declarative fragment composition, and
// provenance-preserving runtime configuration resolution.
//
// Every YAML document loaded by yamlconfig is parsed as a single strict
// document: duplicate mapping keys, trailing documents in a multi-document
// stream, and unknown fields in closed target structures fail closed with
// source-located diagnostics (file:line:column).
package yamlconfig
