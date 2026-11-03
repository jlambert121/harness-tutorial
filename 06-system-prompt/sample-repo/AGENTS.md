# textkit

Small string utilities. Standard library only; do not add dependencies.

## Conventions
- Every exported function has a doc comment that starts with its name.
- Tests are table-driven and name their fields `in`, `want`; compare with `got != tt.want`.
- Handle Unicode correctly: operate on runes, not bytes.

## Before finishing
Run `gofmt -l .` (must print nothing) and `go test ./...` (must pass).
