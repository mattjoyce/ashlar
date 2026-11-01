---
id: 1
status: todo
priority: Normal
blocked_by: [2, 3]
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

- **Precedence.** A key that matches a named field always goes to that field,
  including fields promoted from inline structs. Only leftover keys go to the
  inline map. This matches yaml.v3.
- **Closedness.** An inline map opens only its own struct to extra keys. Every
  struct without one stays closed and rejects unknown keys with
  `ErrUnknownField`. That includes structs decoded inside catch-all entries.
- **Eligible fields.** Only an exported field of map kind declared directly on
  the struct can be the catch-all. An unexported inline map is ignored, like
  any unexported field, so the struct stays closed. Each of these fails with
  `ErrValidation`:
  - an exported inline pointer-to-map (yaml.v3 also rejects it)
  - more than one inline map on one struct
  - an inline map inside an embedded or inline struct. yaml.v3 doesn't promote
    these, and Ashlar rejects them rather than ignore them.
  None of these cases ever falls back to a wrapper key named after the field.
- **Key type.** The map key must be string-kinded, which matches `decodeMap`. This
  is a deliberate extension of yaml.v3, which requires exactly `string`. Any other
  key kind fails with `ErrValidation`.
- **When validation runs.** These rules are checked each time a struct mapping is
  decoded, including empty mappings and mappings with only named keys. A null
  struct value and an omitted struct field behave as they do today. Ashlar does
  not add a separate pass over the type.
- **Element decoding.** Each entry goes through the existing `decodeInto` at its
  flat pointer. A bad value gets a `file:line:col` error, and a sensitive value
  gets redacted. `cpu: null` creates a `"cpu": 0` entry, the same as
  `decodeMap` today.
- **Destination reuse.** This follows yaml.v3. A nil map is allocated when the
  first unmatched key arrives. With no unmatched keys, the field is left as it
  was. Unmatched keys are added to a map that already has entries and do not
  replace it.
- **Provenance and composition.** No change. Origins and sensitivity live on the
  `ashlar.Value` tree by document pointer, so an entry keeps its flat path
  (`/timeouts/cpu`, not `/timeouts/overrides/cpu`). Include merging already runs
  on document pointers, and there is no `/overrides` container to merge or
  clear, so it needs no new rules.

## Acceptance criteria

- [ ] `SPEC.md` §3.4 (YAML loader) documents the catch-all rules and the one
      exception they make to unknown-field rejection. The `DecodeValueStrict`
      and `DecodeStrict` doc comments say the same. JSON-contract decoding (§3.2)
      is unchanged.
- [ ] Ductile's shape `yaml:",inline,omitempty"` decodes `poll`, `handle`, and
      `cpu` correctly.
- [ ] Tag handling. `json:",inline"` works as the fallback, a YAML tag takes
      precedence over a JSON tag, and `yaml:"-"` excludes the field. No general
      validation of tag options is added.
- [ ] A named field wins over the catch-all, including a field promoted from an
      inline struct.
- [ ] With no extra keys, a nil map stays nil and a map that already has entries
      is unchanged. With extra keys, those entries are added to the existing
      entries.
- [ ] An omitted `cpu` creates no entry. Both `cpu: null` and `cpu: 0s` create a
      zero-duration entry. `Root().PresenceAt` tells omitted, null, and present
      apart at the flat pointer.
- [ ] `cpu: banana` reports an error located at `/timeouts/cpu`.
- [ ] A sensitive entry stays redacted in error strings and provenance output.
      This holds under the default key classification and under a
      caller-supplied redactor.
- [ ] Each case fails with `ErrValidation`, including on an empty mapping:
      two inline maps, a non-string key kind, an inline pointer-to-map, and an
      inline map inside an inline struct. An unexported inline map leaves the
      struct closed.
- [ ] A struct inside a catch-all entry still rejects unknown keys. A sibling
      struct without a catch-all still rejects them too.
- [ ] After a successful decode, `Resolved.Explain` reports the `file:line:col`
      origin of an entry at its flat pointer. One key contains `/` or `~` to
      check RFC 6901 escaping.
- [ ] Two included files contribute different extra keys and both override one
      shared key. The test resolves and decodes them, then checks the entries
      kept, the winning value, and the superseded origin of the shared key.
- [ ] A struct shaped like Ductile's (`Overrides map[string]time.Duration` tagged
      inline) changes behavior, and the commit message calls this out.
      - `overrides: {cpu: 15s}` used to fill the map through a wrapper key nobody
        declared. Now it fails with a located decode error at `/overrides`.
      - `overrides: 15s` becomes a legitimate entry named `"overrides"`.
- [ ] All AGENTS.md §4 gates pass.

## Implementation notes

- Write failing tests first: table-driven, using real YAML files in
  `t.TempDir()`.
- If `yamlconfig/decode.go` (362 lines now) goes past roughly 400 lines, move
  field indexing into its own file.

## Narrative
- 2026-11-01: Reviewed a comment saying Ductile supports inline maps and Ashlar
  doesn't. Ashlar's code confirmed it, and showed something worse. Ashlar ignores
  `,inline` on a map and accepts an `overrides:` wrapper key nobody declared.
  Traced Ductile's only use of an inline map to `TimeoutsConfig.Overrides` (P2-05).
  Provenance already lives on the `ashlar.Value` tree, so this is a decode-only
  change with no public API change and no new dependency. (by @claude)
- 2026-11-01: A GPT Astra review (gpt-6-astra) confirmed the bug and the outcome
  of the `overrides:` change. It found a wrong spec reference: YAML decoding is
  §3.4, not §3.2. It also showed the design missed several things. A check on
  the type alone is skipped for `null` and omitted fields. Pointer-to-map fields
  and unexported fields weren't covered. The draft went beyond yaml.v3 by
  promoting catch-alls out of inline structs. It didn't say what happens to a
  map that already has entries. Decisions: don't promote catch-alls (reject a
  nested one instead, which also cuts conflict cases nobody needs); keep
  string-kinded keys to match `decodeMap`; add to existing maps as yaml.v3 does;
  validate when each struct mapping is decoded. Also added acceptance criteria
  for null entries, closedness, redaction, and merging across includes.
  (by @claude)
- 2026-11-01: Blocked on cards 2 and 3. Card 2 fixes a jsoncontract test that
  fails on master. Card 3 builds the test base that AGENTS.md §3 expects. This
  change opens a deliberate exception to strict decoding, so it shouldn't land
  until syntax-error location, redaction and provenance have real tests.
  (by @claude)
