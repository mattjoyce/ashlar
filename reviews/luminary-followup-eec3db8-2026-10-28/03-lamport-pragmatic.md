# Lamport × Thomas/Hunt — follow-up invariant review

Reviewed frozen commit `eec3db8`, especially changes from `96667bf`, against the synced Ashlar cards and engineering contract. Axis: system × self. No production code changed. Scratch probes are preserved in `reproductions/lamport-probes_test.go.txt`; copy beside the YAML package's tests to reproduce. Tests assert desired behavior, so the four adjacent counterexamples intentionally fail.

## Closure evidence

The committed `TestLuminaryReviewCounterexamples` suite passes. Six independent original probes were rerun against both frozen revisions: all six fail at `96667bf` and pass at `eec3db8`.

| Previous finding | Closure in this review |
|---|---|
| C1 sensitive environment conversion errors | Original boolean secret probe passes; committed decoder probes pass. |
| C2 ancestor sensitivity | Original `/credentials/user` report probe passes. |
| C3 explicit replace policy | Original explicit ObjectRule:MergeReplace probe passes. The new targeted-override regression below still requires attention. |
| C4 array overrides | Original `/items/0` update passes. Index syntax and ancestor policy need additional checks below. |
| S1 frozen redaction | Committed mutable-redactor counterexample passes. |
| S2 recursive layer origins | Committed container-origin counterexample passes. |
| S3 escaped pointer patterns | Independent `/a~1b/*` forbid-duplicate probe passes; malformed-pattern committed test passes. |
| S4 interpolation semantics | Independent `${A}/${B}` with A=`${B}`, B=`end` now yields `${B}/end`. |

These closures establish the named examples, not universal correctness or a full quality-gate verdict.

## Lamport: state transitions must preserve the policy

### L1 — 🔴 must-fix: targeted overrides bypass ancestor merge policy

**Locus:** `yamlconfig/resolve.go:161–176`, `243–274`. `composeNode` runs only when the pointer tokens have been exhausted. Every ancestor is traversed and reconstructed without consulting its rule.

**Reproduced regression:** start with `locked: {x: old}`, declare `/locked: MergeForbidDuplicate`, then explicitly set `/locked/x` to `new`. Resolution succeeds and changes the supposedly protected subtree. The same probe passes at `96667bf` because it rejects the update, and fails at `eec3db8` because the update is admitted.

A second probe declares `/service: MergeReplace`, starts with `{host: old, port: 80}`, then explicitly sets `/service/host`. The old implementation removes `port` under that ancestor replacement rule; the new implementation retains it. Both ancestor-policy probes were run on both revisions, confirming changed behavior rather than inference alone.

**Invariant:** a path update must either preserve the declared composition policy at every affected boundary, or use a separately specified override policy. The current single `Policy` option provides no declaration that ancestor rules stop applying during explicit/environment layering.

**Fix:** define targeted-update semantics explicitly and enforce ancestor restrictions while traversing. At minimum, reject a nested update beneath a forbidden existing subtree. Decide how ancestor replace/append rules interact with targeted updates, preserve the existing contract until that decision is made, and add parity tests for fragment, environment, and explicit layers.

### L2 — 🟠 should-fix: array override accepts signed indices outside the declared pointer grammar

**Locus:** `yamlconfig/resolve.go:206–216`.

**Reproduced:** explicit pointer `/items/+0` successfully updates the first element of `items: [old]`. The code rejects leading zeroes but relies on `strconv.Atoi`, which accepts `+0` and `-0`. Array index tokens must be `0` or a nonzero decimal digit followed by decimal digits; signs are not allowed. This is an adjacent validation defect in the new array-update path, not a claim that all pointer handling was previously correct.

**Fix:** validate the full index grammar before conversion, then range-check. Reuse one internal index parser for lookup and update so those operations cannot diverge. Cover `0`, `1`, `00`, `+0`, `-0`, empty, `-`, overflow, and bounds.

## Thomas/Hunt: fix the model behind the example

### L3 — 🟠 should-fix: policy validation accepts unknown rule values and silently replaces

**Locus:** `yamlconfig/compose.go:92–115`, `290–291`.

**Reproduced:** assigning `MergeRule(255)` to `/x` and composing `x: old` with `x: new` succeeds. Validation checks pointer and pattern spelling but never checks the enum domain. The default switch branch treats an unrecognized rule as replace. This gap predates the patch; the new validation function provides the appropriate place to close it.

**Fix:** validate ObjectRule, ArrayRule, ScalarRule, each exact rule, and each pattern rule against the supported enum values. Return structured ErrComposition for unsupported values and ensure the result remains omitted. Do not make the permissive default branch a recovery policy for malformed caller configuration.

The useful craftsmanship signal is that the direct fixes work, but the array update was tested primarily at the leaf. The replacement of a tree-building algorithm with direct traversal changed the semantics of every ancestor. Regression tests should cover the transition's invariants, not only the example that motivated it.

## Convergence and tension

Both voices converge on separating the specification of path updates from their implementation: the implementation must state which ancestors are affected, which rules apply, and how provenance changes. A small matrix of existing node kind × path token × merge rule × layer would expose L1 and L2 without requiring a formal-methods toolchain.

The genuine tension is between a practical targeted edit (change one array element or object leaf and preserve siblings) and uniform composition semantics (a parent replace/forbid rule governs the entire affected subtree). Both are useful operations. Silently switching between them under the same `Policy` option makes the caller guess. Choose and document whether these are separate operations or how ancestor rules constrain targeted edits; retain fail-closed behavior for forbidden transitions.

## Verdict

**Hold for L1.** The original reported counterexamples are fixed, including the single-pass interpolation choice. The follow-up independently reproduces a new ancestor-policy regression and two bounded validation gaps. Fix and test those transitions before treating the composition/override review as closed.
