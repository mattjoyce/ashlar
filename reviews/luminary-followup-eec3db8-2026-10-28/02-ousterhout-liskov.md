# Ousterhout × Liskov — follow-up contract audit

Reviewed frozen commit `eec3db8` against `96667bf` in `/tmp/ashlar-review-eec3db8`. Lens: surface × substitution, After/code-audit pass. Independently inspected the diff and card closure claims without reading other follow-up reports. No production files changed.

## Closure table

`go test -run 'TestContractFollowup|TestLuminaryReviewCounterexamples' -v .` passes. Follow-up probes assert observed defects; their passing does **not** mean the behavior is correct.

| Prior finding | Assessment |
|---|---|
| C1 sensitive env conversion / YAML custom decoder errors | Original scalar counterexamples pass; unsafe causes removed and sensitive custom-error text suppressed. Not an exhaustive redaction certification. |
| C2 ancestor-sensitive reports | Original counterexample passes. |
| C3 explicit MergeReplace rewritten | Original counterexample closes: explicit and unspecified rules have distinct representations. |
| C4 array pointer overrides | Canonical-index cases close; malformed index grammar remains open (OL2). |
| S1 mutable redactor changes resolved reports | Original counterexample passes; Resolved no longer retains the callback. |
| S2 descendant layer origins absent | Original leaf-value case closes; member-key stamping contradicts the new API comment (OL3). |
| S3 escaped patterns | Escaped-pattern and malformed-pattern counterexamples pass. |
| S4 interpolation reinserts substitutions | Single-pass counterexample passes. |
| Nested JSON error location | Counterexample passes, but the new precheck introduces OL1. |

## Ousterhout: how much behavior must a caller understand?

### OL1 — 🔴 Must fix: diagnostic precheck becomes an incompatible second JSON decoder

**Loci:** `jsoncontract/decode.go:120–123`, `147–177`, `192–212`. Field collection retains a type but drops tag options.

The new precheck rejects valid encodings accepted by both encoding/json and baseline Ashlar:

- `{"port":"8080"}` into an integer field tagged `json:"port,string"` returns ErrValidation instead of port 8080.
- `{"data":"aGVsbG8="}` into a byte-slice field tagged `json:"data"` returns ErrValidation instead of `hello`.

Both failures were reproduced at eec3db8. A baseline probe on 96667bf decoded both successfully. This is a regression, not a request for a new feature.

**Ousterhout:** callers now need to know two subtly different decoding languages. The second implementation relocates complexity into consumers.

**Liskov:** improving error locations strengthened preconditions on previously accepted inputs. A diagnostic enhancement must preserve successful behavior.

**Fix:** retain field metadata including `,string`, support byte-slice semantics, and preserve the standard decoder's conversion contract. Add public regression cases for quoted scalars, base64 byte slices, and named byte-slice types. Prefer a source-aware diagnostic adapter with explicit compatibility cases over an expanding partial decoder clone.

### OL2 — 🟠 Should fix: array traversal accepts noncanonical index tokens

**Locus:** `yamlconfig/resolve.go:206–235`; the same grammar weakness exists in reads at `pointer.go:145–150`.

Both `/items/+0` and `/items/-0` successfully overwrite element zero of `items: [old]`. Checking leading zeros and then calling strconv.Atoi permits signed zero. RFC 6901 array indices require unsigned decimal digits without leading zeros. These remain valid object keys but must not select array elements.

**Fix:** share an index parser between reads and updates that requires `0` or `[1-9][0-9]*`, then checks overflow and bounds. Test signs, leading zeros, overflow, and bounds without changing object-token behavior.

## Liskov: are the new promises kept?

### OL3 — 🟠 Should fix: recursive layer stamping leaves existing key origins unchanged

**Locus:** `provenance.go:279–305`, especially `!m.KeyOrigin.Valid()` at line 302.

StampLayerOrigin promises to apply the origin recursively to nodes **and object member keys**. Parse `port: 80` from a.yaml, then call ExplicitSetting at /service with label cli. The child value becomes explicit:cli, but its member key remains a.yaml:1:1. Reproduced through LookupMember. Unknown-field diagnostics use KeyOrigin, making the distinction observable.

**Fix:** decide whether KeyOrigin means lexical origin or effective-layer origin. For the latter, stamp existing origins too and preserve lexical information separately if needed. For the former, correct the new API comment and specification. Test key and value provenance together.

### OL4 — 🟡 Decision required: targeted updates bypass ancestor merge policy

**Locus:** `yamlconfig/resolve.go:161–176`, `243–264`.

With Rules[/service] = MergeForbidDuplicate, an explicit /service/port override succeeds and changes the port. Only the target leaf reaches composeNode; ancestors are rebuilt without consulting their rules. The old whole-tree composition path consulted ancestor policy. The observed behavior is verified, but whether targeted setters should count as a second ancestor definition needs an explicit contract decision.

**Fix:** document targeted-update versus fragment-composition semantics. If ancestor forbiddance protects a subtree, enforce it during descent. If targeted updates intentionally bypass it, state that distinction in ResolveOptions/CompositionPolicy and test it so consumers do not mistake merge rules for write restrictions.

## Convergence and tension

Both lenses converge on OL1: the decoder should not make consumers discover new exceptions in successful input handling. Existing counterexamples were repaired, but additional logic widened the behavioral surface without compatibility coverage.

The genuine tension is targeted overrides. Ousterhout favors direct path updates that preserve siblings and avoid manufactured overlay trees. Liskov asks whether callers relying on ancestor composition rules retain their guarantees. An explicit path-update contract can satisfy both; silently switching meanings cannot.

Key provenance has a similar tension: preserving literal source coordinates is useful, while layer constructors claim ownership of effective fields. Name both concepts if both matter.

## Verdict and evidence

**Hold for OL1.** Principal prior API counterexamples have credible fixes, but strict decoding now rejects previously supported encodings. Correct array-index grammar and settle key-origin/ancestor-policy semantics before declaring the broader API contract complete. This review did not independently rerun the full merge-grade suite.

Scratch evidence is preserved under `reproductions/contract-followup-probes.go.txt` and `reproductions/contract-baseline-decode.go.txt`. These probes assert observed behavior rather than serving as ready-to-merge regressions.
