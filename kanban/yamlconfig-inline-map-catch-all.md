---
id: 1
status: todo
priority: Normal
blocked_by: []
tags: [yamlconfig, decode, ductile-compat, spec]
---

# Support inline map catch-all fields in DecodeStrict

When Ductile moves its config loading onto Ashlar, I want `DecodeStrict` to honor
`yaml:",inline"` on a map field, so I can keep per-plugin timeout overrides like
`plugins.stress.timeouts.cpu: 15s` without adding an `overrides:` wrapper key to
operator YAML.

## Background

Go YAML libraries give `,inline` two meanings:

- On a struct field, the child struct's fields get promoted into the parent's
  closed key set. Ashlar supports this.
- On a map field, the map becomes a catch-all. Any key that doesn't match a named
  sibling field lands in the map. Ashlar does not support this.

Ductile uses the map form in exactly one place,
`~/Projects/ductile/internal/config/types.go:330`:

```go
type TimeoutsConfig struct {
    Poll      time.Duration            `yaml:"poll"`
    Handle    time.Duration            `yaml:"handle"`
    Health    time.Duration            `yaml:"health,omitempty"`
    Init      time.Duration            `yaml:"init,omitempty"`
    Overrides map[string]time.Duration `yaml:",inline,omitempty"`
}
```

Readers of `Overrides` in Ductile: `ResolvedPluginConf.Timeout`
(`types.go:386`), `Dispatcher.getTimeout` (`dispatch/dispatcher.go:1734`),
`EffectivePluginConf` (`types.go:523`), `config show --effective`
(`cmd/ductile/config.go:424`, which shows each entry as `timeouts.<cmd>` with its
source), and the audit snapshot (`configsnapshot/snapshot.go:594`).

## Current behavior (bug)

In `yamlconfig/decode.go:250`, `indexStructFieldsInto` treats `inline` only when
the field type is a struct. For a map, the check falls through. Because the tag's
name is empty, the field gets indexed under its lowercased Go name. With the
struct above:

- `cpu: 15s` fails with `ErrUnknownField`.
- `overrides: {cpu: 15s}` is silently accepted. Ashlar invents a wrapper key the
  YAML never declared.

## Design

- **Precedence.** A key that matches a named field always goes to that field.
  Only leftover keys go to the inline map. This matches yaml.v3.
- **Closedness.** An inline map deliberately opens its struct to extra keys.
  Every struct without one stays closed and still rejects unknown keys with
  `ErrUnknownField`.
- **Cardinality.** At most one inline map per struct, counting maps reached
  through embedded or inline structs. If a struct has two, decoding fails with
  `ErrValidation`. The check runs on the struct type, so it fails the same way
  for every input.
- **Key type.** The map key must be string-kinded. Anything else fails with
  `ErrValidation`.
- **Element decoding.** Elements go through the existing `decodeInto`, so a bad
  value still gets a `file:line:col` error and a sensitive value still gets
  redacted.
- **Allocation.** The map is allocated lazily. It stays nil when no extra keys
  appear, which matches yaml.v3 and Ductile's `omitempty`.
- **Provenance.** No change needed. Origins live on the `ashlar.Value` tree by
  document pointer, so an entry keeps its flat path (`/timeouts/cpu`, not
  `/timeouts/overrides/cpu`) for `Explain`, `ExplainAll`, and redaction.

## Acceptance criteria

- [ ] `SPEC.md` §3.2 `DecodeStrict` documents the inline-map rule and the one
      exception it makes to unknown-field rejection.
- [ ] Failing tests land first: table-driven, using real YAML files in `t.TempDir()`.
- [ ] Ductile's `TimeoutsConfig` shape decodes `poll`, `handle`, and `cpu`
      correctly.
- [ ] A named field wins over the inline map.
- [ ] A document with no extra keys leaves the map nil.
- [ ] An invalid element value, such as `cpu: banana`, reports a located error.
- [ ] A sensitive element value stays redacted in error strings.
- [ ] Two inline maps in one struct, and a non-string key type, each fail with
      `ErrValidation`.
- [ ] `Resolved.Explain` reports the `file:line:col` origin of an inline-map
      entry at its flat pointer.
- [ ] `overrides: {cpu: 15s}` no longer creates a `cpu` entry through an invented
      wrapper key. `overrides` is now treated as an extra key, and for
      `map[string]time.Duration` it fails with a located decode error. The commit
      message calls this out as a behavior change.
- [ ] If `yamlconfig/decode.go` (362 lines now) goes past roughly 400 lines,
      field indexing moves into its own file.
- [ ] All AGENTS.md §4 gates pass.

## Narrative
- 2026-11-01: Reviewed a comment saying Ductile supports inline maps and Ashlar
  doesn't. Ashlar's code confirmed it, and showed something worse. Ashlar ignores
  `,inline` on a map and accepts an `overrides:` wrapper key nobody declared.
  Traced Ductile's only use of an inline map to `TimeoutsConfig.Overrides` (P2-05).
  Provenance already lives on the `ashlar.Value` tree, so this is a decode-only
  change with no public API change and no new dependency. (by @claude)
