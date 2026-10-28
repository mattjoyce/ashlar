# Hickey × Armstrong — foundations code audit

Reviewed fixed commit `96667bf212d5eb11753b44baee8973e9f2f69ebd` in `/tmp/ashlar-review-96667bf`. Axis: structure × runtime. This independent review asks whether immutable representations and policy boundaries remain trustworthy when composition, redaction, and conversion fail. No other panel reports were consulted. No implementation changes were made.

## Verdict

**Hold for fixes.** The separation between quarantined JSON reading and YAML composition is sound, and values generally use private storage and defensive copies. However, reporting and error construction bypass the safety policy; a supposedly immutable resolved result retains executable caller state; default selection rewrites explicit composition policy.

## Findings

### HA1 — 🔴 Must-fix: provenance reporting loses ancestor sensitivity

**Locus:** `provenance.go:186–218`, especially recursive calls at 199 and 212. Compare the explicit `parentSensitive` propagation in `sanitizeTree` at 237 onward.

**Verified reproduction:** parse `credentials:\n  username: hidden-user\n` with `yamlconfig.Read`, then call `FormatProvenanceReport(nil)`. Output is:

```text
/credentials/username: config.yaml:2:13 = "hidden-user"
```

The default policy recognizes `credentials` as sensitive, but the report walks straight through the container and asks only about the leaf. An explicitly sensitivity-marked container is lost the same way. The sanitizer already treats container classification as inherited, so the two public disclosure surfaces disagree.

**Hickey:** sensitivity is a property of the represented subtree, yet three different traversals reconstruct it independently. This braids presentation with classification.

**Armstrong:** a caller using the advertised redacted provenance report can disclose secrets without any error. The failure bypasses containment entirely.

**Fix:** establish one effective-sensitivity traversal, carrying ancestor sensitivity into descendants, and use it for reports, direct resolved-path explanations, and sanitization. Ensure returned `FieldProvenance` histories are safe too. Add regression cases for a sensitive object, sensitive array, and sensitivity set on provenance rather than inferred from a key.

### HA2 — 🔴 Must-fix: sensitive boolean environment conversion exposes raw input

**Locus:** `yamlconfig/resolve.go:341–352`; `errors.go:134–139` appends the underlying cause text.

**Verified reproduction:** resolve YAML `token: false` with binding `/token` → `TOKEN` and lookup returning `secret-boolean-payload`. The resulting diagnostic contains:

```text
env:TOKEN (/token): ashlar: validation error: invalid boolean value in environment variable: strconv.ParseBool: parsing "secret-boolean-payload": invalid syntax
```

`applyEnvLayer` computes sensitivity, but `coerceEnvScalar` attaches the unredacted `strconv` error to `NewError`. The generic error formatter subsequently prints it. The adjacent numeric conversion deliberately drops its unsafe cause, showing inconsistent treatment of the same boundary.

**Hickey:** category, safe human message, and arbitrary underlying error text are separate contracts. Combining them indiscriminately makes a structured error unsafe.

**Armstrong:** malformed input is precisely when diagnostics must be safest. Here failure handling becomes the disclosure path.

**Fix:** construct a safe conversion diagnostic without raw input in its printable cause; preserve classification through safe structured metadata. Apply the same explicit rule to foreign errors at transformer/verifier boundaries (`yamlconfig/includes.go:155–175` currently forwards hook errors verbatim), and custom decoder failures. Test both `Error()` and formatting, plus any exposed cause API, using synthetic secret markers.

### HA3 — 🟠 Should-fix: an immutable resolved value retains mutable redaction behavior

**Locus:** `yamlconfig/resolve.go:134–138`, `yamlconfig/resolved.go:13–17`, and report calls at 49–69.

**Verified reproduction:** resolve `{value: hidden-value}` with a `RedactorFunc` closing over `sensitive := true`. The first report redacts. Change that caller variable to false without resolving again: the same `Resolved` now reports `"hidden-value"`.

```text
before=/value: config.yaml:1:8 = [REDACTED]
after=/value: config.yaml:1:8 = "hidden-value"
```

This is a sequential reproduction, not a hypothetical data race. Concurrent mutation would introduce an additional caller synchronization requirement that the immutable-value contract does not express.

**Hickey:** `Resolved` contains process behavior tied to external identity, not just a historical value. Its output can change while its root and provenance do not.

**Armstrong:** logging a previously safe result later can disclose data if an unrelated owner changes the callback's state.

**Fix:** evaluate caller policy at the resolution boundary and materialize effective sensitivity into immutable per-node provenance, including ancestor inheritance. Resolved reporting should use that stored classification. If dynamic presentation policy is desirable, expose it explicitly as a separate operation rather than embedding it in a value described as immutable.

### HA4 — 🟠 Should-fix: default normalization silently changes explicit merge policy

**Locus:** `yamlconfig/resolve.go:142–145`.

**Verified reproduction:** resolve two fragments `{one: 1}` and `{two: 2}` with `CompositionPolicy{ObjectRule: MergeReplace}`. The result retains both keys. The requested root replacement should discard `one`, but normalization converts it to `MergeMap` when `Rules` is nil. Supplying an empty non-nil `Rules` map changes that behavior despite declaring no additional rule.

**Hickey:** an enum's zero value represents two concerns: a real rule and absence of policy. Inspecting an unrelated map to infer intent makes those concerns inseparable.

**Armstrong:** the system quietly produces a different configuration from the declared policy, rather than rejecting ambiguity or obeying it.

**Fix:** represent absence separately from an explicit policy, or require callers to select `DefaultCompositionPolicy()` and honor every supplied rule verbatim. Test equivalent nil and empty rule maps and parity between direct `ComposeValues` and `ResolveFragments` for explicit rules.

## Evidence and limits

Four public-API scratch tests were run with `go test -run TestHickeyReview -count=1 .` at the frozen revision. All four failed their safety/behavior assertions exactly as described above. Tests covered container redaction, boolean coercion, explicit merge replacement, and callback-dependent reporting. The temporary scratch file was removed afterward to avoid contaminating other reviewers' test runs. These are targeted counterexamples, not a claim of exhaustive security coverage.

## Where the two lenses converge

The strongest common diagnosis is that important policy decisions remain implicit and are recomputed at the wrong boundaries. Sensitivity should become immutable data before disclosure; explicit merge policy should stay explicit. These structural corrections reduce the number of runtime failure paths that need independent defenses.

## Genuine tension

Caller-supplied hooks keep Ashlar uncoupled from applications, which is structurally valuable. Runtime containment requires clear admission boundaries for the behavior and errors those hooks produce. The answer is not to remove extension points or add hidden recovery: accept hooks for resolution, freeze their classified results into the resolved value, and define a safe error contract at each boundary. If callers require dynamically changing redaction, make that a named presentation operation with an explicit lifetime rather than an invisible property of an immutable result.

Armstrong's process-supervision vocabulary is applied here as library failure containment: Ashlar must return a structured failure so its host can decide recovery. Introducing supervisors, retries, or panic recovery inside this library would add unrequested policy.
