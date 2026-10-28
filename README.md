# Ashlar

An **ashlar** is a stone dressed and squared so it fits precisely with other stones in masonry.

**Ashlar** is a Go library for strict document reading, structural schema validation, and traceable runtime configuration composition. Configuration fragments, environment overrides, and explicit flags compose predictably into a validated configuration whose effective values remain traceable to their sources.

> **Status:** v0.1.0. Pre-1.0: the public surface in `SPEC.md` is implemented and reviewed, but may still change before v1.0.0.

---

## The Metaphor and Its Boundary

In masonry, dressing an ashlar establishes its shape and fit before it is laid into a course:

- **Incoming material:** YAML configuration fragments, JSON data contracts, environment variables, and explicit overrides.
- **Dressing and squaring:** Strict parsing, structural schema validation, and explicitly declared composition rules.
- **Fit and provenance:** A resolved configuration value where every effective setting can explain where it came from (default, file and line, environment variable, or explicit override) and what it overrode.

**Ashlar prepares the building material; the consuming application determines what gets built.**

Ashlar deliberately refuses pieces that do not fit rather than silently reshaping their meaning:

1. **Runtime configuration (YAML):** Composes declared `include:` fragments, defaults, environment bindings, and explicit overrides under application-supplied rules, preserving field presence (`omitted` vs. explicit `false`, `0`, `""`, `[]`, or `null`) and per-value provenance.
2. **Data contracts and manifests (JSON):** Provides independently usable strict single-document reading, source-located diagnostics, and structural validation against application-supplied schemas **without** includes, merging, environment interpolation, or default insertion, preserving original document bytes and numeric lexemes.

---

## Shared Mechanism vs. Application Policy

| Ashlar Owns (Mechanism) | Consumer Owns (Policy) |
| :--- | :--- |
| Strict YAML/JSON document loading, source locations, and cycle-checked `include:` traversal | Configuration structs, defaults, and deployment directory conventions |
| Composition at the document/node level under declared rules | Which maps merge, which lists append or replace, and when duplicates fail |
| Presence tracking (`omitted` vs. explicit zero/empty/false values) | Domain validation and cross-field semantic invariants |
| Environment lookup, interpolation hooks, and explicit override layering | Environment prefixes, supported keys, legacy aliases, and precedence rules |
| Independently usable structural validation via JSON Schema (Draft 2020-12) | Schemas, canonicalization, cryptographic hashing, and catalog admission |
| Path-resolution primitives and redacted diagnostic formatting | Which fields are paths, their resolution bases, and secret/integrity providers |

### Redaction

A node is redacted when its own provenance is marked sensitive or the active `Redactor` classifies its pointer as sensitive. A `CompositionPolicy.Redactor` you supply **replaces** `DefaultRedactor()`; it does not extend it. To keep the default secret-key rules and add your own, combine them:

```go
policy.Redactor = ashlar.CombineRedactors(ashlar.DefaultRedactor(), myRedactor)
```

---

## Quality Gates

```bash
gofmt -l .
goimports -l .
go vet ./...
golangci-lint run ./...
gosec -severity medium ./...
go test -race -count=1 -shuffle=on -vet=all ./...
```

---

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
