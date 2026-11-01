---
id: 3
status: done
priority: High
blocked_by: [2]
tags: [tests, corpus, coverage, redaction, provenance]
---

# Build a test base that covers Ashlar's binding promises

When I change a decoder or loader, I want tests that fail if any AGENTS.md
promise breaks, so I can make feature changes like card 1 without guessing what
they break.

## Current state (2026-11-01, Go 1.25.0)

Statement coverage with `-coverpkg=./...` is 75.8%. Measured per package, it is
root 82%, `jsoncontract` 77%, `schema` 76%, and `yamlconfig` 70%. There are
about 3,700 lines of tests against about 5,600 lines of source.

The coverage number is not the problem. These functions have 0% coverage, and
each one backs a promise in AGENTS.md:

| Untested | Promise |
|---|---|
| `yamlconfig/loader.go:126` `classifyYAMLError`, `:159` `classifyYAMLOrigin`, `:169` `findKeyColumnOnLine`, `:181` `firstNonSpaceColumn` | YAML syntax errors located at `file:line:column` |
| `yamlconfig/loader.go:349` `convertYAMLFloat`, and `:327` `convertYAMLInt` at 30% | Exact number spellings kept |
| `provenance.go:46` `Superseded` | Override history in provenance |
| `redact.go:128` `CombineRedactors`, `:165` `RedactString` | Secret redaction. The README documents `CombineRedactors`, but no test runs it. |
| `errors.go:202` `Error`, `:214` `Format`, `:232` `Unwrap` | Decoder text withheld from error output |
| `yamlconfig/decode.go:303` `valueToGoAny` | Decoding into `any` and into `json.Unmarshaler` targets |
| `yamlconfig/decode.go:179` `decodeSlice` (only covered from root-package tests) | Decoding YAML sequences |

## Approach

Organize the work by AGENTS.md §3's compatibility corpus, not by function name.
Every test goes through the public API, using real YAML and JSON files in
`t.TempDir()`. If a function above is still unreached afterward, it is either
dead code to delete or a missing promise to bring up. Don't reach it through
internals just to raise the number.

## Acceptance criteria

- [x] **Syntax errors.** Each of these reports an exact `file:line:column`, and
      the message contains no secret value:
      - malformed YAML at top level, nested, and inside an included file
      - a duplicate key
      - a trailing document
      - a tab used for indentation
- [x] **Numeric fidelity, YAML and JSON.** These spellings are kept exactly:
      `1`, `1.0`, `1e0`, `-0`, `0x1F` and `0o17` where YAML allows them,
      integers past 2^63, and `.inf`/`.nan`. Decoding into `int`, `int64`,
      `uint`, `float64` and `json.Number` either succeeds exactly or reports a
      located overflow error.
- [x] **Presence.** These are told apart at both the `Value` and the decode
      level: an omitted key, explicit `false`, `0`, `""`, `[]`, `{}`, and `null`.
      The test runs this through includes and environment overrides.
- [x] **Provenance.** `Superseded` and `Explain` report the full override
      chain (default, then file A, then file B, then env, then explicit), with
      exact origins. Every output of `ExplainAll` is checked against a golden
      report.
- [x] **Redaction.** `DefaultRedactor`, a custom redactor, and
      `CombineRedactors` each run against error strings, `%+v` and `%#v`
      formatting, `Explain` output, the provenance report, and `Sanitize`. A
      table of planted secrets proves none of them comes out.
- [x] **Strictness.** Each case is rejected with the right sentinel and a
      location:
      - unknown fields at the root, nested, inside slices, and inside map values
      - include cycles, both direct and indirect
      - unresolved required values
      - a failure partway through resolution, which must leave no partial
        configuration
- [x] **Decode targets.** Sequences into slices, decoding into `any`, and
      `TextUnmarshaler` and `json.Unmarshaler` targets are all exercised, with
      both success and located failure.
- [x] Every exported function and method is called by at least one test, or the
      card records why not.
- [x] Tests pass under `-race -shuffle=on` with `-count=3`, to catch tests that
      depend on order.
- [x] Any bug the new tests find gets its own card or fix commit. It is never
      worked around in the test.
- [x] `scripts/gate.sh` exits 0.

## Out of scope

- A coverage percentage target or a coverage gate in CI. That needs its own
  decision.
- Fuzzing. It's worth a separate card once this base exists.

## Narrative
- 2026-11-01: Ran a coverage audit before starting card 1 (inline map
  catch-all). Total coverage looked reasonable, but the untested code was
  concentrated in exactly the paths AGENTS.md treats as binding: syntax error
  location, number spellings, override history and redaction. Decided card 1
  waits on this base. (by @claude)
- 2026-11-01: Done. The work was split by corpus area across four parallel
  agents in separate worktrees. Each wrote tests only and reported bugs
  instead of working around them, and I fixed every bug in the library. Test
  code went from about 3,700 to about 8,700 lines. Total coverage rose from
  75.8% to 84.9%, and no function is left at 0%. `-race -shuffle=on
  -count=3` passes. The tests found eight bugs:
  1. `Resolved.DecodeStrict` applied `DefaultRedactor` on top of a custom
     policy, contradicting the README's "replaces, not extends".
  2. YAML syntax errors guessed their column (the first non-blank byte),
     because yaml.v3 reports only a line.
  3. A required null was reported at the document root.
  4. An omitted `Override` left a phantom key that `Keys` showed but presence
     called omitted.
  5. YAML numbers couldn't decode into `json.Number`.
  6. `json.Unmarshaler` targets received re-marshalled floats.
  7. `1e400` silently became a string.
  8. A custom unmarshaler could echo a sensitive child of a mapping (a real
     leak).

  I made three design decisions. They are recorded in SPEC.md and need your
  review:
  - YAML syntax errors are located `file:line` only. yaml.v3 gives no
    column, and getting one would mean a new dependency.
  - YAML-only number spellings such as hex and `0o` octal are stored in JSON
    decimal form. `.inf` and `.nan` are rejected.
  - An omitted override is an `ErrComposition`.

  I also deleted dead code instead of testing it: the duplicate-key branch of
  `classifyYAMLError` and `findKeyColumnOnLine`. Open questions are on
  card 4. (by @claude)
