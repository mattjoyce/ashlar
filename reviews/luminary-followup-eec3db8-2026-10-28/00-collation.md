# Ashlar follow-up review — eec3db8

Reviewed `eec3db85555c93b5ff2424ebd56902af47d24a45` against `96667bf` in a detached worktree. Synced and read cards 00, 1.3, 1.4, 4.1, 4.2, and 4.3 before reviewing. Their latest narrative records fixes for C1–C4/S1–S4 and a rebuilt gosec. Three independent reviewers applied Hickey × Armstrong, Ousterhout × Liskov, and Lamport × Thomas/Hunt; the coordinator read all reports and independently verified gates and JSON compatibility. Question: do the fixes justify closing the recorded review and proceeding to consumer adoption?

## Verdict table

| Pair | Axis | Verdict |
|---|---|---|
| Hickey × Armstrong | Structure × runtime | Original examples fixed; sensitive data still escapes duration, hook, and JSON decoder error boundaries. |
| Ousterhout × Liskov | Surface × substitution | Original examples fixed; new diagnostic precheck rejects previously supported JSON encodings. |
| Lamport × Thomas/Hunt | System × self | Original examples fixed; targeted overrides newly bypass ancestor composition rules. |

## What is verified closed

The committed counterexample suite passes. Independent reruns also pass the original boolean-secret, sensitive-parent report, explicit root replacement, array-element override, escaped pattern, and single-pass interpolation examples. The custom-redactor mutation and descendant-value origin cases pass in the committed suite. These are credible fixes to all eight named examples; they do not establish the broader guarantees under every input.

All required baseline quality gates now pass, including a successful gosec run over 25 files / 5418 lines with zero issues. The previous analyzer/toolchain blocker is closed. See [verification evidence](04-verification.md).

## Shared finding

**The fixes repair the reported examples, but related paths still interpret the same contracts differently.** The new path updater omits ancestor policy; the JSON precheck implements only part of the decoder's accepted language; redaction decisions are partly frozen and partly recomputed. The next pass should consolidate each contract's implementation and test variations across its boundaries rather than add isolated special cases.

## Where the reviews converge

### R1 — 🔴 Must-fix: nested overrides bypass ancestor composition policy

Evidence: [OL4](02-ousterhout-liskov.md), [L1](03-lamport-pragmatic.md). Locus: `yamlconfig/resolve.go:161–176,243–274`.

With `/locked: MergeForbidDuplicate`, explicitly updating `/locked/x` now succeeds. The invariant reviewer ran the same assertion against both revisions: the old code rejected it, the new code admits it. Only the target leaf reaches composeNode; the ancestors are rebuilt without their rules.

Unified action: define targeted-update semantics and enforce declared ancestor restrictions. At minimum restore rejection beneath an existing forbidden subtree. Test fragment/env/explicit paths together. The contract lens flags the semantics as a decision; this collation treats the undocumented loss of an existing fail-closed restriction as blocking until repaired or explicitly redesigned. Parent MergeReplace behavior is also changed, but its intended interaction with a targeted edit is a separate design choice, not an automatic instruction to delete siblings.

### R2 — 🔴 Must-fix: JSON diagnostic precheck rejects supported encodings

Evidence: [OL1](02-ousterhout-liskov.md) and the coordinator's independent [baseline comparison](04-verification.md). Locus: `jsoncontract/decode.go:120–123,147–177,192–212`.

An integer tagged `json:"count,string"` rejects `"42"`; a []byte field rejects base64 `"aGVsbG8="`. Both succeed with encoding/json and with Ashlar at 96667bf, and fail at eec3db8. Field metadata loses tag options, and the precheck assumes every slice requires a JSON array.

Unified action: preserve successful decoding semantics while adding exact diagnostics. Retain necessary tag metadata and supported special encodings, or use a source-aware error adapter that does not impose a second incompatible decoder. Add compatibility cases for quoted scalars and byte slices, including named types.

### R3 — 🔴 Must-fix: custom JSON decoder errors disclose secret input

Evidence: [F3](01-hickey-armstrong.md) and independent coordinator UnmarshalJSON probe. Locus: `jsoncontract/decode.go:49–53,71–89`.

A TextUnmarshaler or UnmarshalJSON implementation returning input in its error causes that secret to appear in DecodeStrict's printable error. The YAML-specific guards do not cover this adapter. This is remaining boundary work, not a regression newly introduced by eec3db8.

Unified action: intercept custom errors where node context is available, or issue a safe value-free diagnostic if classification/context is unavailable. Do not print arbitrary provider error strings as the fallback. Preserve useful safe classifications deliberately.

