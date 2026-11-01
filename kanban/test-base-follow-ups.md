---
id: 4
status: done
priority: Normal
blocked_by: []
tags: [follow-up, yamlconfig, jsoncontract, decode]
---

# Decide the open questions the test base raised

Building the test base for card 3 turned up behavior that SPEC.md doesn't
define. Each item below needs a decision before it gets a test. None of them is
a regression.

## Open questions

- **Where an omitted required value is reported.** There's no node to point
  at, so `verifyRequiredPointers` uses the root origin. With several
  fragments, that's the last fragment's root (`other.yaml:1:1`), which reads
  as if the problem were in that file. Options: the first fragment, the nearest
  present ancestor, or no location at all.
  **Decided 2026-11-02: no location.** An omitted required value is reported
  with its pointer and message only. A null or blank value keeps its own
  origin. The message says the caller's `Required` list asked for the value
  and no layer set it. AGENTS.md:21 and SPEC.md each get a sentence saying a
  value no layer set has no source location. A GPT luminaries panel
  (Hickey × Armstrong, Ousterhout × Liskov) agreed.
- **`DecodeStrict` writes partway before failing.** Fields are set in document
  order, so an unknown key that comes later leaves the target half-filled.
  AGENTS.md's all-or-nothing rule covers resolution. Should it cover decoding,
  for example by decoding into a fresh value and copying it on success?
  **Decided 2026-11-02:**
  - Before writing anything, yamlconfig rejects unknown keys in a pass over
    key names only, as jsoncontract already does. This pass shares one
    field-index function with the decoder so their rules can't drift. Card 1
    changes those rules.
  - Document that a value error, such as a bad type or a custom unmarshaler
    failing, can leave `dst` partly written. Show the pattern of decoding into
    a fresh value and assigning it on success. Do this in both packages.
  - Reconcile SPEC.md:20. Unknown keys are rejected before any write; value
    errors are not atomic.
  - Rejected options: a zero-then-copy (loses defaults the caller filled in),
    a shallow copy (pointers are shared), a dry run (zero values can fail
    where the real `dst` succeeds, and hooks run twice), and `DecodeAs[T]`
    (new API).
- **YAML `017`.** go-yaml reads it as YAML 1.1 octal (15). YAML 1.2 core reads
  it as decimal 17. Either pick one and document it, or reject leading-zero
  integers.
  **Decided 2026-11-02: reject.**
  - Plain untagged scalars, and `!!int` or `!!float` scalars, that match
    `^[+-]?0[0-9_]*[0-9][0-9_]*$` fail with `ErrSyntax` via `scalarSyntaxErr`.
    The check sits in loader.go before tag dispatch and normalization.
  - Rejected: `00`, `007`, `08`, `09`, `0_17`, `+017`, `-017`, `!!int 017`,
    `!!float 08`.
  - Accepted: `0`, `-0`, `0.5`, `0e1`, `0o17`, `0x1F`, `010.5`, `1_000`.
  - Kept as strings: `!!str 017` and the quoted `"017"`.
  - The message is fixed text and never derived from the input: "leading-zero
    integer spelling is not allowed; use decimal without leading zeros or
    explicit 0o octal notation".
  - This is a breaking change for v0.3.0 notes. None of Ductile's 710 YAML
    files is affected. SPEC.md lists the accepted spellings.
- **Overflow wording.** An overflow into an integer type reports "expected
  int64, got number" rather than naming the overflow.
  **Decided 2026-11-02:** each failure gets its own fixed message, in both
  yamlconfig and jsoncontract. The messages never print the number. What
  decodes successfully doesn't change, so `1.0` and `1e0` are still rejected
  for int fields, as `encoding/json` does.

  | Case | Message |
  |---|---|
  | Wrong kind | `expected <type>, got <kind>` (unchanged) |
  | Decimal point or exponent into an int field | `number requires integer notation for <type>` |
  | Minus sign into an unsigned field, including `-0` | `negative sign is not allowed for <type>` |
  | Integer or float out of range | `number out of range for <type>` |

  Range errors are classified with `errors.As(*strconv.NumError)` and
  `errors.Is(strconv.ErrRange)`. For unsigned fields the minus sign is checked
  from the spelling. Tests cover every int and uint width, both float widths,
  and `-0`.
- **The pointer for a JSON hex error.** `jsoncontract` reports `0x1F` at pointer
  `""`. Its `file:line:col` is right.
  **Decided 2026-11-02: narrow suffix check.**
  - **The suffix check.** `scanNumber` and `scanLiteral` reject an immediately
    adjacent letter, digit, `.`, `+`, `-` or `_`. The error is `ErrSyntax` at
    the value's pointer, with the fixed message `invalid number` or
    `invalid literal`.
  - **Error span.** The span starts at the offending byte. This also fixes
    root-level suffix errors that were located one byte too far.
  - **Array elements.** Array value-token reads pass the element pointer
    (document.go:243, :272).
  - **Separator errors are unchanged.** Whitespace-separated or `1"a"` cases
    stay as container separator errors, or as `ErrTrailingContent` at the
    root.
  - **Root `0x1F` changes error kind,** from `ErrTrailingContent` to
    `ErrSyntax`; v0.3.0 notes.
  - **Tests** assert the pointer for nested, array and root cases.

  Cause: the lexer, not the parser, rejects `x` (lexer.go:167). It runs with
  the container pointer that document.go:206 passes to `next`.
