# Hickey × Armstrong — follow-up audit

Reviewed fixed commit `eec3db8` and diff `96667bf..eec3db8` in `/tmp/ashlar-review-eec3db8`. Read the updated cards 1.3, 4.1, 4.2, and 4.3; they record C1–C4/S1–S4 as resolved. This review independently checks closure and adjacent failure paths on the structure × runtime axis. No other new panel reports were read, and no production code was changed.

## Verdict

**Hold for redaction fixes.** The original minimal counterexamples are addressed, and freezing classification into values is the right structural direction. The broader claim that sensitive errors are now contained is not yet true: quoted duration inputs, JSON custom decoder errors, and include hook errors still disclose synthetic secret markers. Negative classification and safe-suffix exemptions also disagree with the new reporting behavior.

## Closure of the original Hickey × Armstrong findings

| Original finding / collated ID | Follow-up result |
|---|---|
| HA1 / C2: sensitive container's ordinary child leaks in provenance reports | Original reproduction closed. New recursive sensitivity propagation protects `/credentials/user` in reports and path explanations. |
| HA2 / C1: sensitive boolean environment coercion leaks `strconv` cause | Original reproduction closed: raw conversion cause is no longer retained. Foreign-error audit scope remains open; see F1–F3. |
| HA3 / S1: mutation of caller RedactorFunc changes an existing Resolved report | Original reproduction closed: Resolved no longer retains the callback. The negative side of the frozen classification is not honored; see F4. |
| HA4 / C3: explicit MergeReplace silently normalized to MergeMap | Original reproduction closed: MergeUnspecified now represents absence separately; explicit MergeReplace survives normalization. |

`go test -run TestLuminaryReviewCounterexamples -count=1 .` passed at the fixed revision. That suite also exercises the reported C4/S2/S3/S4 fixes. This pass is evidence for those specific examples, not exhaustive proof of all variations.

## Remaining and newly exposed findings

### F1 — 🔴 Must-fix: escaped sensitive durations bypass the new scrubber

**Locus:** `yamlconfig/decode.go:88–90,342–357`; `redact.go:174–181`.

**Reproduced:** parse YAML `secret: "marker\nsecret-material"` and decode `secret` into `time.Duration`. The diagnostic is:

```text
a.yaml:1:9 (/secret): ashlar: validation error: invalid duration "marker\nsecret-material"
```

The value contains a real newline. The error message is constructed with `%q`, so it contains the reversible escaped form instead. `NewRedactedError` attempts replacement of the raw scalar and does not find it in the already-quoted message. Removing the foreign `time.ParseDuration` cause fixed one path but retained a library-generated disclosure path. This is an adjacent residual defect, not proof that the original boolean fix failed.

**Hickey:** safe diagnostic text and raw value formatting remain interleaved. A post-format scrubber cannot be the contract boundary for arbitrary representations.

**Armstrong:** malformed secret input still exits through a printable error, with no failure containment.

**Fix:** choose a safe message before quoting or formatting a sensitive value, e.g. a value-free duration diagnostic or an explicit redacted literal. Test newline, quote, backslash, and control-character payloads. Assertions should detect recoverable/escaped content, not just `strings.Contains(err.Error(), rawSecret)`.

### F2 — 🔴 Must-fix: transformer and verifier errors still expose arbitrary hook text

**Locus:** `yamlconfig/includes.go:155–175`; `errors.go:134–139` prints the cause.

**Reproduced using a real file under `t.TempDir()`:** an include Transformer returning `errors.New("decrypt failed: secret-hook-material")` yields:

```text
a.yaml: ashlar: composition error: transform bytes for "a.yaml": decrypt failed: secret-hook-material
```

An IntegrityVerifier returning `errors.New("verify failed: secret-hook-material")` likewise exposes the marker. Both were checked through formatted error output. This boundary was explicitly in the prior C1 audit scope; these paths are unchanged by `eec3db8`.

**Hickey:** a narrow hook signature does not itself define a safe error contract. Arbitrary provider prose is still admitted directly into library diagnostics.

**Armstrong:** decrypt/integrity failure should terminate resolution with safe context. It currently also transports potentially sensitive provider state into logs.

**Fix:** return a generic safe diagnostic identifying the failing hook and source. Establish a structured safe-error boundary for provider classification instead of printing arbitrary causes or relying on whole-input substring replacement. Inspect Error(), formatting, Cause(), and unwrap behavior when defining the guarantee.

### F3 — 🔴 Must-fix: JSON custom decoder failures remain unredacted

**Locus:** `jsoncontract/decode.go:49–53,71–89`.

