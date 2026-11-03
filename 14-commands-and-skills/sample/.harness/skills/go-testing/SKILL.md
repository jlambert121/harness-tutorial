---
name: go-testing
description: Conventions for writing and running Go tests in this repository. Use when adding or changing tests.
---
# Go testing in this repository

## Shape
- One `TestXxx` per exported function; table-driven with a slice of anonymous structs.
- Name the fields `name`, `in`, `want` (add more fields only when needed).
- Run each case with `t.Run(tt.name, ...)` so failures name the case.
- Compare with `got != tt.want` for comparable types; use `reflect.DeepEqual` or `slices.Equal` otherwise.
- Failure messages read `Func(%q) = %q, want %q`.

## Cases to include
- The empty input.
- One ordinary case.
- Unicode: at least one multi-byte string (é, 日本語).
- The boundary the function is most likely to get wrong (off-by-one, trailing space).

## Running
- `go test ./...` must pass and `gofmt -l .` must print nothing before you finish.
- Use `go test -run TestName -v ./...` while iterating.

See `example_test.go.txt` in this skill's directory for a complete example.
