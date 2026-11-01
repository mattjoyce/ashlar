---
id: 5
status: backlog
priority: Normal
blocked_by: []
tags: [yamlconfig, decode, ductile-compat, spec]
---

# Report every unknown field without decoding

When Ductile moves its config loading onto Ashlar, I want to list every
unknown key in a document with its location, so that during migration Ductile
can warn about them all at once instead of failing on the first.

Raised in the Ductile consumer check on PR #3 as "an opt-in lenient decode
that collects every unknown field with its location".

## Why this fits Ashlar

SPEC §1 invariant 6: whether an unknown key is fatal is a choice an
application can reasonably make differently, for example while migrating
operators' files. Ashlar keeps the strict default and supplies the mechanism;
the caller supplies the policy.

## Shape (to decide)

- A function that walks a value against a destination type and returns every
  unknown key as an `ErrUnknownField` error with pointer and `file:line:col`.
  It decodes nothing.
- It must use the decoder's own field index (`indexStructFields` in
  yamlconfig/fields.go, which returns `structFields` with card 1's catch-all), so it cannot disagree
  with `DecodeStrict`. Today `checkKnownKeys` walks the same tree and stops at
  the first hit.
- `DecodeStrict` and `DecodeValueStrict` stay strict. There is no lenient
  decode mode. A caller that wants to warn calls the report, acts on it, and
  decodes into a type that tolerates the keys or strips them first.

## Open questions

- Return type: a slice of `*ashlar.Error`, or one error built with
  `errors.Join`.
- Whether the report should continue past a shape error (card 1's
  `ErrValidation` for an unsupported catch-all) or stop there.
- How a caller decodes after warning. Options: a value-level helper that drops
  reported keys, or leave it to the caller.
- SPEC §1 invariant 5 says unknown fields fail. Amend it to say the strict
  decoders fail and the report exists for callers whose policy differs.

## Narrative
- 2026-11-02: Created from the Ductile consumer check on PR #3. Reshaped from
  a lenient decode mode to a report, so strict decoding stays the default and
  the warn-or-fail choice stays with the caller. (by @claude)
