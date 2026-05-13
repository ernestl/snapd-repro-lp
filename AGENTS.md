# Agent Guidelines

Rules for AI agents generating or modifying code in this repository.

## General

Purpose of the project is written out in README.md and DESIGN.md.

## Go Version

Use the Go version specified in `go.mod`. Do not upgrade or change it without justification.

## Workshop

The project defines a [Workshop](https://github.com/canonical/workshop) environment
(`workshop.yaml`). Use it for a reproducible dev setup with Go and project tools:

```bash
workshop launch
workshop run -- build   # compile
workshop run -- test    # run all tests
workshop run -- lint    # run golangci-lint
```

## Tests

- Every new Go file must have a corresponding `_test.go` file with unit tests.
- Tests must be meaningful: they must actually execute the code under test,
  assert on real outputs or side effects, and cover both happy paths and
  error/edge cases. Trivial tests that only check that a function compiles
  or returns a non-nil value are not acceptable.
- Run tests with:
      go test ./...
  Or via workshop:
      workshop run -- test
- All tests must pass before submitting changes.

## Linter

- Use `golangci-lint` for static analysis.
- Run with:
      golangci-lint run
  Or via workshop:
      workshop run -- lint
- All lint issues must be resolved before submitting changes.

## General Conventions

- Follow standard Go idioms and formatting (`gofmt`).
- Keep dependencies minimal — see `go.mod`. Avoid adding new dependencies
  without justification.
- No agent SDK: LLM interaction uses raw HTTP to OpenRouter (see `llm.go`).
