# Reproductions

Target commit: `96667bf212d5eb11753b44baee8973e9f2f69ebd`.

These are review artifacts, stored as `.go.txt` so they do not enter the normal test suite. All payloads are synthetic. The detached review worktree is `/tmp/ashlar-review-96667bf` and retains the scratch files for inspection.

To reproduce in a fresh checkout of the target commit:

1. Copy `contract_review_probe_test.go.txt` to the checkout root as `contract_review_probe_test.go`.
2. Copy `lamport_review_test.go.txt` into `yamlconfig/` as `lamport_review_test.go`.
3. Run:

```sh
go test -run 'TestContractReview(PolicySubstitution|ArrayOverride|SecretEnvError|ContainerOrigin)$' -v -count=1 .
go test -run TestLamportReview -v -count=1 ./yamlconfig
```

The four contract probes PASS because they assert the observed defect: policy discrepancy, array error, disclosed marker, and missing child origin. A fifth exploratory nested-location probe is intentionally not selected and is not evidence for a finding.

The six Lamport probes FAIL because they assert the desired invariant. Observed failures:

- Boolean error includes `private-material`.
- Array element override returns array/object conflict.
- Explicit replacement retains the default field.
- Escaped forbid pattern permits the duplicate.
- Sensitive-parent report includes `private-material`.
- Interpolation returns `end/end` rather than the proposed single-pass `${B}/end`.

The final case asserts a recommended semantic choice; the specification does not yet explicitly settle recursive versus single-pass interpolation. Review these scratch probes before converting them into permanent regression tests; they intentionally prioritize small counterexamples over repository test polish.
