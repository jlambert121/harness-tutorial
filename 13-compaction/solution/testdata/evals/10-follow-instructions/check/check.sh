#!/bin/sh
set -e
test -z "$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
go vet ./...
go test ./... 2>&1
