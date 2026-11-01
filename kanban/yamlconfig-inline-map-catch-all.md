---
id: 1
status: doing
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

- **Precedence.** A key that matches a named field always goes to that field,
  including fields promoted from inline structs. Only leftover keys go to the
  inline map. This matches yaml.v3.
- **Closedness.** An inline map opens only its own struct to extra keys. Every
  struct without one stays closed and rejects unknown keys with
  `ErrUnknownField`. That includes structs decoded inside catch-all entries.
- **Tag parsing.** Unchanged. Ashlar reads the `yaml` tag first, falls back to
  the `json` tag, and `-` excludes the field. If a name is given alongside
  `inline` (`yaml:"overrides,inline"`), it is ignored, as in yaml.v3, and the
  field is still the catch-all.
- **Eligible fields.** Only an exported field of map kind declared directly on
  the struct can be the catch-all. An unexported inline map is ignored, like
  any unexported field, so the struct stays closed. An anonymous map with no
  `,inline` tag is not a catch-all. It stays a named field under its lowercased
  type name, as it does today. Each of these fails with `ErrValidation`:
  - an exported inline pointer-to-map
  - more than one inline map on one struct
  - an inline map inside an embedded or inline struct, including an inline
    pointer-to-struct. yaml.v3 doesn't promote these and silently keeps the
    outer struct closed. Ashlar rejects them so the mistake is visible. This is
    checked while the outer struct is decoded, so the same inner type decoded
    on its own is valid.
  yaml.v3 panics on the first two cases and on a non-string key. AGENTS.md
  forbids panics in library code, so Ashlar returns an error instead. None of
  these cases ever falls back to a wrapper key named after the field. Each error
  is reported at the pointer and origin of the struct mapping being decoded (for
  Ductile, `/timeouts`), and the message names the Go type and the field.
- **Key type.** The map key must be string-kinded, which matches `decodeMap`. This
  is a deliberate extension of yaml.v3, which requires exactly `string`. Any other
  key kind fails with `ErrValidation`.
- **When validation runs.** These rules are checked each time a struct mapping is
  decoded, including empty mappings and mappings with only named keys. A null
  struct value and an omitted struct field produce no error, because no mapping
  is decoded there. yaml.v3 behaves the same way. Ashlar does not add a
  separate pass over the type.
- **Element decoding.** Each entry goes through the existing `decodeInto` at its
  flat pointer. A bad value gets a `file:line:col` error, and a sensitive value
  gets redacted. `cpu: null` creates a `"cpu": 0` entry, the same as
  `decodeMap` today.
- **Destination reuse.** This matches `decodeMap`, so one decoder has one rule
  for maps. With no unmatched keys, the field is left as it was, so a nil map
  stays nil. When at least one unmatched key arrives, the catch-all is replaced
  with a fresh map holding only those entries. yaml.v3 adds to an existing map
  instead, but Ashlar's named map fields already differ from yaml.v3 in the
  same way.
- **Provenance and composition.** No change. Origins and sensitivity live on the
  `ashlar.Value` tree by document pointer, so an entry keeps its flat path
  (`/timeouts/cpu`, not `/timeouts/overrides/cpu`). Include merging already runs
  on document pointers, and there is no `/overrides` container to merge or
  clear, so it needs no new rules.

## Acceptance criteria

- [x] `SPEC.md` §3.4 gains a paragraph on how `DecodeStrict` and
      `DecodeValueStrict` handle tags: `yaml` first with `json` as fallback,
      `-`, inline struct promotion, unknown-field rejection, and the catch-all
      rules. It lists `DecodeValueStrict`, which SPEC.md doesn't mention today.
      The two doc comments say the same. JSON-contract decoding (§3.2) is
      unchanged.
- [x] Ductile's shape `yaml:",inline,omitempty"` decodes `poll`, `handle`, and
      `cpu` correctly.
- [x] Tag handling, as a regression check. `json:",inline"` works as the
      fallback, a YAML tag takes precedence over a JSON tag, `yaml:"-"`
      excludes the field, and `yaml:"overrides,inline"` is still the catch-all.
      No general validation of tag options is added.
- [x] A named field wins over the catch-all, including a field promoted from an
      inline struct.
- [x] With no extra keys, a nil map stays nil and a map that already has entries
      is unchanged. With extra keys, the map holds exactly those entries.
- [x] A catch-all keyed by a named string type (`type Command string`) decodes
      its entries.
- [x] An omitted `cpu` creates no entry. Both `cpu: null` and `cpu: 0s` create a
      zero-duration entry. `Root().PresenceAt` tells omitted, null, and present
      apart at the flat pointer.
- [x] `cpu: banana` reports an error located at `/timeouts/cpu`.
- [x] A sensitive entry stays redacted in error strings and provenance output.
      This holds under the default key classification and under a
      caller-supplied redactor.
- [x] Each case fails with `ErrValidation`, located at the struct mapping's
      pointer, including on an empty mapping: two inline maps, a non-string key
      kind, an inline pointer-to-map, and an inline map inside an inline
      struct. The same struct at a `null` or omitted position produces no error.
      An unexported inline map leaves the struct closed.
- [x] A struct inside a catch-all entry still rejects unknown keys. A sibling
      struct without a catch-all still rejects them too.
- [x] After a successful decode, `Resolved.Explain` reports the `file:line:col`
      origin of an entry at its flat pointer. One key contains `/` or `~` to
      check RFC 6901 escaping.
- [x] Composition regression check, with no new rules. An included file
      overrides one extra key, and the decoded entry has the winning value and
      the winning origin.
- [x] A struct shaped like Ductile's (`Overrides map[string]time.Duration` tagged
      inline) changes behavior, and the commit message calls this out.
      - `overrides: {cpu: 15s}` used to fill the map through a wrapper key nobody
        declared. Now it fails with a located decode error at `/overrides`.
      - `overrides: 15s` becomes a legitimate entry named `"overrides"`.
- [x] All AGENTS.md §4 gates pass.

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

- 2026-11-01: A Fable review found the card accurate about yaml.v3 and the
  current code, and safe for Ductile. Ductile has no `overrides:` wrapper, and
  `config show --effective` takes its sources from the struct, not from Ashlar
  provenance. It found two places where two engineers would build different
  things. Decisions: (1) destination reuse now matches `decodeMap`, which
  replaces the map, instead of yaml.v3, which adds to it, so Ashlar's decoder
  has one rule for maps; (2) shape errors are located at the struct mapping.
  Also added: a stated tag-parsing rule; a positive test for named string key
  types; a SPEC criterion that can actually be met, since §3.4 had no tag rules
  to amend; a note that yaml.v3 panics where Ashlar returns errors; the
  anonymous-map and inline-pointer edge cases; and a test that null and
  omitted positions produce no error. The include-merge test is trimmed to a
  regression check. (by @claude)
- 2026-11-02: Card 4 moved field indexing to `yamlconfig/fields.go` and added
  `checkKnownKeys`, which rejects unknown keys before decoding writes
  anything. The catch-all rules must apply to both, through the shared
  `indexStructFields`, or the pre-check will reject keys the catch-all
  should accept. The line references to decode.go above are now stale.
  (by @claude)
- 2026-11-02: Implemented on branch card1-inline-map-catch-all. The catch-all
  lives in `structFields` (yamlconfig/fields.go), which both the unknown-key
  pre-check and `decodeStruct` use. Tests in yamlconfig/inline_map_test.go
  failed on master before the change. `decodeStruct` moved to fields.go to
  keep decode.go under 400 lines. (by @claude)
