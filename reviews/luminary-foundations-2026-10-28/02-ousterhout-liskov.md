# Ousterhout × Liskov — API contract audit

Target: `96667bf212d5eb11753b44baee8973e9f2f69ebd`. The independent reviewer returned four reproduced findings before reaching a usage limit. The coordinating reviewer reran its four probes and wrote this report from their results. No implementation changes.

## Verdict

Hold for contract fixes. The public abstractions are useful, but equivalent policy inputs do not retain equivalent behavior across entry points, and pointer/provenance promises fail for containers.

## Findings

### OL1 — 🔴 Must-fix: explicit replacement changes meaning across APIs

Locus: `yamlconfig/resolve.go:142–145`.

With `CompositionPolicy{ObjectRule: MergeReplace}`, composing `{a: 1}` then `{b: 2}` directly produces keys `[b]`; resolving the same fragments produces `[a b]`. `normalizePolicy` interprets the valid zero-valued enum as an absent policy. An empty non-nil Rules map also changes the interpretation.

**Ousterhout:** normalization hides a policy decision rather than implementation complexity. **Liskov:** the same policy cannot be substituted across composition and resolution without changing its postcondition.

Fix: represent policy absence separately; preserve explicit rules. Test parity between entry points and nil/empty rule maps. Do not change the enum interpretation without addressing API compatibility.

### OL2 — 🟠 Should-fix: RFC 6901 overrides cannot address array elements

Locus: `yamlconfig/resolve.go:152–185`, especially unconditional ObjectValue construction at 175.

For `servers: [{host: old}]`, ExplicitSetting at `/servers/0/host` returns an array/object composition error instead of updating the selected element. Every pointer segment is turned into an object key, even when the existing node is an array.

**Ousterhout:** callers must understand an undocumented restriction of the synthetic-tree implementation. **Liskov:** an API advertised in terms of RFC 6901 pointers supports fewer locations than Value.At.

Fix: traverse the existing tree and reconstruct the addressed path immutably, handling object keys and array indices according to the actual node kind. Specify missing-parent and out-of-range behavior; numeric object keys must remain valid object keys.

### OL3 — 🟠 Should-fix: container settings leave leaf origins unset

Locus: `yamlconfig/resolve.go:46–61`.

A DefaultSetting at `/service` containing `{port: "80"}` produces `default:app` on `/service` and no origin on `/service/port`. ExplicitSetting has the same shallow provenance assignment. This contradicts the per-effective-value origin contract and leaves leaf reports unable to explain supplied values.

**Ousterhout:** the setting constructor requires callers to stamp every descendant manually. **Liskov:** scalar and container settings do not deliver equivalent provenance guarantees.

Fix: define and implement recursive layer-origin assignment, including key origins and a deliberate rule for existing child history. Test nested objects and arrays for both default and explicit layers.

### OL4 — 🔴 Must-fix: failed boolean coercion discloses sensitive input

Locus: `yamlconfig/resolve.go:341–352`; printable cause handling in `errors.go:134–139`.

Resolve `secret: false` with `/secret` bound to SECRET and value `sensitive-probe`. The returned error includes `strconv.ParseBool: parsing "sensitive-probe"`. Sensitivity computed before coercion is not enforced when creating the error.

**Ousterhout:** error wrapping exposes implementation data that should be hidden at the boundary. **Liskov:** the failure result violates the library-wide non-disclosure contract.

Fix: keep raw conversion input out of printable error messages and causes, while preserving safe classification. Extend tests to malformed sensitive input, not just successfully resolved secrets.

## Convergence and tension

Both lenses ask for APIs whose stated behavior is complete enough that callers need no knowledge of internal normalization or synthetic trees. The genuine tension is a convenient zero-options resolver versus explicit, substitutable composition policy. Retain convenience through an explicit absence/default mechanism, not by guessing intent from unrelated fields.

## Evidence

Saved probe: `reproductions/contract_review_probe_test.go.txt`. Run the four tests selected in the reproduction README. These probes assert the observed defects, so PASS confirms the counterexamples. A fifth exploratory location test is excluded from findings; it was not used as evidence.
