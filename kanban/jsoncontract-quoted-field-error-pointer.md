---
id: 2
status: done
priority: High
blocked_by: []
tags: [jsoncontract, tests, gate, errors]
---

# Fix the failing jsoncontract test and error classification

When I run the AGENTS.md §4 gate on master, I want it to pass, so I can trust a
red result to mean a real regression.

## Problem

`TestDecodeStrictMatchesEncodingJSON/quoted_bad_content`
(`jsoncontract/decode_test.go:139`) fails on Go 1.25.0, three runs out of three.

```
decode_test.go:211: aErr.Pointer = "", want "/port"
```

The case decodes `{"port":"abc"}` into a struct field tagged `json:"port,string"`.
A probe shows that `encoding/json` returns a plain `*errors.errorString` for this
input:

```
json: invalid use of ,string struct tag, trying to unmarshal "abc" into int
```

It is not a `*json.UnmarshalTypeError`, so `classifyDecodeError`
(`jsoncontract/decode.go:103`) skips its typed branch and falls through to the
last return, which reports the root pointer. Because that typed branch never
runs, `pointerFromDotField` and `originAt` have 0% coverage.

`classifyDecodeError` also recognizes unknown fields with
`strings.HasPrefix(msg, "json: unknown field ")`. AGENTS.md §2 forbids checking
errors by string matching. `encoding/json` has no typed error for unknown fields,
so the match may be unavoidable, but it then needs a stated exception.

## Acceptance criteria

- [x] Record which Go version or `encoding/json` behavior the test was written
      against (introduced at or before `97e7604`).
- [x] Decide which side is wrong:
      - The code is wrong if Ashlar promises a `/port` pointer for bad `,string`
        content. Then Ashlar must derive the pointer without parsing
        `encoding/json` error text. One option is to have Ashlar's own value
        tree find the field before decoding. Another is to type-check the value
        against the field itself.
      - The test is wrong if no such pointer is promised. Then change the
        expectation to the root pointer, and state that limitation in SPEC.md
        §3.2.
- [x] Either way, keep the decoder text withheld from the error. The
      `pin-sekrit-3` leak case at `decode_test.go:290` stays green.
- [x] Add a test that reaches `*json.UnmarshalTypeError`, for example
      `{"port":"abc"}` into a plain `int` field. That covers
      `pointerFromDotField` and `originAt` and checks the reported pointer and
      `file:line:col`.
- [x] The unknown-field check either stops using string matching or gets a code
      comment explaining why `encoding/json` leaves no alternative, with the
      exception noted in AGENTS.md or SPEC.md.
- [x] `scripts/gate.sh` exits 0.

## Narrative
- 2026-11-01: Found while checking how thorough Ashlar's tests are before
  starting card 1. The failure is deterministic on Go 1.25.0. Card 3 (the test
  base) builds on a green suite, so this card comes first. (by @claude)
- 2026-11-01: Resolved. The test never passed: it fails at `203377c`, where it
  was added, and at `97e7604`. `encoding/json` reports bad `,string` content as
  an untyped `*errors.errorString` with no field name, so the pointer could only
  come from Ashlar. That made the code wrong, not the test, because the
  `DecodeStrict` contract promises a location wherever the failing value can be
  identified. The pre-check now validates the quoted literal itself:
  `checkQuotedContent` decodes the string content into the field's type and
  rejects surrounding whitespace, which `encoding/json` also rejects. The
  existing confirmation step still discards any pre-check error that
  `encoding/json` would not also raise, so this can't reject accepted input. The
  message names only the Go type. The typed `UnmarshalTypeError` branch turned
  out to be reachable through a map key that isn't an integer (`map[int]T` given
  `"abc"`). It now has tests for its pointer, its `file:line:col` and the
  withheld key text. Coverage showed the unknown-field string match never ran,
  because `checkKnownFields` rejects unknown keys before `encoding/json` does. I
  removed it rather than add an exception to the no-string-matching rule. No
  SPEC change was needed, since the behavior now matches the existing doc
  comment. (by @claude)