**Reproduced:** read `{"secret":"json-secret-material"}`, decode into a struct whose `secret` field implements TextUnmarshaler returning `fmt.Errorf("rejected %s", input)`. The error is:

```text
a.json:1:1: ashlar: validation error: rejected json-secret-material
```

The new YAML decoder guard does not apply to this independent JSON decoding path. A custom JSON unmarshaler can take the same generic error route. This is a residual foreign-error boundary, not a new dependency or an argument for coupling JSON to YAML.

**Hickey:** the shared safe-error mechanism should be common while parser/decoder composition remains quarantined. Fixing one adapter does not establish the invariant across the public surface.

**Armstrong:** caller-extensible decoding must fail without disclosing the sensitive input it was given.

**Fix:** intercept custom decode failures at a boundary with enough source/path context to classify the node, or emit a value-free generic diagnostic when that context is unavailable. Do not forward arbitrary custom error text as the fallback. Add JSON TextUnmarshaler/UnmarshalJSON cases, including errors containing only a prefix or encoded form of the input.

### F4 — 🟠 Should-fix: Resolved freezes positive sensitivity but reclassifies negative decisions

**Locus:** `yamlconfig/resolved.go:49–69` passes nil redactors to every formatter; nil means DefaultRedactor rather than “use frozen provenance only.”

**Reproduced:** resolve `token: public-label` with an explicit custom RedactorFunc returning false for all fields. `resolved.Root().At("/token").Provenance().Sensitive` is false, but `resolved.Explain("/token")` prints `[REDACTED]` because the default policy is applied again. No callback mutation is needed.

**Hickey:** classified data and classification mechanism are still mixed at presentation. A frozen false decision is indistinguishable from an unclassified false value in the reporting API.

**Armstrong:** this variation over-redacts rather than leaks, but it violates caller policy and the documented frozen-classification behavior.

**Fix:** give Resolved a reporting path that uses stored classifications exclusively, without implicitly reinvoking DefaultRedactor. Keep default inference for raw Value operations if that remains their documented contract. Verify custom policies that intentionally classify a default-sensitive key as public as well as policies adding new sensitive keys.

### F5 — 🟠 Should-fix: ancestor propagation defeats documented safe-suffix exceptions

**Locus:** `provenance.go:187–190,230–237,253–267`; `value.go:157–161,205–215` recursively marks children sensitive when the container provenance becomes sensitive.

**Reproduced:** `credentials:\n  token_file: public-path\n` passed to `FormatProvenanceReport(nil)` emits `[REDACTED]` for `/credentials/token_file`, despite the declared `_file` exemption. During traversal, the sensitive credentials container is materialized with WithProvenance, which recursively marks descendants. `effectiveNodeSensitivity` then sees the child's flag and returns true before it can consider the safe suffix. FreezeSensitivity similarly rebuilds a sensitive object after evaluating children, and its constructor can override a child's exemption.

**Hickey:** inheritance is implemented both in traversal and generic value constructors; those mechanisms impose incompatible policy precedence.

**Armstrong:** this is conservative disclosure behavior, but it makes documented exceptions unreliable and risks repeated compensating changes elsewhere.

**Fix:** define precedence between explicit sensitivity, inherited sensitivity, and key-policy exemptions in one classifier. Materialize its result without constructors reclassifying the descendants. Test direct reporting, freezing, sanitization, and direct path explanation against the same table of expected classifications.

## Evidence and limits

The shipped counterexample suite passed. A separate scratch file `hickey_followup_probe_test.go` exercised five new public-API probes using `go test -run TestHickeyFollowup -count=1 .`; all five failed their intended assertions, with the hook probe failing separately for transformer and verifier. The scratch file was removed after recording results. Only synthetic markers were used. No production fixes, commits, or kanban edits were made by this reviewer.

The root review coordinator owns full quality-gate verification. This report does not independently certify the rebuilt gosec tool; no security-scan claim is inferred from these targeted tests.

## Convergence

The two lenses converge on one concrete correction: classify once at a clear boundary, store the result as data, and format only safe data afterward. The commit makes meaningful progress on that architecture, but formatting raw values before scrubbing and admitting arbitrary foreign error text still bypass it.

## Genuine tension

Caller-defined redaction can intentionally exempt a field that the default heuristic marks sensitive. Runtime caution favors conservative redaction, while the library's mechanism/policy separation favors honoring the caller's explicit contract. The implementation should not silently choose between them at formatting time. Specify policy precedence when resolving, store that decision, and let reporting faithfully use it. Similarly, safe provider errors should preserve useful classification without treating every provider message as safe to print.
