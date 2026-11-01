---
id: 6
status: backlog
priority: Normal
blocked_by: []
tags: [yamlconfig, includes, ductile-compat, spec]
---

# Make directory includes configurable

When a config includes a directory, I want to declare whether it is read
recursively and which files count, so that a nested folder can't silently
join my configuration.

Raised in the Ductile consumer check on PR #3: "a configurable
directory-include policy (non-recursive, `.yaml` only)".

## Current behavior

`walkYAMLDir` (yamlconfig/includes.go:330) always walks the directory
recursively and takes every `.yaml` and `.yml` file, sorted by path. Nothing
in `IncludeOptions` changes this.

## Why this fits Ashlar

Composition under declared rules is Ashlar's mechanism (SPEC §1 invariant 6).
Which files make up a configuration is the application's policy.

## Shape (to decide)

- New fields on `IncludeOptions`: recursion on or off, and the accepted
  extensions.
- The default. Non-recursive is the safer default, because a recursive walk
  pulls in any nested folder without the include list naming it. Changing
  the default breaks callers that rely on recursion, so it would ship in
  v0.4.0 with a release note.
- How hidden files and directories (`.git`, editor swap files) are treated.
  Today a `.foo.yaml` is included.
- Sort order across subdirectories, if recursion stays available.

## Narrative
- 2026-11-02: Created from the Ductile consumer check on PR #3. (by @claude)
