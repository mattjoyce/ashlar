// Package jsoncontract provides a strictly quarantined, single-document JSON
// reader for data contracts and manifests.
//
// In accordance with the Ashlar engineering contract, jsoncontract is strictly
// isolated from runtime configuration features: it never performs file
// includes, fragment merging, environment variable interpolation, or default
// value insertion, and it never mutates raw document bytes or numeric lexemes.
//
// Every parsed Document preserves:
//   - A defensive copy of the original raw bytes (Document.RawBytes).
//   - An immutable ashlar.Value tree (Document.Root) where every object key and
//     value carries its exact 1-based file:line:column SourceSpan and every
//     numeric token preserves its exact character spelling (1, 1.0, 1e0, 64-bit
//     integers).
//   - Fail-closed rejection of duplicate object keys at any nesting depth
//     (ashlar.ErrDuplicateKey) and trailing tokens or additional JSON documents
//     after the root value (ashlar.ErrTrailingContent).
package jsoncontract
