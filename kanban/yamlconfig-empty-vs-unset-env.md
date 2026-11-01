---
id: 7
status: backlog
priority: Normal
blocked_by: []
tags: [yamlconfig, env, ductile-compat, spec]
---

# Tell an empty environment variable from an unset one in interpolation

When an operator sets `FOO=` on purpose, I want `${FOO}` to be able to mean
"empty" rather than "missing", so that an empty value is a legitimate setting.

Raised in the Ductile consumer check on PR #3: "a way to tell an empty env var
apart from an unset one".

## Current behavior

- **Env binding** already tells them apart (yamlconfig/env.go:57). An unset
  variable is skipped, and a set-but-empty one is applied and coerced to the
  existing node's kind.
- **`${VAR}` interpolation** does not. yamlconfig/env.go:138 treats an empty
  value the same as an unset one and fails with `ErrUnresolved`.

## First step

Ask Ductile which case it hits. If it is binding, the gap may be coercing ""
into a non-string field rather than presence. If it is interpolation, see
below.

## Shape (to decide)

- Take shell syntax as the model rather than inventing some, with one
  difference. In shell, plain `${VAR}` expands an unset variable to empty.
  Ashlar's `${VAR}` already behaves like shell's `${VAR:?}`, failing on unset
  or empty, and keeps doing so. Add `${VAR?}`, which fails only when the
  variable is unset. No default-value syntax (`${VAR:-x}`) unless asked
  for separately.
- Or a per-binding option on `EnvBinding`. That is simpler to parse but
  applies to every reference at once.
- SPEC §3.4 documents the syntax and the presence of the resulting node.

## Narrative
- 2026-11-02: Created from the Ductile consumer check on PR #3. Env binding
  already distinguishes the two; only interpolation does not. (by @claude)
