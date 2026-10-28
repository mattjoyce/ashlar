# Follow-up reproduction artifacts

Target: `eec3db8`; comparison revision: `96667bf`. Files are stored as `.go.txt` to keep intentional review failures out of normal project tests. Use a detached checkout of the target revision. All secret markers are synthetic.

- `coordinator_review_test.go.txt`: copy to root as `coordinator_review_test.go`; run `go test -run TestCoordinator -v -count=1 .`. Three tests fail for two JSON compatibility regressions and custom JSON error disclosure. The compatibility tests pass at 96667bf.
- `hickey_followup_probe_test.go.txt`: copy to root with `.txt` removed; run `go test -run TestHickeyFollowup -v -count=1 .`. These tests assert desired non-disclosure/classification behavior and intentionally fail at eec3db8.
- `lamport-probes_test.go.txt`: copy beside tests in yamlconfig as `lamport_review_test.go`; inspect test names and run with their TestLamport prefix. Original probes pass; adjacent ancestor-policy, signed-index, and invalid-enum assertions fail. Consult the pair report for baseline comparison.
- `contract-followup-probes.go.txt`: copy to root as `contract_followup_probe_test.go`; run `go test -run TestContractFollowup -v -count=1 .`. These probes assert observed defects, so PASS confirms the counterexamples.
- `contract-baseline-decode.go.txt`: companion baseline source retained by the contract reviewer; inspect its package and run in a separate scratch location as appropriate.

These probes are review evidence rather than polished permanent regression tests. Review their assertions and error handling before promoting them into the project suite. Required baseline quality gates were run before these scratch files were added and all passed; see ../04-verification.md.