- **`*big.Int` targets.** `big.Int` is a `TextUnmarshaler`, so a numeric
  scalar decoded into it fails with "expected string for big.Int".
  **Decided 2026-11-02: when a number reaches a target that implements
  `json.Unmarshaler`, the decoder uses it.** Strings keep the text-first order.
  The dispatch rule below goes in SPEC.md (near §353) and in the decoder docs.
  The handler that gets picked owns any error, with no fallback.

  | Source | Text + JSON | Text only | JSON only | Neither |
  |---|---|---|---|---|
  | null | zero | zero | zero | zero |
  | number | JSON | error | JSON | built-in |
  | string | text | text | JSON | built-in |
  | bool/array/object | error | error | JSON | built-in |

  - `big.Float` and `big.Rat` only have a text parser, so they still need
    quoting. Document this.
  - `1.0` and `1e0` still fail for `big.Int`, as in `encoding/json`.
  - Fix "exact lexeme" in the decode.go:132 comment and in the docs. The JSON
    parser receives the stored, normalized JSON number, so `0x1F` arrives as
    `31`.
  - Tests cover nil pointers, populated pointers, and which method is picked
    on types that implement both.

- **Decode-time messages for values that aren't classified as sensitive.**
  `v: sekrit123` decoded into a `time.Duration` reports
  `invalid duration "sekrit123"`. The same goes for a custom
  `TextUnmarshaler` that echoes its input. This follows the current policy,
  which redacts sensitive nodes only. Load-time errors now withhold all
  content. Should decode-time errors do the same, at the cost of less helpful
  messages?
  **Decided 2026-11-02: follow Go convention, keep the redactor.**
  - **Common practice, checked in source.** encoding/json, mapstructure v2.5.0
    (which Viper decodes through), goccy/go-yaml v1.19.2 and yaml.v3 v3.0.1
    don't redact. Their own wording names the types and the location. Most
    wrap the underlying parser error, and `strconv` and `time` errors quote
    the input.
  - **Ashlar's rule.** Ashlar's wording names the target type. The parser's
    error is wrapped with `%w`, as in
    `cannot parse value as time.Duration: time: invalid duration "15 seconds"`.
    Custom unmarshaler errors get the same prefix and are wrapped the same way.
    Nodes the redactor classifies as sensitive stay redacted, as today.
  - **Load errors stay content-free,** as in v0.2.0. Items 3 and 5 keep their
    fixed messages.
  - **AGENTS.md and SPEC.md.** Reword to promise redaction of values the
    redactor classifies as sensitive, rather than that no raw value ever
    appears.
  - **Considered, not adopted:** withholding all decode content, with or
    without keeping causes, and a caller option to hide or reveal. Withholding
    traded diagnostics for protection against classification misses that the
    common libraries don't attempt. A caller who needs more protection can
    supply a stricter redactor.
- **Include paths in diagnostics.** A GPT review asked to hide include target
  paths in cycle and verifier messages. I declined for PR #1. An included
  file's path is its provenance and appears in the `file:line:col` origin of
  every value read from it, and AGENTS.md promises that. Hiding it in a few
  messages would protect nothing. Escaping and missing targets, which never
  become origins, are already withheld. Revisit only if include paths are
  ever treated as secret material.
  **Decided 2026-11-02: won't do.** Paths and include chains are public
  provenance metadata. If paths ever become sensitive, revisit origins,
  include chains, file lists and provenance together. A target is withheld
  only when its initial `Stat` fails (includes.go:213). If `Stat` succeeds and
  the later read fails (includes.go:160), the path appears in the origin. That
  file exists, so its path is ordinary provenance.

- **The YAML syntax-error line comes from parsing error text.**
  `classifyYAMLOrigin` (yamlconfig/loader.go:147) runs a regex over yaml.v3's
  `err.Error()` to find "line N:". yaml.v3 exposes no structured position for
  parser errors. AGENTS.md forbids inspecting errors by string matching.
  Options: keep it as a documented exception, drop the line number, or get the
  position some other way. Raised by a GPT luminaries review on 2026-11-02.
  **Decided 2026-11-02: keep it as a documented, narrow exception.**
  AGENTS.md gains one sentence. Error text may be read only to extract a
  position for display, and only from a third-party error that has no
  structured form. It must never be used for classification or control flow.
  Make sure a test pins the fallback: when the text doesn't match, the error
  is still `ErrSyntax`, located at the file only.

## Narrative
- 2026-11-01: Collected from the four card 3 test agents' reports. (by @claude)
- 2026-11-02: Decided all nine items one at a time with the user. Each had a
  GPT (gpt-6-astra) luminaries review, using the Hickey × Armstrong and
  Ousterhout × Liskov lenses. Item 7 moved from withholding all content to Go
  convention after comparing encoding/json, mapstructure, yaml.v3 and
  goccy/go-yaml. Ready to implement; items 3, 5 and 7 are behavior changes
  for the v0.3.0 notes. (by @claude)
- 2026-11-02: Implemented all nine decisions on branch card4-decisions, one
  commit per item, with tests that fail without each change. Field indexing
  now lives in yamlconfig/fields.go and is shared by the decoder and the
  unknown-key pre-check, which card 1 must extend. (by @claude)
