# Lamport × Thomas/Hunt — invariant audit

Target: `96667bf212d5eb11753b44baee8973e9f2f69ebd`. An independent reviewer authored six public-API counterexample tests before reaching a usage limit. The coordinating reviewer executed them, verified their failure output, and completed this report. This is not a fully completed independent third review.

## Verdict

Hold for invariant fixes. Existing examples pass, but configuration policy does not remain stable across normalization, pointer encoding, and interpolation.

## Findings

### LP1 — 🔴 Must-fix: secrets escape two disclosure paths

Loci: `yamlconfig/resolve.go:341–352`; `provenance.go:186–218`.

The boolean-conversion test exposes `private-material` in the strconv cause. The report test for `credentials: {user: private-material}` prints the child value even though the ancestor is sensitive under the default policy.

**Lamport:** non-disclosure must be invariant under failure and tree descent. **Thomas/Hunt:** successful redaction examples do not explain these failing paths. Fix error construction and propagate effective sensitivity through traversal; retain both counterexamples as regression tests.

### LP2 — 🔴 Must-fix: declared replacement is silently rewritten

Locus: `yamlconfig/resolve.go:142–145`.

With root MergeReplace, a default `/from_default` survives a file containing only `/from_file`. The declared transition should replace the root; normalization changes the transition to merge.

**Lamport:** distinct states, absent policy and explicit replacement, share one representation. **Thomas/Hunt:** nil-map heuristics cannot establish caller intent. Fix the representation of absence and test the transition table.

### LP3 — 🟠 Should-fix: array pointers do not survive override construction

Locus: `yamlconfig/resolve.go:167–185`.

Overriding `/items/0` in `items: [old]` fails because a synthetic object is composed over an array.

**Lamport:** location interpretation must be consistent between lookup and update. **Thomas/Hunt:** add traversal tests involving arrays and numeric object keys, then implement updates against the existing tree.

### LP4 — 🔴 Must-fix: escaped pointer patterns bypass a conflict rule

Locus: `yamlconfig/compose.go:100–120`.

The pattern `/a~1b/*` with MergeForbidDuplicate permits two fragments to define `a/b: {x: ...}`. Pattern parts remain escaped, while target pointer tokens are decoded; `a~1b` is compared with `a/b` and silently fails to match.

**Lamport:** equivalent encoded and decoded locations must designate the same policy target. **Thomas/Hunt:** the matcher works for ordinary examples only because both representations happen to coincide there.

Fix: parse literal pattern components using RFC 6901 decoding, reserving wildcard semantics explicitly; reject malformed patterns. Test both `~1` and `~0`, literal asterisks under the chosen grammar, and conflict enforcement.

### LP5 — 🟠 Should-fix: interpolation reinterprets inserted text

Locus: `yamlconfig/resolve.go:270–295`, particularly ReplaceAll at 292.

Template `${A}/${B}`, with A=`${B}` and B=`end`, resolves to `end/end`. A substitution over the original template would yield `${B}/end`. Because the loop repeatedly edits the expanded string, a later substitution consumes content introduced by an earlier one. The behavior depends on which variable expressions appeared in the original template; it is not a coherent recursive-expansion algorithm either.

**Lamport:** a substitution's domain needs to be defined: original source tokens or recursively introduced tokens. **Thomas/Hunt:** repeated global replacement accidentally selects a third behavior.

Fix: choose and document the semantics. Recommended: expand original matches once using a builder or callback, treating environment values as opaque data. If recursive expansion is required, explicitly add cycle/depth handling and provenance for every contributing variable. The observed output is verified; single-pass behavior is a recommended policy, not a previously explicit specification requirement.

## Convergence and tension

The lenses converge on explicit representations and counterexample tests. The tension is how much specification to add before correcting behavior. Use small transition tables for policy/default distinctions and pointer updates, plus a clear interpolation rule; a heavyweight formal-methods project is unnecessary.

## Evidence

All six tests in `reproductions/lamport_review_test.go.txt` fail at the frozen commit, each at the invariant described above. The interpolation test asserts the recommended single-pass semantics; its policy qualification is intentional. No implementation fixes were applied.
