# AGENTS.md — Ashlar Engineering Contract

This document governs how code and specifications are written in **Ashlar**. It is binding for human and AI contributors alike.

---

## 1. Where Design Lives, and the Review Lens

This file governs how work is done: coding standards, testing, quality gates, and commit discipline. It does not decide what Ashlar does.

- **Product design lives in `SPEC.md`.** Its §1 invariants and public surface say what Ashlar guarantees. The README's mechanism-versus-policy table says what Ashlar owns and what the consuming application owns. Argue a design question from those and from what consumers need. Change them by an explicit decision recorded in `SPEC.md`, never by citing this file.
- **Review lens: Hickey × Armstrong** (*structure × runtime*). Run design proposals, API boundaries, and code reviews through it as a set of questions, not as rules:
  - **Hickey.** Is this simple rather than merely easy? Does it keep value, state, and identity apart? Does it braid mechanism with a policy the caller should own?
  - **Armstrong.** Does a piece that does not fit fail fast, loudly, and at its source? Can a failure leave a partial result the spec does not allow? Who decides what happens next, and do they get enough information to decide?

---

## 2. Go Coding Standards

### Dependencies
- **Standard library first:** `testing`, `errors`, `fmt.Errorf(... %w ...)`, `io/fs`, `path/filepath`, `context`.
- **Minimal external footprint:** Ashlar is a foundational library imported by applications with strict dependency budgets. Every direct dependency added to `go.mod` requires explicit human approval, a statement of what the standard library lacks, and an accounting of its transitive cost.
- **No application coupling:** Ashlar must never import consumer application packages, database drivers, or specific vault/encryption backends. Secret decryption and file-integrity checks enter through the caller-supplied hooks in `SPEC.md`.

### Package & Code Structure
- Group packages by concern, not by layer. Keep non-public implementation details in `internal/` where appropriate.
- Define interfaces **where they are used** and keep them small.
- **Accept interfaces, return concrete types** from constructors.
- Refactor functions longer than roughly **60 lines** and files longer than roughly **400 lines**.
- Every exported type, function, constant, and method requires a clear doc comment.
- Avoid package stutter, vague names, and reflection tricks that obscure behavior.

### Error Handling & Safety
- Wrap propagated errors with useful context (`fmt.Errorf("...: %w", err)`).
- Use `errors.Is` and `errors.As` with exported sentinel or structured error types; never inspect errors by string matching.
  One narrow exception: a third-party error that exposes no structured position may have its text read to extract a position for display (yaml.v3's "line N:"). The text must never drive classification or control flow, and a failed match must fall back to a coarser location, as `SPEC.md` §1 invariant 5 requires.
- Separate document/syntax errors, structural/schema errors, composition errors, and application validation errors clearly.
- Never `panic` in library code. Never log directly from library code or log-and-return the same error; return structured, redactable errors to the caller.
- Handle every error or explicitly comment why it is safe to ignore.
- Put `context.Context` first for any blocking, I/O-bound, or cancellable operation.

### Style & Naming
- `gofmt` and `goimports` are mandatory.
- `golangci-lint` and `gosec` must pass with zero findings. `#nosec` requires explicit human approval, the rule ID, and a concrete justification.
- Configuration and data keys in examples, schemas, and tests use `snake_case`; environment variables use `UPPER_SNAKE`.

---

## 3. Testing Contract

- **Real components over mocks.** Exercise real files in `t.TempDir()`, real schemas, and real parsers. Each test owns its own temporary directory and environment (`t.Setenv`) so tests are hermetic and safe under `-race -shuffle=on`.
- **Test observable behavior, not internals.** Keep `*_test.go` files beside the code they exercise. Use table-driven tests for multiple meaningful cases.
- **Compatibility corpus:** Ashlar's test suite must cover the real edge cases of its consumers:
  - Presence preservation: omitted keys vs. explicit `false`, `0`, `""`, `[]`, and `null`.
  - Numeric fidelity: preserving exact number lexemes (`1`, `1.0`, `1e0`, large integers) when reading JSON manifests.
  - Strictness: rejecting duplicate keys, unknown fields, trailing documents, and include cycles with source-aware diagnostics.
  - Provenance: verifying that every effective value reports its exact origin (`default`, `file:line:col`, `env`, `explicit`) and override history.

---

## 4. Merge-Grade Quality Gates

Before any code commit or merge request is considered complete, all of the following gates must pass:

```bash
gofmt -l .
goimports -l .
go vet ./...
golangci-lint run ./...
gosec -severity medium ./...
go test -race -count=1 -shuffle=on -vet=all ./...
```

---

## 5. Workflow & Commit Discipline

- **Design before typing:** Agree on the specification and public surface before implementing packages.
- **Scope discipline:** Do not expand scope silently or invent unrequested features.
- **Commits & attribution:** Commit in logical, self-contained chunks with conventional subjects (`docs:`, `feat:`, `fix:`, `test:`, `chore:`). Agents SHALL include the harness, version, and model in PR descriptions and as trailers at the end of every commit message so GitHub renders co-author attribution and `git interpret-trailers` can query provenance:
  ```text
  Agent-Harness: <harness> <version>
  Agent-Model: <model>
  Co-authored-by: <harness>/<version> (<model>) <noreply@provider.invalid>
  ```
  (Use the provider's canonical noreply address when one exists, e.g. `noreply@anthropic.com`, `noreply@openai.com`, or `noreply@google.com`.)
- **Push discipline:** Do not push to a remote without explicit permission in the current session.