### R4 — 🟠 Should-fix: signed array indices are admitted

Evidence: [OL2](02-ousterhout-liskov.md), [L2](03-lamport-pragmatic.md). Locus: `yamlconfig/resolve.go:206–235`, also `pointer.go:145–150`.

`/items/+0` and `/items/-0` select element zero. Use a shared strict unsigned decimal index parser before conversion/range checking. Preserve these strings as legal object keys; only array traversal restricts their syntax. The read-side weakness predates the new update path.

## Single-lens signals

### R5 — 🔴 Must-fix: quoted sensitive durations defeat raw substring scrubbing

[F1](01-hickey-armstrong.md), `yamlconfig/decode.go:88–90,342–357`, `redact.go:174–181`. A secret containing a newline is emitted in reversible escaped form by `%q`; the subsequent raw-value scrub cannot find it. Use a safe value-free message before formatting sensitive data. Test quotes, backslashes, control characters, and recoverable encodings, not just raw substring absence.

### R6 — 🔴 Must-fix: include hook errors expose arbitrary provider text

[F2](01-hickey-armstrong.md), `yamlconfig/includes.go:155–175`, `errors.go:134–139`. Both transformer and verifier errors containing synthetic secret markers are printed verbatim. These unchanged boundaries were in the previous foreign-error audit scope. Define safe hook failure context and classification without automatically exposing arbitrary causes, including through Cause/unwrap access if the documented guarantee covers them.

### R7 — 🟠 Should-fix: frozen public classifications are overridden during reporting

[F4](01-hickey-armstrong.md), `yamlconfig/resolved.go:49–69`. A custom policy marks `/token` public and stored Sensitive is false, but reports pass nil to Value formatting and reapply DefaultRedactor, hiding the value. Reporting a Resolved should use its stored classification exclusively, or explicitly document an additional policy layer. The observed consequence is over-redaction, not disclosure.

### R8 — 🟠 Should-fix: safe-suffix exceptions lose to recursive constructor marking

[F5](01-hickey-armstrong.md), `provenance.go:187–190,230–237,253–267`, `value.go:157–161,205–215`. `/credentials/token_file` is redacted despite the advertised safe-suffix exemption. Establish precedence for explicit sensitivity, inheritance, and policy exemptions once; constructors should not overwrite an already computed classification.

### R9 — 🟠 Should-fix: layer stamping leaves key origins inconsistent with its new promise

[OL3](02-ousterhout-liskov.md), `provenance.go:279–305`. Stamping a parsed object as explicit updates child values but retains valid original file KeyOrigin. Decide whether KeyOrigin is lexical or effective-layer origin; update behavior or documentation accordingly, and test unknown-field diagnostics as well as value provenance.

### R10 — 🟠 Should-fix: unsupported merge enums silently replace

[L3](03-lamport-pragmatic.md), `yamlconfig/compose.go:92–115,290–291`. MergeRule(255) is accepted and falls through to replacement. This gap predates the patch. Validate enum values in structural, exact, and pattern rules and return ErrComposition with an omitted result for invalid policy.

## Genuine tensions to resolve

1. **Targeted edits versus whole-subtree composition.** Direct updates naturally preserve siblings; parent replacement naturally discards them. Both operations are useful, but a shared Policy option cannot silently change meaning. Specify their relationship while preserving fail-closed forbiddance.
2. **Conservative redaction versus caller-owned classification.** Conservative defaults are useful for raw Values. A frozen Resolved needs stable, explicit precedence, including decisions that a value is public and documented suffix exemptions. Avoid recomputing policy in formatters or generic constructors.
3. **Lexical source versus effective-layer provenance.** A key can have an original file coordinate and later be supplied through an explicit layer. Name both meanings if both are required; do not promise recursive replacement while retaining one silently.
4. **Precise decode diagnostics versus compatibility.** Better locations should not narrow successful inputs. The public decoder contract, not convenience of the precheck, must govern acceptance.

## Build verdict

**Hold for follow-up fixes.** Credit the completed work: all original examples and all required gates pass. The blocking set is R1, R2, R3, R5, and R6: one newly introduced policy bypass, one demonstrated decoding compatibility regression, and three remaining secret-disclosure boundaries. Resolve the lower-severity contract issues before declaring the public surface stable.

Tests and source inspections are preserved in the pair reports and [reproductions](reproductions/README.md). No production implementation edits, card mutations, commits, or pushes were made in this review. The current cards' closure notes remain historical evidence of the fixes; the broader adoption verdict is not yet closed. This is a focused follow-up, not exhaustive security certification.
