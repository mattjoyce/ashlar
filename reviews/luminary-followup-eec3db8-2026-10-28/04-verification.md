# Verification evidence

Revision: `eec3db8`, reviewed in `/tmp/ashlar-review-eec3db8`. Prior revision: `96667bf`. Cards 00, 1.3, 1.4, 4.1, 4.2, and 4.3 were synced and read before review. Their latest notes claim C1–C4/S1–S4 were fixed and gosec rebuilt.

## Baseline gates

Run before scratch probes were added:

- `gofmt -l .`: clean.
- `goimports -l .`: clean.
- `go vet ./...`: passed.
- `golangci-lint run ./...`: zero issues.
- `gosec -severity medium ./...`: exit 0; 25 files, 5418 lines, zero issues. The previous toolchain blocker is resolved.
- `go test -race -count=1 -shuffle=on -vet=all ./...`: passed across core, JSON, schema, and YAML packages.

## Coordinator's independent probes

`reproductions/coordinator_review_test.go.txt` contains executable Go tests, stored as text to avoid adding them to the project suite. Copy it to the checkout root as `coordinator_review_test.go` and run:

```sh
go test -run TestCoordinator -count=1 -v .
```

Observed at eec3db8:

1. `json:"count,string"` with `{"count":"42"}`: stdlib accepts 42; DecodeStrict rejects the string as incompatible with int.
2. `[]byte` with `{"data":"aGVsbG8="}`: stdlib accepts `hello`; DecodeStrict rejects the string as incompatible with []uint8.
3. A custom UnmarshalJSON implementation returning an error containing a synthetic password value exposes that value through DecodeStrict's returned error.

The first two probes were also executed against 96667bf and both passed. They are demonstrated regressions introduced by the new precheck, not merely unsupported new feature requests. Loci: `jsoncontract/decode.go:120–126,147–166`; field discovery drops tag options. The JSON custom-error disclosure occurs at `jsoncontract/decode.go:71–89`; it is remaining boundary work, not claimed as newly introduced by this fix.

The compatibility fix should preserve actual encoding/json field semantics while maintaining exact-path diagnostics. The error-boundary fix should not forward arbitrary custom-decoder text into printable diagnostics when safe classification cannot be established.

Scratch tests live only in detached review worktrees. No implementation files, cards, commits, or remote branches were changed by this review.
