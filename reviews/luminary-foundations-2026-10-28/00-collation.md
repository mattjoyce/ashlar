# Ashlar foundations — Luminaries review, 2026-10-28

Reviewed commit `96667bf212d5eb11753b44baee8973e9f2f69ebd` in a detached worktree. Question: do the foundational APIs uphold Ashlar's structural, provenance, and fail-closed guarantees sufficiently for consumer adoption? The panel used Hickey × Armstrong (structure/runtime), Ousterhout × Liskov (surface/substitution), and Lamport × Thomas/Hunt (invariants/understanding). No production files were edited.

The reviewers worked independently until a usage limit interrupted them. Hickey × Armstrong saved a complete report; Ousterhout × Liskov returned findings and probes; Lamport × Thomas/Hunt left six counterexample tests. The coordinator reran the latter two sets, completed their reports, and read all three before collation. Convergence below refers to independently produced evidence, not three completed independent narrative reviews.

## Verdict table

| Pair | Axis | Verdict |
|---|---|---|
| Hickey × Armstrong | Structure × runtime | Disclosure safety is recomputed inconsistently; mutable policy remains inside an immutable result. |
| Ousterhout × Liskov | Surface × substitution | Policy, pointers, and container provenance do not preserve their contracts across APIs. |
| Lamport × Thomas/Hunt | System × self | Ordinary examples conceal failures involving sensitivity inheritance and encoded locations. |

## Shared finding

**Declared policy and metadata do not consistently survive representation boundaries.** Explicit replacement becomes merge; ancestor sensitivity disappears at leaves; array pointers become object trees; escaped pattern tokens are compared with decoded locations. The architectural separation is promising, but safety depends on preserving these meanings through every transformation.

## Where the pairs converge

### C1 — 🔴 Must-fix: sensitive boolean conversion errors print raw input

Evidence: [HA2](01-hickey-armstrong.md), [OL4](02-ousterhout-liskov.md), [LP1](03-lamport-pragmatic.md). All three independently targeted the path.

Locus: `yamlconfig/resolve.go:341–352`, `errors.go:134–139`. Invalid secret boolean input is included in the wrapped strconv error. Unified fix: define safe diagnostic construction at conversion/foreign-error boundaries; never attach raw-input-bearing printable causes to a supposedly redacted diagnostic. Verify formatted errors and exposed cause behavior with synthetic markers.

### C2 — 🔴 Must-fix: provenance reports disclose children of sensitive containers

Evidence: [HA1](01-hickey-armstrong.md), [LP1](03-lamport-pragmatic.md).

Locus: `provenance.go:186–218`. `credentials: {user: private-material}` is printed unredacted. Unified fix: carry effective ancestor sensitivity through reporting, sanitization, and path explanations using consistent rules. Cover objects, arrays, and explicitly marked provenance; review structured history output too.

### C3 — 🔴 Must-fix: resolution silently changes explicit MergeReplace to MergeMap

Evidence: [HA4](01-hickey-armstrong.md), [OL1](02-ousterhout-liskov.md), [LP2](03-lamport-pragmatic.md).

Locus: `yamlconfig/resolve.go:142–145`. The same policy produces `[b]` through ComposeValues and `[a b]` through ResolveFragments. Unified fix: represent absent policy separately from a valid zero enum. Test identical interpretation across entry points and nil/empty rule maps. Collation raises HA's should-fix severity because silently retaining replaced settings violates declared composition policy.

### C4 — 🟠 Should-fix: overrides cannot address array elements

Evidence: [OL2](02-ousterhout-liskov.md), [LP3](03-lamport-pragmatic.md).

Locus: `yamlconfig/resolve.go:167–185`. `/servers/0/host` becomes an object under `/servers`, causing an array/object conflict. Unified fix: update paths against the existing node kinds, reconstructing immutably; specify missing paths and bounds without treating every numeric token as an array index.

## Genuine cross-pair tensions

### T1 — convenient defaults versus explicit policy

Ousterhout favors a small usable entry point; Liskov and Lamport require identical meaning for explicit inputs. Hickey exposes the conflation of absence with a valid rule. Preserve the convenient default resolver through an explicit absence representation; decide the public API change before implementation.

### T2 — caller-controlled redaction versus stable resolved values

Hickey values application-owned policy and pure results; Armstrong requires disclosure decisions to remain safe later. HA3 demonstrates that a RedactorFunc capturing mutable state changes the same Resolved report from redacted to visible. Decide whether resolution freezes classification or reporting explicitly accepts dynamic policy. Recommendation: freeze sensitivity into resolved data; keep any dynamic presentation operation separate. This is an API contract decision, not a claim that all caller callbacks must be pure.

### T3 — interpolation convenience versus literal environment data

LP5 proves inserted `${B}` can be re-expanded by a later replacement. Decide whether expansion is single-pass or recursively specified. Recommendation: single-pass over original template matches. Recursive semantics would require additional cycle, depth, and provenance rules. The present behavior is verified; single-pass is a recommendation rather than an already explicit contract.

## Single-lens signals

| ID | Severity | Finding | Locus and action |
|---|---|---|---|
| S1 / HA3 | 🟠 | Mutable redactor changes a previously resolved report | `yamlconfig/resolved.go:13–17,49–69`: freeze classification or explicitly define dynamic reporting. |
| S2 / OL3 | 🟠 | Container defaults/explicit settings leave descendant origins unset | `yamlconfig/resolve.go:46–61`: recursively assign layer provenance under a defined history rule. |
| S3 / LP4 | 🔴 | Escaped wildcard pointer pattern bypasses MergeForbidDuplicate | `yamlconfig/compose.go:100–120`: decode literal pattern tokens consistently; test `~1` and `~0`. |
| S4 / LP5 | 🟠 | Interpolation reprocesses introduced environment text | `yamlconfig/resolve.go:270–295`: choose semantics and implement deliberately. |

## Build verdict

**Hold for fixes before consumer adoption.** The first blocking set is C1, C2, C3, and S3: two disclosure defects and two silent policy violations. Address the remaining pointer/provenance defects and resolve the policy decisions before treating the public surface as stable. This is a targeted first pass, not an exhaustive security or compatibility audit.

Baseline checks at the frozen revision, before review probes were added:

- `gofmt -l .` and `goimports -l .`: clean.
- `go vet ./...`: passed.
- `golangci-lint run ./...`: passed, zero issues.
- `go test -race -count=1 -shuffle=on -vet=all ./...`: passed across all four packages.
- `gosec -severity medium ./...`: incomplete; installed analyzer cannot decode Go 1.27.3 export data version 5 (supports at most version 4). Its zero-issue summary is not a successful security gate.

Targeted probes then reproduced the counterexamples. See [reproduction instructions](reproductions/README.md). No production fixes, commits, pushes, or kanban changes were made. The documentation reconciliation pass is deferred until fixes settle the contracts.
