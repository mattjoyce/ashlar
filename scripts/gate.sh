#!/usr/bin/env bash
# Merge-grade gate from AGENTS.md section 4. Exit 0 only when every check passes.
set -u
fail=0
step() { echo "== $1"; shift; if ! "$@"; then echo "FAIL: $*"; fail=1; fi; }
gobin="$(go env GOPATH)/bin"
export PATH="$PATH:$gobin"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then echo "FAIL: gofmt: $unformatted"; fail=1; fi
unimported=$(goimports -l .)
if [ -n "$unimported" ]; then echo "FAIL: goimports: $unimported"; fail=1; fi
step vet go vet ./...
step lint golangci-lint run ./...
step gosec gosec -quiet -severity medium ./...
step test go test -race -count=1 -shuffle=on -vet=all ./...
exit $fail