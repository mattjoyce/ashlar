---
id: 4
status: backlog
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
- **`DecodeStrict` writes partway before failing.** Fields are set in document
  order, so an unknown key that comes later leaves the target half-filled.
  AGENTS.md's all-or-nothing rule covers resolution. Should it cover decoding,
  for example by decoding into a fresh value and copying it on success?
- **YAML `017`.** go-yaml reads it as YAML 1.1 octal (15). YAML 1.2 core reads
  it as decimal 17. Either pick one and document it, or reject leading-zero
  integers.
- **Overflow wording.** An overflow into an integer type reports "expected
  int64, got number" rather than naming the overflow.
- **The pointer for a JSON hex error.** `jsoncontract` reports `0x1F` at pointer
  `""`. Its `file:line:col` is right.
- **`*big.Int` targets.** `big.Int` is a `TextUnmarshaler`, so a numeric
  scalar decoded into it fails with "expected string for big.Int".

## Narrative
- 2026-11-01: Collected from the four card 3 test agents' reports. (by @claude)
